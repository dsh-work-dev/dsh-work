package pet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type packageFile struct {
	RelativePath string
	AbsolutePath string
	Size         int64
	Data         []byte
}

type packageInventory struct {
	Source        PackageSource
	Root          string
	CanonicalRoot string
	Files         map[string]packageFile
	Ordered       []string
}

func packageIssue(source PackageSource, code string, severity IssueSeverity, retryable bool, args map[string]string) Issue {
	if source.Kind == "" {
		source.Kind = SourceUnknown
	}
	safeArgs := make(map[string]string, len(args)+1)
	for key, value := range args {
		if safe, ok := safeIssueArgument(key, value); ok {
			safeArgs[key] = safe
		}
	}
	if _, ok := safeArgs["folder"]; !ok {
		folder := source.Folder
		if folder == "" {
			folder = filepath.Base(filepath.Clean(source.Root))
		}
		safeArgs["folder"] = sanitizeFolderIdentity(folder)
	}
	return Issue{
		Code:                code,
		Args:                safeArgs,
		SourceKind:          source.Kind,
		Retryable:           retryable,
		Severity:            severity,
		SafeFallbackSummary: defaultIssueSummary(code),
	}
}

func safeIssueArgument(key, value string) (string, bool) {
	if key == "folder" {
		return sanitizeFolderIdentity(value), true
	}
	if key != "resource" && key != "profile" && key != "root" {
		return "", false
	}
	if value == "" || filepath.IsAbs(value) || strings.ContainsAny(value, `/\\`) || strings.Contains(value, "://") || strings.Contains(value, ":") {
		return "", false
	}
	return safeString(value, MaxSafeTextRunes)
}

func packageError(source PackageSource, code string, severity IssueSeverity, retryable bool, args map[string]string) error {
	return &DiagnosticError{Issues: []Issue{packageIssue(source, code, severity, retryable, args)}}
}

func rebindPackageError(source PackageSource, err error) error {
	issues := Diagnostics(err)
	if len(issues) == 0 {
		return err
	}
	rebound := make([]Issue, 0, len(issues))
	for _, issue := range issues {
		code := issue.Code
		if safe, ok := safeString(code, MaxSafeTextRunes); ok && !strings.ContainsAny(safe, `/\\`) && !strings.Contains(safe, "://") {
			code = safe
		} else {
			code = IssuePackageReadFailed
		}
		severity := issue.Severity
		if severity != SeverityInfo && severity != SeverityWarning && severity != SeverityError {
			severity = SeverityError
		}
		bound := packageIssue(source, code, severity, issue.Retryable, issue.Args)
		if safe, ok := safeSummary(issue.SafeFallbackSummary); ok {
			bound.SafeFallbackSummary = safe
		}
		rebound = append(rebound, bound)
	}
	return &DiagnosticError{Issues: boundedIssues(rebound)}
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func normalizePackageSource(source PackageSource) (PackageSource, string, error) {
	if source.Root == "" {
		return PackageSource{}, "", packageError(source, IssuePackageReadFailed, SeverityError, true, nil)
	}

	entryExplicit := source.ManifestPath != "" || source.Entry != ""
	entry := source.ManifestPath
	if entry == "" {
		entry = source.Entry
	}
	if entry == "" {
		if source.Kind == SourceCommunity || strings.EqualFold(filepath.Base(source.Root), "config.jsonc") {
			entry = "config.jsonc"
		} else if source.Kind == SourceDshPets || strings.EqualFold(filepath.Base(source.Root), "dsh-pet.json") {
			entry = "dsh-pet.json"
		} else if source.Kind == SourceCodexAvatars || strings.EqualFold(filepath.Base(source.Root), "avatar.json") {
			entry = "avatar.json"
		} else {
			entry = "pet.json"
		}
	}
	if source.Kind == "" {
		if strings.EqualFold(filepath.Base(entry), "config.jsonc") {
			source.Kind = SourceCommunity
		} else if strings.EqualFold(filepath.Base(entry), "dsh-pet.json") {
			source.Kind = SourceDshPets
		} else if strings.EqualFold(filepath.Base(entry), "avatar.json") {
			source.Kind = SourceCodexAvatars
		} else {
			source.Kind = SourceCodexPets
		}
	}
	if source.Kind != SourceCodexPets && source.Kind != SourceCodexAvatars && source.Kind != SourceDshPets && source.Kind != SourceCommunity {
		return PackageSource{}, "", packageError(source, IssueManifestInvalid, SeverityError, false, nil)
	}

	root, err := filepath.Abs(filepath.Clean(source.Root))
	if err != nil {
		return PackageSource{}, "", packageError(source, IssuePathOutsideRoot, SeverityError, false, nil)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return PackageSource{}, "", packageError(source, IssuePackageReadFailed, SeverityError, true, nil)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return PackageSource{}, "", packageError(source, IssueSymlinkEscape, SeverityError, false, nil)
	}
	if isReparsePoint(root) {
		return PackageSource{}, "", packageError(source, IssueSymlinkEscape, SeverityError, false, nil)
	}
	if !rootInfo.IsDir() {
		// The adapter also accepts an explicit manifest path as a convenience
		// seam. It still canonicalizes the containing directory before reading.
		if rootInfo.Mode().IsRegular() && (strings.EqualFold(filepath.Base(root), "pet.json") || strings.EqualFold(filepath.Base(root), "avatar.json") || strings.EqualFold(filepath.Base(root), "dsh-pet.json") || strings.EqualFold(filepath.Base(root), "config.jsonc")) {
			entry = filepath.Base(root)
			root = filepath.Dir(root)
			rootInfo, err = os.Lstat(root)
			if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || isReparsePoint(root) {
				return PackageSource{}, "", packageError(source, IssuePackageReadFailed, SeverityError, true, nil)
			}
		} else {
			return PackageSource{}, "", packageError(source, IssuePackageReadFailed, SeverityError, true, nil)
		}
	}
	if !entryExplicit && source.Kind == SourceCodexPets {
		if _, dshErr := os.Lstat(filepath.Join(root, "dsh-pet.json")); dshErr == nil {
			entry = "dsh-pet.json"
			source.Kind = SourceDshPets
		}
	}
	if !entryExplicit && source.Kind == SourceCodexPets {
		if _, petErr := os.Lstat(filepath.Join(root, "pet.json")); errors.Is(petErr, os.ErrNotExist) {
			if _, avatarErr := os.Lstat(filepath.Join(root, "avatar.json")); avatarErr == nil {
				entry = "avatar.json"
				source.Kind = SourceCodexAvatars
			}
		}
	}

	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return PackageSource{}, "", packageError(source, IssueSymlinkEscape, SeverityError, false, nil)
	}
	canonicalRoot, err = filepath.Abs(filepath.Clean(canonicalRoot))
	if err != nil {
		return PackageSource{}, "", packageError(source, IssueSymlinkEscape, SeverityError, false, nil)
	}

	relative, pathCode := safeRelativePath(entry)
	if pathCode != "" {
		return PackageSource{}, "", packageError(source, pathCode, SeverityError, false, nil)
	}
	manifestPath := filepath.Join(root, relative)
	if _, err := safeExistingPath(root, canonicalRoot, manifestPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PackageSource{}, "", packageError(source, IssueManifestMissing, SeverityError, true, nil)
		}
		if diagnostic, ok := err.(*DiagnosticError); ok {
			return PackageSource{}, "", rebindPackageError(source, diagnostic)
		}
		return PackageSource{}, "", packageError(source, IssuePackageReadFailed, SeverityError, true, nil)
	}

	if source.Folder == "" {
		source.Folder = filepath.Base(root)
	}
	source.Root = root
	source.ManifestPath = relative
	source.Entry = relative
	source.StableSourceKey = StableSourceKey(source.Kind, source.Folder)
	return source, canonicalRoot, nil
}

func inspectPackage(ctx context.Context, source PackageSource) (packageInventory, error) {
	if err := contextErr(ctx); err != nil {
		return packageInventory{}, err
	}
	normalized, canonicalRoot, err := normalizePackageSource(source)
	if err != nil {
		return packageInventory{}, err
	}
	inventory := packageInventory{
		Source:        normalized,
		Root:          normalized.Root,
		CanonicalRoot: canonicalRoot,
		Files:         make(map[string]packageFile),
	}
	maxBytes, maxFiles := MaxPackageBytes, MaxPackageFiles
	if normalized.Kind == SourceCommunity {
		maxBytes, maxFiles = MaxCommunityBytes, MaxCommunityFiles
	}
	var totalBytes int64
	walkErr := filepath.WalkDir(inventory.Root, func(path string, entry os.DirEntry, walkErr error) error {
		if err := contextErr(ctx); err != nil {
			return err
		}
		if walkErr != nil {
			return packageError(normalized, IssuePackageReadFailed, SeverityError, true, nil)
		}
		if path == inventory.Root {
			return nil
		}
		relative, err := filepath.Rel(inventory.Root, path)
		if err != nil {
			return packageError(normalized, IssuePathOutsideRoot, SeverityError, false, nil)
		}
		relative, pathCode := safeRelativePath(relative)
		if pathCode != "" {
			return packageError(normalized, pathCode, SeverityError, false, nil)
		}
		if entry.IsDir() {
			if entry.Type()&os.ModeSymlink != 0 || isReparsePoint(path) {
				return packageError(normalized, IssueSymlinkEscape, SeverityError, false, nil)
			}
			if _, err := safeExistingPath(inventory.Root, inventory.CanonicalRoot, path); err != nil {
				if diagnostic, ok := err.(*DiagnosticError); ok {
					return rebindPackageError(normalized, diagnostic)
				}
				return packageError(normalized, IssuePackageReadFailed, SeverityError, true, nil)
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || isReparsePoint(path) {
			return packageError(normalized, IssueSymlinkEscape, SeverityError, false, nil)
		}
		info, err := entry.Info()
		if err != nil {
			return packageError(normalized, IssuePackageReadFailed, SeverityError, true, nil)
		}
		if !info.Mode().IsRegular() {
			return packageError(normalized, IssueUnsafeResource, SeverityError, false, map[string]string{"resource": "non-regular"})
		}
		if _, err := safeExistingPath(inventory.Root, inventory.CanonicalRoot, path); err != nil {
			if diagnostic, ok := err.(*DiagnosticError); ok {
				return rebindPackageError(normalized, diagnostic)
			}
			return packageError(normalized, IssuePackageReadFailed, SeverityError, true, nil)
		}
		if len(inventory.Files) >= maxFiles {
			return packageError(normalized, IssueFileCountExceeded, SeverityError, false, nil)
		}
		if info.Size() < 0 || info.Size() > MaxPackageBytes {
			return packageError(normalized, IssuePackageTooLarge, SeverityError, false, nil)
		}
		if relative == normalized.ManifestPath && info.Size() > MaxManifestBytes {
			return packageError(normalized, IssueManifestTooLarge, SeverityError, false, nil)
		}
		if totalBytes > maxBytes-info.Size() {
			return packageError(normalized, IssuePackageTooLarge, SeverityError, false, nil)
		}
		if unsafeFileName(relative) {
			return packageError(normalized, IssueUnsafeResource, SeverityError, false, map[string]string{"resource": "executable"})
		}
		totalBytes += info.Size()
		inventory.Files[relative] = packageFile{
			RelativePath: relative,
			AbsolutePath: path,
			Size:         info.Size(),
		}
		inventory.Ordered = append(inventory.Ordered, relative)
		return nil
	})
	if walkErr != nil {
		if errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded) {
			return packageInventory{}, walkErr
		}
		if diagnostic, ok := walkErr.(*DiagnosticError); ok {
			return packageInventory{}, diagnostic
		}
		return packageInventory{}, packageError(normalized, IssuePackageReadFailed, SeverityError, true, nil)
	}
	if len(inventory.Files) == 0 {
		return packageInventory{}, packageError(normalized, IssueManifestMissing, SeverityError, true, nil)
	}
	for index, relative := range inventory.Ordered {
		if err := contextErr(ctx); err != nil {
			return packageInventory{}, err
		}
		file := inventory.Files[relative]
		data, err := readFileExactly(file.AbsolutePath, file.Size, MaxPackageBytes)
		if err != nil {
			if errors.Is(err, errFileGrew) {
				return packageInventory{}, packageError(normalized, IssuePackageTooLarge, SeverityError, false, nil)
			}
			if errors.Is(err, errUnsafeFile) {
				return packageInventory{}, packageError(normalized, IssueSymlinkEscape, SeverityError, false, nil)
			}
			return packageInventory{}, packageError(normalized, IssuePackageReadFailed, SeverityError, true, nil)
		}
		if isUnsafeResourceContent(relative, data) {
			return packageInventory{}, packageError(normalized, IssueUnsafeResource, SeverityError, false, map[string]string{"resource": "executable"})
		}
		file.Data = data
		inventory.Files[relative] = file
		inventory.Ordered[index] = relative
	}
	return inventory, nil
}

var errFileGrew = errors.New("package file grew while reading")
var errUnsafeFile = errors.New("package file became unsafe while reading")
var errFileChanged = errors.New("package file changed while reading")

func readFileExactly(path string, expected, limit int64) ([]byte, error) {
	before, err := regularPackageFile(path)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) {
		return nil, errFileChanged
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit || int64(len(data)) != expected {
		return nil, errFileGrew
	}
	after, err := regularPackageFile(path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) {
		return nil, errFileChanged
	}
	return data, nil
}

func regularPackageFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || isReparsePoint(path) || !info.Mode().IsRegular() {
		return nil, errUnsafeFile
	}
	return info, nil
}

func safeRelativePath(raw string) (string, string) {
	if raw == "" || !utf8.ValidString(raw) || strings.IndexByte(raw, 0) >= 0 {
		return "", IssuePathOutsideRoot
	}
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "\\") || filepath.IsAbs(raw) {
		return "", IssuePathOutsideRoot
	}
	if strings.Contains(raw, "://") || strings.HasPrefix(strings.ToLower(raw), "data:") || strings.HasPrefix(strings.ToLower(raw), "file:") {
		return "", IssueRemoteResource
	}
	// Treat both slash conventions as separators even on Unix. A package
	// created on one platform must not become an escape when read on another.
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '\\' })
	for _, part := range parts {
		if part == ".." {
			return "", IssuePathOutsideRoot
		}
	}
	if strings.Contains(raw, ":") {
		return "", IssuePathOutsideRoot
	}
	normalized := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(raw, "\\", "/")))
	if normalized == "." || normalized == "" || normalized == ".." || strings.HasPrefix(normalized, ".."+string(filepath.Separator)) {
		return "", IssuePathOutsideRoot
	}
	return normalized, ""
}

func safeExistingPath(root, canonicalRoot, candidate string) (string, error) {
	absCandidate, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil || !pathWithin(root, absCandidate) {
		return "", &DiagnosticError{Issues: []Issue{{Code: IssuePathOutsideRoot, SourceKind: SourceUnknown, Severity: SeverityError}}}
	}
	info, err := os.Lstat(absCandidate)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || isReparsePoint(absCandidate) {
		return "", &DiagnosticError{Issues: []Issue{{Code: IssueSymlinkEscape, SourceKind: SourceUnknown, Severity: SeverityError}}}
	}
	resolved, err := filepath.EvalSymlinks(absCandidate)
	if err != nil {
		return "", &DiagnosticError{Issues: []Issue{{Code: IssueSymlinkEscape, SourceKind: SourceUnknown, Severity: SeverityError}}}
	}
	resolved, err = filepath.Abs(filepath.Clean(resolved))
	if err != nil || !pathWithin(canonicalRoot, resolved) {
		return "", &DiagnosticError{Issues: []Issue{{Code: IssueSymlinkEscape, SourceKind: SourceUnknown, Severity: SeverityError}}}
	}
	return resolved, nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func unsafeFileName(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".exe", ".dll", ".sys", ".com", ".scr", ".msi", ".app", ".appimage", ".bin", ".so", ".dylib", ".wasm", ".plugin", ".lnk", ".url", ".desktop", ".command", ".vbs", ".ps1", ".psm1", ".bat", ".cmd", ".sh", ".bash", ".zsh", ".fish", ".py", ".pyc", ".rb", ".lua", ".pl", ".php", ".jar", ".class", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".html", ".htm", ".xhtml", ".svg", ".css", ".zip", ".tar", ".gz", ".bz2", ".xz", ".zst", ".7z", ".rar", ".iso", ".dmg", ".deb", ".rpm", ".msix", ".apk":
		return true
	default:
		return false
	}
}

func isUnsafeResourceContent(path string, data []byte) bool {
	if unsafeFileName(path) {
		return true
	}
	if strings.EqualFold(filepath.Base(path), "pet.json") || strings.EqualFold(filepath.Base(path), "avatar.json") || strings.EqualFold(filepath.Base(path), "dsh-pet.json") {
		return false
	}
	prefix := bytes.TrimSpace(data)
	if len(prefix) > 4096 {
		prefix = prefix[:4096]
	}
	if bytes.HasPrefix(prefix, []byte("MZ")) || bytes.HasPrefix(prefix, []byte("#!")) || bytes.HasPrefix(prefix, []byte("\x7fELF")) {
		return true
	}
	if len(prefix) >= 4 {
		magic := string(prefix[:4])
		if magic == "\xfe\xed\xfa\xce" || magic == "\xce\xfa\xed\xfe" || magic == "\xfe\xed\xfa\xcf" || magic == "\xcf\xfa\xed\xfe" {
			return true
		}
	}
	lower := strings.ToLower(string(prefix))
	return strings.Contains(lower, "<script") || strings.Contains(lower, "<!doctype html") || strings.Contains(lower, "<html")
}

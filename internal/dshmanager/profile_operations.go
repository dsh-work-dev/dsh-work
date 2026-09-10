package dshmanager

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/local/dsh-work/internal/lifecycle"
)

// CloneProfile creates a sibling profile with a deterministic, unique name.
// User-authored manifests and configuration are copied, while dependency
// trees and DSH-generated files are deliberately left for DSH to rebuild when
// the clone is first launched.
func (m *Manager) CloneProfile(ctx context.Context, request ProfileCloneRequest) (ProfileCloneResult, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return ProfileCloneResult{}, err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return ProfileCloneResult{}, err
	}
	if err := m.ensureMutationAllowed(); err != nil {
		return ProfileCloneResult{}, err
	}
	if err := validateProfileRef(request.Profile); err != nil {
		return ProfileCloneResult{}, err
	}

	m.mu.RLock()
	config := m.configSnapshotLocked()
	m.mu.RUnlock()
	dataDirectory, ok := findDataDirectory(config.DataDirectories, request.Profile.DataDirectoryID)
	if !ok {
		return ProfileCloneResult{}, failure(lifecycle.ErrorProfileNotFound, "DSH data directory was not found", "the profile data directory is not in the catalog")
	}
	if dataDirectory.Ownership == DataDirectoryOwnershipUser && !directoryExists(dataDirectory.Path) {
		return ProfileCloneResult{}, failure(lifecycle.ErrorProfileNotFound, "The selected user DSH data directory is unavailable", "register an existing DSH data directory")
	}
	profileRoot := filepath.Join(dataDirectory.Path, "profiles")
	source := filepath.Join(profileRoot, request.Profile.Name)
	info, err := os.Lstat(source)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return ProfileCloneResult{}, failure(lifecycle.ErrorProfileInvalid, "the DSH profile path is not a directory", "choose an existing custom profile")
	}
	if err != nil || !info.IsDir() {
		if errors.Is(err, os.ErrNotExist) && isBuiltInProfile(config.ProfileCatalog, request.Profile.Name) {
			if err := m.materializeBuiltInProfile(ctx, config, dataDirectory, request.Profile.Name); err != nil {
				return ProfileCloneResult{}, err
			}
			info, err = os.Lstat(source)
		}
	}
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return ProfileCloneResult{}, failure(lifecycle.ErrorProfileInvalid, "the DSH profile path is not a directory", "choose an existing custom profile")
	}
	if err != nil || !info.IsDir() {
		return ProfileCloneResult{}, failure(lifecycle.ErrorProfileNotFound, "DSH profile was not found", "choose an existing custom profile")
	}
	cloneName := nextProfileName(config.ProfileCatalog, dataDirectory, request.Profile.Name)
	if cloneName == "" {
		return ProfileCloneResult{}, failure(lifecycle.ErrorProfileCloneFailed, "the profile could not be cloned", "no available profile name was found")
	}
	destination := filepath.Join(profileRoot, cloneName)
	if err := copyProfileTree(ctx, source, destination); err != nil {
		cleanupErr := os.RemoveAll(destination)
		if cancellation := contextError(ctx); cancellation != nil {
			if cleanupErr != nil {
				return ProfileCloneResult{}, failureWithMeta(lifecycle.ErrorProfileCloneFailed, "the profile clone was cancelled and its partial directory could not be removed", "the clone directory may need manual cleanup", true, true)
			}
			return ProfileCloneResult{}, cancellation
		}
		if cleanupErr != nil {
			return ProfileCloneResult{}, failureWithMeta(lifecycle.ErrorProfileCloneFailed, "the profile could not be cloned or cleaned up", "the clone directory may need manual cleanup", true, true)
		}
		return ProfileCloneResult{}, failureWithMeta(lifecycle.ErrorProfileCloneFailed, "the profile could not be cloned", "the original profile was not changed", true, false)
	}
	snapshot, err := m.Snapshot(context.Background())
	if err != nil {
		return ProfileCloneResult{}, err
	}
	return ProfileCloneResult{
		Profile:  ProfileRef{DataDirectoryID: request.Profile.DataDirectoryID, Name: cloneName},
		Snapshot: snapshot,
	}, nil
}

// DeleteProfile removes one custom profile directory. Built-in profiles and
// any profile that participates in the configured/current/known-good context
// or is the retained failed-switch target are protected so a destructive
// operation cannot invalidate a live or recoverable Run context.
func (m *Manager) DeleteProfile(ctx context.Context, request ProfileDeleteRequest) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	if err := m.ensureMutationAllowed(); err != nil {
		return Snapshot{}, err
	}
	if err := validateProfileRef(request.Profile); err != nil {
		return Snapshot{}, err
	}

	m.mu.RLock()
	config := m.configSnapshotLocked()
	current := cloneRunContext(m.current)
	configured := cloneRunContext(m.configured)
	knownGood := cloneRunContext(m.knownGood)
	lastSwitchAttempt := cloneSwitchAttempt(m.lastSwitchAttempt)
	m.mu.RUnlock()
	dataDirectory, ok := findDataDirectory(config.DataDirectories, request.Profile.DataDirectoryID)
	if !ok {
		return Snapshot{}, failure(lifecycle.ErrorProfileNotFound, "DSH data directory was not found", "the profile data directory is not in the catalog")
	}
	if dataDirectory.Ownership == DataDirectoryOwnershipUser && !directoryExists(dataDirectory.Path) {
		return Snapshot{}, failure(lifecycle.ErrorProfileNotFound, "The selected user DSH data directory is unavailable", "register an existing DSH data directory")
	}
	if isBuiltInProfile(config.ProfileCatalog, request.Profile.Name) {
		return Snapshot{}, failure(lifecycle.ErrorProfileInvalid, "built-in DSH profiles cannot be deleted", "choose a custom profile")
	}
	m.mu.RLock()
	protected := m.safeMode != nil && m.safeMode.ReturnTo.Profile == request.Profile
	m.mu.RUnlock()
	if protected {
		return Snapshot{}, failure(lifecycle.ErrorProfileInUse, "profile is retained for leaving safe mode", "return to the previous environment before deleting it")
	}
	failedTarget := lastSwitchAttempt != nil && lastSwitchAttempt.Target.Profile == request.Profile
	if profileRefMatches(current, request.Profile) || profileRefMatches(configured, request.Profile) || profileRefMatches(knownGood, request.Profile) || failedTarget {
		return Snapshot{}, failure(lifecycle.ErrorProfileInUse, "the selected DSH profile is in use", "switch to another profile before deleting it")
	}

	profilePath := filepath.Join(dataDirectory.Path, "profiles", request.Profile.Name)
	info, err := os.Lstat(profilePath)
	if err != nil || !info.IsDir() {
		return Snapshot{}, failure(lifecycle.ErrorProfileNotFound, "DSH profile was not found", "choose an existing custom profile")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Snapshot{}, failure(lifecycle.ErrorProfileInvalid, "the DSH profile path is not a directory", "choose an existing custom profile")
	}
	if _, err := m.prepareProfileBackups(ctx, request.Profile); err != nil {
		return Snapshot{}, recoveryFailure(err, lifecycle.ErrorProfileDeleteFailed, "Profile backups could not be prepared for deletion.")
	}
	if err := os.RemoveAll(profilePath); err != nil {
		return Snapshot{}, failureWithMeta(lifecycle.ErrorProfileDeleteFailed, "the DSH profile could not be deleted", "the profile may be only partially removed", true, true)
	}
	return m.Snapshot(context.Background())
}

// BackupProfile writes a compressed profile backup inside its owner's hidden
// backup directory. Dependency trees and other generated directories are
// excluded; manifests, lockfiles, patches and user-authored config files are
// retained so a future restore can ask DSH to reconcile dependencies again.
func (m *Manager) BackupProfile(ctx context.Context, request ProfileBackupRequest) (ProfileBackupResult, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return ProfileBackupResult{}, err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return ProfileBackupResult{}, err
	}
	if err := m.ensureMutationAllowed(); err != nil {
		return ProfileBackupResult{}, err
	}
	if err := validateProfileRef(request.Profile); err != nil {
		return ProfileBackupResult{}, err
	}

	m.mu.RLock()
	config := m.configSnapshotLocked()
	m.mu.RUnlock()
	dataDirectory, ok := findDataDirectory(config.DataDirectories, request.Profile.DataDirectoryID)
	if !ok {
		return ProfileBackupResult{}, failure(lifecycle.ErrorProfileNotFound, "DSH data directory was not found", "the profile data directory is not in the catalog")
	}
	if dataDirectory.Ownership == DataDirectoryOwnershipUser && !directoryExists(dataDirectory.Path) {
		return ProfileBackupResult{}, failure(lifecycle.ErrorProfileNotFound, "The selected user DSH data directory is unavailable", "register an existing DSH data directory")
	}
	profilePath := filepath.Join(dataDirectory.Path, "profiles", request.Profile.Name)
	info, err := os.Lstat(profilePath)
	if err != nil || !info.IsDir() {
		return ProfileBackupResult{}, failure(lifecycle.ErrorProfileNotFound, "DSH profile was not found", "choose an existing custom profile")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ProfileBackupResult{}, failure(lifecycle.ErrorProfileInvalid, "the DSH profile path is not a directory", "choose an existing custom profile")
	}
	backupDirectory, err := m.prepareProfileBackups(ctx, request.Profile)
	if err != nil {
		return ProfileBackupResult{}, profileBackupFailure(err)
	}
	if err := validateBackupProfile(profilePath); err != nil {
		return ProfileBackupResult{}, profileBackupFailure(err)
	}
	if err := os.MkdirAll(backupDirectory, 0o700); err != nil {
		return ProfileBackupResult{}, profileBackupFailure(err)
	}
	createdAt := time.Now().UTC()
	base := safeBackupPart(request.Profile.Name)
	if base == "" {
		base = "profile"
	}
	filename := base + "-" + createdAt.Format("20060102-150405") + ".zip"
	destination := filepath.Join(backupDirectory, filename)
	for index := 2; ; index++ {
		_, statErr := os.Stat(destination)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if statErr == nil {
			filename = base + "-" + createdAt.Format("20060102-150405") + "-" + strconv.Itoa(index) + ".zip"
			destination = filepath.Join(backupDirectory, filename)
			continue
		}
		return ProfileBackupResult{}, profileBackupFailure(statErr)
	}
	if err := writeProfileArchive(ctx, request.Profile, profilePath, backupDirectory, destination, createdAt); err != nil {
		if cancellation := contextError(ctx); cancellation != nil {
			return ProfileBackupResult{}, cancellation
		}
		return ProfileBackupResult{}, profileBackupFailure(err)
	}
	stat, err := os.Stat(destination)
	if err != nil {
		return ProfileBackupResult{}, profileBackupFailure(err)
	}
	return ProfileBackupResult{Profile: request.Profile, FileName: filename, Size: stat.Size(), CreatedAt: createdAt.Format(time.RFC3339)}, nil
}

func nextProfileName(catalog ProfileCatalog, dataDirectory DataDirectoryInfo, base string) string {
	for index := 1; index <= 9999; index++ {
		candidate := base + " " + strconv.Itoa(index)
		if !validProfileName(candidate) || profileExists(catalog, dataDirectory, candidate) {
			continue
		}
		return candidate
	}
	return ""
}

func copyProfileTree(ctx context.Context, source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := contextError(ctx); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(destination, 0o700)
		}
		if entry.IsDir() && profileGeneratedDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if !entry.IsDir() && profileGeneratedFile(entry.Name()) {
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("profile contains a symbolic link")
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !entry.Type().IsRegular() {
			return errors.New("profile contains an unsupported file")
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeOutputErr := output.Close()
		closeInputErr := input.Close()
		return errors.Join(copyErr, closeOutputErr, closeInputErr)
	})
}

func addProfileBackupFiles(ctx context.Context, archive *zip.Writer, profilePath string) error {
	return filepath.WalkDir(profilePath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := contextError(ctx); err != nil {
			return err
		}
		relative, err := filepath.Rel(profilePath, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			if backupExcludedDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if profileGeneratedFile(entry.Name()) {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("profile contains a symbolic link")
		}
		if !entry.Type().IsRegular() {
			return errors.New("profile contains an unsupported file")
		}
		return writeBackupEntry(archive, filepath.ToSlash(relative), func(writer io.Writer) error {
			input, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(writer, input)
			closeErr := input.Close()
			return errors.Join(copyErr, closeErr)
		})
	})
}

func backupExcludedDirectory(name string) bool {
	return profileGeneratedDirectory(name)
}

func profileGeneratedDirectory(name string) bool {
	switch strings.ToLower(name) {
	case profileBackupFolder, "node_modules", ".cache", ".pnpm", ".dsh-module-fallback", "dist", "build", "tmp", "temp", "logs", ".logs", ".git":
		return true
	default:
		return false
	}
}

func profileGeneratedFile(name string) bool {
	return strings.EqualFold(name, "cordis.yml")
}

func profileRefMatches(context *RunContext, ref ProfileRef) bool {
	return context != nil && context.Profile == ref
}

func writeBackupEntry(archive *zip.Writer, name string, write func(io.Writer) error) error {
	header := &zip.FileHeader{Name: filepath.ToSlash(name), Method: zip.Deflate}
	header.SetMode(0o600)
	writer, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}
	return write(writer)
}

func safeBackupPart(value string) string {
	var builder strings.Builder
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '-' || character == '_' || character == '.' {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('_')
		}
		if builder.Len() >= 80 {
			break
		}
	}
	return strings.Trim(builder.String(), " .")
}

func profileBackupFailure(_ error) error {
	return failureWithMeta(lifecycle.ErrorProfileBackupFailed, "the profile backup could not be created", "the profile was not changed", true, false)
}

// ExportProfile writes a portable archive selected by the native save dialog.
// It does not create a local backup or include the owner's backup history.
func (m *Manager) ExportProfile(ctx context.Context, ref ProfileRef, destination string) (resultErr error) {
	defer func() {
		resultErr = recoveryFailure(resultErr, lifecycle.ErrorProfileBackupFailed, "The profile could not be exported.")
	}()
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := m.ensureMutationAllowed(); err != nil {
		return err
	}
	if err := validateProfileRef(ref); err != nil {
		return err
	}
	directory, err := m.backupDataDirectory(ref.DataDirectoryID)
	if err != nil {
		return err
	}
	source := filepath.Join(directory.Path, "profiles", ref.Name)
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("profile must be a directory")
	}
	if err := validateBackupProfile(source); err != nil {
		return err
	}
	if !filepath.IsAbs(destination) {
		return errors.New("export destination must be absolute")
	}
	// Resolve existing parents to reject a linked path into the source tree.
	resolvedSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(resolvedSource, filepath.Join(parent, filepath.Base(destination)))
	if err == nil && filepath.IsLocal(relative) {
		return errors.New("choose an export location outside the profile directory")
	}
	if info, err := os.Lstat(destination); err == nil && !info.Mode().IsRegular() {
		return errors.New("export destination must be a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeProfileArchive(ctx, ref, source, parent, destination, time.Now().UTC())
}

func writeProfileArchive(ctx context.Context, ref ProfileRef, profilePath, backupDirectory, destination string, createdAt time.Time) error {
	temporary, err := os.CreateTemp(backupDirectory, ".profile-backup-*.part")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keepTemporary := false
	defer func() {
		if !keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	archive := zip.NewWriter(temporary)
	manifest := struct {
		Profile   ProfileRef `json:"profile"`
		CreatedAt string     `json:"createdAt"`
		Format    string     `json:"format"`
	}{Profile: ref, CreatedAt: createdAt.Format(time.RFC3339), Format: "dsh-work-profile-v1"}
	if err := writeBackupEntry(archive, "_dsh-work/manifest.json", func(writer io.Writer) error {
		return json.NewEncoder(writer).Encode(manifest)
	}); err != nil {
		_ = archive.Close()
		_ = temporary.Close()
		return err
	}
	if err := addProfileBackupFiles(ctx, archive, profilePath); err != nil {
		_ = archive.Close()
		_ = temporary.Close()
		if cancellation := contextError(ctx); cancellation != nil {
			return cancellation
		}
		return err
	}
	if err := archive.Close(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	keepTemporary = true
	return nil
}

package pet

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// CatalogConfig supplies the narrow environment seams used by PetCatalog.
// CodexHome, HomeDir, and Env are test seams; production callers normally
// leave them empty so the process environment and user home are used.
type CatalogConfig struct {
	CommunityHome string
	CodexHome     string
	CacheRoot     string
	Adapter       PetAdapter
	HomeDir       func() (string, error)
	Env           func(string) string
}

// PetCatalog is the Host-facing P1 catalog boundary. It exposes snapshots and
// normalized definitions, never a package path or an unverified resource.
type PetCatalog interface {
	Snapshot(context.Context) (CatalogSnapshot, error)
	Refresh(context.Context) (CatalogSnapshot, error)
	Inspect(context.Context, PackageSource) (Inspection, error)
	Resolve(context.Context, string) (PetDefinition, error)
}

// RevalidatingCatalog is the optional commit-time source check used by a
// selection transaction. Resolve intentionally serves the immutable accepted
// cache so ordinary reads remain stable after a user package changes; a
// commit explicitly opts into re-reading the source through this seam.
type RevalidatingCatalog interface {
	PetCatalog
	Revalidate(context.Context, string) (PetDefinition, error)
}

// Catalog owns immutable snapshot publication and materialized package data.
// Settings and overlay integration remain outside this package.
type Catalog struct {
	mu        sync.RWMutex
	config    CatalogConfig
	adapter   PetAdapter
	cacheRoot string
	snapshot  CatalogSnapshot
	entries   map[string]catalogEntry
	refresh   *refreshOperation
}

type catalogEntry struct {
	definition PetDefinition
	item       PetListItem
	source     PackageSource
}

type refreshOperation struct {
	done     chan struct{}
	snapshot CatalogSnapshot
	err      error
}

type scanResult struct {
	roots  []CatalogRoot
	items  []catalogEntry
	issues []Issue
	err    error
}

type candidate struct {
	source PackageSource
}

var _ PetCatalog = (*Catalog)(nil)

func NewPetCatalog(config CatalogConfig) (*Catalog, error) {
	if config.Adapter == nil {
		config.Adapter = NewDefaultAdapter()
	}
	cacheRoot := strings.TrimSpace(config.CacheRoot)
	if cacheRoot == "" {
		root, err := os.UserCacheDir()
		if err != nil || strings.TrimSpace(root) == "" {
			return nil, &DiagnosticError{Issues: []Issue{{
				Code:                IssuePackageReadFailed,
				SourceKind:          SourceCodexHome,
				Retryable:           true,
				Severity:            SeverityError,
				SafeFallbackSummary: "the Pet cache location is unavailable",
			}}}
		}
		cacheRoot = filepath.Join(root, "dsh-work", "pets")
	}
	abs, err := filepath.Abs(filepath.Clean(cacheRoot))
	if err != nil || strings.TrimSpace(abs) == "" {
		return nil, errors.New("pet cache root is invalid")
	}
	return &Catalog{
		config:    config,
		adapter:   config.Adapter,
		cacheRoot: filepath.Clean(abs),
		snapshot:  CatalogSnapshot{ScanState: ScanNeverScanned},
		entries:   make(map[string]catalogEntry),
	}, nil
}

func NewCatalog(config CatalogConfig) (*Catalog, error) {
	return NewPetCatalog(config)
}

func (c *Catalog) Snapshot(ctx context.Context) (CatalogSnapshot, error) {
	if err := contextErr(ctx); err != nil {
		return CatalogSnapshot{}, err
	}
	if c == nil {
		return CatalogSnapshot{}, errors.New("pet catalog is unavailable")
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot.clone(), nil
}

func (c *Catalog) Refresh(ctx context.Context) (CatalogSnapshot, error) {
	if c == nil {
		return CatalogSnapshot{}, errors.New("pet catalog is unavailable")
	}
	if err := contextErr(ctx); err != nil {
		return CatalogSnapshot{}, err
	}
	c.mu.Lock()
	if c.refresh != nil {
		op := c.refresh
		c.mu.Unlock()
		return waitRefresh(ctx, op)
	}
	op := &refreshOperation{done: make(chan struct{})}
	previous := c.snapshot.clone()
	c.refresh = op
	c.snapshot.ScanState = ScanScanning
	c.mu.Unlock()

	go c.runRefresh(ctx, previous, op)
	return waitRefresh(ctx, op)
}

func waitRefresh(ctx context.Context, op *refreshOperation) (CatalogSnapshot, error) {
	select {
	case <-op.done:
		return op.snapshot.clone(), op.err
	case <-contextDone(ctx):
		return CatalogSnapshot{}, ctx.Err()
	}
}

func contextDone(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}

func (c *Catalog) runRefresh(ctx context.Context, previous CatalogSnapshot, op *refreshOperation) {
	result := c.scan(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if errors.Is(result.err, context.Canceled) || errors.Is(result.err, context.DeadlineExceeded) {
		// Cancellation does not publish a new or partial result. Restore the
		// exact prior snapshot and keep its materialized entries.
		c.snapshot = previous.clone()
		op.snapshot = c.snapshot.clone()
		op.err = result.err
	} else if result.err != nil {
		c.snapshot = failedSnapshot(previous, result)
		op.snapshot = c.snapshot.clone()
		op.err = result.err
	} else if err := contextErr(ctx); err != nil {
		// A scan that finished just as its context was cancelled is still not
		// allowed to publish a new revision.
		c.snapshot = previous.clone()
		op.snapshot = c.snapshot.clone()
		op.err = err
	} else {
		c.snapshot = readySnapshot(previous, result)
		c.entries = make(map[string]catalogEntry, len(result.items))
		for _, entry := range result.items {
			c.entries[entry.item.StableSourceKey] = cloneCatalogEntry(entry)
		}
		op.snapshot = c.snapshot.clone()
	}
	c.refresh = nil
	close(op.done)
}

func readySnapshot(previous CatalogSnapshot, result scanResult) CatalogSnapshot {
	items := make([]PetListItem, 0, len(result.items))
	for _, entry := range result.items {
		items = append(items, entry.item.clone())
	}
	sortItems(items)
	issues := boundedIssues(result.issues)
	sortIssues(issues)
	now := time.Now().UTC()
	return CatalogSnapshot{
		Revision:  previous.Revision + 1,
		ScanState: ScanReady,
		Stale:     false,
		ScannedAt: &now,
		Roots:     append([]CatalogRoot(nil), result.roots...),
		Items:     items,
		Issues:    issues,
	}
}

func failedSnapshot(previous CatalogSnapshot, result scanResult) CatalogSnapshot {
	snapshot := previous.clone()
	snapshot.ScanState = ScanFailed
	snapshot.Stale = previous.Revision > 0 || len(previous.Items) > 0
	if previous.Revision == 0 && len(previous.Items) == 0 {
		snapshot.Items = nil
		snapshot.ScannedAt = nil
	}
	if len(result.roots) > 0 {
		snapshot.Roots = append([]CatalogRoot(nil), result.roots...)
	}
	issues := append([]Issue(nil), result.issues...)
	if len(issues) == 0 {
		issues = []Issue{{
			Code:                IssueHomeUnavailable,
			SourceKind:          SourceCodexHome,
			Retryable:           true,
			Severity:            SeverityError,
			SafeFallbackSummary: "the Codex pet directory could not be read",
		}}
	}
	sortIssues(issues)
	snapshot.Issues = boundedIssues(issues)
	return snapshot
}

func cloneCatalogEntry(entry catalogEntry) catalogEntry {
	entry.definition = entry.definition.clone()
	entry.item = entry.item.clone()
	return entry
}

func (c *Catalog) Inspect(ctx context.Context, source PackageSource) (Inspection, error) {
	if c == nil {
		return Inspection{}, errors.New("pet catalog is unavailable")
	}
	if err := contextErr(ctx); err != nil {
		return Inspection{}, err
	}
	definition, inventory, err := c.loadSource(ctx, source)
	if err != nil {
		return Inspection{}, err
	}
	definition, err = c.materializeDefinition(ctx, inventory, definition)
	if err != nil {
		return Inspection{}, err
	}
	item := summaryForDefinition(definition)
	return Inspection{
		Item:       item,
		Definition: definition,
		Issues:     cloneIssues(definition.Diagnostics),
	}, nil
}

func (c *Catalog) Resolve(ctx context.Context, stableSourceKey string) (PetDefinition, error) {
	if c == nil {
		return PetDefinition{}, errors.New("pet catalog is unavailable")
	}
	if err := contextErr(ctx); err != nil {
		return PetDefinition{}, err
	}
	stableSourceKey = strings.TrimSpace(stableSourceKey)
	c.mu.RLock()
	entry, ok := c.entries[stableSourceKey]
	c.mu.RUnlock()
	if !ok {
		return PetDefinition{}, &DiagnosticError{Issues: []Issue{{
			Code:                IssueCatalogItemNotFound,
			SourceKind:          SourceUnknown,
			Retryable:           true,
			Severity:            SeverityError,
			SafeFallbackSummary: "the selected Pet is unavailable",
		}}}
	}
	// Resolve returns the immutable materialized definition accepted by the
	// latest complete scan. This keeps ordinary reads and previews stable even
	// when an external package is edited after the scan.
	return entry.definition.clone(), nil
}

// Revalidate is the explicit commit-time source check. It never changes the
// published snapshot or catalog entry; callers can use the returned complete
// definition for renderer preflight and only then commit a new preference.
func (c *Catalog) Revalidate(ctx context.Context, stableSourceKey string) (PetDefinition, error) {
	if c == nil {
		return PetDefinition{}, errors.New("pet catalog is unavailable")
	}
	if err := contextErr(ctx); err != nil {
		return PetDefinition{}, err
	}
	stableSourceKey = strings.TrimSpace(stableSourceKey)
	c.mu.RLock()
	entry, ok := c.entries[stableSourceKey]
	c.mu.RUnlock()
	if !ok {
		return PetDefinition{}, &DiagnosticError{Issues: []Issue{{
			Code:                IssueCatalogItemNotFound,
			SourceKind:          SourceUnknown,
			Retryable:           true,
			Severity:            SeverityError,
			SafeFallbackSummary: "the selected Pet is unavailable",
		}}}
	}
	definition, inventory, err := c.loadSource(ctx, entry.source)
	if err != nil {
		return PetDefinition{}, err
	}
	return c.materializeDefinition(ctx, inventory, definition)
}

func (c *Catalog) scan(ctx context.Context) scanResult {
	if err := contextErr(ctx); err != nil {
		return scanResult{err: err}
	}
	home, err := c.resolveCodexHome()
	if err != nil {
		issues := []Issue{{
			Code:                IssueHomeUnavailable,
			SourceKind:          SourceCodexHome,
			Retryable:           true,
			Severity:            SeverityError,
			SafeFallbackSummary: "the Codex pet directory could not be read",
		}}
		return scanResult{issues: issues, err: stableScanError(issues)}
	}
	roots, candidates, issues, err := discoverCandidates(ctx, home)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return scanResult{roots: roots, issues: issues, err: err}
		}
		return scanResult{roots: roots, issues: issues, err: stableScanError(issues)}
	}
	if c.config.CommunityHome != "" {
		more, status, notes, _ := discoverRoot(ctx, c.config.CommunityHome, "pets", SourceCommunity, "config.jsonc")
		candidates = append(candidates, more...)
		issues = append(issues, notes...)
		roots = append(roots, CatalogRoot{Kind: RootKind("dsh-community"), Status: status})
	}
	entries := make([]catalogEntry, 0, len(candidates))
	for _, candidate := range candidates {
		if err := contextErr(ctx); err != nil {
			return scanResult{roots: roots, issues: issues, err: err}
		}
		definition, inventory, err := c.loadSource(ctx, candidate.source)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return scanResult{roots: roots, issues: issues, err: err}
			}
			issues = appendDiagnosticIssues(issues, candidate.source, err)
			continue
		}
		definition, err = c.materializeDefinition(ctx, inventory, definition)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return scanResult{roots: roots, issues: issues, err: err}
			}
			issues = appendDiagnosticIssues(issues, candidate.source, err)
			continue
		}
		item := summaryForDefinition(definition)
		entries = append(entries, catalogEntry{definition: definition, item: item, source: candidate.source})
		issues = append(issues, definition.Diagnostics...)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return itemLess(entries[i].item, entries[j].item)
	})
	if err := contextErr(ctx); err != nil {
		return scanResult{roots: roots, issues: issues, err: err}
	}
	sortIssues(issues)
	return scanResult{roots: roots, items: entries, issues: boundedIssues(issues)}
}

func (c *Catalog) resolveCodexHome() (string, error) {
	// CODEX_HOME is the source-format contract. A non-empty environment value
	// wins over injected/default locations, including the optional test seam.
	env := c.config.Env
	if env == nil {
		env = os.Getenv
	}
	configured := env("CODEX_HOME")
	explicitHome := configured != ""
	if configured == "" {
		configured = strings.TrimSpace(c.config.CodexHome)
		explicitHome = configured != ""
	}
	if configured == "" {
		homeDir := c.config.HomeDir
		if homeDir == nil {
			homeDir = os.UserHomeDir
		}
		home, err := homeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return "", errors.New("user home is unavailable")
		}
		configured = filepath.Join(home, ".codex")
	}
	abs, err := filepath.Abs(filepath.Clean(configured))
	if err != nil || strings.TrimSpace(abs) == "" {
		return "", errors.New("CODEX_HOME is invalid")
	}
	info, err := os.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		// A missing default .codex directory is a valid empty catalog. An
		// explicitly configured CODEX_HOME is reported as a retryable failure.
		if !explicitHome {
			return abs, nil
		}
		return "", errors.New("CODEX_HOME does not exist")
	}
	if err != nil || !info.IsDir() {
		return "", errors.New("CODEX_HOME is not readable")
	}
	return filepath.Clean(abs), nil
}

func discoverCandidates(ctx context.Context, home string) ([]CatalogRoot, []candidate, []Issue, error) {
	roots := make([]CatalogRoot, 0, 2)
	issues := make([]Issue, 0)
	petCandidates, petStatus, petIssues, petErr := discoverRoot(ctx, home, "pets", SourceCodexPets, "pet.json")
	roots = append(roots, CatalogRoot{Kind: RootCodexPets, Status: petStatus})
	issues = append(issues, petIssues...)
	if petErr != nil {
		return roots, nil, issues, petErr
	}
	avatarCandidates, avatarStatus, avatarIssues, avatarErr := discoverRoot(ctx, home, "avatars", SourceCodexAvatars, "avatar.json")
	roots = append(roots, CatalogRoot{Kind: RootCodexAvatars, Status: avatarStatus})
	issues = append(issues, avatarIssues...)
	if avatarErr != nil {
		return roots, nil, issues, avatarErr
	}

	byFolder := make(map[string]PackageSource, len(petCandidates)+len(avatarCandidates))
	conflicts := make(map[string]bool)
	for _, item := range petCandidates {
		key := folderIdentity(item.source.Folder)
		if _, exists := byFolder[key]; exists {
			if !conflicts[key] {
				issues = appendIssue(issues, Issue{
					Code:       IssueDiscoveryConflict,
					Args:       map[string]string{"folder": sanitizeFolderIdentity(item.source.Folder)},
					SourceKind: SourceCodexPets,
					Severity:   SeverityWarning,
				})
				conflicts[key] = true
			}
			continue
		}
		byFolder[key] = item.source
	}
	for _, item := range avatarCandidates {
		key := folderIdentity(item.source.Folder)
		if _, exists := byFolder[key]; exists {
			if !conflicts[key] {
				issues = appendIssue(issues, Issue{
					Code:       IssueDiscoveryConflict,
					Args:       map[string]string{"folder": sanitizeFolderIdentity(item.source.Folder)},
					SourceKind: SourceCodexAvatars,
					Retryable:  false,
					Severity:   SeverityWarning,
				})
				conflicts[key] = true
			}
			continue
		}
		byFolder[key] = item.source
	}
	candidates := make([]candidate, 0, len(byFolder))
	for _, source := range byFolder {
		candidates = append(candidates, candidate{source: source})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].source.StableSourceKey < candidates[j].source.StableSourceKey
	})
	return roots, candidates, issues, nil
}

func discoverRoot(ctx context.Context, home, name string, sourceKind SourceKind, manifest string) ([]candidate, RootStatus, []Issue, error) {
	if err := contextErr(ctx); err != nil {
		return nil, RootUnreadable, nil, err
	}
	root := filepath.Join(home, name)
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, RootMissing, nil, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(root) {
		return nil, RootUnreadable, []Issue{{
			Code:       IssueRootUnreadable,
			SourceKind: sourceKind,
			Retryable:  true,
			Severity:   SeverityWarning,
			Args:       map[string]string{"root": name},
		}}, errors.New("Codex pet root is unreadable")
	}
	directory, err := os.Open(root)
	if err != nil {
		return nil, RootUnreadable, []Issue{{
			Code:       IssueRootUnreadable,
			SourceKind: sourceKind,
			Retryable:  true,
			Severity:   SeverityWarning,
			Args:       map[string]string{"root": name},
		}}, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(MaxCatalogPackages + 1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err != nil {
		return nil, RootUnreadable, []Issue{{
			Code:       IssueRootUnreadable,
			SourceKind: sourceKind,
			Retryable:  true,
			Severity:   SeverityWarning,
			Args:       map[string]string{"root": name},
		}}, err
	}
	tooManyEntries := len(entries) > MaxCatalogPackages
	if tooManyEntries {
		entries = entries[:MaxCatalogPackages]
	}
	candidates := make([]candidate, 0, len(entries))
	issues := make([]Issue, 0)
	if tooManyEntries {
		issues = append(issues, Issue{
			Code:                IssueCatalogLimitExceeded,
			SourceKind:          sourceKind,
			Retryable:           false,
			Severity:            SeverityWarning,
			Args:                map[string]string{"root": name},
			SafeFallbackSummary: defaultIssueSummary(IssueCatalogLimitExceeded),
		})
	}
	for _, entry := range entries {
		if err := contextErr(ctx); err != nil {
			return nil, RootAvailable, nil, err
		}
		packageRoot := filepath.Join(root, entry.Name())
		folder := entry.Name()
		if !validFolderIdentity(folder) {
			continue
		}
		candidateKind := sourceKind
		candidateManifest := manifest
		if sourceKind == SourceCodexPets {
			// Default Codex discovery is intentionally limited to the documented
			// pets/*/pet.json shape. Native-only packages remain available through
			// the explicit dsh-native adapter seam; a dual package may opt into the
			// native manifest after its Codex pet.json entry has been discovered.
			if _, petErr := os.Lstat(filepath.Join(packageRoot, manifest)); errors.Is(petErr, os.ErrNotExist) {
				continue
			}
			// dsh-pet.json is the native/dual-compatible entry and has
			// precedence over a Codex pet.json in the same package folder.
			nativePath := filepath.Join(packageRoot, "dsh-pet.json")
			if _, nativeErr := os.Lstat(nativePath); nativeErr == nil {
				candidateKind = SourceDshPets
				candidateManifest = "dsh-pet.json"
			}
		}
		source := NewCodexPackageSource(packageRoot, candidateKind, folder)
		if candidateKind == SourceCommunity {
			source.ManifestPath = "config.jsonc"
			source.Entry = "config.jsonc"
		}
		if candidateKind == SourceDshPets {
			source = NewDshNativePackageSource(packageRoot, folder)
		}
		if entry.Type()&os.ModeSymlink != 0 || isReparsePoint(packageRoot) {
			// Keep a visible candidate for a present but unsafe package so a
			// same-folder Codex avatar cannot bypass a higher-precedence pets
			// entry. Load will publish the bounded symlink diagnostic.
			candidates = append(candidates, candidate{source: source})
			continue
		}
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(packageRoot, candidateManifest)
		manifestInfo, err := os.Lstat(manifestPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || manifestInfo.IsDir() || manifestInfo.Mode()&os.ModeSymlink != 0 || isReparsePoint(manifestPath) {
			// A manifest entry exists, even if it is unsafe or unreadable. Keep
			// the candidate to preserve pets-over-avatars precedence.
			candidates = append(candidates, candidate{source: source})
			continue
		}
		candidates = append(candidates, candidate{source: source})
	}
	return candidates, RootAvailable, issues, nil
}

func validFolderIdentity(folder string) bool {
	if folder == "" || folder == "." || folder == ".." || !utf8Safe(folder) {
		return false
	}
	for _, r := range folder {
		if r < 0x20 || r == 0x7f || r == '\x00' || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func folderIdentity(folder string) string {
	return strings.ToLower(folder)
}

func utf8Safe(value string) bool {
	return utf8.ValidString(value)
}

func appendDiagnosticIssues(issues []Issue, source PackageSource, err error) []Issue {
	if diagnostic := Diagnostics(err); len(diagnostic) > 0 {
		for _, issue := range diagnostic {
			if issue.SourceKind != SourceCodexPets && issue.SourceKind != SourceCodexAvatars && issue.SourceKind != SourceDshPets && issue.SourceKind != SourceCodexHome {
				issue.SourceKind = source.Kind
			}
			if safeCode, ok := safeString(issue.Code, MaxSafeTextRunes); ok && !strings.ContainsAny(safeCode, `/\\`) && !strings.Contains(safeCode, "://") {
				issue.Code = safeCode
			} else {
				issue.Code = IssuePackageReadFailed
			}
			if issue.Severity != SeverityInfo && issue.Severity != SeverityWarning && issue.Severity != SeverityError {
				issue.Severity = SeverityError
			}
			issue.Args = packageIssue(source, issue.Code, issue.Severity, issue.Retryable, issue.Args).Args
			if issue.SafeFallbackSummary != "" {
				if safe, ok := safeSummary(issue.SafeFallbackSummary); ok {
					issue.SafeFallbackSummary = safe
				} else {
					issue.SafeFallbackSummary = ""
				}
			}
			issues = appendIssue(issues, issue)
		}
		return issues
	}
	return appendIssue(issues, packageIssue(source, IssuePackageReadFailed, SeverityError, true, nil))
}

func summaryForDefinition(definition PetDefinition) PetListItem {
	capability := "visual.frame-sequence"
	if definition.Geometry.FrameCount == 1 {
		capability = "visual.static"
	}
	capabilities := []string{capability}
	if definition.Directions != nil {
		capabilities = append(capabilities, "look.directional16")
	}
	issueCodes := make([]string, 0, len(definition.Diagnostics))
	for _, issue := range definition.Diagnostics {
		issueCodes = append(issueCodes, issue.Code)
	}
	sort.Strings(issueCodes)
	return PetListItem{
		StableSourceKey: definition.Source.StableKey,
		DisplayName:     definition.Identity.DisplayName,
		Description:     definition.Identity.Description,
		SourceBadge:     definition.Source.Badge,
		Availability:    AvailabilityReady,
		Capabilities:    capabilities,
		IssueCodes:      issueCodes,
	}
}

func stableScanError(issues []Issue) error {
	if len(issues) == 0 {
		issues = []Issue{{
			Code:                IssueHomeUnavailable,
			SourceKind:          SourceCodexHome,
			Retryable:           true,
			Severity:            SeverityError,
			SafeFallbackSummary: "the Codex pet directory could not be read",
		}}
	}
	return &DiagnosticError{Issues: boundedIssues(issues)}
}

func itemLess(left, right PetListItem) bool {
	leftName := strings.ToLower(strings.TrimSpace(left.DisplayName))
	rightName := strings.ToLower(strings.TrimSpace(right.DisplayName))
	if leftName != rightName {
		return leftName < rightName
	}
	return left.StableSourceKey < right.StableSourceKey
}

func sortItems(items []PetListItem) {
	sort.SliceStable(items, func(i, j int) bool { return itemLess(items[i], items[j]) })
}

func (c *Catalog) loadSource(ctx context.Context, source PackageSource) (PetDefinition, packageInventory, error) {
	inventory, err := inspectPackage(ctx, source)
	if err != nil {
		return PetDefinition{}, packageInventory{}, err
	}
	if adapter, ok := c.adapter.(interface {
		loadFromInventory(context.Context, packageInventory) (PetDefinition, packageInventory, error)
	}); ok {
		return adapter.loadFromInventory(ctx, inventory)
	}
	definition, err := c.adapter.Load(ctx, source)
	return definition, inventory, err
}

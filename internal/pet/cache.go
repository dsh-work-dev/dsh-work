package pet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// Materialized packages are bounded independently by the package limits.
	// Keeping eight accepted digests gives selection/revalidation a small
	// amount of history without allowing every source edit to grow the cache
	// forever.
	maxDigestCacheEntries = 8
	maxDigestCacheBytes   = 8 * MaxPackageBytes
	maxDigestCacheNodes   = MaxCommunityFiles * 4
	maxDigestCacheScan    = maxDigestCacheEntries + MaxPackageFiles
)

var materializeCacheMu sync.Mutex

type digestCacheEntry struct {
	name    string
	path    string
	size    int64
	modTime time.Time
}

func packageDigest(inventory packageInventory) string {
	hash := sha256.New()
	paths := append([]string(nil), inventory.Ordered...)
	sort.Strings(paths)
	for _, relative := range paths {
		file := inventory.Files[relative]
		_, _ = io.WriteString(hash, filepath.ToSlash(relative))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(file.Data)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// materializePackage publishes an accepted package under a digest-named
// application-owned directory. The source is read before this method and is
// never modified. A complete temporary tree is renamed into place only after
// every file has been copied and verified.
func materializePackage(ctx context.Context, cacheRoot string, inventory packageInventory) (string, error) {
	if err := contextErr(ctx); err != nil {
		return "", err
	}
	// Publication and pruning share one lock. Without this, concurrent
	// materializations could each consider the other's current digest stale
	// and delete a package that is still being published.
	materializeCacheMu.Lock()
	defer materializeCacheMu.Unlock()

	digest := packageDigest(inventory)
	if len(digest) != sha256.Size*2 {
		return "", errors.New("invalid package digest")
	}
	cacheRoot, err := filepath.Abs(filepath.Clean(cacheRoot))
	if err != nil || strings.TrimSpace(cacheRoot) == "" {
		return "", errors.New("pet cache root is invalid")
	}
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return "", packageError(inventory.Source, IssuePackageReadFailed, SeverityError, true, nil)
	}
	cacheInfo, err := os.Lstat(cacheRoot)
	if err != nil || !cacheInfo.IsDir() || cacheInfo.Mode()&os.ModeSymlink != 0 || isReparsePoint(cacheRoot) {
		return "", packageError(inventory.Source, IssuePackageReadFailed, SeverityError, true, nil)
	}
	finalPath := filepath.Join(cacheRoot, digest)
	if !pathWithin(cacheRoot, finalPath) {
		return "", packageError(inventory.Source, IssuePathOutsideRoot, SeverityError, false, nil)
	}
	if info, err := os.Lstat(finalPath); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(finalPath) || !verifyMaterializedPackage(finalPath, inventory) {
			return "", packageError(inventory.Source, IssuePackageReadFailed, SeverityError, true, nil)
		}
		_ = cleanupDigestCache(cacheRoot, digest)
		return finalPath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", packageError(inventory.Source, IssuePackageReadFailed, SeverityError, true, nil)
	}

	temporary, err := os.MkdirTemp(cacheRoot, ".pet-*")
	if err != nil {
		return "", packageError(inventory.Source, IssuePackageReadFailed, SeverityError, true, nil)
	}
	keepTemporary := false
	defer func() {
		if !keepTemporary {
			_ = os.RemoveAll(temporary)
		}
	}()

	paths := append([]string(nil), inventory.Ordered...)
	sort.Strings(paths)
	for _, relative := range paths {
		if err := contextErr(ctx); err != nil {
			return "", err
		}
		file := inventory.Files[relative]
		if err := validateCacheRelativePath(relative); err != nil {
			return "", packageError(inventory.Source, IssuePathOutsideRoot, SeverityError, false, nil)
		}
		target := filepath.Join(temporary, relative)
		if !pathWithin(temporary, target) {
			return "", packageError(inventory.Source, IssuePathOutsideRoot, SeverityError, false, nil)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return "", packageError(inventory.Source, IssuePackageReadFailed, SeverityError, true, nil)
		}
		if err := os.WriteFile(target, file.Data, 0o600); err != nil {
			return "", packageError(inventory.Source, IssuePackageReadFailed, SeverityError, true, nil)
		}
		if err := os.Chmod(target, 0o444); err != nil {
			return "", packageError(inventory.Source, IssuePackageReadFailed, SeverityError, true, nil)
		}
	}
	if !verifyMaterializedPackage(temporary, inventory) {
		return "", packageError(inventory.Source, IssuePackageReadFailed, SeverityError, true, nil)
	}
	if err := makeCacheTreeReadOnly(temporary); err != nil {
		return "", packageError(inventory.Source, IssuePackageReadFailed, SeverityError, true, nil)
	}
	if err := os.Rename(temporary, finalPath); err != nil {
		if info, statErr := os.Lstat(finalPath); statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && !isReparsePoint(finalPath) && verifyMaterializedPackage(finalPath, inventory) {
			_ = cleanupDigestCache(cacheRoot, digest)
			return finalPath, nil
		}
		return "", packageError(inventory.Source, IssuePackageReadFailed, SeverityError, true, nil)
	}
	keepTemporary = true
	// Cleanup is maintenance after a successful atomic publication. A cleanup
	// failure must not discard a valid newly materialized package; unsafe or
	// unrecognized entries are deliberately left untouched by the helper.
	_ = cleanupDigestCache(cacheRoot, digest)
	return finalPath, nil
}

func cleanupDigestCache(cacheRoot, currentDigest string) error {
	return cleanupDigestCacheWithLimits(cacheRoot, currentDigest, maxDigestCacheEntries, maxDigestCacheBytes)
}

func cleanupDigestCacheWithLimits(cacheRoot, currentDigest string, maxEntries int, maxBytes int64) error {
	if maxEntries < 1 || maxBytes < 0 || !isDigestCacheName(currentDigest) {
		return errors.New("invalid digest cache cleanup limits")
	}
	root, err := filepath.Abs(filepath.Clean(cacheRoot))
	if err != nil || strings.TrimSpace(root) == "" {
		return errors.New("pet cache root is invalid")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || isReparsePoint(root) {
		return errors.New("pet cache root is unsafe")
	}

	directory, err := os.Open(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(maxDigestCacheScan + 1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err != nil {
		return err
	}
	if len(entries) > maxDigestCacheScan {
		// Do not enumerate an attacker-controlled cache directory without a
		// bound. Cleanup is maintenance; retaining entries is safer than an
		// unbounded scan or a partial deletion.
		return errors.New("too many digest cache entries")
	}
	candidates := make([]digestCacheEntry, 0, len(entries))
	var totalBytes int64
	for _, entry := range entries {
		name := entry.Name()
		if !isDigestCacheName(name) {
			continue
		}
		path := filepath.Join(root, name)
		if !pathWithin(root, path) {
			continue
		}
		info, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(path) {
			// A digest-looking file, symlink or reparse point is not an
			// application-owned cache directory. Never remove it.
			continue
		}
		size, safe := digestCacheTreeSize(root, path)
		if !safe || totalBytes > int64(^uint64(0)>>1)-size {
			// Conservatively retain entries whose tree cannot be bounded or
			// whose size cannot be represented safely.
			continue
		}
		candidates = append(candidates, digestCacheEntry{
			name:    name,
			path:    path,
			size:    size,
			modTime: info.ModTime(),
		})
		totalBytes += size
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].modTime.Equal(candidates[j].modTime) {
			return candidates[i].name < candidates[j].name
		}
		return candidates[i].modTime.Before(candidates[j].modTime)
	})
	for len(candidates) > maxEntries || totalBytes > maxBytes {
		oldest := -1
		for index := range candidates {
			if candidates[index].name != currentDigest {
				oldest = index
				break
			}
		}
		if oldest < 0 {
			// The current digest is always retained, even if one package is
			// larger than the configured aggregate byte budget.
			return nil
		}
		entry := candidates[oldest]
		if err := removeDigestCacheEntry(root, entry); err != nil {
			return err
		}
		totalBytes -= entry.size
		candidates = append(candidates[:oldest], candidates[oldest+1:]...)
	}
	return nil
}

func isDigestCacheName(name string) bool {
	if len(name) != sha256.Size*2 || name != strings.ToLower(name) {
		return false
	}
	for _, char := range name {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func digestCacheTreeSize(cacheRoot, entryRoot string) (int64, bool) {
	if !pathWithin(cacheRoot, entryRoot) {
		return 0, false
	}
	var total int64
	nodes := 0
	err := filepath.WalkDir(entryRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		nodes++
		if nodes > maxDigestCacheNodes || !pathWithin(entryRoot, path) || isReparsePoint(path) || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("unsafe digest cache tree")
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("non-regular digest cache entry")
		}
		info, err := entry.Info()
		if err != nil || info.Size() < 0 || info.Size() > MaxPackageBytes || total > MaxCommunityBytes-info.Size() {
			return errors.New("unbounded digest cache entry")
		}
		total += info.Size()
		return nil
	})
	return total, err == nil
}

func removeDigestCacheEntry(cacheRoot string, entry digestCacheEntry) error {
	root, err := filepath.Abs(filepath.Clean(cacheRoot))
	if err != nil || strings.TrimSpace(root) == "" || !isDigestCacheName(entry.name) {
		return errors.New("digest cache path is invalid")
	}
	path, err := filepath.Abs(filepath.Clean(entry.path))
	if err != nil || !pathWithin(root, path) || path != filepath.Join(root, entry.name) {
		return errors.New("digest cache path is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(path) {
		return errors.New("digest cache path is unsafe")
	}
	if _, safe := digestCacheTreeSize(root, path); !safe {
		return errors.New("digest cache tree is unsafe")
	}
	if err := makeCacheTreeWritable(path); err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		// Do not leave a partially deleted cache tree writable when the
		// filesystem refuses the removal.
		_ = makeCacheTreeReadOnly(path)
		return err
	}
	return nil
}

func validateCacheRelativePath(relative string) error {
	clean, code := safeRelativePath(relative)
	if code != "" || clean != relative {
		return errors.New("invalid cache relative path")
	}
	return nil
}

func verifyMaterializedPackage(root string, inventory packageInventory) bool {
	expected := make(map[string]struct{}, len(inventory.Ordered))
	for _, relative := range inventory.Ordered {
		if validateCacheRelativePath(relative) != nil {
			return false
		}
		expected[relative] = struct{}{}
	}
	seen := make(map[string]struct{}, len(expected))
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || isReparsePoint(path) {
				return errors.New("invalid cache root")
			}
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Type()&os.ModeSymlink != 0 || isReparsePoint(path) {
				return errors.New("symlink in cache")
			}
			prefix := relative + string(filepath.Separator)
			belongsToExpected := false
			for expectedPath := range expected {
				if strings.HasPrefix(expectedPath, prefix) {
					belongsToExpected = true
					break
				}
			}
			if !belongsToExpected {
				return errors.New("unexpected cache directory")
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || isReparsePoint(path) || !entry.Type().IsRegular() {
			return errors.New("non-regular cache entry")
		}
		if _, ok := expected[relative]; !ok {
			return errors.New("unexpected cache entry")
		}
		seen[relative] = struct{}{}
		return nil
	}); err != nil || len(seen) != len(expected) {
		return false
	}
	for _, relative := range inventory.Ordered {
		path := filepath.Join(root, relative)
		if !pathWithin(root, path) {
			return false
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(path) || info.Mode().Perm()&0o222 != 0 || info.Size() != int64(len(inventory.Files[relative].Data)) {
			return false
		}
		data, err := os.ReadFile(path)
		if err != nil || HashBytes(data) != HashBytes(inventory.Files[relative].Data) {
			return false
		}
	}
	return true
}

func makeCacheTreeReadOnly(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if isReparsePoint(path) || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("reparse point in cache")
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o500)
		}
		if !entry.Type().IsRegular() {
			return errors.New("non-regular cache entry")
		}
		return os.Chmod(path, 0o444)
	})
}

func makeCacheTreeWritable(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if isReparsePoint(path) || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("reparse point in cache")
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		if !entry.Type().IsRegular() {
			return errors.New("non-regular cache entry")
		}
		return os.Chmod(path, 0o600)
	})
}

func (c *Catalog) materializeDefinition(ctx context.Context, inventory packageInventory, definition PetDefinition) (PetDefinition, error) {
	_, err := materializePackage(ctx, c.cacheRoot, inventory)
	if err != nil {
		return PetDefinition{}, err
	}
	definition.Source.Digest = packageDigest(inventory)
	return definition, nil
}

package pet

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMaterializePackagePrunesOldDigestsWithinBounds(t *testing.T) {
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	keepFile := filepath.Join(cacheRoot, "keep-me.txt")
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keepFile, []byte("do not remove"), 0o600); err != nil {
		t.Fatal(err)
	}

	paths := make([]string, 0, maxDigestCacheEntries+3)
	for index := 0; index < maxDigestCacheEntries+3; index++ {
		path, err := materializePackage(context.Background(), cacheRoot, cacheInventoryForTest(index, []byte{byte(index + 1)}))
		if err != nil {
			t.Fatalf("materializePackage(%d): %v", index, err)
		}
		paths = append(paths, path)
	}

	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	digestCount := 0
	for _, entry := range entries {
		if entry.IsDir() && isDigestCacheName(entry.Name()) {
			digestCount++
		}
	}
	if digestCount > maxDigestCacheEntries {
		t.Fatalf("digest cache contains %d entries, want at most %d", digestCount, maxDigestCacheEntries)
	}
	if _, err := os.Stat(paths[len(paths)-1]); err != nil {
		t.Fatalf("current digest was pruned: %v", err)
	}
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatalf("old digest still exists, err=%v", err)
	}
	if _, err := os.Stat(keepFile); err != nil {
		t.Fatalf("non-digest cache-root entry changed: %v", err)
	}
}

func TestCleanupDigestCacheEnforcesByteLimitAndRetainsCurrent(t *testing.T) {
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	paths := make([]string, 0, 3)
	for index := 0; index < 3; index++ {
		path, err := materializePackage(context.Background(), cacheRoot, cacheInventoryForTest(index, []byte("x")))
		if err != nil {
			t.Fatalf("materializePackage(%d): %v", index, err)
		}
		paths = append(paths, path)
		stamp := time.Unix(int64(index+1), 0)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatalf("Chtimes(%q): %v", path, err)
		}
	}

	if err := cleanupDigestCacheWithLimits(cacheRoot, filepath.Base(paths[2]), 10, 1); err != nil {
		t.Fatalf("cleanupDigestCacheWithLimits: %v", err)
	}
	for index, path := range paths[:2] {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("old digest %d remains after byte pruning, err=%v", index, err)
		}
	}
	if _, err := os.Stat(paths[2]); err != nil {
		t.Fatalf("current digest was pruned by byte limit: %v", err)
	}
}

func TestCleanupDigestCacheSkipsNonCompliantEntriesAndStaysInsideRoot(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "cache")
	outsideRoot := filepath.Join(root, "outside")
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	outsideMarker := filepath.Join(outsideRoot, "marker.txt")
	if err := os.WriteFile(outsideMarker, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}

	nonDigestDir := filepath.Join(cacheRoot, "not-a-digest")
	if err := os.MkdirAll(nonDigestDir, 0o700); err != nil {
		t.Fatal(err)
	}
	nonDigestMarker := filepath.Join(nonDigestDir, "marker.txt")
	if err := os.WriteFile(nonDigestMarker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	uppercaseDigest := filepath.Join(cacheRoot, strings.Repeat("A", sha256.Size*2))
	if err := os.MkdirAll(uppercaseDigest, 0o700); err != nil {
		t.Fatal(err)
	}
	uppercaseMarker := filepath.Join(uppercaseDigest, "marker.txt")
	if err := os.WriteFile(uppercaseMarker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	regularDigest := filepath.Join(cacheRoot, strings.Repeat("b", sha256.Size*2))
	if err := os.WriteFile(regularDigest, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	oldPath, err := materializePackage(context.Background(), cacheRoot, cacheInventoryForTest(10, []byte("old")))
	if err != nil {
		t.Fatal(err)
	}
	currentPath, err := materializePackage(context.Background(), cacheRoot, cacheInventoryForTest(11, []byte("current")))
	if err != nil {
		t.Fatal(err)
	}
	oldStamp := time.Unix(1, 0)
	currentStamp := time.Unix(2, 0)
	if err := os.Chtimes(oldPath, oldStamp, oldStamp); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(currentPath, currentStamp, currentStamp); err != nil {
		t.Fatal(err)
	}

	symlinkDigest := filepath.Join(cacheRoot, strings.Repeat("c", sha256.Size*2))
	symlinkCreated := os.Symlink(outsideRoot, symlinkDigest) == nil
	if symlinkCreated {
		t.Cleanup(func() { _ = os.Remove(symlinkDigest) })
	}

	if err := cleanupDigestCacheWithLimits(cacheRoot, filepath.Base(currentPath), 1, 0); err != nil {
		t.Fatalf("cleanupDigestCacheWithLimits: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old compliant digest remains, err=%v", err)
	}
	if _, err := os.Stat(currentPath); err != nil {
		t.Fatalf("current compliant digest was removed: %v", err)
	}
	if _, err := os.Stat(nonDigestMarker); err != nil {
		t.Fatalf("non-digest directory changed: %v", err)
	}
	if _, err := os.Stat(uppercaseMarker); err != nil {
		t.Fatalf("uppercase digest directory changed: %v", err)
	}
	if _, err := os.Stat(regularDigest); err != nil {
		t.Fatalf("digest-looking regular file changed: %v", err)
	}
	if _, err := os.Stat(outsideMarker); err != nil {
		t.Fatalf("path outside cache root was affected: %v", err)
	}
	outsideDigest := filepath.Join(outsideRoot, strings.Repeat("d", sha256.Size*2))
	if err := os.MkdirAll(outsideDigest, 0o700); err != nil {
		t.Fatal(err)
	}
	outsideDigestMarker := filepath.Join(outsideDigest, "marker.txt")
	if err := os.WriteFile(outsideDigestMarker, []byte("outside digest"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeDigestCacheEntry(cacheRoot, digestCacheEntry{
		name: strings.Repeat("d", sha256.Size*2), path: outsideDigest,
	}); err == nil {
		t.Fatal("removeDigestCacheEntry accepted a path outside the cache root")
	}
	if _, err := os.Stat(outsideDigestMarker); err != nil {
		t.Fatalf("outside digest path was affected by rejected removal: %v", err)
	}
	if symlinkCreated {
		if _, err := os.Lstat(symlinkDigest); err != nil {
			t.Fatalf("digest-looking symlink was removed: %v", err)
		}
	}
}

func TestIsDigestCacheNameRequiresLowercaseSHA256Hex(t *testing.T) {
	tests := map[string]bool{
		strings.Repeat("a", sha256.Size*2):         true,
		strings.Repeat("A", sha256.Size*2):         false,
		strings.Repeat("a", sha256.Size*2-1):       false,
		strings.Repeat("g", sha256.Size*2):         false,
		"../" + strings.Repeat("a", sha256.Size*2): false,
	}
	for name, want := range tests {
		if got := isDigestCacheName(name); got != want {
			t.Errorf("isDigestCacheName(%q) = %v, want %v", name, got, want)
		}
	}
}

func cacheInventoryForTest(index int, data []byte) packageInventory {
	payload := append([]byte{byte(index)}, data...)
	return packageInventory{
		Source: PackageSource{Kind: SourceDshPets, Folder: "cache-test"},
		Files: map[string]packageFile{
			"asset.dat": {RelativePath: "asset.dat", Data: payload},
		},
		Ordered: []string{"asset.dat"},
	}
}

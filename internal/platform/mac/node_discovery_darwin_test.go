//go:build darwin && cgo

package mac

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMacNodeDiscoveryUsesFallbackNodeAndMatchingNPM(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pathBin := filepath.Join(root, "path")
	nodeBin := filepath.Join(root, "n", "bin")
	for _, bin := range []string{pathBin, nodeBin} {
		if err := os.MkdirAll(bin, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	npm := filepath.Join(nodeBin, "npm")
	if err := os.WriteFile(npm, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathBin)
	node, gotNPM := findMacNode([]string{nodeBin})
	if node != filepath.Join(nodeBin, "node") || gotNPM != npm {
		t.Fatalf("Node/npm pair = %q, %q", node, gotNPM)
	}
	node, gotNPM = findMacNode(nil)
	if node != filepath.Join(pathBin, "node") || gotNPM != "" {
		t.Fatalf("Node-only fallback = %q, %q", node, gotNPM)
	}
}

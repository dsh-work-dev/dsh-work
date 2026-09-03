package workspacecontext

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSurfaceResolverRequiresExplicitWorkspaceSelectionPerGeneration(t *testing.T) {
	resolved, err := (SurfaceResolver{}).Resolve(context.Background(), "generation-1", Request{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.GenerationID != "generation-1" || resolved.State != StateSelectionRequired || resolved.ID != "" || resolved.Path != "" {
		t.Fatalf("unexpected unresolved context: %+v", resolved)
	}
	if err := resolved.ValidateForGeneration("generation-2"); err == nil {
		t.Fatal("ValidateForGeneration() accepted a context from another generation")
	}
}

func TestNewSelectedCanonicalizesAndValidatesExistingDirectory(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "project")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	selected, err := NewSelected("generation-1", "workspace-1", filepath.Join(directory, "."), "Project")
	if err != nil {
		t.Fatal(err)
	}
	if selected.State != StateSelected || selected.Title != "Project" || selected.Path != directory {
		t.Fatalf("unexpected selected context: %+v", selected)
	}
	if err := selected.ValidateForGeneration("generation-1"); err != nil {
		t.Fatalf("ValidateForGeneration() error = %v", err)
	}

	if _, err := NewSelected("generation-1", "workspace-2", filepath.Join(root, "missing"), "Missing"); err == nil {
		t.Fatal("NewSelected() accepted a missing directory")
	}
}

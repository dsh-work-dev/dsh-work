package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/diagnostics"
)

func TestDiagnosticsServiceRequiresWorkspace(t *testing.T) {
	service := NewDiagnosticsService(diagnostics.New(diagnostics.Config{Dir: t.TempDir()}), func(string) error { return nil }, func(string) (string, error) {
		return filepath.Join(t.TempDir(), "diagnostics.zip"), nil
	})
	ctx := LocalClientContext(context.Background(), "worker")
	if _, err := service.GetProblems(ctx); err == nil {
		t.Fatal("worker window received the problem list")
	}
	if _, err := service.GetReport(ctx, ""); err == nil {
		t.Fatal("worker window received a report")
	}
	if _, err := service.ExportBundle(ctx); err == nil {
		t.Fatal("worker window exported a bundle")
	}
	if err := service.OpenLogFolder(ctx); err == nil {
		t.Fatal("worker window opened the log folder")
	}
}

func TestExportBundleCancelledReturnsEmptyResult(t *testing.T) {
	logDir := t.TempDir()
	service := NewDiagnosticsService(diagnostics.New(diagnostics.Config{Dir: logDir}), func(string) error { return nil }, func(string) (string, error) {
		return "", nil
	})
	result, err := service.ExportBundle(LocalClientContext(context.Background(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != "" || result.Size != 0 || result.Omitted != nil {
		t.Fatalf("cancelled result = %#v, want zero value", result)
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("cancelled export created files: %v", entries)
	}
}

func TestExportBundleWritesChosenPath(t *testing.T) {
	outputDir := t.TempDir()
	chosenPath := filepath.Join(outputDir, "chosen.zip")
	service := NewDiagnosticsService(diagnostics.New(diagnostics.Config{Dir: t.TempDir()}), func(string) error { return nil }, func(string) (string, error) {
		return chosenPath, nil
	})
	result, err := service.ExportBundle(LocalClientContext(context.Background(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != chosenPath || result.Size <= 0 {
		t.Fatalf("export result = %#v", result)
	}
	if info, err := os.Stat(chosenPath); err != nil || info.Size() == 0 {
		t.Fatalf("bundle file = %#v, error = %v", info, err)
	}
}

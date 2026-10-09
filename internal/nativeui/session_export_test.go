package nativeui

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSessionExportAtomically(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "session.zip")
	if err := os.WriteFile(destination, []byte("existing archive"), 0600); err != nil {
		t.Fatal(err)
	}
	writeFailure := errors.New("stream interrupted")
	if err := writeSessionExportAtomically(destination, func(writer io.Writer) error {
		_, _ = io.WriteString(writer, "partial archive")
		return writeFailure
	}); !errors.Is(err, writeFailure) {
		t.Fatalf("failed stream error = %v", err)
	}
	if contents, err := os.ReadFile(destination); err != nil || string(contents) != "existing archive" {
		t.Fatalf("failed stream changed destination: contents=%q err=%v", contents, err)
	}

	if err := writeSessionExportAtomically(destination, func(writer io.Writer) error {
		_, err := io.WriteString(writer, "complete archive")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(destination); err != nil || string(contents) != "complete archive" {
		t.Fatalf("completed stream contents=%q err=%v", contents, err)
	}
	leftovers, err := filepath.Glob(filepath.Join(directory, ".dsh-session-export-*.tmp"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary exports remain: %v, err=%v", leftovers, err)
	}
}

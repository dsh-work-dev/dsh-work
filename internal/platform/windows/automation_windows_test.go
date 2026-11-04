//go:build windows

package windows

import (
	"archive/zip"
	"context"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/dshmanager"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutomationDriverArchiveRejectsTraversalAndWrongRoot(t *testing.T) {
	for _, name := range []string{"../escaped.exe", "other/cua-driver.exe", "cua-driver-rs-0.28.1-windows-x86_64/../../escaped.exe"} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "driver.zip")
			f, e := os.Create(archive)
			if e != nil {
				t.Fatal(e)
			}
			z := zip.NewWriter(f)
			entry, e := z.Create(name)
			if e != nil {
				t.Fatal(e)
			}
			entry.Write([]byte("fixture"))
			z.Close()
			f.Close()
			if e := extractAutomationDriver(context.Background(), archive, filepath.Join(root, "out")); e == nil {
				t.Fatal("unsafe driver archive accepted")
			}
		})
	}
}
func TestAutomationDriverSignatureFailureDoesNotPublish(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "automation", "cua-driver", automation.DriverVersion, "windows-amd64")
	os.MkdirAll(destination, 0700)
	os.WriteFile(filepath.Join(destination, "release.json"), []byte(automation.DriverSHA256), 0600)
	i := NewAutomationPreparer(root)
	i.signature = func(string, string) error { return os.ErrPermission }
	if e := i.driver(context.Background(), func(automation.Progress) {}); e == nil || automation.FailureCode(e) != "verify" {
		t.Fatal("signature failure not propagated", e)
	}
}
func TestLiveAutomationDependencyPreparation(t *testing.T) {
	if os.Getenv("DSH_WORK_AUTOMATION_PREPARE_TEST") != "1" {
		t.Skip("explicit network preparation test")
	}
	i := NewAutomationPreparer(t.TempDir())
	resolved, err := NewRunNodeResolver(NewCommandExecutor(), "").Resolve(context.Background(), dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem}, nil)
	if err != nil {
		t.Fatal(err)
	}
	i.ConfigureToolchain(func(context.Context) (dshmanager.ResolvedNode, error) { return resolved, nil })
	for _, c := range []automation.Capability{automation.Browser, automation.Computer} {
		t.Run(string(c), func(t *testing.T) {
			report := func(p automation.Progress) {
				if p.ReceivedBytes == 0 {
					t.Log("step", p.Step)
				}
			}
			if err := i.Prepare(context.Background(), c, report); err != nil {
				t.Fatal(err)
			}
			if err := i.Prepare(context.Background(), c, report); err != nil {
				t.Fatal("verified asset reuse", err)
			}
		})
	}
}

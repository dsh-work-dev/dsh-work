//go:build windows

package windows

import (
	"archive/zip"
	"context"
	"errors"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/dshadapter"
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

type failedComponentExecutor struct{}

func (failedComponentExecutor) Run(context.Context, string, []string, map[string]string, string) (dshadapter.CommandResult, error) {
	return dshadapter.CommandResult{}, errors.New("fixture download failure")
}

func TestLiveAUVPreparationAndFailedRepair(t *testing.T) {
	if os.Getenv("DSH_WORK_AUV_PREPARE_TEST") != "1" {
		t.Skip("explicit AUV installation validation")
	}
	i := NewAutomationPreparer(t.TempDir())
	resolved, err := NewRunNodeResolver(NewCommandExecutor(), "").Resolve(context.Background(), dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem}, nil)
	if err != nil {
		t.Fatal(err)
	}
	i.ConfigureToolchain(func(context.Context) (dshmanager.ResolvedNode, error) { return resolved, nil })
	report := func(p automation.Progress) { t.Log("step", p.Step) }
	if err = i.Prepare(context.Background(), automation.AUV, report); err != nil {
		t.Fatal(err)
	}
	dir, err := automation.PublishedPackage(i.root, automation.AUV)
	if err != nil {
		t.Fatal(err)
	}
	if !i.Ready(automation.AUV) {
		t.Fatal("published AUV incomplete")
	}
	binaryVersion, versionErr := i.executor.Run(context.Background(), filepath.Join(dir, "node_modules", "@auv-js", "cli-win32-x64-msvc", "bin", "auv.exe"), []string{"--version"}, nil, dir)
	if versionErr != nil || !strings.Contains(binaryVersion.Stdout, automation.AUVVersion) {
		t.Fatal("AUV binary version", binaryVersion, versionErr)
	}
	i.executor = failedComponentExecutor{}
	if err = i.Repair(context.Background(), automation.AUV, report); err == nil {
		t.Fatal("failed repair succeeded")
	}
	after, _ := automation.PublishedPackage(i.root, automation.AUV)
	if after != dir || !i.Ready(automation.AUV) {
		t.Fatal("failed repair replaced usable installation")
	}
	binary := filepath.Join(dir, "node_modules", "@auv-js", "cli-win32-x64-msvc", "bin", "auv.exe")
	if err = os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	if i.Ready(automation.AUV) {
		t.Fatal("missing executable still ready")
	}
	i.executor = NewCommandExecutor()
	if err = i.Prepare(context.Background(), automation.AUV, report); err != nil {
		t.Fatal("missing executable was not repaired", err)
	}
	if !i.Ready(automation.AUV) {
		t.Fatal("AUV recovery incomplete")
	}
}

func TestLiveDriverStagedPublicationAndSignatureFailureRecovery(t *testing.T) {
	archive := os.Getenv("DSH_WORK_DRIVER_ARCHIVE_TEST")
	if archive == "" {
		t.Skip("explicit verified driver archive validation")
	}
	i := NewAutomationPreparer(t.TempDir())
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	ownedArchive := filepath.Join(i.root, "automation", "downloads", automation.DriverArchive)
	if err = os.MkdirAll(filepath.Dir(ownedArchive), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(ownedArchive, data, 0600); err != nil {
		t.Fatal(err)
	}
	report := func(automation.Progress) {}
	if err = i.driver(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	for _, row := range i.Inventory() {
		if row.ID == "cua-driver" && row.InstalledVersion != automation.DriverVersion {
			t.Fatal("driver self-reported version", row)
		}
	}
	receipt := filepath.Join(i.driverRoot(), "release.json")
	if err = os.WriteFile(receipt, []byte("force staged replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = i.driver(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	previous, err := filepath.Glob(filepath.Join(filepath.Dir(i.driverRoot()), ".driver-previous-*"))
	if err != nil || len(previous) != 1 {
		t.Fatal("previous complete driver not retained", previous, err)
	}
	i.signature = func(string, string) error { return os.ErrPermission }
	if err = i.driver(context.Background(), report); err == nil {
		t.Fatal("unverified staged driver published")
	}
	if !i.driverReady() {
		t.Fatal("signature failure removed previous published driver")
	}
	b, err := os.ReadFile(receipt)
	if err != nil || string(b) != automation.DriverSHA256 {
		t.Fatal("failed verification changed published receipt", err)
	}
	olderLocation := filepath.Join(i.root, "automation", "cua-driver", "0.27.0", "windows-amd64")
	if err = os.MkdirAll(filepath.Dir(olderLocation), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(i.driverRoot(), olderLocation); err != nil {
		t.Fatal(err)
	}
	for _, row := range i.Inventory() {
		if row.ID == "cua-driver" && (row.InstalledVersion != automation.DriverVersion || row.Location != olderLocation) {
			t.Fatal("older managed location hid actual executable version", row)
		}
	}
}
func TestAutomationDriverSignatureFailureDoesNotPublish(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "automation", "cua-driver", automation.DriverVersion, "windows-amd64")
	os.MkdirAll(destination, 0700)
	os.WriteFile(filepath.Join(destination, "release.json"), []byte(automation.DriverSHA256), 0600)
	i := NewAutomationPreparer(root)
	i.signature = func(string, string) error { return os.ErrPermission }
	i.downloader = &nodeArchiveDownloaderFake{data: []byte("fixture-invalid-archive")}
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

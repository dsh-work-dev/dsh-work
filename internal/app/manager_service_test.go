package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestProfileImportUsesCurrentDirectoryAndExportUsesNativeSelection(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	defer f.close()
	f.startReady(t)
	snapshot, err := f.manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	current := snapshot.Current.Profile
	var home string
	for _, directory := range snapshot.DataDirectories {
		if directory.ID == current.DataDirectoryID {
			home = directory.Path
		}
	}
	if err := os.WriteFile(filepath.Join(home, "profiles", current.Name, "package.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	service := NewManagerService(f.manager)
	ctx := context.WithValue(context.Background(), application.WindowKey, startupTestWindow{name: "settings"})
	archive := filepath.Join(t.TempDir(), "export.zip")
	SetProfileExportAction(service, func(string) (string, error) { return archive, nil })
	if file, err := service.ExportProfile(ctx, current); err != nil || file != "export.zip" {
		t.Fatalf("export = %q, %v", file, err)
	}
	SetBackupFileActions(service, func() (string, error) { return archive, nil }, nil)
	result, err := service.ImportProfileBackup(ctx)
	if err != nil || result == nil || result.Profile.DataDirectoryID != current.DataDirectoryID || result.Profile == current {
		t.Fatalf("import = %#v, %v", result, err)
	}
	SetProfileExportAction(service, func(string) (string, error) { return "", nil })
	if file, err := service.ExportProfile(ctx, current); err != nil || file != "" {
		t.Fatalf("cancelled picker = %q, %v", file, err)
	}
}

type startupTestWindow struct {
	application.Window
	name string
}

func (w startupTestWindow) Name() string { return w.name }

func TestRuntimeSurfaceRequiresStartupOriginTrust(t *testing.T) {
	trusted := true
	service := NewManagerServiceWithRuntimeProgress(nil, nil, nil, func() bool { return trusted })
	ctx := context.WithValue(context.Background(), application.WindowKey, startupTestWindow{name: "workspace"})
	if !service.runtimeSurfaceAuthorized(ctx) {
		t.Fatal("local startup shell rejected")
	}
	trusted = false
	if service.runtimeSurfaceAuthorized(ctx) {
		t.Fatal("DSH page retained runtime management access after navigation")
	}
	ctx = context.WithValue(context.Background(), application.WindowKey, startupTestWindow{name: "settings"})
	if !service.runtimeSurfaceAuthorized(ctx) {
		t.Fatal("Settings access rejected")
	}
	if service.runtimeSurfaceAuthorized(context.Background()) {
		t.Fatal("missing window accepted")
	}
}

func TestManagerServiceRequiresTheSettingsWindow(t *testing.T) {
	root := t.TempDir()
	manager, err := dshmanager.New(dshmanager.Config{
		StatePath: filepath.Join(root, "manager.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewManagerService(manager).GetSnapshot(context.Background())
	assertServiceFailureCode(t, err, lifecycle.ErrorTrustedSurfaceRequired)
}

func TestHostServiceRefusesControlsFromAnUntrustedSurface(t *testing.T) {
	status := NewHostService(nil, func() bool { return true }, nil).GetStatus(context.Background())
	if status.Error == nil || status.Error.Code != lifecycle.ErrorTrustedSurfaceRequired {
		t.Fatalf("untrusted Host status = %+v", status)
	}
}

func TestManagerServicePublishesOneTerminalPreparation(t *testing.T) {
	var preparations []acquisition.OperationStatus
	service := &ManagerService{publishAcquisition: func(preparation acquisition.OperationStatus) {
		preparations = append(preparations, preparation)
	}}
	operationID := "test-operation"
	artifact := acquisition.ArtifactIdentity{Kind: acquisition.ArtifactNode, Name: "node", Version: "v24.20.0"}
	var last lifecycle.RuntimePreparation
	service.publishObservedPreparation(operationID, artifact, &last, lifecycle.RuntimePreparation{State: lifecycle.RuntimePreparationInstalled, Source: lifecycle.RuntimePreparationSourceOfficial})
	if len(preparations) != 0 {
		t.Fatalf("adapter terminal preparation leaked before Manager completion: %#v", preparations)
	}
	service.publishTerminalPreparation(operationID, artifact, "v24.20.0", last, lifecycle.Failure{Code: lifecycle.ErrorCancelled, Summary: "cancelled"})
	if len(preparations) != 1 || preparations[0].State != acquisition.OperationCanceled || preparations[0].CanCancel || preparations[0].OperationID != operationID || preparations[0].Artifact.Kind != acquisition.ArtifactNode || preparations[0].Result == nil {
		t.Fatalf("terminal preparations = %#v", preparations)
	}
}

func TestManagerServiceKeepsNestedNodeFailureOnNodeArtifact(t *testing.T) {
	var status acquisition.OperationStatus
	service := &ManagerService{publishAcquisition: func(value acquisition.OperationStatus) { status = value }}
	service.publishTerminalPreparation(
		"install-dsh",
		acquisition.ArtifactIdentity{Kind: acquisition.ArtifactDSH, Name: "@deepseek-ai/dsh", Version: "2.4.6"},
		"2.4.6",
		lifecycle.RuntimePreparation{
			State:         lifecycle.RuntimePreparationAcquiringNode,
			Operation:     lifecycle.RuntimePreparationOperationDownloadNode,
			TargetVersion: "v24.20.0",
			Source:        lifecycle.RuntimePreparationSourceOfficial,
		},
		lifecycle.Failure{Code: lifecycle.ErrorRuntimeInstallFailed, Summary: "Node installation failed", Retryable: true},
	)
	if status.Artifact.Kind != acquisition.ArtifactNode || status.Artifact.Version != "v24.20.0" || status.State != acquisition.OperationFailed {
		t.Fatalf("nested Node terminal status = %#v", status)
	}
}

func TestManagerServiceProjectsArtifactRouteAttemptAndProgress(t *testing.T) {
	var status acquisition.OperationStatus
	service := &ManagerService{publishAcquisition: func(value acquisition.OperationStatus) { status = value }}
	service.publishOperationStatus("install-dsh", acquisition.ArtifactIdentity{Kind: acquisition.ArtifactDSH, Name: "@deepseek-ai/dsh", Version: "2.4.6"}, lifecycle.RuntimePreparation{
		State: lifecycle.RuntimePreparationAcquiringNode, Operation: lifecycle.RuntimePreparationOperationDownloadNode,
		TargetVersion: "v24.20.0", Source: lifecycle.RuntimePreparationSourceMirror,
		ReceivedBytes: 10, TotalBytes: 20, HasTotal: true, CanCancel: true,
	})
	if status.OperationID != "install-dsh" || status.Artifact.Kind != acquisition.ArtifactNode || status.Artifact.Version != "v24.20.0" || status.Route != acquisition.RouteMirror || status.Attempt != 2 || status.ReceivedBytes != 10 || !status.CanCancel {
		t.Fatalf("acquisition projection = %#v", status)
	}
}

func assertServiceFailureCode(t *testing.T, err error, want lifecycle.ErrorCode) {
	t.Helper()
	var failure lifecycle.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("error %v is not a lifecycle failure", err)
	}
	if failure.Code != want {
		t.Fatalf("failure code = %s, want %s", failure.Code, want)
	}
}

package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
)

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

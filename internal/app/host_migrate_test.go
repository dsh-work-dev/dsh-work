package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
)

type appMigrationTestManager struct {
	*dshmanager.Manager
	calls   int
	launch  dshmanager.ResolvedLaunch
	request dshmanager.AppProfileMigrationRequest
	report  dshmanager.AppProfileMigrationReport
}

func (m *appMigrationTestManager) MigrateIntoAppProfile(_ context.Context, launch dshmanager.ResolvedLaunch, request dshmanager.AppProfileMigrationRequest) (dshmanager.AppProfileMigrationReport, error) {
	m.calls++
	m.launch = launch
	m.request = request
	return m.report, nil
}

type hostMigrationAppProfile struct{}

func (hostMigrationAppProfile) AppProfileBundles() []string { return nil }

func (hostMigrationAppProfile) InitializeAppProfile(profilePath string) error {
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		return err
	}
	manifest, err := json.Marshal(map[string]any{"name": "dsh-work-profile", "dependencies": map[string]string{}})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(profilePath, "package.json"), manifest, 0o600)
}

func TestHostMigrationRequiresCurrentAppProfile(t *testing.T) {
	fixture := newRunContextSwitchFixture(t)
	defer fixture.close()
	migrationManager := &appMigrationTestManager{Manager: fixture.manager}
	fixture.host.deps.Manager = migrationManager
	fixture.startReady(t)

	_, err := fixture.host.MigrateToAppProfile(context.Background(), dshmanager.AppProfileMigrationRequest{})
	if err == nil {
		t.Fatal("MigrateToAppProfile() error = nil, want busy failure outside DSH Work")
	}
	var failure *lifecycle.Failure
	if !errors.As(err, &failure) || failure == nil || failure.Code != lifecycle.ErrorManagerOperationBusy {
		t.Fatalf("MigrateToAppProfile() error = %v, want MANAGER_OPERATION_BUSY", err)
	}
	if migrationManager.calls != 0 {
		t.Fatalf("migration calls = %d, want 0", migrationManager.calls)
	}
}

func TestHostMigrationRunsOnceInsideReadyAppProfileSwitch(t *testing.T) {
	fixture := newRunContextSwitchFixture(t, func(config *dshmanager.Config) {
		managed := config.DataDirectories[0]
		managed.ID = "dsh-work"
		managed.Name = "DSH Work"
		config.DataDirectories = []dshmanager.DataDirectoryInfo{managed}
		config.DefaultRunContext = dshmanager.RunContext{
			RuntimeID: "dsh-alpha",
			Node:      dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem},
			Profile:   dshmanager.ProfileRef{DataDirectoryID: "dsh-work", Name: dshmanager.AppProfileName},
		}
		config.AppProfile = hostMigrationAppProfile{}
	})
	defer fixture.close()
	migrationManager := &appMigrationTestManager{
		Manager: fixture.manager,
		report:  dshmanager.AppProfileMigrationReport{Applied: []string{"patch"}},
	}
	fixture.host.deps.Manager = migrationManager
	fixture.startReady(t)
	request := dshmanager.AppProfileMigrationRequest{Source: dshmanager.ProfileRef{DataDirectoryID: "other", Name: "src"}, Patch: true}

	result, err := fixture.host.MigrateToAppProfile(context.Background(), request)
	if err != nil {
		t.Fatalf("MigrateToAppProfile() error = %v", err)
	}
	if migrationManager.calls != 1 || migrationManager.launch.Target.Profile != (dshmanager.ProfileRef{DataDirectoryID: "dsh-work", Name: dshmanager.AppProfileName}) || !reflect.DeepEqual(migrationManager.request, request) {
		t.Fatalf("migration calls/launch/request = %d / %#v / %#v", migrationManager.calls, migrationManager.launch.Target, migrationManager.request)
	}
	if len(result.Report.Applied) != 1 || result.Report.Applied[0] != "patch" {
		t.Fatalf("report = %#v, want migration result report", result.Report)
	}
	if result.Snapshot.Current == nil || result.Snapshot.Current.Profile != migrationManager.launch.Target.Profile {
		t.Fatalf("post-migration current context = %#v, want app profile", result.Snapshot.Current)
	}
	if fixture.supervisor.starts != 2 {
		t.Fatalf("Worker starts = %d, want one restart (2 total)", fixture.supervisor.starts)
	}
}

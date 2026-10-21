package dshmanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
)

type testAppProfileProvider struct {
	paths []string
	err   error
}

func (p *testAppProfileProvider) AppProfileBundles() []string {
	return []string{"@deepseek-ai/dsh-base", "@deepseek-ai/dsh-web-app"}
}

func (p *testAppProfileProvider) InitializeAppProfile(profilePath string) error {
	p.paths = append(p.paths, profilePath)
	if p.err != nil {
		return p.err
	}
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(profilePath, "package.json"), []byte(`{}`), 0o600)
}

func appProfileLaunch(directory DataDirectoryInfo) ResolvedLaunch {
	return ResolvedLaunch{
		DataDirectory: directory,
		Target: RunContext{
			RuntimeID: "dsh-test",
			Node:      NodeSelection{Kind: NodeSelectionSystem},
			Profile:   ProfileRef{DataDirectoryID: directory.ID, Name: AppProfileName},
		},
	}
}

func TestPrepareRunContextInitializesManagedAppProfile(t *testing.T) {
	m := newTestManager(t)
	provider := &testAppProfileProvider{}
	m.config.AppProfile = provider
	directory := m.config.DataDirectories[0]
	if err := m.PrepareRunContext(context.Background(), appProfileLaunch(directory)); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(directory.Path, "profiles", AppProfileName)
	if len(provider.paths) != 1 || provider.paths[0] != want {
		t.Fatalf("initialized paths = %v, want %s", provider.paths, want)
	}
}

func TestPrepareRunContextDoesNotInitializeOtherProfiles(t *testing.T) {
	m := newTestManager(t)
	provider := &testAppProfileProvider{}
	m.config.AppProfile = provider
	directory := m.config.DataDirectories[0]
	web := ResolvedLaunch{DataDirectory: directory, Target: RunContext{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: directory.ID, Name: "web"}}}
	if err := m.PrepareRunContext(context.Background(), web); err != nil {
		t.Fatal(err)
	}

	userPath := filepath.Join(t.TempDir(), "user-home")
	if err := os.MkdirAll(userPath, 0o700); err != nil {
		t.Fatal(err)
	}
	user := DataDirectoryInfo{ID: "user-home", Path: userPath, Ownership: DataDirectoryOwnershipUser}
	userProfile := ResolvedLaunch{DataDirectory: user, Target: RunContext{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: user.ID, Name: AppProfileName}}}
	if err := m.PrepareRunContext(context.Background(), userProfile); err != nil {
		t.Fatal(err)
	}
	if len(provider.paths) != 0 {
		t.Fatalf("initialized non-app profiles: %v", provider.paths)
	}
}

func TestResolveStartupLaunchInitializesManagedAppProfile(t *testing.T) {
	m := newTestManager(t)
	provider := &testAppProfileProvider{}
	m.config.AppProfile = provider
	launch, err := m.ResolveStartupLaunch(context.Background(), LaunchRequest{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: AppProfileName}})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(m.config.DataDirectories[0].Path, "profiles", AppProfileName)
	if len(provider.paths) != 1 || provider.paths[0] != want || launch.Target.Profile.Name != AppProfileName {
		t.Fatalf("launch=%+v initialized=%v, want path %s", launch, provider.paths, want)
	}
}

func TestPrepareRunContextRequiresAppProfileProvider(t *testing.T) {
	m := newTestManager(t)
	err := m.PrepareRunContext(context.Background(), appProfileLaunch(m.config.DataDirectories[0]))
	if err == nil {
		t.Fatal("PrepareRunContext succeeded without an app profile provider")
	}
	assertFailureCode(t, err, lifecycle.ErrorProfilePreparationFailed)
}

func TestSnapshotListsManagedAppProfileAsProtected(t *testing.T) {
	m := newTestManager(t)
	snapshot, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range snapshot.Profiles {
		if profile.Ref != (ProfileRef{DataDirectoryID: "dsh-work", Name: AppProfileName}) {
			continue
		}
		if profile.Exists || profile.Kind != ProfileKindApp || profile.Renamable || profile.Deletable || !profile.AutoInitialize {
			t.Fatalf("app profile info = %+v", profile)
		}
		return
	}
	t.Fatal("managed app profile was not listed")
}

func TestRenameProfileRejectsReservedAppProfileName(t *testing.T) {
	for _, name := range []string{AppProfileName, "DSH-Work"} {
		t.Run(name, func(t *testing.T) {
			m := newTestManager(t)
			_, err := m.RenameProfile(context.Background(), ProfileRenameRequest{
				Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web-clean"}, NewName: name,
			})
			if err == nil {
				t.Fatal("RenameProfile accepted a reserved app profile name")
			}
			assertFailureCode(t, err, lifecycle.ErrorProfileInvalid)
		})
	}
}

func TestCloneProfileInitializesMissingManagedAppProfile(t *testing.T) {
	m := newTestManager(t)
	provider := &testAppProfileProvider{}
	m.config.AppProfile = provider
	result, err := m.CloneProfile(context.Background(), ProfileCloneRequest{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: AppProfileName}})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(m.config.DataDirectories[0].Path, "profiles", AppProfileName)
	if len(provider.paths) != 1 || provider.paths[0] != want {
		t.Fatalf("initialized paths = %v, want %s", provider.paths, want)
	}
	if result.Profile.Name != AppProfileName+" 1" {
		t.Fatalf("clone name = %q", result.Profile.Name)
	}
}

package dshmanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type nodeCatalogFake struct {
	release NodeReleaseInfo
	calls   int
}

func (f *nodeCatalogFake) RefreshLatest(context.Context) (NodeReleaseInfo, error) {
	f.calls++
	return f.release, nil
}

type nodeInstallerFake struct {
	installation NodeInstallationInfo
	installCalls int
	removeCalls  int
	cancel       context.CancelFunc
}

type nodeResolverFake struct {
	selection NodeSelection
	resolved  ResolvedNode
}

type nodeRequiringRuntimeInstaller struct {
	path          string
	receivedNodes []NodeInstallationInfo
}

func (f *nodeRequiringRuntimeInstaller) Install(ctx context.Context, version string) (RuntimeInfo, error) {
	return f.InstallWithNodes(ctx, version, nil, nil)
}

func (*nodeRequiringRuntimeInstaller) RequiresNodeAcquisition(context.Context, []NodeInstallationInfo) (bool, error) {
	return true, nil
}

func (f *nodeRequiringRuntimeInstaller) InstallWithNodes(_ context.Context, version string, nodes []NodeInstallationInfo, _ RuntimeInstallObserver) (RuntimeInfo, error) {
	f.receivedNodes = cloneNodes(nodes)
	return RuntimeInfo{ID: "dsh-" + version, Version: version, Path: f.path, Source: RuntimeSourceManaged}, nil
}

func (f *nodeResolverFake) Resolve(_ context.Context, selection NodeSelection, _ []NodeInstallationInfo) (ResolvedNode, error) {
	f.selection = selection
	return f.resolved, nil
}

func (f *nodeInstallerFake) Install(context.Context, NodeReleaseInfo, RuntimeInstallObserver) (NodeInstallationInfo, error) {
	f.installCalls++
	if f.cancel != nil {
		f.cancel()
	}
	return f.installation, nil
}

func (f *nodeInstallerFake) Remove(context.Context, NodeInstallationInfo) error {
	f.removeCalls++
	return nil
}

func nodeFixture(t *testing.T) (NodeReleaseInfo, NodeInstallationInfo) {
	t.Helper()
	root := t.TempDir()
	nodePath := filepath.Join(root, "node.exe")
	npmPath := filepath.Join(root, "npm.cmd")
	for _, path := range []string{nodePath, npmPath} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	release := NodeReleaseInfo{
		Version: "v26.0.0", Platform: "windows", Architecture: "x64",
		Filename: "node-v26.0.0-win-x64.zip", SHA256: "abc123",
		Source: RuntimeArtifactSourceOfficial, ObservedAt: "2026-09-06T00:00:00Z",
	}
	installation := NodeInstallationInfo{
		ID: "node-v26.0.0-windows-x64", Version: release.Version,
		Platform: release.Platform, Architecture: release.Architecture,
		NodePath: nodePath, NPMPath: npmPath, Ownership: NodeOwnershipManaged,
		InstallSource: RuntimeArtifactSourceOfficial, SHA256: release.SHA256,
		Installed: true, Removable: true, Verified: true,
	}
	return release, installation
}

func TestManagerNodeCatalogRefreshIsExplicit(t *testing.T) {
	release, _ := nodeFixture(t)
	catalog := &nodeCatalogFake{release: release}
	manager, err := New(Config{StatePath: filepath.Join(t.TempDir(), "manager.json"), NodeCatalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if catalog.calls != 0 || snapshot.LatestNode != nil {
		t.Fatalf("Snapshot refreshed remote Node metadata: calls=%d latest=%#v", catalog.calls, snapshot.LatestNode)
	}
	snapshot, err = manager.RefreshLatestNode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if catalog.calls != 1 || snapshot.LatestNode == nil || snapshot.LatestNode.Version != release.Version {
		t.Fatalf("explicit refresh result: calls=%d latest=%#v", catalog.calls, snapshot.LatestNode)
	}
}

func TestSnapshotIncludesLocalSystemNodeObservationWithoutAcquisition(t *testing.T) {
	resolver := &nodeResolverFake{resolved: ResolvedNode{Version: "23.4.5", NodePath: `C:\Program Files\nodejs\node.exe`}}
	manager, err := New(Config{StatePath: filepath.Join(t.TempDir(), "manager.json"), NodeResolver: resolver})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SystemNode == nil || snapshot.SystemNode.Version != "23.4.5" || snapshot.SystemNode.Selection.Kind != NodeSelectionSystem {
		t.Fatalf("system Node observation = %#v", snapshot.SystemNode)
	}
}

func TestManagerInstallsLatestNodeWithoutSwitchingRunContext(t *testing.T) {
	release, installation := nodeFixture(t)
	catalog := &nodeCatalogFake{release: release}
	installer := &nodeInstallerFake{installation: installation}
	manager, err := New(Config{
		StatePath: filepath.Join(t.TempDir(), "manager.json"), NodeCatalog: catalog, NodeInstaller: installer,
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := manager.InstallLatestNode(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.InstallLatestNode(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.calls != 2 || installer.installCalls != 2 {
		t.Fatalf("latest resolution/install calls = %d/%d", catalog.calls, installer.installCalls)
	}
	if len(second.Nodes) != 1 || second.Nodes[0].ID != installation.ID {
		t.Fatalf("idempotent Node catalog = %#v", second.Nodes)
	}
	if first.Configured != nil || first.Current != nil || first.KnownGood != nil {
		t.Fatalf("Node download switched Run context: %#v", first)
	}
}

func TestManagerCleansNewNodeWhenCancellationArrivesAfterInstall(t *testing.T) {
	release, installation := nodeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	installer := &nodeInstallerFake{installation: installation, cancel: cancel}
	manager, err := New(Config{
		StatePath: filepath.Join(t.TempDir(), "manager.json"), NodeCatalog: &nodeCatalogFake{release: release}, NodeInstaller: installer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.InstallLatestNode(ctx, nil); err == nil {
		t.Fatal("InstallLatestNode() error = nil")
	}
	if installer.removeCalls != 1 {
		t.Fatalf("candidate cleanup calls = %d, want 1", installer.removeCalls)
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Nodes) != 0 {
		t.Fatalf("cancelled Node was registered: %#v", snapshot.Nodes)
	}
}

func TestManagerCleansNewNodeWhenCatalogPersistenceFails(t *testing.T) {
	release, installation := nodeFixture(t)
	installer := &nodeInstallerFake{installation: installation}
	manager, err := New(Config{
		StatePath: filepath.Join(t.TempDir(), "manager.json"), NodeCatalog: &nodeCatalogFake{release: release}, NodeInstaller: installer,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.store = failingStateStore{}
	if _, err := manager.InstallLatestNode(context.Background(), nil); err == nil {
		t.Fatal("InstallLatestNode() error = nil")
	}
	if installer.removeCalls != 1 {
		t.Fatalf("candidate cleanup calls = %d, want 1", installer.removeCalls)
	}
}

func TestExplicitDSHInstallAcquiresNodeOnlyWhenInstallerRequiresIt(t *testing.T) {
	release, installation := nodeFixture(t)
	root := t.TempDir()
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	nodeInstaller := &nodeInstallerFake{installation: installation}
	runtimeInstaller := &nodeRequiringRuntimeInstaller{path: runtimePath}
	manager, err := New(Config{
		StatePath:        filepath.Join(root, "manager.json"),
		DSHReleases:      []DSHReleaseInfo{{Version: "2.4.6", Source: RuntimeArtifactSourceOfficial, ObservedAt: "2026-09-06T00:00:00Z"}},
		RuntimeInstaller: runtimeInstaller, NodeCatalog: &nodeCatalogFake{release: release}, NodeInstaller: nodeInstaller,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.InstallRuntimeWithProgress(context.Background(), "2.4.6", nil)
	if err != nil {
		t.Fatal(err)
	}
	if nodeInstaller.installCalls != 1 || len(runtimeInstaller.receivedNodes) != 1 || runtimeInstaller.receivedNodes[0].ID != installation.ID {
		t.Fatalf("Node acquisition handoff: calls=%d nodes=%#v", nodeInstaller.installCalls, runtimeInstaller.receivedNodes)
	}
	if len(snapshot.Nodes) != 1 || len(snapshot.Runtimes) != 1 || snapshot.Configured != nil {
		t.Fatalf("independent installation snapshot = %#v", snapshot)
	}
}

func TestManagerRemovesOnlyManagedRemovableNode(t *testing.T) {
	_, installation := nodeFixture(t)
	installer := &nodeInstallerFake{installation: installation}
	manager, err := New(Config{
		StatePath: filepath.Join(t.TempDir(), "manager.json"), Nodes: []NodeInstallationInfo{installation}, NodeInstaller: installer,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.RemoveNode(context.Background(), installation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if installer.removeCalls != 1 || len(snapshot.Nodes) != 0 {
		t.Fatalf("remove result: calls=%d nodes=%#v", installer.removeCalls, snapshot.Nodes)
	}
}

func TestManagerResolveLaunchCarriesCompleteManagedNodeSelection(t *testing.T) {
	_, installation := nodeFixture(t)
	root := t.TempDir()
	runtimePath := filepath.Join(root, "dsh.cmd")
	profileRoot := filepath.Join(root, "dsh", "profiles", "web")
	if err := os.MkdirAll(profileRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	selection := NodeSelection{Kind: NodeSelectionManaged, InstallationID: installation.ID}
	resolver := &nodeResolverFake{resolved: ResolvedNode{
		Version: installation.Version, NodePath: installation.NodePath, NPMPath: installation.NPMPath,
		ChildEnvironment: map[string]string{"PATH": filepath.Dir(installation.NodePath)},
	}}
	manager, err := New(Config{
		StatePath: filepath.Join(root, "manager.json"), Nodes: []NodeInstallationInfo{installation}, NodeResolver: resolver,
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "1.2.3", Path: runtimePath}},
		DataDirectories: []DataDirectoryInfo{{ID: "dsh-work", Name: "dsh-work", Path: filepath.Join(root, "dsh"), Ownership: DataDirectoryOwnershipDSHWork}},
		ProfileCatalog:  testProfileCatalog{definitions: []ProfileDefinition{{Name: "web", Kind: ProfileKindBuiltIn}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := manager.ResolveLaunch(context.Background(), LaunchRequest{
		RuntimeID: "dsh-test", Node: selection, Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Target.Node != selection || resolved.Node.Selection != selection || resolver.selection != selection {
		t.Fatalf("resolved Node selection = target %#v node %#v resolver %#v", resolved.Target.Node, resolved.Node.Selection, resolver.selection)
	}
	if resolved.Node.ChildEnvironment["PATH"] != filepath.Dir(installation.NodePath) {
		t.Fatalf("resolved child environment = %#v", resolved.Node.ChildEnvironment)
	}
}

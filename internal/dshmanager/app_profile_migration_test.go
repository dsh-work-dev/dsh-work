package dshmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
)

type migrationTestRunner struct {
	calls     [][]string
	failMatch string
}

func (r *migrationTestRunner) Run(_ context.Context, _ string, args []string, _ map[string]string, _ string) (CommandResult, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if r.failMatch != "" && strings.Contains(strings.Join(args, " "), r.failMatch) {
		return CommandResult{}, errors.New("plugin install failed")
	}
	return CommandResult{}, nil
}

type appProfileMigrationFixture struct {
	manager    *Manager
	managed    DataDirectoryInfo
	user       DataDirectoryInfo
	sourcePath string
	targetPath string
	launch     ResolvedLaunch
	root       string
}

func newAppProfileMigrationFixture(t *testing.T, sourceDependencies, targetDependencies map[string]string, runner CommandRunner) appProfileMigrationFixture {
	t.Helper()
	root := t.TempDir()
	managed := DataDirectoryInfo{ID: "dsh-work", Name: "DSH Work", Path: filepath.Join(root, "managed"), Ownership: DataDirectoryOwnershipDSHWork}
	user := DataDirectoryInfo{ID: "user", Name: "User DSH", Path: filepath.Join(root, "user"), Ownership: DataDirectoryOwnershipUser}
	sourcePath := filepath.Join(user.Path, "profiles", "src")
	targetPath := filepath.Join(managed.Path, "profiles", AppProfileName)
	for _, path := range []string{sourcePath, targetPath} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeMigrationManifest(t, sourcePath, sourceDependencies)
	writeMigrationManifest(t, targetPath, targetDependencies)
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{
		StatePath:       filepath.Join(root, "state.json"),
		DataDirectories: []DataDirectoryInfo{managed, user},
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "1.0.0", Path: runtimePath, Installed: true}},
		CommandRunner:   runner,
		PluginCommands:  testPluginCommands{},
		ProfileReader:   FileProfileReader{},
	}
	manager, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return appProfileMigrationFixture{
		manager: manager, managed: managed, user: user, sourcePath: sourcePath, targetPath: targetPath, root: root,
		launch: ResolvedLaunch{
			Target:        RunContext{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: AppProfileName}},
			Runtime:       RuntimeInfo{ID: "dsh-test", Version: "1.0.0", Path: runtimePath, Installed: true},
			DataDirectory: managed,
		},
	}
}

func writeMigrationManifest(t *testing.T, path string, dependencies map[string]string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"name": "migration-fixture", "dependencies": dependencies})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "package.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeInstalledMigrationPackage(t *testing.T, profilePath, packageName, version string) {
	t.Helper()
	packagePath := filepath.Join(append([]string{profilePath, "node_modules"}, strings.Split(filepath.ToSlash(packageName), "/")...)...)
	if err := os.MkdirAll(packagePath, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]string{"name": packageName, "version": version})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packagePath, "package.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeMigrationFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeAppProfileMigrationListsInstalledRegistryPluginsPatchAndCredentials(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t,
		map[string]string{"@a/p": "^1.0.0", "@b/q": "^2.0.0", "@c/local": "file:../x"},
		map[string]string{"@b/q": "^2.0.0"}, nil)
	writeInstalledMigrationPackage(t, fixture.sourcePath, "@a/p", "1.2.0")
	writeInstalledMigrationPackage(t, fixture.sourcePath, "@b/q", "2.0.0")
	writeInstalledMigrationPackage(t, fixture.sourcePath, "@c/local", "1.0.0")
	writeInstalledMigrationPackage(t, fixture.targetPath, "@b/q", "2.0.0")
	writeMigrationFile(t, filepath.Join(fixture.sourcePath, "cordis.patch.yml"), "- id: x\n- id: y\n- name: z\n")
	writeMigrationFile(t, filepath.Join(fixture.targetPath, "cordis.patch.yml"), "- id: x\n")
	writeMigrationFile(t, filepath.Join(fixture.user.Path, ".credentials.yaml"), migrationSourceCredentials)
	writeMigrationFile(t, filepath.Join(fixture.managed.Path, ".credentials.yaml"), migrationTargetCredentials)

	analysis, err := fixture.manager.AnalyzeAppProfileMigration(context.Background(), AppProfileMigrationSource{Home: fixture.user.Path, Profile: "src"})
	if err != nil {
		t.Fatal(err)
	}
	want := AppProfileMigrationAnalysis{
		Home: canonicalImportPath(fixture.user.Path), Profiles: []string{"src"},
		Plugins: []AppProfileMigrationPlugin{{Package: "@a/p", Version: "1.2.0"}, {Package: "@b/q", Version: "2.0.0", AlreadyInstalled: true}},
		Data:    []AppProfileMigrationData{{Kind: "patch", Count: 3}, {Kind: "credentials", Count: 2}},
	}
	if !reflect.DeepEqual(analysis, want) {
		t.Fatalf("analysis = %#v, want %#v", analysis, want)
	}
}

func TestAnalyzeAppProfileMigrationRejectsDshWorkData(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	_, err := fixture.manager.AnalyzeAppProfileMigration(context.Background(), AppProfileMigrationSource{
		Home: fixture.managed.Path, Profile: "other",
	})
	assertFailureCode(t, err, lifecycle.ErrorProfileInvalid)
}
func TestMigrateIntoAppProfileMergesPatchNodesWithoutChangingExistingEntries(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	writeMigrationFile(t, filepath.Join(fixture.sourcePath, "cordis.patch.yml"), "- id: x\n- id: y\n- name: z\n")
	writeMigrationFile(t, filepath.Join(fixture.targetPath, "cordis.patch.yml"), "- id: x\n  config: {a: 1}\n- id: k\n  config: !!js \"1+1\"\n")
	report, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, AppProfileMigrationRequest{
		Source: AppProfileMigrationSource{Home: fixture.user.Path, Profile: "src"}, Patch: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Applied, []string{"patch"}) {
		t.Fatalf("applied = %#v, want [patch]", report.Applied)
	}
	patch, err := readPatchSequence(filepath.Join(fixture.targetPath, "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(patch.Content) != 4 || scalarValue(mappingValue(patch.Content[0], "id")) != "x" || scalarValue(mappingValue(patch.Content[1], "id")) != "k" || scalarValue(mappingValue(patch.Content[2], "id")) != "y" || scalarValue(mappingValue(patch.Content[3], "name")) != "z" {
		t.Fatalf("merged patch sequence = %#v", patch.Content)
	}
	if scalarValue(mappingValue(mappingValue(patch.Content[0], "config"), "a")) != "1" {
		t.Fatalf("existing x config changed: %#v", patch.Content[0])
	}
	config := mappingValue(patch.Content[1], "config")
	if config == nil || config.ShortTag() != "!!js" || config.Value != "1+1" {
		t.Fatalf("existing !!js tag not preserved: %#v", config)
	}
}

func TestMigrateIntoAppProfileWritesPatchAsBlockSequenceWithoutDuplicatingHeader(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	const patchHeader = "# Your patch layer for this dsh profile, applied after every bundle layer:\n# a top-level YAML array of loader patch entries (id-targeted config\n# overrides, disables, and insert lists; `!!js` expressions allowed).\n"
	writeMigrationFile(t, filepath.Join(fixture.sourcePath, "cordis.patch.yml"), patchHeader+"- id: migrated\n  config: !!js \"1+1\"\n")
	writeMigrationFile(t, filepath.Join(fixture.targetPath, "cordis.patch.yml"), patchHeader+"[]\n")
	report, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, AppProfileMigrationRequest{
		Source: AppProfileMigrationSource{Home: fixture.user.Path, Profile: "src"}, Patch: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Applied, []string{"patch"}) {
		t.Fatalf("applied = %#v, want [patch]", report.Applied)
	}
	data, err := os.ReadFile(filepath.Join(fixture.targetPath, "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "Your patch layer for this dsh profile"); got != 1 {
		t.Fatalf("patch header count = %d, want 1; contents:\n%s", got, data)
	}
	firstEntryLine := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			firstEntryLine = line
			break
		}
	}
	if firstEntryLine != "- id: migrated" {
		t.Fatalf("first patch content line = %q, want block sequence item; contents:\n%s", firstEntryLine, data)
	}
	patch, err := readPatchSequence(filepath.Join(fixture.targetPath, "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	config := mappingValue(patch.Content[0], "config")
	if config == nil || config.ShortTag() != "!!js" || config.Value != "1+1" {
		t.Fatalf("migrated !!js tag not preserved: %#v", config)
	}
}

const migrationSourceCredentials = `version: 1
refs:
  A: "1"
  B: "2"
records:
  x/k1:
    kind: api-key
    value: source-api-key
  client-connection/browser-session:
    kind: grant
    value: source-grant
`

const migrationTargetCredentials = `version: 1
refs:
  B: keep
records:
  client-connection/browser-session:
    kind: grant
    value: target-grant
`

func TestMigrateIntoAppProfileMergesCredentialEntriesWithoutOverwriting(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	writeMigrationFile(t, filepath.Join(fixture.user.Path, ".credentials.yaml"), migrationSourceCredentials)
	writeMigrationFile(t, filepath.Join(fixture.managed.Path, ".credentials.yaml"), migrationTargetCredentials)
	request := AppProfileMigrationRequest{Source: AppProfileMigrationSource{Home: fixture.user.Path, Profile: "src"}, Credentials: true}

	analysis, err := fixture.manager.AnalyzeAppProfileMigration(context.Background(), request.Source)
	if err != nil {
		t.Fatal(err)
	}
	wantData := []AppProfileMigrationData{{Kind: "credentials", Count: 2}}
	if !reflect.DeepEqual(analysis.Data, wantData) {
		t.Fatalf("analysis data = %#v, want %#v", analysis.Data, wantData)
	}

	report, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Applied, []string{"credentials"}) {
		t.Fatalf("applied = %#v, want [credentials]", report.Applied)
	}
	targetPath := filepath.Join(fixture.managed.Path, ".credentials.yaml")
	doc, refs, records, err := credentialEntries(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if scalarValue(refs["A"]) != "1" || scalarValue(refs["B"]) != "keep" {
		t.Fatalf("merged refs = %#v, want A=1 and existing B=keep", refs)
	}
	apiKey := records["x/k1"]
	if apiKey == nil || scalarValue(mappingValue(apiKey, "kind")) != "api-key" || scalarValue(mappingValue(apiKey, "value")) != "source-api-key" {
		t.Fatalf("api-key record was not migrated: %#v", apiKey)
	}
	root := credentialRoot(doc)
	grant := mappingValue(mappingValue(root, "records"), "client-connection/browser-session")
	if scalarValue(mappingValue(grant, "kind")) != "grant" || scalarValue(mappingValue(grant, "value")) != "target-grant" {
		t.Fatalf("target grant changed or was lost: %#v", grant)
	}
	if sourceGrant := records["client-connection/browser-session"]; sourceGrant != nil {
		t.Fatalf("source grant was exposed as migratable: %#v", sourceGrant)
	}

	analysis, err = fixture.manager.AnalyzeAppProfileMigration(context.Background(), request.Source)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(analysis.Data, []AppProfileMigrationData{{Kind: "credentials", Covered: true}}) {
		t.Fatalf("analysis after merge = %#v, want covered credentials", analysis.Data)
	}
	second, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Applied) != 0 || len(second.Failures) != 0 {
		t.Fatalf("covered credentials report = %#v, want no-op", second)
	}
}

func TestMigrateIntoAppProfileCreatesCredentialDocumentWhenTargetIsAbsent(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	writeMigrationFile(t, filepath.Join(fixture.user.Path, ".credentials.yaml"), migrationSourceCredentials)
	request := AppProfileMigrationRequest{Source: AppProfileMigrationSource{Home: fixture.user.Path, Profile: "src"}, Credentials: true}
	report, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Applied, []string{"credentials"}) {
		t.Fatalf("applied = %#v, want [credentials]", report.Applied)
	}
	doc, refs, records, err := credentialEntries(filepath.Join(fixture.managed.Path, ".credentials.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if scalarValue(mappingValue(credentialRoot(doc), "version")) != "1" || scalarValue(refs["A"]) != "1" || scalarValue(refs["B"]) != "2" {
		t.Fatalf("new credential document = %#v refs=%#v", doc, refs)
	}
	if len(records) != 1 || records["x/k1"] == nil || records["client-connection/browser-session"] != nil {
		t.Fatalf("new credential records = %#v, want only source api-key record", records)
	}
}

func TestMigrateIntoAppProfileUsesTwoSpaceYAMLIndentation(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	writeMigrationFile(t, filepath.Join(fixture.sourcePath, "cordis.patch.yml"), "- id: migrated\n  config:\n    nested:\n      value: keep\n")
	writeMigrationFile(t, filepath.Join(fixture.user.Path, ".credentials.yaml"), migrationSourceCredentials)
	request := AppProfileMigrationRequest{
		Source: AppProfileMigrationSource{Home: fixture.user.Path, Profile: "src"}, Patch: true, Credentials: true,
	}
	if _, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, request); err != nil {
		t.Fatal(err)
	}

	patch, err := os.ReadFile(filepath.Join(fixture.targetPath, "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patch), "\n  config:\n    nested:\n      value: keep\n") {
		t.Fatalf("patch is not encoded with two-space nesting:\n%s", patch)
	}
	credentials, err := os.ReadFile(filepath.Join(fixture.managed.Path, ".credentials.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(credentials), "\n  x/k1:\n    kind: api-key\n    value: source-api-key\n") {
		t.Fatalf("credentials are not encoded with two-space nesting:\n%s", credentials)
	}
}

func TestAnalyzeAppProfileMigrationOmitsUnsupportedCredentialVersion(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	writeMigrationFile(t, filepath.Join(fixture.user.Path, ".credentials.yaml"), "version: 2\nrefs:\n  A: value\n")
	analysis, err := fixture.manager.AnalyzeAppProfileMigration(context.Background(), AppProfileMigrationSource{Home: fixture.user.Path, Profile: "src"})
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Data) != 0 {
		t.Fatalf("unsupported credentials version listed for migration: %#v", analysis.Data)
	}
}

func TestMigrateIntoAppProfileInstallsRequestedPluginsIndependently(t *testing.T) {
	runner := &migrationTestRunner{failMatch: "@b/fail@1.0.0"}
	fixture := newAppProfileMigrationFixture(t,
		map[string]string{"@a/p": "^1.0.0", "@b/fail": "^1.0.0", "@b/q": "^2.0.0"},
		map[string]string{"@b/q": "^2.0.0"}, runner)
	writeInstalledMigrationPackage(t, fixture.sourcePath, "@a/p", "1.2.0")
	writeInstalledMigrationPackage(t, fixture.sourcePath, "@b/fail", "1.0.0")
	writeInstalledMigrationPackage(t, fixture.sourcePath, "@b/q", "2.0.0")
	writeInstalledMigrationPackage(t, fixture.targetPath, "@b/q", "2.0.0")
	request := AppProfileMigrationRequest{Source: AppProfileMigrationSource{Home: fixture.user.Path, Profile: "src"}, Plugins: []string{"@b/fail", "@a/p", "@b/q"}}
	report, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Applied, []string{"@a/p"}) || len(report.Failures) != 1 || report.Failures[0].Item != "@b/fail" || report.Failures[0].Message == "" {
		t.Fatalf("migration report = %#v", report)
	}
	if len(runner.calls) != 2 || strings.Contains(strings.Join(runner.calls[0], " ")+strings.Join(runner.calls[1], " "), "@b/q") {
		t.Fatalf("plugin command calls = %#v", runner.calls)
	}
}

func TestMigrateIntoAppProfileIgnoresPluginsOutsideAnalysis(t *testing.T) {
	runner := &migrationTestRunner{}
	fixture := newAppProfileMigrationFixture(t, map[string]string{"@a/p": "^1.0.0"}, nil, runner)
	writeInstalledMigrationPackage(t, fixture.sourcePath, "@a/p", "1.2.0")
	report, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, AppProfileMigrationRequest{
		Source: AppProfileMigrationSource{Home: fixture.user.Path, Profile: "src"}, Plugins: []string{"@unknown/not-listed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 0 || len(report.Applied) != 0 || len(report.Failures) != 0 {
		t.Fatalf("unexpected migration for unlisted package: calls=%#v report=%#v", runner.calls, report)
	}
}

func workspaceFixture(global, workspaces map[string]any) map[string]any {
	return map[string]any{
		"unit":        map[string]any{"name": "workspace", "version": 2},
		"global":      global,
		"tables":      map[string]any{"workspaces": workspaces},
		"fixtureRoot": map[string]any{"kept": true},
	}
}

func writeWorkspaceFixture(t *testing.T, path string, document map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

func readWorkspaceFixture(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func makeSessionFixture(t *testing.T, root, project, session, fileName, contents string) string {
	t.Helper()
	path := filepath.Join(root, project, session, fileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnalyzeAppProfileMigrationCountsSessionsAndWorkspaceChangesWithoutProfile(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	writeMigrationManifest(t, fixture.sourcePath, map[string]string{"@example/plugin": "^1.0.0"})
	writeInstalledMigrationPackage(t, fixture.sourcePath, "@example/plugin", "1.0.0")
	writeMigrationFile(t, filepath.Join(fixture.sourcePath, "cordis.patch.yml"), "- id: source-patch\n")

	sourceSessions := filepath.Join(fixture.user.Path, "sessions")
	for _, session := range []string{"one", "two", "three"} {
		makeSessionFixture(t, sourceSessions, "project-key", session, "session.jsonl", session)
	}
	if err := os.MkdirAll(filepath.Join(fixture.managed.Path, "sessions", "project-key", "two"), 0o700); err != nil {
		t.Fatal(err)
	}

	sourceWorkspace := workspaceFixture(
		map[string]any{"workspaceIds": []string{"source-same", "source-new"}},
		map[string]any{
			"source-same": map[string]any{"path": filepath.Join(fixture.root, "same"), "sessionIds": []string{"old", "new"}, "title": "source title"},
			"source-new":  map[string]any{"path": filepath.Join(fixture.root, "new"), "sessionIds": []string{"new-session"}},
		},
	)
	targetWorkspace := workspaceFixture(
		map[string]any{"workspaceIds": []string{"target-same"}},
		map[string]any{
			"target-same": map[string]any{"path": filepath.Join(fixture.root, "same"), "sessionIds": []string{"old"}, "title": "target title"},
		},
	)
	writeWorkspaceFixture(t, filepath.Join(fixture.user.Path, "storages", "workspace.json"), sourceWorkspace)
	writeWorkspaceFixture(t, filepath.Join(fixture.managed.Path, "storages", "workspace.json"), targetWorkspace)

	analysis, err := fixture.manager.AnalyzeAppProfileMigration(context.Background(), AppProfileMigrationSource{
		Home: fixture.user.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Home != canonicalImportPath(fixture.user.Path) || !reflect.DeepEqual(analysis.Profiles, []string{"src"}) {
		t.Fatalf("source identity = Home %q Profiles %#v", analysis.Home, analysis.Profiles)
	}
	if len(analysis.Plugins) != 0 {
		t.Fatalf("profile-less analysis listed plugins: %#v", analysis.Plugins)
	}
	wantData := []AppProfileMigrationData{{Kind: "sessions", Count: 2}, {Kind: "workspaces", Count: 2}}
	if !reflect.DeepEqual(analysis.Data, wantData) {
		t.Fatalf("analysis data = %#v, want %#v", analysis.Data, wantData)
	}
}

func TestMigrateIntoAppProfileCopiesSessionsProjectionCacheAndAttachments(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	sourceSessionRoot := filepath.Join(fixture.user.Path, "sessions")
	newSessionFile := makeSessionFixture(t, sourceSessionRoot, "project-key", "new-session", "nested/session.jsonl", "new session bytes")
	existingSourceFile := makeSessionFixture(t, sourceSessionRoot, "project-key", "existing-session", "session.jsonl", "source version")
	existingTargetFile := filepath.Join(fixture.managed.Path, "sessions", "project-key", "existing-session", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(existingTargetFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existingTargetFile, []byte("target wins"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceProjection := filepath.Join(fixture.user.Path, "storages", "session_projcache", "sessions", "new-session.json")
	writeMigrationFile(t, sourceProjection, `{"record":{"rows":{"title":"Imported title"}}}`)
	existingProjection := filepath.Join(fixture.managed.Path, "storages", "session_projcache", "sessions", "existing-session.json")
	writeMigrationFile(t, existingProjection, "target projection")
	sourceAttachment := filepath.Join(fixture.user.Path, "attachments", "v1", "objects", "ab", "new-object")
	writeMigrationFile(t, sourceAttachment, "new attachment")
	writeMigrationFile(t, filepath.Join(fixture.user.Path, "attachments", "v1", "objects", "ab", "existing-object"), "source attachment")
	writeMigrationFile(t, filepath.Join(fixture.user.Path, "attachments", "v1", "tmp", "scratch"), "temporary attachment")
	targetAttachment := filepath.Join(fixture.managed.Path, "attachments", "v1", "objects", "ab", "existing-object")
	writeMigrationFile(t, targetAttachment, "target attachment")

	sourceFiles := []string{newSessionFile, existingSourceFile, sourceProjection, sourceAttachment}
	type sourceSnapshot struct {
		data    string
		modTime int64
	}
	before := make(map[string]sourceSnapshot, len(sourceFiles))
	for _, path := range sourceFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = sourceSnapshot{data: string(data), modTime: info.ModTime().UnixNano()}
	}

	report, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, AppProfileMigrationRequest{
		Source: AppProfileMigrationSource{Home: fixture.user.Path}, Sessions: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Applied, []string{"sessions"}) || len(report.Failures) != 0 {
		t.Fatalf("migration report = %#v", report)
	}
	if got, err := os.ReadFile(filepath.Join(fixture.managed.Path, "sessions", "project-key", "new-session", "nested", "session.jsonl")); err != nil || string(got) != "new session bytes" {
		t.Fatalf("new session file = %q, err=%v", got, err)
	}
	if got, err := os.ReadFile(existingTargetFile); err != nil || string(got) != "target wins" {
		t.Fatalf("existing session = %q, err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(fixture.managed.Path, "storages", "session_projcache", "sessions", "new-session.json")); err != nil || string(got) != `{"record":{"rows":{"title":"Imported title"}}}` {
		t.Fatalf("projection cache = %q, err=%v", got, err)
	}
	if got, err := os.ReadFile(existingProjection); err != nil || string(got) != "target projection" {
		t.Fatalf("existing projection cache = %q, err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(fixture.managed.Path, "attachments", "v1", "objects", "ab", "new-object")); err != nil || string(got) != "new attachment" {
		t.Fatalf("new attachment = %q, err=%v", got, err)
	}
	if got, err := os.ReadFile(targetAttachment); err != nil || string(got) != "target attachment" {
		t.Fatalf("existing attachment = %q, err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(fixture.managed.Path, "attachments", "v1", "tmp", "scratch")); !os.IsNotExist(err) {
		t.Fatalf("top-level attachments/tmp was copied, err=%v", err)
	}
	if err := filepath.WalkDir(filepath.Join(fixture.managed.Path, "sessions"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".dsh-work-import-") {
			t.Errorf("session import staging path remains: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for path, expected := range before {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != expected.data || info.ModTime().UnixNano() != expected.modTime {
			t.Errorf("source changed during import: %s", path)
		}
	}
	if runtime.GOOS != "windows" {
		for _, item := range []string{
			filepath.Join(fixture.managed.Path, "sessions", "project-key", "new-session", "nested", "session.jsonl"),
			filepath.Join(fixture.managed.Path, "storages", "session_projcache", "sessions", "new-session.json"),
			filepath.Join(fixture.managed.Path, "attachments", "v1", "objects", "ab", "new-object"),
		} {
			info, err := os.Stat(item)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("new imported file %s mode = %o, want 600", item, info.Mode().Perm())
			}
		}
	}
}

func TestMigrateIntoAppProfileRecoversInterruptedImportResidue(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	sourceSession := makeSessionFixture(t, filepath.Join(fixture.user.Path, "sessions"), "project-key", "new-session", "session.jsonl", "source session")
	stagingPath := filepath.Join(fixture.managed.Path, "sessions", "project-key", ".dsh-work-import-new-session")
	writeMigrationFile(t, filepath.Join(stagingPath, "session.jsonl"), "partial session")

	sourceWorkspacePath := filepath.Join(fixture.user.Path, "storages", "workspace.json")
	targetWorkspacePath := filepath.Join(fixture.managed.Path, "storages", "workspace.json")
	writeWorkspaceFixture(t, sourceWorkspacePath, workspaceFixture(
		map[string]any{"workspaceIds": []string{"source-workspace"}},
		map[string]any{"source-workspace": map[string]any{"path": filepath.Join(fixture.root, "project"), "sessionIds": []string{"new-session"}}},
	))
	legacyWorkspaceTemporary := targetWorkspacePath + ".dsh-work-tmp"
	writeMigrationFile(t, legacyWorkspaceTemporary, "partial workspace")

	sourceAttachmentPath := filepath.Join(fixture.user.Path, "attachments", "v1", "objects", "ab", "new-attachment")
	targetAttachmentPath := filepath.Join(fixture.managed.Path, "attachments", "v1", "objects", "ab", "new-attachment")
	writeMigrationFile(t, sourceAttachmentPath, "source attachment")
	legacyAttachmentTemporary := targetAttachmentPath + ".dsh-work-tmp"
	writeMigrationFile(t, legacyAttachmentTemporary, "partial attachment")

	report, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, AppProfileMigrationRequest{
		Source: AppProfileMigrationSource{Home: fixture.user.Path}, Sessions: true, Workspaces: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Applied, []string{"workspaces", "sessions"}) || len(report.Failures) != 0 {
		t.Fatalf("migration report = %#v", report)
	}
	if got, err := os.ReadFile(filepath.Join(fixture.managed.Path, "sessions", "project-key", "new-session", "session.jsonl")); err != nil || string(got) != "source session" {
		t.Fatalf("imported session = %q, err=%v", got, err)
	}
	if got, err := os.ReadFile(sourceSession); err != nil || string(got) != "source session" {
		t.Fatalf("source session = %q, err=%v", got, err)
	}
	for _, stalePath := range []string{stagingPath, legacyWorkspaceTemporary, legacyAttachmentTemporary} {
		if _, err := os.Lstat(stalePath); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("interrupted import residue remains at %s (err=%v)", stalePath, err)
		}
	}
	if err := filepath.WalkDir(filepath.Join(fixture.managed.Path, "sessions"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".dsh-work-import-") {
			t.Errorf("session import staging path remains: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, targetPath := range []string{targetWorkspacePath, targetAttachmentPath} {
		matches, err := filepath.Glob(targetPath + ".dsh-work-tmp-*")
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Errorf("temporary files remain beside %s: %#v", targetPath, matches)
		}
	}
	if got, err := os.ReadFile(targetWorkspacePath); err != nil || len(got) == 0 {
		t.Fatalf("imported workspace = %q, err=%v", got, err)
	}
	if got, err := os.ReadFile(targetAttachmentPath); err != nil || string(got) != "source attachment" {
		t.Fatalf("imported attachment = %q, err=%v", got, err)
	}
}

func TestMigrateIntoAppProfileSkipsSessionContainingSymlink(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	sourceSession := filepath.Join(fixture.user.Path, "sessions", "project-key", "linked-session")
	if err := os.MkdirAll(sourceSession, 0o700); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(fixture.root, "linked-target")
	writeMigrationFile(t, targetFile, "target")
	if err := os.Symlink(targetFile, filepath.Join(sourceSession, "link")); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	report, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, AppProfileMigrationRequest{
		Source: AppProfileMigrationSource{Home: fixture.user.Path}, Sessions: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Applied) != 0 || len(report.Failures) != 1 ||
		report.Failures[0].Item != "sessions" || !strings.Contains(report.Failures[0].Message, "linked-session") {
		t.Fatalf("migration report = %#v", report)
	}
	if _, err := os.Lstat(filepath.Join(fixture.managed.Path, "sessions", "project-key", "linked-session")); !os.IsNotExist(err) {
		t.Fatalf("session with symlink was partially imported: err=%v", err)
	}
}

func TestMigrateIntoAppProfileMergesWorkspacesAndPreservesTargetFields(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	targetSamePath := `c:\SAME`
	if runtime.GOOS != "windows" {
		// Path case folding is platform-specific; POSIX paths are case-sensitive.
		targetSamePath = `C:\same`
	}
	sourceDocument := workspaceFixture(
		map[string]any{
			"workspaceIds":       []string{"source-same", "collision"},
			"archivedSessionIds": []string{"archived-source", "shared"},
			"pinnedSessionIds":   []string{"pinned-source"},
			"defaultWorkspaceId": "source-default",
		},
		map[string]any{
			"source-same": map[string]any{"path": `C:\same`, "sessionIds": []string{"existing-session", "new-session"}, "title": "source title"},
			"collision":   map[string]any{"path": `C:\new`, "sessionIds": []string{"new-workspace-session"}, "sourceUnknown": "kept"},
		},
	)
	targetDocument := workspaceFixture(
		map[string]any{
			"workspaceIds":        []string{"target-same", "collision"},
			"archivedSessionIds":  []string{"archived-target", "shared"},
			"pinnedSessionIds":    []string{"pinned-target"},
			"defaultWorkspaceId":  "target-default",
			"targetGlobalUnknown": "keep",
		},
		map[string]any{
			"target-same": map[string]any{"path": targetSamePath, "sessionIds": []string{"existing-session"}, "title": "target title", "targetUnknown": true},
			"collision":   map[string]any{"path": `C:\other`, "sessionIds": []string{"other-session"}},
		},
	)
	sourcePath := filepath.Join(fixture.user.Path, "storages", "workspace.json")
	targetPath := filepath.Join(fixture.managed.Path, "storages", "workspace.json")
	sourceBytes := writeWorkspaceFixture(t, sourcePath, sourceDocument)
	writeWorkspaceFixture(t, targetPath, targetDocument)

	source := AppProfileMigrationSource{Home: fixture.user.Path}
	analysis, err := fixture.manager.AnalyzeAppProfileMigration(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(analysis.Data, []AppProfileMigrationData{{Kind: "workspaces", Count: 2}}) {
		t.Fatalf("workspace analysis = %#v", analysis.Data)
	}
	report, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, AppProfileMigrationRequest{
		Source: source, Workspaces: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Applied, []string{"workspaces"}) || len(report.Failures) != 0 {
		t.Fatalf("migration report = %#v", report)
	}
	target := readWorkspaceFixture(t, targetPath)
	targetGlobal := target["global"].(map[string]any)
	if targetGlobal["defaultWorkspaceId"] != "target-default" || targetGlobal["targetGlobalUnknown"] != "keep" {
		t.Fatalf("target global fields changed: %#v", targetGlobal)
	}
	if got := stringValues(targetGlobal["archivedSessionIds"]); !reflect.DeepEqual(got, []string{"archived-target", "shared", "archived-source"}) {
		t.Fatalf("archived session IDs = %#v", got)
	}
	if got := stringValues(targetGlobal["pinnedSessionIds"]); !reflect.DeepEqual(got, []string{"pinned-target", "pinned-source"}) {
		t.Fatalf("pinned session IDs = %#v", got)
	}
	workspaces := workspaceRecords(target)
	matched := workspaces["target-same"].(map[string]any)
	if got := stringValues(matched["sessionIds"]); !reflect.DeepEqual(got, []string{"existing-session", "new-session"}) ||
		matched["title"] != "target title" || matched["targetUnknown"] != true {
		t.Fatalf("same-path workspace was not merged into target record: %#v", matched)
	}
	newID := ""
	for id, value := range workspaces {
		path, _ := workspacePath(value)
		if path == `C:\new` {
			newID = id
		}
	}
	if newID == "" || newID == "collision" {
		t.Fatalf("new workspace ID = %q, want a non-conflicting ID", newID)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(newID) {
		t.Fatalf("generated workspace ID is not UUIDv4: %q", newID)
	}
	if got := stringValues(targetGlobal["workspaceIds"]); !reflect.DeepEqual(got, []string{"target-same", "collision", newID}) {
		t.Fatalf("workspace IDs = %#v, want generated workspace appended", got)
	}
	newWorkspace := workspaces[newID].(map[string]any)
	if newWorkspace["sourceUnknown"] != "kept" {
		t.Fatalf("new source workspace lost unknown fields: %#v", newWorkspace)
	}
	if target["fixtureRoot"].(map[string]any)["kept"] != true {
		t.Fatalf("unknown target root field lost: %#v", target["fixtureRoot"])
	}
	if data, err := os.ReadFile(sourcePath); err != nil || string(data) != string(sourceBytes) {
		t.Fatalf("source workspace changed: err=%v", err)
	}

	analysis, err = fixture.manager.AnalyzeAppProfileMigration(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(analysis.Data, []AppProfileMigrationData{{Kind: "workspaces", Covered: true}}) {
		t.Fatalf("workspace analysis after import = %#v, want covered", analysis.Data)
	}
	before, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, AppProfileMigrationRequest{
		Source: source, Workspaces: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Applied) != 0 || len(second.Failures) != 0 || string(before) != string(after) {
		t.Fatalf("repeat workspace import modified target: report=%#v", second)
	}
}

func TestMigrateIntoAppProfileCopiesWorkspaceBytesWhenTargetIsAbsent(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	sourcePath := filepath.Join(fixture.user.Path, "storages", "workspace.json")
	targetPath := filepath.Join(fixture.managed.Path, "storages", "workspace.json")
	const raw = "{\n  \"unit\": {\"name\": \"workspace\", \"version\": 2},\n  \"global\": {\"workspaceIds\": [\"w1\"]},\n  \"tables\": {\"workspaces\": {\"w1\": {\"path\": \"C:/project\", \"sessionIds\": []}}}\n}\n"
	writeMigrationFile(t, sourcePath, raw)
	report, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, AppProfileMigrationRequest{
		Source: AppProfileMigrationSource{Home: fixture.user.Path}, Workspaces: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Applied, []string{"workspaces"}) {
		t.Fatalf("applied = %#v", report.Applied)
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != raw {
		t.Fatalf("target bytes = %q, want source bytes %q", got, raw)
	}
}

func TestAnalyzeAppProfileMigrationIgnoresUnsupportedTargetWorkspaceUnit(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	source := workspaceFixture(map[string]any{"workspaceIds": []string{"w1"}}, map[string]any{
		"w1": map[string]any{"path": filepath.Join(fixture.root, "project"), "sessionIds": []string{}},
	})
	target := workspaceFixture(map[string]any{}, map[string]any{})
	target["unit"] = map[string]any{"name": "workspace", "version": 3}
	writeWorkspaceFixture(t, filepath.Join(fixture.user.Path, "storages", "workspace.json"), source)
	writeWorkspaceFixture(t, filepath.Join(fixture.managed.Path, "storages", "workspace.json"), target)

	analysis, err := fixture.manager.AnalyzeAppProfileMigration(context.Background(), AppProfileMigrationSource{Home: fixture.user.Path})
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Data) != 0 {
		t.Fatalf("unsupported target workspace unit was listed: %#v", analysis.Data)
	}
}

func TestMigrateIntoAppProfileImportsLegacyRedirectedData(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	legacyRoot := filepath.Join(fixture.root, "legacy")
	legacyHome := filepath.Join(legacyRoot, "environment")
	legacyProfile := filepath.Join(legacyHome, "profiles", "web")
	if err := os.MkdirAll(legacyProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	writeMigrationManifest(t, legacyProfile, nil)
	writeMigrationFile(t, filepath.Join(legacyProfile, "cordis.patch.yml"), "- id: legacy-patch\n")

	userData := filepath.Join(legacyRoot, "user-data")
	sessionsRoot := filepath.Join(userData, "sessions")
	makeSessionFixture(t, sessionsRoot, "legacy-project", "legacy-one", "session.jsonl", "one")
	makeSessionFixture(t, sessionsRoot, "legacy-project", "legacy-two", "session.jsonl", "two")
	writeWorkspaceFixture(t, filepath.Join(userData, "storages", "workspace.json"), workspaceFixture(
		map[string]any{"workspaceIds": []string{"legacy-workspace"}},
		map[string]any{"legacy-workspace": map[string]any{"path": filepath.Join(fixture.root, "legacy-project"), "sessionIds": []string{"legacy-one", "legacy-two"}}},
	))
	writeMigrationFile(t, filepath.Join(userData, ".credentials.yaml"), migrationSourceCredentials)
	writeMigrationFile(t, filepath.Join(userData, "attachments", "v1", "objects", "legacy-object"), "attachment")
	redirects := []map[string]any{
		{"id": "session-persistence-jsonl", "config": map[string]string{"root": sessionsRoot}},
		{"id": "storage-json", "config": map[string]string{"root": filepath.Join(userData, "storages")}},
		{"id": "attachment-local", "config": map[string]string{"dshHome": userData}},
		{"id": "credentials", "config": map[string]string{"path": filepath.Join(userData, ".credentials.yaml")}},
	}
	redirectBytes, err := json.Marshal(redirects)
	if err != nil {
		t.Fatal(err)
	}
	writeMigrationFile(t, filepath.Join(legacyHome, "user-data.patch.json"), string(redirectBytes))

	source := AppProfileMigrationSource{Home: legacyRoot, Profile: "web"}
	analysis, err := fixture.manager.AnalyzeAppProfileMigration(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Home != canonicalImportPath(legacyHome) || !reflect.DeepEqual(analysis.Profiles, []string{"web"}) {
		t.Fatalf("legacy source identity = %#v", analysis)
	}
	wantData := []AppProfileMigrationData{
		{Kind: "patch", Count: 1},
		{Kind: "sessions", Count: 2},
		{Kind: "workspaces", Count: 1},
		{Kind: "credentials", Count: 3},
	}
	if !reflect.DeepEqual(analysis.Data, wantData) {
		t.Fatalf("legacy analysis data = %#v, want %#v", analysis.Data, wantData)
	}
	report, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, AppProfileMigrationRequest{
		Source: source, Patch: true, Credentials: true, Workspaces: true, Sessions: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Applied, []string{"patch", "credentials", "workspaces", "sessions"}) || len(report.Failures) != 0 {
		t.Fatalf("legacy migration report = %#v", report)
	}
	for _, session := range []string{"legacy-one", "legacy-two"} {
		if _, err := os.Stat(filepath.Join(fixture.managed.Path, "sessions", "legacy-project", session, "session.jsonl")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(fixture.managed.Path, "attachments", "v1", "objects", "legacy-object")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(fixture.managed.Path, "storages", "workspace.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := credentialEntries(filepath.Join(fixture.managed.Path, ".credentials.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeAppProfileMigrationRejectsUnknownSourceProfile(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	_, err := fixture.manager.AnalyzeAppProfileMigration(context.Background(), AppProfileMigrationSource{
		Home: fixture.user.Path, Profile: "missing",
	})
	assertFailureCode(t, err, lifecycle.ErrorProfileNotFound)
}

func TestMigrateIntoAppProfileRepeatImportIsCoveredAndWritesNothing(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	writeMigrationFile(t, filepath.Join(fixture.sourcePath, "cordis.patch.yml"), "- id: repeat-patch\n")
	makeSessionFixture(t, filepath.Join(fixture.user.Path, "sessions"), "project", "session-one", "session.jsonl", "session")
	writeMigrationFile(t, filepath.Join(fixture.user.Path, "storages", "session_projcache", "sessions", "session-one.json"), `{"title":"Repeat"}`)
	writeMigrationFile(t, filepath.Join(fixture.user.Path, "attachments", "v1", "objects", "repeat"), "attachment")
	writeWorkspaceFixture(t, filepath.Join(fixture.user.Path, "storages", "workspace.json"), workspaceFixture(
		map[string]any{"workspaceIds": []string{"workspace-one"}},
		map[string]any{"workspace-one": map[string]any{"path": filepath.Join(fixture.root, "project"), "sessionIds": []string{"session-one"}}},
	))
	writeMigrationFile(t, filepath.Join(fixture.user.Path, ".credentials.yaml"), migrationSourceCredentials)

	source := AppProfileMigrationSource{Home: fixture.user.Path, Profile: "src"}
	request := AppProfileMigrationRequest{
		Source: source, Patch: true, Credentials: true, Workspaces: true, Sessions: true,
	}
	first, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, request)
	if err != nil {
		t.Fatal(err)
	}
	wantApplied := []string{"patch", "credentials", "workspaces", "sessions"}
	if !reflect.DeepEqual(first.Applied, wantApplied) || len(first.Failures) != 0 {
		t.Fatalf("first import report = %#v", first)
	}

	analysis, err := fixture.manager.AnalyzeAppProfileMigration(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	wantData := []AppProfileMigrationData{
		{Kind: "patch", Count: 1, Covered: true},
		{Kind: "sessions", Covered: true},
		{Kind: "workspaces", Covered: true},
		{Kind: "credentials", Covered: true},
	}
	if !reflect.DeepEqual(analysis.Data, wantData) {
		t.Fatalf("analysis after import = %#v, want %#v", analysis.Data, wantData)
	}

	targetFiles := []string{
		filepath.Join(fixture.targetPath, "cordis.patch.yml"),
		filepath.Join(fixture.managed.Path, ".credentials.yaml"),
		filepath.Join(fixture.managed.Path, "storages", "workspace.json"),
		filepath.Join(fixture.managed.Path, "sessions", "project", "session-one", "session.jsonl"),
		filepath.Join(fixture.managed.Path, "storages", "session_projcache", "sessions", "session-one.json"),
		filepath.Join(fixture.managed.Path, "attachments", "v1", "objects", "repeat"),
	}
	type targetSnapshot struct {
		data    string
		modTime int64
	}
	before := make(map[string]targetSnapshot, len(targetFiles))
	for _, path := range targetFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = targetSnapshot{data: string(data), modTime: info.ModTime().UnixNano()}
	}
	second, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Applied) != 0 || len(second.Failures) != 0 {
		t.Fatalf("repeat import report = %#v, want no-op", second)
	}
	for path, expected := range before {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != expected.data || info.ModTime().UnixNano() != expected.modTime {
			t.Errorf("covered repeat import rewrote %s", path)
		}
	}
}

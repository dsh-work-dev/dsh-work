package dshmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

	analysis, err := fixture.manager.AnalyzeAppProfileMigration(context.Background(), ProfileRef{DataDirectoryID: fixture.user.ID, Name: "src"})
	if err != nil {
		t.Fatal(err)
	}
	want := AppProfileMigrationAnalysis{
		Plugins: []AppProfileMigrationPlugin{{Package: "@a/p", Version: "1.2.0"}, {Package: "@b/q", Version: "2.0.0", AlreadyInstalled: true}},
		Data:    []AppProfileMigrationData{{Kind: "patch", Count: 3}, {Kind: "credentials", Count: 2}},
	}
	if !reflect.DeepEqual(analysis, want) {
		t.Fatalf("analysis = %#v, want %#v", analysis, want)
	}
}

func TestAnalyzeAppProfileMigrationOmitsManagedCredentialsAndRejectsAppSource(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	managedSource := filepath.Join(fixture.managed.Path, "profiles", "other")
	if err := os.MkdirAll(managedSource, 0o700); err != nil {
		t.Fatal(err)
	}
	writeMigrationFile(t, filepath.Join(fixture.managed.Path, ".credentials.yaml"), "shared: true\n")
	analysis, err := fixture.manager.AnalyzeAppProfileMigration(context.Background(), ProfileRef{DataDirectoryID: fixture.managed.ID, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Data) != 0 {
		t.Fatalf("managed-directory migration data = %#v, want no credentials", analysis.Data)
	}
	_, err = fixture.manager.AnalyzeAppProfileMigration(context.Background(), ProfileRef{DataDirectoryID: fixture.managed.ID, Name: AppProfileName})
	assertFailureCode(t, err, lifecycle.ErrorProfileInvalid)
}

func TestMigrateIntoAppProfileMergesPatchNodesWithoutChangingExistingEntries(t *testing.T) {
	fixture := newAppProfileMigrationFixture(t, nil, nil, nil)
	writeMigrationFile(t, filepath.Join(fixture.sourcePath, "cordis.patch.yml"), "- id: x\n- id: y\n- name: z\n")
	writeMigrationFile(t, filepath.Join(fixture.targetPath, "cordis.patch.yml"), "- id: x\n  config: {a: 1}\n- id: k\n  config: !!js \"1+1\"\n")
	report, err := fixture.manager.MigrateIntoAppProfile(context.Background(), fixture.launch, AppProfileMigrationRequest{
		Source: ProfileRef{DataDirectoryID: fixture.user.ID, Name: "src"}, Patch: true,
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
		Source: ProfileRef{DataDirectoryID: fixture.user.ID, Name: "src"}, Patch: true,
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
	request := AppProfileMigrationRequest{Source: ProfileRef{DataDirectoryID: fixture.user.ID, Name: "src"}, Credentials: true}

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
	request := AppProfileMigrationRequest{Source: ProfileRef{DataDirectoryID: fixture.user.ID, Name: "src"}, Credentials: true}
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
		Source: ProfileRef{DataDirectoryID: fixture.user.ID, Name: "src"}, Patch: true, Credentials: true,
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
	analysis, err := fixture.manager.AnalyzeAppProfileMigration(context.Background(), ProfileRef{DataDirectoryID: fixture.user.ID, Name: "src"})
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
	request := AppProfileMigrationRequest{Source: ProfileRef{DataDirectoryID: fixture.user.ID, Name: "src"}, Plugins: []string{"@b/fail", "@a/p", "@b/q"}}
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
		Source: ProfileRef{DataDirectoryID: fixture.user.ID, Name: "src"}, Plugins: []string{"@unknown/not-listed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 0 || len(report.Applied) != 0 || len(report.Failures) != 0 {
		t.Fatalf("unexpected migration for unlisted package: calls=%#v report=%#v", runner.calls, report)
	}
}

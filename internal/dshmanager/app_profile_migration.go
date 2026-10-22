package dshmanager

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"

	"github.com/local/dsh-work/internal/lifecycle"
	"gopkg.in/yaml.v3"
)

func isRegistryPlugin(info PluginInfo) bool {
	return (info.SourceKind == PluginSourcePublicRegistry || info.SourceKind == PluginSourcePrivateRegistry) && info.Version != ""
}

func patchIDs(seq *yaml.Node) map[string]bool {
	ids := make(map[string]bool)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return ids
	}
	for _, item := range seq.Content {
		id, ok := patchItemID(item)
		if ok {
			ids[id] = true
		}
	}
	return ids
}

func patchItemID(item *yaml.Node) (string, bool) {
	if item == nil || item.Kind != yaml.MappingNode {
		return "", false
	}
	id := mappingValue(item, "id")
	if id == nil || id.Kind != yaml.ScalarNode || id.ShortTag() != "!!str" || id.Value == "" {
		return "", false
	}
	return id.Value, true
}

func managedImportDataDirectory(config Config) (DataDirectoryInfo, error) {
	managed, ok := findDataDirectory(config.DataDirectories, "dsh-work")
	if !ok || managed.Ownership != DataDirectoryOwnershipDSHWork {
		return DataDirectoryInfo{}, failure(lifecycle.ErrorProfileNotFound, "DSH Work data directory was not found", "the managed DSH data directory is not in the catalog")
	}
	return managed, nil
}

func importProfilePath(source importSource, profile string) (string, error) {
	for _, name := range source.Profiles {
		if name == profile {
			return filepath.Join(source.Home, "profiles", profile), nil
		}
	}
	return "", failure(lifecycle.ErrorProfileNotFound, "The source profile was not found.", "choose an existing profile in the selected DSH folder")
}

func analyzeAppProfileMigration(ctx context.Context, config Config, selection AppProfileMigrationSource, source importSource, managed DataDirectoryInfo) (AppProfileMigrationAnalysis, error) {
	analysis := AppProfileMigrationAnalysis{
		Home:     source.Home,
		Profiles: append([]string{}, source.Profiles...),
		Plugins:  []AppProfileMigrationPlugin{},
		Data:     []AppProfileMigrationData{},
	}
	targetPath := filepath.Join(managed.Path, "profiles", AppProfileName)

	if selection.Profile != "" {
		sourcePath, err := importProfilePath(source, selection.Profile)
		if err != nil {
			return AppProfileMigrationAnalysis{}, err
		}
		reader := config.ProfileReader
		if reader == nil {
			reader = FileProfileReader{}
		}
		sourcePlugins, _ := reader.Read(ctx, sourcePath)
		targetPlugins, _ := reader.Read(ctx, targetPath)
		targetByPackage := make(map[string]bool, len(targetPlugins))
		for _, plugin := range targetPlugins {
			targetByPackage[plugin.Package] = true
		}
		for _, plugin := range sourcePlugins {
			if isRegistryPlugin(plugin) {
				analysis.Plugins = append(analysis.Plugins, AppProfileMigrationPlugin{
					Package: plugin.Package, Version: plugin.Version, AlreadyInstalled: targetByPackage[plugin.Package],
				})
			}
		}
		sort.Slice(analysis.Plugins, func(i, j int) bool { return analysis.Plugins[i].Package < analysis.Plugins[j].Package })

		sourcePatch, sourcePatchErr := readPatchSequence(filepath.Join(sourcePath, "cordis.patch.yml"))
		if sourcePatchErr == nil && len(sourcePatch.Content) > 0 {
			data := AppProfileMigrationData{Kind: "patch", Count: len(sourcePatch.Content)}
			targetPatch, targetPatchErr := readPatchSequence(filepath.Join(targetPath, "cordis.patch.yml"))
			if targetPatchErr == nil {
				targetIDs := patchIDs(targetPatch)
				data.Covered = true
				for _, item := range sourcePatch.Content {
					id, ok := patchItemID(item)
					if !ok || !targetIDs[id] {
						data.Covered = false
						break
					}
				}
			}
			analysis.Data = append(analysis.Data, data)
		}
	}

	sessionCount, hasSessions, err := importSessionCount(source.Sessions, managed.Path)
	if err != nil {
		return AppProfileMigrationAnalysis{}, err
	}
	if hasSessions {
		analysis.Data = append(analysis.Data, AppProfileMigrationData{Kind: "sessions", Count: sessionCount, Covered: sessionCount == 0})
	}

	workspaceCount, hasWorkspaces, err := analyzeWorkspaceImport(
		filepath.Join(source.Storages, "workspace.json"),
		filepath.Join(managed.Path, "storages", "workspace.json"),
	)
	if err != nil {
		return AppProfileMigrationAnalysis{}, err
	}
	if hasWorkspaces {
		analysis.Data = append(analysis.Data, AppProfileMigrationData{Kind: "workspaces", Count: workspaceCount, Covered: workspaceCount == 0})
	}

	sourceCredentials := source.Credentials
	if info, statErr := os.Lstat(sourceCredentials); statErr == nil && info.Mode().IsRegular() {
		_, sourceRefs, sourceRecords, sourceErr := credentialEntries(sourceCredentials)
		targetCredentials := filepath.Join(managed.Path, ".credentials.yaml")
		targetDoc, targetRefs, _, targetErr := credentialEntries(targetCredentials)
		if sourceErr == nil && targetErr == nil && (len(sourceRefs) > 0 || len(sourceRecords) > 0) {
			missing := missingCredentialCount(sourceRefs, sourceRecords, targetRefs, credentialRecordKeys(targetDoc))
			analysis.Data = append(analysis.Data, AppProfileMigrationData{Kind: "credentials", Count: missing, Covered: missing == 0})
		}
	}
	return analysis, nil
}

func (m *Manager) AnalyzeAppProfileMigration(ctx context.Context, source AppProfileMigrationSource) (AppProfileMigrationAnalysis, error) {
	if err := contextError(ctx); err != nil {
		return AppProfileMigrationAnalysis{}, err
	}
	m.mu.RLock()
	config := m.configSnapshotLocked()
	m.mu.RUnlock()

	managed, err := managedImportDataDirectory(config)
	if err != nil {
		return AppProfileMigrationAnalysis{}, err
	}
	resolved, err := resolveImportSource(source.Home, managed)
	if err != nil {
		return AppProfileMigrationAnalysis{}, err
	}
	return analyzeAppProfileMigration(ctx, config, source, resolved, managed)
}

func (m *Manager) MigrateIntoAppProfile(ctx context.Context, launch ResolvedLaunch, request AppProfileMigrationRequest) (AppProfileMigrationReport, error) {
	if err := m.requireNoCurrentRunContext(); err != nil {
		return AppProfileMigrationReport{}, err
	}
	if err := contextError(ctx); err != nil {
		return AppProfileMigrationReport{}, err
	}
	m.mu.RLock()
	config := m.configSnapshotLocked()
	m.mu.RUnlock()

	managed, err := managedImportDataDirectory(config)
	if err != nil {
		return AppProfileMigrationReport{}, err
	}
	resolved, err := resolveImportSource(request.Source.Home, managed)
	if err != nil {
		return AppProfileMigrationReport{}, err
	}
	if launch.Target.Profile != (ProfileRef{DataDirectoryID: "dsh-work", Name: AppProfileName}) || !isAppProfile(launch.DataDirectory, launch.Target.Profile.Name) {
		return AppProfileMigrationReport{}, failure(lifecycle.ErrorProfileInvalid, "the migration target is not the DSH Work profile", "switch to the managed DSH Work profile before migrating")
	}

	analysis, err := analyzeAppProfileMigration(ctx, config, request.Source, resolved, managed)
	if err != nil {
		return AppProfileMigrationReport{}, err
	}
	requestedPlugins := make(map[string]bool, len(request.Plugins))
	for _, packageName := range request.Plugins {
		requestedPlugins[packageName] = true
	}
	var report AppProfileMigrationReport
	targetPath := filepath.Join(managed.Path, "profiles", AppProfileName)
	if request.Patch && migrationDataAvailable(analysis.Data, "patch") {
		if err := migratePatchLayer(filepath.Join(resolved.Home, "profiles", request.Source.Profile), targetPath); err != nil {
			report.Failures = append(report.Failures, AppProfileMigrationFailure{Item: "patch", Message: err.Error()})
		} else {
			report.Applied = append(report.Applied, "patch")
		}
	}
	if request.Credentials && migrationDataAvailable(analysis.Data, "credentials") {
		targetCredentials := filepath.Join(managed.Path, ".credentials.yaml")
		applied, err := migrateCredentials(resolved.Credentials, targetCredentials)
		if err != nil {
			report.Failures = append(report.Failures, AppProfileMigrationFailure{Item: "credentials", Message: err.Error()})
		} else if applied {
			report.Applied = append(report.Applied, "credentials")
		}
	}
	if request.Workspaces && migrationDataAvailable(analysis.Data, "workspaces") {
		applied, err := migrateWorkspaces(
			filepath.Join(resolved.Storages, "workspace.json"),
			filepath.Join(managed.Path, "storages", "workspace.json"),
		)
		if err != nil {
			report.Failures = append(report.Failures, AppProfileMigrationFailure{Item: "workspaces", Message: err.Error()})
		} else if applied {
			report.Applied = append(report.Applied, "workspaces")
		}
	}
	if request.Sessions && migrationDataAvailable(analysis.Data, "sessions") {
		failures := migrateSessionHistory(resolved, managed)
		if len(failures) > 0 {
			report.Failures = append(report.Failures, AppProfileMigrationFailure{Item: "sessions", Message: strings.Join(failures, "; ")})
		} else {
			report.Applied = append(report.Applied, "sessions")
		}
	}
	for _, plugin := range analysis.Plugins {
		if !requestedPlugins[plugin.Package] || plugin.AlreadyInstalled {
			continue
		}
		_, err := m.ApplyPlugin(ctx, launch, plugin.Package+"@"+plugin.Version, "add")
		if err == nil {
			report.Applied = append(report.Applied, plugin.Package)
		} else {
			report.Failures = append(report.Failures, AppProfileMigrationFailure{Item: plugin.Package, Message: migrationFailureMessage(err)})
		}
		if err := contextError(ctx); err != nil {
			return report, err
		}
	}
	return report, nil
}

func migrationDataAvailable(data []AppProfileMigrationData, kind string) bool {
	for _, item := range data {
		if item.Kind == kind && !item.Covered {
			return true
		}
	}
	return false
}

type importSessionDirectory struct {
	project string
	name    string
	path    string
}

func collectImportSessions(root string) ([]importSessionDirectory, bool, error) {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() {
		return nil, false, nil
	}
	projects, err := os.ReadDir(root)
	if err != nil {
		return nil, true, err
	}
	sessions := make([]importSessionDirectory, 0)
	for _, project := range projects {
		projectPath := filepath.Join(root, project.Name())
		projectInfo, err := os.Lstat(projectPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, true, err
		}
		if !projectInfo.IsDir() || projectInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}
		children, err := os.ReadDir(projectPath)
		if err != nil {
			return nil, true, err
		}
		for _, child := range children {
			sessionPath := filepath.Join(projectPath, child.Name())
			sessionInfo, err := os.Lstat(sessionPath)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, true, err
			}
			if !sessionInfo.IsDir() || sessionInfo.Mode()&os.ModeSymlink != 0 {
				continue
			}
			sessions = append(sessions, importSessionDirectory{project: project.Name(), name: child.Name(), path: sessionPath})
		}
	}
	return sessions, true, nil
}

func importSessionCount(sourceRoot, managedPath string) (int, bool, error) {
	sessions, exists, err := collectImportSessions(sourceRoot)
	if err != nil || !exists {
		return 0, exists, err
	}
	count := 0
	for _, session := range sessions {
		target := filepath.Join(managedPath, "sessions", session.project, session.name)
		present, err := importPathExists(target)
		if err != nil {
			return 0, true, err
		}
		if !present {
			count++
		}
	}
	return count, true, nil
}

func importPathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func readWorkspaceDocument(path string) (map[string]any, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, nil, errors.New("workspace storage contains multiple JSON values")
		}
		return nil, nil, err
	}
	if document == nil {
		return nil, nil, errors.New("workspace storage must be a JSON object")
	}
	return document, data, nil
}

func workspaceUnitV2(document map[string]any) bool {
	unit, ok := document["unit"].(map[string]any)
	if !ok || unit["name"] != "workspace" {
		return false
	}
	switch version := unit["version"].(type) {
	case json.Number:
		value, err := version.Float64()
		return err == nil && value == 2
	case float64:
		return version == 2
	case int:
		return version == 2
	default:
		return false
	}
}

func workspaceDocumentSupported(document map[string]any) bool {
	if !workspaceUnitV2(document) {
		return false
	}
	_, ok := document["global"].(map[string]any)
	if !ok {
		return false
	}
	tables, ok := document["tables"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = tables["workspaces"].(map[string]any)
	return ok
}

func workspaceRecords(document map[string]any) map[string]any {
	tables, _ := document["tables"].(map[string]any)
	records, _ := tables["workspaces"].(map[string]any)
	if records == nil {
		return map[string]any{}
	}
	return records
}

func workspaceGlobal(document map[string]any) map[string]any {
	global, _ := document["global"].(map[string]any)
	if global == nil {
		global = map[string]any{}
		document["global"] = global
	}
	return global
}

func workspaceOrder(document map[string]any, records map[string]any) []string {
	order := make([]string, 0, len(records))
	seen := make(map[string]bool, len(records))
	global, _ := document["global"].(map[string]any)
	if global != nil {
		if ids, ok := global["workspaceIds"].([]any); ok {
			for _, value := range ids {
				id, ok := value.(string)
				if ok && !seen[id] {
					if _, exists := records[id]; exists {
						order = append(order, id)
						seen[id] = true
					}
				}
			}
		}
	}
	remaining := make([]string, 0, len(records)-len(order))
	for id := range records {
		if !seen[id] {
			remaining = append(remaining, id)
		}
	}
	sort.Strings(remaining)
	return append(order, remaining...)
}

func workspacePath(record any) (string, bool) {
	value, ok := record.(map[string]any)
	if !ok {
		return "", false
	}
	path, ok := value["path"].(string)
	return path, ok && path != ""
}

func workspacePathsEqual(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func matchingWorkspaceID(sourceRecord any, targetDocument map[string]any) string {
	sourcePath, ok := workspacePath(sourceRecord)
	if !ok {
		return ""
	}
	targetRecords := workspaceRecords(targetDocument)
	for _, id := range workspaceOrder(targetDocument, targetRecords) {
		targetPath, ok := workspacePath(targetRecords[id])
		if ok && workspacePathsEqual(sourcePath, targetPath) {
			return id
		}
	}
	return ""
}

func stringValues(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func workspaceAddsSessionIDs(sourceRecord, targetRecord any) bool {
	source, sourceOK := sourceRecord.(map[string]any)
	target, targetOK := targetRecord.(map[string]any)
	if !sourceOK || !targetOK {
		return false
	}
	targetIDs := make(map[string]bool)
	for _, id := range stringValues(target["sessionIds"]) {
		targetIDs[id] = true
	}
	for _, id := range stringValues(source["sessionIds"]) {
		if !targetIDs[id] {
			return true
		}
	}
	return false
}

func workspaceMergeCount(sourceDocument, targetDocument map[string]any) int {
	sourceRecords := workspaceRecords(sourceDocument)
	targetRecords := workspaceRecords(targetDocument)
	count := 0
	for _, sourceID := range workspaceOrder(sourceDocument, sourceRecords) {
		sourceRecord := sourceRecords[sourceID]
		targetID := matchingWorkspaceID(sourceRecord, targetDocument)
		if targetID == "" {
			count++
			continue
		}
		if workspaceAddsSessionIDs(sourceRecord, targetRecords[targetID]) {
			count++
		}
	}
	return count
}

func analyzeWorkspaceImport(sourcePath, targetPath string) (int, bool, error) {
	sourceInfo, err := os.Lstat(sourcePath)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if !sourceInfo.Mode().IsRegular() {
		return 0, false, nil
	}
	sourceDocument, _, err := readWorkspaceDocument(sourcePath)
	if err != nil || !workspaceDocumentSupported(sourceDocument) {
		return 0, false, nil
	}

	targetDocument := map[string]any{
		"global": map[string]any{},
		"tables": map[string]any{"workspaces": map[string]any{}},
	}
	targetInfo, err := os.Lstat(targetPath)
	if err == nil {
		if !targetInfo.Mode().IsRegular() {
			return 0, false, nil
		}
		targetDocument, _, err = readWorkspaceDocument(targetPath)
		if err != nil || !workspaceDocumentSupported(targetDocument) {
			return 0, false, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, false, err
	}
	return workspaceMergeCount(sourceDocument, targetDocument), true, nil
}

func appendUniqueValues(current, incoming any) ([]any, bool) {
	values, _ := current.([]any)
	result := make([]any, 0, len(values)+len(arrayValues(incoming)))
	changed := false
	for _, value := range values {
		found := false
		for _, existing := range result {
			if reflect.DeepEqual(existing, value) {
				found = true
				break
			}
		}
		if found {
			changed = true
			continue
		}
		result = append(result, value)
	}
	for _, value := range arrayValues(incoming) {
		found := false
		for _, existing := range result {
			if reflect.DeepEqual(existing, value) {
				found = true
				break
			}
		}
		if !found {
			result = append(result, value)
			changed = true
		}
	}
	return result, changed
}

func arrayValues(value any) []any {
	values, _ := value.([]any)
	return values
}

func appendUniqueStrings(current any, incoming []string) ([]any, bool) {
	values, ok := current.([]any)
	if !ok {
		values = []any{}
	}
	result := append([]any(nil), values...)
	seen := make(map[string]bool)
	for _, value := range result {
		if text, ok := value.(string); ok {
			seen[text] = true
		}
	}
	added := false
	for _, value := range incoming {
		if seen[value] {
			continue
		}
		result = append(result, value)
		seen[value] = true
		added = true
	}
	return result, added
}

func newWorkspaceID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func mergeWorkspaceDocuments(sourceDocument, targetDocument map[string]any) (bool, error) {
	sourceRecords := workspaceRecords(sourceDocument)
	targetTables, _ := targetDocument["tables"].(map[string]any)
	targetRecords, _ := targetTables["workspaces"].(map[string]any)
	targetGlobal := workspaceGlobal(targetDocument)
	changed := false

	workspaceIDs, _ := targetGlobal["workspaceIds"].([]any)
	if workspaceIDs == nil {
		workspaceIDs = []any{}
	}

	workspaceIDInUse := func(id string) bool {
		if _, exists := targetRecords[id]; exists {
			return true
		}
		for _, value := range workspaceIDs {
			if existing, ok := value.(string); ok && existing == id {
				return true
			}
		}
		return false
	}
	for _, sourceID := range workspaceOrder(sourceDocument, sourceRecords) {
		sourceRecord := sourceRecords[sourceID]
		if targetID := matchingWorkspaceID(sourceRecord, targetDocument); targetID != "" {
			sourceWorkspace, sourceOK := sourceRecord.(map[string]any)
			targetWorkspace, targetOK := targetRecords[targetID].(map[string]any)
			if sourceOK && targetOK {
				merged, added := appendUniqueStrings(targetWorkspace["sessionIds"], stringValues(sourceWorkspace["sessionIds"]))
				if added {
					targetWorkspace["sessionIds"] = merged
					changed = true
				}
			}
			continue
		}

		targetID := sourceID
		if workspaceIDInUse(targetID) {
			for {
				generated, err := newWorkspaceID()
				if err != nil {
					return false, err
				}
				if !workspaceIDInUse(generated) {
					targetID = generated
					break
				}
			}
		}
		targetRecords[targetID] = sourceRecord
		workspaceIDs = append(workspaceIDs, targetID)
		changed = true
	}

	for _, field := range []string{"archivedSessionIds", "pinnedSessionIds"} {
		merged, added := appendUniqueValues(targetGlobal[field], workspaceGlobal(sourceDocument)[field])
		if added {
			targetGlobal[field] = merged
			changed = true
		}
	}
	if changed {
		targetGlobal["workspaceIds"] = workspaceIDs
	}
	return changed, nil
}

func migrateWorkspaces(sourcePath, targetPath string) (bool, error) {
	sourceDocument, sourceBytes, err := readWorkspaceDocument(sourcePath)
	if err != nil {
		return false, err
	}
	if !workspaceDocumentSupported(sourceDocument) {
		return false, errors.New("source workspace storage is not a workspace version 2 document")
	}
	if err := ensureImportDirectory(filepath.Dir(targetPath)); err != nil {
		return false, err
	}
	targetInfo, err := os.Lstat(targetPath)
	if errors.Is(err, os.ErrNotExist) {
		return writeImportedBytes(targetPath, sourceBytes, false)
	}
	if err != nil {
		return false, err
	}
	if !targetInfo.Mode().IsRegular() {
		return false, errors.New("target workspace storage is not a regular file")
	}
	targetDocument, _, err := readWorkspaceDocument(targetPath)
	if err != nil {
		return false, err
	}
	if !workspaceDocumentSupported(targetDocument) {
		return false, errors.New("target workspace storage is not a workspace version 2 document")
	}
	changed, err := mergeWorkspaceDocuments(sourceDocument, targetDocument)
	if err != nil || !changed {
		return false, err
	}
	data, err := json.MarshalIndent(targetDocument, "", "  ")
	if err != nil {
		return false, err
	}
	return writeImportedBytes(targetPath, data, true)
}

func ensureImportDirectory(path string) error {
	path = filepath.Clean(path)
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("import target directory is not a regular directory: %s", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return err
	}
	if err := ensureImportDirectory(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		info, statErr := os.Lstat(path)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("import target directory is not a regular directory: %s", path)
		}
	}
	return nil
}

func writeImportedBytes(path string, data []byte, replace bool) (bool, error) {
	if err := ensureImportDirectory(filepath.Dir(path)); err != nil {
		return false, err
	}
	if err := removeLegacyImportTemporary(path); err != nil {
		return false, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".dsh-work-tmp-*")
	if err != nil {
		return false, err
	}
	temporary := file.Name()
	defer func() {
		_ = os.Remove(temporary)
	}()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return false, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	if !replace {
		present, err := importPathExists(path)
		if err != nil {
			return false, err
		}
		if present {
			return false, nil
		}
	}
	if err := os.Rename(temporary, path); err != nil {
		if !replace {
			present, existsErr := importPathExists(path)
			if existsErr == nil && present {
				return false, nil
			}
		}
		return false, err
	}
	return true, nil
}

func removeLegacyImportTemporary(path string) error {
	err := os.Remove(path + ".dsh-work-tmp")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func migrateSessionHistory(source importSource, managed DataDirectoryInfo) []string {
	failures := make([]string, 0)
	sessions, exists, err := collectImportSessions(source.Sessions)
	if err != nil {
		failures = append(failures, "session history: "+err.Error())
	} else if exists {
		for _, session := range sessions {
			targetProject := filepath.Join(managed.Path, "sessions", session.project)
			targetSession := filepath.Join(targetProject, session.name)
			present, err := importPathExists(targetSession)
			if err != nil {
				failures = append(failures, fmt.Sprintf("session %s: %v", session.name, err))
				continue
			}
			if present {
				continue
			}
			if err := ensureImportDirectory(targetProject); err != nil {
				failures = append(failures, fmt.Sprintf("session %s: %v", session.name, err))
				continue
			}
			temporary := filepath.Join(targetProject, ".dsh-work-import-"+session.name)
			if err := os.RemoveAll(temporary); err != nil {
				failures = append(failures, fmt.Sprintf("session %s: %v", session.name, err))
				continue
			}
			if err := os.Mkdir(temporary, 0o700); err != nil {
				failures = append(failures, fmt.Sprintf("session %s: %v", session.name, err))
				continue
			}
			if err := copyImportTree(session.path, temporary); err != nil {
				_ = os.RemoveAll(temporary)
				failures = append(failures, fmt.Sprintf("session %s: %v", session.name, err))
				continue
			}
			present, err = importPathExists(targetSession)
			if err != nil {
				_ = os.RemoveAll(temporary)
				failures = append(failures, fmt.Sprintf("session %s: %v", session.name, err))
				continue
			}
			if present {
				_ = os.RemoveAll(temporary)
				continue
			}
			if err := os.Rename(temporary, targetSession); err != nil {
				_ = os.RemoveAll(temporary)
				if present, statErr := importPathExists(targetSession); statErr == nil && present {
					continue
				}
				failures = append(failures, fmt.Sprintf("session %s: %v", session.name, err))
			}
		}
	}

	if err := migrateProjectionCache(source.Storages, managed.Path); err != nil {
		failures = append(failures, "projection cache: "+err.Error())
	}
	if err := migrateAttachments(source.Attachments, managed.Path); err != nil {
		failures = append(failures, "attachments: "+err.Error())
	}
	return failures
}

func copyImportTree(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("session contains a symbolic link or special file")
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		targetPath := filepath.Join(target, entry.Name())
		info, err := os.Lstat(sourcePath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("session contains a symbolic link or special file")
		}
		if info.IsDir() {
			if err := os.Mkdir(targetPath, 0o700); err != nil {
				return err
			}
			if err := copyImportTree(sourcePath, targetPath); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return errors.New("session contains a symbolic link or special file")
		}
		if err := copyImportFile(sourcePath, targetPath); err != nil {
			return err
		}
	}
	return nil
}

func copyImportFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func migrateProjectionCache(sourceStorages, managedPath string) error {
	sourceRoot := filepath.Join(sourceStorages, "session_projcache", "sessions")
	info, err := os.Lstat(sourceRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("projection cache source is not a regular directory")
	}
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		return err
	}
	targetRoot := filepath.Join(managedPath, "storages", "session_projcache", "sessions")
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		sourcePath := filepath.Join(sourceRoot, entry.Name())
		info, err := os.Lstat(sourcePath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		targetPath := filepath.Join(targetRoot, entry.Name())
		present, err := importPathExists(targetPath)
		if err != nil {
			return err
		}
		if present {
			continue
		}
		if err := ensureImportDirectory(targetRoot); err != nil {
			return err
		}
		if _, err := copyImportFileAtomically(sourcePath, targetPath); err != nil {
			return err
		}
	}
	return nil
}

func migrateAttachments(sourceRoot, managedPath string) error {
	info, err := os.Lstat(sourceRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("attachment source is not a regular directory")
	}
	return filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == sourceRoot {
			return nil
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		first := strings.Split(relative, string(filepath.Separator))[0]
		if first == "tmp" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		targetPath := filepath.Join(managedPath, "attachments", "v1", relative)
		present, err := importPathExists(targetPath)
		if err != nil {
			return err
		}
		if present {
			return nil
		}
		if err := ensureImportDirectory(filepath.Dir(targetPath)); err != nil {
			return err
		}
		if _, err := copyImportFileAtomically(path, targetPath); err != nil {
			return err
		}
		return nil
	})
}

func copyImportFileAtomically(source, target string) (bool, error) {
	input, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer input.Close()
	if err := ensureImportDirectory(filepath.Dir(target)); err != nil {
		return false, err
	}
	if err := removeLegacyImportTemporary(target); err != nil {
		return false, err
	}
	output, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".dsh-work-tmp-*")
	if err != nil {
		return false, err
	}
	temporary := output.Name()
	defer func() {
		_ = os.Remove(temporary)
	}()
	_, copyErr := io.Copy(output, input)
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return false, copyErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	present, err := importPathExists(target)
	if err != nil {
		return false, err
	}
	if present {
		return false, nil
	}
	if err := os.Rename(temporary, target); err != nil {
		if present, statErr := importPathExists(target); statErr == nil && present {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func migratePatchLayer(sourcePath, targetPath string) error {
	source, err := readPatchSequence(filepath.Join(sourcePath, "cordis.patch.yml"))
	if err != nil {
		return err
	}
	target, err := readPatchSequence(filepath.Join(targetPath, "cordis.patch.yml"))
	if errors.Is(err, os.ErrNotExist) {
		target = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	} else if err != nil {
		return err
	}
	if len(source.Content) > 0 {
		source.Content[0].HeadComment = ""
	}
	ids := patchIDs(target)
	for _, item := range source.Content {
		if id, hasID := patchItemID(item); hasID {
			if ids[id] {
				continue
			}
			ids[id] = true
		}
		target.Content = append(target.Content, item)
	}
	target.Style &^= yaml.FlowStyle
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(target); err != nil {
		_ = encoder.Close()
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	temporaryPath := filepath.Join(targetPath, "cordis.patch.yml.dsh-work-tmp")
	defer os.Remove(temporaryPath)
	if err := os.WriteFile(temporaryPath, buffer.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(temporaryPath, filepath.Join(targetPath, "cordis.patch.yml"))
}

func credentialEntries(path string) (doc *yaml.Node, refs, records map[string]*yaml.Node, err error) {
	refs = make(map[string]*yaml.Node)
	records = make(map[string]*yaml.Node)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, refs, records, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, nil, nil, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, nil, nil, errors.New("credentials document must be a YAML mapping")
	}
	root := document.Content[0]
	version := mappingValue(root, "version")
	if version == nil || version.Kind != yaml.ScalarNode || version.ShortTag() != "!!int" || version.Value != "1" {
		return nil, nil, nil, errors.New("credentials document must use version 1")
	}
	refsNode := mappingValue(root, "refs")
	if refsNode != nil && refsNode.Kind != yaml.MappingNode {
		return nil, nil, nil, errors.New("credentials refs must be a YAML mapping")
	}
	recordsNode := mappingValue(root, "records")
	if recordsNode != nil && recordsNode.Kind != yaml.MappingNode {
		return nil, nil, nil, errors.New("credentials records must be a YAML mapping")
	}
	refs = credentialNodeEntries(refsNode)
	for key, value := range credentialNodeEntries(recordsNode) {
		kind := mappingValue(value, "kind")
		if kind != nil && kind.Kind == yaml.ScalarNode && kind.ShortTag() == "!!str" && kind.Value == "api-key" {
			records[key] = value
		}
	}
	return &document, refs, records, nil
}

func credentialNodeEntries(mapping *yaml.Node) map[string]*yaml.Node {
	entries := make(map[string]*yaml.Node)
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return entries
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		key := mapping.Content[index]
		if key.Kind == yaml.ScalarNode && key.ShortTag() == "!!str" {
			entries[key.Value] = mapping.Content[index+1]
		}
	}
	return entries
}

func credentialRecordKeys(doc *yaml.Node) map[string]bool {
	keys := make(map[string]bool)
	root := credentialRoot(doc)
	if root == nil {
		return keys
	}
	for key := range credentialNodeEntries(mappingValue(root, "records")) {
		keys[key] = true
	}
	return keys
}

func credentialRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	return doc.Content[0]
}

func missingCredentialCount(sourceRefs, sourceRecords, targetRefs map[string]*yaml.Node, targetRecordKeys map[string]bool) int {
	missing := 0
	for key := range sourceRefs {
		if _, exists := targetRefs[key]; !exists {
			missing++
		}
	}
	for key := range sourceRecords {
		if !targetRecordKeys[key] {
			missing++
		}
	}
	return missing
}

func migrateCredentials(sourcePath, targetPath string) (bool, error) {
	_, sourceRefs, sourceRecords, err := credentialEntries(sourcePath)
	if err != nil {
		return false, err
	}
	targetDoc, targetRefs, _, err := credentialEntries(targetPath)
	if err != nil {
		return false, err
	}
	if targetDoc == nil {
		targetDoc = newCredentialsDocument()
	}
	root := credentialRoot(targetDoc)
	refsNode := ensureCredentialMap(root, "refs")
	recordsNode := ensureCredentialMap(root, "records")
	targetRecordKeys := credentialRecordKeys(targetDoc)
	refsAdded := appendCredentialEntries(refsNode, sourceRefs, targetRefs)
	recordsAdded := appendCredentialRecords(recordsNode, sourceRecords, targetRecordKeys)
	if !refsAdded && !recordsAdded {
		return false, nil
	}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(targetDoc); err != nil {
		_ = encoder.Close()
		return false, err
	}
	if err := encoder.Close(); err != nil {
		return false, err
	}
	temporaryPath := targetPath + ".dsh-work-tmp"
	defer os.Remove(temporaryPath)
	file, err := os.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return false, err
	}
	if _, err := file.Write(buffer.Bytes()); err != nil {
		_ = file.Close()
		return false, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(temporaryPath, targetPath); err != nil {
		return false, err
	}
	return true, nil
}

func newCredentialsDocument() *yaml.Node {
	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "version"},
			{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "refs"},
			{Kind: yaml.MappingNode, Tag: "!!map"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "records"},
			{Kind: yaml.MappingNode, Tag: "!!map"},
		},
	}}}
}

func ensureCredentialMap(root *yaml.Node, key string) *yaml.Node {
	if mapping := mappingValue(root, key); mapping != nil {
		return mapping
	}
	mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, mapping)
	return mapping
}

func appendCredentialEntries(target *yaml.Node, source, targetEntries map[string]*yaml.Node) bool {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	added := false
	for _, key := range keys {
		if _, exists := targetEntries[key]; exists {
			continue
		}
		target.Content = append(target.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, source[key])
		targetEntries[key] = source[key]
		added = true
	}
	return added
}

func appendCredentialRecords(target *yaml.Node, source map[string]*yaml.Node, targetKeys map[string]bool) bool {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	added := false
	for _, key := range keys {
		if targetKeys[key] {
			continue
		}
		target.Content = append(target.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, source[key])
		targetKeys[key] = true
		added = true
	}
	return added
}

func migrationFailureMessage(err error) string {
	var failureValue lifecycle.Failure
	if errors.As(err, &failureValue) {
		return failureValue.Summary
	}
	var failurePointer *lifecycle.Failure
	if errors.As(err, &failurePointer) && failurePointer != nil {
		return failurePointer.Summary
	}
	return err.Error()
}

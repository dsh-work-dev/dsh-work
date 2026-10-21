package dshmanager

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/local/dsh-work/internal/lifecycle"
	"gopkg.in/yaml.v3"
)

func migrationPaths(config Config, source ProfileRef) (sourceDirectory DataDirectoryInfo, sourcePath, targetPath string, err error) {
	if err := validateProfileRef(source); err != nil {
		return DataDirectoryInfo{}, "", "", err
	}
	sourceDirectory, ok := findDataDirectory(config.DataDirectories, source.DataDirectoryID)
	if !ok {
		return DataDirectoryInfo{}, "", "", failure(lifecycle.ErrorProfileNotFound, "DSH data directory was not found", "the source profile data directory is not in the catalog")
	}
	if sourceDirectory.Ownership == DataDirectoryOwnershipUser && !directoryExists(sourceDirectory.Path) {
		return DataDirectoryInfo{}, "", "", failure(lifecycle.ErrorProfileNotFound, "The selected user DSH data directory is unavailable", "register an existing DSH data directory")
	}
	if isAppProfile(sourceDirectory, source.Name) {
		return DataDirectoryInfo{}, "", "", failure(lifecycle.ErrorProfileInvalid, "Choose another profile to migrate from.", "the DSH Work profile cannot be its own migration source")
	}
	sourcePath = filepath.Join(sourceDirectory.Path, "profiles", source.Name)
	info, statErr := os.Lstat(sourcePath)
	if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return DataDirectoryInfo{}, "", "", failure(lifecycle.ErrorProfileNotFound, "The source profile was not found.", "choose an existing profile directory")
	}
	targetDirectory, ok := findDataDirectory(config.DataDirectories, "dsh-work")
	if !ok || targetDirectory.Ownership != DataDirectoryOwnershipDSHWork {
		return DataDirectoryInfo{}, "", "", failure(lifecycle.ErrorProfileNotFound, "DSH Work data directory was not found", "the managed DSH data directory is not in the catalog")
	}
	targetPath = filepath.Join(targetDirectory.Path, "profiles", AppProfileName)
	return sourceDirectory, sourcePath, targetPath, nil
}

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

func analyzeAppProfileMigration(ctx context.Context, config Config, sourceDirectory DataDirectoryInfo, sourcePath, targetPath string) AppProfileMigrationAnalysis {
	analysis := AppProfileMigrationAnalysis{Plugins: []AppProfileMigrationPlugin{}, Data: []AppProfileMigrationData{}}
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

	if sourceDirectory.Ownership == DataDirectoryOwnershipUser {
		sourceCredentials := filepath.Join(sourceDirectory.Path, ".credentials.yaml")
		if info, statErr := os.Lstat(sourceCredentials); statErr == nil && info.Mode().IsRegular() {
			_, sourceRefs, sourceRecords, sourceErr := credentialEntries(sourceCredentials)
			targetCredentials := filepath.Join(targetDirectoryPath(config), ".credentials.yaml")
			targetDoc, targetRefs, _, targetErr := credentialEntries(targetCredentials)
			if sourceErr == nil && targetErr == nil && (len(sourceRefs) > 0 || len(sourceRecords) > 0) {
				missing := missingCredentialCount(sourceRefs, sourceRecords, targetRefs, credentialRecordKeys(targetDoc))
				analysis.Data = append(analysis.Data, AppProfileMigrationData{Kind: "credentials", Count: missing, Covered: missing == 0})
			}
		}
	}
	return analysis
}

func targetDirectoryPath(config Config) string {
	if directory, ok := findDataDirectory(config.DataDirectories, "dsh-work"); ok {
		return directory.Path
	}
	return ""
}

func (m *Manager) AnalyzeAppProfileMigration(ctx context.Context, source ProfileRef) (AppProfileMigrationAnalysis, error) {
	if err := contextError(ctx); err != nil {
		return AppProfileMigrationAnalysis{}, err
	}
	m.mu.RLock()
	config := m.configSnapshotLocked()
	m.mu.RUnlock()
	sourceDirectory, sourcePath, targetPath, err := migrationPaths(config, source)
	if err != nil {
		return AppProfileMigrationAnalysis{}, err
	}
	return analyzeAppProfileMigration(ctx, config, sourceDirectory, sourcePath, targetPath), nil
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
	sourceDirectory, sourcePath, targetPath, err := migrationPaths(config, request.Source)
	if err != nil {
		return AppProfileMigrationReport{}, err
	}
	if launch.Target.Profile != (ProfileRef{DataDirectoryID: "dsh-work", Name: AppProfileName}) || !isAppProfile(launch.DataDirectory, launch.Target.Profile.Name) {
		return AppProfileMigrationReport{}, failure(lifecycle.ErrorProfileInvalid, "the migration target is not the DSH Work profile", "switch to the managed DSH Work profile before migrating")
	}

	analysis := analyzeAppProfileMigration(ctx, config, sourceDirectory, sourcePath, targetPath)
	requestedPlugins := make(map[string]bool, len(request.Plugins))
	for _, packageName := range request.Plugins {
		requestedPlugins[packageName] = true
	}
	var report AppProfileMigrationReport
	if request.Patch && migrationDataAvailable(analysis.Data, "patch") {
		if err := migratePatchLayer(sourcePath, targetPath); err != nil {
			report.Failures = append(report.Failures, AppProfileMigrationFailure{Item: "patch", Message: err.Error()})
		} else {
			report.Applied = append(report.Applied, "patch")
		}
	}
	if request.Credentials && migrationDataAvailable(analysis.Data, "credentials") {
		targetCredentials := filepath.Join(targetDirectoryPath(config), ".credentials.yaml")
		sourceCredentials := filepath.Join(sourceDirectory.Path, ".credentials.yaml")
		applied, err := migrateCredentials(sourceCredentials, targetCredentials)
		if err != nil {
			report.Failures = append(report.Failures, AppProfileMigrationFailure{Item: "credentials", Message: err.Error()})
		} else if applied {
			report.Applied = append(report.Applied, "credentials")
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

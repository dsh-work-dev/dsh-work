package dshmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type profileRecoveryFile struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Last          string               `json:"last,omitempty"`
	Points        []storedRestorePoint `json:"points"`
}

type profileProvenanceFile struct {
	Records []PluginProvenanceRecord `json:"records"`
}

func profileRecordsDir(dataDirectory DataDirectoryInfo, profile string) string {
	return filepath.Join(dataDirectory.Path, "profiles", profile, profileRecordsFolder)
}

type recordProfile struct {
	ref ProfileRef
	dir string
}

func recordProfiles(directories []DataDirectoryInfo) ([]recordProfile, error) {
	var profiles []recordProfile
	var readError error
	for _, directory := range directories {
		entries, err := os.ReadDir(filepath.Join(directory.Path, "profiles"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			readError = errors.Join(readError, err)
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || strings.EqualFold(entry.Name(), "node_modules") {
				continue
			}
			profiles = append(profiles, recordProfile{
				ref: ProfileRef{DataDirectoryID: directory.ID, Name: entry.Name()},
				dir: profileRecordsDir(directory, entry.Name()),
			})
		}
	}
	return profiles, readError
}

func readProfileRecord(path string, value any, unreadable map[string]bool) bool {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		unreadable[path] = true
		return false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxManagerStateBytes+1))
	if err != nil || len(data) > maxManagerStateBytes || json.Unmarshal(data, value) != nil {
		unreadable[path] = true
		return false
	}
	return true
}

func loadProfileRecords(dataDirectories []DataDirectoryInfo) (points []storedRestorePoint, last map[string]string, provenance []PluginProvenanceRecord, unreadable map[string]bool) {
	last = map[string]string{}
	unreadable = map[string]bool{}
	profiles, _ := recordProfiles(dataDirectories)
	for _, profile := range profiles {
		var recovery profileRecoveryFile
		path := filepath.Join(profile.dir, "recovery.json")
		if readProfileRecord(path, &recovery, unreadable) {
			if recovery.SchemaVersion != 1 {
				unreadable[path] = true
			} else {
				for _, point := range recovery.Points {
					point.Target.Profile = profile.ref
					points = append(points, point)
				}
				if recovery.Last != "" {
					last[profilePointKey(profile.ref)] = recovery.Last
				}
			}
		}
		var sources profileProvenanceFile
		if readProfileRecord(filepath.Join(profile.dir, "provenance.json"), &sources, unreadable) {
			for _, record := range sources.Records {
				record.Profile = profile.ref
				provenance = append(provenance, record)
			}
		}
	}
	return
}

func pruneRecordReferences(recovery *VersionRecoveryState) {
	if recovery == nil {
		return
	}
	ids := map[string]bool{}
	for _, point := range recovery.Points {
		ids[point.ID] = true
	}
	if !ids[recovery.LastRunning] {
		recovery.LastRunning = ""
	}
	if recovery.Pending != nil && !ids[recovery.Pending.PointID] {
		recovery.Pending = nil
	}
	for profile, id := range recovery.LastByProfile {
		if !ids[id] {
			delete(recovery.LastByProfile, profile)
		}
	}
}

func (m *Manager) loadRecords(directories []DataDirectoryInfo, appState *State) {
	points, last, provenance, unreadable := loadProfileRecords(directories)
	m.recordCache = map[string][]byte{}
	m.unreadableRecords = unreadable
	if m.versionRecovery == nil && len(points) > 0 {
		m.versionRecovery = &VersionRecoveryState{SchemaVersion: 1}
	}
	if m.versionRecovery != nil {
		m.versionRecovery.Points = points
		m.versionRecovery.LastByProfile = last
		pruneRecordReferences(m.versionRecovery)
	}
	m.pluginProvenance = provenance
	var merged State
	if appState != nil {
		merged = *appState
	}
	merged.VersionRecovery = m.versionRecovery
	merged.PluginProvenance = provenance
	if validateState(merged) == nil {
		return
	}
	if m.versionRecovery != nil {
		m.versionRecovery.Points = nil
		m.versionRecovery.LastByProfile = map[string]string{}
		m.versionRecovery.LastRunning = ""
		m.versionRecovery.Pending = nil
	}
	m.pluginProvenance = nil
	profiles, _ := recordProfiles(directories)
	for _, profile := range profiles {
		for _, name := range []string{"recovery.json", "provenance.json"} {
			path := filepath.Join(profile.dir, name)
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				m.unreadableRecords[path] = true
			}
		}
	}
}

func (m *Manager) saveState(ctx context.Context, state State) error {
	if err := validateState(state); err != nil {
		return err
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	profiles, err := recordProfiles(state.DataDirectories)
	if err != nil {
		return err
	}
	points := map[ProfileRef][]storedRestorePoint{}
	provenance := map[ProfileRef][]PluginProvenanceRecord{}
	if state.VersionRecovery != nil {
		for _, point := range state.VersionRecovery.Points {
			points[point.Target.Profile] = append(points[point.Target.Profile], point)
		}
	}
	for _, record := range state.PluginProvenance {
		provenance[record.Profile] = append(provenance[record.Profile], record)
	}
	m.recordMu.Lock()
	defer m.recordMu.Unlock()
	for _, profile := range profiles {
		recovery := profileRecoveryFile{SchemaVersion: 1, Points: append([]storedRestorePoint{}, points[profile.ref]...)}
		if state.VersionRecovery != nil {
			recovery.Last = state.VersionRecovery.LastByProfile[profilePointKey(profile.ref)]
		}
		if err := m.writeProfileRecord(filepath.Join(profile.dir, "recovery.json"), recovery, len(recovery.Points) > 0 || recovery.Last != ""); err != nil {
			return err
		}
		sources := profileProvenanceFile{Records: append([]PluginProvenanceRecord{}, provenance[profile.ref]...)}
		if err := m.writeProfileRecord(filepath.Join(profile.dir, "provenance.json"), sources, len(sources.Records) > 0); err != nil {
			return err
		}
	}
	return m.store.Save(ctx, m.config.StatePath, state)
}

func (m *Manager) writeProfileRecord(path string, value any, hasRecords bool) error {
	if m.unreadableRecords[path] {
		return nil
	}
	if !hasRecords {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil || bytes.Equal(data, m.recordCache[path]) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".record-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	if m.recordCache == nil {
		m.recordCache = map[string][]byte{}
	}
	m.recordCache[path] = data
	return nil
}

package diagnostics

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/logging"
)

type bundleEntry struct {
	name        string
	data        []byte
	modTime     time.Time
	isLogBackup bool
	isWorker    bool
	omitted     bool
}

func (d *Diagnostics) WriteBundle(ctx context.Context, outputPath string) (BundleResult, error) {
	if !filepath.IsAbs(outputPath) {
		return BundleResult{}, errors.New("diagnostics bundle path must be absolute")
	}
	if err := ctx.Err(); err != nil {
		return BundleResult{}, err
	}

	environment := d.environment(ctx)
	environmentData, err := json.MarshalIndent(environment, "", "  ")
	if err != nil {
		return BundleResult{}, err
	}
	entries, err := d.bundleFiles()
	if err != nil {
		return BundleResult{}, err
	}
	entries = append(entries, bundleEntry{name: "environment.json", data: []byte(d.anonymize(string(environmentData)))})

	omitted := make([]string, 0)
	manifestData, err := d.manifest(environment.AppVersion, omitted)
	if err != nil {
		return BundleResult{}, err
	}
	if sizeWithManifest(entries, manifestData) > d.config.MaxBundleBytes {
		for _, index := range backupOrder(entries) {
			omitted = append(omitted, entries[index].name)
			entries[index].omitted = true
			manifestData, err = d.manifest(environment.AppVersion, omitted)
			if err != nil {
				return BundleResult{}, err
			}
			if sizeWithManifest(entries, manifestData) <= d.config.MaxBundleBytes {
				break
			}
		}
	}
	if sizeWithManifest(entries, manifestData) > d.config.MaxBundleBytes {
		return BundleResult{}, errors.New("diagnostics bundle exceeds the maximum size after removing backups")
	}

	entries = append(entries, bundleEntry{name: "manifest.json", data: manifestData})
	kept := entries[:0]
	for _, entry := range entries {
		if !entry.omitted {
			kept = append(kept, entry)
		}
	}
	size, err := writeBundleZip(outputPath, kept)
	if err != nil {
		return BundleResult{}, err
	}
	return BundleResult{Path: outputPath, Size: size, Omitted: omitted}, nil
}

func (d *Diagnostics) bundleFiles() ([]bundleEntry, error) {
	if d.Dir() == "" {
		return nil, nil
	}
	var entries []bundleEntry
	logEntries, err := os.ReadDir(d.Dir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, file := range logEntries {
		name := file.Name()
		if file.IsDir() || !(strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".log.gz")) {
			continue
		}
		info, err := file.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("stat log file %s: %w", name, err)
		}
		data, err := os.ReadFile(filepath.Join(d.Dir(), name))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read log file %s: %w", name, err)
		}
		bundleName := name
		if strings.HasSuffix(name, ".gz") {
			reader, err := gzip.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, fmt.Errorf("open gzip log %s: %w", name, err)
			}
			data, err = io.ReadAll(reader)
			closeErr := reader.Close()
			if err != nil {
				return nil, fmt.Errorf("read gzip log %s: %w", name, err)
			}
			if closeErr != nil {
				return nil, fmt.Errorf("close gzip log %s: %w", name, closeErr)
			}
			bundleName = strings.TrimSuffix(name, ".gz")
		}
		backup, worker := isLogBackup(bundleName)
		entries = append(entries, bundleEntry{
			name:        path.Join("logs", bundleName),
			data:        []byte(d.anonymize(string(data))),
			modTime:     info.ModTime(),
			isLogBackup: backup,
			isWorker:    worker,
		})
	}

	crashDir := filepath.Join(d.Dir(), logging.CrashDirName)
	crashFiles, err := os.ReadDir(crashDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, file := range crashFiles {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".txt") {
			continue
		}
		info, err := file.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("stat crash file %s: %w", file.Name(), err)
		}
		data, err := os.ReadFile(filepath.Join(crashDir, file.Name()))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read crash file %s: %w", file.Name(), err)
		}
		entries = append(entries, bundleEntry{
			name:    path.Join(logging.CrashDirName, file.Name()),
			data:    []byte(d.anonymize(string(data))),
			modTime: info.ModTime(),
		})
	}

	dshEntries, err := d.dshBundleFiles()
	if err != nil {
		return nil, err
	}
	entries = append(entries, dshEntries...)
	return entries, nil
}

func (d *Diagnostics) dshBundleFiles() ([]bundleEntry, error) {
	now := d.config.Now()
	since := now.Add(-7 * 24 * time.Hour)
	rows, err := readRecords(d.Dir(), logging.DaemonLogFile, since)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		path string
		time time.Time
	}
	latestByPath := make(map[string]time.Time)
	for _, row := range rows {
		if row.message != "dsh startup diagnostics written" || row.time.After(now) {
			continue
		}
		logPath := stringField(row.fields, "dshlog")
		if logPath == "" {
			continue
		}
		info, err := os.Stat(logPath)
		if errors.Is(err, os.ErrNotExist) || err == nil && info.IsDir() {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat DSH log %s: %w", logPath, err)
		}
		if previous, ok := latestByPath[logPath]; !ok || row.time.After(previous) {
			latestByPath[logPath] = row.time
		}
	}
	candidates := make([]candidate, 0, len(latestByPath))
	for logPath, eventTime := range latestByPath {
		candidates = append(candidates, candidate{path: logPath, time: eventTime})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].time.Equal(candidates[j].time) {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].time.After(candidates[j].time)
	})

	entries := make([]bundleEntry, 0, 3)
	seenNames := make(map[string]struct{})
	for _, item := range candidates {
		name := filepath.Base(item.path)
		if name == "." || name == string(filepath.Separator) {
			continue
		}
		if _, exists := seenNames[name]; exists {
			continue
		}
		data, err := os.ReadFile(item.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read DSH log %s: %w", item.path, err)
		}
		info, err := os.Stat(item.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("stat DSH log %s: %w", item.path, err)
		}
		entries = append(entries, bundleEntry{
			name:    path.Join("dsh", name),
			data:    []byte(d.anonymize(string(data))),
			modTime: info.ModTime(),
		})
		seenNames[name] = struct{}{}
		if len(entries) == 3 {
			break
		}
	}
	return entries, nil
}

func isLogBackup(name string) (backup, worker bool) {
	if !strings.HasSuffix(name, ".log") {
		return false, false
	}
	base := strings.TrimSuffix(filepath.Base(name), ".log")
	if !strings.Contains(base, "-") {
		return false, false
	}
	return true, strings.HasPrefix(base, "worker-")
}

func backupOrder(entries []bundleEntry) []int {
	var worker, other []int
	for i, entry := range entries {
		if !entry.isLogBackup {
			continue
		}
		if entry.isWorker {
			worker = append(worker, i)
		} else {
			other = append(other, i)
		}
	}
	oldestFirst := func(indices []int) {
		sort.Slice(indices, func(i, j int) bool {
			left, right := entries[indices[i]], entries[indices[j]]
			if left.modTime.Equal(right.modTime) {
				return left.name < right.name
			}
			return left.modTime.Before(right.modTime)
		})
	}
	oldestFirst(worker)
	oldestFirst(other)
	return append(worker, other...)
}

func (d *Diagnostics) manifest(appVersion string, omitted []string) ([]byte, error) {
	manifest := struct {
		BundleVersion int      `json:"bundleVersion"`
		CreatedAt     string   `json:"createdAt"`
		AppVersion    string   `json:"appVersion"`
		Omitted       []string `json:"omitted"`
	}{
		BundleVersion: 1,
		CreatedAt:     d.config.Now().Format(time.RFC3339),
		AppVersion:    appVersion,
		Omitted:       append([]string{}, omitted...),
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	return []byte(d.anonymize(string(data))), nil
}

func sizeWithManifest(entries []bundleEntry, manifest []byte) int64 {
	total := int64(len(manifest))
	for _, entry := range entries {
		if !entry.omitted {
			total += int64(len(entry.data))
		}
	}
	return total
}

func writeBundleZip(outputPath string, entries []bundleEntry) (int64, error) {
	directory := filepath.Dir(outputPath)
	temporary, err := os.CreateTemp(directory, ".dsh-work-diagnostics-*.zip")
	if err != nil {
		return 0, err
	}
	temporaryPath := temporary.Name()
	keepTemporary := false
	defer func() {
		if !keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	writer := zip.NewWriter(temporary)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		compressed, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			_ = temporary.Close()
			return 0, err
		}
		if _, err := compressed.Write(entry.data); err != nil {
			_ = writer.Close()
			_ = temporary.Close()
			return 0, err
		}
	}
	if err := writer.Close(); err != nil {
		_ = temporary.Close()
		return 0, err
	}
	if err := temporary.Close(); err != nil {
		return 0, err
	}
	info, err := os.Stat(temporaryPath)
	if err != nil {
		return 0, err
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		return 0, err
	}
	keepTemporary = true
	return info.Size(), nil
}

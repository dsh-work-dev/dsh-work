package diagnostics

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type record struct {
	time     time.Time
	timeText string
	level    string
	message  string
	proc     string
	mod      string
	code     string
	gen      string
	op       string
	run      string
	fields   map[string]any
}

func readRecords(dir, base string, since time.Time) ([]record, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	prefix := strings.TrimSuffix(base, ".log") + "-"
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		if name == base || strings.HasPrefix(name, prefix) && (strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".log.gz")) {
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	sort.Strings(paths)

	var records []record
	for _, path := range paths {
		fileRecords, err := readRecordFile(path, since)
		if err != nil {
			return nil, err
		}
		records = append(records, fileRecords...)
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].time.Before(records[j].time) })
	return records, nil
}

func readRecordFile(path string, since time.Time) ([]record, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.ModTime().Before(since) {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if strings.HasSuffix(path, ".gz") {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("open gzip log %s: %w", path, err)
		}
		data, err = io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil {
			return nil, fmt.Errorf("read gzip log %s: %w", path, err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close gzip log %s: %w", path, closeErr)
		}
	}

	return parseRecords(data, since), nil
}

func parseRecords(data []byte, since time.Time) []record {
	lines := bytes.Split(data, []byte{'\n'})
	parsed := make([]record, 0, len(lines))
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(line, &fields); err != nil || fields == nil {
			continue
		}
		timeText, ok := fields["time"].(string)
		if !ok {
			continue
		}
		when, err := time.Parse(time.RFC3339Nano, timeText)
		if err != nil || when.Before(since) {
			continue
		}
		parsed = append(parsed, record{
			time:     when,
			timeText: timeText,
			level:    stringField(fields, "level"),
			message:  stringField(fields, "msg"),
			proc:     stringField(fields, "proc"),
			mod:      stringField(fields, "mod"),
			code:     stringField(fields, "code"),
			gen:      stringField(fields, "gen"),
			op:       stringField(fields, "op"),
			run:      stringField(fields, "run"),
			fields:   fields,
		})
	}
	return parsed
}

func stringField(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return value
}

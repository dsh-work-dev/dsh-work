package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type fileSpec struct {
	name                              string
	maxSizeMB, maxBackups, maxAgeDays int
}

var (
	daemonFile = fileSpec{"daemon.log", 5, 5, 14}
	workerFile = fileSpec{"worker.log", 10, 3, 7}
	uiFile     = fileSpec{"ui.log", 2, 3, 14}
)

const (
	crashKeep        = 10
	crashMaxAge      = 30 * 24 * time.Hour
	pendingLimit     = 200
	earlyLimit       = 1 << 20
	startupLogPrefix = "Full diagnostics: "
)

func (l *Logger) rotateCrash() string {
	dir := filepath.Join(l.dir, "crash")
	current := filepath.Join(dir, string(l.proc)+".current")
	rotated := ""
	if info, err := os.Stat(current); err == nil && info.Size() > 0 {
		base := fmt.Sprintf("%s-%s", l.proc, info.ModTime().Format("20060102-150405"))
		for suffix := 0; ; suffix++ {
			name := base + ".txt"
			if suffix > 0 {
				name = fmt.Sprintf("%s-%d.txt", base, suffix)
			}
			target := filepath.Join(dir, name)
			if _, err := os.Stat(target); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				break
			}
			if os.Rename(current, target) == nil {
				rotated = name
			}
			break
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return rotated
	}
	type crashFile struct {
		name    string
		modTime time.Time
	}
	files := make([]crashFile, 0, len(entries))
	prefix := string(l.proc) + "-"
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".txt") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, crashFile{name: name, modTime: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime.Equal(files[j].modTime) {
			return files[i].name > files[j].name
		}
		return files[i].modTime.After(files[j].modTime)
	})
	cutoff := l.now().Add(-crashMaxAge)
	for i, file := range files {
		if file.name == rotated {
			continue
		}
		if i >= crashKeep || file.modTime.Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, file.name))
		}
	}
	return rotated
}

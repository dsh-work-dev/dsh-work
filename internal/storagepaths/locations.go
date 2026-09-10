// Package storagepaths owns the desktop's storage locations, independently of
// DSH_HOME. A small locator stays at the OS configuration location so moving the
// application store does not make it undiscoverable on the next launch.
package storagepaths

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	copyfs "github.com/otiai10/copy"
)

type Locations struct {
	Root string `json:"root"`
	// Empty means follow Root/user-data, including when Root is moved.
	UserData string `json:"userData"`
}

func (l Locations) UserDataPath() string {
	if l.UserData != "" {
		return l.UserData
	}
	return filepath.Join(l.Root, "user-data")
}

type State struct {
	Current Locations  `json:"current"`
	Pending *Locations `json:"pending,omitempty"`
	Error   string     `json:"error,omitempty"`
}
type Manager struct {
	mu    sync.Mutex
	path  string
	state State
}

func Open(path, defaultRoot string) (*Manager, error) {
	m := &Manager{path: path, state: State{Current: Locations{Root: defaultRoot}}}
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &m.state)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !filepath.IsAbs(m.state.Current.Root) {
		return nil, errors.New("invalid storage root")
	}
	return m, nil
}
func (m *Manager) Snapshot() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state
	if s.Pending != nil {
		p := *s.Pending
		s.Pending = &p
	}
	return s
}
func (m *Manager) Save(next Locations) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	next, err = validate(m.state.Current, next)
	if err != nil {
		return m.state, err
	}
	s := m.state
	s.Error = ""
	s.Pending = &next
	if next == s.Current {
		s.Pending = nil
	}
	if err = m.write(s); err != nil {
		return m.state, err
	}
	m.state = s
	return s, nil
}
func (m *Manager) Cancel() (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state
	s.Pending = nil
	s.Error = ""
	if err := m.write(s); err != nil {
		return m.state, err
	}
	m.state = s
	return s, nil
}
func (m *Manager) write(s State) error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(m.path), ".locations-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), m.path)
}
func same(a, b string) bool {
	if left, e := os.Stat(a); e == nil {
		if right, e := os.Stat(b); e == nil {
			return os.SameFile(left, right)
		}
	}
	rel, err := filepath.Rel(a, b)
	return err == nil && rel == "."
}
func inside(root, path string) bool {
	r, e := filepath.Rel(root, path)
	return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}
func clean(path string) (string, error) {
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) {
		return "", errors.New("请选择绝对路径")
	}
	path = filepath.Clean(path)
	if filepath.Dir(path) == path {
		return "", errors.New("请选择专用文件夹，不能使用磁盘根目录")
	}
	// Resolve existing ancestors, including junctions, before containment checks.
	probe := path
	var suffix []string
	for {
		_, err := os.Lstat(probe)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		suffix = append(suffix, filepath.Base(probe))
		next := filepath.Dir(probe)
		if next == probe {
			return "", err
		}
		probe = next
	}
	resolved, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return "", err
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, suffix[i])
	}
	return resolved, nil
}
func validate(old, next Locations) (Locations, error) {
	var err error
	next.Root, err = clean(next.Root)
	if err != nil {
		return next, err
	}
	if next.UserData != "" {
		next.UserData, err = clean(next.UserData)
		if err != nil {
			return next, err
		}
	}
	if next.UserData != "" && (inside(next.Root, next.UserData) || inside(next.UserData, next.Root)) {
		return next, errors.New("独立用户数据位置必须在 DSH Work 存储位置之外；放在内部请使用默认位置")
	}
	moves := [][2]string{{old.Root, next.Root}, {old.UserDataPath(), next.UserDataPath()}}
	for _, p := range moves {
		if same(p[0], p[1]) {
			continue
		}
		if inside(p[0], p[1]) || inside(p[1], p[0]) {
			return next, errors.New("新旧位置不能互相包含")
		}
	}
	// External user-data must never become part of a relocated application tree.
	if old.UserData != "" && (inside(next.Root, old.UserData) || inside(old.UserData, next.Root)) {
		return next, errors.New("存储位置不能包含现有独立用户数据位置")
	}
	if next.UserData != "" && (inside(old.Root, next.UserData) || inside(next.UserData, old.Root)) {
		return next, errors.New("独立用户数据位置不能包含现有 DSH Work 存储位置")
	}
	for _, p := range moves {
		if same(p[0], p[1]) {
			continue
		}
		entries, e := os.ReadDir(p[1])
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return next, e
		}
		if len(entries) > 0 {
			return next, fmt.Errorf("目标文件夹不是空的：%s", p[1])
		}
	}
	return next, nil
}

type relocation struct{ source, target, stage string }

// ApplyPending is called under the desktop process lock, before opening any
// Worker, settings writer or WebView. Copies are committed only after all data
// and dependency links are prepared. Existing copyfs handles file mechanics;
// our small boundary adds path validation, link rebasing and locator commit.
func (m *Manager) ApplyPending() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Pending == nil {
		return nil
	}
	old := m.state.Current
	next, err := validate(old, *m.state.Pending)
	if err == nil {
		err = m.migrate(old, next)
	}
	if err != nil {
		s := m.state
		s.Error = err.Error()
		s.Pending = nil
		if e := m.write(s); e != nil {
			return errors.Join(err, e)
		}
		m.state = s
	}
	return err
}
func (m *Manager) migrate(old, next Locations) error {
	var moves []relocation
	if !same(old.Root, next.Root) {
		moves = append(moves, relocation{source: old.Root, target: next.Root})
	}
	userMoved := !same(old.UserDataPath(), next.UserDataPath())
	// Default user-data is copied independently to allow default <-> external.
	if userMoved {
		moves = append(moves, relocation{source: old.UserDataPath(), target: next.UserDataPath()})
	}
	mappings := [][2]string{{old.UserDataPath(), next.UserDataPath()}, {old.Root, next.Root}}
	rebase := func(p string) string {
		for _, r := range mappings {
			if inside(r[0], p) {
				rel, _ := filepath.Rel(r[0], p)
				return filepath.Join(r[1], rel)
			}
		}
		return p
	}
	var published []string
	committed := false
	defer func() {
		for _, mv := range moves {
			if mv.stage != "" {
				_ = os.RemoveAll(mv.stage)
			}
		}
		if !committed {
			for i := len(published) - 1; i >= 0; i-- {
				_ = os.RemoveAll(published[i])
			}
		}
	}()
	for i := range moves {
		mv := &moves[i]
		// Stage beside the application destination; default user-data is installed
		// into that staged tree below rather than creating its final parent early.
		parent := filepath.Dir(mv.target)
		if same(mv.target, filepath.Join(next.Root, "user-data")) && !same(old.Root, next.Root) {
			parent = filepath.Dir(next.Root)
		}
		if err := os.MkdirAll(parent, 0700); err != nil {
			return err
		}
		var err error
		mv.stage, err = os.MkdirTemp(parent, ".dsh-work-migrate-*")
		if err != nil {
			return err
		}
		if _, err = os.Stat(mv.source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		err = copyfs.Copy(mv.source, mv.stage, copyfs.Options{Sync: true, Skip: func(info fs.FileInfo, src, dst string) (bool, error) {
			if same(mv.source, old.Root) && (same(src, filepath.Join(old.Root, "manager.lock")) || same(src, old.UserDataPath())) {
				return true, nil
			}
			if info.Mode()&os.ModeSymlink != 0 {
				link, e := os.Readlink(src)
				if e != nil {
					return true, e
				}
				if filepath.IsAbs(link) {
					link = rebase(link)
				}
				if e = os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
					return true, e
				}
				return true, os.Symlink(link, dst)
			}
			return false, nil
		}})
		if err != nil {
			return fmt.Errorf("迁移 %s：%w", mv.source, err)
		}
		if same(mv.source, old.Root) {
			if err = rebaseManager(filepath.Join(mv.stage, "manager.json"), rebase); err != nil {
				return err
			}
		}
	}
	// Publish root first, then its default data child (or the external data root).
	for _, mv := range moves {
		if entries, e := os.ReadDir(mv.target); e == nil && len(entries) > 0 {
			return fmt.Errorf("目标文件夹已被占用：%s", mv.target)
		} else if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if err := os.Remove(mv.target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(mv.stage, mv.target); err != nil {
			return err
		}
		published = append(published, mv.target)
	}
	s := State{Current: next}
	if err := m.write(s); err != nil {
		return err
	}
	m.state = s
	committed = true
	// Commit is durable. Failure to remove the old copy must not revert the new
	// active location or repeat the migration on next launch.
	for i := len(moves) - 1; i >= 0; i-- {
		if err := os.RemoveAll(moves[i].source); err != nil {
			// The caller still owns the old manager lock until ApplyPending
			// returns. Leave that single file for the caller to close/remove.
			if entries, e := os.ReadDir(moves[i].source); e == nil && len(entries) == 1 && entries[0].Name() == "manager.lock" {
				continue
			}
			m.state.Error = "迁移已完成，旧位置未能清理：" + moves[i].source
			_ = m.write(m.state)
		}
	}
	return nil
}
func rebaseManager(path string, rebase func(string) string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var value any
	if err = json.Unmarshal(b, &value); err != nil {
		return err
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			if filepath.IsAbs(x) {
				return rebase(x)
			}
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		case map[string]any:
			for k, v := range x {
				x[k] = walk(v)
			}
		}
		return v
	}
	b, err = json.MarshalIndent(walk(value), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

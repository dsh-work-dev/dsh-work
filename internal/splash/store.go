package splash

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

var (
	ErrUnsupported = errors.New("splash: unsupported file type")
	ErrTooLarge    = errors.New("splash: file too large")
	ErrNotRegular  = errors.New("splash: not a regular file")
	ErrBuiltin     = errors.New("splash: built-in splash cannot be removed")
	ErrNotFound    = errors.New("splash: not found")
	ErrUnavailable = errors.New("splash: storage unavailable")
)

const defaultMaxBytes int64 = 200 << 20

type media struct{ kind, contentType string }

// mediaTypes is the import allowlist. Files are only ever shown through
// <img> or <video>, where SVG runs no script and loads nothing external.
var mediaTypes = map[string]media{
	".png": {"image", "image/png"}, ".jpg": {"image", "image/jpeg"}, ".jpeg": {"image", "image/jpeg"},
	".webp": {"image", "image/webp"}, ".gif": {"image", "image/gif"}, ".avif": {"image", "image/avif"},
	".svg": {"image", "image/svg+xml"}, ".webm": {"video", "video/webm"}, ".mp4": {"video", "video/mp4"},
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Splash is one choice in the splash list.
type Splash struct {
	ID       string `json:"id"`
	Name     string `json:"name"` // empty for built-ins; the frontend names them
	Kind     string `json:"kind"` // scene, image or video
	URL      string `json:"url"`  // "/__splash/<id>" for media, empty for scenes
	Builtin  bool   `json:"builtin"`
	Selected bool   `json:"selected"`
}

type item struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	File string `json:"file"`
}

type catalog struct {
	Version  int    `json:"version"`
	Selected string `json:"selected"`
	Items    []item `json:"items"`
}

// Store keeps the catalog and imported files in one directory.
type Store struct {
	dir      string
	mu       sync.Mutex
	maxBytes int64
	copyFile func(dst io.Writer, src io.Reader) (int64, error)
}

// NewStore never fails: without a directory only the built-ins are offered.
// It removes files left by an interrupted import or catalog write.
func NewStore(dir string) *Store {
	if dir != "" {
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".import-") || strings.HasPrefix(entry.Name(), ".catalog-") {
				_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
			}
		}
	}
	return &Store{dir: dir, maxBytes: defaultMaxBytes}
}

func (s *Store) load() catalog {
	empty := catalog{Version: 1, Selected: BuiltinWhale}
	if s.dir == "" {
		return empty
	}
	data, err := os.ReadFile(filepath.Join(s.dir, "catalog.json"))
	if err != nil {
		return empty
	}
	var c catalog
	if json.Unmarshal(data, &c) != nil || c.Version != 1 {
		return empty
	}
	valid := make([]item, 0, len(c.Items))
	for _, it := range c.Items {
		ext := filepath.Ext(it.File)
		if m, ok := mediaTypes[ext]; ok && idPattern.MatchString(it.ID) && it.File == it.ID+ext && m.kind == it.Kind && s.present(it) {
			valid = append(valid, it)
		}
	}
	c.Items = valid
	return c
}

func (s *Store) present(it item) bool {
	info, err := os.Lstat(filepath.Join(s.dir, it.File))
	return err == nil && info.Mode().IsRegular()
}

func (s *Store) save(c catalog) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".catalog-*.json")
	if err != nil {
		return err
	}
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = os.Rename(tmp.Name(), filepath.Join(s.dir, "catalog.json"))
	}
	if writeErr != nil {
		_ = os.Remove(tmp.Name())
	}
	return writeErr
}

func selected(c catalog) string {
	if isBuiltin(c.Selected) {
		return c.Selected
	}
	for _, it := range c.Items {
		if it.ID == c.Selected {
			return it.ID
		}
	}
	return BuiltinWhale
}

func view(it item, current string) Splash {
	return Splash{ID: it.ID, Name: it.Name, Kind: it.Kind, URL: "/__splash/" + it.ID, Selected: it.ID == current}
}

func builtinView(id, current string) Splash {
	return Splash{ID: id, Kind: "scene", Builtin: true, Selected: id == current}
}

// List returns the built-ins first, then imported files in import order.
func (s *Store) List() ([]Splash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.load()
	current := selected(c)
	out := make([]Splash, 0, len(builtinIDs)+len(c.Items))
	for _, id := range builtinIDs {
		out = append(out, builtinView(id, current))
	}
	for _, it := range c.Items {
		out = append(out, view(it, current))
	}
	return out, nil
}

// Current is the splash to show; anything unusable falls back to the whale.
func (s *Store) Current() Splash {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.load()
	current := selected(c)
	for _, it := range c.Items {
		if it.ID == current {
			return view(it, current)
		}
	}
	return builtinView(current, current)
}

func (s *Store) Select(id string) (Splash, error) {
	if s.dir == "" {
		return Splash{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.load()
	var chosen *item
	for i := range c.Items {
		if c.Items[i].ID == id {
			chosen = &c.Items[i]
		}
	}
	if chosen == nil && !isBuiltin(id) {
		return Splash{}, ErrNotFound
	}
	c.Selected = id
	if err := s.save(c); err != nil {
		return Splash{}, err
	}
	if chosen != nil {
		return view(*chosen, id), nil
	}
	return builtinView(id, id), nil
}

// Import copies one allowlisted image or video into the store. It does not
// change the selection.
func (s *Store) Import(path string) (Splash, error) {
	if s.dir == "" {
		return Splash{}, ErrUnavailable
	}
	ext := strings.ToLower(filepath.Ext(path))
	m, ok := mediaTypes[ext]
	if !ok {
		return Splash{}, ErrUnsupported
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Splash{}, err
	}
	if !info.Mode().IsRegular() {
		return Splash{}, ErrNotRegular
	}
	if info.Size() > s.maxBytes {
		return Splash{}, ErrTooLarge
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return Splash{}, err
	}
	source, err := os.Open(path)
	if err != nil {
		return Splash{}, err
	}
	defer source.Close()
	tmp, err := os.CreateTemp(s.dir, ".import-*")
	if err != nil {
		return Splash{}, err
	}
	copyFile := s.copyFile
	if copyFile == nil {
		copyFile = io.Copy
	}
	written, copyErr := copyFile(tmp, io.LimitReader(source, s.maxBytes+1))
	closeErr := tmp.Close()
	if copyErr == nil && written > s.maxBytes {
		copyErr = ErrTooLarge
	}
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(tmp.Name())
		return Splash{}, copyErr
	}
	id := newID()
	file := id + ext
	if err := os.Rename(tmp.Name(), filepath.Join(s.dir, file)); err != nil {
		_ = os.Remove(tmp.Name())
		return Splash{}, err
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	for utf8.RuneCountInString(name) > 64 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	it := item{ID: id, Name: name, Kind: m.kind, File: file}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.load()
	c.Items = append(c.Items, it)
	if err := s.save(c); err != nil {
		_ = os.Remove(filepath.Join(s.dir, file))
		return Splash{}, err
	}
	return view(it, selected(c)), nil
}

func (s *Store) Remove(id string) error {
	if isBuiltin(id) {
		return ErrBuiltin
	}
	if s.dir == "" {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.load()
	index := -1
	for i, it := range c.Items {
		if it.ID == id {
			index = i
		}
	}
	if index < 0 {
		return ErrNotFound
	}
	file := c.Items[index].File
	c.Items = append(c.Items[:index], c.Items[index+1:]...)
	if c.Selected == id {
		c.Selected = BuiltinWhale
	}
	if err := s.save(c); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(s.dir, file))
	return nil
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

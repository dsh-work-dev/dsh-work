package embeddedbrowser

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

// InstanceConfig is assigned by the daemon's persistent store. Neither PID nor
// window/browser leases choose the persistent browser identity.
type InstanceConfig struct {
	ID        string
	Profile   string
	directory string
}
type TabDescriptor struct {
	ArtifactName string `json:"artifactName,omitempty"`
	Slot         string `json:"slot"`
	URL          string `json:"url"`
	Title        string `json:"title"`
}
type instanceCatalog struct {
	Version int               `json:"version"`
	Keys    map[string]string `json:"instances"`
}
type InstanceStore struct {
	mu           sync.Mutex
	root         string
	lease        io.Closer
	catalog      instanceCatalog
	descriptions map[string]map[string]TabDescriptor
}

// OpenInstanceStore reuses the app's kernel file-lease primitive through a
// narrow injected dependency; it does not introduce another lock implementation.
func OpenInstanceStore(root string, acquire func(string) (io.Closer, error)) (*InstanceStore, error) {
	if !filepath.IsAbs(root) || acquire == nil {
		return nil, errors.New("CEF_INSTANCE_STORE_CONFIG_REQUIRED")
	}
	lease, err := acquire(root)
	if err != nil {
		return nil, fmt.Errorf("CEF_INSTANCE_STORE_BUSY: %w", err)
	}
	s := &InstanceStore{root: root, lease: lease, catalog: instanceCatalog{Version: 1, Keys: map[string]string{}}, descriptions: map[string]map[string]TabDescriptor{}}
	data, err := os.ReadFile(filepath.Join(root, "instances.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		lease.Close()
		return nil, err
	}
	if err = json.Unmarshal(data, &s.catalog); err != nil || s.catalog.Version != 1 || s.catalog.Keys == nil {
		lease.Close()
		return nil, errors.New("CEF_INSTANCE_STORE_INVALID")
	}
	seen := map[string]bool{}
	for key, id := range s.catalog.Keys {
		if !validInstanceKey(key) || !validInstanceID(id) || seen[id] {
			lease.Close()
			return nil, errors.New("CEF_INSTANCE_STORE_INVALID")
		}
		seen[id] = true
	}
	return s, nil
}
func validInstanceKey(key string) bool {
	if len(key) == 0 || len(key) > 64 {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
func validInstanceID(id string) bool {
	if len(id) != 26 {
		return false
	}
	for _, c := range id {
		if !(c >= 'A' && c <= 'Z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}
func (s *InstanceStore) Resolve(key string) (InstanceConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lease == nil {
		return InstanceConfig{}, ErrClosed
	}
	if !validInstanceKey(key) {
		return InstanceConfig{}, errors.New("CEF_INSTANCE_KEY_REJECTED")
	}
	id := s.catalog.Keys[key]
	if id == "" {
		id = rand.Text()
		s.catalog.Keys[key] = id
		if err := writeInstanceJSON(filepath.Join(s.root, "instances.json"), s.catalog); err != nil {
			delete(s.catalog.Keys, key)
			return InstanceConfig{}, err
		}
	}
	directory := filepath.Join(s.root, id)
	return InstanceConfig{ID: id, Profile: filepath.Join(directory, "profile"), directory: directory}, nil
}
func (s *InstanceStore) loadDescriptionsLocked(config InstanceConfig) (map[string]TabDescriptor, error) {
	if !validInstanceID(config.ID) || config.directory != filepath.Join(s.root, config.ID) {
		return nil, errors.New("CEF_INSTANCE_ID_REJECTED")
	}
	if entries, ok := s.descriptions[config.ID]; ok {
		return entries, nil
	}
	entries := map[string]TabDescriptor{}
	data, err := os.ReadFile(filepath.Join(config.directory, "tabs.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if json.Unmarshal(data, &entries) != nil || entries == nil {
			return nil, errors.New("CEF_INSTANCE_DESCRIPTIONS_INVALID")
		}
		for slot, d := range entries {
			safe, e := safeDescriptor(d)
			if e != nil || slot != d.Slot || safe != d {
				return nil, errors.New("CEF_INSTANCE_DESCRIPTIONS_INVALID")
			}
		}
	}
	s.descriptions[config.ID] = entries
	return entries, nil
}
func (s *InstanceStore) Descriptors(config InstanceConfig) ([]TabDescriptor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.loadDescriptionsLocked(config)
	if err != nil {
		return nil, err
	}
	out := make([]TabDescriptor, 0, len(entries))
	for _, d := range entries {
		out = append(out, d)
	}
	return out, nil
}
func (s *InstanceStore) Remember(config InstanceConfig, d TabDescriptor) error {
	safe, err := safeDescriptor(d)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.loadDescriptionsLocked(config)
	if err != nil {
		return err
	}
	old, had := entries[d.Slot]
	if had && old == safe {
		return nil
	}
	entries[d.Slot] = safe
	if err = writeInstanceJSON(filepath.Join(config.directory, "tabs.json"), entries); err != nil {
		if had {
			entries[d.Slot] = old
		} else {
			delete(entries, d.Slot)
		}
		return err
	}
	return nil
}
func (s *InstanceStore) Forget(config InstanceConfig, slot string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.loadDescriptionsLocked(config)
	if err != nil {
		return err
	}
	old, had := entries[slot]
	if !had {
		return nil
	}
	delete(entries, slot)
	if err = writeInstanceJSON(filepath.Join(config.directory, "tabs.json"), entries); err != nil {
		entries[slot] = old
	}
	return err
}
func safeDescriptor(d TabDescriptor) (TabDescriptor, error) {
	if len(d.Slot) == 0 || len(d.Slot) > 1024 || len(d.URL) > 8192 {
		return TabDescriptor{}, errors.New("CEF_DESCRIPTOR_REJECTED")
	}
	if d.ArtifactName != "" {
		if d.URL != "" || len(d.ArtifactName) > 255 || filepath.Base(d.ArtifactName) != d.ArtifactName || strings.ContainsAny(d.ArtifactName, "\\/\x00\r\n") {
			return TabDescriptor{}, errors.New("CEF_DESCRIPTOR_ARTIFACT_REJECTED")
		}
	} else {
		u, err := url.Parse(d.URL)
		if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return TabDescriptor{}, errors.New("CEF_DESCRIPTOR_URL_REJECTED")
		}
		// Persist only a safe navigation description, not token-bearing query/hash,
		// form values, DOM, engine identity, permission or executable state.
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		u.RawFragment = ""
		d.URL = u.String()
	}
	d.Title = strings.Map(func(c rune) rune {
		if c < ' ' || c == 127 {
			return -1
		}
		return c
	}, strings.ToValidUTF8(d.Title, ""))
	if len(d.Title) > 1024 {
		d.Title = d.Title[:1024]
		for !utf8.ValidString(d.Title) {
			d.Title = d.Title[:len(d.Title)-1]
		}
	}
	return d, nil
}
func writeInstanceJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".instance-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
func (s *InstanceStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lease == nil {
		return nil
	}
	err := s.lease.Close()
	if err == nil {
		s.lease = nil
	}
	return err
}

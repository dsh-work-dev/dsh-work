package automation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/browserhost"
	"github.com/local/dsh-work/internal/settings"
)

const BrowserProtocol = 1
const DevelopmentExtensionID = "fpemabjfldbpdmjfdofdnfdaccafobod"

var ErrBrowserOffline = errors.New("BROWSER_OFFLINE")
var ErrBrowserConflict = errors.New("BROWSER_IDENTITY_CONFLICT")
var ErrBrowserBinding = errors.New("BROWSER_BINDING_REQUEST_INVALID")
var ErrBrowserProtocol = errors.New("BROWSER_UPDATE_REQUIRED")

type BrowserHello struct {
	Source           string `json:"source,omitempty"`
	Protocol         int    `json:"protocol"`
	ExtensionVersion string `json:"extensionVersion"`
	ExtensionID      string `json:"extensionId"`
	Instance         string `json:"instance"`
	WireProtocol     int    `json:"wireProtocol,omitempty"`
}

// Native adapters provide browser family after checking their admission context;
// neither browser family nor connection ID is an Agent tool parameter.
type browserConnection struct {
	browser    string
	hello      BrowserHello
	peer       BrowserPeer
	executable string
}
type BrowserBindingRequest struct {
	ID         string    `json:"id"`
	Connection string    `json:"connection"`
	Browser    string    `json:"browser"`
	Name       string    `json:"name"`
	Expires    time.Time `json:"expires"`
}
type BrowserBindingView struct {
	LoadedVersion   string `json:"loadedVersion,omitempty"`
	LoadedSource    string `json:"loadedSource,omitempty"`
	ExpectedSource  string `json:"expectedSource,omitempty"`
	ID              string `json:"id"`
	Browser         string `json:"browser"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	LaunchDirectory string `json:"launchDirectory,omitempty"`
}
type BrowserProfilesSnapshot struct {
	Selected     string                     `json:"selected"`
	Bindings     []BrowserBindingView       `json:"bindings"`
	Pending      []BrowserBindingRequest    `json:"pending"`
	Registration []browserhost.Registration `json:"registration"`
	Discovery    *browserhost.Discovery     `json:"discovery,omitempty"`
	Opening      string                     `json:"opening,omitempty"`
}
type BrowserLease struct {
	Binding    string
	Connection string
	Epoch      uint64
}
type profileState struct {
	prefs        settings.BrowserProfiles
	connections  map[string]browserConnection
	pending      map[string]BrowserBindingRequest
	conflicts    map[string]bool
	registration []browserhost.Registration
	discovery    *browserhost.Discovery
	launcher     browserhost.ProfileLauncher
	opening      string
	launchCancel context.CancelFunc
}

func newProfileState(p *settings.BrowserProfiles) profileState {
	v := profileState{prefs: settings.BrowserProfiles{SchemaVersion: 1}, connections: map[string]browserConnection{}, pending: map[string]BrowserBindingRequest{}, conflicts: map[string]bool{}}
	if p != nil && p.Validate() == nil {
		v.prefs = *p
		v.prefs.Bindings = append([]settings.BrowserBinding(nil), p.Bindings...)
	}
	return v
}
func browserID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}
func instanceKey(browser, extension, instance string) string {
	return browser + "/" + extension + "/" + instance
}
func connectionKey(c browserConnection) string {
	return instanceKey(c.browser, c.hello.ExtensionID, c.hello.Instance)
}
func bindingKey(b settings.BrowserBinding) string {
	return instanceKey(b.Browser, b.ExtensionID, b.Instance)
}
func (s *Service) bindingConnectionLocked(b settings.BrowserBinding) (string, error) {
	k := bindingKey(b)
	if s.profiles.conflicts[k] {
		return "", ErrBrowserConflict
	}
	for id, c := range s.profiles.connections {
		if connectionKey(c) == k {
			return id, nil
		}
	}
	return "", ErrBrowserOffline
}
func (s *Service) selectedConnectionLocked() (string, error) {
	if s.root.Err() != nil {
		return "", ErrRevoked
	}
	for _, b := range s.profiles.prefs.Bindings {
		if b.ID == s.profiles.prefs.Selected {
			return s.bindingConnectionLocked(b)
		}
	}
	return "", ErrBrowserOffline
}
func (s *Service) profileSnapshotLocked() BrowserProfilesSnapshot {
	v := BrowserProfilesSnapshot{Selected: s.profiles.prefs.Selected, Bindings: []BrowserBindingView{}, Pending: []BrowserBindingRequest{}}
	v.Registration = append([]browserhost.Registration(nil), s.profiles.registration...)
	v.Opening = s.profiles.opening
	if s.profiles.discovery != nil {
		copy := *s.profiles.discovery
		v.Discovery = &copy
	}
	for _, b := range s.profiles.prefs.Bindings {
		status := "connected"
		if _, e := s.bindingConnectionLocked(b); e != nil {
			status = "offline"
			if errors.Is(e, ErrBrowserConflict) {
				status = "conflict"
			}
		}
		view := BrowserBindingView{ID: b.ID, Browser: b.Browser, Name: b.Name, Status: status}
		if s.gate != nil {
			view.ExpectedSource = s.gate.config.ExtensionSource
		}
		if id, e := s.bindingConnectionLocked(b); e == nil {
			view.LoadedSource = s.profiles.connections[id].hello.Source
			view.LoadedVersion = s.profiles.connections[id].hello.ExtensionVersion
		}

		if b.Launch != nil {
			view.LaunchDirectory = filepath.Join(b.Launch.UserDataDirectory, b.Launch.ProfileDirectory)
		}
		v.Bindings = append(v.Bindings, view)
	}
	for _, p := range s.profiles.pending {
		if time.Now().Before(p.Expires) {
			v.Pending = append(v.Pending, p)
		}
	}
	sort.Slice(v.Pending, func(i, j int) bool { return v.Pending[i].ID < v.Pending[j].ID })
	return v
}

func (s *Service) BrowserRegistration(result []browserhost.Registration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles.registration = append([]browserhost.Registration(nil), result...)
	s.publishLocked()
}
func (s *Service) profileRouteLocked() string {
	id, e := s.selectedConnectionLocked()
	if e != nil {
		return e.Error()
	}
	return id
}
func (s *Service) profileChangeLocked(previous string) []*entry {
	var old []*entry
	if previous != s.profileRouteLocked() {
		s.slots[Browser].state.Epoch++
		old = s.revokeLocked(Browser, "")
	}
	s.publishLocked()
	return old
}

// BrowserConnected admits metadata only; setup and execution require separate
// decisions. A duplicate identity quarantines ALL its connections until all
// disconnect, so dropping one duplicate cannot silently restore an old route.
func (s *Service) BrowserConnected(browser string, h BrowserHello) (string, error) {
	return s.connectBrowser(browser, h, nil, "")
}

// ConnectBrowserPeer publishes the transport and connection atomically. build
// may only allocate the adapter; it must not do I/O or re-enter Service.
func (s *Service) ConnectBrowserPeer(browser string, h BrowserHello, build func(string) BrowserPeer) (string, error) {
	if h.WireProtocol != 2 || build == nil {
		return "", ErrBrowserProtocol
	}
	return s.connectBrowser(browser, h, build, "")
}
func (s *Service) ConnectBrowserHost(browser, executable string, h BrowserHello, build func(string) BrowserPeer) (string, error) {
	if h.WireProtocol != 2 || build == nil {
		return "", ErrBrowserProtocol
	}
	return s.connectBrowser(browser, h, build, executable)
}
func (s *Service) connectBrowser(browser string, h BrowserHello, build func(string) BrowserPeer, executable string) (string, error) {
	if h.Protocol != BrowserProtocol || !(h.ExtensionVersion == "0.1.0" && h.WireProtocol == 0 || h.ExtensionVersion == "0.2.0" && h.WireProtocol == 2) {
		return "", ErrBrowserProtocol
	}
	if (browser != "chrome" && browser != "edge") || !settings.ValidExtensionID(h.ExtensionID) || !settings.ValidBrowserID(h.Instance) {
		return "", ErrBrowserBinding
	}
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	if s.root.Err() != nil {
		s.mu.Unlock()
		return "", ErrRevoked
	}
	if len(s.profiles.connections) >= 64 {
		s.mu.Unlock()
		return "", errors.New("BROWSER_CONNECTION_LIMIT")
	}
	id, e := browserID()
	if e != nil {
		s.mu.Unlock()
		return "", e
	}
	before := s.profileRouteLocked()
	c := browserConnection{browser: browser, hello: h, executable: executable}
	if build != nil {
		c.peer = build(id)
		if c.peer == nil {
			s.mu.Unlock()
			return "", ErrBrowserProtocol
		}
	}
	k := connectionKey(c)
	for _, other := range s.profiles.connections {
		if connectionKey(other) == k {
			s.profiles.conflicts[k] = true
		}
	}
	s.profiles.connections[id] = c
	if s.profiles.conflicts[k] {
		for key, p := range s.profiles.pending {
			if connectionKey(s.profiles.connections[p.Connection]) == k {
				delete(s.profiles.pending, key)
			}
		}
	}
	old := s.profileChangeLocked(before)
	s.mu.Unlock()
	s.release(old)
	return id, nil
}
func (s *Service) BrowserDisconnected(id string) {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	c, ok := s.profiles.connections[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	before := s.profileRouteLocked()
	delete(s.profiles.connections, id)
	for key, p := range s.profiles.pending {
		if p.Connection == id {
			delete(s.profiles.pending, key)
		}
	}
	k := connectionKey(c)
	still := false
	for _, other := range s.profiles.connections {
		still = still || connectionKey(other) == k
	}
	if !still {
		delete(s.profiles.conflicts, k)
	}
	old := s.profileChangeLocked(before)
	s.mu.Unlock()
	s.release(old)
}
func (s *Service) RequestBrowserBinding(connection, name string) (BrowserBindingRequest, error) {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.profiles.connections[connection]
	if s.root.Err() != nil || !ok {
		return BrowserBindingRequest{}, ErrBrowserBinding
	}
	// The extension's authenticated stable instance is sufficient for binding.
	// This is a display label, not a claim to know the browser's profile name/path.
	if name == "" {
		name = "Profile " + strings.ToUpper(c.hello.Instance[:8])
	}
	if !settings.ValidBrowserName(name) {
		return BrowserBindingRequest{}, ErrBrowserBinding
	}
	if s.profiles.conflicts[connectionKey(c)] {
		return BrowserBindingRequest{}, ErrBrowserConflict
	}
	for _, b := range s.profiles.prefs.Bindings {
		if bindingKey(b) == connectionKey(c) {
			return BrowserBindingRequest{}, errors.New("BROWSER_ALREADY_BOUND")
		}
	}
	for key, p := range s.profiles.pending {
		if p.Connection == connection || !time.Now().Before(p.Expires) {
			delete(s.profiles.pending, key)
		}
	}
	if len(s.profiles.pending) >= 64 {
		return BrowserBindingRequest{}, ErrBrowserBinding
	}
	id, e := browserID()
	if e != nil {
		return BrowserBindingRequest{}, e
	}
	p := BrowserBindingRequest{id, connection, c.browser, name, time.Now().Add(2 * time.Minute)}
	s.profiles.pending[id] = p
	s.publishLocked()
	return p, nil
}
func (s *Service) CancelBrowserBinding(id string) {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.profiles.pending, id)
	s.publishLocked()
}
func (s *Service) saveProfiles(ctx context.Context, p settings.BrowserProfiles) error {
	if s.manager == nil {
		return errors.New("AUTOMATION_SETTINGS_UNAVAILABLE")
	}
	_, e := s.manager.SetBrowserProfiles(ctx, p)
	return e
}

// ConfirmBrowserBinding is a trusted settings action. Candidate IDs refer to
// one live native connection, not a caller-supplied UUID or profile path.
func (s *Service) ConfirmBrowserBinding(ctx context.Context, id string) (string, error) {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	p, ok := s.profiles.pending[id]
	c, live := s.profiles.connections[p.Connection]
	if s.root.Err() != nil || !ok || !live || !time.Now().Before(p.Expires) {
		delete(s.profiles.pending, id)
		s.mu.Unlock()
		return "", ErrBrowserBinding
	}
	if s.profiles.conflicts[connectionKey(c)] {
		s.mu.Unlock()
		return "", ErrBrowserConflict
	}
	for _, b := range s.profiles.prefs.Bindings {
		if bindingKey(b) == connectionKey(c) {
			s.mu.Unlock()
			return "", ErrBrowserBinding
		}
	}
	key, e := browserID()
	if e != nil {
		s.mu.Unlock()
		return "", e
	}
	prefs := s.profiles.prefs
	prefs.Bindings = append(append([]settings.BrowserBinding(nil), prefs.Bindings...), settings.BrowserBinding{ID: key, Browser: c.browser, ExtensionID: c.hello.ExtensionID, Instance: c.hello.Instance, Name: p.Name})
	s.mu.Unlock()
	if e = s.saveProfiles(ctx, prefs); e != nil {
		return "", e
	}
	s.mu.Lock()
	s.profiles.prefs = prefs
	delete(s.profiles.pending, id)
	s.publishLocked()
	s.mu.Unlock()
	return key, nil
}
func (s *Service) SelectBrowserBinding(ctx context.Context, id string) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	if s.root.Err() != nil {
		s.mu.Unlock()
		return ErrRevoked
	}
	prefs := s.profiles.prefs
	valid := id == ""
	for _, b := range prefs.Bindings {
		valid = valid || b.ID == id
	}
	if !valid {
		s.mu.Unlock()
		return ErrBrowserBinding
	}
	if prefs.Selected == id {
		s.mu.Unlock()
		return nil
	}
	prefs.Selected = id
	s.mu.Unlock()
	if e := s.saveProfiles(ctx, prefs); e != nil {
		return e
	}
	s.mu.Lock()
	s.profiles.prefs = prefs
	s.cancelBrowserOpeningLocked()
	// Selection is also a policy boundary even when both selections are offline.
	s.slots[Browser].state.Epoch++
	old := s.revokeLocked(Browser, "")
	s.publishLocked()
	s.mu.Unlock()
	s.release(old)
	return nil
}

// RenameBrowserBinding changes only the user's label. Identity, selection and
// active leases remain unchanged, including when another binding has this name.
func (s *Service) RenameBrowserBinding(ctx context.Context, id, name string) error {
	if !settings.ValidBrowserName(name) {
		return errors.New("BROWSER_BINDING_NAME_INVALID")
	}
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	if s.root.Err() != nil {
		s.mu.Unlock()
		return ErrRevoked
	}
	prefs := s.profiles.prefs
	prefs.Bindings = append([]settings.BrowserBinding(nil), prefs.Bindings...)
	found := false
	for i := range prefs.Bindings {
		if prefs.Bindings[i].ID == id {
			prefs.Bindings[i].Name = name
			found = true
			break
		}
	}
	s.mu.Unlock()
	if !found {
		return ErrBrowserBinding
	}
	if err := s.saveProfiles(ctx, prefs); err != nil {
		return err
	}
	s.mu.Lock()
	s.profiles.prefs = prefs
	s.publishLocked()
	s.mu.Unlock()
	return nil
}
func (s *Service) ForgetBrowserBinding(ctx context.Context, id string) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	if s.root.Err() != nil {
		s.mu.Unlock()
		return ErrRevoked
	}
	prefs := s.profiles.prefs
	prefs.Bindings = nil
	found := false
	for _, b := range s.profiles.prefs.Bindings {
		if b.ID == id {
			found = true
		} else {
			prefs.Bindings = append(prefs.Bindings, b)
		}
	}
	if !found {
		s.mu.Unlock()
		return ErrBrowserBinding
	}
	selected := prefs.Selected == id
	if selected {
		prefs.Selected = ""
	}
	s.mu.Unlock()
	if e := s.saveProfiles(ctx, prefs); e != nil {
		return e
	}
	s.mu.Lock()
	s.profiles.prefs = prefs
	var old []*entry
	if selected {
		s.cancelBrowserOpeningLocked()
		s.slots[Browser].state.Epoch++
		old = s.revokeLocked(Browser, "")
	}
	s.publishLocked()
	s.mu.Unlock()
	s.release(old)
	return nil
}
func (s *Service) SelectedBrowserConnection() (BrowserLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, e := s.selectedConnectionLocked()
	if e != nil {
		return BrowserLease{}, e
	}
	return BrowserLease{s.profiles.prefs.Selected, id, s.slots[Browser].state.Epoch}, nil
}
func (s *Service) ValidateBrowserLease(l BrowserLease) error {
	current, e := s.SelectedBrowserConnection()
	if e != nil {
		return e
	}
	if current != l {
		return ErrRevoked
	}
	return nil
}

// BrowserConnectionStatus projects only this connection's binding; an unbound
// extension cannot inspect other profiles, pending candidates or session state.
func (s *Service) BrowserConnectionStatus(id string) BrowserBindingView {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.profiles.connections[id]
	if !ok || s.root.Err() != nil {
		return BrowserBindingView{Status: "offline"}
	}
	status := "connected"
	if s.profiles.conflicts[connectionKey(c)] {
		status = "conflict"
	}
	for _, b := range s.profiles.prefs.Bindings {
		if bindingKey(b) == connectionKey(c) {
			return BrowserBindingView{ID: b.ID, Browser: b.Browser, Name: b.Name, Status: status}
		}
	}
	if status == "conflict" {
		return BrowserBindingView{Browser: c.browser, Status: status}
	}
	return BrowserBindingView{Browser: c.browser, Status: "unbound"}
}

func (s *Service) BrowserDiscovery(v browserhost.Discovery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles.discovery = &v
	s.publishLocked()
}

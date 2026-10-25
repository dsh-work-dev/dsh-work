// Package dshinteraction tracks pending interactions reported by DSH pages.
// It has no transport or desktop-framework dependencies.
package dshinteraction

import (
	"errors"
	"sort"
	"sync"
)

// Interaction is the current pending interaction for one DSH session.
type Interaction struct {
	Kind     string
	Key      string
	ToolName string
}

// Report is the state sent by one DSH page to the trusted shell.
type Report struct {
	Instance  string `json:"instance"`
	SessionID string `json:"sessionId"`
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	ToolName  string `json:"toolName"`
	Current   string `json:"current"`
	Visible   bool   `json:"visible"`
}

type page struct {
	instance     string
	current      string
	visible      bool
	interactions map[string]Interaction
}

type change struct {
	added       bool
	surface     string
	instance    string
	sessionID   string
	interaction Interaction
}

// Store owns one current DSH page and its interaction snapshot per surface.
type Store struct {
	mu          sync.Mutex
	pages       map[string]*page
	owners      map[string]string
	onAdded     []func(surface, instance, sessionID string, it Interaction)
	onRemoved   []func(instance, sessionID string, it Interaction)
	changes     []change
	dispatching bool
}

// New creates an empty interaction store.
func New() *Store {
	return &Store{pages: make(map[string]*page), owners: make(map[string]string)}
}

// OnAdded registers a synchronous callback for a newly observed interaction key.
func (s *Store) OnAdded(callback func(surface, instance, sessionID string, it Interaction)) {
	if callback == nil {
		return
	}
	s.mu.Lock()
	s.onAdded = append(s.onAdded, callback)
	s.mu.Unlock()
}

// OnRemoved registers a synchronous callback for a removed interaction.
func (s *Store) OnRemoved(callback func(instance, sessionID string, it Interaction)) {
	if callback == nil {
		return
	}
	s.mu.Lock()
	s.onRemoved = append(s.onRemoved, callback)
	s.mu.Unlock()
}

// Report updates the page and interaction identified by the trusted surface.
func (s *Store) Report(surface string, report Report) error {
	s.mu.Lock()
	if owner, ok := s.owners[report.Instance]; ok && owner != surface {
		s.mu.Unlock()
		return errors.New("interaction instance belongs to another surface")
	}
	current := s.pages[surface]
	if current != nil && current.instance != report.Instance {
		s.removePageLocked(surface, current)
		current = nil
	}
	if current == nil {
		current = &page{instance: report.Instance, interactions: make(map[string]Interaction)}
		s.pages[surface] = current
		s.owners[report.Instance] = surface
	}
	if report.SessionID == "" {
		current.current = report.Current
		current.visible = report.Visible
	} else if report.Kind == "" {
		s.removeInteractionLocked(current, report.Instance, report.SessionID)
	} else {
		interaction := Interaction{Kind: report.Kind, Key: report.Key, ToolName: report.ToolName}
		previous, exists := current.interactions[report.SessionID]
		current.interactions[report.SessionID] = interaction
		if !exists || previous.Key != interaction.Key {
			if exists {
				s.changes = append(s.changes, change{instance: report.Instance, sessionID: report.SessionID, interaction: previous})
			}
			s.changes = append(s.changes, change{added: true, surface: surface, instance: report.Instance, sessionID: report.SessionID, interaction: interaction})
		}
	}
	s.mu.Unlock()
	s.dispatch()
	return nil
}

// Reset removes the page only when instance is still owned by surface.
func (s *Store) Reset(surface, instance string) {
	s.mu.Lock()
	if current := s.pages[surface]; current != nil && current.instance == instance {
		s.removePageLocked(surface, current)
	}
	s.mu.Unlock()
	s.dispatch()
}

// ClearWindow removes the page and interactions owned by surface.
func (s *Store) ClearWindow(surface string) {
	s.mu.Lock()
	if current := s.pages[surface]; current != nil {
		s.removePageLocked(surface, current)
	}
	s.mu.Unlock()
	s.dispatch()
}

// ClearAll removes every page and interaction.
func (s *Store) ClearAll() {
	s.mu.Lock()
	surfaces := make([]string, 0, len(s.pages))
	for surface := range s.pages {
		surfaces = append(surfaces, surface)
	}
	sort.Strings(surfaces)
	for _, surface := range surfaces {
		s.removePageLocked(surface, s.pages[surface])
	}
	s.mu.Unlock()
	s.dispatch()
}

// Owner returns the surface that currently owns instance.
func (s *Store) Owner(instance string) (surface string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	surface, ok = s.owners[instance]
	return surface, ok
}

// Page returns the current page state for surface.
func (s *Store) Page(surface string) (instance, current string, visible bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.pages[surface]
	if value == nil {
		return "", "", false, false
	}
	return value.instance, value.current, value.visible, true
}

func (s *Store) removePageLocked(surface string, current *page) {
	sessionIDs := make([]string, 0, len(current.interactions))
	for sessionID := range current.interactions {
		sessionIDs = append(sessionIDs, sessionID)
	}
	sort.Strings(sessionIDs)
	for _, sessionID := range sessionIDs {
		s.changes = append(s.changes, change{instance: current.instance, sessionID: sessionID, interaction: current.interactions[sessionID]})
	}
	delete(s.owners, current.instance)
	delete(s.pages, surface)
}

func (s *Store) removeInteractionLocked(current *page, instance, sessionID string) {
	interaction, ok := current.interactions[sessionID]
	if !ok {
		return
	}
	delete(current.interactions, sessionID)
	s.changes = append(s.changes, change{instance: instance, sessionID: sessionID, interaction: interaction})
}

// dispatch drains callbacks in state-change order. The queue lets callbacks
// re-enter the store without holding its state lock or recursively dispatching.
func (s *Store) dispatch() {
	s.mu.Lock()
	if s.dispatching {
		s.mu.Unlock()
		return
	}
	s.dispatching = true
	s.mu.Unlock()

	for {
		s.mu.Lock()
		if len(s.changes) == 0 {
			s.dispatching = false
			s.mu.Unlock()
			return
		}
		item := s.changes[0]
		s.changes[0] = change{}
		s.changes = s.changes[1:]
		added := make([]func(surface, instance, sessionID string, it Interaction), len(s.onAdded))
		copy(added, s.onAdded)
		removed := make([]func(instance, sessionID string, it Interaction), len(s.onRemoved))
		copy(removed, s.onRemoved)
		s.mu.Unlock()

		if item.added {
			for _, callback := range added {
				callback(item.surface, item.instance, item.sessionID, item.interaction)
			}
		} else {
			for _, callback := range removed {
				callback(item.instance, item.sessionID, item.interaction)
			}
		}
	}
}

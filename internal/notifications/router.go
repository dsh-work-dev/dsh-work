// Package notifications owns dsh-work's notification vocabulary and delivery
// policy. It deliberately has no Wails, window-handle or DSH DOM dependency.
package notifications

import (
	"context"
	"errors"
	"strings"
	"sync"
	"unicode/utf8"
)

type Class string

const (
	ClassActionRequired Class = "action-required"
	ClassCompleted      Class = "completed"
	ClassError          Class = "error"
	ClassLifecycle      Class = "lifecycle"
)

var classPreferenceRules = map[Class]func(Preferences) bool{
	ClassActionRequired: func(preferences Preferences) bool { return preferences.InteractionRequired },
	ClassCompleted:      func(preferences Preferences) bool { return preferences.Completed },
	ClassError:          func(preferences Preferences) bool { return preferences.Errors },
	ClassLifecycle:      func(preferences Preferences) bool { return preferences.Lifecycle },
}

type PreferenceKey string

const (
	PreferenceEnabled             PreferenceKey = "enabled"
	PreferenceCompleted           PreferenceKey = "completed"
	PreferenceInteractionRequired PreferenceKey = "interactionRequired"
	PreferenceErrors              PreferenceKey = "errors"
	PreferenceLifecycle           PreferenceKey = "lifecycle"
)

// Preferences is persisted by dsh-work and projected to the trusted Settings
// surface. Defaults keep meaningful task and error signals available while
// leaving routine lifecycle noise off.
type Preferences struct {
	Enabled             bool `json:"enabled"`
	Completed           bool `json:"completed"`
	InteractionRequired bool `json:"interactionRequired"`
	Errors              bool `json:"errors"`
	Lifecycle           bool `json:"lifecycle"`
}

func DefaultPreferences() Preferences {
	return Preferences{
		Enabled:             true,
		Completed:           true,
		InteractionRequired: true,
		Errors:              true,
		Lifecycle:           false,
	}
}

func (p Preferences) Allows(class Class) bool {
	if !p.Enabled {
		return false
	}
	rule, ok := classPreferenceRules[class]
	return ok && rule(p)
}

func (p Preferences) Set(key PreferenceKey, enabled bool) (Preferences, bool) {
	switch key {
	case PreferenceEnabled:
		p.Enabled = enabled
	case PreferenceCompleted:
		p.Completed = enabled
	case PreferenceInteractionRequired:
		p.InteractionRequired = enabled
	case PreferenceErrors:
		p.Errors = enabled
	case PreferenceLifecycle:
		p.Lifecycle = enabled
	default:
		return p, false
	}
	return p, true
}

type Event struct {
	ID                        string
	Class                     Class
	Title                     string
	Body                      string
	Target                    string
	AllowWhenWorkspaceFocused bool
}

type Delivery interface {
	Send(context.Context, Event) error
}

var (
	ErrInvalidEvent = errors.New("notification event is invalid")
	ErrNoDelivery   = errors.New("notification delivery is unavailable")
)

type DeliveryFailureHandler func(Event)

// Router applies preference, foreground and per-session deduplication rules
// before handing an event to the platform adapter. A logical event is marked
// as consumed before delivery so a native adapter failure cannot create a
// duplicate if the source retries the same event.
type Router struct {
	mu          sync.Mutex
	preferences Preferences
	delivery    Delivery
	isActive    func() bool
	onFailure   DeliveryFailureHandler
	seen        map[string]struct{}
}

const maxRememberedEvents = 512

func NewRouter(delivery Delivery, isActive func() bool) *Router {
	return &Router{
		preferences: DefaultPreferences(),
		delivery:    delivery,
		isActive:    isActive,
		seen:        make(map[string]struct{}),
	}
}

func (r *Router) SetPreferences(preferences Preferences) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.preferences = preferences
	r.mu.Unlock()
}

func (r *Router) Preferences() Preferences {
	if r == nil {
		return DefaultPreferences()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.preferences
}

func (r *Router) SetDeliveryFailureHandler(handler DeliveryFailureHandler) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.onFailure = handler
	r.mu.Unlock()
}

func (r *Router) Publish(ctx context.Context, event Event) error {
	if r == nil {
		return ErrNoDelivery
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	event, err := normalizeEvent(event)
	if err != nil {
		return err
	}

	r.mu.Lock()
	if _, exists := r.seen[event.ID]; exists {
		r.mu.Unlock()
		return nil
	}
	if len(r.seen) >= maxRememberedEvents {
		// Fail closed rather than evicting an old ID. Eviction would allow a
		// replayed logical event to create a second desktop delivery.
		r.mu.Unlock()
		return nil
	}
	r.seen[event.ID] = struct{}{}
	allowed := r.preferences.Allows(event.Class)
	isActive := r.isActive
	delivery := r.delivery
	onFailure := r.onFailure
	r.mu.Unlock()
	if !allowed {
		return nil
	}
	if isActive != nil && isActive() &&
		(!event.AllowWhenWorkspaceFocused || (event.Class != ClassActionRequired && event.Class != ClassError)) {
		return nil
	}
	if delivery == nil {
		if onFailure != nil {
			onFailure(event)
		}
		return ErrNoDelivery
	}
	err = delivery.Send(ctx, event)
	if err != nil && onFailure != nil {
		onFailure(event)
	}
	return err
}

func normalizeEvent(event Event) (Event, error) {
	event.ID = strings.TrimSpace(event.ID)
	event.Title = cleanText(event.Title, 120)
	event.Body = cleanText(event.Body, 400)
	event.Target = cleanText(event.Target, 80)
	if event.ID == "" || utf8.RuneCountInString(event.ID) > 160 || event.Class == "" || event.Title == "" || !validClass(event.Class) {
		return Event{}, ErrInvalidEvent
	}
	return event, nil
}

func validClass(class Class) bool {
	_, ok := classPreferenceRules[class]
	return ok
}

func cleanText(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes])
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

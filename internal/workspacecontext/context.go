package workspacecontext

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// State describes what the DSH Workspace surface must do for one session.
// SelectionRequired is an explicit result, not a fallback to a process
// directory or another Work-owned path.
type State string

const (
	StateSelected          State = "selected"
	StateSelectionRequired State = "selection-required"
)

// Request is supplied by an explicit launch/session action. The DSH Workspace
// surface is the source of the stable ID and canonical directory when it has
// already resolved a Workspace.
type Request struct {
	ID    string `json:"id,omitempty"`
	Path  string `json:"path,omitempty"`
	Title string `json:"title,omitempty"`
}

// Context is intentionally per-generation. It is never part of the durable
// Work Run context or the global manager state.
type Context struct {
	GenerationID string `json:"generationId"`
	State        State  `json:"state"`
	ID           string `json:"id,omitempty"`
	Path         string `json:"path,omitempty"`
	Title        string `json:"title,omitempty"`
}

// Resolver obtains a DSH-owned Workspace context for one Worker generation.
// Implementations may call DSH's Workspace seam; they must not persist the
// returned context in Work's global configuration.
type Resolver interface {
	Resolve(context.Context, string, Request) (Context, error)
}

// ResolverFunc adapts a function to Resolver.
type ResolverFunc func(context.Context, string, Request) (Context, error)

func (f ResolverFunc) Resolve(ctx context.Context, generationID string, request Request) (Context, error) {
	return f(ctx, generationID, request)
}

// SurfaceResolver is the default boundary for the current DSH integration.
// With no explicit request it tells DSH to present its Workspace
// selection/creation surface. A request from that surface is validated and
// carried only for the current generation.
type SurfaceResolver struct{}

func (SurfaceResolver) Resolve(ctx context.Context, generationID string, request Request) (Context, error) {
	if err := contextError(ctx); err != nil {
		return Context{}, err
	}
	if strings.TrimSpace(generationID) == "" {
		return Context{}, errors.New("workspace generation ID is required")
	}
	if strings.TrimSpace(request.ID) == "" && strings.TrimSpace(request.Path) == "" && strings.TrimSpace(request.Title) == "" {
		return Context{GenerationID: generationID, State: StateSelectionRequired}, nil
	}
	return NewSelected(generationID, request.ID, request.Path, request.Title)
}

// NewSelected creates a validated context from a DSH Workspace selection.
// Paths are canonicalized with the same existing-directory boundary used by
// DSH's Workspace contract; no directory is created or moved here.
func NewSelected(generationID, id, path, title string) (Context, error) {
	if strings.TrimSpace(generationID) == "" {
		return Context{}, errors.New("workspace generation ID is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Context{}, errors.New("workspace ID is required")
	}
	if !filepath.IsAbs(strings.TrimSpace(path)) {
		return Context{}, errors.New("workspace path must be absolute")
	}
	canonical, err := canonicalDirectory(path)
	if err != nil {
		return Context{}, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = filepath.Base(canonical)
	}
	return Context{
		GenerationID: generationID,
		State:        StateSelected,
		ID:           id,
		Path:         canonical,
		Title:        title,
	}, nil
}

// ValidateForGeneration ensures a context cannot be replayed into another
// Worker generation or smuggle an uncanonical/nonexistent directory across
// the Host–Adapter boundary.
func (c Context) ValidateForGeneration(generationID string) error {
	if strings.TrimSpace(generationID) == "" || c.GenerationID != generationID {
		return errors.New("workspace context belongs to another generation")
	}
	switch c.State {
	case StateSelectionRequired:
		if c.ID != "" || c.Path != "" || c.Title != "" {
			return errors.New("workspace selection-required context must not contain a workspace record")
		}
		return nil
	case StateSelected:
		if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Title) == "" {
			return errors.New("selected workspace context requires an ID and title")
		}
		canonical, err := canonicalDirectory(c.Path)
		if err != nil {
			return err
		}
		if canonical != c.Path {
			return errors.New("selected workspace path is not canonical")
		}
		return nil
	default:
		return fmt.Errorf("unsupported workspace context state %q", c.State)
	}
}

func canonicalDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("workspace path is required")
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("workspace path must be absolute")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("stat workspace path: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("workspace path is not a directory")
	}
	return filepath.Clean(canonical), nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

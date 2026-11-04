package automation

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/local/dsh-work/internal/settings"
)

// ConfigureBrowserBackend installs a provider, never an authorization policy.
// New() stays closed until the daemon installs the separate Go authorization gateway.
func (s *Service) ConfigureBrowserBackend(factory Factory, schemas []json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.browserFactory = factory
	s.browserSchemas = nil
	for _, schema := range schemas {
		s.browserSchemas = append(s.browserSchemas, append(json.RawMessage(nil), schema...))
	}
	s.publishLocked()
}

// NewDevelopmentAcceptance is used only by the explicit scratch acceptance
// executable. It constructs its own settings scope under an owned Temp root;
// no environment variable or normal daemon can opt an existing Service in.
// This is not the step-5 product authorization interface.
func NewDevelopmentAcceptance(root string, prepare Preparer) (*Service, *settings.Manager, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, nil, err
	}
	temp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return nil, nil, err
	}
	rel, err := filepath.Rel(temp, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, nil, errors.New("BROWSER_ACCEPTANCE_REQUIRES_SCRATCH")
	}
	marker, err := os.ReadFile(filepath.Join(resolved, "browser-acceptance-owner.txt"))
	if err != nil || strings.TrimSpace(string(marker)) != "dsh-work-browser-acceptance-v1" {
		return nil, nil, errors.New("BROWSER_ACCEPTANCE_OWNER_REQUIRED")
	}
	m, err := settings.New(settings.Config{Path: filepath.Join(resolved, "settings.json")})
	if err != nil {
		return nil, nil, err
	}
	s := New(m, prepare)
	s.development = true
	return s, m, nil
}

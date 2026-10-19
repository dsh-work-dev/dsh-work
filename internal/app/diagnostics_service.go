package app

import (
	"context"
	"errors"
	"time"

	"github.com/local/dsh-work/internal/diagnostics"
)

// DiagnosticsService serves the problem list, issue reports and diagnostics
// bundles to the trusted workbench window.
type DiagnosticsService struct {
	diagnostics *diagnostics.Diagnostics
	open        func(string) error
	choose      func(defaultName string) (string, error)
}

func NewDiagnosticsService(d *diagnostics.Diagnostics, open func(string) error, choose func(defaultName string) (string, error)) *DiagnosticsService {
	return &DiagnosticsService{diagnostics: d, open: open, choose: choose}
}

func (s *DiagnosticsService) GetProblems(ctx context.Context) ([]diagnostics.Problem, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	return s.diagnostics.Problems(ctx)
}

func (s *DiagnosticsService) GetReport(ctx context.Context, problemID string) (string, error) {
	if err := s.check(ctx); err != nil {
		return "", err
	}
	return s.diagnostics.Report(ctx, problemID)
}

func (s *DiagnosticsService) ExportBundle(ctx context.Context) (diagnostics.BundleResult, error) {
	if err := s.check(ctx); err != nil {
		return diagnostics.BundleResult{}, err
	}
	name := "dsh-work-diagnostics-" + time.Now().Format("20060102-150405") + ".zip"
	path, err := s.choose(name)
	if err != nil {
		return diagnostics.BundleResult{}, err
	}
	if path == "" {
		return diagnostics.BundleResult{}, nil
	}
	return s.diagnostics.WriteBundle(ctx, path)
}

func (s *DiagnosticsService) OpenLogFolder(ctx context.Context) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	dir := s.diagnostics.Dir()
	if dir == "" {
		return errors.New("log folder is unavailable")
	}
	return s.open(dir)
}

func (s *DiagnosticsService) check(ctx context.Context) error {
	if s == nil || s.diagnostics == nil {
		return settingsUnavailable()
	}
	if !isTrustedWindow(ctx, "workspace") {
		return trustedSurfaceRequired("Diagnostics are available in Settings.")
	}
	return nil
}

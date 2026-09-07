package pet

import (
	"fmt"

	"github.com/local/dsh-work/internal/lifecycle"
)

// Diagnostic is both the stable public error shape and a bounded warning
// record. Error messages intentionally contain no package paths, raw JSON or
// filesystem details.
type Diagnostic struct {
	Code          string `json:"code"`
	Summary       string `json:"summary"`
	Retryable     bool   `json:"retryable"`
	CorrelationID string `json:"correlationId"`
}

func (d Diagnostic) Error() string {
	if d.Code == "" {
		return d.Summary
	}
	return fmt.Sprintf("%s: %s", d.Code, d.Summary)
}

const (
	CodeManifestMissing         = "codex.manifest-missing"
	CodeManifestInvalid         = "codex.manifest-invalid"
	CodePathOutsideRoot         = "codex.path-outside-root"
	CodeVersionGeometryMismatch = "codex.version-geometry-mismatch"
	CodeInvalidSpritesheet      = "codex.invalid-spritesheet"
	CodeInvalidAnimation        = "codex.invalid-animation"
	CodeV2SemanticsUnverified   = "codex.v2-semantics-unverified"
	CodeLegacyProfileDisabled   = "codex.legacy-profile-disabled"
	CodePackageUnreadable       = "codex.package-unreadable"
	CodeLoadCanceled            = "codex.load-canceled"
	CodePackageUnsafe           = "codex.package-unsafe"
)

const (
	DiagnosticCodeManifestMissing         = CodeManifestMissing
	DiagnosticCodeManifestInvalid         = CodeManifestInvalid
	DiagnosticCodePathOutsideRoot         = CodePathOutsideRoot
	DiagnosticCodeVersionGeometryMismatch = CodeVersionGeometryMismatch
	DiagnosticCodeInvalidSpritesheet      = CodeInvalidSpritesheet
	DiagnosticCodeInvalidAnimation        = CodeInvalidAnimation
)

func diagnostic(code, summary string, retryable bool) Diagnostic {
	return Diagnostic{Code: code, Summary: summary, Retryable: retryable, CorrelationID: lifecycle.NewCorrelationID()}
}

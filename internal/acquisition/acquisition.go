// Package acquisition coordinates one explicit artifact operation across an
// ordered set of transport routes. Transport grammar and error recognition
// stay in adapters; callers see one terminal result with safe provenance.
package acquisition

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ArtifactKind string

const (
	ArtifactNode   ArtifactKind = "node"
	ArtifactDSH    ArtifactKind = "dsh"
	ArtifactPlugin ArtifactKind = "plugin"
)

type Route string

const (
	RouteOfficial Route = "official"
	RouteMirror   Route = "mirror"
	RouteLocal    Route = "local"
)

type FailureKind string

const (
	FailureReachability   FailureKind = "reachability"
	FailureNotFound       FailureKind = "not-found"
	FailureAuthentication FailureKind = "authentication"
	FailureAuthorization  FailureKind = "authorization"
	FailureIntegrity      FailureKind = "integrity"
	FailureSemantic       FailureKind = "semantic"
	FailureCanceled       FailureKind = "canceled"
	FailureLocalIO        FailureKind = "local-io"
)

// Failure is the adapter-to-coordinator error contract. Summary must be safe
// for persistence and UI projection: it must not contain endpoints or secrets.
type Failure struct {
	Kind    FailureKind
	Summary string
	Cause   error
}

func (f Failure) Error() string {
	if strings.TrimSpace(f.Summary) != "" {
		return f.Summary
	}
	if f.Cause != nil {
		return f.Cause.Error()
	}
	return string(f.Kind)
}

func (f Failure) Unwrap() error { return f.Cause }

type ArtifactIdentity struct {
	Kind         ArtifactKind `json:"kind"`
	Name         string       `json:"name"`
	Version      string       `json:"version,omitempty"`
	Platform     string       `json:"platform,omitempty"`
	Architecture string       `json:"architecture,omitempty"`
	Filename     string       `json:"filename,omitempty"`
}

type SourceCandidate struct {
	Route    Route
	Location string
}

type Request struct {
	OperationID string
	Identity    ArtifactIdentity
	Candidates  []SourceCandidate
	StagingRoot string
	StagePrefix string
}

type AttemptRequest struct {
	OperationID string
	Identity    ArtifactIdentity
	Candidate   SourceCandidate
	StagingPath string
}

type IntegrityEvidence struct {
	Algorithm string `json:"algorithm,omitempty"`
	Digest    string `json:"digest,omitempty"`
}

type AttemptResult struct {
	ResolvedIdentity ArtifactIdentity
	PayloadPath      string
	Integrity        IntegrityEvidence
}

// AttemptAdapter owns one transport- or package-manager-specific attempt.
// Returned failures must be classified at that adapter seam.
type AttemptAdapter interface {
	Attempt(context.Context, AttemptRequest) (AttemptResult, error)
}

type Observer func(Progress)

type Progress struct {
	OperationID string
	Identity    ArtifactIdentity
	Route       Route
	Attempt     int
}

type OperationState string

const (
	OperationActive    OperationState = "active"
	OperationSucceeded OperationState = "succeeded"
	OperationFailed    OperationState = "failed"
	OperationCanceled  OperationState = "canceled"
)

// OperationStatus is the bounded, credential-free projection emitted to the
// trusted Settings surface for one explicit artifact operation.
type OperationStatus struct {
	OperationID   string           `json:"operationId"`
	Artifact      ArtifactIdentity `json:"artifact"`
	State         OperationState   `json:"state"`
	Step          string           `json:"step"`
	Route         Route            `json:"route,omitempty"`
	Attempt       int              `json:"attempt,omitempty"`
	ReceivedBytes int64            `json:"receivedBytes,omitempty"`
	TotalBytes    int64            `json:"totalBytes,omitempty"`
	HasTotal      bool             `json:"hasTotal"`
	CanCancel     bool             `json:"canCancel"`
	Result        *OperationResult `json:"result,omitempty"`
}

type OperationFailure struct {
	Kind      FailureKind `json:"kind"`
	Summary   string      `json:"summary"`
	Retryable bool        `json:"retryable"`
}

type OperationResult struct {
	Succeeded bool              `json:"succeeded"`
	Route     Route             `json:"route,omitempty"`
	Failure   *OperationFailure `json:"failure,omitempty"`
}

type Attempt struct {
	Route     Route       `json:"route"`
	Succeeded bool        `json:"succeeded,omitempty"`
	Failure   FailureKind `json:"failure,omitempty"`
	Summary   string      `json:"summary,omitempty"`
}

type Result struct {
	Identity     ArtifactIdentity  `json:"identity"`
	Route        Route             `json:"route,omitempty"`
	FallbackUsed bool              `json:"fallbackUsed"`
	Attempts     []Attempt         `json:"attempts"`
	Integrity    IntegrityEvidence `json:"integrity,omitempty"`
	StagingPath  string            `json:"-"`
	PayloadPath  string            `json:"-"`
}

// Acquire owns source ordering, mirror eligibility, per-attempt staging and
// one terminal result. It never retries a terminal semantic or integrity
// failure and never leaks candidate locations into Result.
func Acquire(ctx context.Context, request Request, adapter AttemptAdapter, observer Observer) (Result, error) {
	result := Result{Identity: request.Identity, Attempts: make([]Attempt, 0, len(request.Candidates))}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateRequest(request, adapter); err != nil {
		return result, err
	}
	for index, candidate := range request.Candidates {
		if err := ctx.Err(); err != nil {
			failure := canceledFailure(err)
			return result, failure
		}
		stage, err := os.MkdirTemp(request.StagingRoot, request.StagePrefix)
		if err != nil {
			failure := Failure{Kind: FailureLocalIO, Summary: "acquisition staging could not be prepared", Cause: err}
			return result, failure
		}
		if observer != nil {
			observer(Progress{OperationID: request.OperationID, Identity: request.Identity, Route: candidate.Route, Attempt: index + 1})
		}
		attemptResult, attemptErr := adapter.Attempt(ctx, AttemptRequest{
			OperationID: request.OperationID,
			Identity:    request.Identity,
			Candidate:   candidate,
			StagingPath: stage,
		})
		if attemptErr == nil {
			resolved := attemptResult.ResolvedIdentity
			if resolved.Kind == "" {
				resolved = request.Identity
			}
			if resolved != request.Identity {
				_ = os.RemoveAll(stage)
				failure := Failure{Kind: FailureIntegrity, Summary: "the acquired artifact identity did not match the request"}
				result.Attempts = append(result.Attempts, Attempt{Route: candidate.Route, Failure: failure.Kind, Summary: failure.Summary})
				return result, failure
			}
			result.Identity = resolved
			result.Route = candidate.Route
			result.FallbackUsed = candidate.Route == RouteMirror
			result.Integrity = attemptResult.Integrity
			result.StagingPath = stage
			result.PayloadPath = attemptResult.PayloadPath
			result.Attempts = append(result.Attempts, Attempt{Route: candidate.Route, Succeeded: true})
			return result, nil
		}

		failure := classifyFailure(ctx, attemptErr)
		result.Attempts = append(result.Attempts, Attempt{Route: candidate.Route, Failure: failure.Kind, Summary: failure.Summary})
		if cleanupErr := os.RemoveAll(stage); cleanupErr != nil {
			return result, Failure{Kind: FailureLocalIO, Summary: "acquisition staging could not be cleaned", Cause: cleanupErr}
		}
		if failure.Kind == FailureReachability && hasEligibleMirror(request.Candidates, index+1) {
			continue
		}
		return result, failure
	}
	return result, Failure{Kind: FailureSemantic, Summary: "acquisition did not produce an artifact"}
}

func validateRequest(request Request, adapter AttemptAdapter) error {
	if adapter == nil {
		return Failure{Kind: FailureSemantic, Summary: "acquisition adapter is unavailable"}
	}
	if strings.TrimSpace(request.OperationID) == "" || request.Identity.Kind == "" || strings.TrimSpace(request.Identity.Name) == "" {
		return Failure{Kind: FailureSemantic, Summary: "acquisition request is invalid"}
	}
	root, err := filepath.Abs(request.StagingRoot)
	if err != nil || strings.TrimSpace(root) == "" {
		return Failure{Kind: FailureLocalIO, Summary: "acquisition staging is unavailable", Cause: err}
	}
	if len(request.Candidates) == 0 || request.Candidates[0].Route != RouteOfficial {
		return Failure{Kind: FailureSemantic, Summary: "acquisition must start with the official source"}
	}
	for index, candidate := range request.Candidates {
		if candidate.Route != RouteOfficial && candidate.Route != RouteMirror && candidate.Route != RouteLocal {
			return Failure{Kind: FailureSemantic, Summary: fmt.Sprintf("acquisition source %d is invalid", index+1)}
		}
		if candidate.Route == RouteMirror && index == 0 {
			return Failure{Kind: FailureSemantic, Summary: "a mirror cannot be the first acquisition source"}
		}
	}
	return nil
}

func classifyFailure(ctx context.Context, err error) Failure {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		return canceledFailure(err)
	}
	var failure Failure
	if errors.As(err, &failure) {
		if failure.Kind == "" {
			failure.Kind = FailureSemantic
		}
		return failure
	}
	return Failure{Kind: FailureSemantic, Summary: "acquisition attempt failed", Cause: err}
}

func canceledFailure(cause error) Failure {
	return Failure{Kind: FailureCanceled, Summary: "acquisition was canceled", Cause: cause}
}

func hasEligibleMirror(candidates []SourceCandidate, start int) bool {
	return start < len(candidates) && candidates[start].Route == RouteMirror
}

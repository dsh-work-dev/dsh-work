//go:build windows

package windows

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshadapter"
)

// packageManagerAcquisitionAdapter owns package-manager command grammar and
// translates its presentation output into the coordinator's typed failures.
type packageManagerAcquisitionAdapter struct {
	executor  dshadapter.CommandExecutor
	toolchain runtimeToolchain
	version   string
}

func (a packageManagerAcquisitionAdapter) Attempt(ctx context.Context, request acquisition.AttemptRequest) (acquisition.AttemptResult, error) {
	result, err := a.executor.Run(
		ctx,
		a.toolchain.packageManagerPath,
		packageInstallArgs(a.toolchain.kind, request.StagingPath, a.version, request.Candidate.Location),
		a.toolchain.env,
		request.StagingPath,
	)
	if err != nil {
		return acquisition.AttemptResult{}, classifyPackageManagerFailure(result, err)
	}
	return acquisition.AttemptResult{
		ResolvedIdentity: request.Identity,
		PayloadPath:      request.StagingPath,
	}, nil
}

// nodeArchiveAcquisitionAdapter owns HTTP/downloader error classification.
// Integrity remains inside the downloader that streams the artifact.
type nodeArchiveAcquisitionAdapter struct {
	downloader artifactDownloader
	sha256     string
	report     func(acquisition.Route, int64, int64, bool)
}

func (a nodeArchiveAcquisitionAdapter) Attempt(ctx context.Context, request acquisition.AttemptRequest) (acquisition.AttemptResult, error) {
	destination := filepath.Join(request.StagingPath, request.Identity.Filename)
	err := a.downloader.Download(ctx, request.Candidate.Location, destination, a.sha256, func(received, total int64, hasTotal bool) {
		if a.report != nil {
			a.report(request.Candidate.Route, received, total, hasTotal)
		}
	})
	if err != nil {
		return acquisition.AttemptResult{}, classifyHTTPFailure(err)
	}
	return acquisition.AttemptResult{
		ResolvedIdentity: request.Identity,
		PayloadPath:      destination,
		Integrity: acquisition.IntegrityEvidence{
			Algorithm: "sha256",
			Digest:    strings.ToLower(strings.TrimSpace(a.sha256)),
		},
	}, nil
}

func classifyPackageManagerFailure(result dshadapter.CommandResult, err error) acquisition.Failure {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return acquisition.Failure{Kind: acquisition.FailureCanceled, Summary: "package acquisition was canceled", Cause: err}
	}
	text := strings.ToLower(result.Stdout + "\n" + result.Stderr + "\n" + errorString(err))
	if containsAny(text, "eintegrity", "integrity", "checksum") {
		return acquisition.Failure{Kind: acquisition.FailureIntegrity, Summary: "package integrity verification failed", Cause: err}
	}
	if containsAny(text, "e401", "401 unauthorized", "authentication required", "unable to authenticate", "self_signed", "certificate") {
		return acquisition.Failure{Kind: acquisition.FailureAuthentication, Summary: "package source authentication failed", Cause: err}
	}
	if containsAny(text, "e403", "403 forbidden", "authorization") {
		return acquisition.Failure{Kind: acquisition.FailureAuthorization, Summary: "package source authorization failed", Cause: err}
	}
	if containsAny(text, "e404", "404 not found", "no matching version", "notarget", " etarget") {
		return acquisition.Failure{Kind: acquisition.FailureNotFound, Summary: "the requested package was not found", Cause: err}
	}
	if containsAny(text,
		"enotfound", "eai_again", "econnrefused", "econnreset", "etimedout", "enetunreach", "ehostunreach",
		"err_socket_timeout", "fetch failed", "no such host", "name or service not known",
		"temporary failure in name resolution", "network is unreachable", "connection reset", "connection refused", "timeout",
	) {
		return acquisition.Failure{Kind: acquisition.FailureReachability, Summary: "package source could not be reached", Cause: err}
	}
	return acquisition.Failure{Kind: acquisition.FailureSemantic, Summary: "package manager rejected the acquisition", Cause: err}
}

func classifyHTTPFailure(err error) acquisition.Failure {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return acquisition.Failure{Kind: acquisition.FailureCanceled, Summary: "artifact acquisition was canceled", Cause: err}
	}
	text := strings.ToLower(errorString(err))
	if errors.Is(err, errArtifactIntegrity) || containsAny(text, "integrity", "checksum") {
		return acquisition.Failure{Kind: acquisition.FailureIntegrity, Summary: "artifact integrity verification failed", Cause: err}
	}
	if containsAny(text, "status 401", "unauthorized", "certificate", "self_signed") {
		return acquisition.Failure{Kind: acquisition.FailureAuthentication, Summary: "artifact source authentication failed", Cause: err}
	}
	if containsAny(text, "status 403", "forbidden") {
		return acquisition.Failure{Kind: acquisition.FailureAuthorization, Summary: "artifact source authorization failed", Cause: err}
	}
	if containsAny(text, "status 404", "not found") {
		return acquisition.Failure{Kind: acquisition.FailureNotFound, Summary: "the requested artifact was not found", Cause: err}
	}
	if containsAny(text, "status 500", "status 502", "status 503", "status 504", "enotfound", "eai_again", "econnrefused", "econnreset", "etimedout", "enetunreach", "ehostunreach", "no such host", "name or service not known", "temporary failure in name resolution", "network is unreachable", "network unreachable", "timeout", "connection reset", "connection refused") {
		return acquisition.Failure{Kind: acquisition.FailureReachability, Summary: "artifact source could not be reached", Cause: err}
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return acquisition.Failure{Kind: acquisition.FailureReachability, Summary: "artifact source could not be reached", Cause: err}
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return acquisition.Failure{Kind: acquisition.FailureLocalIO, Summary: "artifact staging failed", Cause: err}
	}
	return acquisition.Failure{Kind: acquisition.FailureSemantic, Summary: "artifact source rejected the acquisition", Cause: err}
}

func containsAny(value string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func acquisitionFailure(err error) acquisition.Failure {
	var failure acquisition.Failure
	if errors.As(err, &failure) {
		return failure
	}
	return acquisition.Failure{Kind: acquisition.FailureSemantic, Summary: "acquisition failed", Cause: err}
}

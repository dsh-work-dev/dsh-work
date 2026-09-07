package acquisition

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type scriptedAdapter struct {
	steps      []scriptedAttempt
	stagePaths []string
}

type scriptedAttempt struct {
	result AttemptResult
	err    error
}

func (a *scriptedAdapter) Attempt(_ context.Context, request AttemptRequest) (AttemptResult, error) {
	a.stagePaths = append(a.stagePaths, request.StagingPath)
	if len(a.steps) == 0 {
		return AttemptResult{}, errors.New("unexpected acquisition attempt")
	}
	step := a.steps[0]
	a.steps = a.steps[1:]
	return step.result, step.err
}

func testRequest(t *testing.T) Request {
	t.Helper()
	return Request{
		OperationID: "install-node",
		Identity: ArtifactIdentity{
			Kind:         ArtifactNode,
			Name:         "node",
			Version:      "v26.0.0",
			Platform:     "windows",
			Architecture: "x64",
			Filename:     "node-v26.0.0-win-x64.zip",
		},
		Candidates: []SourceCandidate{
			{Route: RouteOfficial, Location: "https://nodejs.org/dist/"},
			{Route: RouteMirror, Location: "https://mirror.example/node/"},
		},
		StagingRoot: t.TempDir(),
		StagePrefix: "node-v26.0.0-",
	}
}

func TestAcquireReturnsOfficialSuccessThroughOneInterface(t *testing.T) {
	request := testRequest(t)
	adapter := &scriptedAdapter{steps: []scriptedAttempt{{
		result: AttemptResult{
			ResolvedIdentity: request.Identity,
			Integrity:        IntegrityEvidence{Algorithm: "sha256", Digest: "abc123"},
		},
	}}}

	result, err := Acquire(context.Background(), request, adapter, nil)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if result.Route != RouteOfficial || result.FallbackUsed {
		t.Fatalf("source result = route %q fallback %v", result.Route, result.FallbackUsed)
	}
	if !reflect.DeepEqual(result.Attempts, []Attempt{{Route: RouteOfficial, Succeeded: true}}) {
		t.Fatalf("attempts = %#v", result.Attempts)
	}
	if result.StagingPath == "" {
		t.Fatal("successful acquisition did not retain its staging path")
	}
}

func TestAcquireFallsBackOnlyAfterReachabilityFailureAndUsesFreshStaging(t *testing.T) {
	request := testRequest(t)
	adapter := &scriptedAdapter{steps: []scriptedAttempt{
		{err: Failure{Kind: FailureReachability, Summary: "official source is unreachable"}},
		{result: AttemptResult{ResolvedIdentity: request.Identity}},
	}}

	result, err := Acquire(context.Background(), request, adapter, nil)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if result.Route != RouteMirror || !result.FallbackUsed {
		t.Fatalf("source result = route %q fallback %v", result.Route, result.FallbackUsed)
	}
	wantAttempts := []Attempt{
		{Route: RouteOfficial, Failure: FailureReachability, Summary: "official source is unreachable"},
		{Route: RouteMirror, Succeeded: true},
	}
	if !reflect.DeepEqual(result.Attempts, wantAttempts) {
		t.Fatalf("attempts = %#v, want %#v", result.Attempts, wantAttempts)
	}
	if len(adapter.stagePaths) != 2 || adapter.stagePaths[0] == adapter.stagePaths[1] {
		t.Fatalf("staging paths = %#v, want one fresh path per source", adapter.stagePaths)
	}
	if _, statErr := os.Stat(adapter.stagePaths[0]); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed official staging path still exists: %v", statErr)
	}
	if info, statErr := os.Stat(adapter.stagePaths[1]); statErr != nil || !info.IsDir() {
		t.Fatalf("successful mirror staging path was not retained: %v", statErr)
	}
}

func TestAcquireTreatsNonReachabilityFailuresAsTerminal(t *testing.T) {
	for _, kind := range []FailureKind{
		FailureIntegrity,
		FailureAuthentication,
		FailureNotFound,
		FailureSemantic,
	} {
		t.Run(string(kind), func(t *testing.T) {
			request := testRequest(t)
			adapter := &scriptedAdapter{steps: []scriptedAttempt{
				{err: Failure{Kind: kind, Summary: "terminal failure"}},
				{result: AttemptResult{ResolvedIdentity: request.Identity}},
			}}

			result, err := Acquire(context.Background(), request, adapter, nil)
			if err == nil {
				t.Fatal("Acquire() error = nil")
			}
			var failure Failure
			if !errors.As(err, &failure) || failure.Kind != kind {
				t.Fatalf("Acquire() error = %#v, want %q", err, kind)
			}
			if len(adapter.stagePaths) != 1 || len(result.Attempts) != 1 {
				t.Fatalf("terminal failure attempted mirror: paths=%#v attempts=%#v", adapter.stagePaths, result.Attempts)
			}
		})
	}
}

func TestAcquireCancellationProducesOneTerminalAttemptAndCleansStaging(t *testing.T) {
	request := testRequest(t)
	adapter := &scriptedAdapter{steps: []scriptedAttempt{{err: context.Canceled}}}

	result, err := Acquire(context.Background(), request, adapter, nil)
	if err == nil {
		t.Fatal("Acquire() error = nil")
	}
	var failure Failure
	if !errors.As(err, &failure) || failure.Kind != FailureCanceled {
		t.Fatalf("Acquire() error = %#v, want cancellation", err)
	}
	if len(result.Attempts) != 1 || result.Attempts[0].Failure != FailureCanceled {
		t.Fatalf("attempts = %#v", result.Attempts)
	}
	if len(adapter.stagePaths) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(adapter.stagePaths))
	}
	if _, statErr := os.Stat(adapter.stagePaths[0]); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("cancelled staging path still exists: %v", statErr)
	}
	if filepath.Dir(adapter.stagePaths[0]) != request.StagingRoot {
		t.Fatalf("staging path %q escaped root %q", adapter.stagePaths[0], request.StagingRoot)
	}
}

# ADR-0002: Windows process-boundary library evaluation

- Status: Accepted for F3
- Date: 2026-09-01

## Context

The Windows adapter needs a Job Object so that a DSH Worker and its descendants
are owned by the Host and are cleaned up together. The launch contract also
requires a suspended process, assignment to the Job before resume, an explicit
allow-list of inherited handles, bounded output capture and retryable cleanup.

The implementation should use a mature community library wherever that library
provides the required safety and lifecycle guarantees. It must not introduce
fake production adapters for macOS or Linux while the Windows vertical slice is
being completed.

Candidates evaluated:

- [`golang.org/x/sys/windows`](https://pkg.go.dev/golang.org/x/sys/windows):
  maintained Go bindings for Windows system calls. It exposes the primitives
  needed for Job Objects, process creation, startup attribute lists, handles,
  pipes and wait operations, while leaving orchestration to the caller.
- [`go-winjob`](https://github.com/kolesnikovae/go-winjob): a focused Job Object
  wrapper with creation, assignment, limits and process notifications. It does
  not cover the complete launch contract: in particular, the adapter still
  needs process creation with `STARTUPINFOEX` and an explicit inherited-handle
  list, plus the Work-specific output and cleanup protocol.
- [`hcsshim/internal/jobobject`](https://github.com/microsoft/hcsshim/blob/main/internal/jobobject/jobobject.go):
  a useful Microsoft implementation, but it is an `internal` package coupled
  to the HCS shim and is not an importable general-purpose dependency for this
  application.

## Decision

Keep `golang.org/x/sys/windows` as the only Windows system boundary dependency
for F3. The adapter owns only the narrow Work-specific orchestration around
those bindings:

1. create a kill-on-close Job Object;
2. create the DSH process suspended with an explicit inherited-handle list;
3. assign the process to the Job before resuming it;
4. capture bounded, redacted output and report structured readiness;
5. make shutdown and cleanup retryable and verify that the process boundary is
   empty before reporting success.

This is not a replacement for a general Job Object library. It keeps the
security-critical launch and cleanup invariants in one adapter, where they can
be tested against the Supervisor contract, instead of combining a partial Job
wrapper with a second low-level process-creation implementation.

## Consequences

Positive:

- The dependency remains a mature, general-purpose system-call binding.
- Handle inheritance and assign-before-resume ordering are explicit and
  testable in the adapter.
- No unmaintained or partial library becomes a safety-critical process-boundary
  dependency.
- Shared Supervisor contracts remain platform-neutral; Windows details stay in
  `internal/platform/windows`.

Costs and risks:

- The adapter contains some Windows orchestration code.
- A future library may reduce that code if it can preserve every required
  invariant and provide compatible lifecycle/error semantics.

## Re-evaluation trigger

Before F4 process-hardening work or any expansion to Job limits, notifications
or diagnostics, re-evaluate available libraries. Adopt one only if its API,
maintenance, security posture and platform coverage satisfy the complete launch
and cleanup contract without weakening the explicit handle allow-list or
retryable cleanup guarantees.

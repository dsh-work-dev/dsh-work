# ADR-0008: Atomic Run-context switching and current-profile plugin scope

- Status: Accepted
- Date: 2026-09-03

## Decision

dsh-work treats the DSH runtime, DSH data-directory identity and profile reference
as one Run context. Changing any member while a Worker is running is an
immediate managed switch: dsh-work stops and verifies the old generation, starts
the complete candidate tuple, and commits it only after the candidate reaches
`Ready`. The previous `Ready` tuple remains the known-good rollback context;
failure automatically restores it. There is no pending next-launch context.

The DSH data directory scopes its profiles and their plugin associations. The
runtime is an immutable distribution and does not own profile plugin state.
Profile inspection may name any explicit `ProfileRef`, but plugin installation,
removal and other profile-composition mutations are accepted only when the
reference equals the profile in the current `Ready` Run context. Non-current
profiles are read-only until a successful context switch makes one current.

## Consequences

- Runtime, data-directory and profile changes have one visible, transactional
  lifecycle and cannot leave a failed candidate active.
- The manager must serialize switching and plugin mutations and must reject
  overlapping Worker generations.
- The desktop Host and standalone CLI share a kernel-owned lock in dsh-work
  application data. The CLI is an offline manager surface and rejects commands
  while the desktop process is running, so it cannot create a second
  configured/current view without a Host transaction.
- The Settings UI needs separate inspection and switch affordances for
  profiles, plus read-only presentation for non-current plugin state.
- A stopped dsh-work process has no current `Ready` profile, so profile mutations
  are unavailable until a Run context is ready.

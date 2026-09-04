# ADR-0007: Separate the DSH data directory from Workspace context

- Status: Accepted; supersedes the Workspace scope of ADR-0003
- Date: 2026-09-03

## Context

dsh-work originally treated runtime, DSH home, profile and Workspace as one
launch-selection tuple. That makes two different DSH concepts look like one
dsh-work setting: DSH's home is the data root for profiles and runtime state,
while a DSH Workspace is a persistent record around a working directory and
its sessions. The upstream DSH documentation defines these as separate
concepts and gives Workspace its own registry and directory identity.

The distinction matters for a desktop shell. One DSH data directory can serve
many Workspaces, and a user can switch Workspace without changing the runtime,
profile or data root. A process current directory is also an unsafe and
surprising source of user Workspace state, especially when dsh-work is launched
from a shortcut, installer or shell integration.

## Decision

1. The user-facing label is `DSH data directory`. `DSH home` and `DSH_HOME`
   remain DSH's external technical terms. dsh-work application data, installed
   runtime files and DSH data-directory contents remain distinct.
2. The persisted dsh-work Configured Run context contains only:

   ```text
   DSH runtime identity
   DSH data-directory identity
   profile name
   ```

   The `dshmanager` Module and its public Interface do not persist a Workspace
   path or Workspace identifier in that context.
3. Workspace selection and creation belong to DSH's Workspace surface or to an
   explicit launch/session action. The `workspacecontext` Module resolves the
   DSH-owned Workspace record and exposes a `Workspace context` for one active
   session or Worker generation. A DSH Adapter may carry that context in the
   per-generation `Launch context`, but it must not promote it into global
   settings.
4. Workspace resolution follows DSH's contract: directory identity is
   canonicalized and validated, and unregistering a Workspace does not delete
   or relocate its directory, files, sessions or logs. dsh-work never derives a
   Workspace from its process directory, install directory, operating-system
   home or DSH data directory.
5. `General` exposes the runtime, DSH data directory and profile Run context;
   it has no Workspace picker or editable Workspace path. `Overview` may show
   the current Workspace context read-only. The DSH Workspace surface remains
   the place where a user selects or creates a Workspace.
6. If a supported DSH Adapter needs a directory before its Workspace surface
   can open, its Implementation uses an explicit dsh-work bootstrap directory in
   dsh-work application data. That directory is not registered or presented as a
   user's Workspace and is never the install directory, operating-system home
   or DSH data directory.

This is a direct pre-release schema change. dsh-work reads and writes only the new
launch-target shape; no legacy field alias, compatibility reader or migration
path is required.

The same pre-release rule applies to the dsh-work manager state file: the current
transactional state schema is the only accepted schema. Older manager state is
rejected as invalid rather than migrated or read through compatibility aliases.
This is an intentional exception to the normal migration rule while the data
contract is not stable; it prevents old runtime, data-directory or profile
selection records from silently re-entering the new Run-context model.

This keeps the deep ownership and locality of each concept: the manager owns
the durable Configured Run context, DSH owns Workspace identity and session
semantics, and the Adapter seam carries only the context needed for one run.

The immediate switch, readiness commit and automatic rollback rules are
defined by [ADR-0008](0008-atomic-run-context-switching-and-current-profile-plugin-scope.md).

## Consequences

Positive:

- The Settings surface describes stable global choices instead of a stale
  directory guess.
- Workspace switching does not unexpectedly change runtime, profile or DSH
  data-directory selection.
- DSH remains the source of truth for Workspace registration and sessions.
- The launch Interface is smaller and easier to reason about and test.

Costs and risks:

- Startup needs an explicit no-Workspace state or a DSH-provided Workspace
  picker instead of relying on the current directory.
- The first Adapter contract must define how an explicit Workspace context is
  passed when the DSH version requires one at process start.

## Rejected alternatives

- **Keep Workspace in the global Configured Run context:** couples a session choice to
  runtime/profile settings and makes multiple Workspaces awkward to switch.
- **Use the process current directory as the default Workspace:** produces
  launch-location-dependent state and can accidentally expose an installer,
  repository or user home as a DSH Workspace.
- **Use DSH home as the dsh-work-facing label:** hides the purpose of the data
  root; dsh-work uses `DSH data directory` while the external DSH contract retains
  `DSH home` and `DSH_HOME`.

## Public references

- [DSH Workspace subsystem](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/subsystems/workspace.md)
- [DSH home paths](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/util/home-paths/README.md)
- [DSH architecture and profile layers](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/architecture.md)
- [DSH CLI launcher](https://github.com/deepseek-ai/deepseek-harness/blob/master/apps/cli/README.md)

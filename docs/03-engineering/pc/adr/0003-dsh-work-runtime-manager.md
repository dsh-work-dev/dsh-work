# ADR-0003: External DSH workspace and dsh-work runtime manager

- Status: Accepted direction; CLI contract to be implemented next
- Date: 2026-09-02

## Context

Work owns the desktop lifecycle, trusted recovery surface and native process
boundary. DSH owns the agent runtime, profile composition and DSH Web UI. The
embedded `frontend/dist` assets in Work can therefore be misunderstood as an
embedded DSH application even though they are only the Host shell shown before
readiness and after recovery.

The product also needs to keep more than one DSH version and profile available,
select one for a Work launch and manage DSH plugins. DSH already exposes public
seams for profile selection and profile plugin management:

- `dsh --profile <name>`
- `dsh plugin --profile <name> <pnpm arguments>`

Work must not duplicate those profile or plugin semantics.

## Decision

1. Work continues to embed only the trusted Host shell. It never embeds or
   copies the DSH Web UI into the Work binary.
2. DSH remains an out-of-process runtime. Its Web UI is served by the selected
   Worker and is navigated only after readiness and gateway validation.
3. Add a separate `dsh-work` CLI and a shared runtime-manager Module. The CLI
   owns explicit installation, discovery, selection and removal of DSH
   runtimes; the Module provides Work with a resolved launch selection.
4. A launch selection contains at least an exact DSH runtime version and a DSH
   profile name. The initial F3 version/profile remains the default compatibility
   fixture, not the long-term product limit.
5. Profile creation and plugin operations use DSH's supported profile/plugin
   seam. `dsh-work` may orchestrate those commands, but does not parse or
   reimplement DSH's patch-layer composition.
6. Normal Work GUI startup is read-only with respect to runtime management. It
   must never invoke npm, pnpm, npx or an implicit download. Installation,
   update, rollback and plugin changes are explicit CLI operations.
7. Runtime and profile data are owned by the manager and must be isolated from
   user-owned DSH data unless the user explicitly selects an existing DSH home.
   The first manager implementation will treat a profile as compatible only
   with the runtime selection it was created for; cross-version reuse requires
   an explicit compatibility decision and migration path.

## Consequences

Positive:

- The Work binary stays a Host, not a second DSH distribution.
- Work can switch DSH versions and profiles without changing lifecycle code.
- DSH remains the source of truth for profile and plugin composition.
- Package-manager side effects are explicit and kept out of GUI startup.
- Runtime resolution is testable independently of Wails and platform process
  supervision.

Costs and risks:

- The manager needs a versioned local catalog, installation integrity checks and
  rollback-safe updates.
- Each supported DSH version/profile combination needs its own adapter contract
  tests.
- The CLI and GUI need a stable, versioned selection/configuration format.

## Rejected alternatives

- **Embed DSH assets in Work:** duplicates ownership and prevents independent
  DSH upgrades.
- **Make Host edit DSH profile files directly:** duplicates DSH's composition
  rules and risks corrupting user-owned profile data.
- **Run npm/pnpm during GUI startup:** makes startup non-deterministic and can
  mutate or download dependencies without an explicit user action.

## Follow-up implementation seam

The next vertical slice should define a small `RuntimeManager` Interface with
resolution as its primary operation. CLI commands can sit on top of the same
Module, while the Host only consumes a resolved executable, DSH home and
profile-specific launch arguments. The exact command names and persisted
selection format must be kept in one CLI contract rather than spread through
the Host.

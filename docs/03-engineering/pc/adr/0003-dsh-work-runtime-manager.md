# ADR-0003: External DSH workspace and dsh-work runtime manager

- Status: Superseded by ADR-0007 and ADR-0008 for the launch, switch and plugin policy; retained as a historical record of the initial manager/runtime decision
- Date: 2026-09-02

> Historical note: the launch model in this ADR is replaced by ADR-0007, and
> its switch/plugin policy is replaced by ADR-0008. The runtime/profile
> ownership details remain useful background, but this ADR is not the current
> product contract.

## Context

dsh-work owns the desktop lifecycle, trusted recovery surface and native process
boundary. DSH owns the agent runtime, profile composition and DSH Web UI. The
embedded `frontend/dist` assets in dsh-work can therefore be misunderstood as an
embedded DSH application even though they are only the Host shell shown before
readiness and after recovery.

The product also needs to keep more than one DSH version and profile available,
select one for a dsh-work launch and manage DSH plugins. DSH already exposes public
seams for profile selection and profile plugin management:

- `dsh --profile <name>`
- `dsh plugin --profile <name> <pnpm arguments>`

dsh-work must not duplicate those profile or plugin semantics.

## Decision

1. dsh-work continues to embed only the trusted Host shell. It never embeds or
   copies the DSH Web UI into the dsh-work binary.
2. DSH remains an out-of-process runtime. Its Web UI is served by the selected
   Worker and is navigated only after readiness and gateway validation.
3. Add a separate `dsh-work` CLI and a shared runtime-manager Module. The CLI
   owns explicit installation, discovery, selection and removal of DSH
   runtimes. The Module resolves a launch selection containing an exact
   runtime, DSH home and profile; it does not become the source of truth for
   profile composition.
4. A DSH runtime and a DSH profile are separate concepts:

   ```text
   DSH runtime (immutable installed distribution)
     └── executable, launcher and built-in bundles

   DSH home (data root)
     └── profile: web / coding / ...
           ├── package.json and plugin dependency state
           ├── dsh.profile and ordered bundle references
           ├── profile patch layers
           └── profile data

   launch selection = runtime + DSH home + profile + dsh-work workspace
   ```

   A runtime loads a profile; it does not own that profile. The initial F3
   version/profile remains the default compatibility fixture, not the
   long-term product limit.
5. A DSH profile is the logical owner and enablement scope for its plugins.
   Installing the same plugin into two profiles creates two independent
   profile associations. A package manager may physically deduplicate package
   artifacts, but that implementation detail does not make a plugin global or
   runtime-owned. Profile creation and plugin operations use DSH's supported
   profile/plugin seam. `dsh-work` may orchestrate those commands, but does not
   parse or reimplement DSH's patch-layer composition.
6. Normal dsh-work GUI startup is read-only with respect to runtime management. It
   must never invoke npm, pnpm, npx or an implicit download. Installation,
   update, rollback and plugin changes are explicit CLI operations.
7. The manager's runtime installation store and dsh-work-managed DSH homes are
   distinct from user-owned DSH homes unless the user explicitly selects an
   existing home. A profile is not version-scoped by ownership. A profile may
   be paired with another runtime only after explicit runtime/profile
   compatibility validation; there is no implicit copy, migration or upgrade
   of profile data.
8. dsh-work's integration plugin, when needed, is attached to the selected profile
   for that Worker generation. Its generated overlay is dsh-work-owned and
   disposable, but it is not a global runtime plugin and must not alter other
   profiles.
9. Runtime, DSH home, profile and plugin management is presented in a flat,
   shallow rail inside a second trusted Settings window. `Overview` is first
   and read-only; it distinguishes the active session from the pending next
   launch. One top-level `General` page owns launch target and close policy;
   the `Profiles` resource page owns profiles and their child plugin actions,
   while separate pages own runtimes and homes. The DSH
   Workspace window only loads the external DSH Web UI through the Worker
   gateway. The two windows share the Host process and manager state, but never
   share a WebView document or inject Host management markup into DSH content.
10. The native application menu contains only `Settings` and `Help`; `Help`
    contains update-check and About commands. Workspace lifecycle actions stay
    in the system tray. These commands are handled by the Host and are not
    JavaScript inserted into the DSH page.
11. Every plugin management command requires an explicit `ProfileRef`
    consisting of a DSH home identity and profile name. The Profiles page
    selects that reference independently from General's launch form; the
    backend never infers a profile from a missing command field. A plugin is
    shown only within its selected profile detail, where its management action
    is available. Plugin commands are serialized with catalog persistence and
    report `restartRequired` when they target the active profile; the running
    Worker is not hot-reloaded and the user must explicitly restart it before
    the changed profile composition is used.
12. Custom profile names may be changed by renaming the profile directory,
    because DSH 0.1.2 has no public rename command and defines profile identity
    by `$DSH_HOME/profiles/<name>`. dsh-work never rewrites the profile manifest or
    patch layers, refuses built-in and active profiles, and updates the
   persisted Configured Run context when it references the renamed profile.
13. The Settings window is created lazily and reused as one application-level
    settings and DSH management surface. Closing it hides the window without
    stopping DSH; application quit still cancels management operations and
    cleans the DSH Worker before the Host exits.

## Consequences

Positive:

- The dsh-work binary stays a Host, not a second DSH distribution.
- dsh-work can switch DSH versions and profiles without changing lifecycle code.
- DSH remains the source of truth for profile and plugin composition.
- Plugin enablement and configuration stay isolated per profile while one
  package can be reused physically by a package manager.
- Management UI cannot contaminate the DSH Web UI, while Settings and Help
  remain discoverable from the native application menu and lifecycle actions
  remain available from the tray.
- Package-manager side effects are explicit and kept out of GUI startup.
- Runtime resolution is testable independently of Wails and platform process
  supervision.

Costs and risks:

- The manager needs a versioned local catalog, installation integrity checks and
  rollback-safe updates.
- Each supported runtime/profile compatibility pair needs adapter contract
  tests; profile ownership remains independent of the runtime catalog.
- The CLI and GUI need a stable, versioned selection/configuration format.
- The first slice's runtime/home `remove` operation unregisters catalog
  metadata only and retains files. Physical deletion is a separate future
  data-management operation with its own confirmation and recovery contract.

## Rejected alternatives

- **Embed DSH assets in dsh-work:** duplicates ownership and prevents independent
  DSH upgrades.
- **Make Host edit DSH profile files directly:** duplicates DSH's composition
  rules and risks corrupting user-owned profile data.
- **Run npm/pnpm during GUI startup:** makes startup non-deterministic and can
  mutate or download dependencies without an explicit user action.
- **Inject a dsh-work toolbar into the DSH DOM:** mixes ownership, depends on DSH
  page structure and risks exposing Host controls to untrusted content.

## Follow-up implementation seam

The implemented first slice defines the shared manager resolution contract and
the `dsh-work` commands for catalog inspection, explicit runtime registration/
installation, home registration, selection and profile-scoped plugin add/list/
remove. Runtime/home removal currently unregisters catalog entries while
retaining files. Runtime installation is native Windows work at present; other
platforms return an unavailable result until their native installer exists.
Additional DSH versions still require an adapter compatibility contract before
the Host will launch them.

The GUI implementation must expose the same Module through a thin Manager
window service and route Workspace-window native menu commands to that service.
The plugin command seam must carry `ProfileRef` end to end.

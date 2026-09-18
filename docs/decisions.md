# Accepted decisions

This register states the architectural decisions that govern the current
implementation and the accepted boundaries that remain in force.

## ADR-0001 — Go/Wails Host and out-of-process DSH Worker

Use Go and Wails 3 for the trusted desktop Host and run exact-version DSH as a
separate supervised process. Keep DSH, Wails and platform behavior behind
adapters. Embedded content receives no broad native bridge.

## ADR-0002 — Windows process boundary

Use `golang.org/x/sys/windows` for the Windows primitives needed to create the
Worker suspended, assign it to a kill-on-close Job Object before resume, limit
inherited handles and verify bounded cleanup. More focused libraries evaluated
for the original slice did not cover this complete launch contract.

## ADR-0003 — External runtime manager

dsh-work manages an external catalog of exact-version DSH runtimes and explicit
Node/data-directory/profile selections. Normal startup resolves local files.
Version recovery is a separate path that force-reinstalls the recorded DSH
version into its managed directory through npm or pnpm; it may download packages.
A matching version alone does not skip repair.

The catalog owns its managed installations. Removing a catalog runtime first
renames its directory aside inside the store, then deletes it, and updates
the catalog only after the rename succeeds; a locked file fails the removal
with the runtime intact, and an aside copy that could not be fully deleted is
retried on the next removal. Directories not in the catalog are not swept
automatically, because a lost or reset manager state would otherwise delete
every installed runtime.

## ADR-0004 — Global Settings and tray-aware lifecycle

Persist versioned dsh-work settings separately from DSH data. Closing a desktop
window hides it and retains its UI client/WebView; explicit background Quit owns
Worker cleanup.
Ignore legacy closeToTray values and omit that field on save. The daemon owns
the tray and preferences independently of the client, as specified in ADR-0017.
Windows persistence uses a native replace/write-through operation behind the
platform boundary.

## ADR-0005 — Small typed localization boundary

Persist one Host locale with `en`, `zh-CN` and `ja-JP`. Trusted WebViews and
native menu/tray chrome consume the same typed vocabulary. The DSH Workspace is
not translated or modified by dsh-work.

## ADR-0006 — Host-owned desktop notifications

dsh-work owns desktop-notification preferences, policy, deduplication and native
delivery. DSH retains ownership of contextual in-page notices. Native delivery
failure is diagnostic and does not change the source event outcome.

## ADR-0007 — Separate DSH data directory and Workspace context

The persisted Run context contains runtime, Node selection, DSH data-directory
and profile identity. DSH owns Workspace registration and session semantics; Workspace
context is resolved separately for a generation and is never inferred from the
Host process directory.

## ADR-0008 — Atomic Run-context switching

Changing runtime, DSH data directory or profile replaces the complete Run
context. The candidate becomes current only after readiness and authenticated channel checks;
failure follows the user's recovery preference. Automatic recovery uses a
verified version snapshot and is bounded to prevent retry loops. Normal plugin
mutation requires the current healthy profile; recovery reapplies recorded
inputs after the Worker has stopped. Manager operations are serialized across
desktop and CLI.

## ADR-0009 — Field-compatible manager state

The dsh-work manager persistence file has no schema-version marker. The manager
reads the known State fields, ignores unknown fields, and validates the required
runtime, data-directory, profile and Node identities before restoring a
configured Run context. It does not migrate historical field layouts. Atomic
replacement protects each write from being partially persisted.

This keeps startup focused on whether the selected configuration is usable
instead of whether a marker matches the running build. Required identity checks
still prevent an ambiguous or incomplete selection from reaching launch.

This decision applies to the manager State only. Global Host preferences remain
the separate versioned `internal/settings` contract. The optional
`versionRecovery` payload has its own schema version. Runtime, Node,
DSH-release and plugin records retain their business version fields.

## ADR-0010 — Host-owned Desktop Pet with data-only package adapters

The Desktop Pet is a separate Host-managed native overlay, outside the DSH
Workspace WebView. dsh-work owns the Pet catalog, package validation,
selection, visibility, persistence and native-window lifecycle. Supported Codex
entries, including the `avatars/` compatibility entry, and dsh-native packages
are adapted as data into one renderer contract;
pet packages do not execute code or receive Host capabilities. This preserves
the Host authority boundary while allowing asset formats to change at the
adapter edge.

The Codex `pets/<id>/pet.json` and `avatars/<id>/avatar.json` entries share the
same manifest, profile, geometry and track rules. The `avatars/` path is kept
for Codex compatibility and is not exposed as a separate source format.

## ADR-0011 — Direct Pet scale control and handle-only movement

Pet scaling is a direct trusted Settings slider from 50% to 300%, based on the
96x104 canonical sprite at 100%; window dimensions also reserve activity space.
Native border resize is disabled. The native
window receives pointer input so the drag affordance can appear on hover, but
only the explicit handle moves the window. Position is persisted as a
monitor-relative normalized anchor, and Host resize failure rolls back the
persisted dimensions.


## ADR-0012 — Verified version snapshots and package-manager recovery

Record exact DSH and installed plugin versions plus bounded dependency inputs,
using the existing atomic manager-state writer. Compare inputs captured before
startup with inputs after readiness before advancing success pointers. Keep a
healthy Worker available if snapshot persistence fails.

Recover through package-manager installation and verify both installed inputs
and Worker readiness. Preserve `node_modules` as a directory owned by the
package manager. With the supported pnpm hoisted workflow, force install alone
can skip same-version damaged files, so recovery first removes declared packages
through the package manager, reapplies the recorded inputs and forces a frozen
install. This reuses existing installation and atomic-persistence mechanisms.

Automatic success points are scoped by profile; switches retain the previously
successful environment as their recovery source. Safe mode keeps separate data
and does not replace normal success points. Failure policy, interruption limits
and supported source types are defined in [Version recovery](version-recovery.md).

## ADR-0013 — Persist native window dimensions in Host settings

Workspace and Settings independently persist normal logical
width/height and maximised state in the existing Host settings store. Wails
native events supply observations; writes are debounced and flushed on close
and shutdown. Preserve normal dimensions through minimisation and maximisation.
Restore valid dimensions at creation and use defaults when absent or invalid.

## ADR-0014 — Integrated authenticated desktop channel

Use a per-generation current-user OS pipe for Host/Worker traffic, with mutual
HMAC authentication and fixed trusted/Worker window roles. Wails byte streams
carry bounded Fetch/WebSocket bodies; asset delivery uses native handlers.
Generation ownership and backpressure are part of the contract. The Go Host
owns cookies and native authority. HTTP remains the upstream route protocol
inside the pipe, and its internal loopback authority is not a listening socket.

Reuse go-winio, coder/websocket and Go/Node HTTP implementations. Keep this in
the regular application lifecycle, including restart and safe mode. The daemon
is the background Host owner; the UI forwards Worker traffic through it as
specified in ADR-0017. Remote clients remain deferred. See [Architecture](architecture.md).

## ADR-0015 — Explicit version records and confined file access

Store exact DSH/plugin versions, original dependency specifiers/groups, bundle
order and lock/workspace inputs. Reconstruct dependency fields on recovery,
preserving current non-dependency settings. Keep the lock authoritative and
verify installed versions afterward. A version record is distinct from a profile
backup. Supported recovery limits are in [Version recovery](version-recovery.md).

Use google/safeopen for Windows version-file opening after the reproduced
redirected-AppData os.Root failure; retain rooted publication/removal. Let pnpm
reconcile its own acquisition staging metadata before package removal and
reinstallation. Neither workaround permits unchecked path access or manual
package-tree replacement.

## ADR-0016 — Selective information hierarchy in Settings

Reduce complexity through ordering, grouping and concise copy. Add a management
view only for dense details or accumulating history. Overview version history
and profile backups use focused dialogs; DSH and Node runtime controls stay
on one page. Place failures, diagnostics and next actions near their source.
Reuse the square monochrome system. [Settings and startup](settings.md) defines
the current page layout; [Interface standards](standards/interface.md) governs
future changes.

## ADR-0017 — Per-user daemon and independent desktop client

Run the Host/manager, Worker supervisor, tray, Pet and notification adapters in
a resident per-user daemon. Invoke the same executable separately for the
workbench and Settings UI. Reuse Wails for native surfaces, go-winio for
current-user local IPC, and Go HTTP for control and Worker forwarding. The online
CLI uses the same serialized manager authority.

This boundary preserves tasks through a whole UI-process crash as well as normal
window hiding. It adds a local transport hop and requires typed state/event
projection. Portless communication alone does not require separate processes;
the process split serves the UI-crash isolation contract. It does not implement
remote access or a system-wide Windows service.

Only explicit background shutdown stops the Worker and its children, releases
locks and exits native background surfaces. A desktop window close never
substitutes for that operation. See [Architecture](architecture.md) for the
transport and lifecycle contracts.

## ADR-0018 — Plugin disable and startup-failure plugin actions

An installed third-party plugin can be disabled without uninstalling it. A
disable removes the package from the profile manifest's `dsh.profile.bundles`,
so DSH loads neither the plugin's code nor its patches, and the package stays in
`node_modules`. dsh-work records the disable in its manager state with the
package's layer position. Enabling puts the package back at that position.
Every `dsh plugin` command rebuilds the bundle list from the installed packages
and would re-add the plugin, so the Host re-applies recorded disables before
each launch. The manifest is edited through JSON Patch with the existing
`tailscale/hujson` dependency, so other content is preserved byte for byte.
`@deepseek-ai/*` distribution packages are never disabled.

When a start fails, the DSH adapter reads the captured output for the packages
it names. The Host keeps only third-party packages installed in the failed
profile and offers to disable or remove each one from the startup window. If DSH
rejected its own stored session data, no plugin is offered, because the plugin
that reported the error did not cause it. These actions change only the failed
profile, and only while no Worker is running and no Run context is current.
Normal plugin changes on a running profile keep the stopped-Worker transaction
from ADR-0008.

This decision supersedes the earlier rule that dsh-work never edits DSH
manifests. The one manifest field dsh-work writes is the bundle list.

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
renames its directory aside inside the store, then deletes it. The catalog is
updated once the rename succeeds, and a locked file that blocks the rename
fails the removal with the runtime intact.

Each runtime owns independent files. pnpm installs use
`package-import-method=clone-or-copy` instead of hard-linking from the user's
store, because Windows refuses to delete any link of a native module that a
running Worker has mapped, so a shared file would pin every other runtime.
Copy-on-write clones are used where the volume supports them; otherwise each
runtime costs a full copy on disk. The store location is left to the user's
pnpm configuration. After the rename, deletion is best effort: a file still
held open, for example by a runtime installed before this rule, stays aside and
is retried on later removals without blocking them. Directories not in the catalog are not swept
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
inputs after the Worker has stopped. The startup-failure plugin actions in
ADR-0018 are the one exception. Manager operations are serialized across
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
carry bounded streaming Fetch/WebSocket bodies; finite-request HTTP routing is
the route-selected extension in ADR-0020. Asset delivery uses native handlers.
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

*Superseded by ADR-0021, apart from the startup-failure attribution rules,
which ADR-0021 keeps.*

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

## ADR-0019 — Disabling official loader entries

*Superseded by ADR-0021. dsh-work still reads the layer patches to list
loader entries, but no longer writes the profile patch layer.*

An official loader entry can be turned off without removing the layer that
inserts it. dsh-work writes an id-targeted `disabled: true` row into the
profile's own patch layer, `profiles/<name>/cordis.patch.yml`, which DSH applies
after every layer; enabling removes that key, and the row too when nothing else
is left in it. The file is edited as a YAML node tree with the existing
`gopkg.in/yaml.v3` dependency, so the user's other rows, keys and comments stay.
DSH's plugin commands rebuild only the bundle list, so no dsh-work record is
needed to keep the row.

dsh-work lists the entries by replaying the layer patches in `bundles` order
from the profile's and the runtime's packages. Each entry sits under the
official layer whose `insert` adds it; any later layer's row decides its default
state. An entry the layers turn off outright cannot be disabled again; one with
a `!!js` condition can. Changes use the stopped-Worker transaction from
ADR-0008. `@deepseek-ai/*` layers themselves still cannot be disabled
(ADR-0018).

This decision extends ADR-0018: the profile's patch layer is the second file
dsh-work writes in a DSH profile, after the bundle list.

## ADR-0020 — Route-selected WebView transport over one authenticated pipe

Keep the authenticated per-generation named pipe as the only transport between
the Host, daemon and DSH Worker. Select the WebView carrier by request needs:
ordinary finite HTTP may use Wails' internal HTTP handler, while streaming,
cancellation-sensitive requests and WebSocket upgrades use the bounded Wails
byte streams. Both paths forward to the same daemon and Worker pipe; the choice
does not introduce a TCP listener or a second DSH protocol.

The standard HTTP path remains opt-in through `DSH_WORK_STANDARD_HTTP=1` while
the platform response semantics are limited. On Windows, Wails' AssetServer
buffers the response until the handler completes and does not implement
`http.Flusher`, so it cannot preserve early chunks or cancellation before
end-of-body and cannot carry WebSocket upgrades. The stream path is therefore
the default and the required path for long-running DSH output. Revisit the
default only when the Wails HTTP handler provides equivalent streaming and
upgrade behavior.

The HTTP proxy accepts only relative Worker paths, rejects CONNECT and TRACE,
strips hop-by-hop, cookie and proxy headers, sets the internal Origin and keeps
generation validation at the WebView boundary. DSH continues to expose its
normal Node HTTP routes; only the carrier beneath those routes is the named
pipe. `http://127.0.0.1:1` remains an internal origin and is never bound as a
listening socket. The account sign-in callback in ADR-0022 is the only
loopback listener. It is a browser entry point, not a Worker transport.

## ADR-0021 — DSH PluginManager owns plugin activation

When a profile's Worker is Ready, enabling or disabling a plugin bundle or an
official loader entry calls DSH's own PluginManager Remote methods,
`setBundleEnabled` and `setPluginEnabled`. The Host sends them over the current
Ready Worker's authenticated session. It checks that the session generation
matches the current Host generation and that the target is the running profile.
DSH saves and applies the change, including dependency retention and runtime
unload. The switches appear only when PluginManager reports that an item can
be toggled. Non-running profiles and a Worker that is not Ready show no
switches. dsh-work keeps no disable ledger of its own and does not reapply
disables before launch. Install, upgrade and uninstall still use DSH's plugin
commands through the manager.

A failed start has no Worker to call. In that case the startup window can still
disable a third-party bundle that the failure output names. It does this with
the switch lock held, no Run context current, and the package confirmed as an
installed, non-`@deepseek-ai` bundle of the failed profile. It removes the
package from the profile manifest's `dsh.profile.bundles`, keeps the installed
files and loader patches, and then starts again. This is the same persisted
choice as `setBundleEnabled(name, false)`. Re-enabling happens in normal plugin
management once DSH is running. Attribution rules from ADR-0018 still apply:
only third-party packages installed in the failed profile are offered, and
failures caused by DSH session data offer none.

Using DSH's API keeps dsh-work in step with DSH's deselection, dependency and
unload semantics as they change. The cost is that activation changes need a
running Worker, except for the startup-failure path.

## ADR-0022 — Desktop account sign-in

DSH's account UI appears only when the renderer is marked as a desktop shell,
and a desktop shell must open the browser and receive the OAuth return.
dsh-work provides both around DSH's official account API:

- The Worker bridge defines `dshDesktop` so DSH mounts its account UI.
- dsh-work's per-launch client plugin follows `account/watch` and opens an
  attempt's `authorizeUrl` in the system browser once, when the attempt
  reaches `waiting-browser`. The URL is not available when `startSignIn`
  returns, so the watch stream is the only reliable trigger. DSH's desktop
  shells use the same trigger.
- The daemon owns a `127.0.0.1` callback listener with an ephemeral port and
  substitutes its origin as `callbackOrigin` in `account/startSignIn`. DSH
  accepts only loopback HTTP callback origins, and the browser cannot reach the
  named pipe. The listener serves only the callback and a waiting page, checks
  `Host` and a loopback peer, and relays the callback to the current Worker
  generation. Its result page returns to the app through `dsh-work://open`.
- The installer registers `dsh://` and a dsh-work-specific `dsh-work://`,
  overwriting an existing `dsh://` handler and restoring it on uninstall.

The listener is an exception to "no TCP listener". It accepts no Worker
traffic, lives with the daemon so an open authorization survives a UI restart,
and is limited to requests from the local machine. Revisit it if DSH offers a
callback that does not need a loopback origin.

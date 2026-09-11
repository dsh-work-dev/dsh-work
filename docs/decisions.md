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

## ADR-0004 — Global Settings and tray-aware lifecycle

Persist versioned dsh-work settings separately from DSH data. The close policy
decides whether the last-window close hides to tray or begins full Quit; explicit
Quit remains the operation that owns Worker cleanup. Windows persistence uses a
native replace/write-through operation behind the platform boundary.

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
context. The candidate becomes current only after readiness and gateway checks;
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
192x208 canonical window at 100%. Native border resize is disabled. The native
window receives pointer input so the drag affordance can appear on hover, but
only the explicit handle moves the window. Position is persisted as a
monitor-relative normalized anchor, and Host resize failure rolls back the
persisted dimensions.


## ADR-0012 — Verified version snapshots and package-manager recovery

Accepted 2026-09-11 after implementation and Windows recovery verification.

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

Accepted 2026-09-11. Workspace and Settings independently persist normal logical
width/height and maximised state in the existing Host settings store. Wails
native events supply observations; writes are debounced and flushed on close
and shutdown. Preserve normal dimensions through minimisation and maximisation.
Restore valid dimensions at creation and use defaults when absent or invalid.

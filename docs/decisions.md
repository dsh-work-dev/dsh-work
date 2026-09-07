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

dsh-work manages an external catalog of immutable, exact-version DSH runtimes
and explicit DSH data-directory/profile selections. GUI startup resolves local
files and does not invoke npm, pnpm, npx or an implicit download.

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

The persisted Run context contains runtime, DSH data-directory and profile
identity only. DSH owns Workspace registration and session semantics; Workspace
context is resolved separately for a generation and is never inferred from the
Host process directory.

## ADR-0008 — Atomic Run-context switching

Changing runtime, DSH data directory or profile replaces the complete Run
context. The candidate becomes current only after readiness and gateway checks;
failure restores known-good. Profile mutation is allowed only for the current
healthy profile, and manager operations are serialized across desktop and CLI.

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
the separate versioned `internal/settings` contract, and runtime, Node,
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

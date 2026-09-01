# ADR-0001: Go and Wails 3 desktop Host with an out-of-process DSH Worker

- Status: Accepted for initial implementation
- Date: 2026-08-31

## Context

Work needs Windows, macOS and Linux desktop shells, system-tray integration, a responsive trusted recovery surface and strong ownership of a separate DSH process tree. DSH is a Node.js-based, plugin-composed runtime with a Web UI and public profile／patch seams. Its agent behaviour should remain outside the desktop Host.

Wails 3 currently provides a Go application model and native desktop WebView integration, but is still in beta. DSH is in developer preview and warns that compatibility-breaking changes may occur. Both dependencies therefore need exact version pinning and adapter boundaries.

Official status references:

- [Wails v3 status](https://v3.wails.io/status/)
- [Wails releases](https://github.com/wailsapp/wails/releases)
- [DeepSeek Harness repository](https://github.com/deepseek-ai/deepseek-harness)

## Decision

Use:

- Go for the trusted desktop Host;
- Wails 3 for native window, WebView and desktop integration;
- an out-of-process DSH Worker supervised by Go;
- a Work-owned DSH plugin injected through a supported profile／patch seam;
- private authenticated local IPC for Host capability requests;
- a TypeScript trusted UI, with the UI framework selected separately;
- separate Windows, macOS and Linux process-ownership adapters behind one Supervisor contract.

Pin an exact Wails beta during development. A dependency upgrade is a deliberate change with desktop smoke tests, not an automatic floating update.

## Consequences

Positive:

- Host recovery UI can survive Worker failure.
- Go can own platform process handles, signals and guardian protocols directly.
- DSH remains replaceable and independently upgradeable.
- Web content does not need unrestricted native bindings.
- Platform-specific code is contained.

Costs and risks:

- Two runtimes must be packaged, observed and versioned.
- Wails beta upgrades may require adaptation before its stable release.
- Native WebView and packaging behaviour need a platform test matrix.
- macOS and Linux require a guardian helper because process groups do not provide Windows-style kill-on-close semantics.
- Host–plugin IPC and process supervision add explicit protocol work.

## Rejected alternatives

- **Rewrite DSH behaviour in Go:** creates a second agent and plugin implementation and breaks the ownership boundary.
- **Run DSH inside the Host process:** removes crash isolation and ties incompatible runtimes together.
- **Expose broad native bindings to embedded content:** grants excessive authority to an untrusted boundary.
- **Open the DSH URL only in the system browser:** cannot provide the intended lifecycle, recovery and trusted approval experience.

## Review triggers

Revisit this decision if Wails cannot pass the required Windows, macOS and Linux lifecycle／WebView tests, if DSH removes the supported profile／patch seam, or if the Host cannot distribute a compatible runtime within the project's release constraints.

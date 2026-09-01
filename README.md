# Work

Work is a local-first desktop host for DeepSeek Harness (DSH). It owns the desktop lifecycle around DSH—startup, shutdown, status, permissions, recovery, and controlled browser automation—while DSH continues to own agent and plugin behaviour.

The repository is in the PC foundation phase. The shared contracts target Windows, macOS and Linux; native process supervision is delivered Windows-first behind that seam.

## Project principles

- Keep the desktop host small and focused.
- Preserve a clear boundary between the host and DSH.
- Bind local services to loopback by default.
- Ask before privileged or destructive actions.
- Fail into a recoverable state with actionable diagnostics.
- Keep user data local unless the user explicitly chooses otherwise.

## Documentation

Start with the [documentation index](docs/README.md), then read the [PC scope](docs/01-product/pc/scope.md) and [architecture](docs/03-engineering/pc/architecture.md).

## Foundation status

The F0–F3 Work PC foundation is implemented for the primary Windows target:

- Go 1.25.14, Wails CLI v3.0.0-beta.16, Node 24.20.0 and npm 11.19.0 are locked in [`toolchain.lock.json`](toolchain.lock.json).
- The shared lifecycle, DSH and process-supervisor contracts compile on Windows, macOS and Linux. Native process supervision is intentionally implemented only by the Windows adapter at this stage.
- Windows starts the pinned DSH Web profile on an explicit loopback port, validates readiness, hands the authenticated workspace to the Wails WebView, and verifies bounded cleanup through a Job Object.

## Local checks

Install the pinned local DSH runtime once:

```powershell
wails3 task setup:dsh
```

Run the non-networked checks and Wails build:

```powershell
wails3 task verify
wails3 build
```

Run the real Windows tracer bullet after DSH is installed:

```powershell
wails3 task test:windows-real-dsh
```

The real DSH path uses `tools/dsh/run-dsh.cmd` and a Work-owned DSH home. It does not invoke npm or npx during application startup. Later browser automation, permissions and deep UI milestones remain out of scope for this foundation.

# PC foundation toolchain baseline

This is the F0 baseline for the first Work desktop slice. Exact values are
duplicated in [`toolchain.lock.json`](../../toolchain.lock.json) so setup and
verification scripts can consume one machine-readable manifest.

## Locked tools

| Tool | Version | Role |
|---|---:|---|
| Go | `1.25.14` | Host and shared Modules |
| Wails CLI | `v3.0.0-beta.16` | Desktop composition, bindings and build tasks |
| Wails Go module | `v3.0.0-beta.16` | Native WebView Host |
| Node.js | `24.20.0` | Frontend tooling and local DSH launcher |
| npm | `11.19.0` | Frontend dependency installation |
| DSH | `@deepseek-ai/dsh@0.1.2-alpha.3` | Pinned out-of-process Web Worker |

The current development target is Windows 10/11 `amd64`. macOS and Linux are
validation targets for shared Go contracts and frontend checks in CI. Their
native Supervisor Adapters are planned work; this repository does not claim a
successful macOS or Linux process implementation yet. The DSH entry below is
the exact F3 compatibility fixture. The initial `dsh-work` runtime manager now
owns a versioned catalog and explicit selection; additional DSH versions remain
launchable only after their adapter contract is registered and verified.

## DSH runtime setup

The repository declares the initial DSH compatibility fixture in
`tools/dsh/package.json`. Install it explicitly during development:

```text
npm install --prefix tools/dsh
```

This creates a local, ignored `tools/dsh/node_modules` tree. Work startup never
runs npm, pnpm, npx or another package runner. Until the runtime-manager slice
lands, the Host accepts `WORK_DSH_EXECUTABLE` as an explicit override and
otherwise checks the local locked install at `tools/dsh/node_modules/.bin/dsh`
(with the Windows `.cmd` launcher selected by the Windows Adapter).

`DSH_HOME` is set to a Work-owned application-data directory for each launch.
The Host does not edit a user-owned DSH home during this foundation slice.
On Windows the locked local install is invoked through the checked-in
`tools/dsh/run-dsh.cmd` launcher, which calls the installed DSH entry module
directly and avoids package-manager shim behavior at runtime.

The Settings window's DSH manager and `dsh-work` CLI share the same catalog. Plugin changes
are delegated to `dsh plugin --profile <name> ...` with an explicit DSH home
identity; Work does not configure or relocate npm/pnpm stores.

## Verification inventory

Run these commands from the repository root:

```text
task verify:toolchain
task format:check
task docs:check
task test
task frontend:test
task build
```

`task setup:dsh` is the explicit dependency-install step. It is intentionally
not part of `task build`, `task run` or ordinary startup.

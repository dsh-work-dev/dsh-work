# Contributing to dsh-work

## Toolchain

Use the repository-pinned toolchain from `toolchain.lock.json`:

- Go 1.25.14;
- Wails CLI and module v3.0.0-beta.16;
- Node.js 24 or newer;
- npm 11.19.0.

Use the machine's resolved Go, Node, npm and Wails environment. Do not relocate
module caches, package-manager stores or tool caches to work around sandbox or
permission failures.

Install the locked frontend dependencies with:

```powershell
wails3 task frontend:install
```

The real DSH smoke test also needs the pinned local fixture. Installing it is an
explicit development action:

```powershell
wails3 task setup:dsh
```

Normal application startup never runs this setup task or downloads a runtime.

## Development workflow

Run the Wails development loop:

```powershell
wails3 task dev
```

Run the complete local verification suite before review:

```powershell
wails3 task verify
```

Useful narrower checks are:

```powershell
wails3 task format:check
wails3 task test
wails3 task frontend:test
wails3 task frontend:typecheck
wails3 task frontend:build
wails3 task docs:check
```

On Windows, verify the real DSH tracer bullet after installing the fixture:

```powershell
wails3 task test:windows-real-dsh
```

Exercise the integrated native desktop channel, streams and lifecycle with:

```powershell
wails3 task test:desktop
```

Build and run the desktop application with:

```powershell
wails3 task build
wails3 task run
```

The manager CLI is built by `wails3 task build:dsh-work`. It connects to the
running per-user daemon for online operations. When no daemon is reachable,
manager commands use offline access under the existing manager lock; they do
not bypass a lock held by another process. `status` and `restart` require a
running daemon. `stop --wait` is idempotent when no daemon is reachable: it
waits for any UI endpoint and manager lock to disappear, then reports stopped.
The CLI does not start the daemon automatically.

```powershell
bin\dsh-work-cli.exe status
bin\dsh-work-cli.exe runtime list
bin\dsh-work-cli.exe profile list
bin\dsh-work-cli.exe use --runtime dsh-0.1.5-rc.2 --data-directory dsh-work --profile web
bin\dsh-work-cli.exe plugin list --data-directory dsh-work --profile web
```

## Engineering rules

- Build one end-to-end slice at a time.
- Keep operating-system and DSH details behind narrow adapters.
- Prefer mature maintained dependencies for general-purpose functionality.
  Custom infrastructure needs a recorded boundary or safety reason.
- Keep policy and lifecycle state in domain modules; Wails, DSH and operating
  system primitives stay at the edges.
- Return typed results and stable error codes. Do not parse presentation text as
  a control protocol.
- Give every process, goroutine, timer, subscription and buffer one owner and a
  bounded lifetime.
- Propagate cancellation and verify cleanup on every terminal path.
- Preserve user-owned DSH data by default. Safely replace persisted dsh-work
  state; the manager State follows the field-compatible contract in ADR-0009,
  while global Host preferences remain versioned.
- Never expose arbitrary native methods to WebView content or log credentials,
  cookies, sensitive form values or full page content.
- Put platform differences inside platform adapters and report unsupported
  production behavior honestly.

Interface changes must cover loading, ready, busy, disabled, cancellation and
failure states as applicable. Use semantic tokens, visible keyboard focus,
accessible names and concise user-facing copy. Detailed product UI rules live
in [interface standards](docs/standards/interface.md).

## RED knowledge workflow

`red.toml` defines the repository's Research, Evolve and Document locations:

- decision-blocking unknowns and evidence belong in `.research/`;
- clear but unaccepted or incomplete changes belong in `.evolve/`;
- accepted project facts and rules belong in the Document paths.

When an Evolve item is accepted, update the affected Document sources. Run RED
after changing its configuration or artifacts:

```powershell
red check --json
```

## Public documentation and pull requests

Repository documentation is public. Do not add private research, competitive
analysis, unpublished targets, customer-identifying information, credentials,
tokens or raw diagnostic bundles.

Before review, confirm that:

- observable behavior and accepted documentation agree;
- boundary or dependency decisions are reflected in `docs/decisions.md`;
- cancellation, failure and cleanup paths are tested;
- persisted or wire-format changes document their compatibility contract;
  versioned contracts carry a marker, while explicitly field-compatible
  unversioned contracts are recorded in the decisions register;
- logs, diagnostics and fixtures contain no sensitive data;
- the affected platform and interface states have appropriate evidence.

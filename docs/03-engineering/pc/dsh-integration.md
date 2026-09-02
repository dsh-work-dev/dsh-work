# DSH integration contract

## Supported seam

Work treats DSH as a versioned external runtime. The adapter uses public DSH launcher, profile and patch behaviour and does not modify DSH source code. DSH is currently a fast-moving developer-preview dependency, so Work pins and tests an explicit supported version range.

The `dsh-work` runtime manager may keep multiple installed DSH runtimes. A
runtime is an immutable installed distribution; a profile is named data under
a DSH home and owns its own bundle, plugin and patch composition. One
immutable launch selection pairs an exact runtime with a DSH home and profile
for a Worker generation. The Host consumes that resolved tuple; it does not
install packages or edit profile composition during GUI startup. The initial
F3 exact version remains the baseline fixture while each additional supported
runtime/profile compatibility pair earns its own adapter contract tests.

Official references:

- [DeepSeek Harness repository](https://github.com/deepseek-ai/deepseek-harness)
- [CLI launcher documentation](https://github.com/deepseek-ai/deepseek-harness/blob/master/apps/cli/README.md)
- [Architecture and profile layers](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/architecture.md)

## Adapter interface

The DSH adapter owns:

- version discovery and compatibility decisions;
- command and environment construction;
- Work profile／patch generation;
- readiness-event parsing and health probing;
- graceful-shutdown request when supported;
- classification of exit and protocol failures.

The runtime manager owns runtime installation, local catalog state, launch
selection and explicit profile/plugin management commands. DSH remains the
source of truth for profile composition. Plugin operations delegate to DSH's
supported `dsh plugin --profile` seam; they are not reimplemented by the Host
or DSH Adapter.

No other package builds a DSH command or parses DSH log text.

## Launch preparation

Inputs to one launch are immutable:

```text
generation_id
executable and resolved version
workspace directory
Work-owned DSH home or explicitly selected user home
profile and patch paths
loopback port candidate
private IPC endpoint and one-launch credential
diagnostic verbosity
safe_mode flag
```

The normal implementation launches the Web profile without opening the system browser, passes an explicit loopback port, and injects the Work tool plugin through a generated overlay. Exact arguments live in the version-specific adapter and are covered by command-construction tests.

Work must not invoke a package runner that performs an implicit network download during ordinary startup.

## Readiness contract

Process creation is not readiness. A Worker becomes ready only when all checks pass:

1. the child remains alive;
2. adapter receives a recognised URL announcement or equivalent structured signal;
3. scheme, host and port match the expected loopback policy;
4. an active probe returns an expected application response;
5. the private Work plugin bridge completes its version handshake.

If the runtime does not offer a sufficiently stable announcement, the adapter may probe only the port allocated for that generation. It must not scan unrelated local ports.

## Tool bridge

The Work DSH plugin registers narrowly scoped Tools, a complete `tools/pre-execute` classifier for those Tools, and communication with the Host over a private platform transport. The classifier deterministically returns `allow`, `ask` or `deny`; an unclaimed Work Tool is an integration error and fails closed. `ask` reuses DSH `ctx.approval` and only `allowed-once` reaches the Tool body.

The Host creates the private endpoint before starting the Worker. The handshake authenticates the managed Worker generation, not every plugin executing inside the Worker; all payloads remain untrusted and pass Host hard-policy validation.

Handshake fields:

```text
protocol_version
worker_generation_id
one-launch credential proof
plugin version
supported request types
```

Every request includes a unique request ID, correlation ID, operation kind, canonical payload and cancellation identifier. Duplicate request IDs return the original terminal result or a protocol error; they never execute twice.

Browser-operation requests additionally carry the immutable DSH `callId`, task ID and browser connection／tab generations. The Host does not ask the user again; it correlates DSH audit with its execution event and denies any operation that violates a non-interactive hard rule.

## Worker web access

The DSH endpoint stays on loopback behind a Host-controlled access policy. The adapter contract tests upstream HTTP and WebSocket behaviour, including Origin handling and state-changing requests. If the pinned DSH version does not provide sufficient browser-origin protection, Work exposes the embedded view through a per-generation gateway that authenticates the trusted application session and proxies only required routes.

The gateway is not a general reverse proxy. It rejects unknown upstream targets, unsafe methods outside the required surface, untrusted WebSocket upgrades and requests after generation shutdown.

The Workspace window is the only Work WebView that navigates to the gateway
URL. The Manager window never receives a DSH URL and remains a trusted Host
surface. A native Work menu may be attached to the Workspace window, but its
handlers execute in the Host and do not modify the DSH document.

## Profile ownership

The ownership and relationship model is:

```text
DSH runtime (immutable distribution)
  └── executable, launcher and built-in bundles

DSH home (data root)
  └── DSH profile
        ├── plugin dependency declarations and installed profile state
        ├── ordered bundle references (`dsh.profile`)
        ├── profile patch layers (`cordis.patch.yml`)
        └── profile data

Worker generation = selected runtime + selected DSH home/profile
```

- The profile is the logical owner and enablement scope of its plugins. The
  same plugin in another profile is a separate association.
- Plugin management always carries a `ProfileRef` (DSH home identity plus
  profile name); an active profile is only a default UI context, never an
  implicit backend target.
- A package manager may deduplicate physical package artifacts. Work must not
  configure or relocate that store, and physical deduplication does not make a
  plugin global or runtime-owned.
- Work-owned overlays live in Work application data, are attached to the
  selected profile for one Worker generation and may be recreated from their
  schema.
- User-owned DSH data is never edited in place without an explicit migration.
- A user-selected existing DSH home is mounted through an adapter and backed up before a migration.
- Generated files contain a schema version and generation marker.
- Safe mode uses a separate generated overlay; it does not delete the normal overlay or user profile.

The runtime manager must validate runtime/profile compatibility before launch.
A profile is not automatically copied, migrated or made version-scoped when a
different runtime is selected.

## Compatibility policy

The adapter classifies a runtime as `supported`, `unsupported-old`, `unsupported-new` or `unknown`.

- `supported`: exact contract suite passes for the range.
- `unsupported-old`: refuse normal launch and provide remediation.
- `unsupported-new`: refuse by default because silent incompatibility is unsafe.
- `unknown`: allow only an explicit diagnostic attempt that cannot mutate user-owned data.

Each supported DSH upgrade requires adapter contract tests for command grammar, readiness, plugin injection, permission result handling and shutdown.

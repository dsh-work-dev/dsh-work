# Storage and security architecture

## Storage ownership

Work stores Host-owned data under the operating system's per-user application-data location. DSH Workspaces and DSH data directories remain separate from Work application data and from each other.

| Data | Owner | Persistence | Protection |
|---|---|---|---|
| Host configuration | Work | persistent, versioned | user-only filesystem access |
| Capability grants | Work | persistent only when allowed | user-only access; no high-risk permanent grants |
| Audit events | Work | bounded local retention | redacted before write |
| Diagnostic logs | Work | bounded rotating files | redacted before write |
| Runtime packages | Work or user | versioned cache | integrity checked before execution |
| Generated DSH overlay | Work | reproducible | schema and generation marker |
| DSH data directory, profiles and plugin associations | DSH / user | governed by DSH | data-directory scope is preserved; never silently rewritten by Work |
| DSH Workspace registry and session records | DSH / user | governed by DSH | registration changes do not delete the Workspace directory or files |
| Browser connection metadata | Work | until disconnected | browser／extension identity only; no cookies or passwords |
| Tab assignment | Work | task lifetime | IDs, origin and generation; no unrelated-tab inventory |
| Browser profile and login state | user／browser | browser-owned | never copied into Work storage |
| Credentials | user | when required | operating-system protected credential store |

## Configuration rules

- Persisted documents include a schema version.
- Migration writes a new file, validates it, then atomically replaces the prior version.
- A recoverable backup is kept until the new version starts successfully.
- Unknown fields are preserved only when the schema explicitly supports forward compatibility.
- Configuration paths are canonicalised and checked before file access.
- The persisted Configured Run context contains only DSH runtime,
  DSH data-directory and profile identity; the data directory scopes the
  profile and its plugin associations. A Workspace context is resolved
  separately per session or Worker generation.
- A context-switch candidate is not current and is not committed to the
  Configured Run context until its Worker reaches `Ready`. Work retains the
  last known-good Run context as the automatic rollback source; a failed
  candidate must not replace it.
- Plugin mutations are authorised only for the profile in the current `Ready`
  Run context. Non-current profile/plugin data may be read for inspection but
  must be rejected for mutation at the manager boundary.
- Work does not change global environment variables or package-manager configuration.

## Local endpoints

- DSH Web UI binds to loopback.
- Embedded navigation is restricted to a Host-controlled, per-generation application session; direct Worker access must satisfy the same origin and CSRF contract.
- Host–plugin IPC uses an endpoint restricted to the current user plus a one-launch handshake credential.
- The user's default profile is never launched with legacy remote-debugging command-line switches. Personal-browser auto-connect is enabled and approved through the browser's own control surface; extension mode uses the registered native host.
- All endpoint identifiers and credentials rotate on Worker or browser generation change.

## Threat summary

| Threat | Boundary | Main controls |
|---|---|---|
| Malicious page requests broader access | Browser → Host | typed operations, capability policy, approval, revalidation |
| Compromised plugin calls Host directly | Worker → Host | private authenticated IPC, narrow protocol, deny unknown operations |
| Embedded navigation loads hostile content | WebView → Host | trusted-origin allowlist, external-browser handoff |
| Hostile website probes a local Worker port | browser origin → Worker | verified upstream origin／CSRF controls or Host gateway, per-generation session |
| Secrets leak through logs | All modules → storage | field classification, redaction before formatting and persistence |
| Stale approved Tool is replayed | Worker → Host | DSH call ID, request IDs, canonical payload, browser／tab generation binding |
| Runtime package is replaced | storage → process launch | integrity and version validation before execution |
| Worker escapes lifecycle | process → operating system | Windows Job or macOS／Linux guardian boundary, platform escape fixtures, verified cleanup |
| AI controls unrelated personal tabs | extension → browser | explicit tab assignment, tab-scoped attachment, generation checks |
| Login secrets leak from the user's profile | browser → Host／DSH | no cookie or password APIs, semantic results, structured redaction |

The first-release threat model does not claim protection from arbitrary code already executing as the same operating-system user, an administrator, or a compromised operating system. It does protect against untrusted Worker content, plugins and web pages gaining Host authority without policy and approval.

## Redaction

Redaction is structured first and pattern-based second. Known secret fields are removed before serialisation. Pattern rules provide defence in depth for bearer tokens, API keys, cookies and credential-bearing URLs.

Redaction output must indicate that a value was removed without preserving its length or meaningful prefix. Tests use synthetic secrets and verify absence across logs, audits, UI error payloads and exported diagnostics.

## Diagnostic export

Export is built in a temporary owner-only location, redacted, rendered for preview, and written only after the user chooses a destination. Cancellation removes the temporary artifact. Export never includes browser cookies, stored credentials, conversation bodies or full page contents by default.

## Retention and deletion

- Rotating logs and audit records have bounded retention configurable within supported limits.
- Clearing a browser session closes it before deleting its profile.
- Deletion reports partial failure and leaves a diagnostic record without sensitive contents.
- Uninstall behaviour must state which user-owned data remains; uninstall must not silently delete DSH data directories or Workspaces.

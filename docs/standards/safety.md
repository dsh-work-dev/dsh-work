# Safety and reliability standards

## Authority

- The desktop Host is the final authority for native effects.
- Worker messages, WebView content, process output and persisted configuration
  are untrusted inputs.
- The frontend uses only the generated, allowlisted Host bindings. Workspace
  content has no direct process, filesystem or arbitrary native authority.
- Worker and gateway input is validated for the active generation before it
  changes Host state.
- Local services bind to loopback and still require explicit origin, session
  and route checks; loopback alone is not a trust boundary.

## Data and privacy

- Preserve user and DSH-owned data by default.
- Keep dsh-work application data, installed runtime files, DSH data directories
  and Workspaces as separate ownership domains.
- Persist manager State using its documented field-compatible contract and
  replace complete documents safely. Global Host preferences remain a separate
  versioned contract.
- Do not silently change global shell, package-manager or DSH configuration.
- Redact process output before logging or UI projection.
- Never log credentials, tokens, cookies, sensitive form values or full page
  content.
- Do not upload user content or diagnostics without a separate explicit action.

## Lifecycle reliability

- One owner serializes each lifecycle state machine.
- Each attempt reaches one terminal result: success, cancellation or classified
  failure.
- Startup, shutdown, waits, retries, buffers and background work are bounded.
- Cancellation prevents new effects and propagates through owned work.
- Cleanup is correctness: stopped or quit is not reported before the managed
  boundary is verified clean, or cleanup failure is surfaced.
- A WebView refresh or renderer failure must not restart the Worker or mutate
  native lifecycle state.
- Dependency-specific failures become stable dsh-work errors at the adapter that
  understands them.

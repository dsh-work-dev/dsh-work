# User-browser automation

## Decision

The default path controls the user's running, signed-in Chrome through Chrome's explicit personal-browser agent connection. It does not start a separate profile for ordinary tasks. The user must first enable remote debugging in Chrome and approve Chrome's connection prompt.

The control stack has interchangeable browser adapters:

```text
dsh-work tool
  → DSH tool policy and approval
  → dsh-work Host tool bridge
  → BrowserConnection adapter
      ├── Chrome personal-browser auto-connect (primary)
      ├── signed extension + Native Messaging (compatibility/finer scope)
      └── managed dsh-work profile (safe fallback/test)
```

The primary route preserves current tabs, extensions and login state, but it can expose profile-wide browser data. dsh-work therefore treats connection as a prominent, time-bounded session grant. It does not launch the default profile with legacy `--remote-debugging-port` or `--remote-debugging-pipe`; Chrome 136 and later intentionally ignore those switches for the default data directory.

Official references:

- [Chrome personal-browser agent auto-connect](https://developer.chrome.com/docs/devtools/agents/use-cases/auto-connect)
- [Chrome remote-debugging profile restriction](https://developer.chrome.com/blog/remote-debugging-port)
- [Chrome `debugger` extension API](https://developer.chrome.com/docs/extensions/reference/api/debugger)
- [Chrome native messaging](https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging)
- [Chrome extension permissions](https://developer.chrome.com/docs/extensions/reference/api/permissions)
- [Microsoft Edge native messaging](https://learn.microsoft.com/en-us/microsoft-edge/extensions-chromium/developer-guide/native-messaging)

## Components

### Personal-browser auto-connect adapter

- Requires a supported Chrome version and the browser's remote-debugging setting enabled by the user.
- Connects only after Chrome displays and the user accepts its permission prompt.
- Records the connected browser／profile session and its connection generation.
- Presents available tabs in trusted dsh-work UI; DSH receives only tabs the user assigns.
- Shows a persistent Disconnect action and observes browser-side disconnect.
- Uses a pinned, reviewed agent-control implementation behind `BrowserConnection`; initial implementation may wrap the official Chrome DevTools agent server, but its discovery protocol must be verified before becoming a compatibility promise.

Auto-connect can technically access all profile data surfaced to debugging, including open tabs, storage and cookies. dsh-work policy narrows model-facing operations, but UI must not misrepresent the underlying connection as tab-level technical isolation.

### dsh-work browser extension compatibility adapter

- Manifest V3 extension distributed through supported browser channels when auto-connect is unavailable or finer tab-scoped control is preferred.
- Declares native messaging and debugger capabilities required for direct control.
- Shows connected／attached state in browser UI.
- Attaches only to Host-assigned tab IDs and detaches at task end.
- Translates typed bridge commands into the restricted CDP domain set exposed by `chrome.debugger`.
- Never sends raw cookies, saved passwords, full browsing history or unrelated-tab contents to dsh-work.

### Native messaging helper

The browser launches a small `dsh-work-browser-bridge` executable over the standard length-prefixed stdio protocol. Installers register a manifest separately for supported browsers and platforms.

- Windows: per-user browser registry registration.
- macOS: browser-specific user NativeMessagingHosts location.
- Linux: browser-specific user NativeMessagingHosts location.
- `allowed_origins` contains only the production extension IDs.
- The helper validates the calling extension origin, then connects to the running dsh-work Host over authenticated local IPC.
- The helper contains no browser policy or task logic and exits when either side disconnects.

### dsh-work Host browser service

- owns task, connection and tab-assignment state;
- validates canonical operations and hard safety rules;
- never accepts a raw CDP method from DSH;
- correlates DSH call ID, task ID, tab ID and browser-connection generation;
- discards late events after cancellation or detach.

## Connection and tab model

```text
BrowserConnection
  browser_family
  browser_instance_id
  profile_label          display only; not an identity credential
  extension_id
  connection_method      auto-connect | extension | managed-profile
  connection_generation
  status                 connected | disconnected

TabAssignment
  task_id
  browser_instance_id
  window_id
  tab_id
  initial_origin
  attached_at
  attachment_generation
```

- Connecting a browser does not give an agent permission to execute a high-impact task action.
- A task operates only on its assigned tab even when the underlying auto-connect session is technically profile-wide.
- New tabs created by an approved action must be adopted explicitly into the task before further operations.
- Unrelated tab titles, URLs and contents are not projected to DSH.
- Browser connection and tab attachment are revocable from dsh-work and from the extension.

## Tool protocol

Initial operation kinds:

```text
browser.connection.status
tab.assign
tab.create
page.navigate
page.snapshot
element.activate
element.fill
page.submit
task.cancel
tab.detach
```

Each request contains `protocol_version`, `request_id`, DSH `call_id`, `task_id`, assigned `tab_id`, attachment generation, operation, typed payload, target and scope. Unknown operations are rejected. Duplicate IDs return the recorded terminal result or a protocol error and never execute twice.

Terminal result kinds are `succeeded`, `failed`, `denied`, `cancelled` and `unavailable`.

## Element model

Snapshots expose a bounded structure based on accessible roles, names, states and necessary DOM relationships.

- Element handles are opaque, tab- and document-generation scoped.
- Navigation invalidates old handles.
- A handle is revalidated immediately before action.
- Ambiguous matches fail and request a new snapshot.
- DSH cannot supply arbitrary JavaScript or arbitrary CDP commands.

An adapter may use Chrome Accessibility, DOM, Input, Page and related protocol domains internally, but the public dsh-work protocol remains semantic.

## Operation pipeline

```text
DSH tool pre-policy → DSH one-shot approval when required
  → bridge schema validation → verify connection and assigned tab
  → canonicalise target → Host hard-policy check
  → execute semantic browser operation → sanitise result
  → redacted execution audit → reply through DSH tool call
```

The Host does not show a second approval prompt for an operation already approved through DSH. Browser-owned extension, site-access and debugger warnings remain visible because dsh-work cannot and should not bypass them.

## Navigation and credential rules

- HTTP and HTTPS page navigation are supported.
- Credentials embedded in URLs are rejected.
- Redirects and newly created tabs are checked against task scope.
- Internal browser pages, extension pages, developer tools, `file:` and custom protocols are denied unless a future capability defines them.
- Authentication happens in the user's visible browser tab.
- dsh-work may interact with an already authenticated page but must not call cookie or password-store APIs, export credential values or place them in DSH results.
- TLS errors are terminal failures; automation cannot silently click through them.

## Cancellation and detach

- Cancellation prevents new commands and releases task attachment to assigned tabs.
- Late adapter／protocol events are ignored by attachment generation.
- Task completion releases task control but may leave the user-authorised browser connection active for later tasks.
- Disconnect or dsh-work exit closes the browser adapter connection and bridge IPC without killing the user's browser.
- If the adapter or browser disconnects first, the task returns `unavailable` or `failed` and never falls back to controlling another tab silently.

## Compatibility and fallback

The release matrix lists exact Chrome, Edge or Chromium versions and supported connection methods per operating system. Chrome personal-browser auto-connect is primary where its official contract is available. Edge and extension routes require their own compatibility evidence; unsupported browsers are not silently driven through command-line debugging.

A separate dsh-work-managed profile may remain an explicit fallback for automated tests, privacy-sensitive use or a browser without a supported direct adapter. It is never presented as equivalent to direct user-profile operation and is not the default path.

## Contract tests

Tests cover Chrome auto-connect enable／allow／disconnect, valid and invalid extension IDs, native-host registration, assigned versus unrelated tabs, existing login state, adapter attach／detach, navigation, redirects, stale handles, sensitive fields, DSH approval outcomes, cancellation, browser crash, adapter restart and dsh-work exit. Deterministic tests use local pages; public internet sites are excluded from release gates.

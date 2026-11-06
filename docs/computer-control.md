# Computer control on Windows

Settings → 电脑 selects an engine and manages application access. Installation
is explicit; enabling computer control or selecting an engine only inspects
already-published components. There is no engine fallback.

## Current execution surface

Cua Driver 0.28.1 is connected through the pinned official
`@modelcontextprotocol/client@2.2.0`. The Host projects these tools into the DSH
registry:

| Tool | Contract |
|---|---|
| `computer_windows` | Visible-window metadata, with DSH Work and blocked/elevated targets filtered out. |
| `computer_observe` | An explicitly selected PID/HWND, application permission and screenshot; Cua also returns indexed UIA elements. |
| `computer_click` | A current UIA element or an in-capture window-local point. |
| `computer_type` | Cua: append text to an observed, independently checked editable element. It is not a replace-all operation. |
| `computer_key` | Cua: a key and optional Ctrl/Shift/Alt modifiers on an observed element. Clipboard and system-switching shortcuts are withheld. |
| `computer_scroll` | Cua: the focused region. AUV: an optional screenshot-local point, defaulting to image center. |
| `computer_release` | Release this conversation's engine, temporary grants and desktop lease. |

Input uses window-scoped `delivery_mode: "background"`. An unsupported route is
returned as an error, rather than changing focus. Each attempted action consumes
its observation; observe again to inspect its result. Coordinates must fit the
last returned image. Native browser-window content and input are refused: web
pages use the browser tools and their website/tab authorization contract.

AUV uses the official Node SDK and Windows CLI pinned together to 0.0.43. Its
Windows surface is screenshot observation, coordinate click, scroll and release;
text/key and indexed UIA tools are not registered. The same Host permissions,
snapshot contract, action confirmation, desktop mutex, Stop and pointer apply.
The adapter uses a private launcher-token named pipe, no registration or remote
listener, and explicit `BACKGROUND_ONLY` delivery. It rechecks PID/HWND, title and
frame before acting, maps screenshot pixels through capture bounds, and returns
delivery evidence for the model to verify with another observation. Windows
background keyboard input is unsupported upstream; it is not routed to Cua.

Install or update AUV explicitly in Settings → 电脑 before selecting it. The
updated lock does not install it or change the user's selected engine.

## Application and action decisions belong to the Host

The first content read asks for application access. Permission can last for the
conversation or the current user-message turn. “Always allow” stores an
application rule; Settings can change its access level or remove it. New apps
ask by default. File-manager and settings applications, terminals and code
editors still ask and show a warning without a saved rule, even if new apps are
normally allowed.

Packaged application identity uses Package Family Name. Other applications use
the lowercase executable name and verified Authenticode signer, or the full
lowercase path when no trusted embedded signer is available. Runtime binding
also includes process creation time, HWND incarnation, class/title and bounds.
A WinEvent observer detects handle destruction/reuse. DSH Work and its owned
children, password managers, security/credential UI and elevated processes are
blocked before content is read.

Code editors and terminals default to click-only access. Browser metadata can
appear in discovery, but native browser content remains unavailable. Other apps
default to full control after permission. Payment, sending, publishing, deletion,
installation, settings, commands, closing, submitting, saving and unrecognized
actions default to confirmation. The trusted DSH pre-execute/approval path must
complete before a one-use, argument-bound Host permit can be consumed.

Windows process-path parsing and executable-name classification live in the
native inspector. It supplies application kind (ordinary, browser or terminal)
and the default access level; the shared gateway consumes those results without
parsing executable paths. App-policy overrides do not change the native kind:
browser content remains refused, and terminal submission retains command
confirmation even when an app is granted full access. The kind is internal
inspection metadata, not a user-editable setting or a wire field. Native
reinspection includes it when comparing a target against an issued permit.

This boundary keeps Windows path rules in Windows builds, using Go's standard
`filepath` implementation. Host-dependent parsing in the shared gateway was
rejected because a Windows process path must not change meaning when a shared
test runs on another OS. Revisit the classification boundary when implementing
an actual native backend for another platform.

Cua 0.28.1 does not publish UIA `IsPassword` or `RuntimeId`. For text/key actions,
the Host first refreshes the Cua snapshot and uniquely matches the previously
observed control. A short-lived Job-owned helper reads the same HWND's UIA tree,
matching role, accessible name, bounds and any published automation ID, then
reads native `IsPassword` and `RuntimeId`. Unsupported, ambiguous and timed-out
matches are refused. Immediately before dispatch, a second native check must
match the runtime ID and remain non-password. COM reads run on a separate MTA
thread because the application entry thread is STA.

The two clients do not share an atomic element handle: a UI can still change
between the final check and native delivery. Cua's fresh snapshot token and the
Host checks bound this window, but do not create a transaction. A changed target
requires another observation.

## One owner holds the desktop through cleanup

Go owns conversation identity, grants, permits, serialization and process
lifetimes. A Windows `Local` named mutex also prevents different DSH Work data
roots/processes from operating the same interactive desktop concurrently. Its
opaque synchronization name distinguishes the OS user, window station and
desktop without putting their names in the object name. A dedicated OS thread
acquires/releases the mutex, including abandoned-owner recovery.

The selected Node adapter and its Cua descendants run in the existing Job Object
supervisor and communicate over the existing authenticated, current-user named
pipe. The mutex is retained until the engine Job and all UIA-checker Jobs have been
emptied. Stop cancels in-flight checker requests as part of the same lease.
Cleanup failures retain the stop control and an error state, and block further
execution and component maintenance; the UI directs the user to restart rather
than reporting a successful release.

Normal explicit release and top-level Agent completion release resources and
temporary grants without marking the conversation stopped. A child Agent ending
does not release its top-level owner's desktop. The independent desktop Stop
control revokes future computer dispatch for that user-message round; a new
actual user message can establish a new computer lifetime. Browser lifetimes are
separate. Stop also cancels/terminates owned engine work, but an action already
submitted to the target application may finish afterwards.

The desktop control bar and click-through agent pointer are DSH Work-owned
windows, so they are excluded from actionable targets. The pointer uses the
shared `branding/agent-pointer.svg` and `.json` artwork/hotspot, with target DPI
conversion. It is an application overlay, not the system cursor.
Within the same lease and unchanged target it retains the last actual action
location, so a quick background submission still gives visible feedback. It
does not invent a position before the first located action. A changed target,
Stop or release clears the position. The control bar identifies DSH Work,
distinguishes an executing action from retained computer control, and keeps its
Stop button available independently of the conversation view.

## Why the existing provider is mediated

The prepared computer dependency set contains DSH's rc.5 computer-use/provider
packages and their official MCP client. Loading the provider directly would
register the driver's broader tool surface outside this Host's application,
permit and desktop-lease boundary. The execution adapter therefore uses that
pinned MCP client under Go ownership and registers only the surface above.
Driver management, desktop-wide capture, arbitrary browser, clipboard and
recording tools are not registered. Revisit this boundary if the upstream
provider gains an equivalent enforceable pre-dispatch/ownership contract.

Computer results use rc.5's `createMcpToolDefinition` from the running DSH
runtime. Its attachment service admits and stores screenshots, and its
execution-bound content projection supplies durable image references for the
next model request. Raw MCP image blocks are not DSH attachment references.
The callback retains the same Host-approved execution identity and permit checks.
Observation text also carries a `computer_observation` JSON record containing
the exact PID/HWND, snapshot credential, indexed control names/roles/actions and
screenshot dimensions. Coordinate clicks use window-local screenshot pixels
from the top-left; the model need not read hidden structured output to continue.

## Verification status

The CI Shared contracts matrix exercises shared authorization and lifecycle
logic with injected inspected targets and resources. It does not execute
Windows binaries on macOS/Linux. Windows-only policy tests use drive, UNC,
extended and mixed-case process image paths and pass their classification into
the real authorization gateway. Non-Windows tests exercise the native
unsupported inspector, desktop lease and preparer, and verify that enabling
either engine publishes no computer tools and authorizes no backend launch.
These checks are distinct from the opt-in native interaction tests below;
ordinary CI does not provide their owned application/SDK fixtures.

The opt-in Windows tests exercise the real rc.5 Agent/tool registry, Host app
consent and DSH action approval, official SDK/driver, native fixture state,
password rejection, background append/click/scroll, explicit/top-level release,
scoped Stop and Job/mutex cleanup without model calls. The owned fixture's scroll
witness advanced from line 0 to 49 while its sampled foreground HWND and cursor
were unchanged. The in-flight Stop case records a submitted native callback
finishing afterwards, and rejects subsequent dispatch.

The running development build's settings and native application-permission
panel have been visually inspected. One authorized scratch-conversation model
request completed window discovery and observation, then failed before its
input step: raw MCP screenshot content lacked the DSH attachment reference.
This produced a local `attachmentId` TypeError, reported as `TRANSPORT`.
The projection now uses the official MCP adapter; an offline test with the real
rc.5 attachment store and DeepSeek serializer verifies the next image-bearing
request without network or model calls. A fresh scratch model run then continued
with images, but exposed the missing model-visible snapshot credential. The
Host now publishes the record described above. A real registry observation was
projected through the official adapter and DeepSeek serializer offline; parsing
only the final model-visible text supplied the next background append arguments,
and the owned fixture confirmed the exact append before release.

A subsequent fresh normal-model conversation completed the actual application
permission panel, background append, observation, DSH confirmation for the owned
Slow marker, one background Invoke and explicit release. That run did not use
the Stop bar. Separately, an opt-in native UI harness uses the production window
owner and Stop callback with a real owned Cua lease: its no-delay action retained
the visible shared pointer after `running=false`; clicking the actual Stop
button hid both windows, released the lease and rejected subsequent dispatch.
At 200% DPI the pointer's physical placement matched the shared hotspot, and
foreground samples around the production show helpers were unchanged.

The harness also checks an in-flight provider-return barrier for deterministic
Stop capture; that barrier delays only its test result, not native delivery.
Pointer surface screenshots temporarily remove capture affinity on the isolated
harness-owned pointer HWND and restore `0x11` afterwards. Product capture
protection remains enabled. The native window owner serializes reentrant
creation and uses Wails visibility methods to restore the WebView controller,
not only its HWND.

AUV 0.0.43 was installed in an isolated temporary dependency set. The owned
200%-DPI window captured at 938×729 pixels with matching capture/window bounds.
Its real rc.5 registry path exercised the same official image projection and
offline final model-visible text, rejected an unapproved coordinate click,
confirmed the approved click through an independent fixture counter, observed
background scrolling, released/reacquired the engine, and stopped it with later
dispatch rejected. The desktop mutex was released and no AUV process remained.
This used no model request and did not install into the user's component store.

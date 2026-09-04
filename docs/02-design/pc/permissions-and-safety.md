# Permission ownership and safety

## Decision

dsh-work reuses DSH's approval service and official approval UI for model-requested browser actions. It does not build a competing second approval system.

DSH provides the mechanism, but dsh-work still defines the browser policy. A newly registered DSH Tool is not automatically sensitive: dsh-work must explicitly classify every `browser_*` call through `tools/pre-execute`.

Official references:

- [DSH user approval](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/subsystems/approval.md)
- [DSH Tool pre-execute policy](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/subsystems/tools.md)
- [DSH permission presets](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/subsystems/permission-presets.md)

## Three permission layers

| Layer | Owner | Purpose | Persistence |
|---|---|---|---|
| Browser connection | dsh-work Host + browser | User explicitly lets dsh-work connect to the current browser profile／session | Until disconnect, browser restart or expiry |
| Tool-action approval | DSH `ctx.approval` | Decide whether one high-impact model-requested Tool call may run | One action only |
| Hard safety enforcement | dsh-work Host／browser adapter | Reject invalid, cross-session, protected or unsupported operations | Code and configuration policy |

Browser connection is not blanket approval for every future action. DSH approval is not a substitute for browser-native prompts such as remote-debug connection, extension install, site access, camera, microphone or geolocation.

## dsh-work Tool classifier

One dsh-work-owned `tools/pre-execute` listener claims the complete dsh-work browser Tool vocabulary. It derives a deterministic decision from the Tool name and already validated arguments.

### Automatic inside a connected session

- inspect or snapshot the assigned tab;
- ordinary navigation;
- ordinary click, focus, scroll and keypress;
- fill a non-sensitive field;
- wait for page state;
- detach or cancel.

These actions are automatic because the user already made the high-level decision to connect the browser for AI operation.

### DSH `ask`

- final submission that sends a message, form or publication;
- purchase, payment or financial commitment;
- deletion or irreversible remote-state change;
- account, identity, security or permission changes;
- disclosure or upload of sensitive data;
- file download or opening an external protocol;
- adoption of a newly opened tab or materially expanded target scope when policy requires it.

Only DSH's `allowed-once` outcome executes. `rejected`, `cancelled` and `unavailable` fail closed. If the current DSH session policy disables interactive approval, an `ask` operation is rejected rather than silently upgraded to automatic.

### Always deny

- arbitrary JavaScript or arbitrary CDP methods supplied by the model;
- raw cookies, saved passwords, history or browser credential-store access;
- browser-internal or extension pages;
- a tab not assigned to the task;
- a stale browser／tab generation;
- an operation received after disconnect or cancellation;
- any unknown dsh-work Tool name or unknown operation variant.

Page text and model explanations cannot alter these classifications.

## DSH approval UX

DSH approval identifies the agent, Tool and exact `callId`. Its request intentionally does not duplicate Tool arguments; the official UI attaches the approval to the Tool call already rendered to the user.

dsh-work Tool presentation must therefore make the call card understandable before approval:

- action in plain language;
- exact target origin and assigned tab;
- data being submitted or affected, with sensitive values masked;
- expected external effect;
- reason the classifier selected `ask`.

Closing or cancelling the DSH approval is a non-grant. dsh-work does not display a second native confirmation for the same operation.

## Browser connection grant

Connecting a personal browser can expose open tabs, session and local storage, cookies and other profile data accessible through debugging. The connection surface must state this before the user enables it.

dsh-work shows:

- browser and profile label;
- connection method and start time;
- assigned tab or tabs;
- whether a task is active;
- a prominent Disconnect action.

Disconnect invalidates the connection generation, cancels pending browser operations and prevents new Tool execution. A browser-owned connection prompt or debugging indicator is never hidden or suppressed.

## Trust boundaries

dsh-work treats Agent text, Tool arguments, DSH plugins, browser pages, extension messages and process output as untrusted data.

- A Worker handshake identifies the managed Worker generation; it does not make every plugin trustworthy.
- DSH Tool approval protects execution through the Tool pipeline; it does not sandbox arbitrary plugin code already running inside the DSH process.
- The Host revalidates operation schema, connection, tab assignment and hard-deny rules even after DSH approval.
- Requester display text is context, not a security principal.
- Native messages are accepted only from the registered browser bridge and expected connection generation.

## Approval and execution audit

DSH owns `approval/asked` and `approval/decided` events for one-shot decisions. dsh-work owns browser-connection and execution events. The two records share `callId`, `taskId` and correlation ID.

dsh-work events record event type, timestamp, browser connection generation, assigned tab identity, semantic operation, outcome and error code. They do not duplicate credentials, cookies, full page contents or sensitive form values.

## Prompt-injection containment

Instructions found in a web page are data, not authority. They cannot:

- connect or disconnect a browser;
- assign another tab;
- change `allow`／`ask`／`deny` classification;
- answer a DSH approval request;
- read Host secrets or audit data;
- request raw browser protocol execution;
- override cancellation.

## Local web-origin protection

Loopback does not prevent hostile web pages from probing local ports. dsh-work verifies DSH HTTP and WebSocket Origin／CSRF handling. Where upstream protection is insufficient, a Host-controlled per-generation gateway accepts only the trusted application session and required routes.

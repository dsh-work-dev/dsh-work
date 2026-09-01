# PC release scope

## Release goal

Deliver a dependable Windows, macOS and Linux desktop host for DSH and one useful browser-automation workflow. The release is complete only when ordinary startup, task execution, shutdown and recovery work as one end-to-end experience on every supported platform.

## Target user

The primary user is comfortable using a local AI agent but does not want to manage runtime commands, ports or orphaned processes. They expect clear permission prompts and actionable recovery instructions.

## In scope

### Desktop host

- single-instance application behaviour;
- main window and system tray;
- runtime compatibility check;
- Worker startup, readiness, health, restart and shutdown;
- loopback-only local service access;
- native status, approval and recovery surfaces.

### User-browser automation

- explicit personal-browser agent connection to a supported running Chrome and the user's existing signed-in profile;
- signed extension／native messaging and managed-profile fallback adapters where declared by the compatibility matrix;
- tab selection, attachment and visible control state;
- navigation, page inspection, element activation and text entry;
- DSH-native approval before sensitive submission or trust-boundary changes;
- progress, cancellation and final outcome;
- debugger detachment and bridge cleanup without closing the user's browser or clearing its profile.

### Safety and recovery

- capability- and scope-based approval;
- local audit events;
- redacted diagnostic export with user preview;
- safe-mode entry after repeated startup failure;
- recovery without deleting user-owned DSH data.

## Out of scope

- mobile application or remote access;
- full operating-system UI automation;
- a complete IDE, file manager, Git client or terminal replacement;
- a new plugin marketplace;
- exposing unrestricted desktop APIs to third-party JavaScript;
- silent background execution of destructive actions;
- automatic cloud upload of logs, conversations or credentials;
- background attachment to a browser for which the user has not enabled and approved control;
- reading raw browser cookies, saved passwords, history or unrelated tabs;
- first-release support for browsers outside the declared Chromium compatibility matrix;

## Scope rules

- A feature belongs in the first release only when it is required for the end-to-end workflow or recovery from its likely failures.
- New capabilities need explicit permission semantics before implementation.
- Windows, macOS and Linux process lifecycles must have separate adapters and integration tests behind the same Supervisor contract.
- Optional polish must not delay lifecycle correctness, cancellation, cleanup or recovery.

## Completion definition

Scope is complete when every `MUST` requirement has an acceptance case, all release-blocking cases pass on clean supported Windows, macOS and Linux environments, and no managed process remains after normal or forced application shutdown.

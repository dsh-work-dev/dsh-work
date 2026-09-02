# DSH notification research

- Date checked: 2026-09-03
- Scope: the pinned local DSH runtime (`0.1.2-alpha.3`) and the current public
  DeepSeek Harness repository
- Purpose: determine whether Work can take over an existing DSH web or system
  notification channel

## Findings

### DSH has contextual web notices

The public DSH client contract exposes `SessionInput.notify(level, text)` for
notices associated with a session input surface. The documented levels are
`info` and `error`; this is a contextual UI channel, not a desktop delivery
contract.

- [DSH `SessionInput.notify` contract](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/client/ui-conversation/src/client/input/contract.ts)
- [DSH discussion of the current notice/toast placement](https://github.com/deepseek-ai/deepseek-harness/discussions/489)

The client layout also exposes an overlay slot that can host DSH-owned floating
feedback such as a toast or status pill. Neither surface is a Work notification
API.

### DSH has protocol events, but they are not desktop notifications

The DSH SDK protocol exports typed session and subagent notification messages,
including session event and status notifications. These are runtime protocol
events for clients and SDKs; they are not browser Notification API calls or
operating-system notifications.

- [DSH SDK protocol exports](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/sdk/protocol/src/index.ts)

The local pinned package contains the same separation: notice and toast code is
inside the conversation UI packages, while protocol notifications are used for
runtime observation. No browser `Notification` or service-worker notification
entry point was found in the pinned shell.

### The official shell does not currently provide a stable system channel

An official DSH discussion about background-tab behaviour describes the current
shell as having no Notification API usage and no visibility tracking. The
discussion proposes a future client plugin rather than documenting an existing
core system-notification contract.

- [Official discussion: background-tab notifications](https://github.com/deepseek-ai/deepseek-harness/discussions/5411)

This means there is no existing DSH system-notification stream for Work to
take over directly.

### Community plugins prove the extension seam

Community plugins implement browser or system notifications by observing DSH
client projections and adding their own delivery layer. They demonstrate that
the event seam is useful, but they do not provide a version-stable contract for
Work and may produce duplicate notifications if installed alongside Work.

- [Community DSH browser notification plugin](https://github.com/omdsh-dev/dsh-notification)

## Decision from research

Work should own desktop notification delivery and preferences. DSH should keep
its contextual in-page notices. Work should consume structured DSH events
through a versioned bridge when the event contract is verified for the selected
DSH runtime.

Work must not use any of these as its primary integration:

- DOM or CSS inspection of the DSH page;
- parsing arbitrary process output as user notification events;
- inspecting application WebSocket frames in the gateway;
- overriding `window.Notification` as a substitute for an event contract;
- installing an unowned community plugin as a required dependency.

## Open verification before implementation

The bridge implementation must verify, against the selected DSH version:

1. the stable event names for completion, action-required and error states;
2. the event identity available for deduplication;
3. the target identity needed to focus the correct workspace or session;
4. the supported plugin/client loading seam for a Work-managed bridge;
5. the behaviour when a DSH event arrives during reconnect or Worker restart.

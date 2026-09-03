# Work domain context

This file is the compact ubiquitous language for the Work desktop host. It
contains domain meaning only; product requirements and technical decisions live
in the documents linked from `docs/README.md`.

## Bounded contexts

- **Work host** owns the desktop lifecycle, global preferences and desktop
  delivery of notifications.
- **Work launch context** owns the global runtime, DSH data-directory and
  profile target used to prepare a run; it does not own DSH Workspace records.
- **DSH workspace** owns the agent-facing web experience, Workspace registry
  and feedback that is meaningful only inside a conversation or DSH surface.
- **DSH runtime** owns the runtime protocol and the events emitted by its
  sessions. Work may consume those events but does not redefine DSH session
  semantics.

## Canonical terms

| Term | Meaning | Not this |
|---|---|---|
| Notification event | A meaningful occurrence from Work or DSH that may require delivery outside its source surface. | Raw process output or an arbitrary log line. |
| Desktop notification | A notification delivered through the operating system while Work is not the user's active surface. | A DSH toast or an HTML status message. |
| In-page notice | Contextual feedback rendered by DSH beside the conversation, composer or DSH-owned surface. | A Work-owned global notification. |
| Notification preference | A user choice that enables or suppresses a class of Work desktop notifications. | A switch that changes DSH's own conversation UI. |
| Notification policy | The product rule that decides whether an accepted event is delivered, based on its class, preference and window state. | A user-facing setting. |
| Notification source | The bounded owner that produced an event: Work or DSH. | The transport carrying the event. |
| Notification bridge | The integration boundary that carries structured DSH events to Work without making Work parse DSH presentation markup. | DOM scraping or log parsing. |
| Notification target | The Work or DSH surface the user should return to when acting on a notification. | An arbitrary external URL. |
| Action-required event | An event that needs a user decision before DSH can continue, such as a question or approval. | A routine status update. |
| Completion event | An event that indicates a DSH turn or task has finished. | A partial streaming update. |
| Lifecycle event | A Work or DSH state transition such as startup, unexpected exit or restart. | A successful preference save. |
| Notification delivery | One attempt to present an accepted event through a selected surface. | The event itself; one event may be eligible for more than one surface. |
| Notification deduplication | The rule that prevents one logical event from producing repeated desktop deliveries. | Dismissing or handling the source event. |
| DSH data directory | The user-facing name for the DSH data root that contains profiles and related DSH runtime data. | Work application data, an installed runtime, or a Workspace directory. |
| DSH home | DSH's external technical name for a DSH data directory. Work's domain term is DSH data directory. | A Work-global launch target or a Workspace. |
| DSH Workspace | A DSH-owned persistent record for a canonical directory, its identity/title and associated sessions. | The DSH data directory, a profile, or a Work setting. |
| Workspace context | The DSH Workspace selected or resumed for one active session or Worker generation. | A field in Work's global launch target. |
| Launch target | The persisted Work selection of exactly one DSH runtime, data directory and profile. | A Workspace context or a complete session request. |
| Launch context | The resolved facts for one run: launch target plus the separately resolved Workspace context and generation state. | The durable global configuration document. |

## Ownership rules

1. DSH remains the source of truth for DSH conversation and session state.
2. Work remains the source of truth for desktop notification preferences and
   desktop delivery decisions.
3. A Work preference may suppress a Work desktop delivery, but it must not
   remove or alter an in-page notice that DSH needs for context.
4. A notification event is not a diagnostic event. Diagnostics may explain an
   event, but raw diagnostics are never notification copy.

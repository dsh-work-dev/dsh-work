# dsh-work domain context

This file is the compact ubiquitous language for the dsh-work desktop host. It
contains domain meaning only; product requirements and technical decisions live
in the documents linked from `docs/README.md`.

## Bounded contexts

- **dsh-work host** owns the desktop lifecycle, global preferences, Desktop Pet
  surface and desktop delivery of notifications.
- **dsh-work run context** owns the runtime, DSH data directory and profile that
  dsh-work is currently running or attempting to run, plus verified version snapshots
  used for recovery; it does not own DSH Workspace records.
- **DSH workspace** owns the agent-facing web experience, Workspace registry
  and feedback that is meaningful only inside a conversation or DSH surface.
- **DSH runtime** owns its executable and protocol. dsh-work adapts its launch
  and readiness behavior but does not redefine DSH session semantics.

## Canonical terms

| Term | Meaning | Not this |
|---|---|---|
| Desktop Pet | A dsh-work-owned animated surface displayed in a separate native window outside the DSH Workspace. | DSH conversation content or an arbitrary downloaded application. |
| Pet catalog | The Host-controlled set of validated Pet assets available for selection. | A package execution environment or a DSH plugin registry. |
| Pet visibility intent | The user's persisted choice to show or hide the selected Pet. | Proof that a native window is currently visible. |
| Pet position | A persisted monitor-relative anchor plus native window dimensions and scale. | A DSH Workspace directory or a screen-absolute promise that survives every monitor layout. |
| Pet preview | A Host-projected frame or safe fallback used by trusted Settings UI. | Direct access by Settings UI to package files. |
| Notification event | A meaningful occurrence accepted by the dsh-work notification router for possible desktop delivery. | Raw process output or an arbitrary log line. |
| Desktop notification | A notification delivered through the operating system while dsh-work is not the user's active surface. | A DSH toast or an HTML status message. |
| In-page notice | Contextual feedback rendered by DSH beside the conversation, composer or DSH-owned surface. | A dsh-work-owned global notification. |
| Notification preference | A user choice that enables or suppresses a class of dsh-work desktop notifications. | A switch that changes DSH's own conversation UI. |
| Notification policy | The product rule that decides whether an accepted event is delivered, based on its class, preference and window state. | A user-facing setting. |
| Notification target | The dsh-work or DSH surface the user should return to when acting on a notification. | An arbitrary external URL. |
| Action-required event | An event that needs a user decision before DSH can continue, such as a question or approval. | A routine status update. |
| Completion event | An event that indicates a DSH turn or task has finished. | A partial streaming update. |
| Lifecycle event | A dsh-work or DSH state transition such as startup, unexpected exit or restart. | A successful preference save. |
| Notification delivery | One attempt to present an accepted event through a selected surface. | The event itself; one event may be eligible for more than one surface. |
| Notification deduplication | The rule that prevents one logical event from producing repeated desktop deliveries. | Dismissing or handling the source event. |
| DSH data directory | The user-facing name for the DSH data root that scopes profiles, their plugin associations and related DSH runtime data. | dsh-work application data, an installed runtime, or a Workspace directory. |
| DSH home | DSH's external technical name for a DSH data directory. dsh-work's domain term is DSH data directory. | A Configured Run context or a Workspace. |
| DSH profile | A named configuration scope inside one DSH data directory. The profile owns the plugin association set used when that profile runs. | A runtime, a Workspace, or a global plugin set. |
| DSH plugin | An extension package associated with a profile within a DSH data directory. Its association is not global to the runtime or dsh-work. | A runtime component or a dsh-work-global setting. |
| DSH Workspace | A DSH-owned persistent record for a canonical directory, its identity/title and associated sessions. | The DSH data directory, a profile, or a dsh-work setting. |
| Workspace context | The DSH Workspace selected or resumed for one active session or Worker generation. | A field in dsh-work's run context. |
| Run context | The complete dsh-work selection of one DSH runtime, Node selection, DSH data directory and profile that defines one Worker generation. | A Workspace context or a durable settings document. |
| Configured Run context | The Run context persisted as dsh-work's selected context. While dsh-work is running, changing it starts an immediate context switch rather than waiting for another launch. | A deferred or partially selected target. |
| Known-good run context | The last run context that reached a healthy ready state whose recorded versions identify a recovery target; restoring it requires an available compatible snapshot. | An unverified candidate. |
| Current profile | The profile in the current healthy Run context. Only this profile's plugin associations may be modified; non-current profiles are read-only. | The profile merely selected for inspection. |
| Context switch | A user-requested change of runtime, DSH data directory or profile that takes effect by restarting the Worker and completes only after the new context is ready; failure follows the configured recovery policy. | Editing a deferred selection without applying it. |
| Version snapshot | A recorded DSH/plugin version set with dependency inputs and verification metadata used to reconstruct an environment. | A copy of installed package trees or conversation data. |
| Last successful snapshot | The latest verified success record for a profile; a separate last-running pointer identifies the previous successful environment for switches. | Proof that a Worker is currently alive. |
| Safe mode | A clean Host-prepared environment with a separate data directory and a saved return target. | A replacement for the normal environment's success record. |
| Launch context | The per-generation handoff containing a resolved Run context plus its separately resolved Workspace context. | A durable global configuration document. |

## Ownership rules

1. DSH remains the source of truth for DSH conversation and session state.
2. dsh-work remains the source of truth for desktop notification preferences and
   desktop delivery decisions.
3. dsh-work remains the source of truth for Pet selection, visibility, size,
   position and native-window behavior.
4. A dsh-work preference may suppress a dsh-work desktop delivery, but it must not
   remove or alter an in-page notice that DSH needs for context.
5. The profile in the current run context is the only DSH profile whose plugin
   associations dsh-work may modify. Non-current profiles are read-only until the
   user switches the run context to that profile. Snapshot recovery may reapply
   recorded plugin inputs while the Worker is stopped.
6. A context switch changes the runtime, DSH data directory and profile as one
   unit. It becomes current only after the new Worker is healthy; a failed
   switch follows the configured recovery policy.

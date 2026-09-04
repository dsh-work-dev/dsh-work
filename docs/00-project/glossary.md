# Glossary

| Term | Meaning in dsh-work |
|---|---|
| dsh-work | The desktop host application described by this repository. |
| Host shell | The embedded trusted dsh-work startup, status and recovery UI; it is not the DSH Web UI. |
| DSH | DeepSeek Harness, the agent runtime hosted and supervised by dsh-work. |
| DSH runtime | One immutable, installed and versioned DSH distribution: its executable, launcher and built-in bundles. It does not own profile-specific plugin state. |
| DSH data directory | The user-facing name for the DSH data root that scopes named profiles, their plugin associations and related DSH runtime data. It may be dsh-work-managed or an explicitly selected existing user directory; it is distinct from dsh-work application data and an installed DSH runtime. |
| DSH home | DSH's external technical name for a DSH data directory, including the `DSH_HOME` environment-variable term. dsh-work's domain term is `DSH data directory`. |
| DSH profile | A named configuration under a DSH data directory. It owns ordered bundle references, plugin dependency state, profile patch layers and profile data; it is not owned by a DSH runtime. |
| DSH plugin | A package or bundle associated with one DSH profile through DSH's supported profile plugin seam. The same package in another profile is a separate association. |
| DSH Workspace | A DSH-owned persistent record for a canonical directory, stable identity/title and associated sessions. The dsh-work Workspace window displays its DSH Web UI; it is not the DSH data directory, profile store or Host shell. |
| Workspace context | The DSH Workspace selected or resumed for one active session or Worker generation. It is resolved separately from dsh-work's Run context and may be carried by one launch/session request. |
| Notification event | A meaningful dsh-work or DSH occurrence that may need delivery outside its source surface; raw process output is not a notification event. |
| Desktop notification | A notification delivered through the operating system by dsh-work. |
| In-page notice | Contextual feedback rendered by DSH inside its own Web UI, such as a conversation or composer notice. |
| Notification preference | A dsh-work-global user choice that enables or suppresses a class of desktop notifications; it does not hide DSH in-page notices. |
| Notification policy | The rule that combines an event class, the user's preferences and window state to decide desktop delivery. |
| Notification bridge | A versioned boundary that carries structured DSH events to dsh-work without parsing DSH presentation markup. |
| dsh-work CLI | The explicit operator tool that installs and selects DSH runtimes and manages profile/plugin operations. |
| Runtime/profile compatibility | The result of validating one DSH runtime against one profile's bundle, plugin and patch composition. |
| Runtime selection | The exact DSH runtime selected for one Run context; it is paired with, but does not own, a profile. |
| Run context | The exact triple of DSH runtime identity, DSH data-directory identity and profile name that defines one DSH execution environment. The data directory scopes the profile and its plugin associations; the runtime is an immutable executable distribution. |
| Configured Run context | The Run context persisted by dsh-work as the user's selected context. Changing any member while dsh-work is running starts an immediate managed context switch; it is not a deferred next-launch setting and contains no Workspace. |
| Known-good Run context | The last Run context whose Worker reached `Ready`. It is the automatic rollback destination when a context switch fails. |
| Context switch | The managed stop/start operation that replaces one Run context with another. dsh-work commits the candidate only after readiness and restores the known-good context on failure; overlapping Workers are not allowed. |
| Launch target | The serialized/internal name for a Configured Run context in existing interfaces. It does not mean a target that waits for a future launch. |
| Launch context | The resolved Run context plus a separately resolved Workspace context for one dsh-work Worker generation. |
| Profile reference | The stable pair of DSH data-directory identity and profile name required by profile-scoped operations; a profile name alone is not sufficient. |
| Current profile | The profile reference in the current `Ready` Run context. Plugin installation, removal and other profile mutations are allowed only for this profile; a non-current profile is read-only. |
| Settings window | A separate trusted dsh-work WebView window for dsh-work-global settings and the nested DSH manager. It never loads the DSH Web UI or shares a document with the Workspace window. |
| DSH manager | The runtime catalog, Run context and profile/plugin management area inside the Settings window; it delegates profile composition to DSH, enforces current-profile mutation scope and does not own Workspace selection. |
| Workspace window | The dsh-work WebView window that displays the external DSH workspace. dsh-work's application menu and system tray remain Host-owned without modifying DSH page content. |
| Host | The dsh-work desktop process and its trusted backend services. |
| Worker | A DSH process started and supervised by the Host. |
| Supervisor | Host module responsible for the Worker lifecycle and health. |
| Adapter | A boundary that isolates a version-specific or platform-specific integration. |
| Managed process | A process started by dsh-work and assigned to dsh-work's lifecycle boundary. |
| Ready | The Worker has passed its readiness contract and may be shown to the user. |
| Browser connection | An explicit link between dsh-work and its installed browser extension in the user's existing browser. |
| Tab assignment | The exact browser window and tab the user allows one dsh-work task to control. |
| Tool operation | One requested local action with declared capability, scope and risk. |
| Capability | A category of authority such as browser navigation or file read. |
| Scope | The concrete resource limits attached to a capability, such as an origin or directory. |
| Approval | A user decision that allows one operation or a clearly bounded group of operations. |
| High-risk operation | An operation that may destroy data, expose secrets, cross a trust boundary or execute code. |
| Safe mode | A recovery launch that disables optional integrations while preserving user-owned data. |
| Audit event | A structured local record of a security-relevant request, decision or outcome. |
| PC release | The desktop application release for Windows, macOS and Linux. |
| Process guardian | A small platform helper that survives the GUI Host long enough to terminate and reap a managed Worker group after Host loss. |

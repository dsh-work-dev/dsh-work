# Glossary

| Term | Meaning in Work |
|---|---|
| Work | The desktop host application described by this repository. |
| Host shell | The embedded trusted Work startup, status and recovery UI; it is not the DSH Web UI. |
| DSH | DeepSeek Harness, the agent runtime hosted and supervised by Work. |
| DSH runtime | One immutable, installed and versioned DSH distribution: its executable, launcher and built-in bundles. It does not own profile-specific plugin state. |
| DSH home | A DSH data root containing named profiles and related runtime data. It may be Work-managed or an explicitly selected existing user home. |
| DSH profile | A named configuration under a DSH home. It owns ordered bundle references, plugin dependency state, profile patch layers and profile data; it is not owned by a DSH runtime. |
| DSH plugin | A package or bundle associated with one DSH profile through DSH's supported profile plugin seam. The same package in another profile is a separate association. |
| DSH workspace | The Web UI and session served by the out-of-process DSH Worker for one runtime/profile launch; it is not the profile store or the Host shell. |
| Notification event | A meaningful Work or DSH occurrence that may need delivery outside its source surface; raw process output is not a notification event. |
| Desktop notification | A notification delivered through the operating system by Work. |
| In-page notice | Contextual feedback rendered by DSH inside its own Web UI, such as a conversation or composer notice. |
| Notification preference | A Work-global user choice that enables or suppresses a class of desktop notifications; it does not hide DSH in-page notices. |
| Notification policy | The rule that combines an event class, the user's preferences and window state to decide desktop delivery. |
| Notification bridge | A versioned boundary that carries structured DSH events to Work without parsing DSH presentation markup. |
| dsh-work CLI | The explicit operator tool that installs and selects DSH runtimes and manages profile/plugin operations. |
| Runtime/profile compatibility | The result of validating one DSH runtime against one profile's bundle, plugin and patch composition. |
| Runtime selection | The exact DSH runtime selected for one launch; it is paired with, but does not own, a profile. |
| Launch selection | The immutable runtime, DSH home/profile and workspace inputs resolved for one Work Worker generation. |
| Profile reference | The stable pair of DSH home identity and profile name required by profile-scoped operations; a profile name alone is not sufficient. |
| Settings window | A separate trusted Work WebView window for Work-global settings and the nested DSH manager. It never loads the DSH Web UI or shares a document with the Workspace window. |
| DSH manager | The runtime, DSH home, profile and profile-scoped plugin management area inside the Settings window; it delegates profile composition to DSH. |
| Workspace window | The Work WebView window that displays the external DSH workspace. Work's application menu and system tray remain Host-owned without modifying DSH page content. |
| Host | The Work desktop process and its trusted backend services. |
| Worker | A DSH process started and supervised by the Host. |
| Supervisor | Host module responsible for the Worker lifecycle and health. |
| Adapter | A boundary that isolates a version-specific or platform-specific integration. |
| Managed process | A process started by Work and assigned to Work's lifecycle boundary. |
| Ready | The Worker has passed its readiness contract and may be shown to the user. |
| Browser connection | An explicit link between Work and its installed browser extension in the user's existing browser. |
| Tab assignment | The exact browser window and tab the user allows one Work task to control. |
| Tool operation | One requested local action with declared capability, scope and risk. |
| Capability | A category of authority such as browser navigation or file read. |
| Scope | The concrete resource limits attached to a capability, such as an origin or directory. |
| Approval | A user decision that allows one operation or a clearly bounded group of operations. |
| High-risk operation | An operation that may destroy data, expose secrets, cross a trust boundary or execute code. |
| Safe mode | A recovery launch that disables optional integrations while preserving user-owned data. |
| Audit event | A structured local record of a security-relevant request, decision or outcome. |
| PC release | The desktop application release for Windows, macOS and Linux. |
| Process guardian | A small platform helper that survives the GUI Host long enough to terminate and reap a managed Worker group after Host loss. |

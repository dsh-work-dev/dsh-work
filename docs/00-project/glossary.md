# Glossary

| Term | Meaning in Work |
|---|---|
| Work | The desktop host application described by this repository. |
| DSH | DeepSeek Harness, the agent runtime hosted and supervised by Work. |
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

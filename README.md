# dsh-work

dsh-work is a local-first desktop host for
[DeepSeek Harness (DSH)](https://github.com/deepseek-ai/deepseek-harness). It
turns a separately managed DSH runtime into a dependable desktop application
while leaving agents, plugins, conversations and Workspaces under DSH ownership.

## What it provides

- a trusted desktop shell and Settings surface;
- explicit DSH runtime, data-directory and profile management;
- serialized Run-context switching with configurable recovery;
- automatic and manual version snapshots for DSH and profile plugins;
- supervised Worker startup, readiness and shutdown;
- authenticated local IPC and binary WebView streams to the DSH Workspace;
- a per-user background daemon shared by the desktop UI and online manager CLI;
- background tasks that survive desktop window closure and UI process crashes;
- persistent locale, recovery, Pet and desktop-notification preferences;
- remembered Workspace and Settings window sizes and maximised state;
- Windows Job Object ownership for the Worker process tree.

Closing a desktop window hides it and keeps its WebView in the UI client, so
reopening returns to the same page state. The tray, Worker, Pet and
notifications remain with the background daemon. Use “停止后台并退出” in the
tray or application menu to stop background work and exit.

## Project boundary

dsh-work owns desktop lifecycle, native integration, trusted settings and the
boundary through which DSH reaches the desktop. DSH owns agent behaviour,
profiles and plugin composition, Workspace identity, sessions and conversation
state. dsh-work does not rewrite those systems or inject Host UI into the DSH
Workspace.

## Status

The Windows foundation and per-user NSIS installer are implemented. Shared
contracts compile on Windows, macOS and Linux, while production process
supervision and packaging are still Windows-first. Application self-update,
release signing and update-feed publication remain in the next delivery stage.

See the [project documentation](docs/README.md) for the accepted product and
architecture baseline. Build, test and contribution instructions are in
[CONTRIBUTING.md](CONTRIBUTING.md).

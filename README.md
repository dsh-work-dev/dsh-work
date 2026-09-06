# dsh-work

dsh-work is a local-first desktop host for
[DeepSeek Harness (DSH)](https://github.com/deepseek-ai/deepseek-harness). It
turns a separately managed DSH runtime into a dependable desktop application
while leaving agents, plugins, conversations and Workspaces under DSH ownership.

## What it provides

- a trusted desktop shell and Settings surface;
- explicit DSH runtime, data-directory and profile management;
- atomic Run-context switching with known-good rollback;
- supervised Worker startup, readiness and shutdown;
- a trusted loopback gateway to the DSH Workspace;
- persistent locale, close-to-tray and desktop-notification preferences;
- Windows Job Object ownership for the Worker process tree.

## Project boundary

dsh-work owns desktop lifecycle, native integration, trusted settings and the
boundary through which DSH reaches the desktop. DSH owns agent behaviour,
profiles and plugin composition, Workspace identity, sessions and conversation
state. dsh-work does not rewrite those systems or inject Host UI into the DSH
Workspace.

## Status

The Windows foundation is implemented. Shared contracts compile on Windows,
macOS and Linux, while production process supervision and packaging are still
Windows-first.

See the [project documentation](docs/README.md) for the accepted product and
architecture baseline. Build, test and contribution instructions are in
[CONTRIBUTING.md](CONTRIBUTING.md).

# Project overview

## Purpose

dsh-work turns a command-line-hosted DSH environment into a dependable desktop application. It removes routine lifecycle work from the user and provides a controlled bridge from an agent to local desktop capabilities.

## Product boundary

dsh-work owns:

- application window and system-tray behaviour;
- discovery or provisioning of a compatible DSH runtime;
- DSH process startup, readiness, monitoring and shutdown;
- local permission prompts and audit events;
- an explicit connection to a user's installed browser and controlled tabs;
- diagnostics and recovery entry points.

DSH owns:

- agents, prompts, models and conversations;
- plugin discovery and execution;
- agent-specific tools and business logic;
- its own persistent data formats.

dsh-work must integrate through supported seams. It must not reimplement DSH agent behaviour or silently mutate DSH-owned data.

## First release

The first PC release targets Windows, macOS and Linux and proves one complete local workflow on every supported platform:

1. launch dsh-work;
2. start and display DSH reliably;
3. connect the user's browser and run a DSH-approved task in the selected tab;
4. show its progress and result;
5. exit without leaving managed processes behind;
6. recover from common startup failures without manual file editing.

Remote control, full desktop UI automation and a plugin marketplace are outside this release.

## Design principles

1. **Local by default.** Local services are not exposed to the network unless a future feature explicitly changes that contract.
2. **Least authority.** A tool receives only the capability and scope needed for the current operation.
3. **Visible state.** Starting, ready, busy, waiting for approval, failed and recovering are explicit states.
4. **Recoverable failure.** A failed worker must not make the desktop shell unusable.
5. **Replaceable integration.** DSH, operating-system and browser details sit behind adapters.
6. **No hidden global mutation.** dsh-work does not silently change global shell, package-manager or browser settings.

## Document authority

When documents disagree, use this order:

1. accepted ADR for architecture decisions;
2. product requirements for observable behaviour;
3. security and permission specification;
4. interaction and engineering details;
5. roadmap descriptions.

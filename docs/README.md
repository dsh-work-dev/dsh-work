# DSH Work documentation

This directory contains the accepted project knowledge for DSH Work. It
describes the product as it exists and the rules current implementation must
preserve.

## Reading map

1. [Product](product.md) — purpose, ownership and current capabilities.
2. [Architecture](architecture.md) — implemented modules, dependency direction,
   transport boundaries and runtime invariants.
3. [Desktop Pet](desktop-pet.md) — the Host-owned Pet surface, persistence,
   interaction and current release limits.
4. [Version recovery](version-recovery.md) — verified snapshots, package-manager
   restoration, failure policy and supported limits.
5. [Settings and startup](settings.md) — navigation, selective hierarchy and
   contextual feedback.
6. [Safety standards](standards/safety.md) — authority, data and failure
   containment rules.
7. [Interface standards](standards/interface.md) — trusted UI, copy, states and
   accessibility rules.
8. [Verifying the running app](agent-verification.md) — driving the dev build
   over CDP instead of taking over the desktop.
9. [Set up browser control](browser-setup.md) — enable, install, bind, repair and
   update the Chrome/Edge extension and its components.
10. [Licensing](licensing.md) — AGPL and commercial options, contribution rights
    and release obligations.
11. [Responding to suspected infringement](enforcement.md) — ownership records,
    evidence preservation, compliance notices and escalation.
12. [Name and brand use](branding.md) — DSH Work spelling, technical identifiers,
    visual assets, attribution and third-party distributions.

Design decisions live with the feature or boundary they govern: each section
states the current choice, why it was made, what was rejected and when to
revisit it. When a decision changes, rewrite that section; Git keeps the
history.

Domain terminology and ownership rules are maintained in
[CONTEXT.md](../CONTEXT.md). Development setup, tests and engineering workflow
are maintained in [CONTRIBUTING.md](../CONTRIBUTING.md).

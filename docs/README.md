# dsh-work documentation

This directory contains the accepted project knowledge for dsh-work. It
describes the product as it exists and the rules current implementation must
preserve.

## Reading map

1. [Product](product.md) — purpose, ownership and current capabilities.
2. [Architecture](architecture.md) — implemented modules, dependency direction,
   transport boundaries and runtime invariants.
3. [Decisions](decisions.md) — accepted architectural choices that explain the
   current shape.
4. [Desktop Pet](desktop-pet.md) — the Host-owned Pet surface, persistence,
   interaction and current release limits.
5. [Version recovery](version-recovery.md) — verified snapshots, package-manager
   restoration, failure policy and supported limits.
6. [Settings and startup](settings.md) — navigation, selective hierarchy and
   contextual feedback.
7. [Safety standards](standards/safety.md) — authority, data and failure
   containment rules.
8. [Interface standards](standards/interface.md) — trusted UI, copy, states and
   accessibility rules.

Domain terminology and ownership rules are maintained in
[CONTEXT.md](../CONTEXT.md). Development setup, tests and engineering workflow
are maintained in [CONTRIBUTING.md](../CONTRIBUTING.md).

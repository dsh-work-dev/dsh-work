# Public traceability index

This index connects public project contracts only. Product rationale outside the repository is not required to understand or implement any item.

| Behaviour area | Requirements | Design | Engineering | Acceptance |
|---|---|---|---|---|
| Host lifecycle | FR-LIFE-* | `states-and-errors.md` | `architecture.md` | AC-001–AC-003, AC-023 |
| Worker supervision | FR-SUP-* | `states-and-errors.md` | `dsh-integration.md`, three platform adapters in `process-supervision.md` | AC-002–AC-007 |
| Embedded navigation | FR-WEB-* | `information-architecture.md` | `architecture.md`, `storage-and-security.md` | AC-004, AC-006, AC-015 |
| DSH Run context, profile ownership and Workspace context | FR-MGR-001–FR-MGR-015 | `information-architecture.md`, `interaction-spec.md` | `architecture.md`, `dsh-integration.md`, ADR-0007, ADR-0008 | AC-029, AC-031, AC-032, AC-043–AC-047 |
| Browser tasks | FR-BRW-* | `interaction-spec.md` | `browser-automation.md` | AC-009–AC-015 |
| Permission and audit | FR-SEC-* | `permissions-and-safety.md` | `storage-and-security.md` | AC-008, AC-011–AC-016, AC-019 |
| Notifications | FR-NOT-* | `notifications.md` | `notifications.md`, `dsh-integration.md`, ADR-0006 | AC-036–AC-042 |
| Recovery | FR-REC-* | `states-and-errors.md` | `dsh-integration.md`, `storage-and-security.md` | AC-006, AC-017–AC-019 |
| Reliability | NFR-REL-* | `states-and-errors.md` | `architecture.md`, `process-supervision.md` | AC-002, AC-013, AC-023 |
| Security | NFR-SEC-* | `permissions-and-safety.md` | `storage-and-security.md` | AC-004, AC-008, AC-024 |
| Accessibility | NFR-ACC-* | `interaction-spec.md` | trusted UI implementation | AC-021, AC-022 |
| Observability | NFR-OBS-* | `states-and-errors.md` | all edge adapters | AC-006, AC-010, AC-019 |
| Maintainability | NFR-MNT-* | n/a | `architecture.md` and adapter contracts | AC-006, AC-008, AC-010, AC-018 |
| Platform support | NFR-PORT-* | n/a | `process-supervision.md` and platform adapters | AC-005, AC-025 |

All paths above are relative to their area directories:

- design: `docs/02-design/pc/`
- engineering: `docs/03-engineering/pc/`
- acceptance: `docs/04-delivery/acceptance-criteria.md`

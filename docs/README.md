# dsh-work documentation

This directory contains publishable project documentation. Every file must be understandable without access to private planning material.

## Reading map

| Area | Start here | Purpose |
|---|---|---|
| Project | [Project overview](00-project/overview.md) | Product boundary, principles, terminology |
| Technical guidance | [Global technical guidance](00-project/technical-guidance.md) | Cross-module engineering rules and review criteria |
| PC product | [Scope](01-product/pc/scope.md) | First-release scope and non-goals |
| PC behaviour | [Requirements](01-product/pc/requirements.md) | Normative, testable product contract |
| PC notifications | [Notification product specification](01-product/pc/notifications.md) | Notification classes, preferences and delivery rules |
| PC UI | [UI design guidelines](02-design/pc/ui-guidelines.md) | Global visual language, patterns and acceptance |
| PC interaction | [Interaction specification](02-design/pc/interaction-spec.md) | Screens, actions, feedback and accessibility |
| PC notification design | [Notification interaction design](02-design/pc/notifications.md) | Notification surfaces, routing and accessibility |
| PC engineering | [Architecture](03-engineering/pc/architecture.md) | Modules, boundaries and runtime flow |
| Delivery | [Roadmap](04-delivery/roadmap.md) | Build order and completion gates |
| First implementation | [PC foundation plan](04-delivery/pc-foundation-plan.md) | Windows-first DSH path governed by three-platform contracts |
| Contribution | [Contributing](CONTRIBUTING.md) | How to change code and documentation |

## Normative language

- `MUST` / `MUST NOT`: required for the stated release.
- `SHOULD` / `SHOULD NOT`: expected unless a documented reason prevents it.
- `MAY`: optional behaviour.

Requirements use stable IDs. Design, engineering and test documents refer to those IDs so a behaviour can be followed from specification to verification.

## Documentation rules

- Describe dsh-work itself, its public interfaces and its contribution process.
- Do not publish third-party comparisons, confidential measurements, user research, credentials or unpublished business plans.
- Keep implementation details out of product requirements unless users depend on them.
- Record a significant technical decision in an ADR before changing a public architectural boundary.
- Apply the global technical and UI guidance when changing engineering structure or visible interface.
- Update requirements and acceptance criteria in the same change when observable behaviour changes.

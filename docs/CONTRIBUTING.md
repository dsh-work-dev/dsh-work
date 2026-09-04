# Contributing to dsh-work

## Before changing behaviour

1. Find the affected requirement ID in `01-product/pc/requirements.md`.
2. Check its acceptance case in `04-delivery/acceptance-criteria.md`.
3. Read the relevant state, permission and architecture contract.
4. Apply `00-project/technical-guidance.md` to implementation decisions and `02-design/pc/ui-guidelines.md` to visible interface changes.
5. For a significant boundary or dependency change, add or update an ADR.

If no requirement describes the behaviour, update the product and acceptance documents before or with the implementation.

## Implementation expectations

- Build one end-to-end slice at a time.
- Keep operating-system, DSH and browser details behind adapters.
- Add a failing test at an agreed seam before implementing observable behaviour.
- Use stable error codes and structured events rather than parsing presentation text.
- Bound retries, waits, buffers and background work.
- Preserve cancellation and cleanup on every error path.
- Never log credentials, cookies, sensitive form values or full page contents.

## Documentation safety

Repository documentation is public. Do not add:

- competitive analysis or third-party capability comparisons;
- internal targets, unpublished roadmap details or business strategy;
- user interview notes or customer-identifying information;
- private links, credentials, tokens or raw diagnostic bundles.

Public documentation should describe dsh-work's own behaviour, interfaces, decisions and contribution process. When unsure whether material is publishable, leave it out of the change and ask a maintainer.

## Verification

Run the narrowest affected test while developing, then before review run:

- formatting and static checks;
- all unit and contract tests;
- affected Windows, macOS and Linux platform integration tests;
- documentation link and public-content checks;
- the relevant acceptance scenario.

Release-affecting changes also require packaging and clean-environment tests.

## Pull request checklist

- [ ] Observable behaviour maps to a requirement ID.
- [ ] Acceptance criteria changed when the contract changed.
- [ ] New capability has scope, risk, approval and audit semantics.
- [ ] Failure, cancellation and cleanup paths are tested.
- [ ] Persisted or wire schema changes are versioned and migrated.
- [ ] Logs and diagnostics pass synthetic-secret tests.
- [ ] Public documentation contains no confidential research or internal metrics.
- [ ] Dependency versions and notices are updated where needed.
- [ ] Visible UI follows semantic tokens, complete state coverage and platform accessibility guidance.

# E-68 browser verification

`dashboard-verification.html` and `dashboard-verification.ts` are a local-only mounting harness with explicit synthetic data. They are not imported by the production app. No Host bindings, DSH API calls or real mutations occur. The loopback capture server only stores screenshots/reports under `output/playwright/e68`.

Run the Vite dev server on loopback, open `/dashboard-verification.html`, and use `window.dashboardVerification` for the fixture state/theme controls. Production uses `mountDashboard(...).update(realData)` instead.

## Evidence (2026-11-09)

- All 27 supported theme/mode combinations: eleven tiles retained, no grid-tile intersections, no horizontal body overflow, no SVG conversation labels outside their viewBox.
- Sampled rendered text includes headers, body labels, SVG constellation labels, gauge text and PC captions. Solid-surface alpha-composited contrast measurements passed 4.45:1 tolerance; screenshots/contact sheet also show the translucent/ornamental themes. Final native WebView acceptance belongs to the integrating parent task.
- Found and fixed Claude inherited negative group margins (overlapping grid tiles) and Bauhaus/Classic header reading surfaces.
- Actual pointer drag on SortableJS's compatible fallback engine moved `产出` before `今天的对话` within the conversation group. Native HTML drag did not start reliably over the browser debugging channel; fallback mode now provides the maintained library's pointer/touch path in the WebView.
- Group down movement saved `用量/对话/环境`. Reload preserved both the group order and dragged tile order. Restore defaults removed the saved key and restored all eleven default blocks.
- Continue/open session, environment switch, save restore point, plugin upgrade and plugin settings invoked the expected fixture callback IDs. Failed-host retry and restore were enabled with their real-data-shaped identifiers; retry invoked `retry:memory` in the fixture log.
- Week selection changed the timeline to date labels. Empty input kept all eleven blocks with eleven unavailable badges and em dashes instead of fabricated numeric zero.
- 900px content width retained twelve columns and 7/5 conversation layout; independent screenshot captured. Narrow-container breakpoints were rechecked: 900px = 12 columns / 506px star; 700px = 6 columns / 376px star; 500px = 2 columns / 372px star. See responsive-evidence.json.

Artifacts: `output/playwright/e68/theme-contact-sheet.png`, individual `<theme>-<mode>.png`, `theme-audit.json`, `900-content-width.png`, `interaction-evidence.json`.

Functional checks: frontend `npm run typecheck` passed; three layout-order/normalization tests passed via `npx tsx --test src/dashboard-layout.test.ts`.

## Interaction follow-up (2026-11-25)

- At 900px, moving a tile past the full-width timeline also moved it on screen. Enter and Space preserved the moved tile/group's focus at the boundary; reloading preserved the layout, and Reset restored the default. Card actions did not receive focus or run while editing.
- Mouse drag at 900px and touch drag at 500px moved the output tile ahead of the orbit. A poll between touch-down and the first movement kept the handle connected; the new data appeared on release, with the drop saved. A press without a drag also released the hold.
- With a delayed Week request followed by Month, the earlier response did not replace the Month chart. Keyboard focus stayed on Month; the previous period remained selected until the new data arrived. Pending operations disabled card actions; starting/busy environments disabled mutations, an unavailable restore point disabled Restore, and Privacy was read-only.

The isolated fixture render is in `output/playwright/e68/layout-edit-900-follow-up.png`.

## UI follow-up (not a functional blocker)

At a 900px content pane the constellation labels are necessarily smaller than the surrounding cards; a later visual pass can refine node-label typography/spacing. Additional dense real projects may benefit from a richer overflow-node drill-down. The current star preserves the selected E-68 ring hierarchy, representative visible nodes and truthful full counts.

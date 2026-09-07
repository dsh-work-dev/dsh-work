# Desktop Pet

## Scope and status

dsh-work owns the Desktop Pet as a Host-managed desktop surface. It is rendered
outside the Workspace WebView and is independent of the DSH Workspace window.
This document records the current as-built behavior. It is not a claim that
every platform or release acceptance case has been manually signed off.

The implemented path is Windows-first. Automated checks pass; native platform,
packaging, visual and accessibility checks still require manual evidence before
the feature can be described as release-complete.

## Product behavior

- The pet is a frameless, always-on-top, transparent native window. Native
  border resizing is disabled.
- Pet discovery, selection, visibility and preview are exposed from the Pets
  section of trusted Settings UI. The default state has no selected pet and is
  hidden.
- A selected pet can be shown or hidden without changing the selected asset.
  An unavailable selected key is retained and reported as unavailable rather
  than silently replaced.
- Size is adjusted directly with a Settings slider from 50% to 300% in 5%
  steps. The visible scale marks are 50%, 75%, 100%, 125%, 150%, 200%, 250%
  and 300%. The canonical size is 192x208 pixels at 100%.
- The pet surface receives pointer input so that hover state can be detected.
  A drag handle appears on hover; pressing and holding that handle moves the
  native window. The pet body is not a drag target. Focus styling exists, but
  keyboard reachability and operation remain pending accessibility verification.
- Position is persisted as a monitor-relative normalized anchor together with
  the window dimensions and scale. The active monitor is retained when it is
  still available, with a safe fallback when it is not.
- The Host owns visibility, selection, size, position and preview state. The
  frontend renders projections of that state and does not gain process,
  filesystem or native-window authority.

## Asset and security boundary

The catalog accepts Codex v1/v2 packages from the `pets/` entry and the
`avatars/` compatibility entry, plus dsh-native raster/track packages.
Package adapters normalize those inputs into the renderer contract; they do
not expose arbitrary package code to the Host or WebView.

Untrusted pet packages are treated as data. Discovery and loading enforce the
configured package-root boundary, supported file types, bounded dimensions and
payload sizes, and digest/quarantine checks. Preview and animation failures
are isolated to the affected asset and fall back to a safe empty/error state.

## Implementation boundary

| Area | As-built responsibility |
|---|---|
| `internal/pet` | Catalog, package adapters, cache, runtime state, frame rendering and overlay projection |
| `internal/settings` | Versioned pet preference and persisted monitor-relative position |
| `internal/app/pet_settings_service.go` | Trusted Settings API for pet discovery, selection, visibility, preview and size |
| `main.go` | Native Pet window options, transparency, always-on-top behavior and resize policy |
| `frontend/src/pets.ts` | Pets Settings projection, scale slider commit and error state |
| `frontend/src/pet_overlay.ts` | Pet surface polling, reduced-motion rendering and hover drag affordance |
| `frontend/public/style.css` | Transparent surface, no-drag pet body and handle-only drag region |

The native window is created and controlled by the Host. Generated bindings are
allowlisted Host capabilities. The Pet UI is a consumer of those bindings, not
an alternative lifecycle or persistence authority.

## Persistence contract

Pet preferences are stored separately from DSH data. The persisted preference
contains the selected catalog key, visibility intent and a position record with
monitor identity, normalized anchor, width, height and scale. Invalid or
unsupported persisted values are rejected or normalized at the Host boundary;
the UI does not interpret raw persisted data.

Changing size updates the persisted dimensions and asks the Host to resize the
native window. The Host keeps the preference and native window update
transactional: if the native resize fails, the previous persisted dimensions
are restored and the error is returned to the caller.

## Platform and integration limits

- Windows has the implemented transparent overlay path and is the reference
  platform for the current implementation.
- macOS and Linux remain capability-gated/fallback paths until their native
  transparency, window ownership and packaging evidence is collected.
- The current repository has no DSH task-event producer. Lifecycle/display
  plumbing and reduced-motion handling exist, but end-to-end task-driven pet
  reactions are not documented as complete.
- Manual visual, input, accessibility and packaging verification has not yet
  been collected. In particular, keyboard/focus behavior and the
  maintained-icon treatment of the drag affordance still need a release audit.

## Verification evidence

The implementation has passed:

- `go test . ./cmd/... ./internal/...`
- `go vet ./...`
- `go build -trimpath -buildvcs=false -o bin/dsh-work.exe .`
- `npm run typecheck`
- `npm test` (6/6)
- `npm run build`
- `node scripts/check-public-content.mjs`
- `node scripts/check-wails-dev.mjs`
- `git diff --check`

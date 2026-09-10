# Desktop Pet

## Scope and status

dsh-work owns the Desktop Pet as a Host-managed native surface outside the
DSH Workspace WebView. This document records the implementation through commit
`f06ed37`, including the preceding activity and presentation changes.

Current stage: implemented and automatically verified; native release acceptance
remains open. Windows is the reference platform. Browser verification and a
successful desktop build do not establish native input, stacking, DPI,
accessibility or packaging acceptance.

## Product behavior

- The pet uses a frameless transparent window. Always-on-top is optional,
  persisted and off by default. Native border resizing is disabled.
- Trusted Pets Settings owns discovery, selection, preview, visibility, size
  and topmost preferences. Initially no pet is selected and the surface is
  hidden. An unavailable selected key is retained and reported as unavailable.
- The size slider ranges from 50% to 300% in 5% steps. The canonical sprite
  size is 96x104 pixels at 100%. Window sizing reserves 64 pixels for activity
  and uses a minimum width of 240 pixels; text does not scale with the sprite.
- A compact two-line speech bubble shows the prioritized conversation title
  and activity. Activity changes reveal it for eight seconds; hover can reveal
  it again. Returning to idle dismisses it. Clicking opens the owning DSH
  conversation. There is no conversation dropdown in the pet surface.
- A hover drag handle moves the window. The body responds to clicks with
  available pet actions. Weighted idle variations and click actions restore
  the current task state when finished. Reduced motion suppresses animation
  variation while activity text continues updating; hidden playback stops.
- Windows pointer sampling uses frontend-reported body, bubble and handle
  regions. Body bounds are derived from rendered alpha and accumulated across
  frames to avoid jitter. Transparent margins pass input through; this is a
  bounding-region policy, not per-pixel hit testing. A stale hit map restores
  ordinary input after two seconds.
- Position is persisted as a monitor-relative normalized anchor and sprite
  dimensions/scale. The active monitor is retained when available, with a safe
  fallback otherwise. Keyboard activation exists for the pet body; complete
  keyboard/focus and native drag behavior still require accessibility review.

## DSH activity integration

`internal/dshactivity` mounts an embedded plugin through the Worker's launch
patch. It uses DSH events and projections to publish bounded authenticated
activity snapshots; generation-scoped credentials and cancellation isolate
Worker replacements. The browser plugin handles conversation navigation and
focused read acknowledgement.

The Host aggregates sessions, pending questions/approvals/plan review, terminal
outcomes, goals, queued/steering messages and background jobs. The compact
bubble presents the prioritized activity and links to its conversation;
answering, approving and composing remain in that conversation.

Running work distinguishes thinking, tool execution and result organization.
Concurrent tools are tracked by call identity; one tool result does not finish
another tool's work, and a recoverable tool error does not mark the turn failed.
Completion, cancellation, failure, blocked, token limit and interruption retain
distinct meanings. Assets without dedicated animations use declared fallbacks.

## Asset and playback boundary

The catalog supports:

- Codex v1/v2 sprite packages from `pets/` and the `avatars/` compatibility entry,
  including supported look directions.
- Native `dsh-pet.json` raster/track packages.
- Community `config.jsonc` plus `webm/` packages installed under
  `$DSH_HOME/pets/<id>`; without `DSH_HOME`, the root is `~/.dsh/pets`.

Community compatibility targets the installed layout used by dsh-tauri-pet.
The upstream dsh-pet plugin's separate `dsh-pet/pet/<kind>-config.json` layout,
movement physics and mirroring are outside the implemented adapter. Package
acquisition/download is outside this feature.

Adapters normalize packages as data. Loading enforces root boundaries, safe
resource names, bounded dimensions/payloads and digest/quarantine checks.
Tailscale hujson parses JSONC; ebml-go reads bounded WebM metadata. WebM accepts
VP8/VP9 video with validated dimensions and duration, and rejects audio tracks.
WebView performs actual decoding and reports playback failures locally.

The Host owns the playback timeline and selected resources. The frontend fetches
a normalized plan on selection or visibility reload, then plays raster frames
locally on Canvas. WebM uses two video buffers for transitions. Regular state
polling no longer transfers a rendered PNG for every animation frame.
Selection-scoped opaque media URLs support HTTP Range requests and are revoked
on selection changes. Stale asynchronous responses cannot restore an old pet.

## Implementation boundary

| Area | As-built responsibility |
|---|---|
| `internal/pet` | Catalog, community home resolution, package adapters, cache, runtime timelines and normalized playback plans |
| `internal/settings` | Versioned pet preferences and monitor-relative position |
| `internal/app/pet_*.go` | Trusted Settings/pet APIs, activity projection, media capabilities and hit-region validation |
| `internal/dshactivity` | DSH launch plugin, authenticated activity bridge and navigation |
| `internal/nativeui/pet_*.go` | Window options, activity sizing and platform-specific pointer sampling |
| `main.go` | Service/window assembly and lifetime wiring |
| `frontend/src/pets.ts` | Settings controls and raster/video previews |
| `frontend/src/pet_overlay.ts` | State synchronization, gestures and hit-region reporting |
| `frontend/src/pet_player.ts` | Local Canvas/video playback, visibility cleanup and alpha bounds |
| `frontend/src/pet_activity.ts` | Prioritized conversation bubble and navigation |

The native pointer adapter receives a hit-test callback; it does not depend on
the application service. Generated bindings expose allowlisted Host operations.
The pet frontend does not gain filesystem, process or native-window authority.

## Persistence contract

Pet preferences are separate from DSH data and contain selection, visibility,
`alwaysOnTop` and monitor-relative position/dimensions/scale. Schema 3 uses the
96x104 baseline; migration from earlier schemas preserves slider percentage,
anchor, DPI and topmost intent. Missing legacy topmost values default false.
Invalid values are rejected or normalized at the Host boundary.

Resize and topmost changes coordinate native updates and persistence with
rollback on failure. The frontend consumes the validated state rather than raw
persisted data.

## Verification and remaining acceptance

Recorded implementation checks passed:

- Full `go test ./...` and `go vet ./...`.
- Frontend typecheck, 14 existing tests and production build.
- Installed DSH integration fixtures for activity, pending interactions,
  navigation, generation isolation, overlapping tools and recoverable errors.
- Browser checks for bubble behavior and sprite containment at 50/100/200%.
- Playwright with a mocked Wails bridge for local frame advance, manifest
  fetch frequency, click delivery, phase text, hide/show, alpha bounds and
  actual VP9 WebM decoding; no page errors in that playback check.
- Desktop executable build and `git diff --check`.

Native Windows pass-through/recovery, multi-monitor DPI, OS stacking, real-pet
visual quality, keyboard/focus and packaging still need interactive acceptance.
macOS and Linux remain capability-gated/fallback paths pending native evidence.
RED structural checking could not run because the CLI is unavailable.

# DSH notification boundary

Status: accepted design boundary.

DSH owns contextual notices rendered inside its Web UI. dsh-work owns desktop
delivery, user preferences and native notification routing. The current
implementation therefore routes dsh-work lifecycle failures through the dsh-work
notification policy and keeps the DSH event bridge behind a separate seam.

The bridge may be enabled only after the selected DSH runtime exposes a stable,
versioned event contract containing event identity, source class and a verified
focus target. Until then dsh-work does not inspect DSH DOM/CSS, parse process output
as user events, inspect gateway WebSocket frames or override browser APIs.

The detailed source record is kept in the local-only `.research/` directory.

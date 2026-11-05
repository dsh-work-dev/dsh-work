# Manage browser and computer components

Settings → 浏览器 and Settings → 电脑 list directly managed distributions,
installed versions, the versions supported by this application, and explicit
安装 / 更新 / 修复 actions. 详情 shows the owned location and companion packages.
The browser itself continues to use its own updater.

## Installation and engine selection are separate

Opening Settings checks release metadata, cached for one hour. 检查更新 forces
another check. Startup, enabling automation, and selecting an engine only inspect
existing installations. Artifact downloads require an explicit component action.

The computer engine preference selects Cua Driver or AUV independently of
installation. Preparing another engine never changes the selection. Both use
the authorized Windows surface described in [Computer control](computer-control.md).
Cua supports indexed controls and background text/key input; AUV supports
screenshots, coordinate clicks and scrolling on Windows.
Preparing dependencies does not start an engine process; it starts only for an
authorized computer-tool lifetime.

AUV preparation uses official `@auv-js/sdk@0.0.43` and
`@auv-js/cli-win32-x64-msvc@0.0.43`, including `bin/auv.exe`, under the application's
owned component store. Its exact npm lock and integrity records travel with the
application. The Windows CLI requires Node >=20.19.0. Ordinary local execution
uses an app-owned private named pipe with launcher-token authentication. The
optional Windows Helper is a separate elevated setup operation and is not
installed here. The authoritative contracts are the
[v0.0.43 SDK launcher](https://github.com/moeru-ai/auv/blob/v0.0.43/js/packages/sdk/src/node/daemon.ts)
and [Windows installation instructions](https://github.com/moeru-ai/auv/blob/v0.0.43/README.md#windows).
The SDK and CLI are upgraded together because v0.0.41 changed RPC names; that
release also fixed Windows per-monitor DPI awareness.

## Updates remain compatible and recoverable

Only the frozen combination shipped with this application is installable.
Upstream releases newer than that combination are shown as pending compatibility.
npm's locked installer, the existing acquisition layer, and Cua archive digest
and publisher checks remain the installation trust boundaries. The mature AUV
SDK/CLI distributions are reused instead of a custom transport or input layer.

Package trees are staged and validated before their current pointer changes.
A failed repair preserves the previous published tree and reports its failure.
Cua binaries are verified in staging before replacing the directory; the old
complete directory is retained as `.driver-previous-*`, with rollback if publication
fails. Browser-extension publication retains its own previous-directory backup.
Maintenance is refused while affected automation resources, pending browser
permissions or execution permits, and computer desktop leases/cleanup, are active. Stop those tasks before updating.

The extension row distinguishes files prepared on disk from the selected
browser's live hello. Versions include the local source revision, such as
`0.2.0 · r14`. After preparing a new revision, use Reload on the browser's
extension-management page, then reconnect. The existing binding and extension
storage are retained.

Inventory reads installed npm manifests, the Cua executable's `--version`, and
the extension's disk identity/live hello. Execution still requires the current
locked publication; read-only inventory can show an older installed publication.
Engine process status describes only processes owned by DSH Work, not external
services started by another application.

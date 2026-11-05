# Set up browser control

Use Settings → 浏览器 to prepare browser control before asking an agent to use
it. The current Windows implementation supports Google Chrome and Microsoft
Edge through the DSH Work extension loaded from an unpacked directory.

## Enable and install

1. Start the DSH Work profile with a working Run context. If Node is missing,
   configure it in Settings → 运行时 first.
2. In Settings → 浏览器, enable 浏览器操作 and 使用默认浏览器. In 组件,
   click 安装 for Playwright MCP. Enabling the capability and starting DSH Work
   only inspect existing components; installation requires this explicit action.
3. Click 安装 on the DSH Work extension component, then 打开扩展文件夹. In the browser profile you want to use, open
   `chrome://extensions` or `edge://extensions`, enable developer mode, and
   choose **Load unpacked**. Select the supplied directory containing
   `manifest.json`. Keep that directory in place for updates.
4. Open the DSH Work extension and choose **Connect to DSH Work**. Compare its
   short code with the pending binding in Settings, then confirm there.
5. Explicitly select that binding under 浏览器和个人资料. Wait for the selected
   binding to show connected and the components to show prepared. Resolve any
   registration, compatibility or reload warning before starting a browser task.

First-time component preparation needs access to the npm registry. It uses the
validated Node/npm and child environment of the selected Run context. A valid
matching component set is reused offline; installing the extension alone does
not provide Node or the browser components.

If browser policy prevents developer-mode loading, ask the administrator for
an approved installation method. This build supplies neither a store install
link nor a signed CRX package. Other Chromium-based browsers require their own
native-host adapter and are not currently supported. Native-host registration
from an MSIX package context reports unsupported; use an ordinary unpackaged
Windows installation or development launch.

## Bind each profile once

The extension supplies a persistent random instance identity. You do not enter
a browser profile name, directory or connection key to bind it. The generated
display label identifies the binding, not the browser's actual profile name;
you can rename it in Settings.

Install and bind separately in each profile you want to use, then select one.
An offline selection never falls back to another connected profile. Clearing
extension storage or uninstalling the extension can remove its instance
identity and require a new binding.

启动资料目录 is optional. After connecting and selecting a binding, 选择目录
can associate a local profile directory for reopening it later. This is a
launch hint, not the identity used for binding. Without it, open the selected
profile yourself and reconnect its extension. DSH Work does not change the
system default browser or its startup preferences.

## Allow a conversation to use the browser

Ask a conversation to open `about:blank` for a simple first check. With the
default policy, DSH Work opens its own independent permission window before
the conversation can use the browser. It names the conversation and selected
profile. Choose 允许 or 拒绝; closing this permission window also rejects the
request. 始终允许 changes the browser-access preference, which can be changed
again in Settings → 浏览器.

Website and risk confirmations use the same independent window. Website rules,
operation categories and permission duration are configured in Settings. Password
and one-time-code input stays manual. Website rules gate tool access; they are
not a network firewall, and a redirect can issue a request before its result is
denied.

Each top-level conversation owns its tabs in a separate DSH Work group; child
agents share that conversation's resources. Browser 停止 releases the
conversation's automation and ungroups its tabs, keeping the browser and tabs
open. Disabling browser control stops browser use across conversations.

## Follow browser actions

The Agent uses a solid whale-tail pointer with a white outline and blue glow.
A new or untouched page has no pointer. At the first real target, the pointer
appears near the upper-left corner and follows a short curved path to it.
Explicit clicks wait about 150–350 ms for arrival before reaching the page.

Each document keeps its last pointer position when the Agent switches tabs;
the next action continues from that position. Reading snapshots, joining the
conversation's tab group or switching tabs yourself does not create or move a
pointer. Navigation resets the affected page's pointer. Keyboard input can
highlight its actual target without waiting for animation; untargeted keyboard
scrolling does not reveal a pointer. Reduced-motion preferences use immediate
positioning.

On the Agent's current tab, the website icon keeps its original image with an
activity badge when that image can be read. The badge animates during execution
and stays still while waiting for permission. Switching the Agent to another
tab restores the previous tab's website icon while leaving its pointer still.
Stop or loss of control removes all indicators and restores the websites'
current icons. An unreadable icon is left unchanged.

Click synchronization is skipped when the renderer is initially unavailable or
a cross-origin frame cannot be inspected. Once synchronization starts, a changed
target, navigation or cancellation interrupts the click rather than sending it
to an old location. Input-target highlighting inside frames is currently omitted.

## Repair the step that failed

- **Node/npm unavailable:** use 打开运行时设置, configure or repair Node, then retry
  preparation. No Workspace-level `npm install` is needed.
- **Download or verification failed:** restore network access and retry the
  component step. Existing bindings are retained.
- **Components need reinstalling:** choose 修复 on the affected component, including when the
  page already says prepared. Stop browser use and resolve pending requests
  first. A failed repair keeps the old set usable only if it still passes
  validation, and reports the repair failure separately.
- **Native host registration failed or the application moved:** use 准备连接.
  Registration only updates objects owned by this application. An ownership
  conflict, permission error or unsupported package context must be resolved
  before a connection can work.
- **Selected profile offline:** open that profile, enable its extension and
  reconnect. An absent connection alone cannot distinguish a closed browser
  from a disabled extension.
- **Binding code expired:** start another connection/binding request and compare
  the new code.
- **Duplicate identity:** close all duplicate connections for that identity,
  then reconnect only the intended profile. The remaining first connection is
  not automatically trusted.
- **Reload or compatibility warning:** follow the update steps below. Reconnect
  alone does not reload extension code.

## Update without losing the binding

1. Stop active conversation browser use and finish or reject pending requests.
2. Update DSH Work. The native host is part of the same executable; after an
   installation-path change, use 准备连接 to repair its registration.
3. In Settings → 浏览器, use 准备连接 to prepare the supplied extension directory.
   Keep the existing loaded directory rather than uninstalling the extension.
4. On the browser's extension-management page, click the DSH Work card's
   **Reload** button (the circular arrow). This is distinct from **Reconnect**
   inside the extension popup.
5. Open the extension popup and reconnect. The original binding remains when
   the extension identity and storage are unchanged. Wait for the reload
   warning to disappear before asking a conversation to use the browser.

Component versions follow the application's frozen lock, not an independent
`npm update`. Checking release metadata never downloads or installs components.
A new component set is staged and
validated before its selection pointer changes. Failed publication leaves the
previous selection intact. The extension directory is published as a complete
set with a previous-directory backup; the handshake checks the code actually
loaded in the browser, not just files on disk. User-initiated maintenance is
refused while browser resources, pending confirmations or execution permits
are active.

The browser itself uses its own updater. Component repair does not downgrade
the browser or restore permissions. See [Version recovery](version-recovery.md#browser-components-and-extension-compatibility)
for the scope of version records.

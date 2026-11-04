# Verifying the running app without taking over the desktop

Agents and scripts check dsh-work's UI by talking to the app, not by moving the
user's mouse or reading the screen. Screen-driving tools fight the user for
focus. They also cannot see the workbench: it uses WebView2 composition
hosting, which screen-capture and click tools treat as an opaque region.

## How it works

| Surface | Channel | What it gives |
|---|---|---|
| Workbench shell and DSH page | WebView2 DevTools Protocol (CDP) on a loopback port | script evaluation, synthetic input, page screenshots, console |
| Daemon and runtime | `dsh-work-cli` over the daemon pipe | runtime install, Run context switch, status |
| Notifications | Windows toast history (WinRT) | what was sent, with its payload |
| Daemon WebViews (Pet and browser permission window) | development CDP on the next port | isolated page state and rendered pixels |

**CDP.** Development builds (`!production`) open WebView2's remote-debugging
port when `DSH_WORK_WEBVIEW_DEBUG_PORT` is set
(`internal/desktopapp/debugport_dev.go`); release builds never do. `GET
http://127.0.0.1:<port>/json/list` lists pages, and each has a WebSocket
for CDP commands. Input sent with `Input.dispatchMouseEvent` and
`Input.insertText` goes into the page's event queue, not the OS. The real
cursor, keyboard and foreground window stay with the user. This works while
the window is hidden or covered. `Page.captureScreenshot` renders the page
itself, so it also works for a hidden window.

The workbench page holds two execution contexts:

- **Shell** (`wails.localhost:9245` in dev): the trusted top bar and Settings.
  It can import generated bindings, for example
  `import('/bindings/github.com/local/dsh-work/internal/desktopclient/index.ts')`,
  and call `SettingsService.SetAppearance`, `ShellService.SetPetVisible` and
  similar. Calls carry the window's surface, as they do for real UI.
- **DSH frame** (origin `wails.localhost:48217`): a same-process iframe. Pick
  its context by origin. Its coordinates start 40 px below the window top,
  under the shell bar.

The shell sends DSH commands to the frame the same way the menu does:
`frame.contentWindow.postMessage({version: 1, type: 'dsh-work/command', id:
'session.new'}, origin)`. Other shell-to-page messages
(`dsh-work/answer-approval`, `dsh-work/open-session`) can be sent the same way
to test the page side of a flow on its own.

`scripts/dev-cdp.mjs` wraps this: `eval`, `click`, `type` and `shot`.

## Recipe

1. Start the dev loop from an ordinary terminal with the port set:

   ```powershell
   $env:DSH_WORK_WEBVIEW_DEBUG_PORT = '9333'; wails3 task dev
   ```

   Do not start it from inside another app's MSIX package context, such as the
   Codex desktop app. That process gets that package's private copy of
   `HKCU\Software\Classes` and of AppData. Notification registration lands
   there, so Windows keeps notifications in history without showing them.
2. Read state with `eval`, and act with `click`, `type` or shell bindings.
   Prefer locating elements by role, `aria-label` or text over fixed
   coordinates.
3. Check results in the page (DOM, selected row, composer text), in the
   daemon log (`%LOCALAPPDATA%\dsh-work\logs\daemon.log`), and for
   notifications in `ToastNotificationManager.History.GetHistory('dsh-work-dev')`.
4. For theme work, loop `SetAppearance` over every theme × mode, open the
   surface, compute text contrast on the composited background, and take clipped
   `Page.captureScreenshot`s for a contact sheet. A translucent menu draws its
   fill on an `aria-hidden` material layer next to the content, not on an
   ancestor, so include it when compositing.
5. Restore what you changed: appearance, Pet visibility, Run context, drafts,
   test files.

The daemon's development WebViews use the next port: setting
`DSH_WORK_WEBVIEW_DEBUG_PORT=9333` gives the workbench port 9333 and daemon port
9334. For the independent browser permission window, set `DSH_WORK_CDP_PORT`
to `9334` when running `scripts/dev-cdp.mjs`; the script skips Pet targets.
The permission window is created only for an actual pending request. Capture
it while visible, before answering: a hidden idle window may not produce a
screenshot. Its bindings permit only reading and answering that permission
panel, not general settings operations. Never answer Allow on the user's behalf
without explicit authorization.

## Check browser action feedback

Use an isolated browser profile and local fixture through the real MCP tools.
Confirm that initial navigation, reads and a user-opened tab in the same group
leave an untouched document's pointer hidden. On the first real target, capture
the reveal at the (24,24) CSS-pixel hotspot and the curved movement toward the
target. After operating two documents, switch away and back: each should retain
its own stationary pointer position. Navigation should reset only that document;
release should remove both.

For a distant click, capture intermediate pointer frames and record the page's
mousedown time: the pointer should have arrived before input, with a pulse
following dispatch. Test cancellation during movement and confirm that the
page receives no click. Reading snapshots should not wait for animation.
Check normal and reduced-motion settings, route isolation, and restoration of
the site's latest favicon after release. Direct CDP mouse events alone do not
establish the MCP click behavior.

The development watcher observes embedded JavaScript. Generated Worker assets
are ignored by an absolute-path-compatible glob, and the generator only writes
changed content so a build cannot repeatedly trigger itself.

## Limits

- The daemon debug port is development-only, as is the workbench's. Pet
  bindings answer only the Pet surface; browser-permission bindings answer
  only their own surface.
- Toast banners are outside every WebView. Clicking a banner button needs the
  user or UI Automation while the banner is on screen. To test only the page
  half, send the same `dsh-work/answer-approval` message into the frame.
- Screen capture fails while the session is locked. Ask the user to unlock.
- A conversation prompt reaches the configured model and costs money. Use a
  short prompt in a test conversation, or a local mock provider for repeated
  runs.
- When the checkout carries a Low integrity label (set by Codex's Windows
  sandbox), `bin\dsh-work.exe` cannot open AppData. Run a copy from outside the
  checkout with `FRONTEND_DEVSERVER_URL=http://localhost:9245`. Rebuild
  `dsh-work-cli` before using it; an old build may not match the daemon's
  protocol.

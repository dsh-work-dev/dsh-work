# Browser Lab：隔离浏览器回归与准入测试

先运行不需要 GUI 的检查，三平台使用同一入口：

```sh
npm ci --prefix tools/browser-lab --ignore-scripts
npm test --prefix tools/browser-lab
node tools/browser-lab/cli.mjs list
```

这层验证用例目录、结果门禁、人工证据约束和真实本地 HTTP fixture；**不代表三平台原生浏览器已通过**。

## Windows 原生自动回归

在普通、未打包的 PowerShell 和已解锁的交互桌面运行。CEF、MSVC、Windows SDK、Go 和 Node 使用机器已配置的工具链；脚本不安装或迁移工具缓存。

首次从固定 CEF standard SDK 构建：

```powershell
& tools/browser-lab/native/windows/build.ps1 -CefSdk '<已解压的 CEF SDK 根目录>'
```

固定候选为 `156.0.15+gcf671c0+chromium-156.0.8078.80` Windows x64。构建脚本复制 SDK 到 `.work/` 后叠加 `native/windows/cef-overlay`，保留官方 bootstrap/client DLL 和 renderer sandbox。不会改 SDK 原目录。

已有使用该 overlay 编译的 CEF bundle 时可复用：

```powershell
& tools/browser-lab/native/windows/build.ps1 -CefBundle '<含 cefsimple.exe/dll、libcef.dll 的 Release 目录>'
npm run --prefix tools/browser-lab test:native
```

构建输出在 `.work/bundle`。实验 composition 候选默认 `-Composition bottom`：只在生成的 Wails 副本将 `CreateTargetForHwnd(hwnd, true)` 改为 `false`。`-Composition stock` 可复现原版遮挡；正向 native composition 用例应因此失败。此构建选项不代表项目已接受 fork。

`test:native` 显式使用 **scripted fixture consent**：驱动可信 Wails 授权面板的按钮，调用真实 Go Service，而非跳过 permit。只允许当前测试会话、固定测试标题及本地 fixture 站点；非测试请求拒绝。它不构成真人同意或真实键盘／IME 证据。需要手动回答授权时：

```powershell
node tools/browser-lab/cli.mjs run --profile regression --consent manual
```

外网 HTTPS 是单独开关，默认不会依赖公共网站：

```powershell
node tools/browser-lab/cli.mjs run --profile regression --consent scripted --network
```

`--network` 使用正常 TLS 的 example.com 与错误证书负例。公网站点或网络失败会记录真实错误，不忽略证书、不代理改写页面。

只补测指定用例时加 `--case sandbox.renderer,authorization.generation`；其他用例明确记录 `not-selected`。该报告是定向运行，不会变成全套资格通过；保留之前的全量报告和补测报告作为两份证据。

## 看报告，而不是只看命令变绿

每次运行有独立 run ID，输出到 `results/<run-id>/`：

| 文件 | 用途 |
| --- | --- |
| `report.json` | case ID、执行状态、环境、候选版本、证据、资格判定与清理结果 |
| `junit.xml` | Node 原生 JUnit reporter，供 CI 展示 |
| `evidence/` | 已知 demo HWND 的原生截图、有限日志、进程沙箱与回收证据 |
| `manual-checklist.json` | 本次 run 的真实 IME／混合 DPI 人工记录模板 |

状态包括 `pass`、`fail`、`known-gap`、`blocked`、`manual-required`、`unimplemented`、`not-selected`。已知缺口必须**实际重现对应行为**才标 `known-gap`；其他错误仍失败。声明的缺口不再出现时也失败，要求审查并更新基线，不静默 XPASS。

- `regression`：已测得的已知缺口、人工项和未实现能力可保留，但 report 的 `qualification` 仍是 `not-ready`。失败、缺必要环境、空运行或全跳过均非零退出。
- `qualification`：任何已知缺口、未实现、缺环境或缺人工证据都会非零退出。目前该模式预期不能通过，直到相应能力真正完成。

```powershell
npm run --prefix tools/browser-lab qualify
```

32 个稳定 requirement ID 在 `catalog.mjs`，28 个可执行场景在 `cases/native.mjs`。2 个尚未实现的要求为下载授权生命周期及站点权限原生管理；2 个人工要求为真实原生输入／IME和实际混合 DPI。人工记录须包含同一 run ID、操作者、时间与具体观察；用 `--run-id` 和 `--manual-evidence` 提供，不能拿上次报告或 CDP 填字替代真人输入。

在 Windows 交互终端先收集真实输入证据，再对同一候选构建运行自动测试：

```powershell
node tools/browser-lab/cli.mjs manual --operator '<操作者>' --run-id 'physical-01' --output tools/browser-lab/results/physical-01
node tools/browser-lab/cli.mjs run --run-id 'physical-01' --profile qualification --consent scripted --network --manual-evidence tools/browser-lab/results/physical-01/manual-evidence.json --output tools/browser-lab/results/physical-01-auto
```

人工入口记录实际 composition 事件、原生焦点、显示器/DPI采样和操作者对候选框的观察，随后自动回收。两次使用同一 bundle，期间不要重编译；报告同时校验构建身份。有一块显示器时混合 DPI 保持 `blocked`。自动 CI 不调用这个交互入口。

## 哪些地方与平台有关

```
catalog.mjs / cases/native.mjs     跨平台要求与逻辑
lib/context.mjs / lib/report.mjs   实际授权调用、报告与门禁
platforms/index.mjs                平台能力登记；缺实现返回 blocked
platforms/windows.mjs              Windows 启停、状态归一化、WGC、沙箱探针
platforms/windows/                单 owner Playwright adapter 与固定观察端点
native/windows/                   Wails/Win32 lab host、bootstrap overlay、WGC helper
native/gateway/                   复用项目真实 automation.Service 的隔离网关
fixtures/                        固定 HTML/JS/CSS/SVG、弹窗、无害文本下载
```

公共场景按逻辑 view ID、document identity、host 状态断言，不按 HWND 写业务测试。Windows 适配器把 HWND 归一化为 view ID，只有本平台层查询 Win32／WGC。未来 macOS/Linux 适配器需提供同样的 `state / command / capture / sandboxAssessment / recover` 与固定 backend 契约，声明真实能力；不能只复用 Windows 的成功结果。

完整32项的 macOS/Linux 适配器尚未实现，`run` 入口仍会明确 blocked。另有独立 `smoke` 原生最小闭环，用来先证明这两个平台能实际同窗与同页操作，不替代全套资格验收。

## Linux/X11 与 macOS 原生最小闭环

在对应平台运行（Linux需GTK4/WebKitGTK 6、X11桌面；CI安装命令见workflow）：

```sh
node tools/browser-lab/native/posix/build.mjs
node tools/browser-lab/cli.mjs smoke
```

构建下载固定CEF standard SDK、编译官方wrapper、窄原生bridge与隔离Wails host。Linux使用独立CEF broker、自持消息循环并嵌入原生X11 child；macOS使用同进程NSView及CEF外部消息泵。当前Go binding未证明覆盖固定CEF156、macOS事件协议及沙箱/helper生命周期，因此直接复用官方C++ wrapper，仅桥接必要的初始化、parent、状态和关闭。没有改Wails模块缓存或产品实现。

macOS实验构建仅为Wails的`StatusItemController`加精确编译期命名前缀，避免与CEF的ObjC类冲突；此选项不改CEF或模块缓存。Ubuntu24.04 runner为本次host配置单程序AppArmor userns许可，让WebKit能创建其沙箱；全局限制和浏览器沙箱保留，job结束移除该许可。CEF156 SDK配置deployment target为macOS13，当前Mac CI只验证15，不能据此承诺项目原有macOS12支持。

`smoke` 实际检查Wails与CEF parent关系、原生窗口截图同时包含shell/guest标记、同一document真实输入/点击，以及OnBeforeClose/CefShutdown和进程组回收。只对隔离fixture使用特权CDP，不声称已验证Go Service授权。其余要求报告`not-selected`，qualification保持not-ready。Linux Xvfb只覆盖X11；Wayland需独立compositor验证，真实IME/物理混合DPI保留人工项。

## CI 分层

`.github/workflows/browser-lab.yml`：

1. push/PR：Windows、macOS、Ubuntu 的 contract/fixture matrix，不启动 GUI。
   另有 `native-posix-smoke` job，在Ubuntu 24.04的Xvfb/Openbox与macOS 15上构建并运行真实原生最小闭环，上传JSON/JUnit/原生图/回收证据；不会把纯CDP页面截图当同窗通过。
2. 手动触发 native：仅 main，使用 `[self-hosted, Windows, X64, browser-lab]` 的已准备 runner。runner 要在普通、已解锁的交互用户会话运行，不能是 session 0 服务；需配置 `BROWSER_LAB_CEF_BUNDLE`、原生 MSVC/SDK。native job 串行占用固定 19478–19484 端口。
3. native regression 与 strict qualification 名称分开；不会把 hosted contract matrix 当成三平台原生验收。

原生Windows self-hosted runner需另行准备；不要让它执行不可信PR代码。Linux/macOS最小闭环使用GitHub托管runner。

## 隔离、回收和证据含义

- 每次运行在新 TEMP 子目录复制执行文件，使用专用 CEF/WebView2 profile、fixture 文件和真实 Service scratch settings。端口已占用时停止，不终止占用者。
- 单个 Playwright owner 操作和观察 CEF；Wails 是独立 CDP surface。显示、操作 permit、presentation/document/generation 和故障撤销分别验证。
- renderer 沙箱结果来自实际 token 探针，不把 SDK 全进程都宣称为 AppContainer。保留沙箱、TLS及站点安全机制。
- native composition 截图必须同时含 shell/guest 的固定颜色 landmark，防止“DOM 输入成功但 native guest 被遮住”的假通过。图片仍保留供人工判读；该像素检查不是完整视觉或辅助功能验收。
- scripted consent 只用于独立测试 profile。Go Service/permit是真实逻辑，CEF admission 仍为 lab metadata fixture；CDP 是实验特权传输，非正式产品安全边界。
- popup 与自动下载的现有缺口有稳定 ID。恢复 native download default 后的取消，不冒充批准保存/路径/活跃下载撤销已经实现；权限 query 不读取真实敏感数据。
- 正常结束及失败均尝试 gateway→CEF/host→backend→fixture 回收。必要的强制清理仅针对登记过的进程／私有 staging 范围，并使门禁失败；不删除用户数据或重新定位缓存。
- `private-session.json`、`session.json`、token、profile 与 crash dump 留在私有 TEMP run root，**不上传**。CI 只上传工作流列出的公共报告、日志和 scoped native PNG。

原始 R78 研究记录仍在本机 RED Research；本套件源码、锁文件与 CI 可随 Git 分享，不依赖研究目录或本机绝对路径。

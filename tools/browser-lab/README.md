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

目录 v2 有 34 个稳定 requirement ID：32 个自动场景与 2 个人工场景。自动项覆盖原有 Windows 场景，并单列窗口缩放、原生焦点及定向 ASCII 事件；权限与下载授权生命周期会实际请求，再报告当前能力缺口，不再静态跳过。原生键盘合成只在本次 GitHub runner 桌面执行，不代表物理键盘或 IME。人工记录须包含同一 run ID、操作者、时间与具体观察；用 `--run-id` 和 `--manual-evidence` 提供，不能拿上次报告或 CDP 填字替代真人输入。

本机运行时，新增的合成原生键盘项标为 `not-selected`，其余原有回归照常执行；该项会在 GitHub 的独占测试桌面实跑。真实键盘／中文 IME 仍走人工入口。

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
platforms/shared/                 三平台单 owner CEF backend 与固定观察端点
platforms/posix.mjs               Linux/macOS 全套 adapter、可信 Wails UI fixture
native/windows/                   Wails/Win32 lab host、bootstrap overlay、WGC helper
native/gateway/                   复用项目真实 automation.Service 的隔离网关
fixtures/                        固定 HTML/JS/CSS/SVG、弹窗、无害文本下载
```

公共场景按逻辑 view ID、document identity、host 状态断言，不按 HWND 写业务测试。Windows 适配器把 HWND 归一化为 view ID，只有本平台层查询 Win32／WGC。macOS/Linux 适配器提供同样的 `state / command / capture / sandboxAssessment / recover` 与固定 backend 契约，声明真实能力；不能只复用 Windows 的成功结果。

三平台共用 `run` 全套入口和真实 Go Service 授权/permit 逻辑。POSIX 的可信 Wails 面板由测试专用 ExecJS 回传通道操作；该通道不授予外页宿主能力。单 owner CEF backend 负责页面操作与取证。`smoke` 仍保留为定向诊断入口，只运行最小闭环，报告其余要求 `not-selected`。

## Linux/X11 与 macOS 全套原生回归

在对应平台运行（Linux需GTK4/WebKitGTK 6、X11桌面；CI安装命令见workflow）：

```sh
node tools/browser-lab/native/posix/build.mjs
node tools/browser-lab/cli.mjs run --profile regression --consent scripted --network
```

构建下载固定CEF standard SDK、编译官方wrapper、窄原生bridge与隔离Wails host。Linux使用独立CEF broker、自持消息循环并嵌入原生X11 child；macOS使用同进程NSView及CEF外部消息泵。当前Go binding未证明覆盖固定CEF156、macOS事件协议及沙箱/helper生命周期，因此直接复用官方C++ wrapper，仅桥接必要的初始化、parent、状态和关闭。没有改Wails模块缓存或产品实现。

macOS实验构建仅为Wails的`StatusItemController`加精确编译期命名前缀，避免与CEF的ObjC类冲突；此选项不改CEF或模块缓存。Ubuntu24.04 runner为本次host配置单程序AppArmor userns许可，让WebKit能创建其沙箱；全局限制和浏览器沙箱保留，job结束移除该许可。CEF156 SDK配置deployment target为macOS13，当前Mac CI只验证15，不能据此承诺项目原有macOS12支持。

Mac正常退出先关闭两个CEF view，停止外部泵与Cocoa run loop，待Wails原生run及其autorelease pool返回后，在原主线程执行CefShutdown并记录实际返回，最后退出进程。原来在仍运行的Cocoa循环中直接teardown曾出现Chromium watchdog code2；报告要求正常exit0，不把watchdog退出当正常持久化。watchdog、沙箱及TLS保持默认。

完整入口执行 HTML/localhost/HTTPS、双标签保活、浮动/停靠/缩放、当前屏 DPI、原生 ASCII 焦点路由、真实授权与精确目标绑定、导航/切页/撤销/遮挡防误操作、故障恢复、外页能力与 profile/文件 root 隔离，以及弹窗、下载和权限探测。macOS 候选的 CEF 与 Wails 同进程：真实 broker 故障会连宿主退出，测试记录该隔离缺口，随后重建整个隔离宿主继续；全宿主重启的持久化不能冒充“保留 Wails 的 CEF 冷重启”。Linux Xvfb 只覆盖 X11，未覆盖 Wayland。真实中文 IME 候选框和物理混合 DPI 保留人工项。实际各平台通过/失败以对应运行的 report.json 为准。

## CI 分层

`.github/workflows/browser-lab.yml`：

1. push/PR：Windows、macOS、Ubuntu 的 contract/fixture matrix，不启动 GUI。
   原生 full jobs 在 Windows 2025、Ubuntu 24.04/Xvfb/Openbox 与 macOS 15 上构建并运行完整自动目录，上传 JSON/JUnit、原生窗口图和回收证据。Windows 保留原交互桌面与未打包检查；hosted 环境不满足时报告实际阻断，不绕过或用页面图代替原生同窗图。
2. 手动触发 native：仅 main，使用 `[self-hosted, Windows, X64, browser-lab]` 的已准备 runner。runner 要在普通、已解锁的交互用户会话运行，不能是 session 0 服务；需配置 `BROWSER_LAB_CEF_BUNDLE`、原生 MSVC/SDK。native job 串行占用固定 19478–19484 端口。
3. native regression 与 strict qualification 名称分开；不会把 hosted contract matrix 当成三平台原生验收。

准备好的 Windows self-hosted runner 仍可补充托管环境无法验证的场景；不要让它执行不可信 PR 代码。托管原生作业使用隔离 runner，不接管用户本机桌面。

## 隔离、回收和证据含义

- 每次运行在新 TEMP 子目录复制执行文件，使用专用 CEF/WebView2 profile、fixture 文件和真实 Service scratch settings。端口已占用时停止，不终止占用者。
- 单个 Playwright owner 操作和观察 CEF；Windows Wails 使用独立 CDP surface；POSIX 使用可信面板 ExecJS 通道。显示、操作 permit、presentation/document/generation 和故障撤销分别验证。
- renderer 沙箱逐平台取证：Windows restricted token/Untrusted、Linux renderer 的 no_new_privs/seccomp/capabilities、macOS Seatbelt 对隔离 canary 的策略查询与 broker 对照。探针范围写入证据，不扩称所有进程和所有资源都已覆盖。保留沙箱、TLS及站点安全机制。
- native composition 截图必须同时含 shell/guest 的固定颜色 landmark，防止“DOM 输入成功但 native guest 被遮住”的假通过。图片仍保留供人工判读；该像素检查不是完整视觉或辅助功能验收。
- scripted consent 只用于独立测试 profile。Go Service/permit是真实逻辑，CEF admission 仍为 lab metadata fixture；CDP 是实验特权传输，非正式产品安全边界。
- popup 与自动下载的现有缺口有稳定 ID。恢复 native download default 后的取消，不冒充批准保存/路径/活跃下载撤销已经实现；权限 query 不读取真实敏感数据。
- 正常结束及失败均尝试 gateway→CEF/host→backend→fixture 回收。必要的强制清理仅针对登记过的进程／私有 staging 范围，并使门禁失败；不删除用户数据或重新定位缓存。
- `private-session.json`、`session.json`、token、profile 与 crash dump 留在私有 TEMP run root，**不上传**。CI 只上传工作流列出的公共报告、日志和 scoped native PNG。

原始 R78 研究记录仍在本机 RED Research；本套件源码、锁文件与 CI 可随 Git 分享，不依赖研究目录或本机绝对路径。

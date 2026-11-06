// Stable IDs describe product requirements, not a particular OS or script.
const c = (id, title, capability, extra = {}) => ({id, title, capability, ...extra});
export const catalogVersion = 2;
export const cases = [
 c('native.composition', '宿主与 live guest 同窗可见', 'nativeCapture'),
 c('sandbox.renderer', '实际 renderer token 沙箱', 'sandboxProbe'),
 c('content.html', 'HTML 相对 CSS/JS/图片及同页操作', 'browser'),
 c('content.localhost', 'localhost 同页输入与点击', 'browser'),
 c('content.https', '默认 TLS 的 HTTPS 读取与错误证书拒绝', 'network', {optional: true}),
 c('tabs.retention', '两个真实标签隐藏后保活', 'nativeViews'),
 c('views.float-dock', '同一 guest 浮动与停靠', 'nativeViews'),
 c('views.resize', '改变原生边界后页面保活且 viewport 跟随', 'nativeViews'),
 c('input.native-routing', '原生焦点及定向 ASCII 事件路由', 'nativeInput'),
 c('dpi.current', '当前屏 host/guest DPI 一致', 'nativeViews'),
 c('authorization.display-only', '显示不自动授予操作', 'gateway'),
 c('authorization.exact-once', 'permit 精确请求绑定及单次消耗', 'gateway'),
 c('authorization.secret', '密码字段不允许代理输入', 'gateway'),
 c('identity.navigation', '同 URL 新 document 拒绝旧目标', 'gateway'),
 c('identity.tab-aba', 'A→B→A 拒绝旧观察', 'gateway'),
 c('authorization.revoke', '撤销排队动作且保留显示', 'gateway'),
 c('authorization.generation', 'worker generation 使旧授权失效', 'gateway'),
 c('overlay.presentation', '遮挡禁控、恢复后重新观察', 'overlay'),
 c('overlay.queued', 'inspect 后遮挡阻止最终 dispatch', 'overlay'),
 c('recovery.renderer', '单页崩溃隔离与显式重新加载', 'faultInjection'),
 c('recovery.broker', 'broker 退出隔离与显式重启', 'faultInjection', {platformGaps:{darwin:'R78-macos-shared-broker'}}),
 c('isolation.guest', 'guest 无宿主能力且控制面拒绝未认证请求', 'browser'),
 c('isolation.artifact-root', 'HTML root 阻止目录及 junction 逃逸', 'artifactRoot'),
 c('profile.same-origin', '同 origin CEF 与宿主存储隔离', 'hostStorage'),
 c('profile.persistence', '正常重启保留 persistent profile', 'profileRestart', {platformGaps:{darwin:'R78-macos-shared-broker'}}),
 c('popup.window-open', 'window.open 进入受控页面生命周期', 'popup', {knownGap: 'R78-popup-window-open'}),
 c('popup.target-blank', '_blank 不产生未托管顶层窗口', 'popup', {knownGap: 'R78-popup-unmanaged'}),
 c('download.attached-default', '附加自动化不绕过下载目的地授权', 'download', {knownGap: 'R78-download-auto-save'}),
 c('download.native-default', '恢复 Alloy 默认后观察取消', 'download'),
 c('permissions.defaults', '敏感权限默认未获授予（仅 query）', 'permissions'),
 c('permissions.lifecycle', '站点权限允许/拒绝/撤销的原生流程', 'permissions', {knownGap:'R78-permission-lifecycle'}),
 c('download.authorization', '下载目的地/路径/取消撤销/自动打开契约', 'download', {knownGap:'R78-download-authorization'}),
 c('input.native-ime', '真实键盘焦点与中文 IME 组合提交', 'physicalInput', {manual: true}),
 c('dpi.mixed-monitors', '真实不同缩放显示器之间移动', 'mixedDPI', {manual: true}),
];
export const knownGaps = {
 'R78-permission-lifecycle':'真实本地通知权限请求未形成可允许、拒绝和撤销的宿主许可流程。',
 'R78-download-authorization':'真实原生默认下载取消；缺少批准目的地和活动下载撤销流程。',
 'R78-macos-shared-broker':'当前Mac CEF与Wails同进程，broker故障/冷重启无法保留Wails；必须实际终止及重启后记录该缺口。',
 'R78-popup-window-open': 'CEF 样例产生未初始化 popup，点击 ACK 超时；需托管或明确拒绝。',
 'R78-popup-unmanaged': 'CEF 默认新窗口不在 Wails tab/admission 中。',
 'R78-download-auto-save': 'Playwright 1.58.2 附加默认 allowAndName；未形成原生下载许可。',
};

export function validateCatalog(items = cases) {
 const ids = new Set();
 for (const item of items) {
  if (!/^[a-z][a-z0-9.-]+$/.test(item.id) || ids.has(item.id)) throw Error('Invalid/duplicate case ID: ' + item.id);
  if (!item.title || !item.capability) throw Error('Incomplete case: ' + item.id);
  if (item.knownGap && !knownGaps[item.knownGap]) throw Error('Unregistered known gap: ' + item.id);
  ids.add(item.id);
 }
 return items;
}

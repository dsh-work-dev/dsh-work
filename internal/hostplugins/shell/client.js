// The dsh-work shell menu runs a fixed set of DSH commands. DSH exposes no
// public command invoke to plugins, so a command runs by dispatching its
// current binding through DSH's own keyboard path. A binding the menu cannot
// press (a conflict, an invalid or two-key binding, or settings still loading)
// is reported unavailable rather than pressed into the wrong command.
const SHELL_COMMANDS = ['session.new', 'session.search', 'workspace.add', 'terminal.new', 'browser.new', 'sidebar.left.toggle', 'sidebar.right.toggle', 'settings.open', 'shortcuts.open'];

// Window keys the shell owns while DSH has focus, unless DSH binds the same chord.
function windowKeyAction(event) {
  if (event.altKey || event.metaKey) return undefined;
  if (!event.ctrlKey) return event.code === 'F11' && !event.shiftKey ? 'fullscreen' : undefined;
  if (event.code === 'Equal' || event.code === 'NumpadAdd') return 'zoom-in';
  if (event.shiftKey) return undefined;
  if (event.code === 'Minus' || event.code === 'NumpadSubtract') return 'zoom-out';
  if (event.code === 'Digit0' || event.code === 'Numpad0') return 'zoom-reset';
  return undefined;
}

function keyForCode(code) {
  if (code.startsWith('Key')) return code.slice(3).toLowerCase();
  if (code.startsWith('Digit')) return code.slice(5);
  return code;
}

function connectShell(ctx) {
  const shellOrigin = window.parent && window.parent !== window ? window.location?.ancestorOrigins?.[0] : undefined;
  if (!shellOrigin) return;
  ctx.inject(['shortcuts', 'uiWorkspace', 'layout'], ctx => {
    const {catalog, config, fixedCatalog} = ctx.shortcuts;
    const uiWorkspace = ctx.uiWorkspace;
    const layout = ctx.layout;
    const post = message => window.parent.postMessage({version: 1, ...message}, shellOrigin);
    const entries = () => new Map(catalog.getSnapshot().map(entry => [entry.id, entry]));
    const ready = () => (config?.getSnapshot().status ?? 'ready') === 'ready';
    const hasPublicService = id => id === 'session.new'
      ? typeof uiWorkspace?.startSession === 'function'
      : id === 'sidebar.left.toggle' && typeof layout?.toggleSidebar === 'function';
    const shortcutState = entry => {
      if (!entry.binding) return 'unbound';
      if (!ready() || entry.issue || entry.conflicts?.length || entry.binding.secondCode !== undefined) return 'unavailable';
      return 'ready';
    };
    const commandState = (id, entry) => {
      if (id === 'session.new' || id === 'sidebar.left.toggle') return hasPublicService(id) ? 'ready' : 'unavailable';
      return shortcutState(entry);
    };
    const report = () => {
      const current = entries();
      post({type: 'dsh-work/catalog', commands: SHELL_COMMANDS.flatMap(id => {
        const entry = current.get(id);
        const direct = id === 'session.new' || id === 'sidebar.left.toggle';
        if (!entry && !direct) return [];
        const canPress = !!entry?.binding && ready() && !entry.issue && !entry.conflicts?.length && entry.binding.secondCode === undefined;
        return [{id, keys: canPress ? [...entry.keys] : [], state: commandState(id, entry)}];
      })});
    };
    ctx.effect(() => catalog.subscribe(report));
    if (config) ctx.effect(() => config.subscribe(report));
    report();

    let lastSidebarState;
    const root = document.getElementById('root') ?? document.documentElement;
    const reportSidebar = () => {
      const frame = root?.querySelector?.('[style*="grid-template-columns"]');
      if (!frame) return;
      const open = !frame.hasAttribute('data-sidebar-collapsed');
      if (open === lastSidebarState) return;
      lastSidebarState = open;
      post({type: 'dsh-work/sidebar-state', open});
    };
    ctx.effect(() => {
      const observer = new MutationObserver(reportSidebar);
      observer.observe(root, {attributes: true, attributeFilter: ['data-sidebar-collapsed', 'style'], childList: true, subtree: true});
      reportSidebar();
      return () => observer.disconnect();
    });

    const onMessage = event => {
      if (event.source !== window.parent || event.origin !== shellOrigin) return;
      const data = event.data;
      if (data?.type !== 'dsh-work/command' || !SHELL_COMMANDS.includes(data.id)) return;
      let handled = false;
      try {
        if (data.id === 'session.new') {
          if (typeof uiWorkspace?.startSession === 'function') {
            uiWorkspace.startSession();
            handled = true;
          }
        } else if (data.id === 'sidebar.left.toggle') {
          if (typeof layout?.toggleSidebar === 'function') {
            layout.toggleSidebar();
            handled = true;
          }
        } else {
          const entry = entries().get(data.id);
          if (entry && shortcutState(entry) === 'ready') {
            const modifiers = new Set(entry.binding.modifiers);
            const press = new KeyboardEvent('keydown', {
              code: entry.binding.code, key: keyForCode(entry.binding.code), bubbles: true, cancelable: true,
              ctrlKey: modifiers.has('control'), altKey: modifiers.has('alt'), shiftKey: modifiers.has('shift'), metaKey: modifiers.has('meta'),
            });
            // DSH prevents default on input it consumes, whether it runs the command or declines it.
            handled = !document.body.dispatchEvent(press);
          }
        }
      } catch {
        handled = false;
      }
      post({type: 'dsh-work/command-result', id: data.id, handled});
    };
    const sameChord = (binding, event) => binding?.code === event.code && binding.secondCode === undefined &&
      ['control', 'alt', 'shift', 'meta'].every(modifier => binding.modifiers.includes(modifier) === !!event[modifier === 'control' ? 'ctrlKey' : `${modifier}Key`]);
    const boundInDSH = event => catalog.getSnapshot().some(entry => sameChord(entry.binding, event)) ||
      (fixedCatalog?.getSnapshot() ?? []).some(entry => entry.bindings.some(binding => sameChord(binding, event)));
    // Alt pressed alone or F10 opens the shell menu bar, as in a native window.
    let altAlone = false;
    const keydown = event => {
      if (event.key === 'F10' && !event.ctrlKey && !event.altKey && !event.shiftKey && !event.metaKey) {
        event.preventDefault();
        post({type: 'dsh-work/menu-key', key: 'F10'});
        altAlone = false;
        return;
      }
      const action = windowKeyAction(event);
      if (action && !event.defaultPrevented && !boundInDSH(event)) {
        event.preventDefault();
        post({type: 'dsh-work/window-key', action});
      }
      altAlone = event.key === 'Alt' && !event.repeat;
    };
    const keyup = event => {
      if (event.key === 'Alt' && altAlone) post({type: 'dsh-work/menu-key', key: 'Alt'});
      altAlone = false;
    };
    window.addEventListener('message', onMessage);
    post({type: 'dsh-work/component-ready', id: 'shell'});
    window.addEventListener('keydown', keydown, true);
    window.addEventListener('keyup', keyup, true);
    ctx.effect(() => () => {
      window.removeEventListener('message', onMessage);
      window.removeEventListener('keydown', keydown, true);
      window.removeEventListener('keyup', keyup, true);
    });
  });
}
// Other origins open in the system browser. DSH itself lives only in the shell
// frame: same-origin new windows would be unmanaged DSH pages.
const recentlyOpenedExternal = new Map();
function openExternal(raw) {
  const url = new URL(raw, location.href);
  if (!['http:', 'https:'].includes(url.protocol)) return;
  const now = Date.now();
  recentlyOpenedExternal.forEach((openedAt, href) => {
    if (now - openedAt > 3000) recentlyOpenedExternal.delete(href);
  });
  if (now - (recentlyOpenedExternal.get(url.href) ?? 0) < 3000) return;
  recentlyOpenedExternal.set(url.href, now);
  const workerFetch = globalThis.WorkerBridge?.workerFetch;
  if (typeof workerFetch !== 'function') return;
  void workerFetch('/__work/external?url=' + encodeURIComponent(url.href), {method: 'POST'})
    .then(response => { if (!response.ok) console.warn('Could not open the external browser.'); })
    .catch(() => console.warn('Could not open the external browser.'));
}
function isNewWindowTarget(target) {
  return !!target && !['_self', '_parent', '_top'].includes(target.toLowerCase());
}
function installLinkPolicy() {
  if (!document?.addEventListener || !window) return;
  document.addEventListener('click', event => {
    const anchor = event.target?.closest?.('a[href]');
    if (!anchor) return;
    const url = new URL(anchor.href, location.href);
    if (url.origin !== location.origin) {
      event.preventDefault();
      openExternal(url.href);
    } else if (isNewWindowTarget(anchor.target)) {
      event.preventDefault();
      console.warn('dsh-work does not open DSH in a new window.');
    }
  }, true);
  window.open = ((raw, target) => {
    const url = new URL(raw ?? '', location.href);
    if (url.origin !== location.origin) {
      openExternal(url.href);
      return null;
    }
    if (target === undefined || isNewWindowTarget(target)) {
      console.warn('dsh-work does not open DSH in a new window.');
      return null;
    }
    location.assign(url.href);
    return window;
  });
}
function installSurfaceReporter() {
  const shellOrigin = window.parent !== window ? window.location?.ancestorOrigins?.[0] : undefined;
  if (!shellOrigin || !document?.documentElement || !document?.createElement) return;
  let last = '', pending = 0;
  const resolve = (token, fallback) => {
    const probe = document.createElement('span');
    probe.style.cssText = 'position:absolute;visibility:hidden;color:var(' + token + ',' + fallback + ')';
    document.body.append(probe);
    const value = getComputedStyle(probe).color;
    probe.remove();
    return value;
  };
  const report = () => {
    pending = 0;
    if (!document.body) return;
    const style = getComputedStyle(document.body);
    const surface = {
      type: 'dsh-work/surface',
      background: resolve('--dsw-specific-sidebar-fill', style.backgroundColor),
      color: style.color,
      scheme: getComputedStyle(document.documentElement).colorScheme
    };
    const key = JSON.stringify(surface);
    if (key !== last) {
      last = key;
      window.parent.postMessage(surface, shellOrigin);
    }
  };
  const schedule = () => { if (!pending) pending = requestAnimationFrame(report); };
  new MutationObserver(schedule).observe(document.documentElement, {
    attributes: true, subtree: true, attributeFilter: ['class', 'style', 'data-theme']
  });
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', schedule);
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', schedule); else schedule();
}
window.__ModuleLoader__.load({id: '@dsh-work/shell', factory: () => ({
  inject: ['shortcuts'],
  apply(ctx) {
    connectShell(ctx);
    installLinkPolicy();
    installSurfaceReporter();
  }
})});

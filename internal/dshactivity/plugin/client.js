// DSH's desktop account UI starts sign-in and waits; the desktop shell opens the
// authorization page. The URL exists only after the Host reaches waiting-browser,
// so follow the official account/watch stream, as DSH's own desktop shells do.
// window.open is routed by the worker bridge to the system browser.
function openAccountSignIn(ctx) {
  if (!('dshDesktop' in globalThis)) return;
  ctx.inject(['remote', 'remote.account'], ctx => {
    const remote = ctx.remote, account = remote?.account;
    if (typeof account?.watch !== 'function' || typeof remote.$stream !== 'function') return;
    const stream = remote.$stream({name: 'dsh-work-account-browser', open: signal => account.watch(signal), ended: () => new Error('account stream ended')});
    ctx.effect(() => () => stream.dispose());
    // An attempt already waiting when the page connected was opened earlier.
    let connected = false;
    const seen = new Set();
    (async () => {
      for await (const frame of stream) {
        const attempt = frame.value?.attempt;
        frame.accept?.();
        const first = !connected;
        connected = true;
        if (attempt?.phase !== 'waiting-browser' || typeof attempt.id !== 'string' || seen.has(attempt.id)) continue;
        seen.add(attempt.id);
        if (first || typeof attempt.authorizeUrl !== 'string' || !attempt.authorizeUrl.startsWith('https://')) continue;
        window.open(attempt.authorizeUrl, '_blank', 'noopener,noreferrer');
      }
    })().catch(() => {});
  });
}

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
  ctx.inject(['shortcuts'], ctx => {
    const {catalog, config, fixedCatalog} = ctx.shortcuts;
    const post = message => window.parent.postMessage({version: 1, ...message}, shellOrigin);
    const entries = () => new Map(catalog.getSnapshot().map(entry => [entry.id, entry]));
    const ready = () => (config?.getSnapshot().status ?? 'ready') === 'ready';
    const state = entry => {
      if (!entry.binding) return 'unbound';
      if (!ready() || entry.issue || entry.conflicts?.length || entry.binding.secondCode !== undefined) return 'unavailable';
      return 'ready';
    };
    const report = () => {
      const current = entries();
      post({type: 'dsh-work/catalog', commands: SHELL_COMMANDS.flatMap(id => {
        const entry = current.get(id);
        return entry ? [{id, keys: entry.binding ? [...entry.keys] : [], state: state(entry)}] : [];
      })});
    };
    ctx.effect(() => catalog.subscribe(report));
    if (config) ctx.effect(() => config.subscribe(report));
    report();
    const onMessage = event => {
      if (event.source !== window.parent || event.origin !== shellOrigin) return;
      const data = event.data;
      if (data?.type !== 'dsh-work/command' || !SHELL_COMMANDS.includes(data.id)) return;
      const entry = entries().get(data.id);
      let handled = false;
      if (entry && state(entry) === 'ready') {
        const modifiers = new Set(entry.binding.modifiers);
        const press = new KeyboardEvent('keydown', {
          code: entry.binding.code, key: keyForCode(entry.binding.code), bubbles: true, cancelable: true,
          ctrlKey: modifiers.has('control'), altKey: modifiers.has('alt'), shiftKey: modifiers.has('shift'), metaKey: modifiers.has('meta'),
        });
        // DSH prevents default on input it consumes, whether it runs the command or declines it.
        handled = !document.body.dispatchEvent(press);
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
    window.addEventListener('keydown', keydown, true);
    window.addEventListener('keyup', keyup, true);
    ctx.effect(() => () => {
      window.removeEventListener('message', onMessage);
      window.removeEventListener('keydown', keydown, true);
      window.removeEventListener('keyup', keyup, true);
    });
  });
}

window.__ModuleLoader__.load({id: '@dsh-work/pet-activity', factory: () => ({
  inject: ['sessions', 'uiSession'],
  apply(ctx) {
    openAccountSignIn(ctx);
    connectShell(ctx);
    let stopped = false, timer, ack = '', error = '';
    const abort = new AbortController();
    async function sync() {
      try {
        const list = ctx.sessions.list.getSnapshot();
        const response = await fetch('/__dshwork/activity-client', {method: 'POST', credentials: 'same-origin',
          headers: {'Content-Type': 'application/json'}, signal: AbortSignal.any([abort.signal, AbortSignal.timeout(5000)]),
          body: JSON.stringify({current: list.current, visible: document.visibilityState === 'visible' && document.hasFocus(),
            sessions: Object.values(list.byId ?? {}).slice(0, 256).map(s => ({id: s.id, title: s.displayTitle, parentId: s.parentId})), ack, error})});
        if (!response.ok) throw new Error('Activity connection unavailable');
        const {navigation} = await response.json();
        if (navigation && navigation.id !== ack) {
          error = '';
          try {
            await ctx.sessions.refresh();
            if (navigation.parentSessionId) {
              await ctx.sessions.refreshSubagents(navigation.parentSessionId);
              const address = ctx.sessions.subagentAddress(navigation.sessionId);
              if (!address) throw new Error('Conversation is unavailable');
              ctx.sessions.openSubagent(address);
            } else ctx.sessions.open(navigation.sessionId);
          } catch { error = 'Conversation is unavailable'; }
          ack = navigation.id;
        }
      } catch { /* The Host shows disconnected/expired navigation state; retry while mounted. */ }
      finally { if (!stopped) timer = setTimeout(sync, 500); }
    }
    void sync();
    ctx.effect(() => () => { stopped = true; abort.abort(); clearTimeout(timer); });
  }
})});

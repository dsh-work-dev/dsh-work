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

window.__ModuleLoader__.load({id: '@dsh-work/pet-activity', factory: () => ({
  inject: ['sessions', 'uiSession'],
  apply(ctx) {
    openAccountSignIn(ctx);
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

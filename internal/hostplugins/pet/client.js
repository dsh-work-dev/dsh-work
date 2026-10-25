function shellOrigin() {
  return window.parent && window.parent !== window ? window.location?.ancestorOrigins?.[0] : undefined;
}

function reportComponentReady(id) {
  const origin = shellOrigin();
  if (origin) window.parent.postMessage({version: 1, type: 'dsh-work/component-ready', id}, origin);
}

window.__ModuleLoader__.load({id: '@dsh-work/pet', factory: () => ({
  inject: ['sessions', 'uiSession'],
  apply(ctx, clock = globalThis) {
    reportComponentReady('pet');
    let stopped = false, debounceTimer, retryTimer, inFlight = false, pending = false, retryAttempts = 0, revision = 0;
    const abort = new AbortController();
    const snapshot = () => {
      const list = ctx.sessions.list.getSnapshot();
      return {sessions: Object.values(list.byId ?? {}).slice(0, 256).map(s => ({id: s.id, title: s.displayTitle, parentId: s.parentId}))};
    };
    async function sync() {
      if (stopped || inFlight || !pending) return;
      inFlight = true;
      const requestRevision = revision;
      try {
        const response = await fetch('/__dshwork/pet-activity-client', {method: 'POST', credentials: 'same-origin',
          headers: {'Content-Type': 'application/json'}, signal: AbortSignal.any([abort.signal, AbortSignal.timeout(5000)]),
          body: JSON.stringify(snapshot())});
        if (!response.ok) throw new Error('Activity connection unavailable');
        if (revision === requestRevision) pending = false;
        retryAttempts = 0;
      } catch {
        if (!stopped) {
          retryAttempts++;
          if (retryAttempts <= 3) {
            retryTimer = clock.setTimeout(() => {
              retryTimer = undefined;
              pending = true;
              void sync();
            }, 1000);
          } else if (revision === requestRevision) pending = false;
        }
      } finally {
        inFlight = false;
        if (pending && retryTimer === undefined && !stopped) void sync();
      }
    }
    function scheduleSync() {
      if (stopped) return;
      if (debounceTimer !== undefined) clock.clearTimeout(debounceTimer);
      if (retryTimer !== undefined) {
        clock.clearTimeout(retryTimer);
        retryTimer = undefined;
      }
      retryAttempts = 0;
      debounceTimer = clock.setTimeout(() => {
        debounceTimer = undefined;
        revision++;
        pending = true;
        void sync();
      }, 100);
    }
    const origin = shellOrigin();
    async function openSession(event) {
      if (!origin || event.source !== window.parent || event.origin !== origin) return;
      const message = event.data;
      if (!message || message.version !== 1 || message.type !== 'dsh-work/open-session' ||
          typeof message.id !== 'string' || message.id.length === 0 || message.id.length > 256 ||
          typeof message.sessionId !== 'string' || message.sessionId.length === 0 || message.sessionId.length > 256 ||
          typeof message.parentSessionId !== 'string' || message.parentSessionId.length > 256) return;
      let error = '';
      try {
        await ctx.sessions.refresh();
        if (message.parentSessionId) {
          await ctx.sessions.refreshSubagents(message.parentSessionId);
          const address = ctx.sessions.subagentAddress(message.sessionId);
          if (!address) throw new Error('Conversation is unavailable');
          await ctx.sessions.openSubagent(address);
        } else {
          await ctx.sessions.open(message.sessionId);
        }
      } catch { error = 'Conversation is unavailable'; }
      if (!stopped) window.parent.postMessage({version: 1, type: 'dsh-work/open-session-result', id: message.id, error}, origin);
    }
    const unsubscribeSessions = ctx.sessions.list.subscribe(scheduleSync);
    window.addEventListener('message', openSession);
    // Publish the initial session list once; subsequent requests follow session changes.
    scheduleSync();
    ctx.effect(() => () => {
      stopped = true;
      abort.abort();
      if (debounceTimer !== undefined) clock.clearTimeout(debounceTimer);
      if (retryTimer !== undefined) clock.clearTimeout(retryTimer);
      unsubscribeSessions?.();
      window.removeEventListener('message', openSession);
    });
  }
})});

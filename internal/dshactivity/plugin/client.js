window.__ModuleLoader__.load({id: '@dsh-work/pet-activity', factory: () => ({
  inject: ['sessions', 'uiSession'],
  apply(ctx) {
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

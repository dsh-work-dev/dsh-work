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
    const origin = shellOrigin();
    const instance = crypto.randomUUID();
    const interactionKinds = new Set(['approval', 'question', 'plan-review']);
    let stopped = false, debounceTimer, retryTimer, inFlight = false, pending = false, retryAttempts = 0, revision = 0;
    const abort = new AbortController();
    const interactionTimers = new Map();
    let observedInteractions = new Map();
    let interactionsReady = false;
    let lastCurrent = currentSession();
    let lastVisible = isVisible();
    function currentSession() {
      return Object.values(ctx.sessions.list.getSnapshot().byId ?? {}).find(s => (s.retainedBy?.mainView ?? 0) > 0)?.id ?? '';
    }
    function isVisible() { return document.visibilityState === 'visible'; }
    function reportInteraction(sessionId = '', interaction) {
      if (!origin || stopped) return;
      window.parent.postMessage({version: 1, type: 'dsh-work/interaction', instance, sessionId,
        kind: interaction?.kind ?? '', key: interaction?.key ?? '', toolName: interaction?.toolName ?? '',
        current: currentSession(), visible: isVisible()}, origin);
    }
    function readInteractions() {
      const interactions = new Map();
      for (const status of ctx.uiSession.sessionStatus.getSnapshot().values()) {
        const pendingInteraction = status?.pendingInteraction;
        if (!pendingInteraction || !interactionKinds.has(pendingInteraction.kind)) continue;
        interactions.set(pendingInteraction.sessionId, {
          kind: pendingInteraction.kind,
          key: pendingInteraction.key,
          toolName: pendingInteraction.kind === 'approval' ? pendingInteraction.toolName : '',
        });
      }
      return interactions;
    }
    function clearInteractionTimer(sessionId) {
      const timer = interactionTimers.get(sessionId);
      if (timer !== undefined) clock.clearTimeout(timer);
      interactionTimers.delete(sessionId);
    }
    function observeInteractions() {
      if (stopped) return;
      const next = readInteractions();
      if (!interactionsReady) {
        observedInteractions = next;
        return;
      }
      const sessionIds = new Set([...observedInteractions.keys(), ...next.keys()]);
      for (const sessionId of sessionIds) {
        const previous = observedInteractions.get(sessionId);
        const current = next.get(sessionId);
        if (previous && !current) {
          clearInteractionTimer(sessionId);
          reportInteraction(sessionId);
        } else if (current && (!previous || current.key !== previous.key)) {
          clearInteractionTimer(sessionId);
          const expectedKey = current.key;
          interactionTimers.set(sessionId, clock.setTimeout(() => {
            interactionTimers.delete(sessionId);
            if (stopped) return;
            const latest = readInteractions().get(sessionId);
            if (latest && latest.key === expectedKey) reportInteraction(sessionId, latest);
          }, 250));
        }
      }
      observedInteractions = next;
    }
    function reportContextChanges() {
      const current = currentSession();
      const visible = isVisible();
      if (current === lastCurrent && visible === lastVisible) return;
      lastCurrent = current;
      lastVisible = visible;
      reportInteraction();
    }
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
    function answerApproval(event) {
      if (!origin || event.source !== window.parent || event.origin !== origin) return;
      const message = event.data;
      if (!message || message.version !== 1 || message.type !== 'dsh-work/answer-approval' ||
          typeof message.instance !== 'string' || message.instance.length === 0 ||
          typeof message.sessionId !== 'string' || message.sessionId.length === 0 ||
          typeof message.key !== 'string' || message.key.length === 0 ||
          (message.outcome !== 'allowed-once' && message.outcome !== 'rejected') || message.instance !== instance) return;
      const pendingInteraction = ctx.uiSession.sessionStatus.getSnapshot().get(message.sessionId)?.pendingInteraction;
      if (!pendingInteraction || pendingInteraction.kind !== 'approval' || pendingInteraction.key !== message.key ||
          pendingInteraction.answerable !== true || typeof pendingInteraction.answer !== 'function') return;
      void Promise.resolve().then(() => pendingInteraction.answer(message.outcome)).catch(() => {});
    }
    const unsubscribeSessions = ctx.sessions.list.subscribe(() => {
      scheduleSync();
      reportContextChanges();
    });
    const unsubscribeSessionStatus = ctx.uiSession.sessionStatus.subscribe(observeInteractions);
    document.addEventListener('visibilitychange', reportContextChanges);
    window.addEventListener('message', openSession);
    window.addEventListener('message', answerApproval);
    // Publish the initial session list once; subsequent requests follow session changes.
    scheduleSync();
    // The first status snapshot is a baseline. Publish only page context until an interaction changes.
    observedInteractions = readInteractions();
    interactionsReady = true;
    reportInteraction();
    ctx.effect(() => () => {
      if (stopped) return;
      if (origin) window.parent.postMessage({version: 1, type: 'dsh-work/interaction-reset', instance}, origin);
      stopped = true;
      abort.abort();
      if (debounceTimer !== undefined) clock.clearTimeout(debounceTimer);
      if (retryTimer !== undefined) clock.clearTimeout(retryTimer);
      for (const timer of interactionTimers.values()) clock.clearTimeout(timer);
      interactionTimers.clear();
      unsubscribeSessions?.();
      unsubscribeSessionStatus?.();
      document.removeEventListener('visibilitychange', reportContextChanges);
      window.removeEventListener('message', openSession);
      window.removeEventListener('message', answerApproval);
    });
  }
})});

// The public sidebar slot owns only an icon; its surrounding native button
// normally selects a registered DSH main panel. This entry opens a Host surface
// instead, so capture that one button's activation before DSH selects a panel.
window.__ModuleLoader__.load({id: '@dsh-work/overview', factory: require => {
  const React = require('react');
  return {inject: ['slots', 'layout', 'sessions', 'locale'], apply(ctx) {
    const origin = window.parent !== window ? window.location?.ancestorOrigins?.[0] : undefined;
    if (!origin) return;
    const send = (type, extra = {}) => window.parent.postMessage({version: 1, type, ...extra}, origin);
    // pending/running come from DSH's own session status store; only offline comes from the Host.
    let badge = {pending: 0, running: false, offline: false}, open = false, button, column, resize;
    let reported = '';
    const listeners = new Set();
    const label = () => ({en: 'Overview', 'ja-JP': '概要'}[ctx.locale.getSnapshot?.()?.active] ?? '概览');
    const sidebarWidth = () => Math.max(0, Math.round(column?.getBoundingClientRect().right ?? 0));
    const geometry = () => send('dsh-work/overview-geometry', {sidebarWidth: sidebarWidth()});
    const close = () => { if (open) { open = false; refresh(); send('dsh-work/close-overview'); } };
    const refresh = () => {
      if (button) {
        const suffix = badge.pending ? ` (${badge.pending})` : badge.offline ? ' (!)' : '';
        button.setAttribute('aria-label', label() + suffix);
        if (open) button.setAttribute('aria-current', 'page'); else button.removeAttribute('aria-current');
        button.style.backgroundColor = open ? 'var(--dsw-alias-interactive-bg-hover)' : '';
      }
      for (const listener of listeners) listener();
    };
    const activate = event => {
      event.preventDefault(); event.stopImmediatePropagation();
      open = true; refresh(); send('dsh-work/open-overview', {sidebarWidth: sidebarWidth()});
    };
    // Public stores cover navigation changes. The local column capture also
    // handles reselecting an already-current session, which changes no store.
    const columnClick = event => { if (!button?.contains(event.target) && event.target.closest?.('button,a,[role="button"]')) close(); };
    const detach = () => {
      button?.removeEventListener('click', activate, true);
      column?.removeEventListener('click', columnClick, true);
      resize?.disconnect(); resize = undefined; button = column = undefined;
    };
    const bind = node => {
      detach();
      if (!node) return;
      button = node.closest('button');
      column = button?.closest('nav')?.parentElement;
      button?.addEventListener('click', activate, true);
      column?.addEventListener('click', columnClick, true);
      if (column) { resize = new ResizeObserver(geometry); resize.observe(column); }
      refresh(); geometry();
    };
    // The status store maps session id to {running, pendingInteraction, completionUnread}.
    const statusSelector = value => value;
    function StatusReporter({useSessionStatus}) {
      const statuses = useSessionStatus(statusSelector);
      React.useEffect(() => {
        let pending = 0, running = false;
        const entries = typeof statuses?.values === 'function' ? statuses.values() : Object.values(statuses ?? {});
        for (const entry of entries) {
          if (entry?.pendingInteraction !== null && entry?.pendingInteraction !== undefined && String(entry.pendingInteraction).trim() !== '') pending++;
          else if (entry?.running === true) running = true;
        }
        const key = `${pending}/${running}`;
        if (key === reported) return;
        reported = key;
        badge = {...badge, pending, running}; refresh();
        send('dsh-work/overview-activity', {pending, running});
      }, [statuses]);
      return null;
    }
    function Icon({size = 16, useSessionStatus}) {
      const state = React.useSyncExternalStore(listener => { listeners.add(listener); return () => listeners.delete(listener); }, () => badge);
      // Lucide LayoutDashboard v1.48.0, ISC; shape data from the app's pinned
      // lucide package (dist/esm/icons/layout-dashboard.mjs).
      return React.createElement('span', {ref: bind, style: {position: 'relative', display: 'inline-flex'}},
        typeof useSessionStatus === 'function' && React.createElement(StatusReporter, {useSessionStatus}),
        React.createElement('svg', {width: size, height: size, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: 1.75, strokeLinecap: 'round', strokeLinejoin: 'round', 'aria-hidden': true},
          ...[[7, 9, 3, 3], [7, 5, 14, 3], [7, 9, 14, 12], [7, 5, 3, 16]].map(([width, height, x, y], i) => React.createElement('rect', {key: i, width, height, x, y, rx: 1}))),
        (state.pending > 0 || state.offline || state.running) && React.createElement('span', {
          className: !state.pending && !state.offline && state.running ? 'dsh-work-overview-pulse' : undefined,
          style: {position: 'absolute', left: '65%', top: '-8px', minWidth: '10px', borderRadius: '8px', padding: state.pending || state.offline ? '0 3px' : '3px',
            fontSize: '10px', lineHeight: '14px', background: state.offline && !state.pending ? 'var(--dsw-alias-state-error-primary)' : 'var(--dsw-alias-label-primary)', color: 'var(--dsw-specific-sidebar-fill)'}
        }, state.pending > 0 ? String(Math.min(state.pending, 999)) : state.offline ? '!' : null));
    }
    const receive = event => {
      if (event.source !== window.parent || event.origin !== origin || event.data?.version !== 1) return;
      const d = event.data;
      if (d.type === 'dsh-work/overview-badge') {
        if (typeof d.offline !== 'boolean') return;
        badge = {...badge, offline: d.offline}; refresh();
      }
      if (d.type === 'dsh-work/overview-closed') { open = false; refresh(); }
      if (d.type === 'dsh-work/overview-opened') { open = true; refresh(); }
    };
    window.addEventListener('message', receive);
    // DSH forwards every root layout-store update through this subscription,
    // including viewport/column geometry. Only panel identity is navigation.
    let currentPanel = ctx.layout.panelInfo.getSnapshot().activePanelId;
    const unsubPanel = ctx.layout.panelInfo.subscribe(() => {
      const next = ctx.layout.panelInfo.getSnapshot().activePanelId;
      if (next !== currentPanel) { currentPanel = next; close(); }
    });
    let currentSession = selectedSession();
    function selectedSession() { return Object.values(ctx.sessions.list.getSnapshot().byId ?? {}).find(s => (s.retainedBy?.mainView ?? 0) > 0)?.id; }
    const unsubSessions = ctx.sessions.list.subscribe(() => { const next = selectedSession(); if (next !== currentSession) { currentSession = next; close(); } });
    const css = document.createElement('style');
    css.textContent = '@keyframes dsh-work-overview-pulse{50%{opacity:.35;transform:scale(.75)}}.dsh-work-overview-pulse{animation:dsh-work-overview-pulse 1.4s ease-in-out infinite}@media(prefers-reduced-motion:reduce){.dsh-work-overview-pulse{animation:none}}';
    document.head.append(css);
    ctx.slots.inject('sidebar.panellist', () => ctx.slots.register({name: 'sidebar.panellist', id: 'dsh-work-overview', order: -100, label}, Icon));
    ctx.effect(() => () => { detach(); window.removeEventListener('message', receive); unsubPanel?.(); unsubSessions?.(); css.remove(); listeners.clear(); });
    send('dsh-work/component-ready', {id: 'overview'});
  }};
}});

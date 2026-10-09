let feedbackElement;
let feedbackTimer;

function showSignOutFeedback(state) {
  const host = document.body ?? document.documentElement;
  if (!host) return;
  if (!feedbackElement || !feedbackElement.isConnected) {
    feedbackElement = document.createElement('div');
    feedbackElement.setAttribute('role', 'status');
    feedbackElement.setAttribute('aria-live', 'polite');
    Object.assign(feedbackElement.style, {
      position: 'fixed', top: '20px', right: '20px', zIndex: '2147483647',
      maxWidth: 'min(360px, calc(100vw - 40px))', padding: '12px 16px', borderRadius: '10px',
      background: 'rgba(24, 24, 27, 0.96)', color: '#fff', boxShadow: '0 8px 24px rgba(0, 0, 0, 0.22)',
      font: '500 14px/1.5 system-ui, sans-serif', pointerEvents: 'none', opacity: '0', transition: 'opacity 120ms ease',
    });
    host.appendChild(feedbackElement);
  }
  if (feedbackTimer !== undefined) window.clearTimeout(feedbackTimer);
  const isChinese = (document.documentElement.lang || navigator.language).toLowerCase().startsWith('zh');
  feedbackElement.setAttribute('role', state === 'error' ? 'alert' : 'status');
  feedbackElement.textContent = isChinese
    ? state === 'pending' ? '正在退出登录…' : state === 'success' ? '已退出登录' : '退出登录失败，请重试'
    : state === 'pending' ? 'Signing out…' : state === 'success' ? 'Signed out' : 'Could not sign out. Try again.';
  feedbackElement.style.opacity = '1';
  if (state !== 'pending') {
    feedbackTimer = window.setTimeout(() => { if (feedbackElement) feedbackElement.style.opacity = '0'; }, 3000);
  }
}

async function finishSignOutFeedback(value) {
  if (value instanceof Response) {
    if (!value.ok) {
      showSignOutFeedback('error');
      return;
    }
    try { value = await value.clone().json(); } catch {
      showSignOutFeedback('error');
      return;
    }
  }
  const succeeded = value?.type === 'server-response' && value.result?.ok === true;
  showSignOutFeedback(succeeded ? 'success' : 'error');
}

function reportComponentReady(id) {
  const shellOrigin = window.parent && window.parent !== window ? window.location?.ancestorOrigins?.[0] : undefined;
  if (shellOrigin) window.parent.postMessage({version: 1, type: 'dsh-work/component-ready', id}, shellOrigin);
}

// DSH's desktop account UI starts sign-in and waits; the desktop shell opens the
// authorization page. The URL exists only after the Host reaches waiting-browser,
// so follow the official account/watch stream, as DSH's own desktop shells do.
// window.open is routed by the worker bridge to the system browser.
function openAccountSignIn(ctx) {
  if (!('dshDesktop' in globalThis)) return;
  ctx.inject(['remote', 'remote.account'], ctx => {
    const remote = ctx.remote, account = remote?.account;
    const signOut = account?.signOut;
    if (typeof signOut === 'function') {
      const wrappedSignOut = async (...args) => {
        showSignOutFeedback('pending');
        try {
          const result = await signOut.apply(account, args);
          void finishSignOutFeedback(result);
          return result;
        } catch (error) {
          void finishSignOutFeedback(false);
          throw error;
        }
      };
      try {
        account.signOut = wrappedSignOut;
        ctx.effect(() => () => { if (account.signOut === wrappedSignOut) account.signOut = signOut; });
      } catch { /* The generated Remote facade may be immutable. */ }
    }
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

window.__ModuleLoader__.load({id: '@dsh-work/account', factory: () => ({
  inject: [],
  apply(ctx) {
    reportComponentReady('account');
    openAccountSignIn(ctx);
  }
})});

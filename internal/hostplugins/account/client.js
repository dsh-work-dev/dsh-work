// The shell shows sign-out progress in its own localized, themed status line.
function showSignOutFeedback(state) {
  const shellOrigin = window.parent && window.parent !== window ? window.location?.ancestorOrigins?.[0] : undefined;
  if (shellOrigin) window.parent.postMessage({version: 1, type: 'dsh-work/account-signout', state}, shellOrigin);
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
// @dsh-work/shell's link policy opens the window.open call in the system browser.
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

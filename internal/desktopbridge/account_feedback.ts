export type AccountSignOutEnvelope = {
  type?: unknown;
  result?: {ok?: unknown} | null;
};

let feedbackElement: HTMLDivElement | undefined;
let feedbackTimer: number | undefined;

function signedOutResponse(value: unknown): boolean {
  if (!value || typeof value !== 'object') return false;
  const envelope = value as AccountSignOutEnvelope;
  return envelope.type === 'server-response' && envelope.result?.ok === true;
}

function showAccountSignOutFeedback(state: 'pending' | 'success' | 'error'): void {
  const host = document.body ?? document.documentElement;
  if (!host) return;
  if (!feedbackElement || !feedbackElement.isConnected) {
    feedbackElement = document.createElement('div');
    feedbackElement.setAttribute('role', 'status');
    feedbackElement.setAttribute('aria-live', 'polite');
    Object.assign(feedbackElement.style, {
      position: 'fixed',
      top: '20px',
      right: '20px',
      zIndex: '2147483647',
      maxWidth: 'min(360px, calc(100vw - 40px))',
      padding: '12px 16px',
      borderRadius: '10px',
      background: 'rgba(24, 24, 27, 0.96)',
      color: '#fff',
      boxShadow: '0 8px 24px rgba(0, 0, 0, 0.22)',
      font: '500 14px/1.5 system-ui, sans-serif',
      pointerEvents: 'none',
      opacity: '0',
      transition: 'opacity 120ms ease',
    });
    host.appendChild(feedbackElement);
  }
  if (feedbackTimer !== undefined) window.clearTimeout(feedbackTimer);
  feedbackElement.setAttribute('role', state === 'error' ? 'alert' : 'status');
  feedbackElement.textContent = accountSignOutMessage(state);
  feedbackElement.style.opacity = '1';
  if (state !== 'pending') {
    feedbackTimer = window.setTimeout(() => {
      if (feedbackElement) feedbackElement.style.opacity = '0';
    }, 3000);
  }
}

function accountSignOutMessage(state: 'pending' | 'success' | 'error'): string {
  const isChinese = (document.documentElement.lang || navigator.language).toLowerCase().startsWith('zh');
  if (isChinese) {
    return state === 'pending' ? '正在退出登录…' : state === 'success' ? '已退出登录' : '退出登录失败，请重试';
  }
  return state === 'pending' ? 'Signing out…' : state === 'success' ? 'Signed out' : 'Could not sign out. Try again.';
}

export function showAccountSignOutPending(): void {
  showAccountSignOutFeedback('pending');
}

export async function isAccountSignOutSuccessful(response: Response): Promise<boolean> {
  if (!response.ok) return false;
  try {
    return signedOutResponse(await response.clone().json());
  } catch {
    return false;
  }
}

export async function finishAccountSignOutFeedback(response: Response | boolean): Promise<void> {
  let succeeded = response === true;
  if (response instanceof Response) succeeded = await isAccountSignOutSuccessful(response);
  showAccountSignOutFeedback(succeeded ? 'success' : 'error');
}

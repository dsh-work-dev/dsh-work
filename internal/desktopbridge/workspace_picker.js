// DSH's native directory-picker plugin reads this desktop hook when it loads.
globalThis.__DSH_DIRECTORY_PICKER__ = Object.freeze({
  async pick() {
    const generation = encodeURIComponent(globalThis.__WORK_GENERATION__);
    const controller = new AbortController();
    const abort = () => controller.abort();
    addEventListener('pagehide', abort, {once: true});
    try {
      const {workerFetch} = await import('/__work/bridge.js?generation=' + generation);
      const response = await workerFetch('/__work/workspace-directory', {method: 'POST', signal: controller.signal});
      if (!response.ok) {
        const language = (document.documentElement.lang || navigator.language).toLowerCase();
        throw new Error(language.startsWith('zh') ? '暂时无法打开文件夹选择窗口，请重试。' :
          language.startsWith('ja') ? 'フォルダ選択ウィンドウを開けません。もう一度お試しください。' :
          'Could not open the folder picker. Please try again.');
      }
      return await response.json();
    } finally {
      removeEventListener('pagehide', abort);
    }
  },
});

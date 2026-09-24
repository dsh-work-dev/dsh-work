// Adapt DSH BootPage and uiRenderer's BootHandoff without depending on CSS hashes.
(() => {
  const send = globalThis.fetch.bind(globalThis);
  const generation = globalThis.__WORK_GENERATION__;
  let sawBoot = false, finished = false;
  async function finish(detail) {
    if (finished) return;
    finished = true;
    observer.disconnect();
    for (let attempt = 0; attempt < 3; attempt++) {
      try {
        const response = await send('/__work/web-boot', {
          method: 'POST', headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({Generation: generation, Detail: detail.slice(0, 4096)})
        });
        if (response.ok || response.status === 409) return;
      } catch {}
      await new Promise(resolve => setTimeout(resolve, 250));
    }
  }
  function inspect(records = []) {
    // Cached modules can add and remove BootPage in the same observer batch.
    for (const record of records) for (const node of record.addedNodes) {
      if (node.nodeType === 1 && (node.matches('[data-dsh-boot]') || node.querySelector('[data-dsh-boot]'))) sawBoot = true;
    }
    const root = document.getElementById('root');
    const boot = root?.querySelector('[data-dsh-boot]');
    if (boot) {
      sawBoot = true;
      if (!boot.querySelector('[data-dsh-boot-spinner]') && boot.textContent.includes('Failed to load plugins')) void finish(boot.innerText || boot.textContent);
    } else if (sawBoot && root?.childElementCount && root.textContent.trim()) {
      void finish('');
    }
  }
  const observer = new MutationObserver(inspect);
  observer.observe(document.documentElement, {childList: true, subtree: true, characterData: true});
  inspect();
})();

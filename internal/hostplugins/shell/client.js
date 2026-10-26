// The dsh-work shell menu runs a fixed set of DSH commands. DSH exposes no
// public command invoke to plugins, so a command runs by dispatching its
// current binding through DSH's own keyboard path. A binding the menu cannot
// press (a conflict, an invalid or two-key binding, or settings still loading)
// is reported unavailable rather than pressed into the wrong command.
const SHELL_COMMANDS = ['session.new', 'session.search', 'workspace.add', 'terminal.new', 'browser.new', 'sidebar.left.toggle', 'sidebar.right.toggle', 'settings.open', 'shortcuts.open'];

// Window keys the shell owns while DSH has focus, unless DSH binds the same chord.
function windowKeyAction(event) {
  if (event.altKey || event.metaKey) return undefined;
  if (!event.ctrlKey) return event.code === 'F11' && !event.shiftKey ? 'fullscreen' : undefined;
  if (event.code === 'Equal' || event.code === 'NumpadAdd') return 'zoom-in';
  if (event.shiftKey) return undefined;
  if (event.code === 'Minus' || event.code === 'NumpadSubtract') return 'zoom-out';
  if (event.code === 'Digit0' || event.code === 'Numpad0') return 'zoom-reset';
  return undefined;
}

function keyForCode(code) {
  if (code.startsWith('Key')) return code.slice(3).toLowerCase();
  if (code.startsWith('Digit')) return code.slice(5);
  return code;
}

// The shell sends DSH token values built from the dsh-work theme. Only --dsw-*
// names and dsh-work's own decoration values cross the frame, each checked
// against the CSS property it feeds.
const MAX_THEME_TOKENS = 256;
const DECOR_SHADOWS = ['--dsh-work-stroke', '--dsh-work-raised', '--dsh-work-floating', '--dsh-work-composer', '--dsh-work-button'];
function tokenProperty(name) {
  if (name === '--dsh-work-backdrop') return 'background-image';
  if (name === '--dsh-work-blur' || name === '--dsw-menu-backdrop-filter') return 'backdrop-filter';
  if (DECOR_SHADOWS.includes(name)) return 'box-shadow';
  if (name.startsWith('--dsh-work-')) {
    // dsh-work control values, by suffix (frontend/src/dsh-controls.ts)
    if (/-(border|divider)$/.test(name)) return 'border';
    if (/-radius$/.test(name)) return 'border-radius';
    if (/-image$/.test(name)) return 'background-image';
    if (/-shadow$/.test(name)) return 'box-shadow';
    if (/-weight$/.test(name)) return 'font-weight';
    if (/-font$/.test(name)) return 'font-family';
    if (/-tracking$/.test(name)) return 'letter-spacing';
    if (/-(w|h|left|top|size)$/.test(name)) return 'width';
    return 'color';
  }
  if (name.startsWith('--dsw-radius-')) return 'border-radius';
  if (name.startsWith('--dsw-shadow-')) return 'box-shadow';
  if (name.startsWith('--dsw-font-family') || name === '--dsw-font-mono') return 'font-family';
  return 'color';
}
// DSH's conversation text reads its family from --dsw-font-family; it keeps
// DSH's own font when the interface font changes.
const CONTENT_FONT_TOKENS = ['h1', 'h2', 'h3', 'h4', 'base', 'base-strong', 'base-italic', 'base-strong-italic',
  'table', 'table-head', 'small', 'small-strong', 'small-italic', 'small-strong-italic']
  .map(name => `--dsw-font-markdown-${name}-font-family`);
// No CSS-module class hooks: controls are found by role and structure, and
// buttons and fields by how DSH drew them (classifyControls).
const D = 'body[data-dsh-work-decor]';
// A surface from the measured dsh-work control `name` (frontend/src/dsh-controls.ts).
const skin = name => `border: var(--dsh-work-${name}-border) !important; border-radius: var(--dsh-work-${name}-radius) !important;
  background-color: var(--dsh-work-${name}-bg) !important; background-image: var(--dsh-work-${name}-image) !important;
  box-shadow: var(--dsh-work-${name}-shadow) !important;`;
// DSH switches: the button is the track with one leaf thumb, or the button wraps a
// track that holds the thumb. Only those two structures are restyled.
const TRACK = `${D} [role="switch"]:has(> :only-child):not(:has(> :only-child > *))`;
const WRAPPED = `${D} [role="switch"]:has(> :last-child > :only-child)`;
const switchRules = (track, thumb) => ['off', 'on'].map(state => {
  const checked = state === 'on' ? '[aria-checked="true"]' : '';
  const t = track.replace('[role="switch"]', `[role="switch"]${checked}`);
  const h = thumb.replace('[role="switch"]', `[role="switch"]${checked}`);
  return `${t} { box-sizing: border-box; position: relative; flex: none; width: var(--dsh-work-switch-w) !important; height: var(--dsh-work-switch-h) !important;
  padding: 0 !important; ${skin(`switch-${state}-track`)} transition: background-color 120ms, border-color 120ms !important; }
${h} { box-sizing: border-box; position: absolute !important; margin: 0 !important; transform: none !important;
  width: var(--dsh-work-switch-thumb-size) !important; height: var(--dsh-work-switch-thumb-size) !important;
  top: var(--dsh-work-switch-${state}-thumb-top) !important; left: var(--dsh-work-switch-${state}-thumb-left) !important;
  ${skin(`switch-${state}-thumb`)} transition: left 120ms !important; }`;
}).join('\n');
const CONTROL_STYLE = `
${D} { accent-color: var(--dsh-work-check); }
${switchRules(TRACK, `${TRACK} > :only-child`)}
${WRAPPED} { background: transparent !important; border-color: transparent !important; box-shadow: none !important; }
${switchRules(`${WRAPPED} > :last-child`, `${WRAPPED} > :last-child > :only-child`)}
${D} [data-dsh-work-control="button"] { ${skin('button')} color: var(--dsh-work-button-fg) !important; }
${D} [data-dsh-work-control="button"]:hover:not(:disabled) { background-color: var(--dsh-work-button-hover-bg) !important; }
${D} [data-dsh-work-control="primary"] { ${skin('primary')} color: var(--dsh-work-primary-fg) !important; }
${D} [data-dsh-work-control="primary"]:hover:not(:disabled) { background-color: var(--dsh-work-primary-hover-bg) !important; }
${D} [data-dsh-work-control="field"] { ${skin('input')} }
${D} [data-dsh-work-control="select"] { ${skin('select')} }
${D} option { background-color: var(--dsh-work-option-bg); color: var(--dsh-work-option-fg); }
/* Page titles and section headings outside the conversation take the theme's heading type;
   conversation markdown keeps DSH's content font. */
${D} h1:not([data-conversation-content] *) { font-family: var(--dsh-work-title-font) !important;
  font-weight: var(--dsh-work-title-weight) !important; letter-spacing: var(--dsh-work-title-tracking) !important; }
${D} :is(h2, h3):not([data-conversation-content] *) { font-family: var(--dsh-work-section-font) !important;
  font-weight: var(--dsh-work-section-weight) !important; letter-spacing: var(--dsh-work-section-tracking) !important; }
/* Question answers: a chosen row takes the theme's selected fill; its number marker
   inverts like dsh-work's done marker. Found by data-question-key and ARIA roles. */
${D} [data-question-key] :is([role="radio"], [role="checkbox"])[aria-checked="true"] {
  background-color: var(--dsh-work-selected-bg) !important; color: var(--dsh-work-selected-fg) !important; }
${D} [data-question-key] :is([role="radio"], [role="checkbox"])[aria-checked="true"] > :not(:first-child),
${D} [data-question-key] :is([role="radio"], [role="checkbox"])[aria-checked="true"] > :not(:first-child) * { color: inherit !important; }
${D} [data-question-key] :is([role="radio"], [role="checkbox"])[aria-checked="true"] > :first-child {
  background-color: var(--dsh-work-marker-bg) !important; color: var(--dsh-work-marker-fg) !important; }
`;
// Mark the buttons and fields DSH draws as framed controls so the sheet can give
// them the theme's control look; icon buttons, menu items and the composer keep
// DSH's own. Each is read before it is marked, so the reading is DSH's styling.
const CONTROL_CANDIDATES = ['button', 'input', 'select', 'textarea']
  .map(tag => `${tag}:not([role="switch"]):not([data-dsh-work-control])`).join(', ');
function visibleBorder(style) {
  return style.borderTopStyle !== 'none' && parseFloat(style.borderTopWidth) > 0
    && !/transparent|rgba\([^)]*,\s*0\)/.test(style.borderTopColor);
}
function classifyControls(primaryFill) {
  for (const element of document.querySelectorAll(CONTROL_CANDIDATES)) {
    if (typeof element.closest !== 'function' || !element.getClientRects().length) continue;
    let kind = '';
    const style = getComputedStyle(element);
    // DSH's primary fill, or its info fill (the composer's send button), anywhere but menus.
    if (element.tagName === 'BUTTON' && primaryFill.includes(style.backgroundColor) && !element.closest('[role="menu"], [role="listbox"]')) {
      kind = 'primary';
    } else if (!element.closest('[data-composer-card], [role="menu"], [role="listbox"], [role="tablist"]')) {
      if (element.tagName === 'BUTTON') {
        const text = (element.textContent || '').trim();
        const box = element.getBoundingClientRect();
        // DSH draws secondary buttons with a quiet fill instead of a frame. Navigation
        // rows and selected list rows are filled too, so size and state rule them out.
        const filled = !/transparent|rgba\([^)]*,\s*0\)/.test(style.backgroundColor) && box.width <= 260 && box.height <= 44
          && !element.matches('[aria-current], [aria-selected], [aria-pressed]') && !element.closest('[data-slot="sidebar"], nav, [role="tree"]');
        if (text && (visibleBorder(style) || filled)) kind = 'button';
      } else if (element.tagName === 'SELECT') {
        if (visibleBorder(style)) kind = 'select';
      } else if (!['checkbox', 'radio', 'range', 'hidden', 'file', 'color'].includes(element.type)
        && (visibleBorder(style) || !/transparent|rgba\([^)]*,\s*0\)/.test(style.backgroundColor))) {
        kind = 'field';
      }
    }
    element.setAttribute('data-dsh-work-control', kind);
  }
}
// DSH declares its elevation shadows on every element (`body, body *`), so a
// value on body alone never reaches them; this sheet redeclares them from the
// dsh-work decoration values. The rest uses DSH's semantic hooks.
const DECOR_STYLE = `
body[data-dsh-work-decor], body[data-dsh-work-decor] * {
  --dsw-elevation-stroke: var(--dsh-work-stroke);
  --dsw-elevation-panel: var(--dsh-work-raised);
  --dsw-elevation-soft: var(--dsh-work-raised);
  --dsw-elevation-prominent: var(--dsh-work-floating);
}
body[data-dsh-work-decor] :is([data-slot="main"], [data-slot="main.conversation"]) > :not([data-slot]) { background-image: var(--dsh-work-backdrop); background-attachment: fixed; }
body[data-dsh-work-decor] [data-composer-card] { box-shadow: var(--dsh-work-composer); backdrop-filter: var(--dsh-work-blur); }
/* Lists DSH marks as plugin groups and the task manager list take the theme's group: one
   card with the group border, shadow and radius, rows split by the theme's row divider. */
body[data-dsh-work-decor] [data-plugin-group] > ul,
body[data-dsh-work-decor] [data-testid="task-manager-page"] ul[aria-label] {
  overflow: hidden; padding: 0; gap: 0;
  border: var(--dsh-work-group-border); border-radius: var(--dsw-radius-lg);
  background: var(--dsw-alias-bg-layer-1); box-shadow: var(--dsh-work-group-shadow);
  backdrop-filter: var(--dsh-work-blur);
}
body[data-dsh-work-decor] [data-plugin-group] > ul > li,
body[data-dsh-work-decor] [data-testid="task-manager-page"] ul[aria-label] > li { border-radius: 0; margin: 0; }
/* plugin rows (data-plugin-item) and bundle rows (data-plugin-package) */
body[data-dsh-work-decor] [data-plugin-group] > ul > li { padding: 6px 12px; }
body[data-dsh-work-decor] [data-plugin-group] > ul > li + li,
body[data-dsh-work-decor] [data-testid="task-manager-page"] ul[aria-label] > li + li { border-top: var(--dsh-work-row-divider); }
${CONTROL_STYLE}
`;
function parseHostAppearance(value) {
  if (!value || typeof value !== 'object' || !['light', 'dark'].includes(value.mode) || !value.tokens || typeof value.tokens !== 'object') return null;
  const entries = Object.entries(value.tokens);
  if (entries.length > MAX_THEME_TOKENS) return null;
  const tokens = {};
  for (const [name, token] of entries) {
    if (!/^--(dsw|dsh-work)-[a-z0-9-]+$/.test(name) || typeof token !== 'string' || token.length > 512 || !CSS.supports(tokenProperty(name), token)) return null;
    tokens[name] = token;
  }
  return {mode: value.mode, tokens};
}

const modalSelector = '[role="dialog"][aria-modal="true"], [data-shortcut-modal][role="dialog"]';
function visibleModal() {
  return Array.from(document.querySelectorAll(modalSelector)).filter(element => {
    const style = getComputedStyle(element);
    return style.display !== 'none' && style.visibility !== 'hidden' && element.getClientRects().length > 0;
  }).at(-1);
}

function modalMaskFor(modal) {
  let current = modal;
  while (current?.parentElement && current.parentElement !== document.body) {
    const parent = current.parentElement;
    const mask = Array.from(parent.children).find(element => {
      if (element === current || element.getAttribute('aria-hidden') !== 'true') return false;
      const name = typeof element.className === 'string' ? element.className : '';
      return /mask|backdrop/i.test(name);
    });
    if (mask) return mask;
    current = parent;
  }
  return null;
}

function modalMaskStyle(modal) {
  const mask = modalMaskFor(modal);
  const bodyStyle = getComputedStyle(document.body);
  const fallbackBackground = bodyStyle.getPropertyValue('--dsw-alias-bg-mask-1').trim() || 'rgba(0, 0, 0, 0.24)';
  const fallbackBlur = bodyStyle.getPropertyValue('--dsw-mask-blur').trim() || 'none';
  if (!mask) return {background: fallbackBackground, blur: fallbackBlur};
  const style = getComputedStyle(mask);
  const after = getComputedStyle(mask, '::after');
  const afterBackground = after.backgroundColor;
  const background = afterBackground && afterBackground !== 'rgba(0, 0, 0, 0)' ? afterBackground : style.backgroundColor;
  const blur = style.backdropFilter !== 'none' ? style.backdropFilter : after.backdropFilter;
  return {
    background: background && background !== 'rgba(0, 0, 0, 0)' ? background : fallbackBackground,
    blur: blur && blur !== 'none' ? blur : fallbackBlur,
  };
}

function connectShell(ctx) {
  const shellOrigin = window.parent && window.parent !== window ? window.location?.ancestorOrigins?.[0] : undefined;
  if (!shellOrigin) return;
  const post = message => window.parent.postMessage({version: 1, ...message}, shellOrigin);
  // The workspace and layout services are optional: the menu keeps working
  // without them, and the commands they serve report unavailable.
  const services = {uiWorkspace: undefined, layout: undefined};
  let report = () => {};
  let pendingAppearance;
  let applyHostAppearance = () => {};

  // dsh-work's appearance is a registered DSH theme selected for this page only:
  // a non-built-in theme id is never written to DSH's stored preference.
  ctx.inject(['theme'], themeContext => {
    const theme = themeContext.theme;
    let active;
    let revision = 0;
    let contentFont;
    let decorStyle;
    applyHostAppearance = appearance => {
      const id = `dsh-work-${++revision}`;
      const previous = active;
      const tokens = {...appearance.tokens};
      if (tokens['--dsw-font-family']) {
        // Read DSH's own family before the first dsh-work theme covers it.
        contentFont ??= getComputedStyle(document.body).getPropertyValue('--dsw-font-family').trim();
        if (contentFont) for (const name of CONTENT_FONT_TOKENS) tokens[name] = contentFont;
      }
      if (tokens['--dsh-work-raised']) {
        if (!decorStyle) {
          decorStyle = document.createElement('style');
          decorStyle.dataset.dshWork = 'decor';
          decorStyle.textContent = DECOR_STYLE;
          document.head.append(decorStyle);
        }
        document.body.setAttribute('data-dsh-work-decor', '');
      } else {
        document.body.removeAttribute('data-dsh-work-decor');
        for (const element of document.querySelectorAll('[data-dsh-work-control]')) element.removeAttribute?.('data-dsh-work-control');
        decorStyle?.remove();
        decorStyle = undefined;
      }
      try {
        active = {id, dispose: theme.register({id, colorScheme: appearance.mode, tokens})};
        theme.setTheme(id);
      } catch (error) {
        console.warn('Could not apply the dsh-work appearance to DSH', error);
      }
      if (previous && previous !== active) previous.dispose();
    };
    // DSH re-adopts its stored preference when its settings change; keep ours.
    themeContext.on('theme/change', snapshot => {
      if (active && snapshot.preference !== active.id && snapshot.themes.some(item => item.id === active.id)) theme.setTheme(active.id);
    });
    if (pendingAppearance) applyHostAppearance(pendingAppearance);
    themeContext.effect(() => () => {
      applyHostAppearance = () => {};
      const current = active;
      active = undefined;
      current?.dispose();
      document.body.removeAttribute('data-dsh-work-decor');
      decorStyle?.remove();
      for (const element of document.querySelectorAll('[data-dsh-work-control]')) element.removeAttribute?.('data-dsh-work-control');
    });
  });
  // DSH's own Appearance row would fight dsh-work's theme; an empty row shadows it.
  ctx.inject(['slots'], slotsContext => {
    slotsContext.slots.inject('settings.general.item', () => slotsContext.slots.register({name: 'settings.general.item', id: 'appearance', priority: -1}, () => null));
  });

  // DSH's primary button fill, resolved the way the browser computes a background.
  let fillProbe;
  const resolvedPrimaryFill = () => {
    fillProbe ??= document.createElement('span');
    if (!fillProbe.isConnected) document.body.append(fillProbe);
    return ['--dsw-alias-button-primary-fill', '--dsw-alias-button-info-fill'].map(token => {
      fillProbe.style.cssText = `position:absolute;visibility:hidden;background-color:var(${token})`;
      return getComputedStyle(fillProbe).backgroundColor;
    }).filter(fill => fill && !/rgba\([^)]*,\s*0\)/.test(fill));
  };
  let modalFrame = 0;
  let lastModalState = '';
  const reportModal = () => {
    const modal = visibleModal();
    const state = modal ? {active: true, ...modalMaskStyle(modal)} : {active: false, background: '', blur: ''};
    const key = JSON.stringify(state);
    if (key === lastModalState) return;
    lastModalState = key;
    post({type: 'dsh-work/modal-mask', ...state});
  };
  ctx.effect(() => {
    const schedule = () => {
      if (modalFrame) return;
      modalFrame = requestAnimationFrame(() => {
        modalFrame = 0;
        reportModal();
        if (document.body.hasAttribute?.('data-dsh-work-decor')) {
          try { classifyControls(resolvedPrimaryFill()); } catch (error) { console.warn('dsh-work control styling skipped', error); }
        }
      });
    };
    // DSH mounts and unmounts its modals; attribute changes cover dialogs that stay mounted.
    const observer = new MutationObserver(schedule);
    observer.observe(document.body, {
      attributes: true, subtree: true, childList: true,
      attributeFilter: ['aria-modal', 'data-shortcut-modal', 'hidden', 'open', 'role'],
    });
    schedule();
    return () => {
      observer.disconnect();
      if (modalFrame) cancelAnimationFrame(modalFrame);
    };
  });

  for (const name of Object.keys(services)) {
    ctx.inject([name], ctx => {
      services[name] = ctx[name];
      report();
      ctx.effect(() => () => { services[name] = undefined; report(); });
    });
  }
  ctx.inject(['shortcuts'], ctx => {
    const {catalog, config, fixedCatalog} = ctx.shortcuts;
    const entries = () => new Map(catalog.getSnapshot().map(entry => [entry.id, entry]));
    const ready = () => (config?.getSnapshot().status ?? 'ready') === 'ready';
    const hasPublicService = id => id === 'session.new'
      ? typeof services.uiWorkspace?.startSession === 'function'
      : id === 'sidebar.left.toggle' && typeof services.layout?.toggleSidebar === 'function';
    const shortcutState = entry => {
      if (!entry.binding) return 'unbound';
      if (!ready() || entry.issue || entry.conflicts?.length || entry.binding.secondCode !== undefined) return 'unavailable';
      return 'ready';
    };
    const commandState = (id, entry) => {
      if (id === 'session.new' || id === 'sidebar.left.toggle') return hasPublicService(id) ? 'ready' : 'unavailable';
      return shortcutState(entry);
    };
    report = () => {
      const current = entries();
      post({type: 'dsh-work/catalog', commands: SHELL_COMMANDS.flatMap(id => {
        const entry = current.get(id);
        const direct = id === 'session.new' || id === 'sidebar.left.toggle';
        if (!entry && !direct) return [];
        const canPress = !!entry?.binding && ready() && !entry.issue && !entry.conflicts?.length && entry.binding.secondCode === undefined;
        return [{id, keys: canPress ? [...entry.keys] : [], state: commandState(id, entry)}];
      })});
    };
    ctx.effect(() => catalog.subscribe(report));
    if (config) ctx.effect(() => config.subscribe(report));
    report();

    let lastSidebarState;
    const root = document.getElementById('root') ?? document.documentElement;
    const reportSidebar = () => {
      const frame = root?.querySelector?.('[style*="grid-template-columns"]');
      if (!frame) return;
      const open = !frame.hasAttribute('data-sidebar-collapsed');
      if (open === lastSidebarState) return;
      lastSidebarState = open;
      post({type: 'dsh-work/sidebar-state', open});
    };
    ctx.effect(() => {
      const observer = new MutationObserver(reportSidebar);
      observer.observe(root, {attributes: true, attributeFilter: ['data-sidebar-collapsed', 'style'], childList: true, subtree: true});
      reportSidebar();
      return () => observer.disconnect();
    });

    const onMessage = event => {
      if (event.source !== window.parent || event.origin !== shellOrigin) return;
      const data = event.data;
      if (data?.version !== 1) return;
      if (data.type === 'dsh-work/appearance') {
        const appearance = parseHostAppearance(data);
        if (appearance) {
          pendingAppearance = appearance;
          applyHostAppearance(appearance);
        }
        return;
      }
      if (data.type === 'dsh-work/modal-mask-click') {
        const modal = visibleModal();
        const mask = modal && modalMaskFor(modal);
        if (mask && mask !== modal) mask.click();
        return;
      }
      if (data?.type !== 'dsh-work/command' || !SHELL_COMMANDS.includes(data.id)) return;
      let handled = false;
      try {
        if (data.id === 'session.new') {
          if (typeof services.uiWorkspace?.startSession === 'function') {
            services.uiWorkspace.startSession();
            handled = true;
          }
        } else if (data.id === 'sidebar.left.toggle') {
          if (typeof services.layout?.toggleSidebar === 'function') {
            services.layout.toggleSidebar();
            handled = true;
          }
        } else {
          const entry = entries().get(data.id);
          if (entry && shortcutState(entry) === 'ready') {
            const modifiers = new Set(entry.binding.modifiers);
            const press = new KeyboardEvent('keydown', {
              code: entry.binding.code, key: keyForCode(entry.binding.code), bubbles: true, cancelable: true,
              ctrlKey: modifiers.has('control'), altKey: modifiers.has('alt'), shiftKey: modifiers.has('shift'), metaKey: modifiers.has('meta'),
            });
            // DSH prevents default on input it consumes, whether it runs the command or declines it.
            handled = !document.body.dispatchEvent(press);
          }
        }
      } catch {
        handled = false;
      }
      post({type: 'dsh-work/command-result', id: data.id, handled});
    };
    const sameChord = (binding, event) => binding?.code === event.code && binding.secondCode === undefined &&
      ['control', 'alt', 'shift', 'meta'].every(modifier => binding.modifiers.includes(modifier) === !!event[modifier === 'control' ? 'ctrlKey' : `${modifier}Key`]);
    const boundInDSH = event => catalog.getSnapshot().some(entry => sameChord(entry.binding, event)) ||
      (fixedCatalog?.getSnapshot() ?? []).some(entry => entry.bindings.some(binding => sameChord(binding, event)));
    // Alt pressed alone or F10 opens the shell menu bar, as in a native window.
    let altAlone = false;
    const keydown = event => {
      if (visibleModal() && (event.key === 'F10' || event.key === 'Alt')) {
        altAlone = false;
        return;
      }
      if (event.key === 'F10' && !event.ctrlKey && !event.altKey && !event.shiftKey && !event.metaKey) {
        event.preventDefault();
        post({type: 'dsh-work/menu-key', key: 'F10'});
        altAlone = false;
        return;
      }
      const action = windowKeyAction(event);
      if (action && !event.defaultPrevented && !boundInDSH(event)) {
        event.preventDefault();
        post({type: 'dsh-work/window-key', action});
      }
      altAlone = event.key === 'Alt' && !event.repeat;
    };
    const keyup = event => {
      if (event.key === 'Alt' && altAlone) post({type: 'dsh-work/menu-key', key: 'Alt'});
      altAlone = false;
    };
    window.addEventListener('message', onMessage);
    post({type: 'dsh-work/component-ready', id: 'shell'});
    window.addEventListener('keydown', keydown, true);
    window.addEventListener('keyup', keyup, true);
    ctx.effect(() => () => {
      window.removeEventListener('message', onMessage);
      window.removeEventListener('keydown', keydown, true);
      window.removeEventListener('keyup', keyup, true);
    });
  });
}
// Other origins open in the system browser. DSH itself lives only in the shell
// frame: same-origin new windows would be unmanaged DSH pages.
const recentlyOpenedExternal = new Map();
function openExternal(raw) {
  const url = new URL(raw, location.href);
  if (!['http:', 'https:'].includes(url.protocol)) return;
  const now = Date.now();
  recentlyOpenedExternal.forEach((openedAt, href) => {
    if (now - openedAt > 3000) recentlyOpenedExternal.delete(href);
  });
  if (now - (recentlyOpenedExternal.get(url.href) ?? 0) < 3000) return;
  recentlyOpenedExternal.set(url.href, now);
  const workerFetch = globalThis.WorkerBridge?.workerFetch;
  if (typeof workerFetch !== 'function') return;
  void workerFetch('/__work/external?url=' + encodeURIComponent(url.href), {method: 'POST'})
    .then(response => { if (!response.ok) console.warn('Could not open the external browser.'); })
    .catch(() => console.warn('Could not open the external browser.'));
}
function isNewWindowTarget(target) {
  return !!target && !['_self', '_parent', '_top'].includes(target.toLowerCase());
}
function installLinkPolicy() {
  if (!document?.addEventListener || !window) return;
  document.addEventListener('click', event => {
    const anchor = event.target?.closest?.('a[href]');
    if (!anchor) return;
    const url = new URL(anchor.href, location.href);
    if (url.origin !== location.origin) {
      event.preventDefault();
      openExternal(url.href);
    } else if (isNewWindowTarget(anchor.target)) {
      event.preventDefault();
      console.warn('dsh-work does not open DSH in a new window.');
    }
  }, true);
  window.open = ((raw, target) => {
    const url = new URL(raw ?? '', location.href);
    if (url.origin !== location.origin) {
      openExternal(url.href);
      return null;
    }
    if (target === undefined || isNewWindowTarget(target)) {
      console.warn('dsh-work does not open DSH in a new window.');
      return null;
    }
    location.assign(url.href);
    return window;
  });
}
window.__ModuleLoader__.load({id: '@dsh-work/shell', factory: () => ({
  inject: ['shortcuts'],
  apply(ctx) {
    connectShell(ctx);
    installLinkPolicy();
  }
})});

(() => {
  const statusEl = document.getElementById('status');
  const terminalEl = document.getElementById('terminal');
  const keybar = document.getElementById('keybar');
  const mdToggle = document.querySelector('[data-key="md-toggle"]');
  const mdSubmenu = document.getElementById('md-submenu');
  const modal = document.getElementById('confirm-modal');
  const confirmTitle = document.getElementById('confirm-title');
  const confirmMessage = document.getElementById('confirm-message');
  const confirmBtn = document.getElementById('confirm-exit');
  const cancelBtn = document.getElementById('cancel-exit');
  const pasteProxy = document.getElementById('paste-proxy');
  const root = document.documentElement;
  const userAgent = navigator && navigator.userAgent ? navigator.userAgent : '';
  const isMobileUA = /Android|iPhone|iPad|iPod/i.test(userAgent);
  const pointerFine = Boolean(window.matchMedia && window.matchMedia('(pointer: fine)').matches);
  const hoverHover = Boolean(window.matchMedia && window.matchMedia('(hover: hover)').matches);
  const isDesktopLike = Boolean(pointerFine && hoverHover && !isMobileUA);
  const keybarEnabled = !isDesktopLike;
  const hostLabel = window.location.host || 'localhost';
  const aliasMeta = document.querySelector('meta[name="alices-mirror-alias"]');
  const aliasLabel = aliasMeta ? (aliasMeta.getAttribute('content') || '').trim() : '';
  const titleHostLabel = aliasLabel || hostLabel;
  const titlePrefix = 'alices-mirror|';
  if (keybar && !keybarEnabled) {
    root.classList.add('keybar-hidden');
  }
  const visualViewport = window.visualViewport;
  const useVisualViewport = Boolean(visualViewport && keybarEnabled);
  let baseViewportHeight = window.innerHeight;
  const urlRegex = /(?:https?:\/\/|www\.)[^\s<>\"']+/gi;

  const term = new Terminal({
    cursorBlink: true,
    scrollback: 2000,
    fontFamily: '"JetBrains Mono", "Fira Code", "Cascadia Mono", monospace',
    fontSize: 14,
    theme: {
      background: '#0b0e13',
      foreground: '#e6eef9',
      cursor: '#56d39f',
      selection: '#2a3345'
    }
  });
  const fitAddon = new FitAddon.FitAddon();
  term.loadAddon(fitAddon);
  term.open(terminalEl);
  applyViewportHeight();
  fitAddon.fit();
  term.focus();
  term.onTitleChange(handleTitleChange);
  registerLinkProvider();

  const encoder = new TextEncoder();
  let socket;
  let lineBuffer = '';
  let pendingConfirm = null;
  let pendingCancel = null;
  let terminalFocused = false;
  let pendingKeybarCopy = '';
  let mdOpen = false;
  let suppressKeybarClickUntil = 0;
  let lastTitleCwd = '';
  let lastTitleProc = '';

  function trimTrailingPunctuation(value) {
    let end = value.length;
    while (end > 0 && /[.,;:!?]/.test(value[end - 1])) {
      end -= 1;
    }
    let cleaned = value.slice(0, end);
    if (!cleaned) {
      return '';
    }
    const closingPairs = { ')': '(', ']': '[', '}': '{' };
    while (cleaned.length > 0) {
      const last = cleaned[cleaned.length - 1];
      const opener = closingPairs[last];
      if (!opener) {
        break;
      }
      const openCount = (cleaned.match(new RegExp(`\\${opener}`, 'g')) || []).length;
      const closeCount = (cleaned.match(new RegExp(`\\${last}`, 'g')) || []).length;
      if (closeCount <= openCount) {
        break;
      }
      cleaned = cleaned.slice(0, -1);
    }
    return cleaned;
  }

  function buildLineTextMap(line) {
    if (!line) {
      return null;
    }
    if (typeof line.getCell !== 'function') {
      const fallback = typeof line.translateToString === 'function' ? line.translateToString(true) : '';
      const fallbackMap = Array.from({ length: fallback.length }, (_, index) => index);
      return { text: fallback, indexToCol: fallbackMap };
    }
    const maxCols = typeof line.length === 'number' ? line.length : term.cols;
    const indexToCol = [];
    let text = '';
    for (let col = 0; col < maxCols; col += 1) {
      const cell = line.getCell(col);
      if (!cell) {
        continue;
      }
      const width = typeof cell.getWidth === 'function' ? cell.getWidth() : 1;
      if (width === 0) {
        continue;
      }
      let chars = typeof cell.getChars === 'function' ? cell.getChars() : '';
      if (!chars) {
        chars = ' ';
      }
      text += chars;
      for (let i = 0; i < chars.length; i += 1) {
        indexToCol.push(col);
      }
    }
    let trimLength = text.length;
    while (trimLength > 0 && text[trimLength - 1] === ' ') {
      trimLength -= 1;
    }
    if (trimLength !== text.length) {
      text = text.slice(0, trimLength);
      indexToCol.length = trimLength;
    }
    return { text, indexToCol };
  }

  function openLinkInNewTab(url) {
    const opened = window.open(url, '_blank', 'noopener,noreferrer');
    if (!opened) {
      return false;
    }
    try {
      opened.opener = null;
    } catch (_) {
    }
    return true;
  }

  function resolveLinkTarget(text) {
    if (!text) {
      return '';
    }
    const candidate = text.startsWith('www.') ? `http://${text}` : text;
    try {
      const url = new URL(candidate);
      if (url.protocol !== 'http:' && url.protocol !== 'https:') {
        return '';
      }
      return url.toString();
    } catch (_) {
      return '';
    }
  }

  function shouldSkipLinkActivation() {
    if (typeof term.hasSelection === 'function') {
      return term.hasSelection();
    }
    if (typeof term.getSelection === 'function') {
      return Boolean(term.getSelection());
    }
    return false;
  }

  function registerLinkProvider() {
    if (!term || typeof term.registerLinkProvider !== 'function') {
      return;
    }
    term.registerLinkProvider({
      provideLinks: (y, callback) => {
        const buffer = term.buffer && term.buffer.active;
        if (!buffer || typeof buffer.getLine !== 'function') {
          callback(undefined);
          return;
        }
        const line = buffer.getLine(y - 1);
        if (!line) {
          callback(undefined);
          return;
        }
        const info = buildLineTextMap(line);
        if (!info || !info.text) {
          callback(undefined);
          return;
        }
        const links = [];
        urlRegex.lastIndex = 0;
        let match;
        while ((match = urlRegex.exec(info.text)) !== null) {
          const raw = match[0];
          const startIndex = match.index;
          const cleaned = trimTrailingPunctuation(raw);
          if (!cleaned) {
            continue;
          }
          const endIndex = startIndex + cleaned.length;
          const startCol = info.indexToCol[startIndex];
          const endCol = info.indexToCol[endIndex - 1];
          if (startCol === undefined || endCol === undefined) {
            continue;
          }
          const linkText = cleaned;
          links.push({
            text: linkText,
            range: {
              start: { x: startCol + 1, y },
              end: { x: endCol + 1, y }
            },
            activate: (_event, text) => {
              if (shouldSkipLinkActivation()) {
                return;
              }
              const target = resolveLinkTarget(text);
              if (!target) {
                return;
              }
              if (!openLinkInNewTab(target)) {
                copyTextToClipboard(
                  target,
                  'Popup blocked. Link copied.',
                  'Popup blocked. Copy failed.'
                );
              }
            },
            decorations: { underline: true, pointerCursor: true }
          });
        }
        callback(links.length ? links : undefined);
      }
    });
  }

  function updateTitle(cwd, proc) {
    const safeCwd = cwd || lastTitleCwd;
    const safeProc = proc || lastTitleProc;
    if (!safeCwd || !safeProc) {
      return;
    }
    lastTitleCwd = safeCwd;
    lastTitleProc = safeProc;
    document.title = `${titleHostLabel} - ${safeCwd} - ${safeProc}`;
  }

  function handleTitleChange(title) {
    if (typeof title !== 'string' || !title.startsWith(titlePrefix)) {
      return;
    }
    const payload = title.slice(titlePrefix.length);
    const divider = payload.indexOf('|');
    if (divider <= 0) {
      return;
    }
    const cwd = payload.slice(0, divider);
    const proc = payload.slice(divider + 1);
    updateTitle(cwd, proc);
  }

  if (term.textarea) {
    term.textarea.addEventListener('focus', () => {
      terminalFocused = true;
      scheduleResize(80);
    });
    term.textarea.addEventListener('blur', () => {
      terminalFocused = false;
      scheduleResize(120);
    });
  }

  function updateStatus(message) {
    statusEl.textContent = message;
  }

  function normalizeInput(data) {
    if (typeof data !== 'string' || data.length === 0) {
      return data;
    }
    if (typeof data.normalize !== 'function') {
      return data;
    }
    if (!/[^\x00-\x7f]/.test(data)) {
      return data;
    }
    try {
      return data.normalize('NFC');
    } catch (_) {
      return data;
    }
  }

  function connect() {
    const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
    const wsUrl = `${proto}://${window.location.host}/ws`;
    socket = new WebSocket(wsUrl);
    socket.binaryType = 'arraybuffer';

    socket.onopen = () => {
      updateStatus('Connected');
      sendResize();
    };
    socket.onclose = () => updateStatus('Disconnected');
    socket.onerror = () => updateStatus('Connection error');
    socket.onmessage = (event) => {
      if (typeof event.data === 'string') {
        try {
          const payload = JSON.parse(event.data);
          if (payload.type === 'status' && payload.message) {
            updateStatus(payload.message);
            return;
          }
          if (payload.type === 'reset-failed') {
            const title = payload.title || 'Reset failed';
            const message = payload.message || 'The shell could not be fully reset.';
            showModalNotice(title, message);
            return;
          }
        } catch (_) {
        }
        return;
      }
      term.write(new Uint8Array(event.data));
    };
  }

  function sendBinary(data) {
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      return;
    }
    if (data instanceof Uint8Array) {
      socket.send(data);
      return;
    }
    socket.send(encoder.encode(data));
  }

  function sendResize() {
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      return;
    }
    const payload = {
      type: 'resize',
      cols: term.cols,
      rows: term.rows
    };
    socket.send(JSON.stringify(payload));
  }

  function sendReset() {
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      updateStatus('Not connected.');
      return;
    }
    socket.send(JSON.stringify({ type: 'reset' }));
  }

  let resizeTimer;
  let resizeRaf = 0;

  function applyViewportHeight() {
    const height = useVisualViewport ? visualViewport.height : window.innerHeight;
    if (!height || Number.isNaN(height)) {
      return;
    }
    updateBaseViewportHeight();
    root.style.setProperty('--viewport-height', `${Math.round(height)}px`);
    if (!useVisualViewport || Number.isNaN(baseViewportHeight)) {
      root.style.setProperty('--keyboard-offset', '0px');
      updateKeybarHeight();
      return;
    }
    const offset = Math.max(0, baseViewportHeight - visualViewport.height - visualViewport.offsetTop);
    root.style.setProperty('--keyboard-offset', `${Math.round(offset)}px`);
    updateKeybarHeight();
  }

  function updateKeybarHeight() {
    if (!keybar || !terminalEl) {
      return;
    }
    if (root.classList.contains('keybar-hidden')) {
      const currentValue = parseFloat(getComputedStyle(root).getPropertyValue('--keybar')) || 0;
      if (currentValue !== 0) {
        root.style.setProperty('--keybar', '0px');
      }
      return;
    }
    const rect = keybar.getBoundingClientRect();
    if (!rect || Number.isNaN(rect.height) || rect.height < 1) {
      root.style.setProperty('--keybar', '0px');
      return;
    }
    const height = Math.max(0, Math.round(rect.height));
    const currentValue = parseFloat(getComputedStyle(root).getPropertyValue('--keybar')) || 0;
    if (Math.abs(height - currentValue) < 1) {
      return;
    }
    root.style.setProperty('--keybar', `${height}px`);
  }

  function setMdMenu(open) {
    mdOpen = open;
    if (mdToggle) {
      mdToggle.setAttribute('aria-expanded', String(open));
    }
    if (mdSubmenu) {
      mdSubmenu.setAttribute('aria-hidden', String(!open));
    }
    if (keybar) {
      keybar.classList.toggle('keybar--md-open', open);
    }
    updateKeybarHeight();
  }

  function toggleMdMenu() {
    setMdMenu(!mdOpen);
  }

  function insertSnippet(text) {
    sendBinary(normalizeInput(text));
    term.focus();
  }

  function updateBaseViewportHeight() {
    const innerHeight = window.innerHeight;
    if (!innerHeight || Number.isNaN(innerHeight)) {
      return;
    }
    if (!useVisualViewport) {
      baseViewportHeight = innerHeight;
      return;
    }
    const keyboardClosed = Math.abs(innerHeight - visualViewport.height) <= 1 && visualViewport.offsetTop === 0;
    if (keyboardClosed) {
      baseViewportHeight = innerHeight;
      return;
    }
    if (innerHeight > baseViewportHeight) {
      baseViewportHeight = innerHeight;
    }
  }

  function scheduleResize(delay) {
    applyViewportHeight();
    if (resizeRaf) {
      cancelAnimationFrame(resizeRaf);
    }
    resizeRaf = requestAnimationFrame(() => {
      fitAddon.fit();
      sendResize();
    });
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => {
      fitAddon.fit();
      sendResize();
    }, delay ?? 120);
  }

  const defaultConfirmTitle = confirmTitle ? (confirmTitle.textContent || '').trim() : 'Confirm shell exit';
  const defaultConfirmMessage = confirmMessage ? (confirmMessage.textContent || '').trim()
    : 'This will close the current shell and immediately respawn a fresh one.';
  const defaultConfirmLabel = confirmBtn ? (confirmBtn.textContent || '').trim() : 'Confirm';
  const defaultCancelLabel = cancelBtn ? (cancelBtn.textContent || '').trim() : 'Cancel';

  function openConfirmDialog(options) {
    if (pendingConfirm) {
      return;
    }
    const title = options && options.title ? options.title : defaultConfirmTitle;
    const message = options && options.message ? options.message : defaultConfirmMessage;
    const confirmLabel = options && options.confirmLabel ? options.confirmLabel : defaultConfirmLabel;
    const cancelLabel = options && options.cancelLabel ? options.cancelLabel : defaultCancelLabel;
    const showCancel = options && options.showCancel !== undefined ? options.showCancel : true;
    if (confirmTitle) {
      confirmTitle.textContent = title;
    }
    if (confirmMessage) {
      confirmMessage.textContent = message;
    }
    if (confirmBtn) {
      confirmBtn.textContent = confirmLabel;
    }
    if (cancelBtn) {
      cancelBtn.textContent = cancelLabel;
      cancelBtn.style.display = showCancel ? '' : 'none';
    }
    pendingConfirm = options && options.onConfirm ? options.onConfirm : null;
    pendingCancel = options && options.onCancel ? options.onCancel : null;
    modal.classList.remove('hidden');
  }

  function confirmExit(onConfirm, onCancel) {
    openConfirmDialog({
      title: defaultConfirmTitle,
      message: defaultConfirmMessage,
      confirmLabel: defaultConfirmLabel,
      onConfirm,
      onCancel
    });
  }

  function showModalNotice(title, message) {
    openConfirmDialog({
      title,
      message,
      confirmLabel: 'OK',
      showCancel: false,
      onConfirm: () => {
      }
    });
  }

  function clearConfirm() {
    pendingConfirm = null;
    pendingCancel = null;
    modal.classList.add('hidden');
  }

  confirmBtn.addEventListener('click', () => {
    if (pendingConfirm) {
      pendingConfirm();
    }
    clearConfirm();
  });

  cancelBtn.addEventListener('click', () => {
    if (pendingCancel) {
      pendingCancel();
    }
    clearConfirm();
  });

  modal.addEventListener('click', (event) => {
    if (event.target === modal) {
      if (pendingCancel) {
        pendingCancel();
      }
      clearConfirm();
    }
  });

  function updateLineBuffer(data) {
    for (const ch of data) {
      if (ch === '\r' || ch === '\n') {
        lineBuffer = '';
        continue;
      }
      if (ch === '\x7f') {
        lineBuffer = lineBuffer.slice(0, -1);
        continue;
      }
      if (ch === '\x1b' || ch < ' ') {
        continue;
      }
      lineBuffer += ch;
    }
  }

  function handleInput(data) {
    data = normalizeInput(data);

    if (data === '\x04') {
      confirmExit(() => sendBinary(data));
      return;
    }

    if (data === '\r') {
      const cmd = lineBuffer.trim();
      if (cmd === 'exit' || cmd === 'logout') {
        confirmExit(
          () => {
            sendBinary('\r');
            lineBuffer = '';
          },
          () => {
            sendBinary('\x15');
            lineBuffer = '';
            updateStatus('Exit cancelled.');
          }
        );
        return;
      }
      lineBuffer = '';
      sendBinary(data);
      return;
    }

    updateLineBuffer(data);
    sendBinary(data);
  }

  term.onData(handleInput);

  function isPasteTarget(target) {
    return target === term.textarea || target === pasteProxy;
  }

  document.addEventListener('paste', (event) => {
    if (!isPasteTarget(event.target)) {
      return;
    }
    const text = event.clipboardData && event.clipboardData.getData('text');
    if (!text) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    sendBinary(normalizeInput(text));
  }, true);

  function requestPaste() {
    if (navigator.clipboard && typeof navigator.clipboard.readText === 'function') {
      navigator.clipboard.readText().then((text) => {
        if (text) {
          sendBinary(normalizeInput(text));
          return;
        }
        focusPasteProxy('Use your system paste action.');
      }).catch(() => {
        if (!attemptExecPaste()) {
          focusPasteProxy('Use your system paste action.');
        }
      });
      return;
    }
    if (!attemptExecPaste()) {
      focusPasteProxy('Use your system paste action.');
    }
  }

  function focusPasteProxy(message) {
    pasteProxy.value = '';
    pasteProxy.focus();
    if (message) {
      updateStatus(message);
    }
  }

  function attemptExecPaste() {
    if (!pasteProxy || typeof document.execCommand !== 'function') {
      return false;
    }
    pasteProxy.value = '';
    pasteProxy.focus();
    pasteProxy.select();
    let success = false;
    try {
      success = document.execCommand('paste');
    } catch (_) {
      success = false;
    }
    return success;
  }

  function copyTextToClipboard(text, successMessage, failureMessage) {
    if (!text) {
      return false;
    }
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(() => {
        updateStatus(successMessage);
      }).catch(() => {
        const success = fallbackCopyText(text);
        updateStatus(success ? successMessage : (failureMessage || 'Copy failed.'));
      });
      return true;
    }
    const success = fallbackCopyText(text);
    updateStatus(success ? successMessage : (failureMessage || 'Copy failed.'));
    return success;
  }

  function copySelection(textOverride) {
    const text = textOverride || term.getSelection();
    if (!text) {
      return;
    }
    copyTextToClipboard(text, 'Selection copied.');
  }

  function fallbackCopyText(text) {
    const helper = document.createElement('textarea');
    helper.value = text;
    helper.style.position = 'fixed';
    helper.style.opacity = '0';
    document.body.appendChild(helper);
    helper.focus();
    helper.select();
    let success = false;
    try {
      success = document.execCommand('copy');
    } catch (_) {
      success = false;
    }
    document.body.removeChild(helper);
    return success;
  }

  terminalEl.addEventListener('contextmenu', (event) => {
    const selection = term.getSelection();
    if (!selection) {
      return;
    }
    event.preventDefault();
    copySelection();
  });

  document.addEventListener('keydown', (event) => {
    if (!terminalFocused) {
      return;
    }
    if (event.ctrlKey && !event.shiftKey && !event.altKey && event.key.toLowerCase() === 'c') {
      event.preventDefault();
      event.stopPropagation();
      sendBinary('\x03');
    }
  });

  function handleKeybarAction(key, selectionSnapshot) {
    if (!key) {
      return;
    }
    switch (key) {
      case 'esc':
        sendBinary('\x1b');
        break;
      case 'tab':
        sendBinary('\t');
        break;
      case 'up':
        sendBinary('\x1b[A');
        break;
      case 'down':
        sendBinary('\x1b[B');
        break;
      case 'left':
        sendBinary('\x1b[D');
        break;
      case 'right':
        sendBinary('\x1b[C');
        break;
      case 'ctrlc':
        sendBinary('\x03');
        break;
      case 'copy': {
        const selection = typeof selectionSnapshot === 'string' ? selectionSnapshot : pendingKeybarCopy;
        copySelection(selection);
        pendingKeybarCopy = '';
        break;
      }
      case 'paste':
        requestPaste();
        break;
      case 'ctrlz':
        sendBinary('\x1a');
        break;
      case 'ctrly':
        sendBinary('\x19');
        break;
      case 'enter':
        sendBinary('\r');
        break;
      case 'shift-enter':
        sendBinary('\n');
        break;
      case 'backspace':
        sendBinary('\x7f');
        break;
      case 'clear':
        term.clear();
        term.scrollToBottom();
        term.focus();
        break;
      case 'reset':
        openConfirmDialog({
          title: 'Reset shell',
          message: 'This will terminate the current shell and any running processes, then start a fresh session. Do you want to continue?',
          confirmLabel: 'Reset',
          onConfirm: () => {
            sendReset();
          },
          onCancel: () => {
            updateStatus('Reset cancelled.');
          }
        });
        break;
      case 'md-toggle':
        toggleMdMenu();
        break;
      case 'md-h1':
        insertSnippet('# ');
        break;
      case 'md-h2':
        insertSnippet('## ');
        break;
      case 'md-inline-code':
        insertSnippet('`');
        break;
      case 'md-codeblock':
        insertSnippet('```');
        break;
      default:
        break;
    }
  }

  keybar.addEventListener('pointerdown', (event) => {
    const button = event.target.closest('button');
    const key = button && button.dataset.key;
    if (!key) {
      pendingKeybarCopy = '';
      return;
    }
    const isTouchPointer = !event.pointerType || event.pointerType === 'touch' || event.pointerType === 'pen';
    if (useVisualViewport && isTouchPointer) {
      event.preventDefault();
      event.stopPropagation();
      suppressKeybarClickUntil = Date.now() + 700;
      handleKeybarAction(key, key === 'copy' ? term.getSelection() : '');
      pendingKeybarCopy = '';
      return;
    }
    if (key === 'copy') {
      pendingKeybarCopy = term.getSelection();
    } else {
      pendingKeybarCopy = '';
    }
  }, { passive: false });

  keybar.addEventListener('click', (event) => {
    if (useVisualViewport && suppressKeybarClickUntil > Date.now()) {
      return;
    }
    const button = event.target.closest('button');
    if (!button) {
      return;
    }
    handleKeybarAction(button.dataset.key);
  });

  if (mdSubmenu) {
    setMdMenu(false);
  }

  if (useVisualViewport) {
    visualViewport.addEventListener('resize', () => scheduleResize(80));
    visualViewport.addEventListener('scroll', () => scheduleResize(80));
  }

  window.addEventListener('resize', () => scheduleResize(140));
  window.addEventListener('orientationchange', () => scheduleResize(180));

  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) {
      scheduleResize(180);
    }
  });

  window.addEventListener('pageshow', () => scheduleResize(180));

  connect();
})();

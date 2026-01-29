(() => {
  const statusEl = document.getElementById('status');
  const terminalEl = document.getElementById('terminal');
  const keybar = document.getElementById('keybar');
  const mdToggle = document.querySelector('[data-key="md-toggle"]');
  const mdSubmenu = document.getElementById('md-submenu');
  const modal = document.getElementById('confirm-modal');
  const confirmBtn = document.getElementById('confirm-exit');
  const cancelBtn = document.getElementById('cancel-exit');
  const pasteProxy = document.getElementById('paste-proxy');
  const root = document.documentElement;
  const visualViewport = window.visualViewport;
  const useVisualViewport = Boolean(
    visualViewport
      && ((window.matchMedia && window.matchMedia('(pointer: coarse)').matches)
        || (window.matchMedia && window.matchMedia('(hover: none)').matches)
        || (navigator && navigator.maxTouchPoints > 0))
  );
  let baseViewportHeight = window.innerHeight;

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

  const encoder = new TextEncoder();
  let socket;
  let lineBuffer = '';
  let pendingConfirm = null;
  let pendingCancel = null;
  let terminalFocused = false;
  let pendingKeybarCopy = '';
  let mdOpen = false;

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

  let resizeTimer;
  let resizeRaf = 0;

  function applyViewportHeight() {
    const height = useVisualViewport ? visualViewport.height : window.innerHeight;
    if (!height || Number.isNaN(height)) {
      return;
    }
    updateBaseViewportHeight();
    root.style.setProperty('--viewport-height', `${Math.round(height)}px`);
    updateKeybarHeight();
    if (!useVisualViewport || Number.isNaN(baseViewportHeight)) {
      root.style.setProperty('--keyboard-offset', '0px');
      return;
    }
    const offset = Math.max(0, baseViewportHeight - visualViewport.height - visualViewport.offsetTop);
    root.style.setProperty('--keyboard-offset', `${Math.round(offset)}px`);
  }

  function updateKeybarHeight() {
    if (!keybar) {
      return;
    }
    const rect = keybar.getBoundingClientRect();
    if (!rect || Number.isNaN(rect.height)) {
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

  function confirmExit(onConfirm, onCancel) {
    if (pendingConfirm) {
      return;
    }
    pendingConfirm = onConfirm;
    pendingCancel = onCancel;
    modal.classList.remove('hidden');
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

  function copySelection(textOverride) {
    const text = textOverride || term.getSelection();
    if (!text) {
      return;
    }
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(() => {
        updateStatus('Selection copied.');
      }).catch(() => {
        fallbackCopy(text);
      });
      return;
    }
    fallbackCopy(text);
  }

  function fallbackCopy(text) {
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
    updateStatus(success ? 'Selection copied.' : 'Copy failed.');
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

  keybar.addEventListener('pointerdown', (event) => {
    const button = event.target.closest('button');
    if (!button || button.dataset.key !== 'copy') {
      pendingKeybarCopy = '';
      return;
    }
    pendingKeybarCopy = term.getSelection();
  });

  keybar.addEventListener('click', (event) => {
    const button = event.target.closest('button');
    if (!button) {
      return;
    }
    const key = button.dataset.key;
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
      case 'copy':
        copySelection(pendingKeybarCopy);
        pendingKeybarCopy = '';
        break;
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

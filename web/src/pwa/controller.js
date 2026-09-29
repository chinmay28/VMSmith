// Page-side PWA controller: service-worker registration, the "update
// available" handshake, install-prompt capture, and online/offline state.
//
// All browser globals are injected (`win`, `nav`) so the controller can be
// driven by fakes in web/tests/pwa-controller.test.js. The React surface is
// src/pwa/usePwa.js + src/components/Pwa.jsx; this module has no React
// dependency.
//
// State shape (immutable snapshots, safe for useSyncExternalStore):
//   supported    service workers are available in a secure context
//   registered   registration succeeded
//   online       navigator.onLine, tracked via online/offline events
//   updateReady  a new service worker is installed and waiting
//   installable  the browser offered an install prompt we are holding
//   standalone   running as an installed app (display-mode: standalone)

export const DEFAULT_UPDATE_INTERVAL_MS = 60 * 60 * 1000;

export function createPwaController({
  win,
  nav,
  swUrl = '/sw.js',
  updateIntervalMs = DEFAULT_UPDATE_INTERVAL_MS,
} = {}) {
  const supported = Boolean(win && nav && nav.serviceWorker && win.isSecureContext !== false);

  let state = {
    supported,
    registered: false,
    online: nav ? nav.onLine !== false : true,
    updateReady: false,
    installable: false,
    standalone: detectStandalone(win, nav),
  };
  const listeners = new Set();
  let registration = null;
  let deferredPrompt = null;
  let reloading = false;
  let started = false;

  function setState(patch) {
    const next = { ...state, ...patch };
    if (Object.keys(patch).every((k) => next[k] === state[k])) return;
    state = next;
    for (const listener of listeners) listener();
  }

  function trackWaiting(reg) {
    // A waiting worker only counts as an *update* when a previous worker
    // already controls the page; on first install there is nothing to swap.
    if (reg.waiting && nav.serviceWorker.controller) setState({ updateReady: true });
  }

  function watchInstalling(reg) {
    const worker = reg.installing;
    if (!worker) return;
    worker.addEventListener('statechange', () => {
      if (worker.state === 'installed') trackWaiting(reg);
    });
  }

  function listenWindow() {
    if (!win) return;
    win.addEventListener('online', () => setState({ online: true }));
    win.addEventListener('offline', () => setState({ online: false }));
    win.addEventListener('beforeinstallprompt', (event) => {
      event.preventDefault();
      deferredPrompt = event;
      setState({ installable: true });
    });
    win.addEventListener('appinstalled', () => {
      deferredPrompt = null;
      setState({ installable: false, standalone: true });
    });
  }

  return {
    getState: () => state,

    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },

    // start wires window listeners and (when supported) registers the
    // service worker. Idempotent. Resolves to the registration or null.
    async start() {
      if (started) return registration;
      started = true;
      listenWindow();
      if (!supported) return null;

      nav.serviceWorker.addEventListener('controllerchange', () => {
        // Fired after applyUpdate() activates the new worker. Reload once so
        // the page runs the matching bundle; guard against reload loops.
        if (reloading || !state.updateReady) return;
        reloading = true;
        win.location.reload();
      });

      try {
        registration = await nav.serviceWorker.register(swUrl, { scope: '/' });
      } catch (err) {
        // A failed registration only costs offline support; the GUI keeps
        // working as a plain web app.
        return null;
      }
      setState({ registered: true });
      trackWaiting(registration);
      watchInstalling(registration);
      registration.addEventListener('updatefound', () => watchInstalling(registration));

      if (updateIntervalMs > 0 && typeof win.setInterval === 'function') {
        win.setInterval(() => { registration.update().catch(() => undefined); }, updateIntervalMs);
      }
      return registration;
    },

    // applyUpdate asks the waiting worker to activate; the controllerchange
    // listener above then reloads the page. Returns false when no update is
    // pending.
    applyUpdate() {
      const waiting = registration && registration.waiting;
      if (!waiting) return false;
      waiting.postMessage({ type: 'SKIP_WAITING' });
      return true;
    },

    dismissUpdate() {
      setState({ updateReady: false });
    },

    // promptInstall shows the browser's install dialog captured from
    // beforeinstallprompt. Resolves to 'accepted', 'dismissed', or
    // 'unavailable'.
    async promptInstall() {
      if (!deferredPrompt) return 'unavailable';
      const prompt = deferredPrompt;
      deferredPrompt = null;
      setState({ installable: false });
      await prompt.prompt();
      const choice = await prompt.userChoice;
      return choice && choice.outcome === 'accepted' ? 'accepted' : 'dismissed';
    },
  };
}

export function detectStandalone(win, nav) {
  if (nav && nav.standalone === true) return true; // iOS Safari
  if (!win || typeof win.matchMedia !== 'function') return false;
  return win.matchMedia('(display-mode: standalone)').matches;
}

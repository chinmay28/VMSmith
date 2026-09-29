import { Download, RefreshCw, WifiOff, X } from 'lucide-react';
import { pwa, usePwa } from '../pwa';

// OfflineBanner is shown while the browser reports no connectivity. The
// app shell still renders from the service-worker cache, but every API call
// needs the daemon, so we say so instead of letting requests fail silently.
export function OfflineBanner() {
  const { online } = usePwa();
  if (online) return null;
  return (
    <div
      role="status"
      className="flex items-center gap-2 px-4 py-2 text-xs font-mono bg-amber-950/80 text-amber-200 border-b border-amber-800/60"
      data-testid="pwa-offline-banner"
    >
      <WifiOff size={14} className="shrink-0" />
      <span>Offline — showing the last loaded view. Actions resume when the VM Smith daemon is reachable.</span>
    </div>
  );
}

// UpdateToast appears when a new GUI build has been installed in the
// background (e.g. after the daemon was upgraded). Reload activates it.
export function UpdateToast() {
  const { updateReady } = usePwa();
  if (!updateReady) return null;
  return (
    <div
      role="alert"
      className="fixed z-50 right-4 left-4 sm:left-auto sm:w-96 card border-forge-700/60 shadow-2xl p-4 animate-slide-up"
      style={{ bottom: 'calc(1rem + env(safe-area-inset-bottom))' }}
      data-testid="pwa-update-toast"
    >
      <div className="flex items-start gap-3">
        <RefreshCw size={16} className="text-forge-400 mt-0.5 shrink-0" />
        <div className="flex-1 min-w-0">
          <p className="text-sm font-medium text-steel-100">A new version of VM Smith is available</p>
          <p className="text-xs text-steel-500 mt-0.5">Reload to switch. Open consoles will reconnect.</p>
          <div className="flex gap-2 mt-3">
            <button className="btn-primary text-xs px-3 py-1.5" onClick={() => pwa.applyUpdate()} data-testid="pwa-update-reload">
              Reload
            </button>
            <button className="btn-ghost text-xs" onClick={() => pwa.dismissUpdate()} data-testid="pwa-update-dismiss">
              Later
            </button>
          </div>
        </div>
        <button aria-label="Dismiss update notice" className="text-steel-500 hover:text-steel-300" onClick={() => pwa.dismissUpdate()}>
          <X size={16} />
        </button>
      </div>
    </div>
  );
}

// InstallAppButton surfaces the browser's install prompt when one is on
// offer (Chromium desktop / Android). iOS has no prompt API; docs/PWA.md
// covers "Share → Add to Home Screen".
export function InstallAppButton({ className = '' }) {
  const { installable, standalone } = usePwa();
  if (!installable || standalone) return null;
  return (
    <button
      type="button"
      className={`btn-secondary w-full justify-center text-xs py-1.5 ${className}`}
      onClick={() => pwa.promptInstall()}
      data-testid="pwa-install"
    >
      <Download size={14} />
      Install app
    </button>
  );
}

// AppInstallCard (Settings page) explains the install / offline status of
// this browser session — most importantly that plain-HTTP access from
// another machine cannot install the app, which otherwise fails silently.
export function AppInstallCard() {
  const { supported, registered, installable, standalone } = usePwa();

  let status;
  if (standalone) {
    status = 'Running as an installed app.';
  } else if (!supported) {
    status = 'Install and offline support need a secure origin. Serve the daemon over HTTPS '
      + '(daemon.tls in the config) or open it via localhost.';
  } else if (installable) {
    status = 'This browser can install VM Smith as an app.';
  } else {
    status = "Use your browser's Install app option, or Share → Add to Home Screen on iOS.";
  }

  return (
    <div className="card p-4 mb-6 flex flex-wrap items-center gap-3" data-testid="pwa-app-card">
      <div className="flex-1 min-w-[14rem]">
        <p className="text-sm font-medium text-steel-100">App</p>
        <p className="text-xs text-steel-500 mt-0.5" data-testid="pwa-app-status">{status}</p>
        {supported && (
          <p className="text-xs text-steel-600 mt-0.5" data-testid="pwa-offline-status">
            Offline app shell: {registered ? 'ready' : 'not registered'}
          </p>
        )}
      </div>
      <div className="w-full sm:w-40">
        <InstallAppButton />
      </div>
    </div>
  );
}

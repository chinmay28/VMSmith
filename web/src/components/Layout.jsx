import { useEffect, useState } from 'react';
import { NavLink, useLocation } from 'react-router-dom';
import {
  Activity, Clock, HardDrive, Layers, LayoutDashboard, Menu, Monitor, ScrollText, Server,
  Settings as SettingsIcon, X,
} from 'lucide-react';
import mascot from '../assets/mascot.png';
import { system } from '../api/client';
import { NAV_ITEMS, sectionTitle } from '../navigation';
import { InstallAppButton, OfflineBanner, UpdateToast } from './Pwa';

const ICONS = {
  Activity, Clock, HardDrive, Layers, LayoutDashboard, ScrollText, Server, Settings: SettingsIcon,
};

function Brand() {
  return (
    <div className="flex items-center gap-2.5">
      <div className="w-8 h-8 rounded-md bg-forge-900 border border-forge-700/60 flex items-center justify-center shadow-[0_0_10px_rgba(0,255,65,0.2)]">
        <Monitor size={16} className="text-forge-400" />
      </div>
      <div>
        <span className="font-mono font-bold text-forge-400 text-[24px] tracking-tight" style={{textShadow: '0 0 8px rgba(0,255,65,0.6)'}}>
          VM <span className="text-forge-300">Smith</span>
        </span>
        <p className="text-[11px] font-mono text-steel-600 -mt-0.5 whitespace-nowrap">Agent of the Virtual World</p>
      </div>
    </div>
  );
}

// Layout is the app chrome. At lg+ it is the fixed sidebar; below lg the
// same sidebar becomes a slide-over drawer behind a top bar so every page
// is usable on a phone or in the installed PWA window.
export default function Layout({ children }) {
  const [buildInfo, setBuildInfo] = useState(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const location = useLocation();

  useEffect(() => {
    let cancelled = false;
    system.version()
      .then(info => { if (!cancelled) setBuildInfo(info); })
      .catch(() => { /* footer falls back to a static label */ });
    return () => { cancelled = true; };
  }, []);

  // Navigating (link tap, back button) always closes the drawer.
  useEffect(() => { setDrawerOpen(false); }, [location.pathname]);

  useEffect(() => {
    if (!drawerOpen) return undefined;
    const onKey = (event) => { if (event.key === 'Escape') setDrawerOpen(false); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [drawerOpen]);

  const versionLabel = buildInfo?.version ? `VM Smith ${buildInfo.version}` : 'VM Smith';
  const versionTitle = buildInfo
    ? `commit ${buildInfo.commit} · built ${buildInfo.build_date} · ${buildInfo.go_version} ${buildInfo.os}/${buildInfo.arch}`
    : 'build info unavailable';

  return (
    <div className="flex h-screen h-[100dvh] overflow-hidden">
      {drawerOpen && (
        <div
          className="fixed inset-0 z-40 bg-black/60 backdrop-blur-sm lg:hidden"
          onClick={() => setDrawerOpen(false)}
          data-testid="nav-backdrop"
        />
      )}

      {/* Sidebar (drawer below lg) */}
      <aside
        className={`fixed inset-y-0 left-0 z-50 w-[268px] max-w-[85vw] shrink-0 border-r border-steel-800/60 bg-steel-950 lg:bg-steel-950/80
          flex flex-col overflow-y-auto safe-pt safe-pb transition-transform duration-200
          lg:static lg:z-auto lg:max-w-none lg:translate-x-0
          ${drawerOpen ? 'translate-x-0' : '-translate-x-full max-lg:invisible'}`}
        data-testid="app-sidebar"
      >
        {/* Logo */}
        <div className="px-5 py-5 border-b border-steel-800/40 flex items-start justify-between">
          <Brand />
          <button
            type="button"
            aria-label="Close navigation"
            className="lg:hidden text-steel-500 hover:text-steel-300 p-1 -mr-2"
            onClick={() => setDrawerOpen(false)}
            data-testid="nav-close"
          >
            <X size={18} />
          </button>
        </div>

        {/* Nav */}
        <nav className="flex-1 px-3 py-4 space-y-0.5">
          {NAV_ITEMS.map(({ to, icon, label, testId }) => {
            const Icon = ICONS[icon];
            return (
              <NavLink
                key={to}
                to={to}
                end={to === '/'}
                className={({ isActive }) =>
                  `flex items-center gap-3 px-3 py-2.5 lg:py-2 rounded-md text-sm font-medium transition-all duration-150 ${
                    isActive
                      ? 'bg-steel-800/70 text-forge-300 border-l-2 border-forge-500 -ml-px'
                      : 'text-steel-400 hover:text-steel-200 hover:bg-steel-800/40'
                  }`
                }
                data-testid={testId}
              >
                <Icon size={16} strokeWidth={1.8} />
                {label}
              </NavLink>
            );
          })}
        </nav>

        {/* Mascot */}
        <div className="px-3 pb-2 hidden sm:block">
          <img
            src={mascot}
            alt="V.M. Smith"
            className="w-full rounded-lg opacity-80 hover:opacity-100 transition-opacity duration-300"
          />
        </div>

        {/* Footer */}
        <div className="px-4 py-3 border-t border-steel-800/40 space-y-2">
          <InstallAppButton />
          <p className="text-[10px] font-mono text-forge-800" data-testid="layout-version" title={versionTitle}>
            {versionLabel}
          </p>
        </div>
      </aside>

      <div className="flex-1 flex flex-col min-w-0">
        {/* Mobile top bar */}
        <header className="lg:hidden shrink-0 flex items-center gap-3 px-4 min-h-14 border-b border-steel-800/60 bg-steel-950/90 backdrop-blur safe-pt">
          <button
            type="button"
            aria-label="Open navigation"
            aria-expanded={drawerOpen}
            className="text-steel-300 hover:text-forge-300 p-1 -ml-1"
            onClick={() => setDrawerOpen(true)}
            data-testid="nav-toggle"
          >
            <Menu size={20} />
          </button>
          <Monitor size={16} className="text-forge-400" />
          <span className="font-display font-semibold text-steel-100 truncate" data-testid="mobile-section-title">
            {sectionTitle(location.pathname)}
          </span>
        </header>

        <OfflineBanner />

        {/* Main content */}
        <main className="app-main flex-1 overflow-y-auto safe-pb">
          <div className="max-w-6xl mx-auto px-4 sm:px-6 py-4 sm:py-6">
            {children}
          </div>
        </main>
      </div>

      <UpdateToast />
    </div>
  );
}

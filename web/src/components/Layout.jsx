import { NavLink } from 'react-router-dom';
import { Server, HardDrive, LayoutDashboard, Monitor, ScrollText, Activity } from 'lucide-react';
import mascot from '../assets/mascot.png';

const navItems = [
  { to: '/',         icon: LayoutDashboard, label: 'Dashboard', testId: 'nav-dashboard' },
  { to: '/vms',      icon: Server,          label: 'Machines',  testId: 'nav-vms' },
  { to: '/images',   icon: HardDrive,       label: 'Images',    testId: 'nav-images' },
  { to: '/activity', icon: Activity,        label: 'Activity',  testId: 'nav-activity' },
  { to: '/logs',     icon: ScrollText,      label: 'Logs',      testId: 'nav-logs' },
];

export default function Layout({ children }) {
  return (
    <div className="flex h-screen overflow-hidden">
      {/* Sidebar */}
      <aside className="w-[268px] shrink-0 border-r border-steel-800/60 bg-steel-950/80 flex flex-col">
        {/* Logo */}
        <div className="px-5 py-5 border-b border-steel-800/40">
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
        </div>

        {/* Nav */}
        <nav className="flex-1 px-3 py-4 space-y-0.5">
          {navItems.map(({ to, icon: Icon, label, testId }) => (
            <NavLink
              key={to}
              to={to}
              end={to === '/'}
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium transition-all duration-150 ${
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
          ))}
        </nav>

        {/* Mascot */}
        <div className="px-3 pb-2">
          <img
            src={mascot}
            alt="V.M. Smith"
            className="w-full rounded-lg opacity-80 hover:opacity-100 transition-opacity duration-300"
          />
        </div>

        {/* Footer */}
        <div className="px-4 py-3 border-t border-steel-800/40">
          <p className="text-[10px] font-mono text-forge-800">VM Smith v0.1.0-dev</p>
        </div>
      </aside>

      {/* Main content */}
      <main className="flex-1 overflow-y-auto">
        <div className="max-w-6xl mx-auto px-6 py-6">
          {children}
        </div>
      </main>
    </div>
  );
}

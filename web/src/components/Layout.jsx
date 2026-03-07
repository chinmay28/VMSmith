import { NavLink } from 'react-router-dom';
import { Server, HardDrive, Camera, Network, LayoutDashboard, Anvil } from 'lucide-react';

const navItems = [
  { to: '/',          icon: LayoutDashboard, label: 'Dashboard' },
  { to: '/vms',       icon: Server,          label: 'Machines' },
  { to: '/images',    icon: HardDrive,       label: 'Images' },
];

export default function Layout({ children }) {
  return (
    <div className="flex h-screen overflow-hidden">
      {/* Sidebar */}
      <aside className="w-56 shrink-0 border-r border-steel-800/60 bg-steel-950/80 flex flex-col">
        {/* Logo */}
        <div className="px-5 py-5 border-b border-steel-800/40">
          <div className="flex items-center gap-2.5">
            <div className="w-8 h-8 rounded-md bg-gradient-to-br from-forge-500 to-forge-700 flex items-center justify-center">
              <Anvil size={16} className="text-white" />
            </div>
            <div>
              <span className="font-display font-bold text-steel-100 text-base tracking-tight">
                vm<span className="text-forge-400">Smith</span>
              </span>
              <p className="text-[10px] font-mono text-steel-600 -mt-0.5">vm orchestrator</p>
            </div>
          </div>
        </div>

        {/* Nav */}
        <nav className="flex-1 px-3 py-4 space-y-0.5">
          {navItems.map(({ to, icon: Icon, label }) => (
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
            >
              <Icon size={16} strokeWidth={1.8} />
              {label}
            </NavLink>
          ))}
        </nav>

        {/* Footer */}
        <div className="px-4 py-3 border-t border-steel-800/40">
          <p className="text-[10px] font-mono text-steel-700">vmsmith v0.1.0-dev</p>
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

// Single source of truth for the top-level GUI sections. Layout.jsx renders
// the sidebar / mobile drawer from this list, and web/tests/pwa-manifest.test.js
// checks that every manifest shortcut points at one of these routes, so a new
// section added here is reachable on desktop, mobile, and the installed app.
// `icon` names a lucide-react export (mapped in Layout.jsx).
export const NAV_ITEMS = [
  { to: '/',          icon: 'LayoutDashboard', label: 'Dashboard', testId: 'nav-dashboard' },
  { to: '/vms',       icon: 'Server',          label: 'Machines',  testId: 'nav-vms' },
  { to: '/images',    icon: 'HardDrive',       label: 'Images',    testId: 'nav-images' },
  { to: '/templates', icon: 'Layers',          label: 'Templates', testId: 'nav-templates' },
  { to: '/schedules', icon: 'Clock',           label: 'Schedules', testId: 'nav-schedules' },
  { to: '/activity',  icon: 'Activity',        label: 'Activity',  testId: 'nav-activity' },
  { to: '/logs',      icon: 'ScrollText',      label: 'Logs',      testId: 'nav-logs' },
  { to: '/settings',  icon: 'Settings',        label: 'Settings',  testId: 'nav-settings' },
];

// sectionTitle returns the label of the section owning `pathname`
// (longest matching prefix), used as the mobile top-bar title.
export function sectionTitle(pathname) {
  const match = NAV_ITEMS
    .filter((item) => item.to !== '/' && (pathname === item.to || pathname.startsWith(`${item.to}/`)))
    .sort((a, b) => b.to.length - a.to.length)[0];
  return (match || NAV_ITEMS[0]).label;
}

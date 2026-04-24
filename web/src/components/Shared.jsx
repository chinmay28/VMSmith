import { X, Inbox } from 'lucide-react';

// --- Status Badge ---
const stateStyles = {
  running:  'badge-running',
  stopped:  'badge-stopped',
  creating: 'badge-creating',
  unknown:  'badge-error',
  deleted:  'badge-error',
};

export function StatusBadge({ state }) {
  return (
    <span className={stateStyles[state] || 'badge-stopped'}>
      {state === 'running' && (
        <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 mr-1.5 animate-pulse-slow" />
      )}
      {state}
    </span>
  );
}

// --- Page Header ---
export function PageHeader({ title, subtitle, actions }) {
  return (
    <div className="flex items-start justify-between mb-6">
      <div>
        <h1 className="font-display font-bold text-2xl text-steel-100 tracking-tight">{title}</h1>
        {subtitle && <p className="text-sm text-steel-500 mt-0.5">{subtitle}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}

// --- Modal ---
export function Modal({ open, onClose, title, children, wide }) {
  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className={`relative card border-steel-700/60 shadow-2xl p-0 animate-slide-up ${wide ? 'w-full max-w-2xl' : 'w-full max-w-md'}`}>
        <div className="flex items-center justify-between px-5 py-3.5 border-b border-steel-800/60">
          <h2 className="font-display font-semibold text-steel-100">{title}</h2>
          <button onClick={onClose} className="text-steel-500 hover:text-steel-300 transition-colors">
            <X size={18} />
          </button>
        </div>
        <div className="px-5 py-4">
          {children}
        </div>
      </div>
    </div>
  );
}

// --- Empty State ---
export function EmptyState({ icon: Icon = Inbox, title, description, action }) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center animate-fade-in">
      <div className="w-12 h-12 rounded-xl bg-steel-800/60 border border-steel-700/40 flex items-center justify-center mb-4">
        <Icon size={22} className="text-steel-500" />
      </div>
      <h3 className="font-display font-semibold text-steel-300 mb-1">{title}</h3>
      {description && <p className="text-sm text-steel-500 max-w-xs">{description}</p>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

// --- Loading Spinner ---
export function Spinner({ size = 16 }) {
  return (
    <svg className="animate-spin text-forge-400" width={size} height={size} viewBox="0 0 24 24" fill="none">
      <circle cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeDasharray="30 70" />
    </svg>
  );
}

// --- Stat Card ---
export function StatCard({ label, value, icon: Icon, accent, testId }) {
  return (
    <div className="card-hover px-4 py-3.5">
      <div className="flex items-center justify-between mb-2">
        <span className="text-[10px] font-mono uppercase tracking-[0.15em] text-steel-500">{label}</span>
        {Icon && <Icon size={14} className={accent ? 'text-forge-400' : 'text-steel-600'} />}
      </div>
      <p className="font-display font-bold text-2xl text-steel-100" data-testid={testId}>{value}</p>
    </div>
  );
}

// --- Error Banner ---
export function ErrorBanner({ message, onRetry }) {
  return (
    <div className="card border-red-800/40 bg-red-950/30 px-4 py-3 flex items-center justify-between">
      <p className="text-sm text-red-300">{message}</p>
      {onRetry && (
        <button onClick={onRetry} className="btn-ghost text-red-300 hover:text-red-200">
          Retry
        </button>
      )}
    </div>
  );
}

export function PaginationControls({
  page,
  perPage,
  total,
  perPageOptions = [10, 25, 50, 100],
  itemLabel = 'items',
  onPageChange,
  onPerPageChange,
}) {
  const safeTotal = Number.isFinite(total) ? total : 0;
  const currentPage = Math.max(1, page || 1);
  const currentPerPage = Math.max(1, perPage || perPageOptions[0] || 25);
  const totalPages = Math.max(1, Math.ceil(safeTotal / currentPerPage));
  const start = safeTotal === 0 ? 0 : (currentPage - 1) * currentPerPage + 1;
  const end = safeTotal === 0 ? 0 : Math.min(safeTotal, currentPage * currentPerPage);

  return (
    <div className="mt-4 flex flex-col gap-3 text-sm text-steel-400 md:flex-row md:items-center md:justify-between">
      <div>
        Showing <span className="text-steel-200">{start}-{end}</span> of <span className="text-steel-200">{safeTotal}</span> {itemLabel}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <label className="flex items-center gap-2">
          <span className="text-xs uppercase tracking-wide text-steel-500">Per page</span>
          <select
            className="input py-1 text-xs w-20"
            value={currentPerPage}
            onChange={(e) => onPerPageChange?.(Number(e.target.value))}
          >
            {perPageOptions.map(option => (
              <option key={option} value={option}>{option}</option>
            ))}
          </select>
        </label>
        <button className="btn-secondary" disabled={currentPage <= 1} onClick={() => onPageChange?.(currentPage - 1)}>
          Previous
        </button>
        <span className="text-xs text-steel-500">Page {currentPage} / {totalPages}</span>
        <button className="btn-secondary" disabled={currentPage >= totalPages} onClick={() => onPageChange?.(currentPage + 1)}>
          Next
        </button>
      </div>
    </div>
  );
}

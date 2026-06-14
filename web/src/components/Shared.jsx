import { useState, useEffect } from 'react';
import { X, Inbox, Radio, RotateCcw, WifiOff, CircleDot, Power, SlidersHorizontal, ChevronDown, CheckCircle2, AlertTriangle, XCircle, Info } from 'lucide-react';
import { STATE_LIVE, STATE_RECONNECTING, STATE_FALLBACK, STATE_SHUTDOWN } from '../hooks/useEventStream.js';

// --- Status Badge ---
const stateStyles = {
  running:  'badge-running',
  stopped:  'badge-stopped',
  creating: 'badge-creating',
  paused:   'badge-paused',
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

// --- Severity Badge ---
const severityStyles = {
  error: 'badge-error',
  warn:  'px-1.5 py-0.5 rounded text-[10px] font-mono font-semibold bg-amber-900/40 text-amber-300 border border-amber-700/30',
  info:  'px-1.5 py-0.5 rounded text-[10px] font-mono font-semibold bg-blue-900/40 text-blue-300 border border-blue-700/30',
};

const severityDefault = 'px-1.5 py-0.5 rounded text-[10px] font-mono font-semibold bg-steel-800/60 text-steel-400 border border-steel-700/30';

export function SeverityBadge({ severity }) {
  const className = severityStyles[severity] || severityDefault;
  return <span className={className}>{severity || 'info'}</span>;
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

// --- Live Indicator ---
// Compact pill that surfaces the current useEventStream connection state so
// operators can tell when the page is actually live vs. polling or stale.
export function LiveIndicator({ status }) {
  let className = 'px-1.5 py-0.5 rounded text-[10px] font-mono font-semibold border inline-flex items-center gap-1';
  let label;
  let Icon;

  if (status === STATE_LIVE) {
    className += ' bg-emerald-900/40 text-emerald-300 border-emerald-700/30';
    label = 'live';
    Icon = Radio;
  } else if (status === STATE_RECONNECTING) {
    className += ' bg-amber-900/40 text-amber-300 border-amber-700/30';
    label = 'reconnecting';
    Icon = RotateCcw;
  } else if (status === STATE_FALLBACK) {
    className += ' bg-blue-900/40 text-blue-300 border-blue-700/30';
    label = 'polling';
    Icon = WifiOff;
  } else if (status === STATE_SHUTDOWN) {
    className += ' bg-red-950/50 text-red-300 border-red-800/40';
    label = 'shutdown';
    Icon = Power;
  } else {
    className += ' bg-steel-800/60 text-steel-400 border-steel-700/30';
    label = status || 'offline';
    Icon = CircleDot;
  }

  return (
    <span className={className} data-testid="live-indicator" data-status={status || 'closed'}>
      <Icon size={10} />
      {label}
    </span>
  );
}

// --- Progress Bar ---
// A determinate (value/max) or indeterminate capacity/operation meter. When
// `variant` is omitted the colour auto-escalates with utilisation so operators
// can spot pressure at a glance: green < 75% < amber < 90% < red.
const progressColors = {
  ok: 'bg-forge-500',
  warn: 'bg-amber-500',
  danger: 'bg-red-500',
  info: 'bg-blue-500',
  neutral: 'bg-steel-500',
};

export function ProgressBar({ value = 0, max = 100, variant, indeterminate = false, height = 'h-1.5', className = '', testId }) {
  const pct = max > 0 ? Math.min(100, Math.max(0, (value / max) * 100)) : 0;
  const auto = pct >= 90 ? 'danger' : pct >= 75 ? 'warn' : 'ok';
  const color = progressColors[variant || auto] || progressColors.ok;
  return (
    <div className={`progress-track ${height} ${className}`} data-testid={testId} role="progressbar" aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100}>
      {indeterminate ? (
        <div className="progress-indeterminate" />
      ) : (
        <div className={`progress-fill ${color}`} style={{ width: `${pct}%` }} />
      )}
    </div>
  );
}

// --- Usage Meter ---
// Card-friendly metric block: label + icon, a large primary readout, a
// utilisation bar, and a muted subtitle. Used for host / quota capacity cards.
export function UsageMeter({ label, icon: Icon, value, max, primary, subtitle, variant, showPercent = true, testId }) {
  const hasCap = Number.isFinite(max) && max > 0;
  const pct = hasCap ? Math.min(100, Math.max(0, (value / max) * 100)) : 0;
  return (
    <div className="card-hover px-4 py-3.5" data-testid={testId}>
      <div className="flex items-center justify-between mb-2">
        <span className="text-[10px] font-mono uppercase tracking-[0.15em] text-steel-500">{label}</span>
        {Icon && <Icon size={14} className="text-steel-600" />}
      </div>
      <div className="flex items-baseline justify-between gap-2">
        <p className="font-display font-bold text-2xl text-steel-100">{primary}</p>
        {hasCap && showPercent && (
          <span className="text-xs font-mono text-steel-500">{Math.round(pct)}%</span>
        )}
      </div>
      <ProgressBar value={value} max={hasCap ? max : 100} variant={hasCap ? variant : 'neutral'} className="mt-2.5" />
      {subtitle && <p className="text-xs text-steel-500 mt-1.5">{subtitle}</p>}
    </div>
  );
}

// --- Operation Progress ---
// Status indicator for long-running, opaque operations (image export, snapshot
// create/restore, clone). The backend runs these synchronously with no
// progress stream, so we show an honest indeterminate bar plus a live elapsed
// timer rather than a fake percentage. Render it while the mutation is in
// flight; pass `active={mutation.loading}`.
export function OperationProgress({ active, label, className = '', testId }) {
  const [elapsed, setElapsed] = useState(0);
  useEffect(() => {
    if (!active) return undefined;
    setElapsed(0);
    const start = Date.now();
    const id = setInterval(() => setElapsed(Math.round((Date.now() - start) / 1000)), 250);
    return () => clearInterval(id);
  }, [active]);
  if (!active) return null;
  return (
    <div className={`space-y-1.5 ${className}`} data-testid={testId} role="status" aria-live="polite">
      <div className="flex items-center justify-between text-xs text-steel-400">
        <span className="inline-flex items-center gap-2"><Spinner size={12} />{label}</span>
        <span className="font-mono text-steel-500">{elapsed}s elapsed</span>
      </div>
      <ProgressBar indeterminate />
    </div>
  );
}

// --- Status Banner ---
// Variant-aware inline banner for operation results. Replaces overloading
// ErrorBanner for success/partial messages (which mislabelled success as an
// error in red). `variant` ∈ success | warning | error | info.
const statusVariants = {
  success: { wrap: 'border-emerald-700/40 bg-emerald-950/30 text-emerald-200', icon: CheckCircle2 },
  warning: { wrap: 'border-amber-700/40 bg-amber-950/30 text-amber-200', icon: AlertTriangle },
  error:   { wrap: 'border-red-800/40 bg-red-950/30 text-red-300', icon: XCircle },
  info:    { wrap: 'border-blue-800/40 bg-blue-950/30 text-blue-200', icon: Info },
};

export function StatusBanner({ message, variant = 'info', onDismiss, testId }) {
  if (!message) return null;
  const cfg = statusVariants[variant] || statusVariants.info;
  const Icon = cfg.icon;
  return (
    <div className={`card flex items-center justify-between gap-3 px-4 py-3 ${cfg.wrap}`} data-testid={testId} role="status">
      <div className="flex items-center gap-2.5 min-w-0">
        <Icon size={16} className="shrink-0" />
        <p className="text-sm truncate">{message}</p>
      </div>
      {onDismiss && (
        <button onClick={onDismiss} className="shrink-0 opacity-70 hover:opacity-100 transition-opacity" aria-label="Dismiss">
          <X size={15} />
        </button>
      )}
    </div>
  );
}

// --- Filter Panel ---
// Collapsible container that tames a wide row of filter controls. Renders a
// compact header (icon + title + active-count chip + optional "Clear all")
// and a body that the operator can fold away. Defaults to open so the controls
// are reachable on first paint; collapse state is local (resets per load).
export function FilterPanel({ activeCount = 0, onClear, defaultOpen = true, children, testId, clearTestId }) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div className="card mb-4 overflow-hidden" data-testid={testId}>
      <div className="flex items-center justify-between px-4 py-2.5">
        <button
          type="button"
          className="flex items-center gap-2 text-sm font-medium text-steel-300 hover:text-steel-100 transition-colors"
          onClick={() => setOpen(o => !o)}
          aria-expanded={open}
          data-testid={testId ? `${testId}-toggle` : undefined}
        >
          <SlidersHorizontal size={14} className="text-steel-500" />
          Filters
          {activeCount > 0 && (
            <span className="badge bg-forge-900/60 text-forge-300 border border-forge-700/40">{activeCount} active</span>
          )}
          <ChevronDown size={14} className={`text-steel-500 transition-transform ${open ? 'rotate-180' : ''}`} />
        </button>
        {activeCount > 0 && onClear && (
          <button
            type="button"
            className="btn-ghost text-xs"
            onClick={onClear}
            data-testid={clearTestId || (testId ? `${testId}-clear-all` : undefined)}
          >
            <X size={12} /> Clear all
          </button>
        )}
      </div>
      {open && (
        <div className="px-4 pb-4 pt-1 border-t border-steel-800/40">
          {children}
        </div>
      )}
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

import { useState, useEffect, useCallback, useMemo } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { RefreshCw, Activity as ActivityIcon } from 'lucide-react';
import { events as eventsApi, vms as vmsApi } from '../api/client';
import { PageHeader, Spinner, ErrorBanner, EmptyState, PaginationControls, SeverityBadge } from '../components/Shared';

const DEFAULT_PER_PAGE = 50;
const POLL_INTERVAL_MS = 5000;

const sourceLabel = (source) => {
  switch (source) {
    case 'libvirt': return 'libvirt';
    case 'app':     return 'app';
    case 'system':  return 'system';
    default:        return source || '—';
  }
};

// Local-time formatter that survives missing/zero timestamps. Events shipped
// today on main carry CreatedAt (legacy); future ones will carry OccurredAt.
function formatEventTime(evt) {
  const ts = evt.occurred_at || evt.created_at;
  if (!ts) return '—';
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return '—';
  const date = d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
  const time = d.toLocaleTimeString('en-US', { hour12: false });
  return `${date} ${time}`;
}

export default function Activity({ vmId: vmIdProp = '', embedded = false } = {}) {
  // When embedded inside the VMDetail "Activity" tab, the URL search params
  // are not the user's intent; only the prop is. Otherwise the page is
  // fully filterable via the URL (deep-linkable).
  const [searchParams, setSearchParams] = useSearchParams();
  const useURL = !embedded;

  const initial = useURL
    ? {
        vmId:     searchParams.get('vm_id')   || '',
        type:     searchParams.get('type')    || '',
        source:   searchParams.get('source')  || '',
        severity: searchParams.get('severity') || '',
      }
    : { vmId: vmIdProp, type: '', source: '', severity: '' };

  const [vmFilter, setVmFilter] = useState(initial.vmId);
  const [typeFilter, setTypeFilter] = useState(initial.type);
  const [sourceFilter, setSourceFilter] = useState(initial.source);
  const [severityFilter, setSeverityFilter] = useState(initial.severity);
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState(DEFAULT_PER_PAGE);

  const [items, setItems] = useState([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  // VM lookup map so the timeline can show VM names instead of opaque IDs.
  const [vmIndex, setVmIndex] = useState({});

  // Sync URL when filters change (top-level page only).
  useEffect(() => {
    if (!useURL) return;
    const next = new URLSearchParams();
    if (vmFilter)       next.set('vm_id', vmFilter);
    if (typeFilter)     next.set('type', typeFilter);
    if (sourceFilter)   next.set('source', sourceFilter);
    if (severityFilter) next.set('severity', severityFilter);
    setSearchParams(next, { replace: true });
  }, [useURL, vmFilter, typeFilter, sourceFilter, severityFilter, setSearchParams]);

  // When the parent prop changes (different VM in the embedded tab), reset.
  useEffect(() => {
    if (embedded) {
      setVmFilter(vmIdProp);
      setPage(1);
    }
  }, [embedded, vmIdProp]);

  const fetchEvents = useCallback(async () => {
    try {
      const effectiveVm = embedded ? vmIdProp : vmFilter;
      const response = await eventsApi.list({
        vmId: effectiveVm,
        type: typeFilter,
        source: sourceFilter,
        severity: severityFilter,
        page,
        perPage,
      });
      const data = response?.data || [];
      setItems(Array.isArray(data) ? data : []);
      setTotal(response?.meta?.totalCount ?? data.length ?? 0);
      setError(null);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, [embedded, vmIdProp, vmFilter, typeFilter, sourceFilter, severityFilter, page, perPage]);

  useEffect(() => {
    setLoading(true);
    fetchEvents();
    const id = setInterval(fetchEvents, POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [fetchEvents]);

  // Reset to page 1 when filters change.
  useEffect(() => {
    setPage(1);
  }, [vmFilter, typeFilter, sourceFilter, severityFilter]);

  // Lazily build a VM ID → name map so the timeline can render names.
  // Only top-level Activity needs this; the embedded tab already knows the VM.
  useEffect(() => {
    if (embedded) return;
    let cancelled = false;
    (async () => {
      try {
        const response = await vmsApi.list({ perPage: 200 });
        const vms = response?.data || [];
        if (cancelled) return;
        const map = {};
        for (const vm of vms) map[vm.id] = vm.name;
        setVmIndex(map);
      } catch {
        // Non-fatal: the timeline will just show IDs.
      }
    })();
    return () => { cancelled = true; };
  }, [embedded]);

  const distinctTypes = useMemo(() => {
    const set = new Set(items.map(e => e.type).filter(Boolean));
    return Array.from(set).sort();
  }, [items]);

  const subtitle = embedded
    ? `${total} ${total === 1 ? 'event' : 'events'} for this VM`
    : `${total} ${total === 1 ? 'event' : 'events'}`;

  return (
    <div className="flex flex-col h-full">
      {!embedded && (
        <PageHeader
          title="Activity"
          subtitle={subtitle}
          actions={
            <button className="btn-ghost" onClick={fetchEvents} title="Refresh" data-testid="btn-activity-refresh">
              {loading ? <Spinner size={14} /> : <RefreshCw size={14} />}
            </button>
          }
        />
      )}

      {!embedded && (
        <div className="flex flex-wrap items-center gap-2 mb-4">
          <input
            className="input py-1 text-xs w-44"
            placeholder="Filter by VM ID"
            value={vmFilter}
            onChange={e => setVmFilter(e.target.value.trim())}
            data-testid="activity-filter-vm"
          />
          <select
            className="input py-1 text-xs w-32"
            value={sourceFilter}
            onChange={e => setSourceFilter(e.target.value)}
            data-testid="activity-filter-source"
          >
            <option value="">All sources</option>
            <option value="libvirt">libvirt</option>
            <option value="app">app</option>
            <option value="system">system</option>
          </select>
          <select
            className="input py-1 text-xs w-32"
            value={severityFilter}
            onChange={e => setSeverityFilter(e.target.value)}
            data-testid="activity-filter-severity"
          >
            <option value="">All severities</option>
            <option value="info">info</option>
            <option value="warn">warn</option>
            <option value="error">error</option>
          </select>
          <select
            className="input py-1 text-xs w-44"
            value={typeFilter}
            onChange={e => setTypeFilter(e.target.value)}
            data-testid="activity-filter-type"
          >
            <option value="">All types</option>
            {distinctTypes.map(t => <option key={t} value={t}>{t}</option>)}
          </select>
          {(vmFilter || typeFilter || sourceFilter || severityFilter) && (
            <button
              className="btn-ghost text-xs text-steel-400"
              onClick={() => { setVmFilter(''); setTypeFilter(''); setSourceFilter(''); setSeverityFilter(''); }}
              data-testid="btn-activity-clear-filters"
            >
              Clear
            </button>
          )}
        </div>
      )}

      {error && <div className="mb-3"><ErrorBanner message={error} onRetry={fetchEvents} /></div>}

      <div
        className="flex-1 overflow-y-auto rounded-lg border border-steel-800/60 bg-steel-950/60 font-mono text-xs"
        style={{ minHeight: 0 }}
        data-testid="activity-table"
      >
        {loading && items.length === 0 ? (
          <div className="flex justify-center py-20"><Spinner size={18} /></div>
        ) : items.length === 0 ? (
          <EmptyState
            icon={ActivityIcon}
            title="No events yet"
            description={embedded ? 'No lifecycle events for this VM.' : 'Lifecycle and system events will appear here as they happen.'}
          />
        ) : (
          <table className="w-full border-collapse">
            <thead className="sticky top-0 z-10 bg-steel-900/95 border-b border-steel-800/60">
              <tr>
                <th className="text-left px-3 py-2 text-[10px] uppercase tracking-widest text-steel-500 font-semibold w-44">Time</th>
                <th className="text-left px-3 py-2 text-[10px] uppercase tracking-widest text-steel-500 font-semibold w-20">Severity</th>
                <th className="text-left px-3 py-2 text-[10px] uppercase tracking-widest text-steel-500 font-semibold w-24">Source</th>
                <th className="text-left px-3 py-2 text-[10px] uppercase tracking-widest text-steel-500 font-semibold w-44">Type</th>
                {!embedded && (
                  <th className="text-left px-3 py-2 text-[10px] uppercase tracking-widest text-steel-500 font-semibold w-44">VM</th>
                )}
                <th className="text-left px-3 py-2 text-[10px] uppercase tracking-widest text-steel-500 font-semibold">Message</th>
              </tr>
            </thead>
            <tbody>
              {items.map((evt) => (
                <tr
                  key={evt.id}
                  className="border-b border-steel-800/30 hover:bg-steel-800/20"
                  data-testid={`activity-row-${evt.id}`}
                >
                  <td className="px-3 py-1.5 text-steel-500 whitespace-nowrap">{formatEventTime(evt)}</td>
                  <td className="px-3 py-1.5">
                    <SeverityBadge severity={evt.severity} />
                  </td>
                  <td className="px-3 py-1.5 text-steel-300">{sourceLabel(evt.source)}</td>
                  <td className="px-3 py-1.5 text-forge-300">{evt.type}</td>
                  {!embedded && (
                    <td className="px-3 py-1.5">
                      {evt.vm_id ? (
                        <Link to={`/vms/${evt.vm_id}`} className="text-forge-400 hover:underline">
                          {vmIndex[evt.vm_id] || evt.vm_id}
                        </Link>
                      ) : (
                        <span className="text-steel-600">—</span>
                      )}
                    </td>
                  )}
                  <td className="px-3 py-1.5 text-steel-200">{evt.message || ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {total > 0 && (
        <PaginationControls
          page={page}
          perPage={perPage}
          total={total}
          perPageOptions={[25, 50, 100, 200]}
          itemLabel="events"
          onPageChange={setPage}
          onPerPageChange={(value) => {
            setPerPage(value);
            setPage(1);
          }}
        />
      )}
    </div>
  );
}

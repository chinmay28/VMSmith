import { useEffect, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Clock, Plus, Pencil, Trash2, Play, Search, X,
  ChevronRight, ChevronDown, CheckCircle2, AlertCircle, MinusCircle, Loader2,
} from 'lucide-react';
import { schedules as schedulesApi } from '../api/client';
import { useFetch, useMutation } from '../hooks/useFetch';
import { PageHeader, EmptyState, Spinner, ErrorBanner, Modal, PaginationControls } from '../components/Shared';

const DEFAULT_SCHEDULE_PER_PAGE = 25;

const ACTIONS = ['snapshot', 'start', 'stop', 'restart'];
const CATCH_UP_POLICIES = ['skip', 'run_once', 'run_all'];

// Cron preset helper chips — clicking one fills the cron_spec field with a
// 6-field (WITH seconds) expression. Mirrors roadmap item 5.2.9.
const CRON_PRESETS = [
  { label: 'Hourly', value: '0 0 * * * *', testId: 'cron-preset-hourly' },
  { label: 'Daily 02:00', value: '0 0 2 * * *', testId: 'cron-preset-daily' },
  { label: 'Weekly Sun 03:00', value: '0 0 3 * * 0', testId: 'cron-preset-weekly' },
];

function formatTime(value) {
  if (!value) return '—';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return '—';
  return d.toLocaleString();
}

// `<input type="datetime-local">` returns a naive local-time string
// (`YYYY-MM-DDTHH:MM`). Convert to RFC3339 in UTC so the daemon's
// `parseTimeRangeParam` accepts it. Empty / invalid input → empty string
// so the API client drops the param.
function datetimeLocalToISO(value) {
  if (!value) return '';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return '';
  return d.toISOString();
}

// targetLabel renders a schedule's resolved target: an explicit vm_id, a
// "tag:<a,b>" selector, or "all" when neither is set.
function targetLabel(schedule) {
  if (schedule.vm_id) return schedule.vm_id;
  if (schedule.tag_selector?.length) return `tag:${schedule.tag_selector.join(',')}`;
  return 'all';
}

export default function Schedules() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [showAdd, setShowAdd] = useState(searchParams.get('open') === 'create');
  const [editing, setEditing] = useState(null);
  const prefillVmId = searchParams.get('prefill_vm_id') || '';
  const prefillName = searchParams.get('prefill_name') || '';
  const prefillSchedule = useMemo(
    () => ({ vm_id: prefillVmId, name: prefillName }),
    [prefillVmId, prefillName],
  );

  // Free-text search across name/action/vm_id/tag_selector. `searchInput` is
  // the live value; `searchFilter` is the debounced value that drives the
  // request loop. Mirrors the Settings / TemplateList debounce pattern.
  const [searchInput, setSearchInput] = useState(searchParams.get('search') || '');
  const [searchFilter, setSearchFilter] = useState(searchParams.get('search') || '');

  // Exact tag-selector membership filter (case-insensitive). Debounced like
  // `search`; round-trips through `?tag_selector=`. The symmetric counterpart
  // to filtering by a single `vm_id` for tag-selector-targeted schedules.
  const [tagSelectorInput, setTagSelectorInput] = useState(searchParams.get('tag_selector') || '');
  const [tagSelectorFilter, setTagSelectorFilter] = useState(searchParams.get('tag_selector') || '');

  // Name-prefix filter (5.4.82): case-sensitive HasPrefix on schedule name.
  // Debounced like `search`; round-trips through `?prefix=`. The fifth and
  // final member of the cohort-discrimination name-prefix family alongside
  // snapshots / VMs / images / templates.
  const [prefixInput, setPrefixInput] = useState(searchParams.get('prefix') || '');
  const [prefixFilter, setPrefixFilter] = useState(searchParams.get('prefix') || '');

  const VALID_ACTIONS = ['', ...ACTIONS];
  const initialAction = (() => {
    const raw = (searchParams.get('action') || '').toLowerCase();
    return VALID_ACTIONS.includes(raw) ? raw : '';
  })();
  const [actionFilter, setActionFilter] = useState(initialAction);

  const VALID_CATCH_UP = ['', ...CATCH_UP_POLICIES];
  const initialCatchUp = (() => {
    const raw = (searchParams.get('catch_up_policy') || '').toLowerCase();
    return VALID_CATCH_UP.includes(raw) ? raw : '';
  })();
  const [catchUpFilter, setCatchUpFilter] = useState(initialCatchUp);

  // Exact timezone filter (case-sensitive — IANA timezone names are
  // case-sensitive). Debounced like `search`; round-trips through `?timezone=`.
  const [timezoneInput, setTimezoneInput] = useState(searchParams.get('timezone') || '');
  const [timezoneFilter, setTimezoneFilter] = useState(searchParams.get('timezone') || '');

  const VALID_ENABLED = ['', 'true', 'false'];
  const initialEnabled = (() => {
    const raw = (searchParams.get('enabled') || '').toLowerCase();
    return VALID_ENABLED.includes(raw) ? raw : '';
  })();
  const [enabledFilter, setEnabledFilter] = useState(initialEnabled);

  const VALID_SORT_FIELDS = ['', 'id', 'name', 'created_at', 'next_fire_at'];
  const VALID_SORT_ORDERS = ['', 'asc', 'desc'];
  const initialSort = (() => {
    const raw = (searchParams.get('sort') || '').toLowerCase();
    return VALID_SORT_FIELDS.includes(raw) ? raw : '';
  })();
  const initialOrder = (() => {
    const raw = (searchParams.get('order') || '').toLowerCase();
    return VALID_SORT_ORDERS.includes(raw) ? raw : '';
  })();
  const [sortField, setSortField] = useState(initialSort);
  const [sortOrder, setSortOrder] = useState(initialOrder);

  const [sinceFilter, setSinceFilter] = useState(searchParams.get('since') || '');
  const [untilFilter, setUntilFilter] = useState(searchParams.get('until') || '');
  const sinceParam = useMemo(() => datetimeLocalToISO(sinceFilter), [sinceFilter]);
  const untilParam = useMemo(() => datetimeLocalToISO(untilFilter), [untilFilter]);

  // next_fire_at range filter (5.4.60): inclusive bounds on each schedule's
  // cron-computed next planned fire. Closes the operator query "what's about
  // to fire in the next N hours" — completing the symmetry with the existing
  // next_fire_at sort axis.
  const [nextFireSinceFilter, setNextFireSinceFilter] = useState(searchParams.get('next_fire_since') || '');
  const [nextFireUntilFilter, setNextFireUntilFilter] = useState(searchParams.get('next_fire_until') || '');
  const nextFireSinceParam = useMemo(() => datetimeLocalToISO(nextFireSinceFilter), [nextFireSinceFilter]);
  const nextFireUntilParam = useMemo(() => datetimeLocalToISO(nextFireUntilFilter), [nextFireUntilFilter]);

  // last_fired_at range filter (5.4.74): inclusive bounds on each schedule's
  // most-recent fire timestamp. Closes the SRE triage query "which schedules
  // fired during yesterday's maintenance window" / "which haven't fired since
  // the last daemon restart" — never-fired schedules are excluded whenever
  // either bound is set, mirroring the next-fire range nil-handling and the
  // webhook last_delivery range.
  const [lastFiredSinceFilter, setLastFiredSinceFilter] = useState(searchParams.get('last_fired_since') || '');
  const [lastFiredUntilFilter, setLastFiredUntilFilter] = useState(searchParams.get('last_fired_until') || '');
  const lastFiredSinceParam = useMemo(() => datetimeLocalToISO(lastFiredSinceFilter), [lastFiredSinceFilter]);
  const lastFiredUntilParam = useMemo(() => datetimeLocalToISO(lastFiredUntilFilter), [lastFiredUntilFilter]);

  const initialPage = (() => {
    const parsed = parseInt(searchParams.get('page') || '', 10);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : 1;
  })();
  const initialPerPage = (() => {
    const parsed = parseInt(searchParams.get('per_page') || '', 10);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : DEFAULT_SCHEDULE_PER_PAGE;
  })();
  const [page, setPage] = useState(initialPage);
  const [perPage, setPerPage] = useState(initialPerPage);

  useEffect(() => {
    const trimmed = searchInput.trim();
    const id = setTimeout(() => setSearchFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [searchInput]);

  useEffect(() => {
    const trimmed = tagSelectorInput.trim();
    const id = setTimeout(() => setTagSelectorFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [tagSelectorInput]);

  useEffect(() => {
    const trimmed = timezoneInput.trim();
    const id = setTimeout(() => setTimezoneFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [timezoneInput]);

  useEffect(() => {
    // No trim: the prefix filter is case-sensitive AND space-sensitive at the
    // request boundary, but the backend whitespace-trims so leading/trailing
    // spaces don't reach the predicate. Trim before debouncing to match.
    const trimmed = prefixInput.trim();
    const id = setTimeout(() => setPrefixFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [prefixInput]);

  // Reset to page 1 on any filter / sort change so the user doesn't land on
  // an empty page beyond the post-filter population.
  useEffect(() => {
    setPage(1);
  }, [searchFilter, tagSelectorFilter, actionFilter, catchUpFilter, timezoneFilter, enabledFilter, sinceParam, untilParam, nextFireSinceParam, nextFireUntilParam, lastFiredSinceParam, lastFiredUntilParam, prefixFilter, sortField, sortOrder]);

  useEffect(() => {
    const next = new URLSearchParams(searchParams);
    if (searchFilter) next.set('search', searchFilter); else next.delete('search');
    if (tagSelectorFilter) next.set('tag_selector', tagSelectorFilter); else next.delete('tag_selector');
    if (actionFilter) next.set('action', actionFilter); else next.delete('action');
    if (catchUpFilter) next.set('catch_up_policy', catchUpFilter); else next.delete('catch_up_policy');
    if (timezoneFilter) next.set('timezone', timezoneFilter); else next.delete('timezone');
    if (enabledFilter) next.set('enabled', enabledFilter); else next.delete('enabled');
    if (sinceFilter) next.set('since', sinceFilter); else next.delete('since');
    if (untilFilter) next.set('until', untilFilter); else next.delete('until');
    if (nextFireSinceFilter) next.set('next_fire_since', nextFireSinceFilter); else next.delete('next_fire_since');
    if (nextFireUntilFilter) next.set('next_fire_until', nextFireUntilFilter); else next.delete('next_fire_until');
    if (lastFiredSinceFilter) next.set('last_fired_since', lastFiredSinceFilter); else next.delete('last_fired_since');
    if (lastFiredUntilFilter) next.set('last_fired_until', lastFiredUntilFilter); else next.delete('last_fired_until');
    if (prefixFilter) next.set('prefix', prefixFilter); else next.delete('prefix');
    if (sortField) next.set('sort', sortField); else next.delete('sort');
    if (sortOrder) next.set('order', sortOrder); else next.delete('order');
    if (page > 1) next.set('page', String(page)); else next.delete('page');
    if (perPage !== DEFAULT_SCHEDULE_PER_PAGE) next.set('per_page', String(perPage)); else next.delete('per_page');
    setSearchParams(next, { replace: true });
  }, [searchFilter, tagSelectorFilter, actionFilter, catchUpFilter, timezoneFilter, enabledFilter, sinceFilter, untilFilter, nextFireSinceFilter, nextFireUntilFilter, lastFiredSinceFilter, lastFiredUntilFilter, prefixFilter, sortField, sortOrder, page, perPage]); // eslint-disable-line react-hooks/exhaustive-deps

  const { data: response, loading, error, refresh } = useFetch(
    () => schedulesApi.list({ search: searchFilter, tagSelector: tagSelectorFilter, action: actionFilter, catchUpPolicy: catchUpFilter, timezone: timezoneFilter, enabled: enabledFilter, since: sinceParam, until: untilParam, nextFireSince: nextFireSinceParam, nextFireUntil: nextFireUntilParam, lastFiredSince: lastFiredSinceParam, lastFiredUntil: lastFiredUntilParam, prefix: prefixFilter, sort: sortField, order: sortOrder, page, perPage }),
    [searchFilter, tagSelectorFilter, actionFilter, catchUpFilter, timezoneFilter, enabledFilter, sinceParam, untilParam, nextFireSinceParam, nextFireUntilParam, lastFiredSinceParam, lastFiredUntilParam, prefixFilter, sortField, sortOrder, page, perPage],
    15000,
  );
  const deleteMut = useMutation(schedulesApi.delete);
  const toggleMut = useMutation((id, enabled) => schedulesApi.update(id, { enabled }));
  const runNowMut = useMutation(schedulesApi.runNow);

  const items = response?.data || [];
  const total = response?.meta?.totalCount ?? items.length;

  const handleDelete = async (id, name) => {
    if (!window.confirm(`Delete schedule "${name}"?`)) return;
    await deleteMut.execute(id);
    refresh();
  };

  const handleToggle = async (schedule) => {
    await toggleMut.execute(schedule.id, !schedule.enabled);
    refresh();
  };

  const handleRunNow = async (id) => {
    await runNowMut.execute(id);
    refresh();
  };

  return (
    <div data-testid="schedules-page">
      <PageHeader
        title="Schedules"
        subtitle="Recurring VM operations — snapshots, start/stop, restart"
        actions={
          <button className="btn-primary" onClick={() => setShowAdd(true)} data-testid="add-schedule-btn">
            <Plus size={15} /> Add schedule
          </button>
        }
      />

      {error && <div className="mb-4"><ErrorBanner message={error} onRetry={refresh} /></div>}

      <AddScheduleModal
        open={showAdd}
        initialValues={prefillSchedule}
        onClose={() => {
          setShowAdd(false);
          const next = new URLSearchParams(searchParams);
          next.delete('open');
          next.delete('prefill_vm_id');
          next.delete('prefill_name');
          setSearchParams(next, { replace: true });
        }}
        onCreated={refresh}
      />
      <EditScheduleModal
        schedule={editing}
        open={editing !== null}
        onClose={() => setEditing(null)}
        onUpdated={refresh}
      />

      <div className="mb-4 flex items-center gap-2 flex-wrap">
        <div className="relative flex-1 max-w-md min-w-[200px]">
          <Search size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-steel-500 pointer-events-none" />
          <input
            type="search"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder="Search by name, action, VM, or tag…"
            className="input w-full pl-8 pr-8 py-1.5 text-sm"
            data-testid="schedule-list-search"
            aria-label="Search schedules"
          />
          {searchInput && (
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
              onClick={() => setSearchInput('')}
              data-testid="schedule-list-search-clear"
              aria-label="Clear search"
            >
              <X size={13} />
            </button>
          )}
        </div>
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          Tag selector
          <input
            type="search"
            value={tagSelectorInput}
            onChange={(e) => setTagSelectorInput(e.target.value)}
            placeholder="exact tag…"
            className="input py-1 text-xs w-32"
            data-testid="schedule-tag-selector-filter"
            aria-label="Filter by tag selector"
          />
        </label>
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          Name prefix
          <input
            type="search"
            value={prefixInput}
            onChange={(e) => setPrefixInput(e.target.value)}
            placeholder="nightly-, backup-…"
            className="input py-1 text-xs w-32"
            data-testid="schedule-list-prefix-filter"
            aria-label="Filter by schedule name prefix"
          />
        </label>
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          Action
          <select
            value={actionFilter}
            onChange={(e) => setActionFilter(e.target.value)}
            data-testid="schedule-action-filter"
            aria-label="Filter by action"
            className="input py-1 text-xs"
          >
            <option value="">All</option>
            {ACTIONS.map((a) => <option key={a} value={a}>{a}</option>)}
          </select>
        </label>
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          Catch-up
          <select
            value={catchUpFilter}
            onChange={(e) => setCatchUpFilter(e.target.value)}
            data-testid="schedule-catchup-filter"
            aria-label="Filter by catch-up policy"
            className="input py-1 text-xs"
          >
            <option value="">All</option>
            {CATCH_UP_POLICIES.map((p) => <option key={p} value={p}>{p}</option>)}
          </select>
        </label>
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          Timezone
          <input
            type="text"
            value={timezoneInput}
            onChange={(e) => setTimezoneInput(e.target.value)}
            placeholder="UTC, America/New_York…"
            className="input py-1 text-xs w-40"
            data-testid="schedule-timezone-filter"
            aria-label="Filter by timezone"
          />
        </label>
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          Enabled
          <select
            value={enabledFilter}
            onChange={(e) => setEnabledFilter(e.target.value)}
            data-testid="schedule-enabled-filter"
            aria-label="Filter by enabled"
            className="input py-1 text-xs"
          >
            <option value="">All</option>
            <option value="true">Enabled</option>
            <option value="false">Disabled</option>
          </select>
        </label>
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          Sort
          <select
            value={sortField}
            onChange={(e) => setSortField(e.target.value)}
            data-testid="schedule-list-sort-field"
            aria-label="Sort schedules by"
            className="input py-1 text-xs"
          >
            <option value="">Default (id)</option>
            <option value="id">id</option>
            <option value="name">name</option>
            <option value="created_at">created_at</option>
            <option value="next_fire_at">next_fire_at</option>
          </select>
        </label>
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          Order
          <select
            value={sortOrder}
            onChange={(e) => setSortOrder(e.target.value)}
            data-testid="schedule-list-sort-order"
            aria-label="Sort order"
            className="input py-1 text-xs"
          >
            <option value="">Default (asc)</option>
            <option value="asc">asc</option>
            <option value="desc">desc</option>
          </select>
        </label>
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          Created since
          <input
            type="datetime-local"
            value={sinceFilter}
            onChange={(e) => setSinceFilter(e.target.value)}
            className="input py-1 text-xs"
            data-testid="schedule-list-since-filter"
            aria-label="Filter by created since"
          />
        </label>
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          until
          <input
            type="datetime-local"
            value={untilFilter}
            onChange={(e) => setUntilFilter(e.target.value)}
            className="input py-1 text-xs"
            data-testid="schedule-list-until-filter"
            aria-label="Filter by created until"
          />
        </label>
        {(sinceFilter || untilFilter) && (
          <button
            type="button"
            className="btn-ghost text-xs"
            onClick={() => { setSinceFilter(''); setUntilFilter(''); }}
            data-testid="schedule-list-time-range-clear"
            aria-label="Clear created-at range"
          >
            Clear range
          </button>
        )}
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          Next fire since
          <input
            type="datetime-local"
            value={nextFireSinceFilter}
            onChange={(e) => setNextFireSinceFilter(e.target.value)}
            className="input py-1 text-xs"
            data-testid="schedule-list-next-fire-since-filter"
            aria-label="Filter by next-fire since"
          />
        </label>
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          until
          <input
            type="datetime-local"
            value={nextFireUntilFilter}
            onChange={(e) => setNextFireUntilFilter(e.target.value)}
            className="input py-1 text-xs"
            data-testid="schedule-list-next-fire-until-filter"
            aria-label="Filter by next-fire until"
          />
        </label>
        {(nextFireSinceFilter || nextFireUntilFilter) && (
          <button
            type="button"
            className="btn-ghost text-xs"
            onClick={() => { setNextFireSinceFilter(''); setNextFireUntilFilter(''); }}
            data-testid="schedule-list-next-fire-range-clear"
            aria-label="Clear next-fire range"
          >
            Clear next fire
          </button>
        )}
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          Last fired since
          <input
            type="datetime-local"
            value={lastFiredSinceFilter}
            onChange={(e) => setLastFiredSinceFilter(e.target.value)}
            className="input py-1 text-xs"
            data-testid="schedule-list-last-fired-since-filter"
            aria-label="Filter by last-fired since"
          />
        </label>
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          until
          <input
            type="datetime-local"
            value={lastFiredUntilFilter}
            onChange={(e) => setLastFiredUntilFilter(e.target.value)}
            className="input py-1 text-xs"
            data-testid="schedule-list-last-fired-until-filter"
            aria-label="Filter by last-fired until"
          />
        </label>
        {(lastFiredSinceFilter || lastFiredUntilFilter) && (
          <button
            type="button"
            className="btn-ghost text-xs"
            onClick={() => { setLastFiredSinceFilter(''); setLastFiredUntilFilter(''); }}
            data-testid="schedule-list-last-fired-range-clear"
            aria-label="Clear last-fired range"
          >
            Clear last fired
          </button>
        )}
      </div>

      {loading && !response ? (
        <div className="flex justify-center py-20"><Spinner size={20} /></div>
      ) : items.length === 0 ? (
        <div className="card">
          {searchFilter || actionFilter || catchUpFilter || timezoneFilter || enabledFilter || sinceFilter || untilFilter || nextFireSinceFilter || nextFireUntilFilter || lastFiredSinceFilter || lastFiredUntilFilter || prefixFilter ? (
            <EmptyState
              icon={Search}
              title="No schedules match your filters"
              description="Try a different search term, action, catch-up policy, timezone, enabled state, name prefix, created-at range, next-fire range, or last-fired range."
            />
          ) : (
            <EmptyState
              icon={Clock}
              title="No schedules defined"
              description="Schedules run recurring VM operations — automatic snapshots, nightly shutdowns, scheduled restarts."
            />
          )}
        </div>
      ) : (
        <div className="card overflow-hidden" data-testid="schedule-list">
          <table className="w-full">
            <thead>
              <tr className="border-b border-steel-800/40">
                <th className="table-header table-cell w-8"></th>
                <th className="table-header table-cell">Name</th>
                <th className="table-header table-cell">Action</th>
                <th className="table-header table-cell">Target</th>
                <th className="table-header table-cell">Enabled</th>
                <th className="table-header table-cell">Cron</th>
                <th className="table-header table-cell">Next fire</th>
                <th className="table-header table-cell">Last result</th>
                <th className="table-header table-cell text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {items.map((schedule) => (
                <ScheduleRow
                  key={schedule.id}
                  schedule={schedule}
                  onToggle={() => handleToggle(schedule)}
                  onEdit={() => setEditing(schedule)}
                  onDelete={() => handleDelete(schedule.id, schedule.name)}
                  onRunNow={() => handleRunNow(schedule.id)}
                  runningNow={runNowMut.loading}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}

      {items.length > 0 && (
        <PaginationControls
          page={page}
          perPage={perPage}
          total={total}
          itemLabel="schedules"
          onPageChange={setPage}
          onPerPageChange={(value) => { setPerPage(value); setPage(1); }}
        />
      )}
    </div>
  );
}

function LastResultChip({ result }) {
  if (!result) return <span className="text-xs font-mono text-steel-500">—</span>;
  const lower = String(result).toLowerCase();
  let Icon = MinusCircle;
  let cls = 'text-steel-400';
  if (lower.includes('success') || lower === 'ok') { Icon = CheckCircle2; cls = 'text-emerald-300'; }
  else if (lower.includes('error') || lower.includes('fail')) { Icon = AlertCircle; cls = 'text-red-300'; }
  else if (lower.includes('skip')) { Icon = MinusCircle; cls = 'text-amber-300'; }
  return (
    <span className={`inline-flex items-center gap-1.5 text-xs font-mono ${cls}`} data-testid="schedule-last-result">
      <Icon size={13} /> {result}
    </span>
  );
}

function RunStatusChip({ status }) {
  const lower = String(status || '').toLowerCase();
  let Icon = MinusCircle;
  let cls = 'text-steel-400';
  if (lower === 'success') { Icon = CheckCircle2; cls = 'text-emerald-300'; }
  else if (lower === 'error') { Icon = AlertCircle; cls = 'text-red-300'; }
  else if (lower === 'skipped') { Icon = MinusCircle; cls = 'text-amber-300'; }
  else if (lower === 'running') { Icon = Loader2; cls = 'text-blue-300'; }
  return (
    <span className={`inline-flex items-center gap-1 ${cls}`}>
      <Icon size={11} /> {status}
    </span>
  );
}

// ScheduleRow renders one schedule plus an expandable details row that lazily
// fetches the last 5 runs when opened (mirrors the Activity page disclosure).
function ScheduleRow({ schedule, onToggle, onEdit, onDelete, onRunNow, runningNow }) {
  const [expanded, setExpanded] = useState(false);
  const [runStatus, setRunStatus] = useState('');
  const [runSkipReason, setRunSkipReason] = useState('');
  const [runVMID, setRunVMID] = useState('');
  const [runVMIDDebounced, setRunVMIDDebounced] = useState('');
  const [runSearch, setRunSearch] = useState('');
  const [runSearchDebounced, setRunSearchDebounced] = useState('');
  const [runSort, setRunSort] = useState('');
  const [runOrder, setRunOrder] = useState('');
  const [runFinishedSince, setRunFinishedSince] = useState('');
  const [runFinishedUntil, setRunFinishedUntil] = useState('');
  const [runMinDurationMs, setRunMinDurationMs] = useState('');
  const [runMaxDurationMs, setRunMaxDurationMs] = useState('');

  useEffect(() => {
    const t = setTimeout(() => setRunVMIDDebounced(runVMID.trim()), 250);
    return () => clearTimeout(t);
  }, [runVMID]);

  useEffect(() => {
    const t = setTimeout(() => setRunSearchDebounced(runSearch.trim()), 250);
    return () => clearTimeout(t);
  }, [runSearch]);

  // datetime-local inputs hand back `YYYY-MM-DDTHH:mm`; the API expects
  // RFC3339, so append `:00Z` when a value is set. Empty stays empty so the
  // filter is disabled.
  const toRFC3339 = (v) => (v ? `${v}:00Z` : '');
  const finishedSinceParam = toRFC3339(runFinishedSince);
  const finishedUntilParam = toRFC3339(runFinishedUntil);

  // Number inputs hand back strings; parse to non-negative integers and treat
  // empty / blank / negative / non-numeric values as "filter disabled" so the
  // CLI / API contract (only forward when explicitly set to a valid value) is
  // mirrored client-side.
  const parsePositiveInt = (raw) => {
    const trimmed = (raw || '').trim();
    if (trimmed === '') return undefined;
    const n = Number(trimmed);
    if (!Number.isFinite(n) || n < 0 || !Number.isInteger(n)) return undefined;
    return n;
  };
  const minDurationMsParam = parsePositiveInt(runMinDurationMs);
  const maxDurationMsParam = parsePositiveInt(runMaxDurationMs);

  const { data: runsResponse, loading: runsLoading } = useFetch(
    () => (expanded ? schedulesApi.runs(schedule.id, { perPage: 5, status: runStatus || undefined, skipReason: runSkipReason || undefined, vmId: runVMIDDebounced || undefined, search: runSearchDebounced || undefined, sort: runSort || undefined, order: runOrder || undefined, finishedSince: finishedSinceParam || undefined, finishedUntil: finishedUntilParam || undefined, minDurationMs: minDurationMsParam, maxDurationMs: maxDurationMsParam }) : Promise.resolve(null)),
    [expanded, schedule.id, runStatus, runSkipReason, runVMIDDebounced, runSearchDebounced, runSort, runOrder, finishedSinceParam, finishedUntilParam, minDurationMsParam, maxDurationMsParam],
    null,
  );
  const runs = runsResponse?.data || [];

  return (
    <>
      <tr className="border-b border-steel-800/30 hover:bg-steel-800/20" data-testid={`schedule-row-${schedule.id}`}>
        <td className="px-1 py-1.5 text-steel-500">
          <button
            type="button"
            className="p-0.5 text-steel-500 hover:text-steel-200"
            aria-label={expanded ? 'Hide recent runs' : 'Show recent runs'}
            data-testid={`schedule-row-toggle-${schedule.id}`}
            onClick={() => setExpanded((e) => !e)}
          >
            {expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
          </button>
        </td>
        <td className="table-cell">
          <div className="text-sm text-steel-200">{schedule.name}</div>
          <div className="text-[11px] text-steel-600 font-mono">{schedule.id}</div>
        </td>
        <td className="table-cell">
          <span className="px-1.5 py-0.5 rounded text-[10px] font-mono bg-steel-800/60 text-steel-300 border border-steel-700/30">
            {schedule.action}
          </span>
        </td>
        <td className="table-cell">
          <span className="text-xs font-mono text-steel-300" data-testid={`schedule-target-${schedule.id}`}>
            {targetLabel(schedule)}
          </span>
        </td>
        <td className="table-cell">
          <label className="inline-flex items-center cursor-pointer">
            <input
              type="checkbox"
              checked={Boolean(schedule.enabled)}
              onChange={onToggle}
              data-testid={`schedule-enabled-toggle-${schedule.id}`}
              aria-label="Toggle enabled"
            />
          </label>
        </td>
        <td className="table-cell">
          <span className="text-xs font-mono text-steel-400">{schedule.cron_spec}</span>
        </td>
        <td className="table-cell">
          <span className="text-xs font-mono text-steel-400" data-testid={`schedule-next-fire-${schedule.id}`}>
            {formatTime(schedule.next_fire_at)}
          </span>
        </td>
        <td className="table-cell">
          <LastResultChip result={schedule.last_result} />
        </td>
        <td className="table-cell text-right">
          <div className="inline-flex items-center gap-1.5">
            <button
              className="btn-ghost btn-sm"
              onClick={onRunNow}
              disabled={runningNow}
              data-testid={`schedule-runnow-${schedule.id}`}
              title="Run now"
            >
              <Play size={13} /> Run now
            </button>
            <button
              className="btn-ghost btn-sm"
              onClick={onEdit}
              data-testid={`schedule-edit-${schedule.id}`}
              title="Edit schedule"
            >
              <Pencil size={13} />
            </button>
            <button
              className="btn-ghost btn-sm text-red-400 hover:text-red-300"
              onClick={onDelete}
              data-testid={`schedule-delete-${schedule.id}`}
              title="Delete schedule"
            >
              <Trash2 size={13} />
            </button>
          </div>
        </td>
      </tr>
      {expanded && (
        <tr className="bg-steel-900/40 border-b border-steel-800/30" data-testid={`schedule-runs-${schedule.id}`}>
          <td className="px-1 py-2"></td>
          <td colSpan={8} className="px-3 py-2">
            <div className="flex items-center justify-between mb-1 gap-2">
              <div className="text-[11px] font-mono text-steel-500">Recent runs</div>
              <div className="flex items-center gap-2">
                <input
                  type="text"
                  className="input input-sm text-[11px] py-0.5 w-40"
                  placeholder="Filter runs by VM id"
                  value={runVMID}
                  onChange={(e) => setRunVMID(e.target.value)}
                  data-testid={`schedule-runs-vm-filter-${schedule.id}`}
                  aria-label="Filter runs by VM id"
                />
                <input
                  type="text"
                  className="input input-sm text-[11px] py-0.5 w-44"
                  placeholder="Search error / skip-reason"
                  value={runSearch}
                  onChange={(e) => setRunSearch(e.target.value)}
                  data-testid={`schedule-runs-search-filter-${schedule.id}`}
                  aria-label="Search runs by error or skip-reason"
                />
                <select
                  className="input input-sm text-[11px] py-0.5"
                  value={runStatus}
                  onChange={(e) => setRunStatus(e.target.value)}
                  data-testid={`schedule-runs-status-filter-${schedule.id}`}
                  aria-label="Filter runs by status"
                >
                  <option value="">All statuses</option>
                  <option value="running">Running</option>
                  <option value="success">Success</option>
                  <option value="error">Error</option>
                  <option value="skipped">Skipped</option>
                </select>
                <select
                  className="input input-sm text-[11px] py-0.5"
                  value={runSkipReason}
                  onChange={(e) => setRunSkipReason(e.target.value)}
                  data-testid={`schedule-runs-skip-reason-filter-${schedule.id}`}
                  aria-label="Filter skipped runs by skip reason"
                  title="Narrows skipped runs to a single skip reason; runs without a skip_reason (every non-skipped run) are excluded when set"
                >
                  <option value="">All skip reasons</option>
                  <option value="vm_not_found">vm_not_found</option>
                  <option value="vm_already_stopped">vm_already_stopped</option>
                  <option value="vm_already_running">vm_already_running</option>
                  <option value="concurrent_run">concurrent_run</option>
                  <option value="catch_up_skipped">catch_up_skipped</option>
                  <option value="queue_full">queue_full</option>
                </select>
                <select
                  className="input input-sm text-[11px] py-0.5"
                  value={runSort}
                  onChange={(e) => setRunSort(e.target.value)}
                  data-testid={`schedule-runs-sort-${schedule.id}`}
                  aria-label="Sort runs"
                >
                  <option value="">Sort: Started (newest first)</option>
                  <option value="started_at">Started</option>
                  <option value="finished_at">Finished</option>
                  <option value="duration">Duration</option>
                  <option value="status">Status</option>
                  <option value="id">ID</option>
                </select>
                <select
                  className="input input-sm text-[11px] py-0.5"
                  value={runOrder}
                  onChange={(e) => setRunOrder(e.target.value)}
                  data-testid={`schedule-runs-order-${schedule.id}`}
                  aria-label="Sort order"
                >
                  <option value="">Order: default</option>
                  <option value="asc">Asc</option>
                  <option value="desc">Desc</option>
                </select>
                <input
                  type="datetime-local"
                  className="input input-sm text-[11px] py-0.5"
                  value={runFinishedSince}
                  onChange={(e) => setRunFinishedSince(e.target.value)}
                  data-testid={`schedule-runs-finished-since-filter-${schedule.id}`}
                  aria-label="Finished at or after"
                  title="Finished at or after (excludes still-running runs)"
                />
                <input
                  type="datetime-local"
                  className="input input-sm text-[11px] py-0.5"
                  value={runFinishedUntil}
                  onChange={(e) => setRunFinishedUntil(e.target.value)}
                  data-testid={`schedule-runs-finished-until-filter-${schedule.id}`}
                  aria-label="Finished at or before"
                  title="Finished at or before (excludes still-running runs)"
                />
                {(runFinishedSince || runFinishedUntil) && (
                  <button
                    type="button"
                    className="btn-ghost btn-sm text-[11px] py-0.5"
                    onClick={() => {
                      setRunFinishedSince('');
                      setRunFinishedUntil('');
                    }}
                    data-testid={`schedule-runs-finished-clear-${schedule.id}`}
                    title="Clear finished_at range"
                  >
                    Clear finished
                  </button>
                )}
                <input
                  type="number"
                  min="0"
                  step="1"
                  className="input input-sm text-[11px] py-0.5 w-28"
                  placeholder="Min duration ms"
                  value={runMinDurationMs}
                  onChange={(e) => setRunMinDurationMs(e.target.value)}
                  data-testid={`schedule-runs-min-duration-ms-filter-${schedule.id}`}
                  aria-label="Minimum run duration in milliseconds"
                  title="Lower bound (inclusive) on finished_at - started_at in milliseconds; excludes still-running runs"
                />
                <input
                  type="number"
                  min="0"
                  step="1"
                  className="input input-sm text-[11px] py-0.5 w-28"
                  placeholder="Max duration ms"
                  value={runMaxDurationMs}
                  onChange={(e) => setRunMaxDurationMs(e.target.value)}
                  data-testid={`schedule-runs-max-duration-ms-filter-${schedule.id}`}
                  aria-label="Maximum run duration in milliseconds"
                  title="Upper bound (inclusive) on finished_at - started_at in milliseconds; excludes still-running runs"
                />
                {(runMinDurationMs || runMaxDurationMs) && (
                  <button
                    type="button"
                    className="btn-ghost btn-sm text-[11px] py-0.5"
                    onClick={() => {
                      setRunMinDurationMs('');
                      setRunMaxDurationMs('');
                    }}
                    data-testid={`schedule-runs-duration-clear-${schedule.id}`}
                    title="Clear duration range"
                  >
                    Clear duration
                  </button>
                )}
              </div>
            </div>
            {runsLoading && !runsResponse ? (
              <Spinner size={14} />
            ) : runs.length === 0 ? (
              <div className="text-[11px] text-steel-600" data-testid={`schedule-runs-empty-${schedule.id}`}>No runs yet</div>
            ) : (
              <div className="flex flex-col gap-1">
                {runs.map((run) => (
                  <div
                    key={run.id}
                    className="flex items-center gap-3 text-[11px] font-mono"
                    data-testid={`schedule-run-${run.id}`}
                  >
                    <span className="text-steel-500 whitespace-nowrap">{formatTime(run.started_at)}</span>
                    <span className="text-forge-300">{run.vm_id || '—'}</span>
                    <RunStatusChip status={run.status} />
                    {(run.error || run.skip_reason) && (
                      <span className="text-steel-400">{run.error || run.skip_reason}</span>
                    )}
                  </div>
                ))}
              </div>
            )}
          </td>
        </tr>
      )}
    </>
  );
}

// ScheduleForm is the shared create/edit body. `mode` is 'create' or 'edit'.
function ScheduleForm({ mode, schedule, initialValues, onClose, onSaved }) {
  const [name, setName] = useState('');
  const [action, setAction] = useState('snapshot');
  const [vmId, setVmId] = useState('');
  const [tagsInput, setTagsInput] = useState('');
  const [cronSpec, setCronSpec] = useState('0 0 2 * * *');
  const [timezone, setTimezone] = useState('');
  const [enabled, setEnabled] = useState(true);
  const [catchUpPolicy, setCatchUpPolicy] = useState('skip');
  const [retentionCount, setRetentionCount] = useState('');
  const [maxConcurrent, setMaxConcurrent] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState(null);

  useEffect(() => {
    if (mode === 'edit' && schedule) {
      setName(schedule.name || '');
      setAction(schedule.action || 'snapshot');
      setVmId(schedule.vm_id || '');
      setTagsInput(schedule.tag_selector?.length ? schedule.tag_selector.join(', ') : '');
      setCronSpec(schedule.cron_spec || '');
      setTimezone(schedule.timezone || '');
      setEnabled(schedule.enabled !== false);
      setCatchUpPolicy(schedule.catch_up_policy || 'skip');
      setRetentionCount(schedule.retention_count != null ? String(schedule.retention_count) : '');
      setMaxConcurrent(schedule.max_concurrent != null ? String(schedule.max_concurrent) : '');
    } else if (mode === 'create') {
      setName(initialValues?.name || '');
      setAction('snapshot');
      setVmId(initialValues?.vm_id || '');
      setTagsInput('');
      setCronSpec('0 0 2 * * *');
      setTimezone('');
      setEnabled(true);
      setCatchUpPolicy('skip');
      setRetentionCount('');
      setMaxConcurrent('');
    }
    setErr(null);
    setSubmitting(false);
  }, [mode, schedule, initialValues]);

  const handleSubmit = async (e) => {
    e.preventDefault();
    setErr(null);
    const tags = tagsInput.split(',').map((s) => s.trim()).filter(Boolean);
    const retention = retentionCount.trim() === '' ? undefined : parseInt(retentionCount, 10);
    const concurrent = maxConcurrent.trim() === '' ? undefined : parseInt(maxConcurrent, 10);
    setSubmitting(true);
    try {
      if (mode === 'create') {
        await schedulesApi.create({
          name: name.trim(),
          action,
          vm_id: vmId.trim() || undefined,
          tag_selector: tags.length ? tags : undefined,
          cron_spec: cronSpec.trim(),
          timezone: timezone.trim() || undefined,
          enabled,
          catch_up_policy: catchUpPolicy,
          retention_count: retention,
          max_concurrent: concurrent,
        });
      } else {
        await schedulesApi.update(schedule.id, {
          name: name.trim(),
          action,
          vm_id: vmId.trim(),
          tag_selector: tags,
          cron_spec: cronSpec.trim(),
          timezone: timezone.trim(),
          enabled,
          catch_up_policy: catchUpPolicy,
          retention_count: retention ?? 0,
          max_concurrent: concurrent ?? 0,
        });
      }
      onSaved?.();
      onClose();
    } catch (e2) {
      setErr(e2?.message || `failed to ${mode} schedule`);
      setSubmitting(false);
    }
  };

  const formTestId = mode === 'create' ? 'add-schedule-form' : 'edit-schedule-form';
  const submitTestId = mode === 'create' ? 'schedule-create-submit' : 'schedule-edit-submit';

  return (
    <form onSubmit={handleSubmit} className="space-y-3" data-testid={formTestId}>
      <div>
        <label className="block text-xs font-mono text-steel-400 mb-1">Name</label>
        <input
          className="input w-full"
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
          maxLength={128}
          data-testid="schedule-name-input"
        />
      </div>
      <div>
        <label className="block text-xs font-mono text-steel-400 mb-1">Action</label>
        <select
          className="input w-full"
          value={action}
          onChange={(e) => setAction(e.target.value)}
          data-testid="schedule-action-select"
        >
          {ACTIONS.map((a) => <option key={a} value={a}>{a}</option>)}
        </select>
      </div>
      <div>
        <label className="block text-xs font-mono text-steel-400 mb-1">Target VM ID (optional)</label>
        <input
          className="input w-full"
          placeholder="vm-1741234567890123"
          value={vmId}
          onChange={(e) => setVmId(e.target.value)}
          data-testid="schedule-vmid-input"
        />
      </div>
      <div>
        <label className="block text-xs font-mono text-steel-400 mb-1">Tag selector (optional)</label>
        <input
          className="input w-full"
          placeholder="production, nightly"
          value={tagsInput}
          onChange={(e) => setTagsInput(e.target.value)}
          data-testid="schedule-tags-input"
        />
        <p className="text-[11px] font-mono text-steel-600 mt-1">
          Comma-separated. Mutually exclusive with VM ID; empty = all VMs.
        </p>
      </div>
      <div>
        <label className="block text-xs font-mono text-steel-400 mb-1">Cron spec (6 fields, with seconds)</label>
        <input
          className="input w-full"
          placeholder="0 0 2 * * *"
          value={cronSpec}
          onChange={(e) => setCronSpec(e.target.value)}
          required
          data-testid="schedule-cron-input"
        />
        <div className="flex flex-wrap gap-1.5 mt-1.5">
          {CRON_PRESETS.map((preset) => (
            <button
              key={preset.value}
              type="button"
              className="px-2 py-0.5 rounded text-[10px] font-mono bg-steel-800/60 text-steel-300 border border-steel-700/30 hover:bg-steel-700/60"
              onClick={() => setCronSpec(preset.value)}
              data-testid={preset.testId}
            >
              {preset.label}
            </button>
          ))}
        </div>
      </div>
      <div>
        <label className="block text-xs font-mono text-steel-400 mb-1">Timezone (optional)</label>
        <input
          className="input w-full"
          placeholder="America/New_York"
          value={timezone}
          onChange={(e) => setTimezone(e.target.value)}
          data-testid="schedule-timezone-input"
        />
      </div>
      <div className="flex gap-3">
        <div className="flex-1">
          <label className="block text-xs font-mono text-steel-400 mb-1">Catch-up policy</label>
          <select
            className="input w-full"
            value={catchUpPolicy}
            onChange={(e) => setCatchUpPolicy(e.target.value)}
            data-testid="schedule-catchup-select"
          >
            {CATCH_UP_POLICIES.map((p) => <option key={p} value={p}>{p}</option>)}
          </select>
        </div>
        <div className="flex-1">
          <label className="block text-xs font-mono text-steel-400 mb-1">Retention count</label>
          <input
            className="input w-full"
            type="number"
            min="0"
            value={retentionCount}
            onChange={(e) => setRetentionCount(e.target.value)}
            data-testid="schedule-retention-input"
          />
        </div>
        <div className="flex-1">
          <label className="block text-xs font-mono text-steel-400 mb-1">Max concurrent</label>
          <input
            className="input w-full"
            type="number"
            min="0"
            value={maxConcurrent}
            onChange={(e) => setMaxConcurrent(e.target.value)}
            data-testid="schedule-maxconcurrent-input"
          />
        </div>
      </div>
      <div>
        <label className="flex items-center gap-2 text-xs font-mono text-steel-300">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
            data-testid="schedule-enabled-input"
          />
          Enabled
        </label>
      </div>

      {err && <ErrorBanner message={err} />}

      <div className="flex justify-end gap-2 pt-2">
        <button type="button" className="btn-ghost" onClick={onClose}>Cancel</button>
        <button type="submit" className="btn-primary" disabled={submitting} data-testid={submitTestId}>
          {submitting ? <Spinner size={13} /> : null}
          {submitting ? 'Saving…' : (mode === 'create' ? 'Create schedule' : 'Save changes')}
        </button>
      </div>
    </form>
  );
}

function AddScheduleModal({ open, initialValues, onClose, onCreated }) {
  return (
    <Modal open={open} onClose={onClose} title="Add schedule" wide>
      {open && <ScheduleForm mode="create" initialValues={initialValues} onClose={onClose} onSaved={onCreated} />}
    </Modal>
  );
}

function EditScheduleModal({ schedule, open, onClose, onUpdated }) {
  if (!schedule) return null;
  return (
    <Modal open={open} onClose={onClose} title={`Edit schedule (${schedule.id})`} wide>
      <ScheduleForm mode="edit" schedule={schedule} onClose={onClose} onSaved={onUpdated} />
    </Modal>
  );
}

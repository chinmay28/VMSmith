import { useState, useEffect, useRef, useCallback } from 'react';
import { RefreshCw, ChevronDown } from 'lucide-react';
import { logs as logsApi } from '../api/client';
import { PageHeader, Spinner, ErrorBanner, PaginationControls } from '../components/Shared';

const LEVEL_ORDER = { debug: 0, info: 1, warn: 2, error: 3 };

const levelBadge = (level) => {
  switch (level) {
    case 'error': return 'badge-error';
    case 'warn':  return 'px-1.5 py-0.5 rounded text-[10px] font-mono font-semibold bg-amber-900/40 text-amber-300 border border-amber-700/30';
    case 'debug': return 'px-1.5 py-0.5 rounded text-[10px] font-mono font-semibold bg-steel-800/60 text-steel-400 border border-steel-700/30';
    default:      return 'px-1.5 py-0.5 rounded text-[10px] font-mono font-semibold bg-blue-900/40 text-blue-300 border border-blue-700/30';
  }
};

const sourceBadge = (source) => {
  switch (source) {
    case 'cli':    return 'text-forge-400';
    case 'daemon': return 'text-purple-400';
    default:       return 'text-steel-400'; // api
  }
};

const DEFAULT_PER_PAGE = 100;

export default function LogViewer() {
  const [entries, setEntries] = useState([]);
  const [totalEntries, setTotalEntries] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [levelFilter, setLevelFilter] = useState('debug');
  const [sourceFilter, setSourceFilter] = useState('');
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState(DEFAULT_PER_PAGE);
  const [autoScroll, setAutoScroll] = useState(true);
  const [paused, setPaused] = useState(false);
  const listRef = useRef(null);
  const autoScrollRef = useRef(true);

  useEffect(() => {
    autoScrollRef.current = autoScroll;
  }, [autoScroll]);

  const fetchLogs = useCallback(async () => {
    if (paused) return;
    try {
      const response = await logsApi.list({ level: levelFilter, page, perPage, source: sourceFilter });
      const nextEntries = response?.data?.entries || [];
      setEntries(nextEntries);
      setTotalEntries(response?.meta?.totalCount ?? response?.data?.total ?? nextEntries.length);
      setError(null);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, [levelFilter, page, perPage, sourceFilter, paused]);

  // Initial load + polling every 3 seconds.
  useEffect(() => {
    setLoading(true);
    fetchLogs();
    const id = setInterval(fetchLogs, 3000);
    return () => clearInterval(id);
  }, [fetchLogs]);

  useEffect(() => {
    setPage(1);
  }, [levelFilter, sourceFilter]);

  // Auto-scroll to bottom when new entries arrive — only when the user
  // is already pinned to the bottom. Use direct scrollTop (not smooth
  // scrollIntoView) so we don't fire intermediate scroll events that
  // would race with the user's own scrolling.
  useEffect(() => {
    if (!autoScroll || !listRef.current) return;
    const el = listRef.current;
    el.scrollTop = el.scrollHeight;
  }, [entries, autoScroll]);

  const handleScroll = () => {
    if (!listRef.current) return;
    const { scrollTop, scrollHeight, clientHeight } = listRef.current;
    const atBottom = scrollHeight - scrollTop - clientHeight < 40;
    if (atBottom !== autoScrollRef.current) {
      setAutoScroll(atBottom);
    }
  };

  const filtered = entries.filter(e => LEVEL_ORDER[e.level] >= LEVEL_ORDER[levelFilter]);

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title="Logs"
        subtitle={`${totalEntries} total entries`}
        actions={
          <div className="flex items-center gap-2">
            {/* Source filter */}
            <select
              className="input py-1 text-xs w-28"
              value={sourceFilter}
              onChange={e => setSourceFilter(e.target.value)}
            >
              <option value="">All sources</option>
              <option value="cli">CLI</option>
              <option value="api">API</option>
              <option value="daemon">Daemon</option>
            </select>

            {/* Level filter */}
            <select
              className="input py-1 text-xs w-24"
              value={levelFilter}
              onChange={e => setLevelFilter(e.target.value)}
            >
              <option value="debug">Debug+</option>
              <option value="info">Info+</option>
              <option value="warn">Warn+</option>
              <option value="error">Error</option>
            </select>

            {/* Pause / resume */}
            <button
              className={`btn-secondary text-xs py-1 px-3 ${paused ? 'text-amber-400 border-amber-700/40' : ''}`}
              onClick={() => setPaused(p => !p)}
            >
              {paused ? 'Resume' : 'Pause'}
            </button>

            {/* Manual refresh */}
            <button className="btn-ghost" onClick={fetchLogs} title="Refresh">
              {loading ? <Spinner size={14} /> : <RefreshCw size={14} />}
            </button>
          </div>
        }
      />

      {error && <div className="mb-3"><ErrorBanner message={error} onRetry={fetchLogs} /></div>}

      {/* Log table */}
      <div
        ref={listRef}
        onScroll={handleScroll}
        className="flex-1 overflow-y-auto rounded-lg border border-steel-800/60 bg-steel-950/60 font-mono text-xs"
        style={{ minHeight: 0 }}
      >
        {loading && filtered.length === 0 ? (
          <div className="flex justify-center py-20"><Spinner size={18} /></div>
        ) : filtered.length === 0 ? (
          <p className="text-center text-steel-500 py-20">No log entries yet.</p>
        ) : (
          <table className="w-full border-collapse">
            <thead className="sticky top-0 z-10 bg-steel-900/95 border-b border-steel-800/60">
              <tr>
                <th className="text-left px-3 py-2 text-[10px] uppercase tracking-widest text-steel-500 font-semibold w-44">Time</th>
                <th className="text-left px-3 py-2 text-[10px] uppercase tracking-widest text-steel-500 font-semibold w-16">Level</th>
                <th className="text-left px-3 py-2 text-[10px] uppercase tracking-widest text-steel-500 font-semibold w-16">Source</th>
                <th className="text-left px-3 py-2 text-[10px] uppercase tracking-widest text-steel-500 font-semibold">Message</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((entry, i) => (
                <LogRow key={i} entry={entry} />
              ))}
            </tbody>
          </table>
        )}
      </div>

      {totalEntries > 0 && (
        <PaginationControls
          page={page}
          perPage={perPage}
          total={totalEntries}
          perPageOptions={[50, 100, 200, 500]}
          itemLabel="log entries"
          onPageChange={setPage}
          onPerPageChange={(value) => {
            setPerPage(value);
            setPage(1);
          }}
        />
      )}

      {/* Scroll-to-bottom hint */}
      {!autoScroll && (
        <button
          className="absolute bottom-6 right-6 btn-secondary text-xs py-1 px-3 flex items-center gap-1 shadow-lg"
          onClick={() => {
            setAutoScroll(true);
            if (listRef.current) {
              listRef.current.scrollTop = listRef.current.scrollHeight;
            }
          }}
        >
          <ChevronDown size={13} /> Latest
        </button>
      )}
    </div>
  );
}

function LogRow({ entry }) {
  const [expanded, setExpanded] = useState(false);
  const hasFields = entry.fields && Object.keys(entry.fields).length > 0;

  const ts = new Date(entry.ts);
  const timeStr = ts.toLocaleTimeString('en-US', { hour12: false }) + '.' +
    String(ts.getMilliseconds()).padStart(3, '0');

  return (
    <>
      <tr
        className={`border-b border-steel-800/30 hover:bg-steel-800/20 transition-colors cursor-default ${
          entry.level === 'error' ? 'bg-red-950/10' :
          entry.level === 'warn'  ? 'bg-amber-950/10' : ''
        }`}
        onClick={() => hasFields && setExpanded(e => !e)}
      >
        <td className="px-3 py-1.5 text-steel-500 whitespace-nowrap">{timeStr}</td>
        <td className="px-3 py-1.5">
          <span className={levelBadge(entry.level)}>{entry.level}</span>
        </td>
        <td className={`px-3 py-1.5 ${sourceBadge(entry.source)}`}>{entry.source}</td>
        <td className="px-3 py-1.5 text-steel-200">{entry.msg}</td>
      </tr>
      {expanded && hasFields && (
        <tr className="bg-steel-900/40 border-b border-steel-800/30">
          <td colSpan={4} className="px-6 py-2">
            <div className="flex flex-wrap gap-x-4 gap-y-1">
              {Object.entries(entry.fields).map(([k, v]) => (
                <span key={k} className="text-[11px]">
                  <span className="text-steel-500">{k}=</span>
                  <span className="text-forge-300">{v}</span>
                </span>
              ))}
            </div>
          </td>
        </tr>
      )}
    </>
  );
}

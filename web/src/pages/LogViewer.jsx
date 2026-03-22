import { useState, useEffect, useRef, useCallback } from 'react';
import { RefreshCw, ChevronDown } from 'lucide-react';
import { logs as logsApi } from '../api/client';
import { PageHeader, Spinner, ErrorBanner } from '../components/Shared';

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

export default function LogViewer() {
  const [entries, setEntries] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [levelFilter, setLevelFilter] = useState('debug');
  const [sourceFilter, setSourceFilter] = useState('');
  const [autoScroll, setAutoScroll] = useState(true);
  const [paused, setPaused] = useState(false);
  const bottomRef = useRef(null);
  const listRef = useRef(null);

  const fetchLogs = useCallback(async () => {
    if (paused) return;
    try {
      const data = await logsApi.list({ level: levelFilter, limit: 500, source: sourceFilter });
      setEntries(data?.entries || []);
      setError(null);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, [levelFilter, sourceFilter, paused]);

  // Initial load + polling every 3 seconds.
  useEffect(() => {
    setLoading(true);
    fetchLogs();
    const id = setInterval(fetchLogs, 3000);
    return () => clearInterval(id);
  }, [fetchLogs]);

  // Auto-scroll to bottom when new entries arrive.
  useEffect(() => {
    if (autoScroll && bottomRef.current) {
      bottomRef.current.scrollIntoView({ behavior: 'smooth' });
    }
  }, [entries, autoScroll]);

  const handleScroll = () => {
    if (!listRef.current) return;
    const { scrollTop, scrollHeight, clientHeight } = listRef.current;
    const atBottom = scrollHeight - scrollTop - clientHeight < 40;
    setAutoScroll(atBottom);
  };

  const filtered = entries.filter(e => LEVEL_ORDER[e.level] >= LEVEL_ORDER[levelFilter]);

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title="Logs"
        subtitle={`${filtered.length} entries`}
        actions={
          <div className="flex items-center gap-2">
            {/* Source filter */}
            <select
              data-testid="log-source-filter"
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
              data-testid="log-level-filter"
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
              data-testid="btn-log-pause"
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
        data-testid="log-table"
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
        <div ref={bottomRef} />
      </div>

      {/* Scroll-to-bottom hint */}
      {!autoScroll && (
        <button
          className="absolute bottom-6 right-6 btn-secondary text-xs py-1 px-3 flex items-center gap-1 shadow-lg"
          onClick={() => {
            setAutoScroll(true);
            bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
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
          <span data-testid={`log-level-${entry.level}`} className={levelBadge(entry.level)}>{entry.level}</span>
        </td>
        <td data-testid={`log-source-${entry.source}`} className={`px-3 py-1.5 ${sourceBadge(entry.source)}`}>{entry.source}</td>
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

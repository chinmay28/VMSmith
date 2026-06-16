import { useState, useRef, useEffect } from 'react';
import { Spinner, ProgressBar, OperationProgress } from '../components/Shared';
import { getAuthToken } from '../auth';

// useOperationProgress subscribes to the per-VM operation-progress SSE channel
// and tracks the live percentage for a single op ("export" | "clone" | "boot").
// Returns { percent, done, start, stop, finish, reset } — call start() right
// before the blocking operation (or as soon as the VM exists, for "boot") and
// stop() when finished. Best-effort: if SSE is unavailable the caller simply
// falls back to the indeterminate bar (percent stays null).
//
// `done` flips true when a terminal frame arrives. For the readiness ("boot")
// op the terminal frame carries percent=100 when the VM was confirmed
// reachable, or a value below 100 when the wait timed out — so callers can
// distinguish "ready" from "still booting" via `percent === 100`.
export function useOperationProgress(vmId, op, name) {
  const [percent, setPercent] = useState(null);
  const [done, setDone] = useState(false);
  const esRef = useRef(null);

  const stop = () => {
    if (esRef.current) {
      esRef.current.close();
      esRef.current = null;
    }
  };

  useEffect(() => stop, []);

  const start = () => {
    setPercent(0);
    setDone(false);
    try {
      const token = getAuthToken();
      const qs = token ? `?api_key=${encodeURIComponent(token)}` : '';
      const es = new EventSource(`/api/v1/vms/${vmId}/operations/progress${qs}`);
      esRef.current = es;
      es.addEventListener('operation.progress', (e) => {
        try {
          const msg = JSON.parse(e.data);
          if (msg.op && op && msg.op !== op) return;
          if (msg.name && name && msg.name !== name) return;
          // Apply only meaningful percentages. Terminal frames for export/clone
          // carry percent=0 (the caller drives those to 100 via finish()), so
          // skipping zero keeps their bar from snapping back at the end while
          // still capturing the readiness terminal percent (always > 0).
          if (typeof msg.percent === 'number' && msg.percent > 0) setPercent(msg.percent);
          if (msg.done) { setDone(true); stop(); }
        } catch { /* ignore malformed frame */ }
      });
      es.onerror = () => { /* keep the indeterminate fallback */ };
    } catch { /* EventSource unsupported */ }
  };

  const finish = () => { setPercent(100); };
  const reset = () => { stop(); setPercent(null); setDone(false); };

  return { percent, done, start, stop, finish, reset };
}

// ProgressReadout renders a determinate percentage bar once a real frame has
// arrived, otherwise the indeterminate OperationProgress fallback.
export function ProgressReadout({ active, percent, label, testId }) {
  if (!active) return null;
  if (percent != null && percent > 0) {
    return (
      <div className="space-y-1.5" data-testid={testId} role="status" aria-live="polite">
        <div className="flex items-center justify-between text-xs text-steel-400">
          <span className="inline-flex items-center gap-2"><Spinner size={12} />{label}</span>
          <span className="font-mono">{Math.round(percent)}%</span>
        </div>
        <ProgressBar value={percent} max={100} variant="info" />
      </div>
    );
  }
  return <OperationProgress active label={label} testId={testId} />;
}

import { useEffect, useRef, useState, useCallback } from 'react';
import { vms } from '../api/client';
import { getAuthToken } from '../auth.js';
import { appendSample, sampleTime } from './vmStatsHelpers.js';

const API_BASE = '/api/v1';

export const STATS_STATE_LOADING = 'loading';
export const STATS_STATE_LIVE = 'live';
export const STATS_STATE_RECONNECTING = 'reconnecting';
export const STATS_STATE_FALLBACK = 'fallback';
export const STATS_STATE_ERROR = 'error';
export const STATS_STATE_CLOSED = 'closed';

const FALLBACK_AFTER_FAILURES = 3;
const RECONNECT_BASE_MS = 1000;
const RECONNECT_MAX_MS = 30_000;
const FALLBACK_POLL_INTERVAL_MS = 10_000;

function buildStreamUrl(vmId, apiKey) {
  const params = new URLSearchParams();
  if (apiKey) params.set('api_key', apiKey);
  const qs = params.toString();
  return `${API_BASE}/vms/${encodeURIComponent(vmId)}/stats/stream${qs ? `?${qs}` : ''}`;
}

/**
 * useVMStats fetches an initial /stats snapshot, then subscribes to
 * /stats/stream via EventSource for live samples. On repeated SSE failures it
 * falls back to short-poll mode against /stats so the chart keeps advancing
 * even when SSE is unavailable (tests, proxies, browsers without
 * EventSource).
 *
 * Returns the running snapshot, the (extended) history array, the current
 * sample, and a connection status string consumed by the live indicator.
 */
export function useVMStats(vmId, { enabled = true } = {}) {
  const [snapshot, setSnapshot] = useState(null);
  const [history, setHistory] = useState([]);
  const [status, setStatus] = useState(STATS_STATE_LOADING);
  const [error, setError] = useState(null);

  const historyRef = useRef([]);
  const snapshotRef = useRef(null);
  const closedRef = useRef(false);
  const sourceRef = useRef(null);
  const reconnectTimerRef = useRef(null);
  const fallbackTimerRef = useRef(null);
  const failureCountRef = useRef(0);

  const setHistoryBoth = useCallback((next) => {
    historyRef.current = next;
    setHistory(next);
  }, []);

  const setSnapshotBoth = useCallback((next) => {
    snapshotRef.current = next;
    setSnapshot(next);
  }, []);

  const cap = snapshot?.history_size && snapshot.history_size > 0 ? snapshot.history_size : 360;

  const ingestSample = useCallback((sample) => {
    if (!sample) return;
    const next = appendSample(historyRef.current, sample, cap);
    if (next !== historyRef.current) setHistoryBoth(next);
    const baseSnap = snapshotRef.current || {};
    setSnapshotBoth({
      ...baseSnap,
      current: sample,
      last_sampled_at: sample.timestamp ?? baseSnap.last_sampled_at,
    });
  }, [cap, setHistoryBoth, setSnapshotBoth]);

  const fetchInitial = useCallback(async () => {
    try {
      const snap = await vms.stats(vmId);
      if (closedRef.current) return null;
      const initialHistory = Array.isArray(snap?.history) ? snap.history : [];
      historyRef.current = initialHistory;
      setHistory(initialHistory);
      snapshotRef.current = snap;
      setSnapshot(snap);
      setError(null);
      return snap;
    } catch (e) {
      if (closedRef.current) return null;
      setError(e?.message || 'failed to fetch metrics');
      setStatus(STATS_STATE_ERROR);
      return null;
    }
  }, [vmId]);

  const connect = useCallback(() => {
    if (closedRef.current) return;
    if (typeof window === 'undefined' || typeof window.EventSource === 'undefined') {
      startFallback();
      return;
    }

    setStatus(failureCountRef.current === 0 ? STATS_STATE_LOADING : STATS_STATE_RECONNECTING);

    let es;
    try {
      es = new window.EventSource(buildStreamUrl(vmId, getAuthToken() ?? ''));
    } catch {
      scheduleReconnect();
      return;
    }
    sourceRef.current = es;

    es.onopen = () => {
      failureCountRef.current = 0;
      setStatus(STATS_STATE_LIVE);
    };

    const handleSampleMessage = (msg) => {
      if (!msg?.data) return;
      let parsed;
      try {
        parsed = JSON.parse(msg.data);
      } catch {
        return;
      }
      ingestSample(parsed);
    };

    // The daemon emits frames as `event: vm.stats`. EventSource only routes
    // typed events to addEventListener — onmessage receives only the default
    // (unnamed) event. Wire both so we work against either contract.
    es.addEventListener('vm.stats', handleSampleMessage);
    es.onmessage = handleSampleMessage;

    es.onerror = () => {
      es.close();
      sourceRef.current = null;
      failureCountRef.current += 1;
      if (failureCountRef.current >= FALLBACK_AFTER_FAILURES) {
        startFallback();
        return;
      }
      scheduleReconnect();
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [vmId, ingestSample]);

  const scheduleReconnect = useCallback(() => {
    if (closedRef.current) return;
    setStatus(STATS_STATE_RECONNECTING);
    const attempt = failureCountRef.current;
    const delay = Math.min(RECONNECT_MAX_MS, RECONNECT_BASE_MS * 2 ** Math.max(0, attempt - 1));
    reconnectTimerRef.current = setTimeout(connect, delay);
  }, [connect]);

  const startFallback = useCallback(() => {
    if (closedRef.current) return;
    setStatus(STATS_STATE_FALLBACK);

    const poll = async () => {
      if (closedRef.current) return;
      try {
        const snap = await vms.stats(vmId);
        if (closedRef.current) return;
        if (snap?.current) ingestSample(snap.current);
        snapshotRef.current = { ...(snapshotRef.current || {}), state: snap?.state, last_sampled_at: snap?.last_sampled_at, history_size: snap?.history_size, interval_seconds: snap?.interval_seconds };
        setSnapshot(snapshotRef.current);
      } catch {
        // keep polling regardless
      }
    };

    poll();
    fallbackTimerRef.current = setInterval(poll, FALLBACK_POLL_INTERVAL_MS);
  }, [vmId, ingestSample]);

  useEffect(() => {
    if (!enabled || !vmId) {
      closedRef.current = true;
      setStatus(STATS_STATE_CLOSED);
      return undefined;
    }

    closedRef.current = false;
    failureCountRef.current = 0;
    historyRef.current = [];
    snapshotRef.current = null;
    setHistory([]);
    setSnapshot(null);
    setStatus(STATS_STATE_LOADING);

    let cancelled = false;
    fetchInitial().then(() => {
      if (cancelled || closedRef.current) return;
      connect();
    });

    return () => {
      cancelled = true;
      closedRef.current = true;
      if (sourceRef.current) {
        sourceRef.current.close();
        sourceRef.current = null;
      }
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }
      if (fallbackTimerRef.current) {
        clearInterval(fallbackTimerRef.current);
        fallbackTimerRef.current = null;
      }
      setStatus(STATS_STATE_CLOSED);
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [vmId, enabled]);

  return {
    snapshot,
    history,
    current: snapshot?.current ?? null,
    status,
    error,
  };
}


import { useEffect, useRef, useState, useCallback } from 'react';
import { getAuthToken } from '../auth.js';

const API_BASE = '/api/v1';

export const STATE_CONNECTING = 'connecting';
export const STATE_LIVE = 'live';
export const STATE_RECONNECTING = 'reconnecting';
export const STATE_FALLBACK = 'fallback';
export const STATE_CLOSED = 'closed';

const FALLBACK_AFTER_FAILURES = 3;
const RECONNECT_BASE_MS = 1000;
const RECONNECT_MAX_MS = 30_000;
const FALLBACK_WINDOW_MS = 30_000;
const FALLBACK_POLL_INTERVAL_MS = 10_000;

function buildStreamUrl({ since, apiKey }) {
  const params = new URLSearchParams();
  if (since) params.set('since', since);
  // EventSource cannot send custom headers, so the daemon also accepts
  // ?api_key= for same-origin GUI use. The token never leaves the browser
  // since the page is served from the same daemon.
  if (apiKey) params.set('api_key', apiKey);
  const qs = params.toString();
  return `${API_BASE}/events/stream${qs ? `?${qs}` : ''}`;
}

/**
 * useEventStream subscribes to /api/v1/events/stream via EventSource and exposes
 * connection state plus the most recent event id. Callers wire the optional
 * `onEvent` callback for side effects (e.g. invalidating polling caches).
 *
 * The hook handles automatic reconnect with `Last-Event-ID` (passed via the
 * `since` query param) and falls back to short-poll mode for 30 seconds after
 * repeated failures so consumers keep updating while SSE recovers.
 *
 * @param {object} options
 * @param {(event: object) => void} [options.onEvent] Called for each parsed event.
 * @param {boolean} [options.enabled=true] When false, no connection is opened.
 * @returns {{ status: string, lastEventId: string|null, lastEvent: object|null }}
 */
export function useEventStream({ onEvent, enabled = true } = {}) {
  const [status, setStatus] = useState(STATE_CLOSED);
  const [lastEventId, setLastEventId] = useState(null);
  const [lastEvent, setLastEvent] = useState(null);

  const onEventRef = useRef(onEvent);
  useEffect(() => {
    onEventRef.current = onEvent;
  }, [onEvent]);

  const lastIdRef = useRef(null);
  const failureCountRef = useRef(0);
  const closedRef = useRef(false);
  const sourceRef = useRef(null);
  const reconnectTimerRef = useRef(null);
  const fallbackTimerRef = useRef(null);
  const fallbackExitTimerRef = useRef(null);

  const clearReconnectTimer = useCallback(() => {
    if (reconnectTimerRef.current) {
      clearTimeout(reconnectTimerRef.current);
      reconnectTimerRef.current = null;
    }
  }, []);

  const clearFallbackTimers = useCallback(() => {
    if (fallbackTimerRef.current) {
      clearInterval(fallbackTimerRef.current);
      fallbackTimerRef.current = null;
    }
    if (fallbackExitTimerRef.current) {
      clearTimeout(fallbackExitTimerRef.current);
      fallbackExitTimerRef.current = null;
    }
  }, []);

  const dispatchEvent = useCallback((evt) => {
    if (!evt) return;
    if (evt.id != null) {
      lastIdRef.current = String(evt.id);
      setLastEventId(String(evt.id));
    }
    setLastEvent(evt);
    if (onEventRef.current) {
      try {
        onEventRef.current(evt);
      } catch (e) {
        // Listener errors must not break the stream.
        // eslint-disable-next-line no-console
        console.warn('useEventStream onEvent threw', e);
      }
    }
  }, []);

  const connect = useCallback(() => {
    if (closedRef.current) return;
    if (typeof window === 'undefined' || typeof window.EventSource === 'undefined') {
      return;
    }

    clearReconnectTimer();
    clearFallbackTimers();
    setStatus(failureCountRef.current === 0 ? STATE_CONNECTING : STATE_RECONNECTING);

    const url = buildStreamUrl({
      since: lastIdRef.current ?? '',
      apiKey: getAuthToken() ?? '',
    });

    let es;
    try {
      es = new window.EventSource(url);
    } catch {
      return;
    }
    sourceRef.current = es;

    es.onopen = () => {
      failureCountRef.current = 0;
      setStatus(STATE_LIVE);
    };

    es.onmessage = (msg) => {
      if (!msg?.data) return;
      let parsed;
      try {
        parsed = JSON.parse(msg.data);
      } catch {
        return;
      }
      if (msg.lastEventId) {
        lastIdRef.current = msg.lastEventId;
      }
      dispatchEvent(parsed);
    };

    es.onerror = () => {
      es.close();
      sourceRef.current = null;
      failureCountRef.current += 1;
      if (failureCountRef.current >= FALLBACK_AFTER_FAILURES) {
        clearReconnectTimer();
        clearFallbackTimers();
        setStatus(STATE_FALLBACK);

        const poll = async () => {
          if (closedRef.current) return;
          try {
            const params = new URLSearchParams();
            const after = lastIdRef.current;
            if (after) params.set('until', String(parseInt(after, 10) + 1));
            params.set('per_page', '50');

            const headers = {};
            const apiKey = getAuthToken();
            if (apiKey) headers.Authorization = `Bearer ${apiKey}`;

            const resp = await fetch(`${API_BASE}/events?${params.toString()}`, { headers });
            if (!resp.ok) {
              throw new Error(`HTTP ${resp.status}`);
            }
            const events = await resp.json();
            if (Array.isArray(events)) {
              for (let i = events.length - 1; i >= 0; i -= 1) {
                const event = events[i];
                const seq = parseInt(event?.id ?? '0', 10);
                const cursor = parseInt(after ?? '0', 10);
                if (Number.isFinite(seq) && seq > cursor) {
                  dispatchEvent(event);
                }
              }
            }
          } catch {
            // Swallow, fallback mode keeps polling until we retry SSE.
          }
        };

        poll();
        fallbackTimerRef.current = setInterval(poll, FALLBACK_POLL_INTERVAL_MS);
        fallbackExitTimerRef.current = setTimeout(() => {
          if (closedRef.current) return;
          clearFallbackTimers();
          failureCountRef.current = 0;
          connect();
        }, FALLBACK_WINDOW_MS);
        return;
      }

      clearReconnectTimer();
      setStatus(STATE_RECONNECTING);
      const attempt = failureCountRef.current;
      const delay = Math.min(RECONNECT_MAX_MS, RECONNECT_BASE_MS * 2 ** Math.max(0, attempt - 1));
      reconnectTimerRef.current = setTimeout(connect, delay);
    };
  }, [clearFallbackTimers, clearReconnectTimer, dispatchEvent]);

  useEffect(() => {
    if (!enabled) {
      closedRef.current = true;
      clearReconnectTimer();
      clearFallbackTimers();
      setStatus(STATE_CLOSED);
      return undefined;
    }

    closedRef.current = false;
    failureCountRef.current = 0;

    if (typeof window === 'undefined' || typeof window.EventSource === 'undefined') {
      setStatus(STATE_FALLBACK);
      const poll = async () => {
        if (closedRef.current) return;
        try {
          const params = new URLSearchParams();
          const after = lastIdRef.current;
          if (after) params.set('until', String(parseInt(after, 10) + 1));
          params.set('per_page', '50');

          const headers = {};
          const apiKey = getAuthToken();
          if (apiKey) headers.Authorization = `Bearer ${apiKey}`;

          const resp = await fetch(`${API_BASE}/events?${params.toString()}`, { headers });
          if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
          const events = await resp.json();
          if (Array.isArray(events)) {
            for (let i = events.length - 1; i >= 0; i -= 1) {
              const event = events[i];
              const seq = parseInt(event?.id ?? '0', 10);
              const cursor = parseInt(after ?? '0', 10);
              if (Number.isFinite(seq) && seq > cursor) {
                dispatchEvent(event);
              }
            }
          }
        } catch {
          // Ignore, next poll will try again.
        }
      };
      poll();
      fallbackTimerRef.current = setInterval(poll, FALLBACK_POLL_INTERVAL_MS);
    } else {
      connect();
    }

    return () => {
      closedRef.current = true;
      if (sourceRef.current) {
        sourceRef.current.close();
        sourceRef.current = null;
      }
      clearReconnectTimer();
      clearFallbackTimers();
      setStatus(STATE_CLOSED);
    };
  }, [enabled, clearFallbackTimers, clearReconnectTimer, connect, dispatchEvent]);

  return { status, lastEventId, lastEvent };
}

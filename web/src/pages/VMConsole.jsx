import React, { useState, useEffect, useRef, useCallback } from 'react';
import { useParams } from 'react-router-dom';
import { Monitor, TerminalSquare, Maximize2, RefreshCw, Keyboard } from 'lucide-react';
import RFB from '../vendor/novnc';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import { vms } from '../api/client';

// Build an absolute ws(s):// URL from the relative websocket path the ticket
// endpoint returns. wss is required whenever the page itself is https —
// the daemon rejects mixed-content ws with 403.
function websocketURL(path) {
  const scheme = window.location.protocol === 'https:' ? 'wss://' : 'ws://';
  return scheme + window.location.host + path;
}

const STATUS_STYLES = {
  connecting: 'bg-yellow-500/20 text-yellow-300',
  connected: 'bg-green-500/20 text-green-300',
  disconnected: 'bg-gray-500/20 text-gray-300',
  error: 'bg-red-500/20 text-red-300',
};

function StatusPill({ status, message }) {
  return (
    <span
      data-testid="console-status"
      data-status={status}
      className={`px-2 py-1 rounded text-xs font-medium ${STATUS_STYLES[status] || STATUS_STYLES.disconnected}`}
    >
      {message || status}
    </span>
  );
}

// VNC tab: noVNC RFB client attached to a container div.
function VNCConsole({ vmId, onStatus }) {
  const containerRef = useRef(null);
  const rfbRef = useRef(null);
  const [generation, setGeneration] = useState(0);
  const [status, setStatus] = useState('connecting');
  const [message, setMessage] = useState('Requesting console ticket…');

  const report = useCallback(
    (nextStatus, nextMessage) => {
      setStatus(nextStatus);
      setMessage(nextMessage);
      if (onStatus) onStatus(nextStatus, nextMessage);
    },
    [onStatus]
  );

  useEffect(() => {
    let cancelled = false;

    async function connect() {
      report('connecting', 'Requesting console ticket…');
      let ticket;
      try {
        ticket = await vms.issueConsoleTicket(vmId, 'vnc');
      } catch (err) {
        if (!cancelled) report('error', err?.message || 'Failed to issue console ticket');
        return;
      }
      if (cancelled || !containerRef.current) return;

      report('connecting', 'Connecting to VNC…');
      const rfb = new RFB(containerRef.current, websocketURL(ticket.websocket_url), {
        wsProtocols: ['binary'],
      });
      rfb.scaleViewport = true;
      rfb.resizeSession = false;
      rfbRef.current = rfb;

      rfb.addEventListener('connect', () => {
        if (!cancelled) report('connected', 'Connected');
      });
      rfb.addEventListener('disconnect', (e) => {
        if (cancelled) return;
        if (e?.detail?.clean) {
          report('disconnected', 'Disconnected');
        } else {
          report('error', 'Connection lost');
        }
      });
      rfb.addEventListener('securityfailure', (e) => {
        if (!cancelled) report('error', `VNC security failure: ${e?.detail?.reason || 'unknown'}`);
      });
    }

    connect();
    return () => {
      cancelled = true;
      if (rfbRef.current) {
        try {
          rfbRef.current.disconnect();
        } catch {
          // Already torn down.
        }
        rfbRef.current = null;
      }
    };
  }, [vmId, generation, report]);

  const sendCtrlAltDel = () => {
    if (rfbRef.current && status === 'connected') rfbRef.current.sendCtrlAltDel();
  };

  const toggleFullscreen = () => {
    const el = containerRef.current?.parentElement;
    if (!el) return;
    if (document.fullscreenElement) {
      document.exitFullscreen();
    } else {
      el.requestFullscreen?.();
    }
  };

  return (
    <div className="flex flex-col h-full" data-testid="vnc-console">
      <div className="flex items-center gap-2 px-4 py-2 bg-gray-800 border-b border-gray-700">
        <StatusPill status={status} message={message} />
        <div className="flex-1" />
        <button
          onClick={sendCtrlAltDel}
          disabled={status !== 'connected'}
          data-testid="ctrl-alt-del"
          className="flex items-center gap-1 px-3 py-1.5 text-xs rounded bg-gray-700 hover:bg-gray-600 disabled:opacity-40 disabled:cursor-not-allowed text-gray-200"
        >
          <Keyboard size={14} /> Ctrl-Alt-Del
        </button>
        <button
          onClick={() => setGeneration((g) => g + 1)}
          data-testid="console-reconnect"
          className="flex items-center gap-1 px-3 py-1.5 text-xs rounded bg-gray-700 hover:bg-gray-600 text-gray-200"
        >
          <RefreshCw size={14} /> Reconnect
        </button>
        <button
          onClick={toggleFullscreen}
          data-testid="console-fullscreen"
          className="flex items-center gap-1 px-3 py-1.5 text-xs rounded bg-gray-700 hover:bg-gray-600 text-gray-200"
        >
          <Maximize2 size={14} /> Fullscreen
        </button>
      </div>
      <div className="relative flex-1 bg-black overflow-hidden">
        <div ref={containerRef} className="absolute inset-0" data-testid="vnc-canvas-container" />
        {status !== 'connected' && (
          <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
            <div className="px-4 py-2 rounded bg-gray-900/80 text-sm text-gray-300" data-testid="console-overlay">
              {message}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

// Serial tab: xterm.js attached to the text-subprotocol websocket.
function SerialConsole({ vmId }) {
  const containerRef = useRef(null);
  const [generation, setGeneration] = useState(0);
  const [status, setStatus] = useState('connecting');
  const [message, setMessage] = useState('Requesting console ticket…');

  useEffect(() => {
    let cancelled = false;
    let ws = null;
    let term = null;
    let fit = null;
    let resizeObserver = null;

    async function connect() {
      setStatus('connecting');
      setMessage('Requesting console ticket…');
      let ticket;
      try {
        ticket = await vms.issueConsoleTicket(vmId, 'serial');
      } catch (err) {
        if (!cancelled) {
          setStatus('error');
          setMessage(err?.message || 'Failed to issue console ticket');
        }
        return;
      }
      if (cancelled || !containerRef.current) return;

      term = new Terminal({
        cursorBlink: true,
        fontSize: 14,
        fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
        theme: { background: '#000000' },
      });
      fit = new FitAddon();
      term.loadAddon(fit);
      term.open(containerRef.current);
      fit.fit();
      resizeObserver = new ResizeObserver(() => fit && fit.fit());
      resizeObserver.observe(containerRef.current);

      setMessage('Connecting to serial console…');
      ws = new WebSocket(websocketURL(ticket.websocket_url), ['text']);
      ws.onopen = () => {
        if (cancelled) return;
        setStatus('connected');
        setMessage('Connected');
        term.focus();
      };
      ws.onmessage = (event) => {
        if (term) term.write(typeof event.data === 'string' ? event.data : new Uint8Array(event.data));
      };
      ws.onclose = () => {
        if (!cancelled) {
          setStatus('disconnected');
          setMessage('Disconnected');
        }
      };
      ws.onerror = () => {
        if (!cancelled) {
          setStatus('error');
          setMessage('Connection error');
        }
      };
      term.onData((data) => {
        if (ws && ws.readyState === WebSocket.OPEN) ws.send(data);
      });
    }

    connect();
    return () => {
      cancelled = true;
      if (resizeObserver) resizeObserver.disconnect();
      if (ws) ws.close();
      if (term) term.dispose();
    };
  }, [vmId, generation]);

  return (
    <div className="flex flex-col h-full" data-testid="serial-console">
      <div className="flex items-center gap-2 px-4 py-2 bg-gray-800 border-b border-gray-700">
        <StatusPill status={status} message={message} />
        <div className="flex-1" />
        <button
          onClick={() => setGeneration((g) => g + 1)}
          data-testid="serial-reconnect"
          className="flex items-center gap-1 px-3 py-1.5 text-xs rounded bg-gray-700 hover:bg-gray-600 text-gray-200"
        >
          <RefreshCw size={14} /> Reconnect
        </button>
      </div>
      <div className="relative flex-1 bg-black overflow-hidden p-2">
        <div ref={containerRef} className="absolute inset-0" data-testid="serial-terminal-container" />
      </div>
    </div>
  );
}

// Full-page console surface. Rendered outside the app Layout so a new tab
// gives a clean keyboard-capture surface (Ctrl-W etc. still belong to the
// browser, but nothing in the SPA chrome steals focus).
export default function VMConsole() {
  const { id } = useParams();
  const [tab, setTab] = useState('vnc');
  const [vmName, setVmName] = useState('');

  useEffect(() => {
    let cancelled = false;
    vms
      .get(id)
      .then((vm) => {
        if (!cancelled && vm?.name) {
          setVmName(vm.name);
          document.title = `Console — ${vm.name}`;
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [id]);

  const tabClass = (active) =>
    `flex items-center gap-1.5 px-4 py-2 text-sm border-b-2 ${
      active
        ? 'border-blue-500 text-blue-400'
        : 'border-transparent text-gray-400 hover:text-gray-200'
    }`;

  return (
    <div className="fixed inset-0 flex flex-col bg-gray-900 text-gray-100" data-testid="vm-console-page">
      <header className="flex items-center gap-4 px-4 pt-2 bg-gray-800 border-b border-gray-700">
        <h1 className="text-sm font-semibold text-gray-200 pb-2" data-testid="console-vm-name">
          {vmName || id}
        </h1>
        <nav className="flex">
          <button className={tabClass(tab === 'vnc')} onClick={() => setTab('vnc')} data-testid="tab-vnc">
            <Monitor size={15} /> VNC
          </button>
          <button className={tabClass(tab === 'serial')} onClick={() => setTab('serial')} data-testid="tab-serial">
            <TerminalSquare size={15} /> Serial
          </button>
        </nav>
      </header>
      <main className="flex-1 min-h-0">
        {tab === 'vnc' ? <VNCConsole vmId={id} /> : <SerialConsole vmId={id} />}
      </main>
    </div>
  );
}

import { useState, useEffect, useRef, useCallback } from 'react';
import { useParams } from 'react-router-dom';
import { Monitor, Terminal as TerminalIcon, RotateCcw, Maximize, Minimize, Keyboard } from 'lucide-react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import RFB from '../vendor/novnc/core/rfb.js';
import { vms } from '../api/client';
import { useFetch } from '../hooks/useFetch';
import { StatusBadge, Spinner } from '../components/Shared';

const STATUS_IDLE = 'idle';
const STATUS_CONNECTING = 'connecting';
const STATUS_CONNECTED = 'connected';
const STATUS_DISCONNECTED = 'disconnected';
const STATUS_ERROR = 'error';

// buildWsUrl turns the relative websocket_url returned by the ticket endpoint
// (e.g. "/api/v1/vms/<id>/console?ticket=<t>") into an absolute ws:// or
// wss:// URL on the SPA's own origin. The daemon serves the API and the GUI
// from the same port, and the Vite dev server proxies /api with ws:true, so
// window.location.host is always the right host.
function buildWsUrl(websocketUrl) {
  const proto = window.location.protocol === 'https:' ? 'wss://' : 'ws://';
  return `${proto}${window.location.host}${websocketUrl}`;
}

const statusDotStyles = {
  [STATUS_CONNECTED]: 'bg-emerald-400',
  [STATUS_CONNECTING]: 'bg-amber-400 animate-pulse',
  [STATUS_DISCONNECTED]: 'bg-steel-500',
  [STATUS_ERROR]: 'bg-red-400',
  [STATUS_IDLE]: 'bg-steel-600',
};

export default function VMConsole() {
  const { id } = useParams();
  const { data: vm } = useFetch(() => vms.get(id), [id], 10000);
  const [tab, setTab] = useState('vnc');
  const [status, setStatus] = useState(STATUS_IDLE);
  const [statusMessage, setStatusMessage] = useState('');
  const [fullscreen, setFullscreen] = useState(false);

  const pageRef = useRef(null);
  const vncContainerRef = useRef(null);
  const rfbRef = useRef(null);
  const serialContainerRef = useRef(null);
  const termRef = useRef(null);
  const fitAddonRef = useRef(null);
  const serialWsRef = useRef(null);
  // Monotonic connection generation: stale event handlers from a torn-down
  // connection compare against this and no-op instead of clobbering state.
  const genRef = useRef(0);

  const teardownVNC = useCallback(() => {
    if (rfbRef.current) {
      try { rfbRef.current.disconnect(); } catch { /* already closed */ }
      rfbRef.current = null;
    }
  }, []);

  const teardownSerial = useCallback(() => {
    if (serialWsRef.current) {
      try { serialWsRef.current.close(); } catch { /* already closed */ }
      serialWsRef.current = null;
    }
    if (termRef.current) {
      termRef.current.dispose();
      termRef.current = null;
      fitAddonRef.current = null;
    }
  }, []);

  const connectVNC = useCallback(async () => {
    const gen = ++genRef.current;
    teardownVNC();
    setStatus(STATUS_CONNECTING);
    setStatusMessage('');
    let ticket;
    try {
      ticket = await vms.issueConsoleTicket(id, 'vnc');
    } catch (err) {
      if (gen !== genRef.current) return;
      setStatus(STATUS_ERROR);
      setStatusMessage(err.message);
      return;
    }
    if (gen !== genRef.current || !vncContainerRef.current) return;
    try {
      const rfb = new RFB(vncContainerRef.current, buildWsUrl(ticket.websocket_url), {
        wsProtocols: ['binary'],
      });
      rfb.scaleViewport = true;
      rfb.background = 'transparent';
      rfb.addEventListener('connect', () => {
        if (gen !== genRef.current) return;
        setStatus(STATUS_CONNECTED);
        setStatusMessage('');
      });
      rfb.addEventListener('disconnect', (e) => {
        if (gen !== genRef.current) return;
        setStatus(STATUS_DISCONNECTED);
        setStatusMessage(e.detail?.clean ? 'Connection closed.' : 'Connection lost — the websocket closed unexpectedly.');
      });
      rfb.addEventListener('securityfailure', (e) => {
        if (gen !== genRef.current) return;
        setStatus(STATUS_ERROR);
        setStatusMessage(`Security handshake failed${e.detail?.reason ? `: ${e.detail.reason}` : ''}`);
      });
      rfbRef.current = rfb;
    } catch (err) {
      if (gen !== genRef.current) return;
      setStatus(STATUS_ERROR);
      setStatusMessage(err.message);
    }
  }, [id, teardownVNC]);

  const connectSerial = useCallback(async () => {
    const gen = ++genRef.current;
    teardownSerial();
    setStatus(STATUS_CONNECTING);
    setStatusMessage('');
    let ticket;
    try {
      ticket = await vms.issueConsoleTicket(id, 'serial');
    } catch (err) {
      if (gen !== genRef.current) return;
      setStatus(STATUS_ERROR);
      setStatusMessage(err.message);
      return;
    }
    if (gen !== genRef.current || !serialContainerRef.current) return;

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: '"JetBrains Mono", "Fira Code", monospace',
      theme: { background: '#070d07', foreground: '#bbd8bb', cursor: '#00ff41' },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(serialContainerRef.current);
    fit.fit();
    termRef.current = term;
    fitAddonRef.current = fit;

    const ws = new WebSocket(buildWsUrl(ticket.websocket_url), 'text');
    serialWsRef.current = ws;
    ws.onopen = () => {
      if (gen !== genRef.current) return;
      setStatus(STATUS_CONNECTED);
      setStatusMessage('');
      term.focus();
    };
    ws.onmessage = (e) => {
      if (gen !== genRef.current) return;
      if (typeof e.data === 'string') term.write(e.data);
    };
    ws.onclose = () => {
      if (gen !== genRef.current) return;
      setStatus((prev) => (prev === STATUS_ERROR ? prev : STATUS_DISCONNECTED));
      setStatusMessage((prev) => prev || 'Connection closed.');
    };
    ws.onerror = () => {
      if (gen !== genRef.current) return;
      setStatus(STATUS_ERROR);
      setStatusMessage('Serial console websocket error.');
    };
    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(data);
    });
  }, [id, teardownSerial]);

  const reconnect = useCallback(() => {
    if (tab === 'vnc') connectVNC();
    else connectSerial();
  }, [tab, connectVNC, connectSerial]);

  // (Re-)connect whenever the active tab changes, and tear everything down on
  // unmount. Tickets are single-use, so each attempt mints a fresh one.
  useEffect(() => {
    if (tab === 'vnc') {
      teardownSerial();
      connectVNC();
    } else {
      teardownVNC();
      connectSerial();
    }
    return () => {
      genRef.current += 1;
      teardownVNC();
      teardownSerial();
    };
  }, [tab, connectVNC, connectSerial, teardownVNC, teardownSerial]);

  // Keep the serial terminal sized to its container.
  useEffect(() => {
    const onResize = () => fitAddonRef.current?.fit();
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  useEffect(() => {
    const onFsChange = () => setFullscreen(!!document.fullscreenElement);
    document.addEventListener('fullscreenchange', onFsChange);
    return () => document.removeEventListener('fullscreenchange', onFsChange);
  }, []);

  const toggleFullscreen = () => {
    if (document.fullscreenElement) {
      document.exitFullscreen?.();
    } else {
      pageRef.current?.requestFullscreen?.();
    }
  };

  const sendCtrlAltDel = () => {
    rfbRef.current?.sendCtrlAltDel();
  };

  const showOverlay = status !== STATUS_CONNECTED;

  return (
    <div ref={pageRef} className="h-screen w-screen flex flex-col bg-steel-950" data-testid="console-page">
      {/* Toolbar */}
      <div className="flex items-center justify-between px-4 py-2 border-b border-steel-800/60 bg-steel-900/60 flex-shrink-0">
        <div className="flex items-center gap-3 min-w-0">
          <Monitor size={16} className="text-forge-400 flex-shrink-0" />
          <span className="font-display font-semibold text-steel-100 truncate" data-testid="console-vm-name">
            {vm?.name || id}
          </span>
          {vm && <StatusBadge state={vm.state} />}
          <span className="flex items-center gap-1.5 text-xs text-steel-400" data-testid="console-status" data-status={status}>
            <span className={`w-2 h-2 rounded-full ${statusDotStyles[status] || statusDotStyles[STATUS_IDLE]}`} />
            {status}
          </span>
        </div>
        <div className="flex items-center gap-1">
          <div className="flex items-center mr-2 rounded-md border border-steel-700/50 overflow-hidden">
            <button
              type="button"
              className={`px-3 py-1.5 text-xs flex items-center gap-1.5 transition-colors ${tab === 'vnc' ? 'bg-steel-800 text-forge-300' : 'text-steel-400 hover:text-steel-200'}`}
              onClick={() => setTab('vnc')}
              data-testid="console-tab-vnc"
            >
              <Monitor size={12} /> VNC
            </button>
            <button
              type="button"
              className={`px-3 py-1.5 text-xs flex items-center gap-1.5 transition-colors ${tab === 'serial' ? 'bg-steel-800 text-forge-300' : 'text-steel-400 hover:text-steel-200'}`}
              onClick={() => setTab('serial')}
              data-testid="console-tab-serial"
            >
              <TerminalIcon size={12} /> Serial
            </button>
          </div>
          {tab === 'vnc' && (
            <button
              className="btn-ghost text-xs"
              onClick={sendCtrlAltDel}
              disabled={status !== STATUS_CONNECTED}
              data-testid="console-btn-cad"
              title="Send Ctrl-Alt-Del to the guest"
            >
              <Keyboard size={13} /> Ctrl-Alt-Del
            </button>
          )}
          <button
            className="btn-ghost text-xs"
            onClick={toggleFullscreen}
            data-testid="console-btn-fullscreen"
            title={fullscreen ? 'Exit fullscreen' : 'Fullscreen'}
          >
            {fullscreen ? <Minimize size={13} /> : <Maximize size={13} />} {fullscreen ? 'Exit' : 'Fullscreen'}
          </button>
          <button
            className="btn-ghost text-xs"
            onClick={reconnect}
            data-testid="console-btn-reconnect"
            title="Fetch a fresh ticket and reconnect"
          >
            <RotateCcw size={13} /> Reconnect
          </button>
        </div>
      </div>

      {/* Console area */}
      <div className="relative flex-1 min-h-0">
        <div
          ref={vncContainerRef}
          className={`absolute inset-0 ${tab === 'vnc' ? '' : 'hidden'}`}
          data-testid="console-vnc-container"
        />
        <div
          ref={serialContainerRef}
          className={`absolute inset-0 p-2 ${tab === 'serial' ? '' : 'hidden'}`}
          data-testid="console-serial-container"
        />

        {showOverlay && (
          <div
            className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-steel-950/80 backdrop-blur-sm"
            data-testid="console-overlay"
          >
            {status === STATUS_CONNECTING || status === STATUS_IDLE ? (
              <>
                <Spinner size={22} />
                <p className="text-sm text-steel-400">Connecting to {tab === 'vnc' ? 'VNC' : 'serial'} console…</p>
              </>
            ) : (
              <>
                <p className={`text-sm ${status === STATUS_ERROR ? 'text-red-300' : 'text-steel-300'}`} data-testid="console-overlay-status">
                  {status === STATUS_ERROR ? 'Console unavailable' : 'Disconnected'}
                </p>
                {statusMessage && (
                  <p className="text-xs text-steel-500 max-w-md text-center" data-testid="console-overlay-message">
                    {statusMessage}
                  </p>
                )}
                <button className="btn-primary" onClick={reconnect} data-testid="console-overlay-reconnect">
                  <RotateCcw size={14} /> Reconnect
                </button>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

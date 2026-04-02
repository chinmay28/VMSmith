import { useEffect, useState } from 'react';
import { Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import Dashboard from './pages/Dashboard';
import VMList from './pages/VMList';
import VMDetail from './pages/VMDetail';
import ImageList from './pages/ImageList';
import LogViewer from './pages/LogViewer';
import { APIError, checkAuth, getAPIKey, setAPIKey } from './api/client';

function LoginScreen({ error, busy, onSubmit }) {
  const [value, setValue] = useState(getAPIKey());

  return (
    <div className="min-h-screen bg-steel-950 text-steel-100 flex items-center justify-center px-6">
      <div className="w-full max-w-md rounded-2xl border border-steel-800/80 bg-steel-900/90 p-8 shadow-2xl shadow-black/30">
        <p className="text-xs font-mono uppercase tracking-[0.3em] text-forge-400">VMSmith</p>
        <h1 className="mt-3 text-3xl font-semibold text-white" data-testid="auth-title">API key required</h1>
        <p className="mt-3 text-sm leading-6 text-steel-300">
          This daemon has API authentication enabled. Enter a valid API key to unlock the web UI.
        </p>
        <form
          className="mt-6 space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            onSubmit(value);
          }}
        >
          <label className="block text-sm font-medium text-steel-200" htmlFor="api-key">
            API key
          </label>
          <input
            id="api-key"
            data-testid="auth-api-key"
            type="password"
            value={value}
            onChange={(event) => setValue(event.target.value)}
            placeholder="paste bearer token"
            className="w-full rounded-lg border border-steel-700 bg-steel-950 px-4 py-3 text-sm text-white outline-none ring-0 transition focus:border-forge-500"
            autoFocus
          />
          {error ? (
            <div data-testid="auth-error" className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-200">
              {error}
            </div>
          ) : null}
          <button
            type="submit"
            data-testid="auth-submit"
            disabled={busy || !value.trim()}
            className="w-full rounded-lg bg-forge-500 px-4 py-3 text-sm font-semibold text-steel-950 transition hover:bg-forge-400 disabled:cursor-not-allowed disabled:opacity-60"
          >
            {busy ? 'Checking…' : 'Unlock dashboard'}
          </button>
        </form>
      </div>
    </div>
  );
}

export default function App() {
  const [authState, setAuthState] = useState({ status: 'checking', error: '' });
  const [unlocking, setUnlocking] = useState(false);

  useEffect(() => {
    let cancelled = false;

    async function verify() {
      try {
        const result = await checkAuth();
        if (cancelled) return;
        if (result.requiresAuth) {
          setAuthState({ status: 'locked', error: '' });
          return;
        }
        setAuthState({ status: 'ready', error: '' });
      } catch (err) {
        if (cancelled) return;
        setAuthState({ status: 'ready', error: err instanceof Error ? err.message : 'Unable to reach daemon' });
      }
    }

    verify();
    return () => {
      cancelled = true;
    };
  }, []);

  async function handleUnlock(apiKey) {
    setUnlocking(true);
    setAPIKey(apiKey);
    setAuthState({ status: 'locked', error: '' });
    try {
      const result = await checkAuth();
      if (result.requiresAuth) {
        setAPIKey('');
        setAuthState({ status: 'locked', error: result.error || 'Invalid API key' });
        return;
      }
      setAuthState({ status: 'ready', error: '' });
    } catch (err) {
      if (err instanceof APIError && err.status === 401) {
        setAPIKey('');
        setAuthState({ status: 'locked', error: err.message || 'Invalid API key' });
        return;
      }
      setAuthState({ status: 'locked', error: err instanceof Error ? err.message : 'Unable to reach daemon' });
    } finally {
      setUnlocking(false);
    }
  }

  if (authState.status === 'checking') {
    return (
      <div className="min-h-screen bg-steel-950 text-steel-100 flex items-center justify-center" data-testid="auth-loading">
        <p className="text-sm font-mono text-steel-400">Checking daemon access…</p>
      </div>
    );
  }

  if (authState.status === 'locked') {
    return <LoginScreen busy={unlocking} error={authState.error} onSubmit={handleUnlock} />;
  }

  return (
    <Layout>
      <Routes>
        <Route path="/" element={<Dashboard />} />
        <Route path="/vms" element={<VMList />} />
        <Route path="/vms/:id" element={<VMDetail />} />
        <Route path="/images" element={<ImageList />} />
        <Route path="/logs" element={<LogViewer />} />
      </Routes>
    </Layout>
  );
}

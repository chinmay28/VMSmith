import { useEffect, useMemo, useState } from 'react';
import { KeyRound, Lock, ShieldCheck } from 'lucide-react';
import { vms } from '../api/client';
import {
  clearAuthRequirement,
  getAuthState,
  setAuthToken,
  subscribeAuth,
} from '../auth';
import { Spinner } from './Shared';

function useAuthState() {
  const [authState, setAuthState] = useState(getAuthState());

  useEffect(() => subscribeAuth(() => setAuthState(getAuthState())), []);

  return authState;
}

export default function AuthGate({ children }) {
  const authState = useAuthState();
  const [draftToken, setDraftToken] = useState(authState.token);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState('');

  useEffect(() => {
    setDraftToken(authState.token);
  }, [authState.token]);

  const message = useMemo(
    () => authState.authError || submitError || 'Enter an API key to access the VM Smith dashboard.',
    [authState.authError, submitError],
  );

  async function handleSubmit(event) {
    event.preventDefault();
    const token = draftToken.trim();
    if (!token) {
      setSubmitError('API key is required');
      return;
    }

    setSubmitting(true);
    setSubmitError('');
    setAuthToken(token);

    try {
      await vms.list();
      clearAuthRequirement();
    } catch (err) {
      setSubmitError(err.message || 'Authentication failed');
    } finally {
      setSubmitting(false);
    }
  }

  if (!authState.authRequired) {
    return children;
  }

  return (
    <div className="min-h-screen flex items-center justify-center px-4">
      <div className="w-full max-w-md card p-6 border-steel-700/70 shadow-2xl">
        <div className="flex items-center gap-3 mb-5">
          <div className="w-11 h-11 rounded-xl bg-forge-950/80 border border-forge-700/60 flex items-center justify-center text-forge-300">
            <Lock size={20} />
          </div>
          <div>
            <p className="text-xs font-mono uppercase tracking-[0.18em] text-forge-500">Authentication Required</p>
            <h1 className="font-display font-semibold text-xl text-steel-100">Unlock VM Smith</h1>
          </div>
        </div>

        <p className="text-sm text-steel-400 mb-5" data-testid="auth-message">{message}</p>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="label" htmlFor="api-key">API Key</label>
            <div className="relative">
              <KeyRound size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-steel-500" />
              <input
                id="api-key"
                type="password"
                className="input pl-10"
                value={draftToken}
                onChange={(event) => setDraftToken(event.target.value)}
                autoFocus
                autoComplete="current-password"
                data-testid="auth-token-input"
                placeholder="Paste daemon API key"
              />
            </div>
          </div>

          <button type="submit" className="btn-primary w-full justify-center" disabled={submitting} data-testid="auth-submit">
            {submitting ? <Spinner size={16} /> : <ShieldCheck size={16} />}
            {submitting ? 'Checking key…' : 'Continue'}
          </button>
        </form>
      </div>
    </div>
  );
}

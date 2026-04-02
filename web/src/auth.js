const STORAGE_KEY = 'vmsmith.apiKey';

let state = {
  token: typeof window !== 'undefined' ? window.localStorage.getItem(STORAGE_KEY) || '' : '',
  authRequired: false,
  authError: '',
};

const listeners = new Set();

function emit() {
  for (const listener of listeners) listener();
}

export function subscribeAuth(listener) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getAuthState() {
  return state;
}

export function getAuthToken() {
  return state.token;
}

export function setAuthToken(token) {
  const trimmed = token.trim();
  state = { ...state, token: trimmed, authRequired: false, authError: '' };
  if (typeof window !== 'undefined') {
    if (trimmed) {
      window.localStorage.setItem(STORAGE_KEY, trimmed);
    } else {
      window.localStorage.removeItem(STORAGE_KEY);
    }
  }
  emit();
}

export function clearAuthToken() {
  state = { ...state, token: '', authRequired: true };
  if (typeof window !== 'undefined') {
    window.localStorage.removeItem(STORAGE_KEY);
  }
  emit();
}

export function requireAuth(message = 'API key required') {
  state = { ...state, authRequired: true, authError: message };
  emit();
}

export function clearAuthRequirement() {
  state = { ...state, authRequired: false, authError: '' };
  emit();
}

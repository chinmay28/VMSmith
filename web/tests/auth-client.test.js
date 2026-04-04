import test from 'node:test';
import assert from 'node:assert/strict';

function createLocalStorage() {
  const store = new Map();
  return {
    getItem(key) {
      return store.has(key) ? store.get(key) : null;
    },
    setItem(key, value) {
      store.set(key, String(value));
    },
    removeItem(key) {
      store.delete(key);
    },
    clear() {
      store.clear();
    },
  };
}

global.window = { localStorage: createLocalStorage() };

const auth = await import('../src/auth.js');
const client = await import('../src/api/client.js');

function jsonResponse(status, body) {
  return {
    status,
    ok: status >= 200 && status < 300,
    async json() {
      return body;
    },
  };
}

test.beforeEach(() => {
  auth.setAuthToken('');
  auth.clearAuthRequirement();
  window.localStorage.clear();
  global.fetch = undefined;
});

test('request adds Authorization header when API key is set', async () => {
  auth.setAuthToken('secret-token');

  let headers;
  global.fetch = async (_url, options) => {
    headers = options.headers;
    return jsonResponse(200, []);
  };

  await client.vms.list();

  assert.equal(headers.Authorization, 'Bearer secret-token');
  assert.equal(headers['Content-Type'], 'application/json');
});

test('401 without token marks auth as required', async () => {
  global.fetch = async () => jsonResponse(401, { error: 'API key required' });

  await assert.rejects(() => client.vms.list(), /API key required/);

  assert.equal(auth.getAuthState().authRequired, true);
  assert.equal(auth.getAuthState().authError, 'API key required');
});

test('401 with invalid token clears stored token and keeps auth prompt open', async () => {
  auth.setAuthToken('bad-token');

  global.fetch = async () => jsonResponse(401, { error: 'invalid api key' });

  await assert.rejects(() => client.vms.list(), /invalid api key/);

  assert.equal(auth.getAuthToken(), '');
  assert.equal(window.localStorage.getItem('vmsmith.apiKey'), null);
  assert.equal(auth.getAuthState().authRequired, true);
  assert.equal(auth.getAuthState().authError, 'invalid api key');
});

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
global.fetch = async (url) => ({
  ok: true,
  status: 200,
  headers: {
    get(name) {
      if (name === 'X-Total-Count') return '42';
      return null;
    },
  },
  async json() {
    return [{ id: 'vm-1', name: 'alpha' }];
  },
});

const client = await import('../src/api/client.js');

test('vms.list sends pagination params and returns total count metadata', async () => {
  let requestedUrl = '';
  global.fetch = async (url) => {
    requestedUrl = url;
    return {
      ok: true,
      status: 200,
      headers: {
        get(name) {
          return name === 'X-Total-Count' ? '42' : null;
        },
      },
      async json() {
        return [{ id: 'vm-1', name: 'alpha' }];
      },
    };
  };

  const result = await client.vms.list({ tag: 'prod', page: 3, perPage: 10 });

  assert.equal(requestedUrl, '/api/v1/vms?tag=prod&page=3&per_page=10');
  assert.equal(result.meta.totalCount, 42);
  assert.equal(result.data[0].name, 'alpha');
});

test('images.list sends pagination params and returns total count metadata', async () => {
  let requestedUrl = '';
  global.fetch = async (url) => {
    requestedUrl = url;
    return {
      ok: true,
      status: 200,
      headers: {
        get(name) {
          return name === 'X-Total-Count' ? '7' : null;
        },
      },
      async json() {
        return [{ id: 'img-1', name: 'jammy' }];
      },
    };
  };

  const result = await client.images.list({ page: 2, perPage: 5 });

  assert.equal(requestedUrl, '/api/v1/images?page=2&per_page=5');
  assert.equal(result.meta.totalCount, 7);
  assert.equal(result.data[0].name, 'jammy');
});

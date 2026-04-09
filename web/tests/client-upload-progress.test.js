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
global.FormData = class FormDataMock {
  constructor() {
    this.data = [];
  }
  append(key, value) {
    this.data.push([key, value]);
  }
};
global.XMLHttpRequest = class FakeXHR {
  constructor() {
    this.headers = {};
    this.upload = {};
    FakeXHR.instances.push(this);
  }
  open(method, url) {
    this.method = method;
    this.url = url;
  }
  setRequestHeader(name, value) {
    this.headers[name] = value;
  }
  send(body) {
    this.body = body;
    if (this.upload.onprogress) {
      this.upload.onprogress({ lengthComputable: true, loaded: 25, total: 100 });
      this.upload.onprogress({ lengthComputable: true, loaded: 100, total: 100 });
    }
    this.status = 200;
    this.responseText = JSON.stringify({ id: 'img-1', name: 'demo' });
    this.onload();
  }
};
global.XMLHttpRequest.instances = [];

const auth = await import('../src/auth.js');
const client = await import('../src/api/client.js');

test.beforeEach(() => {
  auth.setAuthToken('');
  auth.clearAuthRequirement();
  window.localStorage.clear();
  global.XMLHttpRequest.instances.length = 0;
});

test('image upload reports progress and sends auth header', async () => {
  auth.setAuthToken('secret-token');
  const progress = [];
  const file = { name: 'disk.qcow2', size: 100 };

  const res = await client.images.upload(file, 'disk', (evt) => progress.push(evt.percent));

  assert.equal(res.id, 'img-1');
  assert.deepEqual(progress, [25, 100]);
  assert.equal(global.XMLHttpRequest.instances[0].method, 'POST');
  assert.equal(global.XMLHttpRequest.instances[0].url, '/api/v1/images/upload');
  assert.equal(global.XMLHttpRequest.instances[0].headers.Authorization, 'Bearer secret-token');
});

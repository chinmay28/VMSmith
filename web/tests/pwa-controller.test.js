import test from 'node:test';
import assert from 'node:assert/strict';
import { createPwaController, detectStandalone } from '../src/pwa/controller.js';

// --- fakes -----------------------------------------------------------------

class FakeWorker extends EventTarget {
  constructor(state = 'installing') {
    super();
    this.state = state;
    this.messages = [];
  }
  postMessage(msg) { this.messages.push(msg); }
  transition(state) {
    this.state = state;
    this.dispatchEvent(new Event('statechange'));
  }
}

class FakeRegistration extends EventTarget {
  constructor({ waiting = null, installing = null } = {}) {
    super();
    this.waiting = waiting;
    this.installing = installing;
    this.updateCalls = 0;
  }
  async update() { this.updateCalls += 1; }
}

function makeEnv({ controller = null, registration = new FakeRegistration(), registerError = null,
  onLine = true, secure = true, standaloneMedia = false, withSW = true } = {}) {
  const win = new EventTarget();
  win.isSecureContext = secure;
  win.reloads = 0;
  win.location = { reload: () => { win.reloads += 1; } };
  win.intervals = [];
  win.setInterval = (fn, ms) => { win.intervals.push({ fn, ms }); return win.intervals.length; };
  win.matchMedia = (q) => ({ matches: standaloneMedia && q === '(display-mode: standalone)' });

  const sw = new EventTarget();
  sw.controller = controller;
  sw.registerCalls = [];
  sw.register = async (url, opts) => {
    sw.registerCalls.push({ url, opts });
    if (registerError) throw registerError;
    return registration;
  };
  const nav = { onLine: onLine, serviceWorker: withSW ? sw : undefined };
  return { win, nav, sw, registration };
}

function installPromptEvent(outcome) {
  const ev = new Event('beforeinstallprompt', { cancelable: true });
  ev.prompted = 0;
  ev.prompt = async () => { ev.prompted += 1; };
  ev.userChoice = Promise.resolve({ outcome });
  return ev;
}

// --- tests -----------------------------------------------------------------

test('registers /sw.js at root scope and reports registered', async () => {
  const env = makeEnv();
  const pwa = createPwaController({ win: env.win, nav: env.nav });
  assert.equal(pwa.getState().supported, true);
  const reg = await pwa.start();
  assert.equal(reg, env.registration);
  assert.deepEqual(env.sw.registerCalls, [{ url: '/sw.js', opts: { scope: '/' } }]);
  assert.equal(pwa.getState().registered, true);
  assert.equal(pwa.getState().updateReady, false);
});

test('start is idempotent', async () => {
  const env = makeEnv();
  const pwa = createPwaController({ win: env.win, nav: env.nav });
  await pwa.start();
  await pwa.start();
  assert.equal(env.sw.registerCalls.length, 1);
});

test('unsupported without serviceWorker or outside a secure context', async () => {
  for (const env of [makeEnv({ withSW: false }), makeEnv({ secure: false })]) {
    const pwa = createPwaController({ win: env.win, nav: env.nav });
    assert.equal(pwa.getState().supported, false);
    assert.equal(await pwa.start(), null);
    assert.equal(env.sw.registerCalls.length, 0);
  }
});

test('works with no browser globals at all (SSR / node import)', async () => {
  const pwa = createPwaController();
  assert.equal(pwa.getState().supported, false);
  assert.equal(pwa.getState().online, true);
  assert.equal(await pwa.start(), null);
});

test('registration failure degrades to a plain web app', async () => {
  const env = makeEnv({ registerError: new Error('SecurityError') });
  const pwa = createPwaController({ win: env.win, nav: env.nav });
  assert.equal(await pwa.start(), null);
  assert.equal(pwa.getState().registered, false);
});

test('a waiting worker at load is an update only when a controller exists', async () => {
  const first = makeEnv({ registration: new FakeRegistration({ waiting: new FakeWorker('installed') }) });
  const p1 = createPwaController({ win: first.win, nav: first.nav });
  await p1.start();
  assert.equal(p1.getState().updateReady, false, 'first install is not an update');

  const upgrade = makeEnv({
    controller: new FakeWorker('activated'),
    registration: new FakeRegistration({ waiting: new FakeWorker('installed') }),
  });
  const p2 = createPwaController({ win: upgrade.win, nav: upgrade.nav });
  await p2.start();
  assert.equal(p2.getState().updateReady, true);
});

test('updatefound -> installed flips updateReady and notifies subscribers', async () => {
  const env = makeEnv({ controller: new FakeWorker('activated') });
  const pwa = createPwaController({ win: env.win, nav: env.nav });
  await pwa.start();
  let notified = 0;
  pwa.subscribe(() => { notified += 1; });

  const next = new FakeWorker('installing');
  env.registration.installing = next;
  env.registration.dispatchEvent(new Event('updatefound'));
  env.registration.waiting = next;
  next.transition('installed');

  assert.equal(pwa.getState().updateReady, true);
  assert.equal(notified, 1);
});

test('applyUpdate posts SKIP_WAITING and reloads exactly once on controllerchange', async () => {
  const waiting = new FakeWorker('installed');
  const env = makeEnv({ controller: new FakeWorker('activated'), registration: new FakeRegistration({ waiting }) });
  const pwa = createPwaController({ win: env.win, nav: env.nav });
  await pwa.start();

  assert.equal(pwa.applyUpdate(), true);
  assert.deepEqual(waiting.messages, [{ type: 'SKIP_WAITING' }]);
  env.sw.dispatchEvent(new Event('controllerchange'));
  env.sw.dispatchEvent(new Event('controllerchange'));
  assert.equal(env.win.reloads, 1);
});

test('controllerchange without an accepted update does not reload (first install claim)', async () => {
  const env = makeEnv();
  const pwa = createPwaController({ win: env.win, nav: env.nav });
  await pwa.start();
  env.sw.dispatchEvent(new Event('controllerchange'));
  assert.equal(env.win.reloads, 0);
});

test('applyUpdate is a no-op without a waiting worker; dismissUpdate hides the prompt', async () => {
  const env = makeEnv({
    controller: new FakeWorker('activated'),
    registration: new FakeRegistration({ waiting: new FakeWorker('installed') }),
  });
  const pwa = createPwaController({ win: env.win, nav: env.nav });
  await pwa.start();
  pwa.dismissUpdate();
  assert.equal(pwa.getState().updateReady, false);
  env.registration.waiting = null;
  assert.equal(pwa.applyUpdate(), false);
});

test('schedules periodic update checks', async () => {
  const env = makeEnv();
  const pwa = createPwaController({ win: env.win, nav: env.nav, updateIntervalMs: 1000 });
  await pwa.start();
  assert.equal(env.win.intervals.length, 1);
  assert.equal(env.win.intervals[0].ms, 1000);
  env.win.intervals[0].fn();
  assert.equal(env.registration.updateCalls, 1);
});

test('tracks online/offline transitions', async () => {
  const env = makeEnv({ onLine: false });
  const pwa = createPwaController({ win: env.win, nav: env.nav });
  assert.equal(pwa.getState().online, false);
  await pwa.start();
  env.win.dispatchEvent(new Event('online'));
  assert.equal(pwa.getState().online, true);
  env.win.dispatchEvent(new Event('offline'));
  assert.equal(pwa.getState().online, false);
});

test('captures the install prompt and replays it once', async () => {
  const env = makeEnv();
  const pwa = createPwaController({ win: env.win, nav: env.nav });
  await pwa.start();
  assert.equal(await pwa.promptInstall(), 'unavailable');

  const ev = installPromptEvent('accepted');
  env.win.dispatchEvent(ev);
  assert.equal(ev.defaultPrevented, true, 'mini-infobar suppressed');
  assert.equal(pwa.getState().installable, true);

  assert.equal(await pwa.promptInstall(), 'accepted');
  assert.equal(ev.prompted, 1);
  assert.equal(pwa.getState().installable, false);
  assert.equal(await pwa.promptInstall(), 'unavailable', 'a prompt can only be used once');
});

test('dismissed install prompt reports dismissed; appinstalled marks standalone', async () => {
  const env = makeEnv();
  const pwa = createPwaController({ win: env.win, nav: env.nav });
  await pwa.start();
  env.win.dispatchEvent(installPromptEvent('dismissed'));
  assert.equal(await pwa.promptInstall(), 'dismissed');
  env.win.dispatchEvent(new Event('appinstalled'));
  assert.equal(pwa.getState().standalone, true);
  assert.equal(pwa.getState().installable, false);
});

test('getState returns a stable snapshot until something changes', async () => {
  const env = makeEnv();
  const pwa = createPwaController({ win: env.win, nav: env.nav });
  const before = pwa.getState();
  env.win.dispatchEvent(new Event('online')); // not started: no listeners yet
  assert.equal(pwa.getState(), before);
  await pwa.start();
  const afterStart = pwa.getState();
  env.win.dispatchEvent(new Event('online')); // already online: no change
  assert.equal(pwa.getState(), afterStart);
});

test('detectStandalone covers display-mode and iOS navigator.standalone', () => {
  assert.equal(detectStandalone(makeEnv({ standaloneMedia: true }).win, {}), true);
  assert.equal(detectStandalone(makeEnv().win, { standalone: true }), true);
  assert.equal(detectStandalone(makeEnv().win, {}), false);
  assert.equal(detectStandalone(undefined, undefined), false);
});

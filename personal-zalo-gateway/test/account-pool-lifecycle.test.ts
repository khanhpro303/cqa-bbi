import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import fs from 'node:fs/promises';
import { createRequire } from 'node:module';
import os from 'node:os';
import path from 'node:path';
import test, { type TestContext } from 'node:test';
import { GatewayStore } from '../src/store/file-store.js';
import { ZaloAccountPool } from '../src/zalo/account-pool.js';

const { Zalo } = createRequire(import.meta.url)('zca-js');
const session = { cookie: [], imei: 'lifecycle-device', userAgent: 'lifecycle-agent' };

class LifecycleListener extends EventEmitter {
  retryOnClose?: boolean;
  stopped = 0;

  start(options: { retryOnClose: boolean }) { this.retryOnClose = options.retryOnClose; }
  requestOldMessages() {}
  stop() { this.stopped++; }

  async close(code: number) {
    for (const callback of this.rawListeners('closed')) {
      await callback.call(this, code, 'lifecycle test');
    }
  }
}

function api(ownId: string, listener = new LifecycleListener()) {
  return {
    listener,
    getOwnId: async () => ownId,
    getUserInfo: async () => ({}),
    getGroupInfo: async () => ({ gridInfoMap: {} }),
    sendMessage: async () => ({}),
  };
}

async function fixture(t: TestContext) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'pzgw-lifecycle-'));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const store = new GatewayStore(path.join(dir, 'state.json'));
  await store.init();
  const account = await store.saveAccount({
    tenantId: 'lifecycle-tenant', channelId: 'lifecycle-channel',
    importEndpoint: 'http://localhost/internal/import', importSecret: 'lifecycle-secret',
  });
  await store.setAccountRuntime(account.id, { sessionData: session });
  return { account, store, pool: new ZaloAccountPool(store) };
}

test('deduplicates concurrent reconnect and QR login attempts', async (t) => {
  const { account, pool, store } = await fixture(t);
  let finishReconnect!: (value: unknown) => void;
  let finishQR!: (value: unknown) => void;
  const reconnectPending = new Promise((resolve) => { finishReconnect = resolve; });
  const qrPending = new Promise((resolve) => { finishQR = resolve; });
  const login = t.mock.method(Zalo.prototype, 'login', () => reconnectPending);
  const loginQR = t.mock.method(Zalo.prototype, 'loginQR', () => qrPending);

  const reconnectOne = pool.reconnect(account.id, session);
  const reconnectTwo = pool.reconnect(account.id, session);
  assert.strictEqual(reconnectOne, reconnectTwo);
  finishReconnect(api('10001'));
  await reconnectOne;
  assert.equal(login.mock.callCount(), 1);

  const qrOne = pool.loginQR(account.id);
  const qrTwo = pool.loginQR(account.id);
  assert.strictEqual(qrOne, qrTwo);
  finishQR(api('10002'));
  await qrOne;
  assert.equal(loginQR.mock.callCount(), 1);
  assert.equal((await store.getAccount(account.id))?.zaloUid, '10002');
});

test('a newer QR login fences a stale reconnect completion', async (t) => {
  const { account, pool, store } = await fixture(t);
  let finishReconnect!: (value: unknown) => void;
  const reconnectPending = new Promise((resolve) => { finishReconnect = resolve; });
  t.mock.method(Zalo.prototype, 'login', () => reconnectPending);
  t.mock.method(Zalo.prototype, 'loginQR', async () => api('20002'));

  const reconnect = pool.reconnect(account.id, session);
  await pool.loginQR(account.id);
  finishReconnect(api('stale-account'));
  await reconnect;

  const saved = await store.getAccount(account.id);
  assert.equal(saved?.zaloUid, '20002');
  assert.equal(saved?.status, 'connected');
});

test('manual reconnect cancels scheduled reconnect and owns retry policy', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { account, pool } = await fixture(t);
  const firstListener = new LifecycleListener();
  const secondListener = new LifecycleListener();
  let calls = 0;
  const login = t.mock.method(Zalo.prototype, 'login', async () =>
    ++calls === 1 ? api('30003', firstListener) : api('30003', secondListener));

  await pool.reconnect(account.id, session);
  assert.equal(firstListener.retryOnClose, false);
  firstListener.emit('connected');
  await firstListener.close(1006);
  assert.equal((await pool.healthSnapshot()).scheduledReconnects, 1);

  await pool.reconnect(account.id, session);
  assert.equal((await pool.healthSnapshot()).scheduledReconnects, 0);
  t.mock.timers.tick(30_000);
  await Promise.resolve();
  assert.equal(login.mock.callCount(), 2);
});

test('non-retryable close requires QR and shutdown stops active listeners', async (t) => {
  const { account, pool, store } = await fixture(t);
  const listener = new LifecycleListener();
  t.mock.method(Zalo.prototype, 'login', async () => api('40004', listener));
  await pool.reconnect(account.id, session);

  await listener.close(1008);
  assert.equal((await store.getAccount(account.id))?.status, 'qr_pending');
  assert.equal((await pool.healthSnapshot()).scheduledReconnects, 0);

  await pool.shutdown(100);
  assert.equal(listener.stopped, 1);
  assert.equal((await pool.healthSnapshot()).shuttingDown, true);
});

test('failed saved session is cleared so restart does not loop login', async (t) => {
  const { account, pool, store } = await fixture(t);
  const login = t.mock.method(Zalo.prototype, 'login', async () => {
    throw new Error('saved session rejected');
  });

  await pool.reconnect(account.id, session);
  assert.equal((await store.getAccount(account.id))?.sessionData, undefined);
  await new ZaloAccountPool(store).bootstrapReconnects();
  assert.equal(login.mock.callCount(), 1);
});

test('declined QR clears the attempt and allows a fresh manual QR', async (t) => {
  const { account, pool, store } = await fixture(t);
  const loginQR = t.mock.method(Zalo.prototype, 'loginQR', async (_options: unknown, callback: (event: unknown) => Promise<void>) => {
    await callback({ type: 0, data: { image: 'temporary-qr' } });
    await callback({ type: 3, actions: { abort() {} } });
    throw new Error('QR declined');
  });

  await pool.loginQR(account.id);
  const declined = await store.getAccount(account.id);
  assert.equal(declined?.lastError, 'qr_declined');
  assert.equal(declined?.qrImage, '');
  assert.equal(declined?.sessionData, undefined);

  await pool.loginQR(account.id);
  assert.equal(loginQR.mock.callCount(), 2);
});

import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import Fastify from 'fastify';
import { GatewayStore } from '../src/store/file-store.js';

const adminSecret = 'gateway-admin-test-secret-at-least-32-bytes';
process.env.PERSONAL_ZALO_GATEWAY_ADMIN_SECRET = adminSecret;
const { registerAccountRoutes } = await import('../src/routes/accounts.js');

async function fixture(sendSessionNotice: (...args: unknown[]) => Promise<string> = async () => 'sent') {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'pzgw-routes-'));
  const store = new GatewayStore(path.join(dir, 'state.json'));
  await store.init();
  const account = await store.saveAccount({
    tenantId: 'tenant-1',
    channelId: 'channel-1',
    importEndpoint: 'http://app:8080/api/internal/imports/personal-zalo',
    importSecret: 'channel-import-secret',
    accountExternalId: 'zalo-1',
    displayName: 'Zalo account',
  });
  await store.setAccountRuntime(account.id, {
    status: 'disconnected',
    sessionData: { cookie: [{ key: 'secret-cookie' }], imei: 'device-1', userAgent: 'agent' },
  });
  const app = Fastify({ logger: false });
  await registerAccountRoutes(app, {
    store,
    syncService: { syncAccount: async () => undefined } as never,
    pool: {
      disconnect: () => undefined,
      loginQR: async () => undefined,
      reconnect: async () => undefined,
      sendSessionNotice,
    } as never,
  });
  return { app, account };
}

test('management routes fail closed without a valid bearer token', async () => {
  const { app } = await fixture();
  try {
    const missing = await app.inject({ method: 'GET', url: '/api/v1/accounts' });
    assert.equal(missing.statusCode, 401);
    const invalid = await app.inject({
      method: 'GET', url: '/api/v1/accounts', headers: { authorization: 'Bearer wrong' },
    });
    assert.equal(invalid.statusCode, 401);
  } finally {
    await app.close();
  }
});

test('authenticated account responses expose only operational fields and hasSession', async () => {
  const { app } = await fixture();
  try {
    const response = await app.inject({
      method: 'GET',
      url: '/api/v1/accounts',
      headers: { authorization: `Bearer ${adminSecret}` },
    });
    assert.equal(response.statusCode, 200);
    const account = response.json().accounts[0];
    assert.equal(account.hasSession, true);
    assert.equal(account.tenantId, 'tenant-1');
    for (const sensitive of ['importSecret', 'importEndpoint', 'sessionData', 'threads']) {
      assert.equal(sensitive in account, false, `${sensitive} must be redacted`);
    }
  } finally {
    await app.close();
  }
});

test('create responses are authenticated and redacted', async () => {
  const { app } = await fixture();
  try {
    const response = await app.inject({
      method: 'POST',
      url: '/api/v1/accounts',
      headers: { authorization: `Bearer ${adminSecret}` },
      payload: {
        tenantId: 'tenant-2',
        channelId: 'channel-2',
        importEndpoint: 'http://app:8080/api/internal/imports/personal-zalo',
        importSecret: 'another-import-secret',
      },
    });
    assert.equal(response.statusCode, 201);
    const account = response.json();
    assert.equal(account.tenantId, 'tenant-2');
    assert.equal(account.hasSession, false);
    assert.equal('importSecret' in account, false);
    assert.equal('importEndpoint' in account, false);
    assert.equal('threads' in account, false);
  } finally {
    await app.close();
  }
});

test('an existing account binding cannot be repointed to another channel', async () => {
  const { app, account } = await fixture();
  try {
    const response = await app.inject({
      method: 'PUT',
      url: `/api/v1/accounts/${account.id}`,
      headers: { authorization: `Bearer ${adminSecret}` },
      payload: { tenantId: account.tenantId, channelId: 'another-channel' },
    });
    assert.equal(response.statusCode, 409);
    assert.equal(response.json().error, 'account_binding_immutable');
  } finally {
    await app.close();
  }
});

test('session notices are authenticated, validated and passed to the pool', async () => {
  const calls: unknown[][] = [];
  const { app, account } = await fixture(async (...args) => {
    calls.push(args);
    return 'skipped_manual';
  });
  try {
    const url = `/api/v1/accounts/${account.id}/chatbot/session-notice`;
    const payload = { threadType: 'group', threadId: '50005', text: ' Hẹn gặp lại ', idleSince: '2026-11-28T10:00:00Z' };
    assert.equal((await app.inject({ method: 'POST', url, payload })).statusCode, 401);
    const headers = { authorization: `Bearer ${adminSecret}` };
    for (const invalid of [{ ...payload, threadType: 'room' }, { ...payload, threadId: 'abc' }, { ...payload, text: ' ' },
      { ...payload, idleSince: 'yesterday' }]) {
      assert.equal((await app.inject({ method: 'POST', url, headers, payload: invalid })).statusCode, 400);
    }
    const response = await app.inject({ method: 'POST', url, headers, payload });
    assert.equal(response.statusCode, 200);
    assert.deepEqual(response.json(), { status: 'skipped_manual' });
    assert.deepEqual(calls, [[account.id, {
      threadType: 'group', threadId: '50005', text: 'Hẹn gặp lại', idleSince: Date.parse('2026-11-28T10:00:00Z'),
    }]]);
  } finally {
    await app.close();
  }
});

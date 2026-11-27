import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import Fastify from 'fastify';
import { registerHealthRoutes, type GatewayRuntimeState } from '../src/health.js';
import { GatewayStore } from '../src/store/file-store.js';
import type { SyncHealthSnapshot } from '../src/sync/sync-service.js';
import type { PoolHealthSnapshot } from '../src/zalo/account-pool.js';

async function fixture() {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'pzgw-health-'));
  const store = new GatewayStore(path.join(dir, 'state.json'));
  await store.init();
  const account = await store.saveAccount({
    tenantId: 'tenant-secret', channelId: 'channel-secret', importEndpoint: 'http://cqa.test', importSecret: 'import-secret',
  });
  await store.recordIncomingMessage(account.id, {
    senderUid: 'customer-secret', senderName: 'Alice', content: 'hello', contentType: 'text', msgId: 'message-secret',
    timestamp: Date.now(), isSelf: false, threadId: 'customer-secret', threadType: 'user',
  });
  const pool: PoolHealthSnapshot = {
    shuttingDown: false, totalAccounts: 1, connectedAccounts: 1, connectingAccounts: 0, disconnectedAccounts: 0,
    qrPendingAccounts: 0, scheduledReconnects: 0, activeChatbotTurns: 0, queuedChatbotTurns: 0,
    unknownChatbotTurns: 0, failedChatbotTurns: 0,
  };
  const sync: SyncHealthSnapshot = {
    running: true, shuttingDown: false, inFlight: 0, failingAccounts: 0, attempts: 1, failures: 0,
    lastSuccessAt: new Date().toISOString(),
  };
  const runtime: GatewayRuntimeState = { started: true, shuttingDown: false };
  const app = Fastify();
  registerHealthRoutes(app, store, { healthSnapshot: async () => pool } as never, { healthSnapshot: () => sync } as never, runtime);
  return { app, store, account, pool, sync, runtime };
}

test('liveness remains independent from account readiness', async () => {
  const { app, pool } = await fixture();
  pool.connectedAccounts = 0;
  pool.disconnectedAccounts = 1;
  const response = await app.inject({ method: 'GET', url: '/health' });
  assert.equal(response.statusCode, 200);
  assert.equal(response.json().status, 'ok');
});

test('readiness returns safe aggregate operational state', async () => {
  const { app } = await fixture();
  const response = await app.inject({ method: 'GET', url: '/ready' });
  assert.equal(response.statusCode, 200);
  const payload = response.json();
  assert.equal(payload.ready, true);
  assert.equal(payload.accounts.pendingConversations, 1);
  assert.equal(payload.accounts.pendingMessages, 1);
  const serialized = response.body;
  for (const secret of ['tenant-secret', 'channel-secret', 'import-secret', 'customer-secret', 'message-secret']) {
    assert.equal(serialized.includes(secret), false);
  }
});

test('readiness degrades for disconnected accounts, blocked sync or shutdown', async () => {
  const { app, store, account, pool, runtime } = await fixture();
  pool.connectedAccounts = 0;
  pool.disconnectedAccounts = 1;
  let response = await app.inject({ method: 'GET', url: '/ready' });
  assert.equal(response.statusCode, 503);

  pool.connectedAccounts = 1;
  pool.disconnectedAccounts = 0;
  await store.setAccountRuntime(account.id, { syncBlockedReason: 'target unavailable' });
  response = await app.inject({ method: 'GET', url: '/ready' });
  assert.equal(response.statusCode, 503);

  await store.setAccountRuntime(account.id, { syncBlockedReason: undefined });
  runtime.shuttingDown = true;
  response = await app.inject({ method: 'GET', url: '/ready' });
  assert.equal(response.statusCode, 503);
});

test('readiness reports actionable chatbot delivery failures', async () => {
  const { app, pool } = await fixture();
  pool.failedChatbotTurns = 1;
  const response = await app.inject({ method: 'GET', url: '/ready' });
  assert.equal(response.statusCode, 503);
  assert.equal(response.json().pool.failedChatbotTurns, 1);
});

test('readiness waits for the live socket even when persisted accounts appear connected', async () => {
  const { app, pool } = await fixture();
  pool.connectedAccounts = 0;
  const response = await app.inject({ method: 'GET', url: '/ready' });
  assert.equal(response.statusCode, 503);
  await app.close();
});

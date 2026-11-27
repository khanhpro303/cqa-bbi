import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import Fastify from 'fastify';
import { GatewayStore } from '../src/store/file-store.js';

test('operators can reconcile only terminal delivery issues, without exposing reply bodies or resending', async (t) => {
  const token = randomBytes(32).toString('hex');
  process.env.PERSONAL_ZALO_GATEWAY_ADMIN_SECRET = token;
  const { registerAccountRoutes } = await import('../src/routes/accounts.js');
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'delivery-issues-'));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const store = new GatewayStore(path.join(dir, 'state.json'));
  await store.init();
  const account = await store.saveAccount({ tenantId: 'tenant', channelId: 'channel', importEndpoint: '', importSecret: 'secret' });
  const message = { msgId: '10', threadType: 'user' as const, threadId: '20', senderName: '', senderUid: '20',
    isSelf: false, timestamp: Date.now(), content: 'private message', contentType: 'text' };
  const time = new Date().toISOString();
  const scope = { tenantId: account.tenantId, channelId: account.channelId, accountExternalId: account.accountExternalId };
  await store.setAccountRuntime(account.id, { chatbotTurns: {
    expired: { ...scope, message, status: 'failed', createdAt: time, updatedAt: time },
    queued: { ...scope, message, status: 'pending', createdAt: time, updatedAt: time },
  } });
  const app = Fastify();
  t.after(() => app.close());
  await registerAccountRoutes(app, { store, pool: {} as never, syncService: {} as never });
  const url = '/api/v1/accounts/' + account.id + '/delivery-issues';
  const headers = { authorization: 'Bearer ' + token };
  assert.equal((await app.inject({ url })).statusCode, 401);
  const response = await app.inject({ url, headers });
  assert.deepEqual(response.json().issues.map((issue: {key: string}) => issue.key), ['expired']);
  assert.ok(!response.body.includes('private message'));
  const acknowledge = (key: string) => app.inject({ url: url + '/' + key + '/acknowledge', method: 'POST', headers });
  assert.equal((await acknowledge('queued')).statusCode, 404);
  assert.equal((await acknowledge('expired')).statusCode, 204);
  assert.deepEqual((await store.listChatbotTurns(account.id)).map(([key]) => key), ['queued']);
});

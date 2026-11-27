import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { setTimeout as delay } from 'node:timers/promises';
import { GatewayStore } from '../src/store/file-store.js';
import { SyncService } from '../src/sync/sync-service.js';

async function storeWithPendingMessage() {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'pzgw-sync-'));
  const stateFile = path.join(dir, 'state.json');
  const store = new GatewayStore(stateFile);
  await store.init();
  const account = await store.saveAccount({
    tenantId: 'tenant-1',
    channelId: 'deleted-channel',
    importEndpoint: 'http://cqa.test/api/internal/imports/personal-zalo',
    importSecret: 'secret-1',
    accountExternalId: 'zalo-1',
  });
  await store.recordIncomingMessage(account.id, {
    senderUid: 'u-1',
    senderName: 'Alice',
    content: 'hello',
    contentType: 'text',
    msgId: 'm-1',
    timestamp: Date.parse('2026-11-18T12:00:00.000Z'),
    isSelf: false,
    threadId: 'u-1',
    threadType: 'user',
  });
  return { store, account, stateFile };
}

function successfulAcknowledgement(init?: RequestInit, overrides: Record<string, unknown> = {}) {
  const request = JSON.parse(String(init?.body));
  const messages = request.conversations.reduce(
    (total: number, conversation: { messages: unknown[] }) => total + conversation.messages.length,
    0,
  );
  return Response.json({
    request_id: request.request_id,
    conversations: request.conversations.length,
    messages_processed: messages,
    messages_inserted: messages,
    messages_deduplicated: 0,
    ...overrides,
  }, { status: 202 });
}

test('an account whose CQA channel is gone is paused instead of retried every interval', async (t) => {
  const { store, account } = await storeWithPendingMessage();
  let calls = 0;
  let answer = (_init?: RequestInit) => new Response('{"error":"invalid_internal_target"}', { status: 401 });
  t.mock.method(globalThis, 'fetch', async (_request: RequestInfo | URL, init?: RequestInit) => {
    calls++;
    return answer(init);
  });
  const service = new SyncService(store, 60_000, 10, 10, 5_000);

  await service.syncAll();
  assert.equal(calls, 1);
  let stored = await store.getAccount(account.id);
  assert.match(stored?.syncBlockedReason || '', /deleted-channel/);

  await service.syncAll();
  assert.equal(calls, 1, 'a paused account must not be sent again on schedule');

  // CQA revalidates the immutable binding: syncing resumes.
  await store.saveAccount({ ...account }, account.id);
  answer = (init) => successfulAcknowledgement(init);
  await service.syncAll();
  assert.equal(calls, 2);
  stored = await store.getAccount(account.id);
  assert.equal(stored?.syncBlockedReason, undefined);
  assert.equal(await store.nextSyncBatch(account.id, 10, 10), null);
  assert.equal(service.healthSnapshot().failingAccounts, 0);
});

test('other import failures are still retried on the next interval', async (t) => {
  const { store, account } = await storeWithPendingMessage();
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async () => {
    calls++;
    return new Response('{"error":"invalid_internal_signature"}', { status: 401 });
  });
  const service = new SyncService(store, 60_000, 10, 10, 5_000);
  await service.syncAll();
  await service.syncAll();
  assert.equal(calls, 2);
  assert.equal((await store.getAccount(account.id))?.syncBlockedReason, undefined);
});

test('a mismatched acknowledgement keeps the batch pending until CQA confirms the same request', async (t) => {
  const { store, account } = await storeWithPendingMessage();
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async (_request: RequestInfo | URL, init?: RequestInit) => {
    calls++;
    return successfulAcknowledgement(init, calls === 1 ? { request_id: 'another-request' } : {});
  });
  const service = new SyncService(store, 60_000, 10, 10, 5_000);

  await service.syncAll();
  assert.ok(await store.nextSyncBatch(account.id, 10, 10), 'unacknowledged messages must remain durable');
  assert.equal(service.healthSnapshot().failures, 1);

  await service.syncAll();
  assert.equal(calls, 2);
  assert.equal(await store.nextSyncBatch(account.id, 10, 10), null);
  assert.equal(service.healthSnapshot().failingAccounts, 0);
});

test('deleteAccount removes the account', async () => {
  const { store, account } = await storeWithPendingMessage();
  assert.equal(await store.deleteAccount(account.id), true);
  assert.equal(await store.getAccount(account.id), null);
  assert.equal(await store.deleteAccount(account.id), false);
});

test('an inconsistent physical and configured Zalo identity is never synchronized', async (t) => {
  const { store, account, stateFile } = await storeWithPendingMessage();
  const state = JSON.parse(await fs.readFile(stateFile, 'utf8'));
  state.accounts[account.id].zaloUid = 'physical-zalo';
  state.accounts[account.id].accountExternalId = 'configured-zalo';
  await fs.writeFile(stateFile, JSON.stringify(state));
  const fetchMock = t.mock.method(globalThis, 'fetch', async () => Response.json({}));
  const service = new SyncService(store, 60_000, 10, 10, 5_000);

  await service.syncAll();

  assert.equal(fetchMock.mock.callCount(), 0);
  await assert.rejects(store.nextSyncBatch(account.id, 10, 10), /account_identity_mismatch/);
});

test('health snapshot reports sync outcomes without account identifiers', async (t) => {
  const { store, account } = await storeWithPendingMessage();
  t.mock.method(globalThis, 'fetch', async (_request: RequestInfo | URL, init?: RequestInit) => successfulAcknowledgement(init));
  const service = new SyncService(store, 60_000, 10, 10, 5_000);
  t.after(() => service.stop());

  service.start();
  for (let attempt = 0; attempt < 100 && !service.healthSnapshot().lastSuccessAt; attempt++) {
    await delay(10);
  }
  const snapshot = service.healthSnapshot();
  assert.equal(snapshot.running, true);
  assert.equal(snapshot.shuttingDown, false);
  assert.equal(snapshot.attempts, 1);
  assert.equal(snapshot.failures, 0);
  assert.equal(snapshot.failingAccounts, 0);
  assert.ok(snapshot.lastAttemptAt);
  assert.ok(snapshot.lastSuccessAt);
  assert.equal(JSON.stringify(snapshot).includes(account.id), false);

  assert.equal(await service.shutdown(100), true);
  assert.equal(service.healthSnapshot().running, false);
  assert.equal(service.healthSnapshot().shuttingDown, true);
});

test('shutdown stops new sync work and reports a bounded drain timeout', async (t) => {
  const { store, account } = await storeWithPendingMessage();
  await store.recordIncomingMessage(account.id, {
    senderUid: 'u-1', senderName: 'Alice', content: 'second', contentType: 'text', msgId: 'm-2',
    timestamp: Date.parse('2026-11-18T12:01:00.000Z'), isSelf: false, threadId: 'u-1', threadType: 'user',
  });
  let releaseFetch!: () => void;
  const pendingFetch = new Promise<void>((resolve) => { releaseFetch = resolve; });
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async (_request: RequestInfo | URL, init?: RequestInit) => {
    calls++;
    await pendingFetch;
    return successfulAcknowledgement(init);
  });
  const service = new SyncService(store, 60_000, 10, 1, 5_000);
  const sync = service.syncAccount(account.id);
  await delay(10);

  assert.equal(await service.shutdown(10), false);
  await service.syncAccount(account.id);
  assert.equal(calls, 1, 'shutdown must reject new sync work');
  releaseFetch();
  await sync;
  assert.equal(calls, 1, 'shutdown must not start the next pending batch');
  assert.equal(service.healthSnapshot().inFlight, 0);
  assert.ok(await store.nextSyncBatch(account.id, 10, 10), 'unsent batches remain durable for restart');
});

test('a successful idle account does not clear another account failure', async (t) => {
  const { store, account } = await storeWithPendingMessage();
  await store.saveAccount({
    tenantId: 'tenant-2', channelId: 'idle', importEndpoint: 'http://cqa.test', importSecret: 'secret-2',
    accountExternalId: 'zalo-2',
  });
  t.mock.method(globalThis, 'fetch', async () => new Response('{"error":"temporary"}', { status: 500 }));
  const service = new SyncService(store, 60_000, 10, 10, 5_000);

  await service.syncAll();
  const snapshot = service.healthSnapshot();
  assert.equal(snapshot.failingAccounts, 1);
  assert.equal(snapshot.failures, 1);

  await store.deleteAccount(account.id);
  await service.syncAll();
  assert.equal(service.healthSnapshot().failingAccounts, 0, 'deleted accounts must not keep readiness degraded');
});

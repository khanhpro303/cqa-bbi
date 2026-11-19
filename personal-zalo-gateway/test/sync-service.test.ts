import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { GatewayStore } from '../src/store/file-store.js';
import { SyncService } from '../src/sync/sync-service.js';

async function storeWithPendingMessage() {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'pzgw-sync-'));
  const store = new GatewayStore(path.join(dir, 'state.json'));
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
  return { store, account };
}

test('an account whose CQA channel is gone is paused instead of retried every interval', async (t) => {
  const { store, account } = await storeWithPendingMessage();
  let calls = 0;
  let answer = () => new Response('{"error":"invalid_internal_target"}', { status: 401 });
  t.mock.method(globalThis, 'fetch', async () => {
    calls++;
    return answer();
  });
  const service = new SyncService(store, 60_000, 10, 10, 5_000);

  await service.syncAll();
  assert.equal(calls, 1);
  let stored = await store.getAccount(account.id);
  assert.match(stored?.syncBlockedReason || '', /deleted-channel/);

  await service.syncAll();
  assert.equal(calls, 1, 'a paused account must not be sent again on schedule');

  // CQA re-points the account at a valid channel: syncing resumes.
  await store.saveAccount({ ...account, channelId: 'new-channel' }, account.id);
  answer = () => Response.json({ request_id: 'r', conversations: 1, messages_processed: 1, messages_inserted: 1, messages_deduplicated: 0 });
  await service.syncAll();
  assert.equal(calls, 2);
  stored = await store.getAccount(account.id);
  assert.equal(stored?.syncBlockedReason, undefined);
  assert.equal(await store.nextSyncBatch(account.id, 10, 10), null);
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

test('deleteAccount removes the account', async () => {
  const { store, account } = await storeWithPendingMessage();
  assert.equal(await store.deleteAccount(account.id), true);
  assert.equal(await store.getAccount(account.id), null);
  assert.equal(await store.deleteAccount(account.id), false);
});

import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { GatewayStore } from '../src/store/file-store.js';

test('GatewayStore deduplicates messages and builds pending sync batches', async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'pzgw-'));
  const store = new GatewayStore(path.join(dir, 'state.json'));
  await store.init();

  const account = await store.saveAccount({
    tenantId: 'tenant-1',
    channelId: 'channel-1',
    importEndpoint: 'http://localhost:8080/api/internal/imports/personal-zalo',
    importSecret: 'secret-1',
  });

  const inserted = await store.recordIncomingMessage(account.id, {
    senderUid: 'u-1',
    senderName: 'Alice',
    content: 'hello',
    contentType: 'text',
    msgId: 'm-1',
    timestamp: Date.parse('2026-04-08T00:00:00.000Z'),
    isSelf: false,
    threadId: 'u-1',
    threadType: 'user',
  });

  const duplicate = await store.recordIncomingMessage(account.id, {
    senderUid: 'u-1',
    senderName: 'Alice',
    content: 'hello',
    contentType: 'text',
    msgId: 'm-1',
    timestamp: Date.parse('2026-04-08T00:00:00.000Z'),
    isSelf: false,
    threadId: 'u-1',
    threadType: 'user',
  });

  assert.equal(inserted, true);
  assert.equal(duplicate, false);

  const batch = await store.nextSyncBatch(account.id, 10, 10);
  assert.ok(batch);
  assert.equal(batch?.conversations.length, 1);
  assert.equal(batch?.conversations[0]?.messages.length, 1);

  await store.markBatchSynced(account.id, { 'user:u-1': ['m-1'] }, '2026-04-08T00:05:00.000Z');
  const nextBatch = await store.nextSyncBatch(account.id, 10, 10);
  assert.equal(nextBatch, null);
});

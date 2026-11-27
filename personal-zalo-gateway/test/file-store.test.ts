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

test('recoverChatbotTurns never replays an interrupted CQA request or Zalo send', async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'pzgw-recovery-'));
  const store = new GatewayStore(path.join(dir, 'state.json'));
  await store.init();
  const account = await store.saveAccount({
    tenantId: 'tenant-1', channelId: 'channel-1', importEndpoint: 'http://localhost', importSecret: 'secret',
    accountExternalId: '10001',
  });
  const incoming = {
    senderUid: '20002', senderName: 'Alice', content: 'hello', contentType: 'text', msgId: 'recovery-1',
    timestamp: Date.now(), isSelf: false, threadId: '20002', threadType: 'user' as const,
    sourceContentIsString: true, sourceTimestampIsValid: true, rawData: { message_type: 'webchat' },
  };
  await store.recordIncomingMessage(account.id, incoming, true);
  const key = await store.activateChatbotTurn(account.id, incoming);
  assert.ok(key);
  await store.transitionChatbotTurn(account.id, key, 'pending', 'processing');

  await store.recoverChatbotTurns(account.id);

  const turns = await store.listChatbotTurns(account.id);
  assert.equal(turns[0]?.[1].status, 'unknown');
  assert.equal(turns[0]?.[1].lastError, 'gateway_restarted_while_cqa_request_was_in_flight');
});

test('20 replayed synced conversations cannot hide the next unsynced conversation', async (t) => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'pzgw-replay-'));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const statePath = path.join(dir, 'state.json');
  const store = new GatewayStore(statePath);
  await store.init();
  const account = await store.saveAccount({
    tenantId: 'tenant', channelId: 'channel', importEndpoint: 'http://localhost/import', importSecret: 'test',
  });
  const incoming = (index: number) => ({
    senderUid: String(index), senderName: 'customer', threadId: String(index), threadType: 'user' as const,
    timestamp: Date.UTC(2026, 0, 1) + index, msgId: String(index), content: 'test', contentType: 'text', isSelf: false,
  });
  for (let index = 1; index <= 20; index++) await store.recordIncomingMessage(account.id, incoming(index));
  const first = await store.nextSyncBatch(account.id, 20, 200);
  assert.equal(first?.conversations.length, 20);
  await store.markBatchSynced(account.id, Object.fromEntries(first!.conversations.map(
    (conv) => [conv.threadKey, conv.messages.map((message) => message.external_id)],
  )), new Date().toISOString());
  for (let index = 1; index <= 20; index++) await store.recordIncomingMessage(account.id, incoming(index));
  assert.equal((await store.getAccount(account.id))?.threads['user:1'].pending, false);

  // Production volumes may already contain pending flags left behind by older releases.
  const state = JSON.parse(await fs.readFile(statePath, 'utf8'));
  for (const thread of first!.conversations) state.accounts[account.id].threads[thread.threadKey].pending = true;
  await fs.writeFile(statePath, JSON.stringify(state));
  await store.recordIncomingMessage(account.id, incoming(21));
  const next = await store.nextSyncBatch(account.id, 20, 200);
  assert.deepEqual(next?.conversations.map((conv) => conv.threadKey), ['user:21']);
});

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
const session = { cookie: [], imei: 'test-device', userAgent: 'test-agent' };

class TestListener extends EventEmitter {
  retryOnClose?: boolean;
  historyRequests = 0;

  start(options: { retryOnClose: boolean }) {
    this.retryOnClose = options.retryOnClose;
  }

  requestOldMessages() {
    this.historyRequests++;
  }

  async close(code: number) {
    for (const handleClose of this.rawListeners('closed')) {
      await handleClose.call(this, code, 'test close');
    }
  }

  async message(message: unknown) {
    for (const handleMessage of this.rawListeners('message')) {
      await handleMessage.call(this, message);
    }
  }

  async oldMessages(messages: unknown[]) {
    for (const handleOldMessages of this.rawListeners('old_messages')) {
      await handleOldMessages.call(this, messages);
    }
  }

  stop() {}
}

async function connectFixture(
  t: TestContext,
  options: { ownId?: string; sendMessage?: (text: string, threadId: string, type: number) => Promise<unknown> } = {},
) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'pzgw-pool-'));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const stateFile = path.join(dir, 'state.json');
  const store = new GatewayStore(stateFile);
  await store.init();
  const account = await store.saveAccount({
    tenantId: 'test-tenant',
    channelId: 'test-channel',
    importEndpoint: 'http://localhost/api/internal/imports/personal-zalo',
    importSecret: 'test-secret',
  });
  await store.setAccountRuntime(account.id, { sessionData: session });

  const listener = new TestListener();
  const login = t.mock.method(Zalo.prototype, 'login', async () => ({
    listener,
    getOwnId: () => options.ownId || 'test-account',
    getUserInfo: async () => ({}),
    getGroupInfo: async () => ({ gridInfoMap: {} }),
    sendMessage: options.sendMessage || (async () => ({})),
  }));
  const pool = new ZaloAccountPool(store);
  await pool.bootstrapReconnects();
  assert.equal((await store.getAccount(account.id))?.status, 'connected');
  return { account, stateFile, store, listener, login, pool };
}

test('a new direct text requests one CQA reply and sends it to the same Zalo user', async (t) => {
  t.after(() => {
    delete process.env.PERSONAL_ZALO_CHATBOT_BASE_URL;
    delete process.env.PERSONAL_ZALO_CHATBOT_SECRET;
  });
  process.env.PERSONAL_ZALO_CHATBOT_BASE_URL = 'http://app:8080';
  process.env.PERSONAL_ZALO_CHATBOT_SECRET = 'chatbot-deployment-secret-32-bytes!';
  let sent!: { text: string; threadId: string; type: number };
  let notifySent!: () => void;
  const sentPromise = new Promise<void>((resolve) => { notifySent = resolve; });
  const sendMessage = async (text: string, threadId: string, type: number) => {
    sent = { text, threadId, type };
    notifySent();
    return {};
  };
  const fetchMock = t.mock.method(globalThis, 'fetch', async () => new Response(JSON.stringify({ reply: 'Chào bạn' }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  }));
  const { listener } = await connectFixture(t, { ownId: '10001', sendMessage });
  const incoming = {
    type: 0,
    threadId: '20002',
    isSelf: false,
    data: {
      uidFrom: '20002',
      dName: 'Khách hàng',
      content: 'Cho tôi hỏi sản phẩm',
      msgType: 'webchat',
      msgId: '30003',
      ts: String(Date.now()),
    },
  };

  await listener.message(incoming);
  await sentPromise;
  await listener.message(incoming);

  assert.equal(fetchMock.mock.callCount(), 1);
  assert.deepEqual(sent, { text: 'Chào bạn', threadId: '20002', type: 0 });
  const request = fetchMock.mock.calls[0]?.arguments[1] as RequestInit;
  assert.equal(JSON.parse(String(request.body)).account_external_id, '10001');
  assert.equal(JSON.parse(String(request.body)).thread_id, '20002');
});

test('old, group, self and disabled chatbot turns never send a Zalo reply', async (t) => {
  t.after(() => {
    delete process.env.PERSONAL_ZALO_CHATBOT_BASE_URL;
    delete process.env.PERSONAL_ZALO_CHATBOT_SECRET;
  });
  process.env.PERSONAL_ZALO_CHATBOT_BASE_URL = 'http://app:8080';
  process.env.PERSONAL_ZALO_CHATBOT_SECRET = 'chatbot-deployment-secret-32-bytes!';
  const sendMessage = t.mock.fn(async () => ({}));
  let notifyRequested!: () => void;
  const requested = new Promise<void>((resolve) => { notifyRequested = resolve; });
  const fetchMock = t.mock.method(globalThis, 'fetch', async () => {
    notifyRequested();
    return new Response(null, { status: 204 });
  });
  const { listener } = await connectFixture(t, { ownId: '10001', sendMessage });
  const baseMessage = {
    type: 0,
    threadId: '20002',
    isSelf: false,
    data: {
      uidFrom: '20002',
      content: 'Xin chào',
      msgType: 'webchat',
      msgId: '40004',
      ts: String(Date.now()),
    },
  };

  await listener.oldMessages([baseMessage]);
  await listener.message({ ...baseMessage, type: 1, threadId: '50005', data: { ...baseMessage.data, msgId: '40005' } });
  await listener.message({ ...baseMessage, isSelf: true, data: { ...baseMessage.data, msgId: '40006' } });
  await listener.message({ ...baseMessage, data: { ...baseMessage.data, msgId: '40007' } });
  await listener.message({ ...baseMessage, data: { ...baseMessage.data, msgId: '40008', ts: undefined } });
  await listener.message({ ...baseMessage, data: { ...baseMessage.data, msgId: '40009', ts: `${Date.now()}invalid` } });
  await listener.message({ ...baseMessage, data: { ...baseMessage.data, msgId: '40010', content: { text: 'test' } } });
  await requested;

  assert.equal(fetchMock.mock.callCount(), 1, 'only the eligible live message reaches CQA');
  assert.equal(sendMessage.mock.callCount(), 0, 'CQA 204 never sends to Zalo');
});

function configureTestChatbot(t: TestContext): void {
  const originalURL = process.env.PERSONAL_ZALO_CHATBOT_BASE_URL;
  const originalSecret = process.env.PERSONAL_ZALO_CHATBOT_SECRET;
  t.after(() => {
    if (originalURL === undefined) delete process.env.PERSONAL_ZALO_CHATBOT_BASE_URL;
    else process.env.PERSONAL_ZALO_CHATBOT_BASE_URL = originalURL;
    if (originalSecret === undefined) delete process.env.PERSONAL_ZALO_CHATBOT_SECRET;
    else process.env.PERSONAL_ZALO_CHATBOT_SECRET = originalSecret;
  });
  process.env.PERSONAL_ZALO_CHATBOT_BASE_URL = 'http://app:8080';
  process.env.PERSONAL_ZALO_CHATBOT_SECRET = 'chatbot-deployment-secret-32-bytes!';
}

function directMessage(msgId: string, threadId = '20002') {
  return {
    type: 0,
    threadId,
    isSelf: false,
    data: { uidFrom: threadId, content: 'Xin chào', msgType: 'webchat', msgId, ts: String(Date.now()) },
  };
}

for (const change of ['manual reply', 'socket close', 'account reassignment'] as const) {
  test(`suppresses in-flight chatbot reply after ${change}`, { timeout: 2000 }, async (t) => {
    configureTestChatbot(t);
    if (change === 'socket close') t.mock.timers.enable({ apis: ['setTimeout'] });
    const sendMessage = t.mock.fn(async () => ({}));
    const { account, listener, store } = await connectFixture(t, { ownId: '10001', sendMessage });
    let requestStarted!: () => void;
    let finishRequest!: (response: Response) => void;
    const started = new Promise<void>((resolve) => { requestStarted = resolve; });
    const pending = new Promise<Response>((resolve) => { finishRequest = resolve; });
    t.mock.method(globalThis, 'fetch', async () => {
      requestStarted();
      return pending;
    });
    const incoming = directMessage('50001');
    const processing = listener.message(incoming);
    await started;

    if (change === 'manual reply') {
      await listener.message({
        ...incoming, isSelf: true, data: { ...incoming.data, msgId: '50002', uidFrom: '10001', content: 'Nhân viên đã trả lời' },
      });
    } else if (change === 'socket close') {
      await listener.close(1008);
    } else {
      await store.saveAccount({
        tenantId: 'another-tenant',
        channelId: 'another-channel',
        importEndpoint: account.importEndpoint,
        importSecret: account.importSecret,
      }, account.id);
    }

    finishRequest(Response.json({ reply: 'Tin bot đang chờ' }));
    await processing;
    assert.equal(sendMessage.mock.callCount(), 0);
  });
}

test('preserves only the latest follow-up while the conversation is busy', { timeout: 2000 }, async (t) => {
  configureTestChatbot(t);
  const sendMessage = t.mock.fn(async () => ({}));
  const { listener } = await connectFixture(t, { ownId: '10001', sendMessage });
  let requestStarted!: () => void;
  let finishRequest!: (response: Response) => void;
  const started = new Promise<void>((resolve) => { requestStarted = resolve; });
  const pending = new Promise<Response>((resolve) => { finishRequest = resolve; });
  const processed: string[] = [];
  t.mock.method(globalThis, 'fetch', async (_url: string, request: RequestInit) => {
    processed.push(JSON.parse(String(request.body)).message_id);
    if (processed.length === 1) {
      requestStarted();
      return pending;
    }
    return Response.json({ reply: 'Trả lời tin mới nhất' });
  });
  const processing = listener.message(directMessage('60001'));
  await started;
  await listener.message(directMessage('60002'));
  await listener.message(directMessage('60003'));
  finishRequest(new Response(null, { status: 204 }));
  await processing;
  assert.deepEqual(processed, ['60001', '60003']);
  assert.equal(sendMessage.mock.callCount(), 1);
});

test('paces sends even when two direct conversations finish together', { timeout: 6500 }, async (t) => {
  configureTestChatbot(t);
  const sendTimes: number[] = [];
  const sendMessage = async () => { sendTimes.push(Date.now()); };
  const { listener } = await connectFixture(t, { ownId: '10001', sendMessage });
  t.mock.method(globalThis, 'fetch', async () => Response.json({ reply: 'Chào bạn' }));
  await Promise.all([listener.message(directMessage('70001')), listener.message(directMessage('70002', '20003'))]);
  assert.equal(sendTimes.length, 2);
  assert.ok(sendTimes[1]! - sendTimes[0]! >= 3000);
});

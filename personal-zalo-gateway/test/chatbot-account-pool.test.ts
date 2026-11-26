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

test('old, self, malformed and disabled chatbot turns never send a Zalo reply', async (t) => {
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

function groupMessage(msgId: string, senderId = '20002', threadId = '50005') {
  return {
    type: 1,
    threadId,
    isSelf: false,
    data: { uidFrom: senderId, content: 'bot ơi tư vấn giúp', msgType: 'webchat', msgId, ts: String(Date.now()) },
  };
}

test('a new group text is signed with sender identity and replies to the originating group', async (t) => {
  configureTestChatbot(t);
  let sent!: { text: string; threadId: string; type: number };
  const sendMessage = async (text: string, threadId: string, type: number) => {
    sent = { text, threadId, type };
  };
  const fetchMock = t.mock.method(globalThis, 'fetch', async () => Response.json({ reply: 'Chào cả nhóm' }));
  const { listener } = await connectFixture(t, { ownId: '10001', sendMessage });

  await listener.message(groupMessage('41001'));

  assert.deepEqual(sent, { text: 'Chào cả nhóm', threadId: '50005', type: 1 });
  const request = fetchMock.mock.calls[0]?.arguments[1] as RequestInit;
  const payload = JSON.parse(String(request.body));
  assert.equal(payload.thread_type, 'group');
  assert.equal(payload.thread_id, '50005');
  assert.equal(payload.sender_id, '20002');
});

test('a manual group reply cancels pending bot replies for every sender in that group', { timeout: 2000 }, async (t) => {
  configureTestChatbot(t);
  const sendMessage = t.mock.fn(async () => ({}));
  const { listener } = await connectFixture(t, { ownId: '10001', sendMessage });
  let startedCount = 0;
  let notifyStarted!: () => void;
  let finishRequests!: (response: Response) => void;
  const started = new Promise<void>((resolve) => { notifyStarted = resolve; });
  const pending = new Promise<Response>((resolve) => { finishRequests = resolve; });
  t.mock.method(globalThis, 'fetch', async () => {
    startedCount++;
    if (startedCount === 2) notifyStarted();
    return (await pending).clone();
  });

  const first = listener.message(groupMessage('42001', '20002'));
  const second = listener.message(groupMessage('42002', '20003'));
  await started;
  await listener.message({
    ...groupMessage('42003', '10001'),
    isSelf: true,
    data: { ...groupMessage('42003', '10001').data, content: 'Nhân viên đang hỗ trợ' },
  });
  finishRequests(Response.json({ reply: 'Tin bot đang chờ' }));
  await Promise.all([first, second]);

  assert.equal(sendMessage.mock.callCount(), 0);
});

test('busy group senders keep separate latest-follow-up slots', { timeout: 2000 }, async (t) => {
  configureTestChatbot(t);
  const sendMessage = t.mock.fn(async () => ({}));
  const { listener } = await connectFixture(t, { ownId: '10001', sendMessage });
  let finishFirst!: (response: Response) => void;
  let notifyFirst!: () => void;
  const firstStarted = new Promise<void>((resolve) => { notifyFirst = resolve; });
  const firstPending = new Promise<Response>((resolve) => { finishFirst = resolve; });
  const processed: Array<{ messageId: string; senderId: string }> = [];
  t.mock.method(globalThis, 'fetch', async (_url: string, request: RequestInit) => {
    const body = JSON.parse(String(request.body));
    processed.push({ messageId: body.message_id, senderId: body.sender_id });
    if (body.message_id === '43001') {
      notifyFirst();
      return firstPending;
    }
    return new Response(null, { status: 204 });
  });

  const first = listener.message(groupMessage('43001', '20002'));
  await firstStarted;
  await listener.message(groupMessage('43002', '20003'));
  await listener.message(groupMessage('43003', '20002'));
  finishFirst(new Response(null, { status: 204 }));
  await first;

  assert.deepEqual(processed, [
    { messageId: '43001', senderId: '20002' },
    { messageId: '43002', senderId: '20003' },
    { messageId: '43003', senderId: '20002' },
  ]);
});

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

test('paces sends across two channel records for the same Zalo account', { timeout: 6500 }, async (t) => {
  configureTestChatbot(t);
  const sendTimes: number[] = [];
  const sendMessage = async () => { sendTimes.push(Date.now()); };
  const { listener, login, pool, store, account } = await connectFixture(t, { ownId: '10001', sendMessage });
  const secondListener = new TestListener();
  login.mock.mockImplementation(async () => ({
    listener: secondListener,
    getOwnId: () => '10001',
    getUserInfo: async () => ({}),
    getGroupInfo: async () => ({}),
    sendMessage,
  }));
  const secondAccount = await store.saveAccount({
    tenantId: account.tenantId, channelId: 'second-channel',
    importEndpoint: account.importEndpoint, importSecret: account.importSecret,
  });
  await pool.reconnect(secondAccount.id, session);
  t.mock.method(globalThis, 'fetch', async () => Response.json({ reply: 'Chào bạn' }));
  await Promise.all([listener.message(directMessage('70001')), secondListener.message(directMessage('70002', '20003'))]);
  assert.equal(sendTimes.length, 2);
  assert.ok(sendTimes[1]! - sendTimes[0]! >= 3000);
});

test('bot suppresses an inbound echo after the original turn has finished', { timeout: 2000 }, async (t) => {
  configureTestChatbot(t);
  const sendMessage = t.mock.fn(async () => ({}));
  const { listener } = await connectFixture(t, { ownId: '10001', sendMessage });
  const fetchMock = t.mock.method(globalThis, 'fetch', async () => Response.json({ reply: 'Phản hồi của bot' }));
  await listener.message(directMessage('80001'));
  const echo = directMessage('80002');
  await listener.message({ ...echo, data: { ...echo.data, content: ' Phản hồi của bot ' } });
  assert.equal(fetchMock.mock.callCount(), 1);
  assert.equal(sendMessage.mock.callCount(), 1);
});

test('a flood cancels a pending reply and the gateway still stores all inbound messages', { timeout: 4000 }, async (t) => {
  configureTestChatbot(t);
  const sendMessage = t.mock.fn(async () => ({}));
  const { account, listener, store } = await connectFixture(t, { ownId: '10001', sendMessage });
  let notifyStarted!: () => void;
  let finishRequest!: (response: Response) => void;
  const started = new Promise<void>((resolve) => { notifyStarted = resolve; });
  const pending = new Promise<Response>((resolve) => { finishRequest = resolve; });
  const fetchMock = t.mock.method(globalThis, 'fetch', async () => {
    notifyStarted();
    return pending;
  });
  const first = listener.message(directMessage('90001'));
  await started;
  for (let n = 2; n <= 7; n++) await listener.message(directMessage(String(90000 + n)));
  finishRequest(Response.json({ reply: 'Phản hồi đang chờ' }));
  await first;
  assert.equal(fetchMock.mock.callCount(), 1);
  assert.equal(sendMessage.mock.callCount(), 0);
  assert.equal((await store.getAccount(account.id))?.threads['user:20002']?.messageOrder.length, 7);
});

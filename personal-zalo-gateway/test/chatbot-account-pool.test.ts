import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { EventEmitter } from 'node:events';
import fs from 'node:fs/promises';
import { createRequire } from 'node:module';
import os from 'node:os';
import path from 'node:path';
import test, { type TestContext } from 'node:test';
import { GatewayStore } from '../src/store/file-store.js';
import type { IncomingGatewayMessage } from '../src/types.js';
import { ZaloAccountPool } from '../src/zalo/account-pool.js';

const { Zalo } = createRequire(import.meta.url)('zca-js');
const session = { cookie: [], imei: 'test-device', userAgent: 'test-agent' };
const deliveryToken = 'test-delivery-token-at-least-16';

function directReply(payload: Record<string, unknown>): Response {
  return Response.json({ ...payload, delivery_token: deliveryToken });
}

function deliveryAuthorization(request: RequestInit): Response | null {
  const payload = JSON.parse(String(request.body));
  return payload.delivery_part ? Response.json({ allowed: true }) : null;
}

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
  options: { ownId?: string; groupIds?: string[]; sendMessage?: (text: string, threadId: string, type: number) => Promise<unknown> } = {},
) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'pzgw-pool-'));
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
    getAllGroups: async () => ({ gridVerMap: Object.fromEntries((options.groupIds ?? []).map((id) => [id, '1'])) }),
    getGroupInfo: async () => ({ gridInfoMap: {} }),
    sendMessage: options.sendMessage || (async () => ({})),
  }));
  const pool = new ZaloAccountPool(store);
  t.after(async () => {
    await pool.shutdown(1_000);
    await fs.rm(dir, { recursive: true, force: true });
  });
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
  const fetchMock = t.mock.method(globalThis, 'fetch', async (_url: string, request: RequestInit) =>
    deliveryAuthorization(request) ?? directReply({ reply: 'Chào bạn' }));
  const { listener, pool } = await connectFixture(t, { ownId: '10001', sendMessage });
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

  // The history replay can win the race against the live event on reconnect.
  await listener.oldMessages([incoming]);
  assert.equal(fetchMock.mock.callCount(), 0);
  await listener.message(incoming);
  await sentPromise;
  await listener.message(incoming);

  assert.equal(fetchMock.mock.callCount(), 2);
  assert.deepEqual(sent, { text: 'Chào bạn', threadId: '20002', type: 0 });
  const request = fetchMock.mock.calls[0]?.arguments[1] as RequestInit;
  assert.equal(JSON.parse(String(request.body)).account_external_id, '10001');
  assert.equal(JSON.parse(String(request.body)).thread_id, '20002');
});

test('a revoked direct delivery is denied immediately before Zalo send', async (t) => {
  configureTestChatbot(t);
  const sendMessage = t.mock.fn(async () => ({}));
  const fetchMock = t.mock.method(globalThis, 'fetch', async (_url: string, request: RequestInit) => {
    const payload = JSON.parse(String(request.body));
    return payload.delivery_part ? new Response(null, { status: 204 }) : directReply({ reply: 'Không được gửi' });
  });
  const { listener } = await connectFixture(t, { ownId: '10001', sendMessage });

  await listener.message(directMessage('31001'));

  assert.equal(fetchMock.mock.callCount(), 2);
  assert.equal(sendMessage.mock.callCount(), 0);
});

test('a persistent delivery authorization outage stops after bounded retries', async (t) => {
  configureTestChatbot(t);
  const sendMessage = t.mock.fn(async () => ({}));
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async () =>
    ++calls === 1 ? directReply({ reply: 'Chờ cấp quyền' }) : new Response('unavailable', { status: 503 }));
  const { listener, pool } = await connectFixture(t, { ownId: '10001', sendMessage });

  await listener.message(directMessage('31002'));

  assert.equal(calls, 4);
  assert.equal(sendMessage.mock.callCount(), 0);
  assert.equal((await pool.healthSnapshot()).failedChatbotTurns, 1);
});

test('a delayed ready reply does not block another account reconnect during bootstrap', { timeout: 3000 }, async (t) => {
  configureTestChatbot(t);
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'pzgw-recovery-'));
  const store = new GatewayStore(path.join(dir, 'state.json'));
  await store.init();
  const first = await store.saveAccount({
    tenantId: 'tenant', channelId: 'first-channel', importEndpoint: 'http://localhost/import',
    importSecret: 'first-secret', accountExternalId: '10001',
  });
  const second = await store.saveAccount({
    tenantId: 'tenant', channelId: 'second-channel', importEndpoint: 'http://localhost/import',
    importSecret: 'second-secret', accountExternalId: '10002',
  });
  await store.setAccountRuntime(first.id, { sessionData: session, zaloUid: '10001' });
  await store.setAccountRuntime(second.id, { sessionData: session, zaloUid: '10002' });
  const oldMessage: IncomingGatewayMessage = {
    senderUid: '20002', senderName: 'Khách', content: 'Tin cũ', contentType: 'text', msgId: '31003',
    timestamp: Date.now() - 10 * 60_000, isSelf: false, threadId: '20002', threadType: 'user',
    rawData: { message_type: 'webchat' }, sourceContentIsString: true, sourceTimestampIsValid: true,
  };
  await store.recordIncomingMessage(first.id, oldMessage, true);
  const turnKey = await store.activateChatbotTurn(first.id, oldMessage);
  assert.ok(turnKey);
  await store.updateChatbotTurn(first.id, turnKey, 'ready', {
    reply: { reply: 'Phản hồi đã tạo', deliveryToken },
  });

  const listeners = [new TestListener(), new TestListener()];
  let loginCount = 0;
  t.mock.method(Zalo.prototype, 'login', async () => {
    const index = loginCount++;
    return {
      listener: listeners[index], getOwnId: () => index === 0 ? '10001' : '10002',
      getUserInfo: async () => ({}), getGroupInfo: async () => ({ gridInfoMap: {} }), sendMessage: async () => ({}),
    };
  });
  let releaseAuthorization!: () => void;
  let authorizationStarted!: () => void;
  const started = new Promise<void>((resolve) => { authorizationStarted = resolve; });
  const blocked = new Promise<void>((resolve) => { releaseAuthorization = resolve; });
  t.mock.method(globalThis, 'fetch', async () => {
    authorizationStarted();
    await blocked;
    return Response.json({ allowed: true });
  });
  const pool = new ZaloAccountPool(store);
  t.after(async () => {
    releaseAuthorization();
    await pool.shutdown(1_000);
    await fs.rm(dir, { recursive: true, force: true });
  });

  const boot = pool.bootstrapReconnects();
  await started;
  await Promise.race([
    boot,
    new Promise((_, reject) => setTimeout(() => reject(new Error('bootstrap blocked on chatbot delivery')), 500)),
  ]);
  assert.equal((await store.getAccount(second.id))?.status, 'connected');
  releaseAuthorization();
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
  const deliveryReceipt = '123e4567-e89b-42d3-a456-426614174000';
  let sent!: { text: string; threadId: string; type: number };
  const sendMessage = async (text: string, threadId: string, type: number) => {
    sent = { text, threadId, type };
  };
  const fetchMock = t.mock.method(globalThis, 'fetch', async (url: string, request: RequestInit) => {
    if (url.endsWith('/thread-access')) return new Response(null, { status: 200 });
    const payload = JSON.parse(String(request.body));
    return payload.delivery_receipt
      ? new Response(null, { status: 204 })
      : Response.json({ reply: 'Chào cả nhóm', delivery_receipt: deliveryReceipt });
  });
  const { listener } = await connectFixture(t, { ownId: '10001', sendMessage });

  await listener.message(groupMessage('41001'));

  assert.deepEqual(sent, { text: 'Chào cả nhóm', threadId: '50005', type: 1 });
  assert.equal(fetchMock.mock.callCount(), 3);
  const request = fetchMock.mock.calls[0]?.arguments[1] as RequestInit;
  const accessRequest = fetchMock.mock.calls[1]?.arguments[1] as RequestInit;
  const receiptRequest = fetchMock.mock.calls[2]?.arguments[1] as RequestInit;
  const payload = JSON.parse(String(request.body));
  const accessPayload = JSON.parse(String(accessRequest.body));
  const receiptPayload = JSON.parse(String(receiptRequest.body));
  assert.equal(payload.thread_type, 'group');
  assert.equal(payload.thread_id, '50005');
  assert.equal(payload.sender_id, '20002');
  assert.equal(accessPayload.thread_id, payload.thread_id);
  assert.equal(accessPayload.sent_at, payload.sent_at);
  assert.equal(receiptPayload.delivery_receipt, deliveryReceipt);
  assert.equal(receiptPayload.text, payload.text);
  assert.equal(receiptPayload.sent_at, payload.sent_at);
  assert.notEqual(receiptPayload.request_id, payload.request_id);
});

test('a live group tag of the connected account reaches CQA without a configured name', async (t) => {
  configureTestChatbot(t);
  const fetchMock = t.mock.method(globalThis, 'fetch', async () => new Response(null, { status: 204 }));
  const { listener } = await connectFixture(t, { ownId: '10001' });
  const tag = '@Tài khoản hỗ trợ';
  for (const [msgId, question] of [['41004', 'FF399 còn hàng không?'], ['41005', '']]) {
    const message = groupMessage(msgId);
    await listener.message({
      ...message,
      data: {
        ...message.data,
        content: `${tag} ${question}`.trim(),
        mentions: [{ uid: '10001', pos: 0, len: tag.length, type: 0 }],
      },
    });
  }

  assert.equal(fetchMock.mock.callCount(), 2);
  const payloads = fetchMock.mock.calls.map((call) => JSON.parse(String((call.arguments[1] as RequestInit).body)));
  assert.deepEqual(payloads.map((payload) => [payload.bot_mentioned, payload.text.trim()]), [
    [true, 'FF399 còn hàng không?'],
    [true, ''],
  ]);
});

test('a failed Zalo group send never acknowledges delivery', async (t) => {
  configureTestChatbot(t);
  const deliveryReceipt = '123e4567-e89b-42d3-a456-426614174000';
  const sendMessage = t.mock.fn(async () => { throw new Error('SDK send failed'); });
  const fetchMock = t.mock.method(globalThis, 'fetch', async (url: string) =>
    url.endsWith('/thread-access')
      ? new Response(null, { status: 200 })
      : Response.json({ reply: 'Không gửi được', delivery_receipt: deliveryReceipt }));
  const { listener } = await connectFixture(t, { ownId: '10001', sendMessage });

  await listener.message(groupMessage('41002'));

  assert.equal(sendMessage.mock.callCount(), 1);
  assert.equal(fetchMock.mock.callCount(), 2);
});

test('revoking a group after reply generation prevents sending and acknowledging it', async (t) => {
  configureTestChatbot(t);
  const sendMessage = t.mock.fn(async () => ({}));
  const fetchMock = t.mock.method(globalThis, 'fetch', async (url: string) =>
    url.endsWith('/thread-access')
      ? new Response(null, { status: 204 })
      : Response.json({ reply: 'Đã tạo phản hồi', delivery_receipt: '123e4567-e89b-42d3-a456-426614174000' }));
  const { listener, store, account } = await connectFixture(t, { ownId: '10001', sendMessage });
  await listener.message(groupMessage('41003'));
  assert.equal(sendMessage.mock.callCount(), 0);
  assert.equal(fetchMock.mock.callCount(), 2);
  assert.equal((await store.getAccount(account.id))?.chatbotTurns?.['41003'], undefined);
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
  finishRequests(Response.json({
    reply: 'Tin bot đang chờ',
    delivery_receipt: '123e4567-e89b-42d3-a456-426614174000',
  }));
  await Promise.all([first, second]);

  assert.equal(sendMessage.mock.callCount(), 0);
  assert.equal(startedCount, 2, 'cancelled replies must not create delivery receipts');
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

for (const change of ['manual reply', 'socket close', 'credentials rotation'] as const) {
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
        tenantId: account.tenantId,
        channelId: account.channelId,
        importEndpoint: account.importEndpoint,
        importSecret: 'rotated-import-secret',
      }, account.id);
    }

    finishRequest(directReply({ reply: 'Tin bot đang chờ' }));
    await processing;
    assert.equal(sendMessage.mock.callCount(), 0);
  });
}

test('preserves every follow-up while the conversation is busy', { timeout: 2000 }, async (t) => {
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
    return new Response(null, { status: 204 });
  });
  const processing = listener.message(directMessage('60001'));
  await started;
  await listener.message(directMessage('60002'));
  await listener.message(directMessage('60003'));
  finishRequest(new Response(null, { status: 204 }));
  await processing;
  assert.deepEqual(processed, ['60001', '60002', '60003']);
  assert.equal(sendMessage.mock.callCount(), 0);
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
  t.mock.method(globalThis, 'fetch', async (_url: string, request: RequestInit) =>
    deliveryAuthorization(request) ?? directReply({ reply: 'Chào bạn' }));
  await Promise.all([listener.message(directMessage('70001')), secondListener.message(directMessage('70002', '20003'))]);
  assert.equal(sendTimes.length, 2);
  assert.ok(sendTimes[1]! - sendTimes[0]! >= 3000);
});

test('bot suppresses an inbound echo after the original turn has finished', { timeout: 2000 }, async (t) => {
  configureTestChatbot(t);
  const sendMessage = t.mock.fn(async () => ({}));
  const { listener } = await connectFixture(t, { ownId: '10001', sendMessage });
  const fetchMock = t.mock.method(globalThis, 'fetch', async (_url: string, request: RequestInit) =>
    deliveryAuthorization(request) ?? directReply({ reply: 'Phản hồi của bot' }));
  await listener.message(directMessage('80001'));
  const echo = directMessage('80002');
  await listener.message({ ...echo, data: { ...echo.data, content: ' Phản hồi của bot ' } });
  assert.equal(fetchMock.mock.callCount(), 2);
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
  finishRequest(directReply({ reply: 'Phản hồi đang chờ' }));
  await first;
  assert.equal(fetchMock.mock.callCount(), 1);
  assert.equal(sendMessage.mock.callCount(), 0);
  assert.equal((await store.getAccount(account.id))?.threads['user:20002']?.messageOrder.length, 7);
});

const knowledgeImageBase64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jHioAAAAASUVORK5CYII=';
const knowledgeImage = {
  filename: 'kb-image.png', media_type: 'image/png', width: 1, height: 1, data_base64: knowledgeImageBase64,
};
const secondImageToken = 'second-delivery-token-at-least-16';
const secondKnowledgeImage = {
  ...knowledgeImage, filename: 'product-side.png', delivery_token: secondImageToken,
};

function selfMessage(msgId: string, content: unknown, msgType = 'webchat') {
  const incoming = directMessage(msgId);
  return { ...incoming, isSelf: true, data: { ...incoming.data, uidFrom: '10001', content, msgType } };
}

test('sends text then one photo and ignores both early and delayed bot echoes', { timeout: 9000 }, async (t) => {
  configureTestChatbot(t);
  const messages: unknown[] = [];
  let listener!: TestListener;
  const sendMessage = async (message: unknown, threadId: string, type: number) => {
    assert.equal(threadId, '20002');
    assert.equal(type, 0);
    messages.push(message);
    if (messages.length === 1) {
      await listener.message(selfMessage('80002', message));
      return { message: { msgId: 80002 }, attachment: [] };
    }
    if (messages.length === 2) {
      return { message: null, attachment: [{ msgId: 80003 }] };
    }
    return { message: { msgId: 80005 }, attachment: [] };
  };
  ({ listener } = await connectFixture(t, { ownId: '10001', sendMessage }));
  let turns = 0;
  t.mock.method(globalThis, 'fetch', async (_url: string, request: RequestInit) => {
    const authorization = deliveryAuthorization(request);
    if (authorization) return authorization;
    if (++turns === 1) return directReply({ reply: 'Hướng dẫn', images: [knowledgeImage] });
    await listener.message(selfMessage('80003', { description: '', href: 'attachment' }, 'chat.photo'));
    return directReply({ reply: 'Tin tiếp theo' });
  });

  await listener.message(directMessage('80001'));
  await listener.message(directMessage('80004'));
  const data = Buffer.from(knowledgeImageBase64, 'base64');
  assert.deepEqual(messages, [
    'Hướng dẫn',
    { msg: '', attachments: [{ filename: 'kb-image.png', data, metadata: { totalSize: data.length, width: 1, height: 1 } }] },
    'Tin tiếp theo',
  ]);
});

test('sends every selected product photo in order with its own delivery authorization', { timeout: 9000 }, async (t) => {
  configureTestChatbot(t);
  const messages: unknown[] = [];
  const authorizedTokens: string[] = [];
  const sendMessage = async (message: unknown) => {
    messages.push(message);
    return typeof message === 'string'
      ? { message: { msgId: 81002 }, attachment: [] }
      : { message: null, attachment: [{ msgId: 81003 + messages.length }] };
  };
  const { listener } = await connectFixture(t, { ownId: '10001', sendMessage });
  t.mock.method(globalThis, 'fetch', async (_url: string, request: RequestInit) => {
    const payload = JSON.parse(String(request.body));
    if (payload.delivery_part) {
      authorizedTokens.push(payload.delivery_token);
      return Response.json({ allowed: true });
    }
    return directReply({ reply: 'Giày LS2 Goby Man', images: [knowledgeImage, secondKnowledgeImage] });
  });

  await listener.message(directMessage('81001'));

  const data = Buffer.from(knowledgeImageBase64, 'base64');
  assert.deepEqual(messages, [
    'Giày LS2 Goby Man',
    { msg: '', attachments: [{ filename: 'kb-image.png', data, metadata: { totalSize: data.length, width: 1, height: 1 } }] },
    { msg: '', attachments: [{ filename: 'product-side.png', data, metadata: { totalSize: data.length, width: 1, height: 1 } }] },
  ]);
  assert.deepEqual(authorizedTokens, [deliveryToken, deliveryToken, secondImageToken]);
});

test('skips a denied product photo and still sends a later authorized photo', { timeout: 9000 }, async (t) => {
  configureTestChatbot(t);
  const deniedToken = 'denied-delivery-token-at-least-16';
  const messages: unknown[] = [];
  const authorizedTokens: string[] = [];
  const { listener } = await connectFixture(t, { ownId: '10001', sendMessage: async (outbound: unknown) => {
    messages.push(outbound);
    return typeof outbound === 'string'
      ? { message: { msgId: 82002 }, attachment: [] }
      : { message: null, attachment: [{ msgId: 82003 + messages.length }] };
  } });
  t.mock.method(globalThis, 'fetch', async (_url: string, request: RequestInit) => {
    const payload = JSON.parse(String(request.body));
    if (payload.delivery_part) {
      authorizedTokens.push(payload.delivery_token);
      return Response.json({ allowed: payload.delivery_token !== deniedToken });
    }
    return directReply({ reply: 'Giày LS2 Goby Man', images: [
      { ...knowledgeImage, delivery_token: deniedToken },
      secondKnowledgeImage,
    ] });
  });

  await listener.message(directMessage('82001'));

  const data = Buffer.from(knowledgeImageBase64, 'base64');
  assert.deepEqual(messages, [
    'Giày LS2 Goby Man',
    { msg: '', attachments: [{ filename: 'product-side.png', data, metadata: { totalSize: data.length, width: 1, height: 1 } }] },
  ]);
  assert.deepEqual(authorizedTokens, [deliveryToken, deniedToken, secondImageToken]);
});

test('a manual photo during bot upload wins even when both images have empty captions', { timeout: 6000 }, async (t) => {
  configureTestChatbot(t);
  let listener!: TestListener;
  const sendMessage = t.mock.fn(async (message: unknown) => {
    if (typeof message === 'string') return { message: { msgId: 90002 }, attachment: [] };
    await listener.message(directMessage('90003'));
    await listener.message(selfMessage('90004', { description: '', href: 'manual' }, 'chat.photo'));
    await listener.message(selfMessage('90005', { description: '', href: 'bot' }, 'chat.photo'));
    return { message: null, attachment: [{ msgId: 90005 }] };
  });
  ({ listener } = await connectFixture(t, { ownId: '10001', sendMessage }));
  const fetch = t.mock.method(globalThis, 'fetch', async (_url: string, request: RequestInit) => {
    const body = JSON.parse(String(request.body));
    if (body.schema_version === 'personal-zalo-internbot-v1') return new Response(null, { status: 400 });
    return deliveryAuthorization(request) ?? directReply({ reply: 'Hướng dẫn', images: [knowledgeImage] });
  });

  await listener.message(directMessage('90001'));
  assert.equal(sendMessage.mock.callCount(), 2);
  const schemas = fetch.mock.calls.map((call) => JSON.parse(String(call.arguments[1]?.body)).schema_version);
  assert.deepEqual(schemas, [
    'personal-zalo-chatbot-v3', 'personal-zalo-chatbot-v3', 'personal-zalo-chatbot-v3', 'personal-zalo-internbot-v1',
  ], 'the queued turn can be observed by Internbot but cannot trigger a legacy chatbot reply');
});

test('a manual photo before attachment delivery suppresses the bot photo', { timeout: 5000 }, async (t) => {
  configureTestChatbot(t);
  let listener!: TestListener;
  const sendMessage = t.mock.fn(async () => {
    await listener.message(selfMessage('91002', { description: '', href: 'manual' }, 'chat.photo'));
    return { message: { msgId: 91003 }, attachment: [] };
  });
  ({ listener } = await connectFixture(t, { ownId: '10001', sendMessage }));
  t.mock.method(globalThis, 'fetch', async (_url: string, request: RequestInit) =>
    deliveryAuthorization(request) ?? directReply({ reply: 'Hướng dẫn', images: [knowledgeImage] }));

  await listener.message(directMessage('91001'));
  assert.equal(sendMessage.mock.callCount(), 1);
});

test('a fourth direct conversation waits durably for a free chatbot slot', { timeout: 3000 }, async (t) => {
  configureTestChatbot(t);
  const { listener } = await connectFixture(t, { ownId: '10001' });
  let release!: () => void;
  const blocked = new Promise<void>((resolve) => { release = resolve; });
  let started = 0;
  let fourthStarted!: () => void;
  const fourth = new Promise<void>((resolve) => { fourthStarted = resolve; });
  const fetch = t.mock.method(globalThis, 'fetch', async (_url: string, request: RequestInit) => {
    const id = JSON.parse(String(request.body)).message_id;
    started++;
    if (id === '92004') fourthStarted();
    else await blocked;
    return new Response(null, { status: 204 });
  });

  const active = ['92001', '92002', '92003'].map((id, index) =>
    listener.message(directMessage(id, String(21001 + index))));
  while (started < 3) await new Promise((resolve) => setTimeout(resolve, 1));
  await listener.message(directMessage('92004', '21004'));
  assert.equal(fetch.mock.callCount(), 3);
  release();
  await fourth;
  await Promise.all(active);
  assert.equal(fetch.mock.callCount(), 4);
});

test('an ambiguous CQA response is retained for operator attention without a Zalo retry', async (t) => {
  configureTestChatbot(t);
  const sendMessage = t.mock.fn(async () => ({}));
  t.mock.method(globalThis, 'fetch', async () => new Response('failed', { status: 500 }));
  const { listener, pool } = await connectFixture(t, { ownId: '10001', sendMessage });

  await listener.message(directMessage('93001'));

  assert.equal(sendMessage.mock.callCount(), 0);
  assert.equal((await pool.healthSnapshot()).unknownChatbotTurns, 1);
});

test('a disconnect before a queued image never replays the already sent text', { timeout: 5000 }, async (t) => {
  configureTestChatbot(t);
  let textSent!: () => void;
  const sent = new Promise<void>((resolve) => { textSent = resolve; });
  const sendMessage = t.mock.fn(async (outbound: unknown) => {
    if (typeof outbound === 'string') textSent();
    return typeof outbound === 'string' ? { message: { msgId: 94002 }, attachment: [] } : {};
  });
  const { account: connectedAccount, listener, pool, store } = await connectFixture(t, { ownId: '10001', sendMessage });
  t.mock.method(globalThis, 'fetch', async (_url: string, request: RequestInit) =>
    deliveryAuthorization(request) ?? directReply({ reply: 'Một lần', images: [knowledgeImage] }));

  const processing = listener.message(directMessage('94001'));
  await sent;
  await listener.close(1008);
  await processing;

  assert.equal(sendMessage.mock.callCount(), 1);
  assert.equal((await pool.healthSnapshot()).failedChatbotTurns, 1);
  const retained = (await store.listChatbotTurns(connectedAccount.id))[0]?.[1];
  assert.equal(retained?.reply?.reply, 'Một lần');
  assert.equal(retained?.reply?.images, undefined);
});

const internbotGroup = '80008';
const internbotText = 'Internbot · Phản hồi đề xuất';

function internbotReply(groupId = internbotGroup, images?: unknown[]): Response {
  return Response.json({ internbot_delivery: { group_id: groupId, text: internbotText, token: deliveryToken, images } });
}

test('an Internbot draft from a direct chat sends text and authorized images only to the supervisor group',
  { timeout: 6000 }, async (t) => {
    configureTestChatbot(t);
    const imageToken = 'image-delivery-token-at-least-16';
    const sendMessage = t.mock.fn(async (_outbound: unknown, _threadId: string, _type: number) => ({}));
    const { listener } = await connectFixture(t, { ownId: '10001', groupIds: [internbotGroup], sendMessage });
    const authorizations: Array<{ part: string; digest: string; token: string }> = [];
    t.mock.method(globalThis, 'fetch', async (url: string, init: RequestInit) => {
      const payload = JSON.parse(String(init.body));
      assert.equal(payload.thread_id, '20002');
      if (url.endsWith('/internbot/delivery')) {
        assert.equal(payload.supervisor_group_id, internbotGroup);
        authorizations.push({ part: payload.delivery_part, digest: payload.delivery_digest, token: payload.delivery_token });
        return new Response(null, { status: 200 });
      }
      assert.equal(url.endsWith('/personal-zalo'), true);
      assert.equal(payload.supports_internbot, true);
      return internbotReply(internbotGroup, [{ ...knowledgeImage, delivery_token: imageToken }]);
    });

    await listener.message(directMessage('95001'));

    assert.deepEqual(sendMessage.mock.calls.map(({ arguments: [, threadId, type] }) => [threadId, type]),
      [[internbotGroup, 1], [internbotGroup, 1]]);
    assert.deepEqual(authorizations, [
      { part: 'text', digest: createHash('sha256').update(internbotText).digest('hex'), token: deliveryToken },
      { part: 'image', digest: createHash('sha256').update(Buffer.from(knowledgeImageBase64, 'base64')).digest('hex'), token: imageToken },
    ]);
  });

test('a manual response in a customer group does not suppress its pending Internbot draft', async (t) => {
  configureTestChatbot(t);
  const sendMessage = t.mock.fn(async (_outbound: unknown, _threadId: string, _type: number) => ({}));
  const { listener } = await connectFixture(t, { ownId: '10001', groupIds: [internbotGroup], sendMessage });
  let started!: () => void;
  const requestStarted = new Promise<void>((resolve) => { started = resolve; });
  let release!: () => void;
  const releaseRequest = new Promise<void>((resolve) => { release = resolve; });
  const turnSchemas: string[] = [];
  t.mock.method(globalThis, 'fetch', async (url: string, init: RequestInit) => {
    const body = JSON.parse(String(init.body));
    if (url.endsWith('/internbot/delivery')) return new Response(null, { status: 200 });
    assert.equal(url.endsWith('/personal-zalo'), true);
    turnSchemas.push(body.schema_version);
    if (body.message_id === '95101') {
      started();
      await releaseRequest;
      return internbotReply();
    }
    return new Response(null, { status: 204 });
  });

  const first = listener.message(groupMessage('95101'));
  await requestStarted;
  await listener.message(groupMessage('95102'));
  await listener.message({ ...groupMessage('95103', '10001'), isSelf: true });
  release();
  await first;

  assert.deepEqual(turnSchemas, ['personal-zalo-chatbot-v2', 'personal-zalo-internbot-v1']);
  assert.deepEqual(sendMessage.mock.calls.map((call) => call.arguments), [[internbotText, internbotGroup, 1]]);
});

test('Internbot stops delivery when the supervisor group is no longer present, is the source, or CQA denies sending', async (t) => {
  configureTestChatbot(t);
  const groupIds = [internbotGroup];
  const sendMessage = t.mock.fn(async () => ({}));
  const { listener } = await connectFixture(t, { ownId: '10001', groupIds, sendMessage });
  let approvals = 0;
  t.mock.method(globalThis, 'fetch', async (url: string, init: RequestInit) => {
    if (url.endsWith('/internbot/delivery')) {
      approvals++;
      return new Response(null, { status: 204 });
    }
    const body = JSON.parse(String(init.body));
    return internbotReply(body.message_id === '95202' ? body.thread_id : internbotGroup);
  });

  await listener.message(groupMessage('95201'));
  await listener.message(groupMessage('95202'));
  groupIds.length = 0;
  await listener.message(groupMessage('95203'));

  assert.equal(approvals, 1);
  assert.equal(sendMessage.mock.callCount(), 0);
});

test('an ambiguous Internbot Zalo send is retained separately from legacy drafts and is never replayed', async (t) => {
  configureTestChatbot(t);
  const sendMessage = t.mock.fn(async () => { throw new Error('transport ended during delivery'); });
  const { account, listener, store } = await connectFixture(t, { ownId: '10001', groupIds: [internbotGroup], sendMessage });
  t.mock.method(globalThis, 'fetch', async (url: string) => url.endsWith('/internbot/delivery') ?
    new Response(null, { status: 200 }) : internbotReply());

  const message = groupMessage('95301');
  await listener.message(message);
  await listener.message(message);

  const [[, retained]] = await store.listChatbotTurns(account.id);
  assert.equal(sendMessage.mock.callCount(), 1);
  assert.equal(retained.status, 'unknown');
  assert.equal(retained.reply, undefined);
  assert.equal(retained.internbotReply?.groupId, internbotGroup);
});

test('a session-close notice goes to the chat and its echo is not a manual reply', { timeout: 10_000 }, async (t) => {
  configureTestChatbot(t);
  const sendMessage = t.mock.fn(async (_text: string, _threadId: string, _type: number) => ({ message: { msgId: '7000001' } }));
  const { account, listener, pool } = await connectFixture(t, { ownId: '10001', sendMessage });
  const idleSince = Date.now() - 1_000;

  assert.equal(await pool.sendSessionNotice(account.id, { threadType: 'user', threadId: '20002', text: 'Hẹn gặp lại', idleSince }), 'sent');
  assert.deepEqual(sendMessage.mock.calls[0]?.arguments, ['Hẹn gặp lại', '20002', 0]);
  // The echo arrives with a different id than the receipt (as when it beats sendMessage).
  await listener.message({
    ...directMessage('7000002'), isSelf: true,
    data: { ...directMessage('7000002').data, uidFrom: '10001', content: 'Hẹn gặp lại' },
  });
  assert.equal(await pool.sendSessionNotice(account.id, { threadType: 'user', threadId: '20002', text: 'Lần hai', idleSince }), 'sent');

  assert.equal(await pool.sendSessionNotice(account.id, { threadType: 'group', threadId: '50005', text: 'Nhóm', idleSince }), 'sent');
  assert.deepEqual(sendMessage.mock.calls[2]?.arguments, ['Nhóm', '50005', 1]);
});

test('a session-close notice is skipped after a manual reply or while the chat is busy', { timeout: 5_000 }, async (t) => {
  configureTestChatbot(t);
  const sendMessage = t.mock.fn(async () => ({}));
  const { account, listener, pool } = await connectFixture(t, { ownId: '10001', sendMessage });
  const idleSince = Date.now() - 1_000;
  await listener.message({
    ...directMessage('7100001'), isSelf: true,
    data: { ...directMessage('7100001').data, uidFrom: '10001', content: 'Nhân viên trả lời tay' },
  });
  assert.equal(await pool.sendSessionNotice(account.id, { threadType: 'user', threadId: '20002', text: 'Hẹn gặp lại', idleSince }), 'skipped_manual');
  assert.equal(await pool.sendSessionNotice(account.id, { threadType: 'user', threadId: '20002', text: 'Hẹn gặp lại', idleSince: Date.now() + 1 }), 'sent');

  let requestStarted!: () => void;
  let finishRequest!: (response: Response) => void;
  const started = new Promise<void>((resolve) => { requestStarted = resolve; });
  const pending = new Promise<Response>((resolve) => { finishRequest = resolve; });
  t.mock.method(globalThis, 'fetch', async () => {
    requestStarted();
    return pending;
  });
  const processing = listener.message(directMessage('7100002', '20003'));
  await started;
  assert.equal(await pool.sendSessionNotice(account.id, { threadType: 'user', threadId: '20003', text: 'Hẹn gặp lại' }), 'skipped_busy');
  finishRequest(new Response(null, { status: 204 }));
  await processing;
  assert.equal(sendMessage.mock.callCount(), 1);
});

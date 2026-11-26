import assert from 'node:assert/strict';
import { createHmac } from 'node:crypto';
import test from 'node:test';
import {
  acknowledgePersonalZaloChatbotDelivery,
  buildChatbotRequest,
  chatbotEndpoint,
  isChatbotEligibleMessage,
  requestPersonalZaloChatbotReply,
} from '../src/chatbot/cqa-chatbot-client.js';
import type { GatewayAccountRecord, IncomingGatewayMessage } from '../src/types.js';

const account: GatewayAccountRecord = {
  id: 'account-1',
  tenantId: 'tenant-1',
  channelId: 'channel-1',
  importEndpoint: 'http://app:8080/prefix/api/internal/imports/personal-zalo',
  importSecret: 'test-secret',
  accountExternalId: '10001',
  zaloUid: '10001',
  status: 'connected',
  displayName: 'Tư vấn',
  createdAt: '2026-11-26T00:00:00.000Z',
  updatedAt: '2026-11-26T00:00:00.000Z',
  threads: {},
};
const deployment = {
  baseUrl: 'http://app:8080/internal-prefix/',
  secret: 'chatbot-deployment-secret-32-bytes!',
};

function message(now: number): IncomingGatewayMessage {
  return {
    senderUid: '20002',
    senderName: 'Khách hàng',
    content: 'Xin chào',
    contentType: 'text',
    msgId: '30003',
    timestamp: now,
    isSelf: false,
    threadId: '20002',
    threadType: 'user',
    rawData: { message_type: 'webchat' },
    sourceContentIsString: true,
    sourceTimestampIsValid: true,
  };
}

test('buildChatbotRequest derives endpoint, scopes identity and signs the exact body', () => {
  const now = Date.parse('2026-11-26T06:00:00.000Z');
  assert.equal(
    chatbotEndpoint(deployment.baseUrl),
    'http://app:8080/internal-prefix/api/internal/chatbot/personal-zalo',
  );
  assert.equal(chatbotEndpoint('file:///tmp/cqa'), null);

  const request = buildChatbotRequest(account, message(now), deployment, now);
  assert.ok(request);
  const payload = JSON.parse(request.body);
  assert.equal(payload.schema_version, 'personal-zalo-chatbot-v1');
  assert.equal(payload.account_external_id, '10001');
  assert.equal(payload.thread_id, '20002');
  assert.equal(payload.thread_type, 'user');
  assert.equal(payload.sender_id, '20002');
  assert.equal(payload.message_id, '30003');
  assert.equal(payload.text, 'Xin chào');
  const expected = createHmac('sha256', account.importSecret)
    .update(request.timestamp)
    .update('.')
    .update(request.requestId)
    .update('.')
    .update(request.body)
    .digest('hex');
  assert.equal(request.signature, expected);
  const expectedDeploymentSignature = createHmac('sha256', deployment.secret)
    .update(request.timestamp)
    .update('.')
    .update(request.requestId)
    .update('.')
    .update(request.body)
    .digest('hex');
  assert.equal(request.chatbotSignature, expectedDeploymentSignature);
  assert.equal(request.body.includes(deployment.secret), false);
});

test('chatbot eligibility rejects replay-prone and non-customer events', () => {
  const now = Date.now();
  const eligible = message(now);
  assert.equal(isChatbotEligibleMessage(eligible, now), true);

  const invalid = [
    { ...eligible, isSelf: true },
    { ...eligible, threadType: 'group' as const, threadId: eligible.senderUid },
    { ...eligible, threadId: '99999' },
    { ...eligible, msgId: '' },
    { ...eligible, timestamp: now - 120_001 },
    { ...eligible, content: 'x'.repeat(2_001) },
    { ...eligible, content: 'Bạn và An đã trở thành bạn bè' },
    { ...eligible, rawData: { message_type: 'chat.photo' } },
    { ...eligible, sourceContentIsString: false },
    { ...eligible, sourceTimestampIsValid: false },
  ];
  for (const candidate of invalid) {
    assert.equal(isChatbotEligibleMessage(candidate, now), false);
  }
});

test('a fresh group text carries separate group and sender identities', () => {
  const now = Date.now();
  const groupMessage: IncomingGatewayMessage = {
    ...message(now),
    threadType: 'group',
    threadId: '50005',
    senderUid: '20002',
  };
  assert.equal(isChatbotEligibleMessage(groupMessage, now), true);

  const request = buildChatbotRequest(account, groupMessage, deployment, now);
  assert.ok(request);
  const payload = JSON.parse(request.body);
  assert.equal(payload.schema_version, 'personal-zalo-chatbot-v2');
  assert.equal(payload.thread_type, 'group');
  assert.equal(payload.thread_id, '50005');
  assert.equal(payload.sender_id, '20002');
});

test('a group delivery receipt preserves the original identity and is independently signed', async (t) => {
  t.after(() => {
    delete process.env.PERSONAL_ZALO_CHATBOT_BASE_URL;
    delete process.env.PERSONAL_ZALO_CHATBOT_SECRET;
  });
  process.env.PERSONAL_ZALO_CHATBOT_BASE_URL = deployment.baseUrl;
  process.env.PERSONAL_ZALO_CHATBOT_SECRET = deployment.secret;
  const now = Date.now();
  const groupMessage = { ...message(now), threadType: 'group' as const, threadId: '50005' };
  const deliveryReceipt = '123e4567-e89b-42d3-a456-426614174000';
  const initial = buildChatbotRequest(account, groupMessage, deployment, now);
  const receipt = buildChatbotRequest(account, groupMessage, deployment, now + 1, deliveryReceipt);
  assert.ok(initial);
  assert.ok(receipt);
  const initialBody = JSON.parse(initial.body);
  const receiptBody = JSON.parse(receipt.body);
  assert.notEqual(receipt.requestId, initial.requestId);
  assert.deepEqual(
    { ...receiptBody, request_id: initialBody.request_id, delivery_receipt: undefined },
    { ...initialBody, delivery_receipt: undefined },
  );
  assert.equal(receiptBody.delivery_receipt, deliveryReceipt);
  assert.notEqual(receipt.signature, initial.signature);

  const fetchMock = t.mock.method(globalThis, 'fetch', async () => new Response(null, { status: 204 }));
  await acknowledgePersonalZaloChatbotDelivery(account, groupMessage, deliveryReceipt);
  assert.equal(fetchMock.mock.callCount(), 1);
  const request = fetchMock.mock.calls[0]?.arguments[1] as RequestInit;
  const body = JSON.parse(String(request.body));
  assert.equal(body.text, groupMessage.content);
  assert.equal(body.sent_at, new Date(groupMessage.timestamp).toISOString());
  assert.equal(body.delivery_receipt, deliveryReceipt);
  const headers = request.headers as Record<string, string>;
  assert.equal(headers['X-CQA-Signature']?.length, 64);
  assert.equal(headers['X-CQA-Chatbot-Signature']?.length, 64);
});

test('CQA 204, failures and oversized replies do not produce a reply', async (t) => {
  t.after(() => {
    delete process.env.PERSONAL_ZALO_CHATBOT_BASE_URL;
    delete process.env.PERSONAL_ZALO_CHATBOT_SECRET;
  });
  process.env.PERSONAL_ZALO_CHATBOT_BASE_URL = deployment.baseUrl;
  process.env.PERSONAL_ZALO_CHATBOT_SECRET = deployment.secret;
  const now = Date.now();
  const currentMessage = message(now);

  const fetchMock = t.mock.method(globalThis, 'fetch', async () => new Response(null, { status: 204 }));
  assert.equal(await requestPersonalZaloChatbotReply(account, currentMessage), null);

  fetchMock.mock.mockImplementation(async () => new Response('failed', { status: 500 }));
  assert.equal(await requestPersonalZaloChatbotReply(account, currentMessage), null);

  fetchMock.mock.mockImplementation(async () => new Response(JSON.stringify({ reply: 'x'.repeat(2_001) }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  }));
  assert.equal(await requestPersonalZaloChatbotReply(account, currentMessage), null);
  assert.equal(fetchMock.mock.callCount(), 3);
  assert.equal(fetchMock.mock.calls[0]?.arguments[1]?.redirect, 'error');
  const headers = fetchMock.mock.calls[0]?.arguments[1]?.headers as Record<string, string>;
  assert.equal(headers['X-CQA-Chatbot-Signature']?.length, 64);
  assert.equal(JSON.stringify(headers).includes(deployment.secret), false);
});

test('chatbot request stays disabled without a strong deployment secret and trusted base URL', async (t) => {
  t.after(() => {
    delete process.env.PERSONAL_ZALO_CHATBOT_BASE_URL;
    delete process.env.PERSONAL_ZALO_CHATBOT_SECRET;
  });
  const fetchMock = t.mock.method(globalThis, 'fetch', async () => new Response(null, { status: 204 }));
  process.env.PERSONAL_ZALO_CHATBOT_BASE_URL = 'http://app:8080';
  process.env.PERSONAL_ZALO_CHATBOT_SECRET = 'too-short';
  assert.equal(await requestPersonalZaloChatbotReply(account, message(Date.now())), null);
  assert.equal(fetchMock.mock.callCount(), 0);
});

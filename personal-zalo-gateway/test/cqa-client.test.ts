import assert from 'node:assert/strict';
import test from 'node:test';
import { createHmac } from 'node:crypto';
import { buildSignedImportRequest, CQAImportError, sendImportBatch } from '../src/sync/cqa-client.js';

function fixtureBatch() {
  return {
    account: {
      id: 'acc-1',
      tenantId: 'tenant-1',
      channelId: 'channel-1',
      importEndpoint: 'http://localhost:8080/api/internal/imports/personal-zalo',
      importSecret: 'secret-1',
      accountExternalId: 'zalo-123',
      status: 'connected',
      displayName: 'Account 1',
      createdAt: '2026-04-08T00:00:00.000Z',
      updatedAt: '2026-04-08T00:00:00.000Z',
      threads: {},
    },
    conversations: [
      {
        threadKey: 'user:u-1',
        conversation: {
          external_id: 'u-1',
          thread_type: 'user',
          external_user_id: 'u-1',
          customer_name: 'Alice',
          last_message_at: '2026-04-08T00:00:00.000Z',
          metadata: {},
        },
        messages: [
          {
            external_id: 'm-1',
            sender_type: 'customer',
            sender_name: 'Alice',
            content: 'hello',
            content_type: 'text',
            attachments: [],
            sent_at: '2026-04-08T00:00:00.000Z',
            raw_data: {},
          },
        ],
      },
    ],
  } as any;
}

function acknowledgementFor(request: RequestInfo | URL, init?: RequestInit, overrides: Record<string, unknown> = {}) {
  assert.equal(init?.redirect, 'error');
  const sent = JSON.parse(String(init?.body));
  return Response.json({
    request_id: sent.request_id,
    conversations: sent.conversations.length,
    messages_processed: 1,
    messages_inserted: 1,
    messages_deduplicated: 0,
    ...overrides,
  }, { status: 202 });
}

test('buildSignedImportRequest signs body with timestamp and request id', () => {
  const batch = fixtureBatch();

  const request = buildSignedImportRequest(batch);
  const expected = createHmac('sha256', 'secret-1')
    .update(request.timestamp)
    .update('.')
    .update(request.requestId)
    .update('.')
    .update(request.body)
    .digest('hex');

  assert.equal(request.signature, expected);
  assert.equal(request.url, 'http://localhost:8080/api/internal/imports/personal-zalo');
});

test('sendImportBatch accepts only the matching 202 acknowledgement', async (t) => {
  const fetchMock = t.mock.method(globalThis, 'fetch', async (request: RequestInfo | URL, init?: RequestInit) =>
    acknowledgementFor(request, init));

  const response = await sendImportBatch(fixtureBatch(), 5_000);

  assert.equal(response.conversations, 1);
  assert.equal(response.messages_processed, 1);
  assert.equal(fetchMock.mock.callCount(), 1);
});

for (const testCase of [
  { name: 'HTTP 200', response: (_request: RequestInfo | URL, init?: RequestInit) => {
    const response = acknowledgementFor(_request, init);
    return new Response(response.body, { status: 200, headers: response.headers });
  } },
  { name: 'different request', response: (request: RequestInfo | URL, init?: RequestInit) =>
    acknowledgementFor(request, init, { request_id: 'different' }) },
  { name: 'different conversation count', response: (request: RequestInfo | URL, init?: RequestInit) =>
    acknowledgementFor(request, init, { conversations: 0 }) },
  { name: 'negative counter', response: (request: RequestInfo | URL, init?: RequestInit) =>
    acknowledgementFor(request, init, { messages_inserted: -1 }) },
  { name: 'inconsistent counters', response: (request: RequestInfo | URL, init?: RequestInit) =>
    acknowledgementFor(request, init, { messages_processed: 1, messages_inserted: 1, messages_deduplicated: 1 }) },
  { name: 'processed more than sent', response: (request: RequestInfo | URL, init?: RequestInit) =>
    acknowledgementFor(request, init, { messages_processed: 2, messages_inserted: 2 }) },
]) {
  test(`sendImportBatch rejects ${testCase.name}`, async (t) => {
    t.mock.method(globalThis, 'fetch', testCase.response);

    await assert.rejects(
      sendImportBatch(fixtureBatch(), 5_000),
      (error: unknown) => error instanceof CQAImportError && error.code === 'invalid_import_ack',
    );
  });
}

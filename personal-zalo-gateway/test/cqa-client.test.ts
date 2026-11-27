import assert from 'node:assert/strict';
import test from 'node:test';
import { createHmac } from 'node:crypto';
import { buildSignedImportRequest, CQAImportError, fetchImportSettings, sendImportBatch } from '../src/sync/cqa-client.js';

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

test('settings uses the same signed import identity and prohibits redirects', async (t) => {
  const { account } = fixtureBatch();
  t.mock.method(globalThis, 'fetch', async (request: RequestInfo | URL, init?: RequestInit) => {
    assert.equal(String(request), `${account.importEndpoint}/settings`);
    assert.equal(init?.redirect, 'error');
    assert.equal(init?.method, 'POST');
    assert.ok(init?.signal instanceof AbortSignal);
    const body = String(init?.body);
    const headers = new Headers(init?.headers);
    const requestID = headers.get('X-CQA-Request-Id');
    const timestamp = headers.get('X-CQA-Timestamp');
    assert.deepEqual(JSON.parse(body), {
      request_id: requestID, tenant_id: 'tenant-1', channel_id: 'channel-1', account_external_id: 'zalo-123',
    });
    assert.equal(headers.get('X-CQA-Signature'), createHmac('sha256', account.importSecret)
      .update(`${timestamp}.${requestID}.${body}`).digest('hex'));
    return Response.json({ active: true, sync_interval_minutes: 30 });
  });

  assert.deepEqual(await fetchImportSettings(account, 5_000), { active: true, sync_interval_minutes: 30 });
});

test('only a missing settings route triggers legacy fallback', async (t) => {
  let status = 404;
  t.mock.method(globalThis, 'fetch', async () => new Response(null, { status }));
  const { account } = fixtureBatch();
  assert.equal(await fetchImportSettings(account, 5_000), undefined);
  status = 409;
  await assert.rejects(fetchImportSettings(account, 5_000), /cqa_import_settings_failed:409/);
});

test('settings rejects an invalid active flag or interval', async (t) => {
  let payload: Record<string, unknown> = { active: 'true' };
  t.mock.method(globalThis, 'fetch', async () => Response.json(payload));
  const { account } = fixtureBatch();
  await assert.rejects(fetchImportSettings(account, 5_000), /invalid_import_settings/);
  for (const invalid of [0, 1.5, 1441, null, '15']) {
    payload = { active: true, sync_interval_minutes: invalid };
    await assert.rejects(fetchImportSettings(account, 5_000), /invalid_import_settings/);
  }
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

import assert from 'node:assert/strict';
import { createHmac, randomBytes } from 'node:crypto';
import test from 'node:test';
import type { GatewayAccountRecord } from '../src/types.js';
import { captureRoster, probeRoster, pushRosterSnapshot } from '../src/zalo/roster.js';

const account: GatewayAccountRecord = {
  id: 'local-account', tenantId: 'tenant', channelId: 'channel', accountExternalId: '10001',
  importEndpoint: 'https://historical-import.invalid/api/internal/imports/personal-zalo',
  importSecret: 'channel-secret', zaloUid: '10001', displayName: 'Bot', status: 'connected',
  createdAt: '', updatedAt: '', threads: {},
};

type Api = Parameters<typeof captureRoster>[0];

test('roster distinguishes IDs across threads and includes a Zalo group name', async () => {
  const api = {
    getAllFriends: async () => [{ userId: '20001', displayName: 'Bạn' }, { userId: '20001', displayName: 'Bạn' }],
    getAllGroups: async () => ({ gridVerMap: { '20001': '1' } }),
    getGroupInfo: async (ids: string[]) => {
      assert.deepEqual(ids, ['20001']);
      return { gridInfoMap: { '20001': { name: 'Nhóm' } } };
    },
  } as unknown as Api;
  const snapshot = await captureRoster(api, account, new Map());
  assert.equal(new Date(snapshot.captured_at).toISOString(), snapshot.captured_at);
  assert.deepEqual(snapshot.entries, [
    { thread_type: 'user', thread_id: '20001', display_name: 'Bạn' },
    { thread_type: 'group', thread_id: '20001', display_name: 'Nhóm' },
  ]);
});

test('only a complete empty response is safe to treat as an empty roster', async () => {
  const api = {
    getAllFriends: async () => [],
    getAllGroups: async () => ({ gridVerMap: {} }),
  } as unknown as Api;
  assert.deepEqual((await captureRoster(api, account, new Map())).entries, []);
  await assert.rejects(captureRoster({
    ...api, getAllGroups: async () => ({}) as Awaited<ReturnType<Api['getAllGroups']>>,
  }, account, new Map()), /invalid_zalo_roster_response/);
});

test('probe and push each use a fresh request ID and both valid signatures', async (t) => {
  const deployment = { baseUrl: 'http://app:8080', secret: randomBytes(32).toString('hex') };
  const requestIDs = new Set<string>();
  let enabled = false;
  const request = t.mock.method(globalThis, 'fetch', async (url: string, init: RequestInit) => {
    assert.equal(url, 'http://app:8080/api/internal/imports/personal-zalo/roster');
    const headers = new Headers(init.headers);
    const body = String(init.body);
    const payload = JSON.parse(body);
    const requestID = headers.get('X-CQA-Request-Id')!;
    const timestamp = headers.get('X-CQA-Timestamp')!;
    const digest = (key: string) => createHmac('sha256', key).update(`${timestamp}.${requestID}.${body}`).digest('hex');
    assert.equal(headers.get('X-CQA-Signature'), digest(account.importSecret));
    assert.equal(headers.get('X-CQA-Chatbot-Signature'), digest(deployment.secret));
    assert.equal(payload.request_id, requestID);
    assert.equal(payload.account_external_id, account.accountExternalId);
    assert.ok(!requestIDs.has(requestID));
    requestIDs.add(requestID);
    if (payload.check_only) {
      assert.equal(payload.entries, undefined);
      return new Response(null, { status: enabled ? 200 : 204 });
    }
    assert.deepEqual(payload.entries, []);
    return new Response(null, { status: 204 });
  });
  assert.equal(await probeRoster(account, deployment), false);
  enabled = true;
  assert.equal(await probeRoster(account, deployment), true);
  await pushRosterSnapshot(account, { account_external_id: account.accountExternalId, captured_at: new Date().toISOString(), entries: [] }, deployment);
  assert.equal(request.mock.callCount(), 3);
});

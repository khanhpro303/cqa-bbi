import { randomUUID } from 'node:crypto';
import { chatbotEndpoint } from '../chatbot/cqa-chatbot-client.js';
import type { PersonalZaloChatbotConfig } from '../config.js';
import { signInternalRequestBody } from '../sync/cqa-client.js';
import type { GatewayAccountRecord, ThreadType } from '../types.js';

interface RosterEntry {
  thread_type: ThreadType;
  thread_id: string;
  display_name?: string;
}

export interface RosterSnapshot {
  account_external_id: string;
  captured_at: string;
  entries: RosterEntry[];
}

interface RosterAPI {
  getAllFriends(count: number, page: number): Promise<unknown>;
  getAllGroups(): Promise<{ gridVerMap?: unknown }>;
  getGroupInfo(ids: string[]): Promise<{ gridInfoMap?: Record<string, { name?: string }> }>;
}

const zaloID = /^\d{1,64}$/;
const friendsPageSize = 20_000;
const maxRosterSize = 25_000;

async function withTimeout<T>(operation: Promise<T>, milliseconds: number): Promise<T> {
  let timeout: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([operation, new Promise<never>((_, reject) => {
      timeout = setTimeout(() => reject(new Error('zalo_roster_timeout')), milliseconds);
      timeout.unref();
    })]);
  } finally {
    clearTimeout(timeout);
  }
}

export async function captureRoster(
  api: RosterAPI,
  account: GatewayAccountRecord,
  groupNames: Map<string, string>,
): Promise<RosterSnapshot> {
  if (!zaloID.test(account.accountExternalId)) throw new Error('invalid_zalo_roster_account');
  const capturedAt = new Date().toISOString();
  const [firstPage, groups] = await withTimeout(Promise.all([
    api.getAllFriends(friendsPageSize, 1),
    api.getAllGroups(),
  ]), 5_000);
  if (!Array.isArray(firstPage) || !groups?.gridVerMap || typeof groups.gridVerMap !== 'object' || Array.isArray(groups.gridVerMap)) {
    throw new Error('invalid_zalo_roster_response');
  }
  let friends = firstPage;
  if (friends.length === friendsPageSize) {
    const nextPage = await withTimeout(api.getAllFriends(friendsPageSize, 2), 3_000);
    if (!Array.isArray(nextPage)) throw new Error('invalid_zalo_roster_response');
    friends = friends.concat(nextPage);
  }
  const groupIDs = Object.keys(groups.gridVerMap);
  if (friends.length + groupIDs.length > maxRosterSize ||
      friends.some((friend) => typeof friend?.userId !== 'string' || !zaloID.test(friend.userId)) ||
      groupIDs.some((id) => !zaloID.test(id) || id === account.accountExternalId)) {
    throw new Error('invalid_zalo_roster_response');
  }

  const uniqueFriends = new Map<string, RosterEntry>();
  for (const friend of friends) {
    if (friend.userId === account.accountExternalId) continue;
    const name: unknown = friend.displayName || friend.zaloName;
    uniqueFriends.set(friend.userId, { thread_type: 'user', thread_id: friend.userId, display_name: typeof name === 'string' ? name : undefined });
  }
  for (const id of groupIDs) {
    const name = account.threads[`group:${id}`]?.customerName;
    if (name && name !== id) groupNames.set(id, name);
  }
  const missingNames = groupIDs.filter((id) => !groupNames.has(id)).slice(0, 50);
  if (missingNames.length > 0) {
    // Names are optional. A group details outage should not hide memberships.
    const info = await withTimeout(api.getGroupInfo(missingNames), 2_000).catch(() => undefined);
    for (const id of missingNames) {
      const name = info?.gridInfoMap?.[id]?.name;
      if (name) groupNames.set(id, name);
    }
  }

  return {
    account_external_id: account.accountExternalId,
    captured_at: capturedAt,
    entries: [
      ...uniqueFriends.values(),
      ...groupIDs.map((id): RosterEntry => ({ thread_type: 'group', thread_id: id, display_name: groupNames.get(id) })),
    ],
  };
}

async function postRoster(
  account: GatewayAccountRecord,
  data: RosterSnapshot | { account_external_id: string; check_only: true },
  deployment: PersonalZaloChatbotConfig,
): Promise<number> {
  const base = chatbotEndpoint(deployment.baseUrl);
  if (!base) throw new Error('invalid_roster_endpoint');
  const endpoint = base.replace(/\/chatbot\/personal-zalo$/, '/imports/personal-zalo/roster');
  const requestId = randomUUID();
  const body = JSON.stringify({ request_id: requestId, tenant_id: account.tenantId, channel_id: account.channelId, ...data });
  const now = Date.now();
  const signed = signInternalRequestBody(account.importSecret, body, requestId, now);
  const response = await fetch(endpoint, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-CQA-Request-Id': signed.requestId,
      'X-CQA-Timestamp': signed.timestamp,
      'X-CQA-Signature': signed.signature,
      'X-CQA-Chatbot-Signature': signInternalRequestBody(deployment.secret, body, requestId, now).signature,
    },
    body,
    redirect: 'error',
    signal: AbortSignal.timeout(5_000),
  });
  return response.status;
}

export async function pushRosterSnapshot(
  account: GatewayAccountRecord,
  snapshot: RosterSnapshot,
  deployment: PersonalZaloChatbotConfig,
): Promise<void> {
  const status = await postRoster(account, snapshot, deployment);
  if (status !== 204) throw new Error(`roster_push_http_${status}`);
}

export async function probeRoster(account: GatewayAccountRecord, deployment: PersonalZaloChatbotConfig): Promise<boolean> {
  const status = await postRoster(account, { account_external_id: account.accountExternalId, check_only: true }, deployment);
  if (status === 204) return false;
  if (status !== 200) throw new Error(`roster_probe_http_${status}`);
  return true;
}

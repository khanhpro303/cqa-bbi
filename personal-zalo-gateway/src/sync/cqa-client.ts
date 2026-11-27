import { createHmac, randomUUID } from 'node:crypto';
import { logger } from '../logger.js';
import type { AccountSyncBatch, GatewayAccountRecord } from '../types.js';

export interface CQAImportResponse {
  request_id: string;
  conversations: number;
  messages_processed: number;
  messages_inserted: number;
  messages_deduplicated: number;
}

export class CQAImportError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    body: string,
  ) {
    super(`cqa_import_failed:${status}:${body}`);
    this.name = 'CQAImportError';
  }

  // CQA answers 401 invalid_internal_target when the batch's tenant/channel
  // no longer resolves to a personal Zalo import channel (usually because
  // the channel was deleted). Retrying the same batch can never succeed.
  get targetGone(): boolean {
    return this.status === 401 && this.code === 'invalid_internal_target';
  }
}

export function signInternalRequestBody(
  secret: string,
  body: string,
  requestId = randomUUID(),
  now = Date.now(),
): { requestId: string; timestamp: string; signature: string } {
  const timestamp = `${Math.floor(now / 1000)}`;
  const signature = createHmac('sha256', secret)
    .update(timestamp)
    .update('.')
    .update(requestId)
    .update('.')
    .update(body)
    .digest('hex');
  return { requestId, timestamp, signature };
}

export function buildSignedImportRequest(accountBatch: AccountSyncBatch): {
  url: string;
  requestId: string;
  timestamp: string;
  signature: string;
  body: string;
} {
  const requestId = randomUUID();
  const payload = {
    schema_version: '2026-04-08',
    request_id: requestId,
    tenant_id: accountBatch.account.tenantId,
    channel_id: accountBatch.account.channelId,
    account_external_id: accountBatch.account.accountExternalId || accountBatch.account.zaloUid || '',
    imported_at: new Date().toISOString(),
    conversations: accountBatch.conversations.map(({ conversation, messages }) => ({
      conversation,
      messages,
    })),
  };
  const body = JSON.stringify(payload);
  const signed = signInternalRequestBody(accountBatch.account.importSecret, body, requestId);

  return {
    url: accountBatch.account.importEndpoint,
    ...signed,
    body,
  };
}

export interface CQAImportSettings {
  active: boolean;
  sync_interval_minutes?: number;
}

export async function fetchImportSettings(account: GatewayAccountRecord, timeoutMs: number): Promise<CQAImportSettings | undefined> {
  const endpoint = new URL(account.importEndpoint);
  endpoint.pathname = `${endpoint.pathname.replace(/\/$/, '')}/settings`;
  const requestId = randomUUID();
  const body = JSON.stringify({
    request_id: requestId,
    tenant_id: account.tenantId,
    channel_id: account.channelId,
    account_external_id: account.accountExternalId || account.zaloUid,
  });
  const signed = signInternalRequestBody(account.importSecret, body, requestId);
  const response = await fetch(endpoint, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-CQA-Request-Id': signed.requestId,
      'X-CQA-Timestamp': signed.timestamp,
      'X-CQA-Signature': signed.signature,
    },
    body,
    redirect: 'error',
    signal: AbortSignal.timeout(timeoutMs),
  });

  if (response.status === 404) {
    // Older CQA releases did not have a settings route.
    await response.body?.cancel();
    return undefined;
  }
  if (response.status !== 200) {
    await response.body?.cancel();
    throw new Error(`cqa_import_settings_failed:${response.status}`);
  }

  const payload = await response.json() as Partial<CQAImportSettings> | null;
  const minutes = payload?.sync_interval_minutes;
  if (typeof payload?.active !== 'boolean' || (minutes !== undefined &&
      (!Number.isInteger(minutes) || minutes < 1 || minutes > 1440))) {
    throw new Error('invalid_import_settings');
  }
  return { active: payload.active, sync_interval_minutes: minutes };
}

export async function sendImportBatch(
  accountBatch: AccountSyncBatch,
  timeoutMs: number,
): Promise<CQAImportResponse> {
  const request = buildSignedImportRequest(accountBatch);
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);

  try {
    const response = await fetch(request.url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-CQA-Request-Id': request.requestId,
        'X-CQA-Timestamp': request.timestamp,
        'X-CQA-Signature': request.signature,
      },
      body: request.body,
      signal: controller.signal,
      redirect: 'error',
    });

    const text = await response.text();
    if (response.status !== 202) {
      let code = response.ok ? 'invalid_import_ack' : '';
      try {
        const responseCode = String((JSON.parse(text) as { error?: unknown }).error || '');
        if (responseCode) code = responseCode;
      } catch {}
      throw new CQAImportError(response.status, code, text);
    }

    let payload: unknown;
    try {
      payload = JSON.parse(text);
    } catch {
      throw new CQAImportError(response.status, 'invalid_import_ack', text);
    }
    if (!isValidImportAcknowledgement(payload, request.requestId, accountBatch)) {
      throw new CQAImportError(response.status, 'invalid_import_ack', text);
    }
    return payload;
  } catch (error) {
    logger.error('[sync] sendImportBatch failed', error);
    throw error;
  } finally {
    clearTimeout(timer);
  }
}

function isValidImportAcknowledgement(
  value: unknown,
  requestId: string,
  accountBatch: AccountSyncBatch,
): value is CQAImportResponse {
  if (!value || typeof value !== 'object') return false;
  const acknowledgement = value as Partial<CQAImportResponse>;
  const counters = [
    acknowledgement.messages_processed,
    acknowledgement.messages_inserted,
    acknowledgement.messages_deduplicated,
  ];
  if (acknowledgement.request_id !== requestId ||
      acknowledgement.conversations !== accountBatch.conversations.length ||
      !counters.every((counter) => Number.isSafeInteger(counter) && Number(counter) >= 0)) {
    return false;
  }

  const sentMessages = accountBatch.conversations.reduce((total, conversation) => total + conversation.messages.length, 0);
  return acknowledgement.messages_processed! <= sentMessages &&
    acknowledgement.messages_inserted! + acknowledgement.messages_deduplicated! === acknowledgement.messages_processed;
}

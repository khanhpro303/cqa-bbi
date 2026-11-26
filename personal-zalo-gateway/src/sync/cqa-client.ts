import { createHmac, randomUUID } from 'node:crypto';
import { logger } from '../logger.js';
import type { AccountSyncBatch } from '../types.js';

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
    });

    if (!response.ok) {
      const text = await response.text();
      let code = '';
      try {
        code = String((JSON.parse(text) as { error?: unknown }).error || '');
      } catch {
        code = '';
      }
      throw new CQAImportError(response.status, code, text);
    }

    return await response.json() as CQAImportResponse;
  } catch (error) {
    logger.error('[sync] sendImportBatch failed', error);
    throw error;
  } finally {
    clearTimeout(timer);
  }
}

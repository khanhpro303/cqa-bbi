import { randomUUID } from 'node:crypto';
import { personalZaloChatbotConfig, type PersonalZaloChatbotConfig } from '../config.js';
import { logger } from '../logger.js';
import { signInternalRequestBody } from '../sync/cqa-client.js';
import type { GatewayAccountRecord, IncomingGatewayMessage } from '../types.js';

const chatbotTimeoutMs = 55_000;
const deliveryReceiptTimeoutMs = 5_000;
const friendSystemEvent = /^(bạn và .* đã trở thành bạn bè|các bạn đã trở thành bạn bè|hai bạn đã trở thành bạn bè|đã chấp nhận yêu cầu kết bạn|you are now connected with|đã trở thành bạn bè trên zalo)/i;
const zaloUID = /^\d{1,64}$/;
const zaloMessageID = /^\d{1,128}$/;
const receiptUUID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

export interface PersonalZaloChatbotReply {
  reply: string;
  deliveryReceipt?: string;
}

export function chatbotEndpoint(baseUrl: string): string | null {
  try {
    const url = new URL(baseUrl);
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return null;
    if (url.username || url.password) return null;
    url.pathname = `${url.pathname.replace(/\/$/, '')}/api/internal/chatbot/personal-zalo`;
    url.search = '';
    url.hash = '';
    return url.toString();
  } catch {
    return null;
  }
}

export function isChatbotEligibleMessage(message: IncomingGatewayMessage, now = Date.now()): boolean {
  const text = message.content.trim();
  const ageMs = now - message.timestamp;
  const validThread = message.threadType === 'user'
    ? message.threadId === message.senderUid
    : message.threadType === 'group' && message.threadId !== message.senderUid;
  return validThread &&
    !message.isSelf &&
    message.sourceContentIsString === true &&
    message.sourceTimestampIsValid === true &&
    message.contentType === 'text' &&
    message.rawData?.message_type === 'webchat' &&
    zaloUID.test(message.senderUid) &&
    zaloUID.test(message.threadId) &&
    zaloMessageID.test(message.msgId) &&
    text.length > 0 &&
    message.content.length <= 2_000 &&
    ageMs <= 120_000 &&
    ageMs >= -30_000 &&
    !friendSystemEvent.test(text);
}

export function buildChatbotRequest(
  account: GatewayAccountRecord,
  message: IncomingGatewayMessage,
  deployment: PersonalZaloChatbotConfig,
  now = Date.now(),
  deliveryReceipt?: string,
): {
  url: string;
  requestId: string;
  timestamp: string;
  signature: string;
  chatbotSignature: string;
  body: string;
} | null {
  // A receipt follows a turn that already passed the freshness check. Keep
  // signing it at the current time while retaining the original sent_at.
  const eligibilityNow = deliveryReceipt ? message.timestamp : now;
  if (!isChatbotEligibleMessage(message, eligibilityNow)) return null;
  const url = chatbotEndpoint(deployment.baseUrl);
  const accountExternalId = account.accountExternalId || account.zaloUid || '';
  if (!url || deployment.secret.length < 32 || !zaloUID.test(accountExternalId)) return null;

  const requestId = randomUUID();
  if (deliveryReceipt !== undefined && (message.threadType !== 'group' || !receiptUUID.test(deliveryReceipt))) return null;
  const payload: Record<string, unknown> = {
    schema_version: message.threadType === 'group' ? 'personal-zalo-chatbot-v2' : 'personal-zalo-chatbot-v1',
    request_id: requestId,
    tenant_id: account.tenantId,
    channel_id: account.channelId,
    account_external_id: accountExternalId,
    thread_id: message.threadId,
    thread_type: message.threadType,
    sender_id: message.senderUid,
    message_id: message.msgId,
    sent_at: new Date(message.timestamp).toISOString(),
    text: message.content,
  };
  if (deliveryReceipt) payload.delivery_receipt = deliveryReceipt;
  const body = JSON.stringify(payload);
  const signed = signInternalRequestBody(account.importSecret, body, requestId, now);
  const chatbotSignature = signInternalRequestBody(deployment.secret, body, requestId, now).signature;
  return { url, body, ...signed, chatbotSignature };
}

export async function requestPersonalZaloChatbotReply(
  account: GatewayAccountRecord,
  message: IncomingGatewayMessage,
): Promise<PersonalZaloChatbotReply | null> {
  const deployment = personalZaloChatbotConfig();
  if (!deployment) return null;
  const request = buildChatbotRequest(account, message, deployment);
  if (!request) return null;

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), chatbotTimeoutMs);
  try {
    const response = await fetch(request.url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-CQA-Request-Id': request.requestId,
        'X-CQA-Timestamp': request.timestamp,
        'X-CQA-Signature': request.signature,
        'X-CQA-Chatbot-Signature': request.chatbotSignature,
      },
      body: request.body,
      signal: controller.signal,
      redirect: 'error',
    });
    if (response.status === 204) return null;
    if (!response.ok) {
      logger.warn(`[chatbot] CQA rejected personal Zalo turn with HTTP ${response.status}`);
      return null;
    }
    const payload = await response.json() as { reply?: unknown; delivery_receipt?: unknown };
    if (typeof payload.reply !== 'string' || payload.reply.trim() === '' || payload.reply.length > 2_000) {
      return null;
    }
    const reply: PersonalZaloChatbotReply = { reply: payload.reply };
    if (message.threadType === 'group' && typeof payload.delivery_receipt === 'string' && receiptUUID.test(payload.delivery_receipt)) {
      reply.deliveryReceipt = payload.delivery_receipt;
    }
    return reply;
  } catch (error) {
    logger.warn('[chatbot] CQA personal Zalo request failed', error);
    return null;
  } finally {
    clearTimeout(timer);
  }
}

export async function acknowledgePersonalZaloChatbotDelivery(
  account: GatewayAccountRecord,
  message: IncomingGatewayMessage,
  deliveryReceipt: string,
): Promise<void> {
  const deployment = personalZaloChatbotConfig();
  if (!deployment || message.threadType !== 'group') return;
  // The original turn already passed the live-message age check. Receipts use
  // that same immutable identity even when Zalo sending waited in the queue.
  const request = buildChatbotRequest(account, message, deployment, Date.now(), deliveryReceipt);
  if (!request) return;

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), deliveryReceiptTimeoutMs);
  try {
    const response = await fetch(request.url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-CQA-Request-Id': request.requestId,
        'X-CQA-Timestamp': request.timestamp,
        'X-CQA-Signature': request.signature,
        'X-CQA-Chatbot-Signature': request.chatbotSignature,
      },
      body: request.body,
      signal: controller.signal,
      redirect: 'error',
    });
    if (response.status !== 204) {
      logger.warn(`[chatbot] CQA rejected personal Zalo delivery receipt with HTTP ${response.status}`);
    }
  } catch (error) {
    logger.warn('[chatbot] CQA personal Zalo delivery receipt failed', error);
  } finally {
    clearTimeout(timer);
  }
}

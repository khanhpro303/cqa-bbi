import { randomUUID } from 'node:crypto';
import { personalZaloChatbotConfig, type PersonalZaloChatbotConfig } from '../config.js';
import { logger } from '../logger.js';
import { signInternalRequestBody } from '../sync/cqa-client.js';
import type { GatewayAccountRecord, IncomingGatewayMessage } from '../types.js';

const chatbotTimeoutMs = 55_000;
const friendSystemEvent = /^(bạn và .* đã trở thành bạn bè|các bạn đã trở thành bạn bè|hai bạn đã trở thành bạn bè|đã chấp nhận yêu cầu kết bạn|you are now connected with|đã trở thành bạn bè trên zalo)/i;
const zaloUID = /^\d{1,64}$/;
const zaloMessageID = /^\d{1,128}$/;

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
): {
  url: string;
  requestId: string;
  timestamp: string;
  signature: string;
  chatbotSignature: string;
  body: string;
} | null {
  if (!isChatbotEligibleMessage(message, now)) return null;
  const url = chatbotEndpoint(deployment.baseUrl);
  const accountExternalId = account.accountExternalId || account.zaloUid || '';
  if (!url || deployment.secret.length < 32 || !zaloUID.test(accountExternalId)) return null;

  const requestId = randomUUID();
  const body = JSON.stringify({
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
  });
  const signed = signInternalRequestBody(account.importSecret, body, requestId, now);
  const chatbotSignature = signInternalRequestBody(deployment.secret, body, requestId, now).signature;
  return { url, body, ...signed, chatbotSignature };
}

export async function requestPersonalZaloChatbotReply(
  account: GatewayAccountRecord,
  message: IncomingGatewayMessage,
): Promise<string | null> {
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
    const payload = await response.json() as { reply?: unknown };
    if (typeof payload.reply !== 'string' || payload.reply.trim() === '' || payload.reply.length > 2_000) {
      return null;
    }
    return payload.reply;
  } catch (error) {
    logger.warn('[chatbot] CQA personal Zalo request failed', error);
    return null;
  } finally {
    clearTimeout(timer);
  }
}

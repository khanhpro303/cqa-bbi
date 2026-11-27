import { randomUUID } from 'node:crypto';
import { personalZaloChatbotConfig, type PersonalZaloChatbotConfig } from '../config.js';
import { logger } from '../logger.js';
import { signInternalRequestBody } from '../sync/cqa-client.js';
import type { GatewayAccountRecord, IncomingGatewayMessage } from '../types.js';

const chatbotTimeoutMs = 55_000;
const deliveryReceiptTimeoutMs = 5_000;
const deliveryAuthorizationAttempts = 3;
const maxImageBytes = 1 << 20;
const maxEncodedImageLength = Math.ceil(maxImageBytes / 3) * 4;
const maxResponseBytes = 64 << 20;
const pngSignature = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
const friendSystemEvent = /^(bạn và .* đã trở thành bạn bè|các bạn đã trở thành bạn bè|hai bạn đã trở thành bạn bè|đã chấp nhận yêu cầu kết bạn|you are now connected with|đã trở thành bạn bè trên zalo)/i;
const zaloUID = /^\d{1,64}$/;
const zaloMessageID = /^\d{1,128}$/;
const receiptUUID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

export interface ChatbotReplyImage {
  filename: `${string}.${string}`;
  width: number;
  height: number;
  data: Buffer;
  deliveryToken?: string;
}

export interface PersonalZaloChatbotReply {
  reply: string;
  deliveryToken?: string;
  deliveryReceipt?: string;
  images?: ChatbotReplyImage[];
}

export interface PersonalZaloInternbotReply {
  groupId: string;
  text: string;
  token: string;
  images?: Array<ChatbotReplyImage & { deliveryToken: string }>;
}

export type PersonalZaloChatbotResult =
  | { outcome: 'reply'; reply: PersonalZaloChatbotReply }
  | { outcome: 'internbot_reply'; reply: PersonalZaloInternbotReply }
  | { outcome: 'no_reply' }
  | { outcome: 'retryable'; error: string }
  | { outcome: 'unknown'; error: string };

export type PersonalZaloDeliveryAuthorization = 'allowed' | 'denied' | 'retryable';

type DeliveryRequest =
  | { kind: 'group_receipt'; receipt: string }
  | { kind: 'direct_authorization'; token: string; part: 'text' | 'image'; digest: string }
  | { kind: 'internbot_authorization'; groupId: string; token: string; part: 'text' | 'image'; digest: string };

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

function groupMentionTurn(message: IncomingGatewayMessage, accountExternalId: string) {
  const { content, mentions } = message;
  if (message.threadType !== 'group' || !Array.isArray(mentions) || mentions.length === 0) {
    return { text: content };
  }
  const spans = mentions.filter((mention) => mention && typeof mention.uid === 'string' &&
    Number.isInteger(mention.pos) && Number.isInteger(mention.len) &&
    mention.pos >= 0 && mention.len > 0 && mention.pos + mention.len <= content.length);
  if (spans.length === 0) return { text: content };

  // Visible names of other people still matter to the question and classifier.
  // Remove their tags only from the text used to look for typed bot aliases.
  let botMentioned = false;
  let text = '';
  let wakeText = '';
  let end = 0;
  for (const { uid, pos, len } of spans.sort((a, b) => a.pos - b.pos)) {
    if (pos < end) continue;
    const ownTag = uid === accountExternalId;
    const beforeTag = content.slice(end, pos);
    wakeText += beforeTag + ' ';
    text += beforeTag + (ownTag ? ' ' : content.slice(pos, pos + len));
    botMentioned ||= ownTag;
    end = pos + len;
  }
  wakeText = (wakeText + content.slice(end)).trim();
  if (!botMentioned) return { text: content, wakeText };
  return { botMentioned, text: wakeText ? (text + content.slice(end)).trim() : '' };
}

export function buildChatbotRequest(
  account: GatewayAccountRecord,
  message: IncomingGatewayMessage,
  deployment: PersonalZaloChatbotConfig,
  now = Date.now(),
  deliveryRequest?: DeliveryRequest,
  internbotOnly = false,
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
  const eligibilityNow = deliveryRequest ? message.timestamp : now;
  if (!isChatbotEligibleMessage(message, eligibilityNow)) return null;
  const url = chatbotEndpoint(deployment.baseUrl);
  const accountExternalId = account.accountExternalId || account.zaloUid || '';
  if (!url || deployment.secret.length < 32 || !zaloUID.test(accountExternalId)) return null;
  const { botMentioned, text, wakeText } = groupMentionTurn(message, accountExternalId);
  if (!botMentioned && !(wakeText ?? text).trim()) return null;

  const requestId = randomUUID();
  if (deliveryRequest?.kind === 'group_receipt' &&
      (message.threadType !== 'group' || !receiptUUID.test(deliveryRequest.receipt))) return null;
  if (deliveryRequest?.kind === 'direct_authorization' &&
      (message.threadType !== 'user' || deliveryRequest.token.length < 16 || deliveryRequest.token.length > 4_096 ||
       !/^[a-f0-9]{64}$/.test(deliveryRequest.digest))) return null;
  if (deliveryRequest?.kind === 'internbot_authorization' &&
      (!validInternbotTarget(deliveryRequest.groupId, account, message) || !validDeliveryToken(deliveryRequest.token) ||
       !/^[a-f0-9]{64}$/.test(deliveryRequest.digest))) return null;
  const payload: Record<string, unknown> = {
    schema_version: internbotOnly ? 'personal-zalo-internbot-v1' :
      message.threadType === 'group' ? 'personal-zalo-chatbot-v2' : 'personal-zalo-chatbot-v3',
    request_id: requestId,
    tenant_id: account.tenantId,
    channel_id: account.channelId,
    account_external_id: accountExternalId,
    thread_id: message.threadId,
    thread_type: message.threadType,
    sender_id: message.senderUid,
    message_id: message.msgId,
    sent_at: new Date(message.timestamp).toISOString(),
    text,
  };
  if (!deliveryRequest) {
    payload.supports_internbot = true;
    const senderName = chatbotDisplayName(message.senderName);
    const sourceName = message.threadType === 'group' ? chatbotDisplayName(message.groupName) : senderName;
    if (senderName) payload.sender_name = senderName;
    if (sourceName) payload.source_name = sourceName;
    if (message.threadType === 'user') payload.multiple_images = true;
  }
  if (botMentioned) payload.bot_mentioned = true;
  if (wakeText !== undefined) payload.group_wake_text = wakeText;
  if (deliveryRequest?.kind === 'group_receipt') payload.delivery_receipt = deliveryRequest.receipt;
  if (deliveryRequest?.kind === 'direct_authorization' || deliveryRequest?.kind === 'internbot_authorization') {
    payload.delivery_token = deliveryRequest.token;
    payload.delivery_part = deliveryRequest.part;
    payload.delivery_digest = deliveryRequest.digest;
  }
  if (deliveryRequest?.kind === 'internbot_authorization') payload.supervisor_group_id = deliveryRequest.groupId;
  const body = JSON.stringify(payload);
  const signed = signInternalRequestBody(account.importSecret, body, requestId, now);
  const chatbotSignature = signInternalRequestBody(deployment.secret, body, requestId, now).signature;
  return { url, body, ...signed, chatbotSignature };
}

function chatbotDisplayName(value: string | undefined): string {
  return (value?.replace(/\s+/g, ' ').trim().slice(0, 128) ?? '').replace(/[\uD800-\uDBFF]$/, '');
}

function validDeliveryToken(value: unknown): value is string {
  return typeof value === 'string' && value.length >= 16 && value.length <= 4_096;
}

function validInternbotTarget(value: unknown, account: GatewayAccountRecord, message: IncomingGatewayMessage): value is string {
  return typeof value === 'string' && zaloUID.test(value) && value !== (account.accountExternalId || account.zaloUid) &&
    (message.threadType !== 'group' || value !== message.threadId);
}

export async function requestPersonalZaloChatbotReply(
  account: GatewayAccountRecord,
  message: IncomingGatewayMessage,
): Promise<PersonalZaloChatbotReply | null> {
  const result = await requestPersonalZaloChatbotTurn(account, message);
  return result.outcome === 'reply' ? result.reply : null;
}

export async function requestPersonalZaloChatbotTurn(
  account: GatewayAccountRecord,
  message: IncomingGatewayMessage,
  internbotOnly = false,
): Promise<PersonalZaloChatbotResult> {
  const deployment = personalZaloChatbotConfig();
  if (!deployment) return { outcome: 'no_reply' };
  const request = buildChatbotRequest(account, message, deployment, Date.now(), undefined, internbotOnly);
  if (!request) return { outcome: 'no_reply' };

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
    if (response.status === 204 || (internbotOnly && response.status === 400)) return { outcome: 'no_reply' };
    if (!response.ok) {
      logger.warn(`[chatbot] CQA rejected personal Zalo turn with HTTP ${response.status}`);
      return { outcome: 'unknown', error: `cqa_http_${response.status}` };
    }
    const payload = await readReplyPayload(response);
    if (payload && Object.hasOwn(payload, 'internbot_delivery')) {
      const delivery = decodeInternbotReply(payload.internbot_delivery, account, message);
      if (!delivery || Object.hasOwn(payload, 'reply')) return { outcome: 'unknown', error: 'invalid_cqa_internbot_reply' };
      return { outcome: 'internbot_reply', reply: delivery };
    }
    if (internbotOnly) return { outcome: 'no_reply' };
    if (typeof payload?.reply !== 'string' || payload.reply.trim() === '' || payload.reply.length > 2_000) {
      return { outcome: 'unknown', error: 'invalid_cqa_reply' };
    }
    const reply: PersonalZaloChatbotReply = { reply: payload.reply };
    if (message.threadType === 'group' && typeof payload.delivery_receipt === 'string' && receiptUUID.test(payload.delivery_receipt)) {
      reply.deliveryReceipt = payload.delivery_receipt;
    } else if (message.threadType === 'user') {
      if (typeof payload.delivery_token !== 'string' || payload.delivery_token.length < 16 || payload.delivery_token.length > 4_096) {
        return { outcome: 'unknown', error: 'invalid_cqa_delivery_token' };
      }
      reply.deliveryToken = payload.delivery_token;
      if (!Array.isArray(payload.images)) return { outcome: 'reply', reply };
      const images = payload.images.flatMap((value) => {
        const image = decodeReplyImage(value);
        return image ? [image] : [];
      });
      if (images.length > 0) reply.images = images;
    }
    return { outcome: 'reply', reply };
  } catch (error) {
    logger.warn('[chatbot] CQA personal Zalo request failed', error);
    if (isDefinitelyUnsentTransportError(error)) {
      return { outcome: 'retryable', error: String(error) };
    }
    return { outcome: 'unknown', error: String(error) };
  } finally {
    clearTimeout(timer);
  }
}

function isDefinitelyUnsentTransportError(error: unknown): boolean {
  const code = (error as { cause?: { code?: unknown } })?.cause?.code;
  return code === 'ECONNREFUSED' || code === 'ENETUNREACH' || code === 'EHOSTUNREACH' || code === 'EAI_AGAIN';
}

async function readReplyPayload(response: Response): Promise<{
  reply?: unknown;
  delivery_token?: unknown;
  images?: unknown;
  delivery_receipt?: unknown;
  internbot_delivery?: unknown;
} | null> {
  const reader = response.body?.getReader();
  if (!reader) return null;
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > maxResponseBytes) {
        await reader.cancel();
        return null;
      }
      chunks.push(value);
    }
    const payload: unknown = JSON.parse(Buffer.concat(chunks, size).toString('utf8'));
    return payload && typeof payload === 'object' && !Array.isArray(payload) ? payload : null;
  } finally {
    reader.releaseLock();
  }
}

function decodeInternbotReply(
  value: unknown,
  account: GatewayAccountRecord,
  message: IncomingGatewayMessage,
): PersonalZaloInternbotReply | null {
  if (!value || typeof value !== 'object') return null;
  const { group_id, text, token, images } = value as Record<string, unknown>;
  if (!validInternbotTarget(group_id, account, message) || !validDeliveryToken(token) ||
      typeof text !== 'string' || !text.trim() || text.length > 2_000) return null;
  const deliveries = Array.isArray(images) ? images.flatMap((value) => {
    const image = decodeReplyImage(value);
    if (!image?.deliveryToken) return [];
    return [{ ...image, deliveryToken: image.deliveryToken }];
  }) : [];
  return { groupId: group_id, text, token, images: deliveries };
}

export async function authorizePersonalZaloInternbotDelivery(
  account: GatewayAccountRecord,
  message: IncomingGatewayMessage,
  groupId: string,
  token: string,
  part: 'text' | 'image',
  digest: string,
): Promise<PersonalZaloDeliveryAuthorization> {
  const deployment = personalZaloChatbotConfig();
  if (!deployment) return 'denied';
  for (let attempt = 0; attempt < deliveryAuthorizationAttempts; attempt++) {
    const request = buildChatbotRequest(account, message, deployment, Date.now(), {
      kind: 'internbot_authorization', groupId, token, part, digest,
    });
    if (!request) return 'denied';
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), deliveryReceiptTimeoutMs);
    try {
      const response = await fetch(`${request.url}/internbot/delivery`, {
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
      if (response.status === 200) return 'allowed';
      if (response.status < 500) return 'denied';
    } catch (error) {
      logger.warn('[chatbot] CQA Internbot delivery authorization failed', error);
    } finally {
      clearTimeout(timer);
    }
    if (attempt + 1 < deliveryAuthorizationAttempts) await new Promise((resolve) => setTimeout(resolve, 250 * (attempt + 1)));
  }
  return 'retryable';
}

export async function authorizePersonalZaloChatbotDelivery(
  account: GatewayAccountRecord,
  message: IncomingGatewayMessage,
  deliveryToken: string,
  part: 'text' | 'image',
  digest: string,
): Promise<PersonalZaloDeliveryAuthorization> {
  const deployment = personalZaloChatbotConfig();
  if (!deployment || message.threadType !== 'user') return 'denied';

  for (let attempt = 0; attempt < deliveryAuthorizationAttempts; attempt++) {
    const request = buildChatbotRequest(account, message, deployment, Date.now(), {
      kind: 'direct_authorization', token: deliveryToken, part, digest,
    });
    if (!request) return 'denied';
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
      if (response.status === 204) return 'denied';
      if (response.ok) {
        const payload = await response.json().catch(() => null) as { allowed?: unknown } | null;
        return payload?.allowed === true ? 'allowed' : 'denied';
      }
      if (response.status < 500) return 'denied';
    } catch (error) {
      logger.warn('[chatbot] CQA personal Zalo delivery authorization failed', error);
    } finally {
      clearTimeout(timer);
    }
    if (attempt + 1 < deliveryAuthorizationAttempts) await new Promise((resolve) => setTimeout(resolve, 250 * (attempt + 1)));
  }
  return 'retryable';
}

export async function authorizePersonalZaloGroupAccess(
  account: GatewayAccountRecord,
  message: IncomingGatewayMessage,
  sentAt: string,
): Promise<PersonalZaloDeliveryAuthorization> {
  const deployment = personalZaloChatbotConfig();
  if (!deployment || message.threadType !== 'group' || !/^\d{1,64}$/.test(message.threadId)) return 'denied';
  const url = chatbotEndpoint(deployment.baseUrl);
  const accountExternalId = account.accountExternalId || account.zaloUid || '';
  if (!url || !/^\d{1,64}$/.test(accountExternalId)) return 'denied';
  const endpoint = `${url}/thread-access`;
  for (let attempt = 0; attempt < deliveryAuthorizationAttempts; attempt++) {
    const requestId = randomUUID();
    const body = JSON.stringify({ request_id: requestId, tenant_id: account.tenantId, channel_id: account.channelId,
      account_external_id: accountExternalId, thread_type: 'group', thread_id: message.threadId, sent_at: sentAt });
    const now = Date.now();
    const signed = signInternalRequestBody(account.importSecret, body, requestId, now);
    const chatbotSignature = signInternalRequestBody(deployment.secret, body, requestId, now).signature;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), deliveryReceiptTimeoutMs);
    try {
      const response = await fetch(endpoint, { method: 'POST', headers: {
        'Content-Type': 'application/json', 'X-CQA-Request-Id': requestId, 'X-CQA-Timestamp': signed.timestamp,
        'X-CQA-Signature': signed.signature, 'X-CQA-Chatbot-Signature': chatbotSignature,
      }, body, signal: controller.signal, redirect: 'error' });
      if (response.status === 204) return 'denied';
      if (response.status === 200) return 'allowed';
      if (response.status < 500) return 'denied';
    } catch (error) {
      logger.warn('[chatbot] CQA personal Zalo group authorization failed', error);
    } finally { clearTimeout(timer); }
    if (attempt + 1 < deliveryAuthorizationAttempts) await new Promise((resolve) => setTimeout(resolve, 250 * (attempt + 1)));
  }
  return 'retryable';
}

function decodeReplyImage(value: unknown): ChatbotReplyImage | null {
  if (!value || typeof value !== 'object') return null;
  const { filename, media_type, width, height, data_base64, delivery_token } = value as Record<string, unknown>;
  if (typeof filename !== 'string' || filename.trim() !== filename || !/^[a-z\d_-]{1,80}\.(?:jpe?g|png)$/i.test(filename) ||
      typeof width !== 'number' || !Number.isSafeInteger(width) || width <= 0 ||
      typeof height !== 'number' || !Number.isSafeInteger(height) || height <= 0 ||
      width * height > 20_000_000 ||
      typeof data_base64 !== 'string' || data_base64.length > maxEncodedImageLength ||
      data_base64.length % 4 !== 0 || !/^[A-Za-z0-9+/]+={0,2}$/.test(data_base64)) {
    return null;
  }

  const data = Buffer.from(data_base64, 'base64');
  if (data.length > maxImageBytes || data.toString('base64') !== data_base64) return null;
  const isPNG = /\.png$/i.test(filename) && media_type === 'image/png' &&
    data.subarray(0, pngSignature.length).equals(pngSignature);
  const isJPEG = /\.jpe?g$/i.test(filename) && media_type === 'image/jpeg' &&
    data[0] === 0xff && data[1] === 0xd8 && data[2] === 0xff;
  if (!isPNG && !isJPEG) return null;
  if (delivery_token !== undefined &&
      (typeof delivery_token !== 'string' || delivery_token.length < 16 || delivery_token.length > 4_096)) return null;
  return { filename: filename as ChatbotReplyImage['filename'], width, height, data,
    ...(typeof delivery_token === 'string' ? { deliveryToken: delivery_token } : {}) };
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
  const request = buildChatbotRequest(account, message, deployment, Date.now(), {
    kind: 'group_receipt', receipt: deliveryReceipt,
  });
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

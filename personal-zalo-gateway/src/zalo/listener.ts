import { logger } from '../logger.js';
import type { GatewayStore } from '../store/file-store.js';
import type { IncomingGatewayMessage, ThreadType } from '../types.js';
import { detectContentType, messageContentToString } from './helpers.js';

async function resolveZaloName(api: any, uid: string): Promise<{ zaloName: string; avatar: string }> {
  try {
    const result = await api.getUserInfo(uid);
    const profiles = result?.changed_profiles || {};
    const profile = profiles[uid] || profiles[`${uid}_0`];
    return {
      zaloName:
        profile?.zaloName ||
        profile?.zalo_name ||
        profile?.displayName ||
        profile?.display_name ||
        '',
      avatar: profile?.avatar || '',
    };
  } catch (error) {
    logger.warn(`[zalo] getUserInfo failed for ${uid}`, error);
    return { zaloName: '', avatar: '' };
  }
}

async function resolveGroupName(api: any, groupId: string): Promise<string> {
  try {
    const result = await api.getGroupInfo(groupId);
    return result?.gridInfoMap?.[groupId]?.name || '';
  } catch (error) {
    logger.warn(`[zalo] getGroupInfo failed for ${groupId}`, error);
    return '';
  }
}

export interface ListenerContext {
  accountId: string;
  api: any;
  store: GatewayStore;
  onDisconnected: (accountId: string) => Promise<void>;
  onConnected?: () => void;
  onLiveMessage?: (message: IncomingGatewayMessage) => Promise<void>;
  onSelfMessage?: (threadId: string, text: string) => void;
}

async function persistMessage(
  store: GatewayStore,
  accountId: string,
  api: any,
  message: any,
): Promise<{ inserted: boolean; message: IncomingGatewayMessage }> {
  const isGroup = message.type === 1;
  const threadType: ThreadType = isGroup ? 'group' : 'user';
  const senderUid = String(message.data?.uidFrom || '');
  let senderName = message.data?.dName || '';

  if (!message.isSelf && senderUid && api.getUserInfo) {
    const userInfo = await resolveZaloName(api, senderUid);
    if (userInfo.zaloName) {
      senderName = userInfo.zaloName;
    }
  }

  let groupName: string | undefined;
  if (isGroup && message.threadId) {
    groupName = await resolveGroupName(api, message.threadId);
  }

  const rawContent = message.data?.content;
  
  const explicitTimestamp = parseExplicitZaloTimestamp(message.data?.ts);
  let fallbackTimestamp = Number(message.data?.time || message.timestamp || message.time ||
    String(message.data?.cliMsgId || '').split('_')[0]);
  if (fallbackTimestamp > 0 && fallbackTimestamp < 100_000_000_000) fallbackTimestamp *= 1000;

  const incomingMessage: IncomingGatewayMessage = {
    senderUid,
    senderName,
    content: messageContentToString(rawContent),
    contentType: detectContentType(message.data?.msgType, rawContent),
    msgId: String(message.data?.msgId || ''),
    timestamp: explicitTimestamp ?? (Number.isFinite(fallbackTimestamp) && fallbackTimestamp > 0 &&
      fallbackTimestamp <= 8.64e15 ? fallbackTimestamp : Date.now()),
    isSelf: Boolean(message.isSelf),
    threadId: String(message.threadId || ''),
    threadType,
    groupName,
    attachments: [],
    rawData: {
      message_type: message.data?.msgType || '',
    },
    sourceContentIsString: typeof rawContent === 'string',
    sourceTimestampIsValid: explicitTimestamp !== null,
  };
  const inserted = await store.recordIncomingMessage(accountId, incomingMessage);
  return { inserted, message: incomingMessage };
}

function parseExplicitZaloTimestamp(value: unknown): number | null {
  const raw = typeof value === 'number' ? String(value) : value;
  if (typeof raw !== 'string' || !/^\d+$/.test(raw)) return null;
  const parsed = Number(raw);
  if (!Number.isSafeInteger(parsed) || parsed <= 0) return null;
  const milliseconds = parsed < 100_000_000_000 ? parsed * 1000 : parsed;
  return milliseconds <= 8.64e15 ? milliseconds : null;
}

export function attachZaloListener(ctx: ListenerContext): void {
  const { accountId, api, store, onConnected, onDisconnected, onLiveMessage, onSelfMessage } = ctx;
  const listener = api.listener;

  listener.on('connected', () => {
    onConnected?.();
    logger.info(`[zalo:${accountId}] listener connected`);
    setTimeout(() => {
      try {
        listener.requestOldMessages(0);
        listener.requestOldMessages(1);
      } catch (error) {
        logger.warn(`[zalo:${accountId}] requestOldMessages failed`, error);
      }
    }, 1000);
  });

  listener.on('message', async (message: any) => {
    try {
      if (onSelfMessage && message.isSelf && message.type === 0) {
        onSelfMessage(String(message.threadId || ''), messageContentToString(message.data?.content));
      }
      const persisted = await persistMessage(store, accountId, api, message);
      if (persisted.inserted && !persisted.message.isSelf && onLiveMessage) {
        return onLiveMessage(persisted.message).catch((error) => {
          logger.error(`[zalo:${accountId}] live chatbot dispatch failed`, error);
        });
      }
    } catch (error) {
      logger.error(`[zalo:${accountId}] message listener failed`, error);
    }
  });

  listener.on('old_messages', async (messages: any[]) => {
    try {
      for (const message of messages) {
        await persistMessage(store, accountId, api, message);
      }
      logger.info(`[zalo:${accountId}] bootstrapped ${messages.length} old messages`);
    } catch (error) {
      logger.error(`[zalo:${accountId}] old_messages listener failed`, error);
    }
  });

  listener.on('undo', async (data: any) => {
    const msgId = String(data?.data?.msgId || data?.msgId || '');
    if (!msgId) return;
    await store.markMessageDeleted(accountId, msgId);
  });

  listener.on('closed', (code: number, reason: string) => {
    logger.warn(`[zalo:${accountId}] listener closed code=${code} reason=${reason}`);
    return onDisconnected(accountId).catch((error) => {
      logger.error(`[zalo:${accountId}] failed to record listener closure`, error);
    });
  });

  listener.on('error', (error: unknown) => {
    logger.error(`[zalo:${accountId}] listener error`, error);
  });

  listener.start({ retryOnClose: true });
}

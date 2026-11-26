import { createRequire } from 'node:module';
import { personalZaloChatbotConfig } from '../config.js';
import { logger } from '../logger.js';
import type { GatewayStore } from '../store/file-store.js';
import type { GatewayAccountRecord, IncomingGatewayMessage, SessionData } from '../types.js';
import {
  acknowledgePersonalZaloChatbotDelivery,
  isChatbotEligibleMessage,
  requestPersonalZaloChatbotReply,
  type ChatbotReplyImage,
  type PersonalZaloChatbotReply,
} from '../chatbot/cqa-chatbot-client.js';
import { FloodDetector, RateLimiter, SelfEchoShield } from '../chatbot/zalo-message-guards.js';
import { attachZaloListener, type ChatbotSelfMessage } from './listener.js';

const require = createRequire(import.meta.url);
// eslint-disable-next-line @typescript-eslint/no-require-imports
const { Zalo } = require('zca-js') as { Zalo: new (opts: { logging: boolean; selfListen?: boolean }) => any };
const outboundMessageId = /^\d{1,128}$/;

interface ZaloInstance {
  zalo: any;
  api: any;
  status: 'connected' | 'disconnected' | 'qr_pending' | 'connecting';
  listenerClosed?: boolean;
  chatbot?: ChatbotSession;
}

interface ActiveChatbotThread {
  cancelled: boolean;
  threadId: string;
  threadType: IncomingGatewayMessage['threadType'];
  latest?: IncomingGatewayMessage;
  pendingEcho?: {
    matches: (message: ChatbotSelfMessage) => boolean;
    candidates: Set<string>;
  };
}

interface ChatbotSession {
  threads: Map<string, ActiveChatbotThread>;
  guards: AccountChatbotGuards;
}

interface AccountChatbotGuards {
  ownId: string;
  rateLimiter: RateLimiter;
  echoShield: SelfEchoShield;
  directEchoIds: SelfEchoShield;
  floodDetector: FloodDetector;
}

type ChatbotOutboundMessage = string | {
  msg: '';
  attachments: [{
    data: Buffer;
    filename: ChatbotReplyImage['filename'];
    metadata: { totalSize: number; width: number; height: number };
  }];
};

interface ZaloSendReceipt {
  message: { msgId: string | number } | null;
  attachment: { msgId: string | number }[];
}

function cancelChatbotTurn(active: ActiveChatbotThread): void {
  active.cancelled = true;
  active.latest = undefined;
}

export class ZaloAccountPool {
  private readonly instances = new Map<string, ZaloInstance>();
  private readonly disconnectHistory = new Map<string, number[]>();
  private readonly chatbotGuards = new Map<string, AccountChatbotGuards>();

  constructor(private readonly store: GatewayStore) {}

  async bootstrapReconnects(): Promise<void> {
    const accounts = await this.store.listAccounts();
    for (const account of accounts) {
      if (!account.sessionData?.imei) continue;
      await this.reconnect(account.id, account.sessionData);
    }
  }

  async loginQR(accountId: string): Promise<void> {
    const account = await this.store.getAccount(accountId);
    if (!account) {
      throw new Error(`account_not_found:${accountId}`);
    }

    const zalo = new Zalo({ logging: false, selfListen: true });
    this.instances.set(accountId, { zalo, api: null, status: 'qr_pending' });
    await this.store.setAccountRuntime(accountId, {
      status: 'qr_pending',
      qrImage: '',
      qrGeneratedAt: new Date().toISOString(),
      lastError: '',
    });

    try {
      const api = await zalo.loginQR({}, async (event: any) => {
        switch (event.type) {
          case 0:
            await this.store.setAccountRuntime(accountId, {
              status: 'qr_pending',
              qrImage: event.data?.image || '',
              qrGeneratedAt: new Date().toISOString(),
            });
            break;
          case 1:
            await this.store.setAccountRuntime(accountId, {
              status: 'qr_pending',
              lastError: 'qr_expired',
            });
            event.actions?.retry?.();
            break;
          case 2:
            await this.store.setAccountRuntime(accountId, {
              displayName: event.data?.display_name || account.displayName,
            });
            break;
          case 4:
            await this.store.setAccountRuntime(accountId, {
              sessionData: {
                cookie: event.data?.cookie,
                imei: event.data?.imei,
                userAgent: event.data?.userAgent,
              },
            });
            break;
        }
      });
      await this.completeLogin(accountId, api);
    } catch (error) {
      await this.store.setAccountRuntime(accountId, {
        status: 'disconnected',
        lastError: String(error),
      });
      throw error;
    }
  }

  async reconnect(accountId: string, credentials: SessionData): Promise<void> {
    const zalo = new Zalo({ logging: false, selfListen: true });
    this.instances.set(accountId, { zalo, api: null, status: 'connecting' });
    await this.store.setAccountRuntime(accountId, {
      status: 'connecting',
      lastError: '',
    });

    try {
      const api = await zalo.login({
        cookie: credentials.cookie,
        imei: credentials.imei,
        userAgent: credentials.userAgent,
      });
      await this.completeLogin(accountId, api);
    } catch (error) {
      await this.store.setAccountRuntime(accountId, {
        status: 'qr_pending',
        lastError: String(error),
      });
      logger.warn(`[zalo:${accountId}] reconnect failed`, error);
    }
  }

  disconnect(accountId: string): void {
    const instance = this.instances.get(accountId);
    if (instance?.api?.listener) {
      try {
        instance.api.listener.stop();
      } catch (error) {
        logger.warn(`[zalo:${accountId}] listener stop failed`, error);
      }
    }
    this.instances.delete(accountId);
    this.chatbotGuards.delete(accountId);
  }

  private async completeLogin(accountId: string, api: any): Promise<void> {
    const instance = this.instances.get(accountId);
    if (!instance) {
      throw new Error(`instance_not_found:${accountId}`);
    }

    instance.api = api;
    instance.status = 'connected';

    const ownId = String(await api.getOwnId());
    let displayName = '';
    let avatarUrl = '';
    try {
      const userInfo = await api.getUserInfo(ownId);
      const profiles = userInfo?.changed_profiles || {};
      const profile = profiles[ownId] || profiles[`${ownId}_0`];
      displayName =
        profile?.zaloName ||
        profile?.zalo_name ||
        profile?.displayName ||
        profile?.display_name ||
        '';
      avatarUrl = profile?.avatar || '';
    } catch (error) {
      logger.warn(`[zalo:${accountId}] getOwnProfile failed`, error);
    }

    if (this.instances.get(accountId) !== instance) return;
    const saved = await this.store.setAccountRuntime(accountId, {
      status: 'connected',
      zaloUid: ownId,
      accountExternalId: ownId,
      displayName,
      avatarUrl,
      qrImage: '',
      lastError: '',
    });

    if (!saved || this.instances.get(accountId) !== instance) return;

    if (personalZaloChatbotConfig()) {
      const previous = this.chatbotGuards.get(accountId);
      const sameZaloAccount = [...this.chatbotGuards.values()].find((guards) => guards.ownId === ownId);
      const guards = previous?.ownId === ownId ? previous : {
        ownId,
        rateLimiter: sameZaloAccount?.rateLimiter ?? new RateLimiter(),
        echoShield: new SelfEchoShield(),
        directEchoIds: new SelfEchoShield(),
        floodDetector: new FloodDetector(),
      };
      this.chatbotGuards.set(accountId, guards);
      instance.chatbot = { threads: new Map(), guards };
    }
    const chatbot = instance.chatbot;
    attachZaloListener({
      accountId,
      api,
      store: this.store,
      onConnected: () => { instance.listenerClosed = false; },
      onDisconnected: (id) => {
        instance.listenerClosed = true;
        if (this.instances.get(id) !== instance) return Promise.resolve();
        return this.handleDisconnect(id);
      },
      onLiveMessage: instance.chatbot ? (message) => this.handleLiveMessage(accountId, ownId, instance, message) : undefined,
      onSelfMessage: chatbot
        ? (threadId, threadType, message) => this.handleSelfMessage(chatbot, threadId, threadType, message)
        : undefined,
    });
  }

  private handleSelfMessage(
    chatbot: ChatbotSession,
    threadId: string,
    threadType: IncomingGatewayMessage['threadType'],
    message: ChatbotSelfMessage,
  ): void {
    const threadKey = chatbotThreadKey(threadType, threadId);
    if (threadType === 'group') {
      if (chatbot.guards.echoShield.isEcho(threadKey, message.text)) return;
      for (const active of chatbot.threads.values()) {
        if (active.threadType === 'group' && active.threadId === threadId) cancelChatbotTurn(active);
      }
      return;
    }

    const active = chatbot.threads.get(threadKey);
    if (!active || chatbot.guards.directEchoIds.isEcho(threadKey, message.id)) return;

    // The socket can receive the echo before sendMessage returns its receipt.
    const pending = active.pendingEcho;
    if (pending?.matches(message) && outboundMessageId.test(message.id) && pending.candidates.size < 8) {
      pending.candidates.add(message.id);
      return;
    }
    cancelChatbotTurn(active);
  }

  private async handleLiveMessage(
    accountId: string,
    ownId: string,
    instance: ZaloInstance,
    message: IncomingGatewayMessage,
  ): Promise<void> {
    const chatbot = instance.chatbot;
    if (!chatbot || !this.canSendChatbotReply(accountId, instance) ||
        message.senderUid === ownId || message.threadId === ownId || !isChatbotEligibleMessage(message)) {
      return;
    }
    const threadKey = chatbotThreadKey(message.threadType, message.threadId, message.senderUid);
    const existing = chatbot.threads.get(threadKey);
    if (chatbot.guards.floodDetector.isFlooding(message.senderUid)) {
      if (existing) {
        existing.cancelled = true;
        existing.latest = undefined;
      }
      return;
    }
    if (chatbot.guards.echoShield.isEcho(chatbotThreadKey(message.threadType, message.threadId), message.content)) return;
    if (existing) {
      if (!existing.latest || message.timestamp >= existing.latest.timestamp) existing.latest = message;
      return;
    }
    const activeOfType = [...chatbot.threads.values()].filter((active) => active.threadType === message.threadType).length;
    if (activeOfType >= 3) return;

    const active: ActiveChatbotThread = {
      cancelled: false,
      threadId: message.threadId,
      threadType: message.threadType,
    };
    chatbot.threads.set(threadKey, active);
    try {
      let current: IncomingGatewayMessage | undefined = message;
      while (current && this.canSendChatbotReply(accountId, instance)) {
        active.latest = undefined;
        active.cancelled = false;
        const account = await this.store.getAccount(accountId);
        if (account?.status === 'connected' && account.zaloUid === ownId && account.accountExternalId === ownId &&
            !active.cancelled && this.canSendChatbotReply(accountId, instance)) {
          const response = await requestPersonalZaloChatbotReply(account, current);
          if (response && !active.cancelled) {
            const sent = await this.sendChatbotReply(chatbot, active, instance, account, current, response);
            if (sent && response.deliveryReceipt) {
              await acknowledgePersonalZaloChatbotDelivery(account, current, response.deliveryReceipt);
            }
          }
        }
        current = active.latest;
      }
    } finally {
      chatbot.threads.delete(threadKey);
    }
  }

  private canSendChatbotReply(accountId: string, instance: ZaloInstance): boolean {
    return this.instances.get(accountId) === instance && instance.status === 'connected' && !instance.listenerClosed;
  }

  private async sendChatbotReply(
    chatbot: ChatbotSession,
    active: ActiveChatbotThread,
    instance: ZaloInstance,
    expectedAccount: GatewayAccountRecord,
    message: IncomingGatewayMessage,
    reply: PersonalZaloChatbotReply,
  ): Promise<boolean> {
    const accountId = expectedAccount.id;
    const { rateLimiter, echoShield, floodDetector } = chatbot.guards;
    const threadKey = chatbotThreadKey(message.threadType, message.threadId);
    const send = async (outbound: ChatbotOutboundMessage): Promise<boolean> => {
      let sent = false;
      await rateLimiter.schedule(async (recordAttempt) => {
        if (active.cancelled || !this.canSendChatbotReply(accountId, instance)) return;
        const account = await this.store.getAccount(accountId);
        if (active.cancelled || !this.canSendChatbotReply(accountId, instance) || floodDetector.isMuted(message.senderUid) ||
            account?.status !== 'connected' || account.zaloUid !== expectedAccount.zaloUid ||
            account.tenantId !== expectedAccount.tenantId || account.channelId !== expectedAccount.channelId ||
            account.accountExternalId !== expectedAccount.accountExternalId || account.importSecret !== expectedAccount.importSecret) {
          return;
        }
        if (typeof outbound === 'string') echoShield.recordSent(threadKey, outbound);
        recordAttempt();
        if (message.threadType === 'group') {
          await instance.api.sendMessage(outbound, message.threadId, 1);
        } else {
          await this.sendDirectChatbotMessage(chatbot, active, instance, message.threadId, outbound);
        }
        sent = true;
      });
      return sent;
    };

    let textSent = false;
    try {
      textSent = await send(reply.reply);
      const image = message.threadType === 'user' ? reply.images?.[0] : undefined;
      if (textSent && image && !active.cancelled) {
        await send({
          msg: '',
          attachments: [{
            data: image.data,
            filename: image.filename,
            metadata: { totalSize: image.data.length, width: image.width, height: image.height },
          }],
        });
      }
    } catch (error) {
      logger.warn(`[zalo:${accountId}] chatbot send failed`, error);
    }
    return textSent;
  }

  private async sendDirectChatbotMessage(
    chatbot: ChatbotSession,
    active: ActiveChatbotThread,
    instance: ZaloInstance,
    threadId: string,
    outbound: ChatbotOutboundMessage,
  ): Promise<void> {
    const pending: NonNullable<ActiveChatbotThread['pendingEcho']> = {
      candidates: new Set(),
      matches: typeof outbound === 'string'
        ? (event) => event.contentType === 'text' && event.text === outbound
        : (event) => event.contentType === 'image' && !event.description,
    };
    active.pendingEcho = pending;
    try {
      const result: ZaloSendReceipt | undefined = await instance.api.sendMessage(outbound, threadId, 0);
      const receipt = typeof outbound === 'string' ? result?.message : result?.attachment?.[0];
      const id = String(receipt?.msgId ?? '');
      if (outboundMessageId.test(id)) {
        pending.candidates.delete(id);
        chatbot.guards.directEchoIds.recordSent(chatbotThreadKey('user', threadId), id);
      }
    } finally {
      if (pending.candidates.size > 0) cancelChatbotTurn(active);
      active.pendingEcho = undefined;
    }
  }

  private async handleDisconnect(accountId: string): Promise<void> {
    await this.store.setAccountRuntime(accountId, {
      status: 'disconnected',
    });

    const now = Date.now();
    const history = (this.disconnectHistory.get(accountId) || []).filter((value) => now - value < 5 * 60_000);
    history.push(now);
    this.disconnectHistory.set(accountId, history);

    if (history.length >= 5) {
      await this.store.setAccountRuntime(accountId, {
        status: 'qr_pending',
        lastError: 'session_unstable_qr_required',
      });
      this.disconnectHistory.delete(accountId);
      return;
    }

    setTimeout(async () => {
      const account = await this.store.getAccount(accountId);
      if (!account?.sessionData?.imei) {
        return;
      }
      await this.reconnect(accountId, account.sessionData);
    }, 30_000);
  }
}

function chatbotThreadKey(threadType: IncomingGatewayMessage['threadType'], threadId: string, senderUid = ''): string {
  if (threadType === 'group' && senderUid) return `group:${threadId}:${senderUid}`;
  return `${threadType}:${threadId}`;
}

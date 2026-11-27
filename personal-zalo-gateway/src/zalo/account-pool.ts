import { createRequire } from 'node:module';
import { setTimeout as delay } from 'node:timers/promises';
import { personalZaloChatbotConfig } from '../config.js';
import { logger } from '../logger.js';
import type { GatewayStore } from '../store/file-store.js';
import type {
  GatewayAccountRecord,
  GatewayChatbotReply,
  GatewayChatbotTurnRecord,
  IncomingGatewayMessage,
  SessionData,
} from '../types.js';
import {
  acknowledgePersonalZaloChatbotDelivery,
  isChatbotEligibleMessage,
  requestPersonalZaloChatbotTurn,
  type ChatbotReplyImage,
  type PersonalZaloChatbotReply,
} from '../chatbot/cqa-chatbot-client.js';
import { FloodDetector, RateLimiter, SelfEchoShield } from '../chatbot/zalo-message-guards.js';
import { attachZaloListener, type ChatbotSelfMessage } from './listener.js';

const require = createRequire(import.meta.url);
// eslint-disable-next-line @typescript-eslint/no-require-imports
const { Zalo } = require('zca-js') as { Zalo: new (opts: { logging: boolean; selfListen?: boolean }) => any };
const outboundMessageId = /^\d{1,128}$/;
const retryableCloseCodes = new Set([1001, 1006, 1011, 1012, 1013]);

interface ZaloInstance {
  generation: number;
  zalo: any;
  api: any;
  status: 'connected' | 'disconnected' | 'qr_pending' | 'connecting';
  listenerClosed?: boolean;
  listenerOpened?: boolean;
  chatbot?: ChatbotSession;
}

export interface PoolHealthSnapshot {
  shuttingDown: boolean;
  totalAccounts: number;
  connectedAccounts: number;
  connectingAccounts: number;
  disconnectedAccounts: number;
  qrPendingAccounts: number;
  scheduledReconnects: number;
  activeChatbotTurns: number;
  queuedChatbotTurns: number;
  unknownChatbotTurns: number;
  failedChatbotTurns: number;
}

interface ActiveChatbotThread {
  cancelled: boolean;
  threadId: string;
  threadType: IncomingGatewayMessage['threadType'];
  pendingEcho?: {
    matches: (message: ChatbotSelfMessage) => boolean;
    candidates: Set<string>;
  };
}

interface ChatbotSession {
  threads: Map<string, ActiveChatbotThread>;
  tasks: Set<Promise<void>>;
  drainPromise?: Promise<void>;
  rescanRequested?: boolean;
  wakeDrain?: () => void;
  waiters: Map<string, { promise: Promise<void>; resolve: () => void }>;
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
}

type ChatbotSendOutcome = 'sent' | 'cancelled' | 'deferred' | 'unknown' | 'failed';

export class ZaloAccountPool {
  private readonly instances = new Map<string, ZaloInstance>();
  private readonly disconnectHistory = new Map<string, number[]>();
  private readonly chatbotGuards = new Map<string, AccountChatbotGuards>();
  private readonly reconnectTimers = new Map<string, ReturnType<typeof setTimeout>>();
  private readonly pendingOperations = new Set<Promise<unknown>>();
  private readonly connectionGenerations = new Map<string, number>();
  private readonly connectionAttempts = new Map<string, { kind: 'qr' | 'reconnect'; promise: Promise<void> }>();
  private shuttingDown = false;

  constructor(private readonly store: GatewayStore) {}

  async bootstrapReconnects(): Promise<void> {
    const accounts = await this.store.listAccounts();
    for (const account of accounts) {
      if (!account.sessionData?.imei) continue;
      await this.reconnect(account.id, account.sessionData);
    }
  }

  loginQR(accountId: string): Promise<void> {
    return this.startConnection(accountId, 'qr', (generation) => this.loginQRInternal(accountId, generation));
  }

  private async loginQRInternal(accountId: string, generation: number): Promise<void> {
    const account = await this.store.getAccount(accountId);
    if (!account) {
      throw new Error(`account_not_found:${accountId}`);
    }
    if (!this.isCurrentGeneration(accountId, generation)) return;

    const zalo = new Zalo({ logging: false, selfListen: true });
    const instance: ZaloInstance = { generation, zalo, api: null, status: 'qr_pending' };
    this.replaceInstance(accountId, instance);
    await this.store.setAccountRuntime(accountId, {
      status: 'qr_pending',
      qrImage: '',
      qrGeneratedAt: new Date().toISOString(),
      lastError: '',
    });

    let terminalQRReason = '';
    try {
      const api = await zalo.loginQR({}, async (event: any) => {
        if (this.instances.get(accountId) !== instance || this.shuttingDown) return;
        switch (event.type) {
          case 0:
            await this.store.setAccountRuntime(accountId, {
              status: 'qr_pending',
              qrImage: event.data?.image || '',
              qrGeneratedAt: new Date().toISOString(),
            });
            break;
          case 1:
          case 3:
            terminalQRReason = event.type === 1 ? 'qr_expired' : 'qr_declined';
            await this.store.setAccountRuntime(accountId, {
              status: 'qr_pending',
              qrImage: '',
              sessionData: undefined,
              lastError: terminalQRReason,
            });
            event.actions?.abort?.();
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
      await this.completeLogin(accountId, instance, api);
    } catch (error) {
      if (this.instances.get(accountId) !== instance || this.shuttingDown) return;
      if (terminalQRReason) return;
      instance.status = 'disconnected';
      await this.store.setAccountRuntime(accountId, {
        status: 'disconnected',
        lastError: String(error),
      });
      throw error;
    }
  }

  reconnect(accountId: string, credentials: SessionData): Promise<void> {
    return this.startConnection(accountId, 'reconnect', (generation) =>
      this.reconnectInternal(accountId, credentials, generation));
  }

  private async reconnectInternal(accountId: string, credentials: SessionData, generation: number): Promise<void> {
    const zalo = new Zalo({ logging: false, selfListen: true });
    const instance: ZaloInstance = { generation, zalo, api: null, status: 'connecting' };
    this.replaceInstance(accountId, instance);
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
      await this.completeLogin(accountId, instance, api);
    } catch (error) {
      if (this.instances.get(accountId) !== instance || this.shuttingDown) return;
      instance.status = 'qr_pending';
      await this.store.setAccountRuntime(accountId, {
        status: 'qr_pending',
        sessionData: undefined,
        lastError: String(error),
      });
      logger.warn(`[zalo:${accountId}] reconnect failed`, error);
    }
  }

  disconnect(accountId: string): void {
    const instance = this.instances.get(accountId);
    this.connectionGenerations.set(accountId, (this.connectionGenerations.get(accountId) ?? 0) + 1);
    this.connectionAttempts.delete(accountId);
    this.instances.delete(accountId);
    this.clearReconnectTimer(accountId);
    this.stopInstance(accountId, instance);
    this.chatbotGuards.delete(accountId);
  }

  private async completeLogin(accountId: string, instance: ZaloInstance, api: any): Promise<void> {
    if (this.instances.get(accountId) !== instance || this.shuttingDown) return;

    instance.api = api;
    instance.status = 'connected';

    const ownId = String(await api.getOwnId());
    if (this.instances.get(accountId) !== instance || this.shuttingDown) return;
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
      instance.chatbot = { threads: new Map(), tasks: new Set(), waiters: new Map(), guards };
    }
    const chatbot = instance.chatbot;
    attachZaloListener({
      accountId,
      api,
      store: this.store,
      isCurrent: () => !this.shuttingDown && this.instances.get(accountId) === instance && !instance.listenerClosed,
      onConnected: () => {
        instance.listenerClosed = false;
        instance.listenerOpened = true;
      },
      onDisconnected: (id, code) => {
        instance.listenerClosed = true;
        if (this.instances.get(id) !== instance) return Promise.resolve();
        return this.handleDisconnect(id, instance, code);
      },
      onLiveMessage: instance.chatbot
        ? (message) => this.trackOperation(this.handleLiveMessage(accountId, ownId, instance, message))
        : undefined,
      onSelfMessage: chatbot
        ? (threadId, threadType, message) => {
            if (this.shuttingDown || this.instances.get(accountId) !== instance) return Promise.resolve();
            return this.handleSelfMessage(accountId, chatbot, threadId, threadType, message);
          }
        : undefined,
    });
    if (chatbot) {
      await this.store.recoverChatbotTurns(accountId);
      await this.drainChatbotQueue(accountId, ownId, instance);
    }
  }

  private async handleSelfMessage(
    accountId: string,
    chatbot: ChatbotSession,
    threadId: string,
    threadType: IncomingGatewayMessage['threadType'],
    message: ChatbotSelfMessage,
  ): Promise<void> {
    const threadKey = chatbotThreadKey(threadType, threadId);
    if (threadType === 'group') {
      if (chatbot.guards.echoShield.isEcho(threadKey, message.text)) return;
      for (const active of chatbot.threads.values()) {
        if (active.threadType === 'group' && active.threadId === threadId) cancelChatbotTurn(active);
      }
      await this.store.cancelChatbotTurns(accountId, threadType, threadId);
      return;
    }

    const active = chatbot.threads.get(threadKey);
    if (chatbot.guards.directEchoIds.isEcho(threadKey, message.id)) return;

    // The socket can receive the echo before sendMessage returns its receipt.
    const pending = active?.pendingEcho;
    if (active && pending?.matches(message) && outboundMessageId.test(message.id) && pending.candidates.size < 8) {
      pending.candidates.add(message.id);
      return;
    }
    if (active) cancelChatbotTurn(active);
    await this.store.cancelChatbotTurns(accountId, threadType, threadId);
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
      await this.store.discardChatbotTurn(accountId, message);
      return;
    }
    const threadKey = chatbotThreadKey(message.threadType, message.threadId, message.senderUid);
    const existing = chatbot.threads.get(threadKey);
    if (chatbot.guards.floodDetector.isFlooding(message.senderUid)) {
      if (existing) cancelChatbotTurn(existing);
      await this.store.cancelChatbotTurns(accountId, message.threadType, message.threadId, message.senderUid);
      return;
    }
    if (chatbot.guards.echoShield.isEcho(chatbotThreadKey(message.threadType, message.threadId), message.content)) {
      await this.store.discardChatbotTurn(accountId, message);
      return;
    }
    const activeOfType = [...chatbot.threads.values()].filter((active) => active.threadType === message.threadType).length;
    const canStartImmediately = !existing && activeOfType < 3;
    const messageId = await this.store.activateChatbotTurn(accountId, message);
    if (!messageId) return;
    let waiter: { promise: Promise<void>; resolve: () => void } | undefined;
    if (canStartImmediately) {
      let resolve!: () => void;
      const promise = new Promise<void>((done) => { resolve = done; });
      waiter = { promise, resolve };
      chatbot.waiters.set(threadKey, waiter);
    }
    chatbot.rescanRequested = true;
    chatbot.wakeDrain?.();
    void this.trackOperation(this.drainChatbotQueue(accountId, ownId, instance));
    await waiter?.promise;
  }

  private async drainChatbotQueue(accountId: string, ownId: string, instance: ZaloInstance): Promise<void> {
    const chatbot = instance.chatbot;
    if (!chatbot) return;
    if (chatbot.drainPromise) return chatbot.drainPromise;

    const drain = async () => {
      while (this.canSendChatbotReply(accountId, instance)) {
        chatbot.rescanRequested = false;
        const turns = await this.store.listChatbotTurns(accountId);
        for (const [messageId, turn] of turns) {
          if (turn.status !== 'pending' && turn.status !== 'ready') continue;
          const message = turn.message;
          const threadKey = chatbotThreadKey(message.threadType, message.threadId, message.senderUid);
          if (chatbot.threads.has(threadKey)) continue;
          const activeOfType = [...chatbot.threads.values()].filter((active) => active.threadType === message.threadType).length;
          if (activeOfType >= 3) continue;
          const active: ActiveChatbotThread = { cancelled: false, threadId: message.threadId, threadType: message.threadType };
          chatbot.threads.set(threadKey, active);
          const task = this.processChatbotTurn(accountId, ownId, instance, active, messageId, turn)
            .catch((error) => logger.error(`[zalo:${accountId}] durable chatbot turn failed`, error))
            .finally(async () => {
              chatbot.threads.delete(threadKey);
              const remaining = await this.store.listChatbotTurns(accountId);
              const threadPending = remaining.some(([, queued]) =>
                (queued.status === 'pending' || queued.status === 'ready') &&
                chatbotThreadKey(queued.message.threadType, queued.message.threadId, queued.message.senderUid) === threadKey);
              if (!threadPending) {
                chatbot.waiters.get(threadKey)?.resolve();
                chatbot.waiters.delete(threadKey);
              }
              chatbot.tasks.delete(task);
            });
          chatbot.tasks.add(task);
        }
        if (chatbot.tasks.size === 0) return;
        if (chatbot.rescanRequested) continue;
        await Promise.race([
          ...chatbot.tasks,
          new Promise<void>((resolve) => { chatbot.wakeDrain = resolve; }),
        ]);
        chatbot.wakeDrain = undefined;
      }
    };
    chatbot.drainPromise = drain().finally(() => {
      chatbot.drainPromise = undefined;
      for (const waiter of chatbot.waiters.values()) waiter.resolve();
      chatbot.waiters.clear();
    });
    return chatbot.drainPromise;
  }

  private async processChatbotTurn(
    accountId: string,
    ownId: string,
    instance: ZaloInstance,
    active: ActiveChatbotThread,
    messageId: string,
    initial: GatewayChatbotTurnRecord,
  ): Promise<void> {
    const message = initial.message;
    if (!isChatbotEligibleMessage(message)) {
      await this.store.updateChatbotTurn(accountId, messageId, 'expired', { lastError: 'chatbot_turn_expired_before_dispatch' });
      return;
    }
    const account = await this.store.getAccount(accountId);
    if (!account || account.zaloUid !== ownId || account.accountExternalId !== ownId ||
        account.tenantId !== initial.tenantId || account.channelId !== initial.channelId ||
        account.accountExternalId !== initial.accountExternalId) {
      await this.store.completeChatbotTurn(accountId, messageId);
      return;
    }
    if (!this.chatbotAccountMatches(account, ownId) || !this.canSendChatbotReply(accountId, instance)) return;

    let reply = initial.reply ? restoreChatbotReply(initial.reply) : undefined;
    if (!reply) {
      if (!await this.store.transitionChatbotTurn(accountId, messageId, 'pending', 'processing')) return;
      const latestAccount = await this.store.getAccount(accountId);
      if (!latestAccount || latestAccount.tenantId !== initial.tenantId || latestAccount.channelId !== initial.channelId ||
          latestAccount.accountExternalId !== initial.accountExternalId) {
        await this.store.completeChatbotTurn(accountId, messageId);
        return;
      }
      if (!this.canSendChatbotReply(accountId, instance) || latestAccount.status !== 'connected') {
        await this.store.transitionChatbotTurn(accountId, messageId, 'processing', 'pending');
        return;
      }
      let result = await requestPersonalZaloChatbotTurn(latestAccount, message);
      for (let attempt = 1; result.outcome === 'retryable' && attempt < 3; attempt++) {
        await delay(attempt * 250);
        const retryAccount = await this.store.getAccount(accountId);
        if (active.cancelled || !retryAccount || retryAccount.tenantId !== initial.tenantId ||
            retryAccount.channelId !== initial.channelId || retryAccount.accountExternalId !== initial.accountExternalId) {
          await this.store.completeChatbotTurn(accountId, messageId);
          return;
        }
        if (!this.canSendChatbotReply(accountId, instance) || retryAccount.status !== 'connected') {
          await this.store.transitionChatbotTurn(accountId, messageId, 'processing', 'pending');
          return;
        }
        result = await requestPersonalZaloChatbotTurn(retryAccount, message);
      }
      if (result.outcome === 'no_reply') {
        await this.store.completeChatbotTurn(accountId, messageId);
        return;
      }
      if (result.outcome === 'unknown') {
        await this.store.updateChatbotTurn(accountId, messageId, 'unknown', { lastError: result.error });
        return;
      }
      if (result.outcome === 'retryable') {
        await this.store.updateChatbotTurn(accountId, messageId, 'failed', { lastError: result.error });
        return;
      }
      reply = result.reply;
      await this.store.updateChatbotTurn(accountId, messageId, 'ready', { reply: storeChatbotReply(reply) });
    }

    const outcome = await this.sendChatbotReply(
      instance.chatbot!, active, instance, account!, message, messageId, reply,
    );
    if (active.cancelled) {
      await this.store.cancelChatbotTurns(accountId, message.threadType, message.threadId, message.senderUid);
      return;
    }
    if (outcome === 'deferred') {
      await this.store.updateChatbotTurn(accountId, messageId, 'ready');
      return;
    }
    if (outcome === 'unknown' || outcome === 'failed') return;
    await this.store.completeChatbotTurn(accountId, messageId);
    if (outcome === 'sent' && reply.deliveryReceipt) {
      await acknowledgePersonalZaloChatbotDelivery(account!, message, reply.deliveryReceipt);
    }
  }

  private chatbotAccountMatches(account: GatewayAccountRecord | null, ownId: string): account is GatewayAccountRecord {
    return account?.status === 'connected' && account.zaloUid === ownId && account.accountExternalId === ownId;
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
    messageId: string,
    reply: PersonalZaloChatbotReply,
  ): Promise<ChatbotSendOutcome> {
    const accountId = expectedAccount.id;
    const { rateLimiter, echoShield, floodDetector } = chatbot.guards;
    const threadKey = chatbotThreadKey(message.threadType, message.threadId);
    const send = async (
      outbound: ChatbotOutboundMessage,
      expectedStatus: 'ready' | 'sending',
    ): Promise<ChatbotSendOutcome> => {
      let sent = false;
      let invoked = false;
      let cancelled = false;
      try {
        await rateLimiter.schedule(async (recordAttempt) => {
          if (active.cancelled) {
            cancelled = true;
            return;
          }
          if (!this.canSendChatbotReply(accountId, instance)) return;
          const account = await this.store.getAccount(accountId);
          if (active.cancelled || floodDetector.isMuted(message.senderUid)) {
            cancelled = true;
            return;
          }
          if (!this.canSendChatbotReply(accountId, instance) ||
              account?.status !== 'connected' || account.zaloUid !== expectedAccount.zaloUid ||
              account.tenantId !== expectedAccount.tenantId || account.channelId !== expectedAccount.channelId ||
              account.accountExternalId !== expectedAccount.accountExternalId || account.importSecret !== expectedAccount.importSecret) {
            if (account && (account.tenantId !== expectedAccount.tenantId || account.channelId !== expectedAccount.channelId ||
                account.accountExternalId !== expectedAccount.accountExternalId || account.importSecret !== expectedAccount.importSecret)) {
              cancelled = true;
            }
            return;
          }
          if (!await this.store.transitionChatbotTurn(accountId, messageId, expectedStatus, 'sending')) {
            cancelled = true;
            return;
          }
          const currentAccount = await this.store.getAccount(accountId);
          if (active.cancelled || currentAccount?.tenantId !== expectedAccount.tenantId || currentAccount.channelId !== expectedAccount.channelId ||
              currentAccount.accountExternalId !== expectedAccount.accountExternalId ||
              currentAccount.importSecret !== expectedAccount.importSecret) {
            cancelled = true;
            return;
          }
          if (!this.canSendChatbotReply(accountId, instance) || currentAccount.status !== 'connected') {
            if (expectedStatus === 'ready') {
              await this.store.transitionChatbotTurn(accountId, messageId, 'sending', 'ready');
            }
            return;
          }
          if (typeof outbound === 'string') echoShield.recordSent(threadKey, outbound);
          recordAttempt();
          invoked = true;
          if (message.threadType === 'group') {
            await instance.api.sendMessage(outbound, message.threadId, 1);
          } else {
            await this.sendDirectChatbotMessage(chatbot, active, instance, message.threadId, outbound);
          }
          sent = true;
        });
      } catch (error) {
        if (invoked) {
          await this.store.updateChatbotTurn(accountId, messageId, 'unknown', { lastError: String(error) });
          return 'unknown';
        }
        throw error;
      }
      return sent ? 'sent' : cancelled ? 'cancelled' : 'deferred';
    };

    try {
      const textOutcome = await send(reply.reply, 'ready');
      if (textOutcome !== 'sent') return textOutcome;
      const image = message.threadType === 'user' ? reply.images?.[0] : undefined;
      if (image && !active.cancelled) {
        const imageOutcome = await send({
          msg: '',
          attachments: [{
            data: image.data,
            filename: image.filename,
            metadata: { totalSize: image.data.length, width: image.width, height: image.height },
          }],
        }, 'sending');
        if (imageOutcome === 'unknown') return 'unknown';
        if (imageOutcome === 'deferred') {
          await this.store.updateChatbotTurn(accountId, messageId, 'failed', {
            lastError: 'text_sent_image_not_sent',
          });
          return 'failed';
        }
        if (imageOutcome === 'failed') return 'failed';
      }
      return 'sent';
    } catch (error) {
      logger.warn(`[zalo:${accountId}] chatbot send failed`, error);
      await this.store.updateChatbotTurn(accountId, messageId, 'failed', { lastError: String(error) });
      return 'failed';
    }
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

  async healthSnapshot(): Promise<PoolHealthSnapshot> {
    const accounts = await this.store.listAccounts();
    const statuses = accounts.map((account) => account.status);
    const turns = accounts.flatMap((account) => Object.values(account.chatbotTurns || {}));
    const instances = [...this.instances.values()];
    return {
      shuttingDown: this.shuttingDown,
      totalAccounts: accounts.length,
      connectedAccounts: instances.filter((instance) =>
        instance.status === 'connected' && instance.listenerOpened === true && !instance.listenerClosed).length,
      connectingAccounts: statuses.filter((status) => status === 'connecting').length,
      disconnectedAccounts: statuses.filter((status) => status === 'disconnected').length,
      qrPendingAccounts: statuses.filter((status) => status === 'qr_pending').length,
      scheduledReconnects: this.reconnectTimers.size,
      activeChatbotTurns: [...this.instances.values()].reduce(
        (total, instance) => total + (instance.chatbot?.threads.size ?? 0),
        0,
      ),
      queuedChatbotTurns: turns.filter((turn) =>
        ['captured', 'pending', 'processing', 'ready', 'sending'].includes(turn.status)).length,
      unknownChatbotTurns: turns.filter((turn) => turn.status === 'unknown').length,
      failedChatbotTurns: turns.filter((turn) => turn.status === 'failed' || turn.status === 'expired').length,
    };
  }

  async shutdown(timeoutMs: number): Promise<void> {
    this.shuttingDown = true;
    this.connectionAttempts.clear();
    for (const timer of this.reconnectTimers.values()) clearTimeout(timer);
    this.reconnectTimers.clear();
    const instances = [...this.instances.entries()];
    this.instances.clear();
    for (const [accountId, instance] of instances) {
      // Removing the instance defers unsent durable turns for recovery.
      // Manual cancellation would discard those turns during a normal deploy.
      this.stopInstance(accountId, instance);
    }

    const pending = Promise.allSettled([...this.pendingOperations]);
    if (timeoutMs <= 0) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    await Promise.race([
      pending,
      new Promise<void>((resolve) => { timer = setTimeout(resolve, timeoutMs); }),
    ]);
    if (timer) clearTimeout(timer);
  }

  private async handleDisconnect(accountId: string, instance: ZaloInstance, code: number): Promise<void> {
    if (this.shuttingDown || this.instances.get(accountId) !== instance) return;
    instance.status = 'disconnected';
    await this.store.setAccountRuntime(accountId, {
      status: 'disconnected',
    });
    if (this.shuttingDown || this.instances.get(accountId) !== instance) return;

    if (!instance.listenerOpened || !retryableCloseCodes.has(code)) {
      instance.status = 'qr_pending';
      await this.store.setAccountRuntime(accountId, {
        status: 'qr_pending',
        sessionData: undefined,
        lastError: `listener_closed_${code}_qr_required`,
      });
      if (this.shuttingDown || this.instances.get(accountId) !== instance) return;
      this.clearReconnectTimer(accountId);
      return;
    }

    const now = Date.now();
    const history = (this.disconnectHistory.get(accountId) || []).filter((value) => now - value < 5 * 60_000);
    history.push(now);
    this.disconnectHistory.set(accountId, history);

    if (history.length >= 5) {
      await this.store.setAccountRuntime(accountId, {
        status: 'qr_pending',
        sessionData: undefined,
        lastError: 'session_unstable_qr_required',
      });
      if (this.shuttingDown || this.instances.get(accountId) !== instance) return;
      instance.status = 'qr_pending';
      this.clearReconnectTimer(accountId);
      this.disconnectHistory.delete(accountId);
      return;
    }

    this.clearReconnectTimer(accountId);
    const timer = setTimeout(() => {
      this.reconnectTimers.delete(accountId);
      if (this.shuttingDown || this.instances.get(accountId) !== instance || instance.status !== 'disconnected') return;
      const reconnect = async () => {
        try {
          const account = await this.store.getAccount(accountId);
          if (!account?.sessionData?.imei || this.instances.get(accountId) !== instance) return;
          await this.reconnect(accountId, account.sessionData);
        } catch (error) {
          logger.warn(`[zalo:${accountId}] scheduled reconnect failed`, error);
        }
      };
      void reconnect();
    }, 30_000);
    this.reconnectTimers.set(accountId, timer);
  }

  private replaceInstance(accountId: string, next: ZaloInstance): void {
    const previous = this.instances.get(accountId);
    this.clearReconnectTimer(accountId);
    this.instances.set(accountId, next);
    this.stopInstance(accountId, previous);
  }

  private stopInstance(accountId: string, instance?: ZaloInstance): void {
    if (!instance?.api?.listener) return;
    try {
      instance.api.listener.stop();
    } catch (error) {
      logger.warn(`[zalo:${accountId}] listener stop failed`, error);
    }
  }

  private clearReconnectTimer(accountId: string): void {
    const timer = this.reconnectTimers.get(accountId);
    if (timer) clearTimeout(timer);
    this.reconnectTimers.delete(accountId);
  }

  private startConnection(
    accountId: string,
    kind: 'qr' | 'reconnect',
    connect: (generation: number) => Promise<void>,
  ): Promise<void> {
    if (this.shuttingDown) return Promise.resolve();
    const existing = this.connectionAttempts.get(accountId);
    if (existing?.kind === kind) return existing.promise;

    const generation = (this.connectionGenerations.get(accountId) ?? 0) + 1;
    this.connectionGenerations.set(accountId, generation);
    this.clearReconnectTimer(accountId);
    const promise = this.trackOperation(connect(generation));
    this.connectionAttempts.set(accountId, { kind, promise });
    void promise.then(
      () => {
        if (this.connectionAttempts.get(accountId)?.promise === promise) this.connectionAttempts.delete(accountId);
      },
      () => {
        if (this.connectionAttempts.get(accountId)?.promise === promise) this.connectionAttempts.delete(accountId);
      },
    );
    return promise;
  }

  private isCurrentGeneration(accountId: string, generation: number): boolean {
    return !this.shuttingDown && this.connectionGenerations.get(accountId) === generation;
  }

  private trackOperation<T>(operation: Promise<T>): Promise<T> {
    this.pendingOperations.add(operation);
    const cleanup = () => { this.pendingOperations.delete(operation); };
    void operation.then(cleanup, cleanup);
    return operation;
  }
}

function chatbotThreadKey(threadType: IncomingGatewayMessage['threadType'], threadId: string, senderUid = ''): string {
  if (threadType === 'group' && senderUid) return `group:${threadId}:${senderUid}`;
  return `${threadType}:${threadId}`;
}

function storeChatbotReply(reply: PersonalZaloChatbotReply): GatewayChatbotReply {
  const image = reply.images?.[0];
  return {
    reply: reply.reply,
    deliveryReceipt: reply.deliveryReceipt,
    images: image ? [{
      filename: image.filename,
      mediaType: image.filename.toLowerCase().endsWith('.png') ? 'image/png' : 'image/jpeg',
      width: image.width,
      height: image.height,
      dataBase64: image.data.toString('base64'),
    }] : undefined,
  };
}

function restoreChatbotReply(reply: GatewayChatbotReply): PersonalZaloChatbotReply {
  const image = reply.images?.[0];
  return {
    reply: reply.reply,
    deliveryReceipt: reply.deliveryReceipt,
    images: image ? [{
      filename: image.filename as `${string}.${string}`,
      width: image.width,
      height: image.height,
      data: Buffer.from(image.dataBase64, 'base64'),
    }] : undefined,
  };
}

import { createRequire } from 'node:module';
import { createHash } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { personalZaloChatbotConfig } from '../config.js';
import { logger } from '../logger.js';
import type { GatewayStore } from '../store/file-store.js';
import type {
  GatewayAccountRecord,
  GatewayChatbotReply,
  GatewayChatbotTurnRecord,
  GatewayInternbotReply,
  IncomingGatewayMessage,
  SessionData,
} from '../types.js';
import {
  acknowledgePersonalZaloChatbotDelivery,
  authorizePersonalZaloChatbotDelivery,
  authorizePersonalZaloGroupAccess,
  authorizePersonalZaloInternbotDelivery,
  isChatbotEligibleMessage,
  requestPersonalZaloChatbotTurn,
  type ChatbotReplyImage,
  type PersonalZaloChatbotReply,
  type PersonalZaloInternbotReply,
} from '../chatbot/cqa-chatbot-client.js';
import { FloodDetector, RateLimiter, SelfEchoShield } from '../chatbot/zalo-message-guards.js';
import { attachZaloListener, type ChatbotSelfMessage } from './listener.js';
import { captureRoster, hasGroupMembership, probeRoster, pushRosterSnapshot, type RosterSnapshot } from './roster.js';

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
  rosterGroupNames?: Map<string, string>;
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
  manuallyAnswered?: boolean;
  mode?: 'chatbot' | 'internbot';
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
  // Session-close notices sent outside a turn, matched by text in 1:1 chats.
  noticeEchoes: SelfEchoShield;
  // When staff last typed in a chat by hand, by chatbotThreadKey(type, id).
  manualRepliesAt: Map<string, number>;
}

export type SessionNoticeOutcome = 'sent' | 'skipped_busy' | 'skipped_manual' | 'unavailable';

const manualReplyMemoryMs = 2 * 24 * 60 * 60_000;

function noteManualActivity(guards: AccountChatbotGuards, threadKey: string, now = Date.now()): void {
  if (guards.manualRepliesAt.size > 5_000) {
    for (const [key, at] of guards.manualRepliesAt) {
      if (now - at > manualReplyMemoryMs) guards.manualRepliesAt.delete(key);
    }
  }
  guards.manualRepliesAt.set(threadKey, now);
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

function noteManualReply(active: ActiveChatbotThread): void {
  active.manuallyAnswered = true;
  if (active.mode === 'chatbot') cancelChatbotTurn(active);
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
  private readonly rosterTimers = new Map<string, ReturnType<typeof setTimeout>>();
  private readonly rosterIntervals = new Map<string, ReturnType<typeof setInterval>>();
  private readonly rosterRunning = new Set<string>();
  private readonly rosterRequested = new Set<string>();
  private shuttingDown = false;

  constructor(private readonly store: GatewayStore) {}

  async captureRoster(accountId: string): Promise<RosterSnapshot> {
    const instance = this.instances.get(accountId);
    const account = await this.store.getAccount(accountId);
    const ownId = account?.zaloUid || '';
    if (!instance || !instance.api || !this.canSendChatbotReply(accountId, instance) ||
        !this.chatbotAccountMatches(account, ownId)) {
      throw new Error('account_not_connected');
    }
    const snapshot = await captureRoster(instance.api, account, instance.rosterGroupNames ??= new Map());
    const current = await this.store.getAccount(accountId);
    if (!this.canSendChatbotReply(accountId, instance) || !this.chatbotAccountMatches(current, ownId) ||
        current.tenantId !== account.tenantId || current.channelId !== account.channelId ||
        current.importSecret !== account.importSecret) {
      throw new Error('account_connection_changed');
    }
    return snapshot;
  }

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
      if (String(error).includes('account_identity_mismatch')) return;
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
      if (String(error).includes('account_identity_mismatch')) return;
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
    this.clearRosterRefresh(accountId);
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
    const boundAccount = await this.store.getAccount(accountId);
    const boundZaloId = boundAccount?.accountExternalId || boundAccount?.zaloUid || '';
    if (!boundAccount || (boundZaloId && boundZaloId !== ownId)) {
      instance.status = 'qr_pending';
      try {
        api.listener?.stop?.();
      } catch (error) {
        logger.warn(`[zalo:${accountId}] mismatched listener stop failed`, error);
      }
      if (boundAccount) {
        await this.store.setAccountRuntime(accountId, {
          status: 'qr_pending',
          qrImage: '',
          sessionData: undefined,
          lastError: 'account_identity_mismatch',
        });
      }
      throw new Error('account_identity_mismatch');
    }
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
        noticeEchoes: new SelfEchoShield(),
        manualRepliesAt: new Map(),
      };
      this.chatbotGuards.set(accountId, guards);
      instance.chatbot = { threads: new Map(), tasks: new Set(), waiters: new Map(), guards };
    }
    const chatbot = instance.chatbot;
    if (chatbot) await this.store.recoverChatbotTurns(accountId);
    attachZaloListener({
      accountId,
      api,
      store: this.store,
      isCurrent: () => !this.shuttingDown && this.instances.get(accountId) === instance && !instance.listenerClosed,
      onConnected: () => {
        instance.listenerClosed = false;
        instance.listenerOpened = true;
        if (chatbot) this.requestRosterRefresh(accountId, instance, true);
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
      onRosterEvent: chatbot ? () => this.requestRosterRefresh(accountId, instance) : undefined,
    });
    if (chatbot) {
      const interval = setInterval(() => this.requestRosterRefresh(accountId, instance), 5 * 60_000);
      interval.unref();
      this.rosterIntervals.set(accountId, interval);
      void this.trackOperation(this.drainChatbotQueue(accountId, ownId, instance))
        .catch((error) => logger.error(`[zalo:${accountId}] chatbot recovery drain failed`, error));
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
      noteManualActivity(chatbot.guards, threadKey);
      for (const active of chatbot.threads.values()) {
        if (active.threadType === 'group' && active.threadId === threadId) noteManualReply(active);
      }
      await this.store.noteManualChatbotReply(accountId, threadType, threadId);
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
    // A session-close notice can echo before sendMessage returns its id.
    if (!active && message.contentType === 'text' && chatbot.guards.noticeEchoes.isEcho(threadKey, message.text)) return;
    noteManualActivity(chatbot.guards, threadKey);
    if (active) noteManualReply(active);
    await this.store.noteManualChatbotReply(accountId, threadType, threadId);
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
          if (!isQueuedChatbotTurn(turn)) continue;
          const message = turn.message;
          const threadKey = chatbotThreadKey(message.threadType, message.threadId, message.senderUid);
          if (chatbot.threads.has(threadKey)) continue;
          const activeOfType = [...chatbot.threads.values()].filter((active) => active.threadType === message.threadType).length;
          if (activeOfType >= 3) continue;
          const active: ActiveChatbotThread = {
            cancelled: false, threadId: message.threadId, threadType: message.threadType,
            manuallyAnswered: turn.manuallyAnswered,
            mode: turn.status === 'ready' ? 'chatbot' : turn.status === 'internbot_ready' ? 'internbot' : undefined,
          };
          chatbot.threads.set(threadKey, active);
          const task = this.processChatbotTurn(accountId, ownId, instance, active, messageId, turn)
            .catch((error) => logger.error(`[zalo:${accountId}] durable chatbot turn failed`, error))
            .finally(async () => {
              chatbot.threads.delete(threadKey);
              const remaining = await this.store.listChatbotTurns(accountId);
              const threadPending = remaining.some(([, queued]) =>
                isQueuedChatbotTurn(queued) &&
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
    if (initial.status === 'pending' && !isChatbotEligibleMessage(message)) {
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

    let reply: PersonalZaloChatbotReply | PersonalZaloInternbotReply | undefined;
    if (initial.status === 'ready' && initial.reply) reply = restoreChatbotReply(initial.reply);
    if (initial.status === 'internbot_ready' && initial.internbotReply) reply = restoreInternbotReply(initial.internbotReply);
    if (initial.status !== 'pending' && !reply) {
      await this.store.updateChatbotTurn(accountId, messageId, 'failed', { lastError: 'invalid_stored_chatbot_reply' });
      return;
    }
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
      const requestTurn = (current: GatewayAccountRecord) => requestPersonalZaloChatbotTurn(current, message,
        Boolean(active.manuallyAnswered || current.chatbotTurns?.[messageId]?.manuallyAnswered));
      let result = await requestTurn(latestAccount);
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
        result = await requestTurn(retryAccount);
      }
      if (active.cancelled || (result.outcome !== 'internbot_reply' && active.manuallyAnswered)) {
        await this.store.completeChatbotTurn(accountId, messageId);
        return;
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
      if (result.outcome === 'internbot_reply') {
        active.mode = 'internbot';
        await this.store.updateChatbotTurn(accountId, messageId, 'internbot_ready', {
          internbotReply: storeInternbotReply(result.reply),
        });
      } else {
        active.mode = 'chatbot';
        await this.store.updateChatbotTurn(accountId, messageId, 'ready', { reply: storeChatbotReply(result.reply) });
      }
    }

    const isInternbot = 'groupId' in reply;
    const outcome = await this.sendChatbotReply(
      instance.chatbot!, active, instance, account!, message, messageId, reply,
    );
    if (active.cancelled) {
      if (active.manuallyAnswered) await this.store.noteManualChatbotReply(accountId, message.threadType, message.threadId);
      return;
    }
    if (outcome === 'deferred') {
      await this.store.updateChatbotTurn(accountId, messageId, isInternbot ? 'internbot_ready' : 'ready');
      return;
    }
    if (outcome === 'unknown' || outcome === 'failed') return;
    await this.store.completeChatbotTurn(accountId, messageId);
    if (outcome === 'sent' && 'deliveryReceipt' in reply && reply.deliveryReceipt) {
      await acknowledgePersonalZaloChatbotDelivery(account!, message, reply.deliveryReceipt);
    }
  }

  /**
   * Posts the backend's session-close notice in a chat, outside any turn. It
   * is skipped while a turn for the chat is running or queued (the person is
   * talking again) and when staff typed in the chat by hand after idleSince.
   * Uses the same rate limiter and echo guards as chatbot replies.
   */
  async sendSessionNotice(accountId: string, input: {
    threadType: IncomingGatewayMessage['threadType'];
    threadId: string;
    text: string;
    idleSince?: number;
  }): Promise<SessionNoticeOutcome> {
    const instance = this.instances.get(accountId);
    const chatbot = instance?.chatbot;
    if (!instance?.api || !chatbot || this.shuttingDown || !this.canSendChatbotReply(accountId, instance)) return 'unavailable';
    const ownId = chatbot.guards.ownId;
    if (!this.chatbotAccountMatches(await this.store.getAccount(accountId), ownId) || input.threadId === ownId) return 'unavailable';
    const threadKey = chatbotThreadKey(input.threadType, input.threadId);
    const busy = async () => {
      if ([...chatbot.threads.values()].some((active) =>
        active.threadType === input.threadType && active.threadId === input.threadId)) return true;
      return (await this.store.listChatbotTurns(accountId)).some(([, turn]) =>
        turn.message.threadType === input.threadType && turn.message.threadId === input.threadId &&
        (isQueuedChatbotTurn(turn) || ['captured', 'processing', 'sending'].includes(turn.status)));
    };
    const manual = () => input.idleSince !== undefined &&
      (chatbot.guards.manualRepliesAt.get(threadKey) ?? 0) >= input.idleSince;
    if (await busy()) return 'skipped_busy';
    if (manual()) return 'skipped_manual';

    let outcome: SessionNoticeOutcome = 'unavailable';
    await chatbot.guards.rateLimiter.schedule(async (recordAttempt) => {
      // The limiter may have waited: check again right before sending.
      if (!this.canSendChatbotReply(accountId, instance)) return;
      if (await busy()) {
        outcome = 'skipped_busy';
        return;
      }
      if (manual()) {
        outcome = 'skipped_manual';
        return;
      }
      chatbot.guards.echoShield.recordSent(threadKey, input.text);
      chatbot.guards.noticeEchoes.recordSent(threadKey, input.text);
      recordAttempt();
      const result: ZaloSendReceipt | undefined =
        await instance.api.sendMessage(input.text, input.threadId, input.threadType === 'group' ? 1 : 0);
      const id = String(result?.message?.msgId ?? '');
      if (input.threadType === 'user' && outboundMessageId.test(id)) chatbot.guards.directEchoIds.recordSent(threadKey, id);
      outcome = 'sent';
    });
    return outcome;
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
    reply: PersonalZaloChatbotReply | PersonalZaloInternbotReply,
  ): Promise<ChatbotSendOutcome> {
    const accountId = expectedAccount.id;
    const { rateLimiter, echoShield, floodDetector } = chatbot.guards;
    const internbot = 'groupId' in reply ? reply : undefined;
    const targetType = internbot ? 'group' : message.threadType;
    const targetID = internbot ? internbot.groupId : message.threadId;
    const readyStatus = internbot ? 'internbot_ready' : 'ready';
    const threadKey = chatbotThreadKey(targetType, targetID);
    const retainReply = () => 'groupId' in reply ? { internbotReply: storeInternbotReply(reply, false) } :
      { reply: storeChatbotReply(reply, false) };
    const textToken = 'groupId' in reply ? reply.token : reply.deliveryToken;
    const send = async (
      outbound: ChatbotOutboundMessage,
      expectedStatus: 'ready' | 'internbot_ready' | 'sending',
      deliveryToken = textToken,
    ): Promise<ChatbotSendOutcome> => {
      let sent = false;
      let invoked = false;
      let cancelled = false;
      let failed = false;
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
          if (internbot) {
            try {
              if (!await hasGroupMembership(instance.api, targetID)) {
                cancelled = true;
                return;
              }
            } catch {
              await this.store.updateChatbotTurn(accountId, messageId, 'failed', {
                lastError: 'internbot_group_membership_unavailable', ...retainReply(),
              });
              failed = true;
              return;
            }
          }
          if (internbot || message.threadType === 'user') {
            if (!deliveryToken) {
              cancelled = true;
              return;
            }
            const part = typeof outbound === 'string' ? 'text' : 'image';
            const digest = createHash('sha256')
              .update(typeof outbound === 'string' ? Buffer.from(outbound, 'utf8') : outbound.attachments[0].data).digest('hex');
            const authorization = internbot ?
              await authorizePersonalZaloInternbotDelivery(account, message, targetID, deliveryToken, part, digest) :
              await authorizePersonalZaloChatbotDelivery(account, message, deliveryToken, part, digest);
            if (authorization === 'denied') {
              cancelled = true;
              return;
            }
            if (authorization === 'retryable') {
              await this.store.updateChatbotTurn(accountId, messageId, 'failed', {
                lastError: `delivery_authorization_unavailable_${part}`, ...retainReply(),
              });
              failed = true;
              return;
            }
            const authorizedAccount = await this.store.getAccount(accountId);
            if (active.cancelled) {
              cancelled = true;
              return;
            }
            if (!this.canSendChatbotReply(accountId, instance) || authorizedAccount?.status !== 'connected') return;
            if (authorizedAccount.zaloUid !== expectedAccount.zaloUid ||
                authorizedAccount.tenantId !== expectedAccount.tenantId || authorizedAccount.channelId !== expectedAccount.channelId ||
                authorizedAccount.accountExternalId !== expectedAccount.accountExternalId ||
                authorizedAccount.importSecret !== expectedAccount.importSecret) {
              cancelled = true;
              return;
            }
          }
          if (!internbot && message.threadType === 'group') {
            const authorization = await authorizePersonalZaloGroupAccess(
              expectedAccount, message, new Date(message.timestamp).toISOString(),
            );
            if (authorization === 'denied') {
              cancelled = true;
              return;
            }
            if (authorization === 'retryable') {
              await this.store.updateChatbotTurn(accountId, messageId, 'failed', {
                lastError: 'group_approval_unavailable', ...retainReply(),
              });
              failed = true;
              return;
            }
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
            if (expectedStatus !== 'sending') {
              await this.store.transitionChatbotTurn(accountId, messageId, 'sending', expectedStatus);
            }
            return;
          }
          if (typeof outbound === 'string') echoShield.recordSent(threadKey, outbound);
          recordAttempt();
          invoked = true;
          if (targetType === 'group') {
            await instance.api.sendMessage(outbound, targetID, 1);
          } else {
            await this.sendDirectChatbotMessage(chatbot, active, instance, message.threadId, outbound);
          }
          sent = true;
        });
      } catch (error) {
        if (invoked) {
          await this.store.updateChatbotTurn(accountId, messageId, 'unknown', {
            lastError: String(error), ...retainReply(),
          });
          return 'unknown';
        }
        throw error;
      }
      return sent ? 'sent' : cancelled ? 'cancelled' : failed ? 'failed' : 'deferred';
    };

    try {
      const textOutcome = await send('groupId' in reply ? reply.text : reply.reply, readyStatus);
      if (textOutcome !== 'sent') return textOutcome;
      const images = internbot || message.threadType === 'user' ? reply.images ?? [] : [];
      for (const image of images) {
        if (active.cancelled) return 'cancelled';
        const imageOutcome = await send({
          msg: '',
          attachments: [{
            data: image.data,
            filename: image.filename,
            metadata: { totalSize: image.data.length, width: image.width, height: image.height },
          }],
        }, 'sending', image.deliveryToken ?? textToken);
        if (imageOutcome === 'unknown') return 'unknown';
        if (imageOutcome === 'deferred') {
          await this.store.updateChatbotTurn(accountId, messageId, 'failed', {
            lastError: 'text_sent_image_not_sent',
            ...retainReply(),
          });
          return 'failed';
        }
        if (imageOutcome === 'failed') return 'failed';
      }
      return 'sent';
    } catch (error) {
      logger.warn(`[zalo:${accountId}] chatbot send failed`, error);
      await this.store.updateChatbotTurn(accountId, messageId, 'failed', {
        lastError: String(error), ...retainReply(),
      });
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
      if (pending.candidates.size > 0) noteManualReply(active);
      active.pendingEcho = undefined;
    }
  }

  private requestRosterRefresh(accountId: string, instance: ZaloInstance, immediate = false): void {
    if (this.shuttingDown || !this.canSendChatbotReply(accountId, instance)) return;
    this.rosterRequested.add(accountId);
    if (this.rosterTimers.has(accountId) || this.rosterRunning.has(accountId)) return;
    const timer = setTimeout(() => {
      this.rosterTimers.delete(accountId);
      void this.refreshRoster(accountId, instance);
    }, immediate ? 0 : 500);
    timer.unref();
    this.rosterTimers.set(accountId, timer);
  }

  private async refreshRoster(accountId: string, instance: ZaloInstance): Promise<void> {
    const deployment = personalZaloChatbotConfig();
    if (!deployment || !this.canSendChatbotReply(accountId, instance)) return;
    this.rosterRunning.add(accountId);
    this.rosterRequested.delete(accountId);
    try {
      const account = await this.store.getAccount(accountId);
      if (!this.chatbotAccountMatches(account, account?.zaloUid || '') || !await probeRoster(account, deployment) ||
          !this.canSendChatbotReply(accountId, instance)) return;
      const snapshot = await this.captureRoster(accountId);
      const current = await this.store.getAccount(accountId);
      if (!this.canSendChatbotReply(accountId, instance) || !this.chatbotAccountMatches(current, snapshot.account_external_id) ||
          current.tenantId !== account.tenantId || current.channelId !== account.channelId ||
          current.importSecret !== account.importSecret) return;
      await pushRosterSnapshot(current, snapshot, deployment);
    } catch (error) {
      logger.warn(`[zalo:${accountId}] roster refresh failed`, error);
    } finally {
      this.rosterRunning.delete(accountId);
      const current = this.instances.get(accountId);
      if (current && this.rosterRequested.has(accountId)) this.requestRosterRefresh(accountId, current);
    }
  }

  private clearRosterRefresh(accountId: string): void {
    clearInterval(this.rosterIntervals.get(accountId));
    clearTimeout(this.rosterTimers.get(accountId));
    this.rosterIntervals.delete(accountId);
    this.rosterTimers.delete(accountId);
    this.rosterRequested.delete(accountId);
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
        isQueuedChatbotTurn(turn) || ['captured', 'processing', 'sending'].includes(turn.status)).length,
      unknownChatbotTurns: turns.filter((turn) => turn.status === 'unknown').length,
      failedChatbotTurns: turns.filter((turn) => turn.status === 'failed' || turn.status === 'expired').length,
    };
  }

  async shutdown(timeoutMs: number): Promise<void> {
    this.shuttingDown = true;
    for (const timer of this.rosterIntervals.values()) clearInterval(timer);
    this.rosterIntervals.clear();
    for (const timer of this.rosterTimers.values()) clearTimeout(timer);
    this.rosterTimers.clear();
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
    this.clearRosterRefresh(accountId);
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
    this.clearRosterRefresh(accountId);
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

function isQueuedChatbotTurn(turn: GatewayChatbotTurnRecord): boolean {
  return turn.status === 'pending' || turn.status === 'ready' || turn.status === 'internbot_ready';
}

function storeInternbotReply(reply: PersonalZaloInternbotReply, includeImages = true): GatewayInternbotReply {
  return {
    groupId: reply.groupId,
    content: storeChatbotReply({ reply: reply.text, deliveryToken: reply.token, images: reply.images }, includeImages),
  };
}

function restoreInternbotReply(reply: GatewayInternbotReply): PersonalZaloInternbotReply | undefined {
  const { reply: text, deliveryToken: token, images } = restoreChatbotReply(reply.content);
  if (!token) return undefined;
  return {
    groupId: reply.groupId, text, token,
    images: images?.flatMap((image) => image.deliveryToken ? [{ ...image, deliveryToken: image.deliveryToken }] : []),
  };
}

function storeChatbotReply(reply: PersonalZaloChatbotReply, includeImages = true): GatewayChatbotReply {
  return {
    reply: reply.reply,
    deliveryToken: reply.deliveryToken,
    deliveryReceipt: reply.deliveryReceipt,
    images: includeImages ? reply.images?.map((image) => ({
      filename: image.filename,
      mediaType: image.filename.toLowerCase().endsWith('.png') ? 'image/png' : 'image/jpeg',
      width: image.width,
      height: image.height,
      dataBase64: image.data.toString('base64'),
      deliveryToken: image.deliveryToken,
    })) : undefined,
  };
}

function restoreChatbotReply(reply: GatewayChatbotReply): PersonalZaloChatbotReply {
  return {
    reply: reply.reply,
    deliveryToken: reply.deliveryToken,
    deliveryReceipt: reply.deliveryReceipt,
    images: reply.images?.map((image) => ({
      filename: image.filename as `${string}.${string}`,
      width: image.width,
      height: image.height,
      data: Buffer.from(image.dataBase64, 'base64'),
      deliveryToken: image.deliveryToken,
    })),
  };
}

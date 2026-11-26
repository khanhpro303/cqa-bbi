import { createRequire } from 'node:module';
import { setTimeout as delay } from 'node:timers/promises';
import { personalZaloChatbotConfig } from '../config.js';
import { logger } from '../logger.js';
import type { GatewayStore } from '../store/file-store.js';
import type { GatewayAccountRecord, IncomingGatewayMessage, SessionData } from '../types.js';
import { isChatbotEligibleMessage, requestPersonalZaloChatbotReply } from '../chatbot/cqa-chatbot-client.js';
import { attachZaloListener } from './listener.js';

const require = createRequire(import.meta.url);
// eslint-disable-next-line @typescript-eslint/no-require-imports
const { Zalo } = require('zca-js') as { Zalo: new (opts: { logging: boolean; selfListen?: boolean }) => any };

interface ZaloInstance {
  zalo: any;
  api: any;
  status: 'connected' | 'disconnected' | 'qr_pending' | 'connecting';
  listenerClosed?: boolean;
  chatbot?: ChatbotSession;
}

interface ActiveChatbotThread {
  cancelled: boolean;
  latest?: IncomingGatewayMessage;
  expectedEcho?: { text: string; expiresAt: number };
}

interface ChatbotSession {
  threads: Map<string, ActiveChatbotThread>;
  sendChain: Promise<void>;
  nextSendAt: number;
}

export class ZaloAccountPool {
  private readonly instances = new Map<string, ZaloInstance>();
  private readonly disconnectHistory = new Map<string, number[]>();

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
      instance.chatbot = { threads: new Map(), sendChain: Promise.resolve(), nextSendAt: 0 };
    }
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
      onSelfMessage: instance.chatbot ? (threadId, text) => {
        const active = instance.chatbot?.threads.get(threadId);
        if (!active) return;
        if (active.expectedEcho?.text === text && active.expectedEcho.expiresAt >= Date.now()) {
          active.expectedEcho = undefined;
          return;
        }
        active.cancelled = true;
        active.latest = undefined;
      } : undefined,
    });
  }

  private async handleLiveMessage(
    accountId: string,
    ownId: string,
    instance: ZaloInstance,
    message: IncomingGatewayMessage,
  ): Promise<void> {
    const chatbot = instance.chatbot;
    if (!chatbot || !this.canSendChatbotReply(accountId, instance) ||
        message.threadId === ownId || !isChatbotEligibleMessage(message)) {
      return;
    }
    const existing = chatbot.threads.get(message.threadId);
    if (existing) {
      if (!existing.latest || message.timestamp >= existing.latest.timestamp) existing.latest = message;
      return;
    }
    if (chatbot.threads.size >= 3) return;

    const active: ActiveChatbotThread = { cancelled: false };
    chatbot.threads.set(message.threadId, active);
    try {
      let current: IncomingGatewayMessage | undefined = message;
      while (current && this.canSendChatbotReply(accountId, instance)) {
        active.latest = undefined;
        active.cancelled = false;
        const account = await this.store.getAccount(accountId);
        if (account?.status === 'connected' && account.zaloUid === ownId && account.accountExternalId === ownId &&
            !active.cancelled && this.canSendChatbotReply(accountId, instance)) {
          const reply = await requestPersonalZaloChatbotReply(account, current);
          if (reply && !active.cancelled) {
            await this.sendChatbotReply(chatbot, active, instance, account, current.threadId, reply);
          }
        }
        current = active.latest;
      }
    } finally {
      chatbot.threads.delete(message.threadId);
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
    threadId: string,
    reply: string,
  ): Promise<void> {
    const accountId = expectedAccount.id;
    const send = chatbot.sendChain.then(async () => {
      if (active.cancelled || !this.canSendChatbotReply(accountId, instance)) return;
      const waitMs = chatbot.nextSendAt - Date.now();
      if (waitMs > 0) await delay(waitMs);
      const account = await this.store.getAccount(accountId);
      if (active.cancelled || !this.canSendChatbotReply(accountId, instance) ||
          account?.status !== 'connected' || account.zaloUid !== expectedAccount.zaloUid ||
          account.tenantId !== expectedAccount.tenantId || account.channelId !== expectedAccount.channelId ||
          account.accountExternalId !== expectedAccount.accountExternalId || account.importSecret !== expectedAccount.importSecret) {
        return;
      }
      active.expectedEcho = { text: reply, expiresAt: Date.now() + 30_000 };
      try {
        await instance.api.sendMessage(reply, threadId, 0);
      } finally {
        chatbot.nextSendAt = Date.now() + 3_000;
      }
    }).catch((error) => {
      active.expectedEcho = undefined;
      logger.warn(`[zalo:${accountId}] chatbot send failed`, error);
    });
    chatbot.sendChain = send;
    await send;
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

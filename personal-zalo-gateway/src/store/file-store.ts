import fs from 'node:fs/promises';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { logger } from '../logger.js';
import type {
  AccountSyncBatch,
  GatewayAccountRecord,
  GatewayChatbotReply,
  GatewayChatbotTurnRecord,
  GatewayChatbotTurnStatus,
  GatewayConversationRecord,
  GatewayStoreShape,
  IncomingGatewayMessage,
  SyncBatchConversation,
  UpsertAccountInput,
} from '../types.js';

function nowISO(): string {
  return new Date().toISOString();
}

function buildThreadKey(threadType: string, externalId: string): string {
  return `${threadType}:${externalId}`;
}

export class GatewayStore {
  private filePath: string;
  private writeChain: Promise<void> = Promise.resolve();

  constructor(filePath: string) {
    this.filePath = filePath;
  }

  async init(): Promise<void> {
    await fs.mkdir(path.dirname(this.filePath), { recursive: true });
    try {
      await fs.access(this.filePath);
    } catch {
      await this.writeState({ accounts: {} });
    }
  }

  async listAccounts(): Promise<GatewayAccountRecord[]> {
    const state = await this.readState();
    return Object.values(state.accounts).sort((a, b) => a.createdAt.localeCompare(b.createdAt));
  }

  async getAccount(accountId: string): Promise<GatewayAccountRecord | null> {
    const state = await this.readState();
    return state.accounts[accountId] || null;
  }

  async saveAccount(input: UpsertAccountInput, accountId?: string): Promise<GatewayAccountRecord> {
    return this.updateState((state) => {
      const id = accountId || randomUUID();
      const existing = state.accounts[id];
      const now = nowISO();
      if (existing && (input.tenantId !== existing.tenantId || input.channelId !== existing.channelId ||
          (input.accountExternalId && existing.accountExternalId && input.accountExternalId !== existing.accountExternalId))) {
        throw new Error('account_binding_immutable');
      }

      const next: GatewayAccountRecord = {
        id,
        tenantId: input.tenantId,
        channelId: input.channelId,
        importEndpoint: input.importEndpoint,
        importSecret: input.importSecret,
        accountExternalId: input.accountExternalId || existing?.accountExternalId || '',
        status: existing?.status || 'disconnected',
        displayName: input.displayName || existing?.displayName || '',
        avatarUrl: existing?.avatarUrl,
        zaloUid: existing?.zaloUid,
        qrImage: existing?.qrImage,
        qrGeneratedAt: existing?.qrGeneratedAt,
        sessionData: existing?.sessionData,
        lastError: existing?.lastError,
        lastImportedAt: existing?.lastImportedAt,
        // Saving re-points the account at a channel CQA just validated.
        syncBlockedReason: undefined,
        syncBlockedAt: undefined,
        createdAt: existing?.createdAt || now,
        updatedAt: now,
        threads: existing?.threads || {},
        chatbotTurns: existing?.chatbotTurns || {},
      };

      state.accounts[id] = next;
      return next;
    });
  }

  async deleteAccount(accountId: string): Promise<boolean> {
    return this.updateState((state) => {
      if (!state.accounts[accountId]) return false;
      delete state.accounts[accountId];
      return true;
    });
  }

  async setAccountRuntime(
    accountId: string,
    changes: Partial<GatewayAccountRecord>,
  ): Promise<GatewayAccountRecord | null> {
    return this.updateState((state) => {
      const account = state.accounts[accountId];
      if (!account) return null;
      if ((changes.tenantId !== undefined && changes.tenantId !== account.tenantId) ||
          (changes.channelId !== undefined && changes.channelId !== account.channelId) ||
          (changes.accountExternalId !== undefined && account.accountExternalId && changes.accountExternalId !== account.accountExternalId) ||
          (changes.zaloUid !== undefined && account.zaloUid && changes.zaloUid !== account.zaloUid) ||
          (changes.accountExternalId !== undefined && account.zaloUid && changes.accountExternalId !== account.zaloUid) ||
          (changes.zaloUid !== undefined && account.accountExternalId && changes.zaloUid !== account.accountExternalId)) {
        throw new Error('account_binding_immutable');
      }
      const next: GatewayAccountRecord = {
        ...account,
        ...changes,
        updatedAt: nowISO(),
      };
      state.accounts[accountId] = next;
      return next;
    });
  }

  async recordIncomingMessage(
    accountId: string,
    message: IncomingGatewayMessage,
    queueChatbotTurn = false,
    historyOnly = false,
  ): Promise<boolean> {
    return this.updateState((state) => {
      const account = state.accounts[accountId];
      if (!account) {
        throw new Error(`account_not_found:${accountId}`);
      }

      const threadKey = buildThreadKey(message.threadType, message.threadId);
      const sentAt = new Date(message.timestamp || Date.now()).toISOString();
      const messageID = message.msgId || `${message.threadId}-${message.timestamp}-${Math.random()}`;
      const existingThread = account.threads[threadKey];

      const conversation: GatewayConversationRecord = existingThread || {
        externalId: message.threadId,
        threadType: message.threadType,
        externalUserId: message.threadType === 'user' ? message.threadId : '',
        customerName: message.threadType === 'group'
          ? message.groupName || 'Nhóm'
          : message.isSelf
            ? message.threadId
            : message.senderName || message.threadId,
        lastMessageAt: sentAt,
        metadata: {},
        messages: {},
        messageOrder: [],
        pending: true,
      };

      conversation.customerName = message.threadType === 'group'
        ? message.groupName || conversation.customerName || 'Nhóm'
        : (!message.isSelf && message.senderName ? message.senderName : conversation.customerName || message.threadId);
      conversation.lastMessageAt = sentAt > conversation.lastMessageAt ? sentAt : conversation.lastMessageAt;
      conversation.metadata = {
        ...conversation.metadata,
        thread_key: threadKey,
      };

      const inserted = !conversation.messages[messageID];
      if (inserted) {
        conversation.messages[messageID] = {
          externalId: messageID,
          historyOnly: historyOnly || undefined,
          senderType: message.isSelf ? 'agent' : 'customer',
          senderName: message.senderName || '',
          content: message.content || '',
          contentType: message.contentType || 'text',
          attachments: message.attachments || [],
          sentAt,
          rawData: {
            ...(message.rawData || {}),
            sender_uid: message.senderUid,
            thread_id: message.threadId,
          },
        };
        conversation.messageOrder.push(messageID);
        conversation.pending = true;
      }
      const record = conversation.messages[messageID]!;
      // History can arrive first on reconnect. Only the first live copy may
      // start a turn. An absent marker on older records keeps them deduplicated.
      const firstLive = !historyOnly && (inserted || record.historyOnly === true);
      if (firstLive) {
        delete record.historyOnly;
        if (queueChatbotTurn && !message.isSelf && !record.deletedAt) {
          account.chatbotTurns ??= {};
          account.chatbotTurns[chatbotTurnKey(message)] = {
            message,
            tenantId: account.tenantId,
            channelId: account.channelId,
            accountExternalId: account.accountExternalId,
            status: 'captured',
            createdAt: nowISO(),
            updatedAt: nowISO(),
          };
        }
      }

      account.threads[threadKey] = conversation;
      account.updatedAt = nowISO();
      account.lastError = '';
      return historyOnly ? inserted : firstLive;
    });
  }

  async recoverChatbotTurns(accountId: string): Promise<void> {
    await this.updateState((state) => {
      const account = state.accounts[accountId];
      if (!account) return;
      for (const turn of Object.values(account.chatbotTurns || {})) {
        if (turn.status === 'captured') {
          turn.status = 'expired';
          turn.lastError = 'gateway_restarted_before_chatbot_guards_completed';
        }
        if (turn.status === 'processing') {
          turn.status = 'unknown';
          turn.lastError = 'gateway_restarted_while_cqa_request_was_in_flight';
        }
        if (turn.status === 'sending') {
          turn.status = 'unknown';
          turn.lastError = 'gateway_restarted_while_zalo_send_was_in_flight';
        }
        turn.updatedAt = nowISO();
      }
    });
  }

  async activateChatbotTurn(accountId: string, message: IncomingGatewayMessage): Promise<string | null> {
    return this.updateState((state) => {
      const key = chatbotTurnKey(message);
      const turn = state.accounts[accountId]?.chatbotTurns?.[key];
      if (!turn || turn.status !== 'captured') return null;
      turn.status = 'pending';
      turn.updatedAt = nowISO();
      return key;
    });
  }

  async discardChatbotTurn(accountId: string, message: IncomingGatewayMessage): Promise<void> {
    await this.updateState((state) => {
      delete state.accounts[accountId]?.chatbotTurns?.[chatbotTurnKey(message)];
    });
  }

  async listChatbotTurns(accountId: string): Promise<Array<[string, GatewayChatbotTurnRecord]>> {
    const account = await this.getAccount(accountId);
    return Object.entries(account?.chatbotTurns || {})
      .sort((a, b) => a[1].message.timestamp - b[1].message.timestamp);
  }

  async updateChatbotTurn(
    accountId: string,
    messageId: string,
    status: GatewayChatbotTurnStatus,
    changes: { reply?: GatewayChatbotReply; lastError?: string } = {},
  ): Promise<boolean> {
    return this.updateState((state) => {
      const turn = state.accounts[accountId]?.chatbotTurns?.[messageId];
      if (!turn) return false;
      turn.status = status;
      turn.updatedAt = nowISO();
      if (changes.reply !== undefined) turn.reply = changes.reply;
      if (changes.lastError !== undefined) turn.lastError = changes.lastError;
      return true;
    });
  }

  async transitionChatbotTurn(
    accountId: string,
    messageId: string,
    expected: GatewayChatbotTurnStatus,
    status: GatewayChatbotTurnStatus,
  ): Promise<boolean> {
    return this.updateState((state) => {
      const turn = state.accounts[accountId]?.chatbotTurns?.[messageId];
      if (!turn || turn.status !== expected) return false;
      turn.status = status;
      turn.updatedAt = nowISO();
      return true;
    });
  }

  async completeChatbotTurn(accountId: string, messageId: string): Promise<void> {
    await this.updateState((state) => {
      delete state.accounts[accountId]?.chatbotTurns?.[messageId];
    });
  }

  async cancelChatbotTurns(
    accountId: string,
    threadType: IncomingGatewayMessage['threadType'],
    threadId: string,
    senderUid?: string,
  ): Promise<void> {
    await this.updateState((state) => {
      const turns = state.accounts[accountId]?.chatbotTurns;
      if (!turns) return;
      for (const [messageId, turn] of Object.entries(turns)) {
        if (turn.message.threadType === threadType && turn.message.threadId === threadId &&
            (!senderUid || turn.message.senderUid === senderUid) &&
            turn.status !== 'unknown' && turn.status !== 'failed' && turn.status !== 'expired') {
          delete turns[messageId];
        }
      }
    });
  }

  async acknowledgeChatbotIssue(accountId: string, turnKey: string): Promise<boolean> {
    return this.updateState((state) => {
      const turns = state.accounts[accountId]?.chatbotTurns;
      if (!turns || !Object.hasOwn(turns, turnKey)) return false;
      const status = turns[turnKey].status;
      if (status !== 'unknown' && status !== 'failed' && status !== 'expired') return false;
      delete turns[turnKey];
      return true;
    });
  }

  async markMessageDeleted(accountId: string, zaloMsgId: string): Promise<void> {
    await this.updateState((state) => {
      const account = state.accounts[accountId];
      if (!account) return undefined;

      for (const conversation of Object.values(account.threads)) {
        const message = conversation.messages[zaloMsgId];
        if (!message) continue;
        message.deletedAt = nowISO();
        message.rawData = {
          ...message.rawData,
          deleted: true,
        };
        if (!message.syncedAt) {
          conversation.pending = true;
        }
      }

      account.updatedAt = nowISO();
      return undefined;
    });
  }

  async nextSyncBatch(
    accountId: string,
    maxConversations: number,
    maxMessagesPerConversation: number,
  ): Promise<AccountSyncBatch | null> {
    const account = await this.getAccount(accountId);
    if (!account) return null;
    if (account.zaloUid && account.accountExternalId && account.zaloUid !== account.accountExternalId) {
      throw new Error('account_identity_mismatch');
    }

    const conversationEntries = Object.entries(account.threads)
      .filter(([, conv]) => conv.pending)
      .sort((a, b) => a[1].lastMessageAt.localeCompare(b[1].lastMessageAt));

    const conversations: SyncBatchConversation[] = [];
    for (const [threadKey, conv] of conversationEntries) {
      if (conversations.length >= maxConversations) break;
      const unsynced = conv.messageOrder
        .map((messageID) => conv.messages[messageID])
        .filter((msg) => !msg.syncedAt)
        .slice(0, maxMessagesPerConversation);

      if (unsynced.length === 0) {
        continue;
      }

      conversations.push({
        threadKey,
        conversation: {
          external_id: conv.externalId,
          thread_type: conv.threadType,
          external_user_id: conv.externalUserId,
          customer_name: conv.customerName,
          last_message_at: conv.lastMessageAt,
          metadata: conv.metadata,
        },
        messages: unsynced.map((msg) => ({
          external_id: msg.externalId,
          sender_type: msg.senderType,
          sender_name: msg.senderName,
          content: msg.content,
          content_type: msg.contentType,
          attachments: msg.attachments,
          sent_at: msg.sentAt,
          raw_data: msg.rawData,
        })),
      });
    }

    if (conversations.length === 0) {
      return null;
    }

    return { account, conversations };
  }

  async markBatchSynced(
    accountId: string,
    syncedMessages: Record<string, string[]>,
    syncedAt: string,
  ): Promise<void> {
    await this.updateState((state) => {
      const account = state.accounts[accountId];
      if (!account) return undefined;

      for (const [threadKey, messageIDs] of Object.entries(syncedMessages)) {
        const conv = account.threads[threadKey];
        if (!conv) continue;
        for (const messageID of messageIDs) {
          const msg = conv.messages[messageID];
          if (!msg) continue;
          msg.syncedAt = syncedAt;
        }
        const stillPending = conv.messageOrder.some((messageID) => !conv.messages[messageID]?.syncedAt);
        conv.pending = stillPending;
        conv.lastSyncedAt = syncedAt;
      }

      account.lastImportedAt = syncedAt;
      account.lastError = '';
      delete account.syncBlockedReason;
      delete account.syncBlockedAt;
      account.updatedAt = syncedAt;
      return undefined;
    });
  }

  private async readState(): Promise<GatewayStoreShape> {
    const raw = await fs.readFile(this.filePath, 'utf8');
    const parsed = JSON.parse(raw) as GatewayStoreShape;
    return {
      accounts: parsed.accounts || {},
    };
  }

  private async writeState(state: GatewayStoreShape): Promise<void> {
    const tmp = `${this.filePath}.tmp`;
    await fs.writeFile(tmp, JSON.stringify(state, null, 2));
    await fs.rename(tmp, this.filePath);
  }

  private async updateState<T>(mutate: (state: GatewayStoreShape) => T): Promise<T> {
    let result!: T;
    const nextWrite = this.writeChain.then(async () => {
      const state = await this.readState();
      result = mutate(state);
      await this.writeState(state);
    }).catch((error) => {
      logger.error('store write failed', error);
      throw error;
    });
    this.writeChain = nextWrite.then(() => undefined, () => undefined);
    await nextWrite;
    return result;
  }
}

function chatbotTurnKey(message: IncomingGatewayMessage): string {
  return [message.threadType, message.threadId, message.senderUid, message.msgId].join(':');
}

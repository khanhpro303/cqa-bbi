import { setTimeout as delay } from 'node:timers/promises';
import { logger } from '../logger.js';
import { CQAImportError, sendImportBatch } from './cqa-client.js';
import type { GatewayStore } from '../store/file-store.js';

export interface SyncHealthSnapshot {
  running: boolean;
  shuttingDown: boolean;
  inFlight: number;
  failingAccounts: number;
  attempts: number;
  failures: number;
  lastAttemptAt?: string;
  lastSuccessAt?: string;
  lastFailureAt?: string;
}

export class SyncService {
  private timer: NodeJS.Timeout | null = null;
  private inFlight = new Set<string>();
  private failingAccounts = new Set<string>();
  private running = false;
  private shuttingDown = false;
  private attempts = 0;
  private failures = 0;
  private lastAttemptAt?: string;
  private lastSuccessAt?: string;
  private lastFailureAt?: string;

  constructor(
    private readonly store: GatewayStore,
    private readonly intervalMs: number,
    private readonly maxConversations: number,
    private readonly maxMessagesPerConversation: number,
    private readonly timeoutMs: number,
  ) {}

  start(): void {
    if (this.timer) return;
    this.running = true;
    this.shuttingDown = false;
    void this.syncAll(); // Run immediately on startup
    this.timer = setInterval(() => {
      void this.syncAll();
    }, this.intervalMs);
  }

  stop(): void {
    this.running = false;
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }
  }

  healthSnapshot(): SyncHealthSnapshot {
    return {
      running: this.running,
      shuttingDown: this.shuttingDown,
      inFlight: this.inFlight.size,
      failingAccounts: this.failingAccounts.size,
      attempts: this.attempts,
      failures: this.failures,
      lastAttemptAt: this.lastAttemptAt,
      lastSuccessAt: this.lastSuccessAt,
      lastFailureAt: this.lastFailureAt,
    };
  }

  async shutdown(timeoutMs: number): Promise<boolean> {
    this.stop();
    this.shuttingDown = true;
    const deadline = Date.now() + timeoutMs;
    while (this.inFlight.size > 0 && Date.now() < deadline) {
      await delay(Math.min(25, Math.max(1, deadline - Date.now())));
    }
    return this.inFlight.size === 0;
  }

  async syncAll(): Promise<void> {
    if (this.shuttingDown) return;
    const accounts = await this.store.listAccounts();
    const accountIds = new Set(accounts.map((account) => account.id));
    for (const accountId of this.failingAccounts) {
      if (!accountIds.has(accountId)) this.failingAccounts.delete(accountId);
    }
    for (const account of accounts) {
      if (!account.accountExternalId && !account.zaloUid) {
        continue;
      }
      // CQA said this account's channel is gone; resending the same batch
      // every interval only floods both logs with 401s.
      if (account.syncBlockedReason) {
        continue;
      }
      try {
        await this.syncAccount(account.id);
      } catch (error) {
        logger.error(`[sync] failed to sync account ${account.id}`, error);
      }
    }
  }

  async syncAccount(accountId: string): Promise<void> {
    if (this.shuttingDown || this.inFlight.has(accountId)) return;
    this.inFlight.add(accountId);
    this.attempts++;
    this.lastAttemptAt = new Date().toISOString();

    try {
      while (true) {
        const batch = await this.store.nextSyncBatch(
          accountId,
          this.maxConversations,
          this.maxMessagesPerConversation,
        );
        if (!batch) {
          this.failingAccounts.delete(accountId);
          this.lastSuccessAt = new Date().toISOString();
          return;
        }
        if (this.shuttingDown) return;

        let response;
        try {
          response = await sendImportBatch(batch, this.timeoutMs);
        } catch (error) {
          if (error instanceof CQAImportError && error.targetGone) {
            const reason = `CQA rejected imports for channel ${batch.account.channelId} (${error.code}); the channel was probably deleted. Reconnect from CQA or delete this gateway account.`;
            await this.store.setAccountRuntime(accountId, {
              syncBlockedReason: reason,
              syncBlockedAt: new Date().toISOString(),
              lastError: reason,
            });
            logger.warn(`[sync] account=${accountId} paused: ${reason}`);
          }
          throw error;
        }
        const syncedAt = new Date().toISOString();
        await this.store.markBatchSynced(
          accountId,
          Object.fromEntries(
            batch.conversations.map((conv) => [
              conv.threadKey,
              conv.messages.map((message) => message.external_id),
            ]),
          ),
          syncedAt,
        );
        logger.info(
          `[sync] account=${accountId} request=${response.request_id} conversations=${response.conversations} inserted=${response.messages_inserted} deduped=${response.messages_deduplicated}`,
        );
        if (this.shuttingDown) {
          this.failingAccounts.delete(accountId);
          this.lastSuccessAt = new Date().toISOString();
          return;
        }
      }
    } catch (error) {
      this.failures++;
      this.failingAccounts.add(accountId);
      this.lastFailureAt = new Date().toISOString();
      throw error;
    } finally {
      this.inFlight.delete(accountId);
    }
  }
}

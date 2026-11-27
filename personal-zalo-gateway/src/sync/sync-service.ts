import { setTimeout as delay } from 'node:timers/promises';
import { logger } from '../logger.js';
import { CQAImportError, fetchImportSettings, sendImportBatch } from './cqa-client.js';
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
  private polling = false;
  private inFlight = new Set<string>();
  private failingAccounts = new Set<string>();
  private lastAccountAttempt = new Map<string, number>();
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
    }, Math.max(1, Math.min(60_000, this.intervalMs)));
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
    while ((this.polling || this.inFlight.size > 0) && Date.now() < deadline) {
      await delay(Math.min(25, Math.max(1, deadline - Date.now())));
    }
    return !this.polling && this.inFlight.size === 0;
  }

  async syncAll(): Promise<void> {
    if (this.shuttingDown || this.polling) return;
    this.polling = true;
    try {
      const accounts = await this.store.listAccounts();
      const accountIds = new Set(accounts.map((account) => account.id));
      for (const accountId of this.failingAccounts) {
        if (!accountIds.has(accountId)) this.failingAccounts.delete(accountId);
      }
      for (const accountId of this.lastAccountAttempt.keys()) {
        if (!accountIds.has(accountId)) this.lastAccountAttempt.delete(accountId);
      }
      for (const account of accounts) {
        if (this.shuttingDown) return;
        if ((!account.accountExternalId && !account.zaloUid) || account.syncBlockedReason || this.inFlight.has(account.id) ||
            (account.accountExternalId && account.zaloUid && account.accountExternalId !== account.zaloUid)) {
          continue;
        }

        let settings;
        try {
          settings = await fetchImportSettings(account, Math.min(5_000, this.timeoutMs));
        } catch (error) {
          this.recordFailure(account.id);
          logger.error(`[sync] could not read settings for account ${account.id}`, error);
          continue;
        }
        if (this.shuttingDown) return;
        if (settings?.active === false) {
          this.failingAccounts.delete(account.id);
          continue;
        }

        const minutes = settings?.sync_interval_minutes;
        const interval = minutes === undefined ? this.intervalMs : minutes * 60_000;
        const previousAttempt = this.lastAccountAttempt.get(account.id);
        if (previousAttempt !== undefined && Date.now() - previousAttempt < interval) continue;

        try {
          await this.syncAccount(account.id);
        } catch (error) {
          logger.error(`[sync] failed to sync account ${account.id}`, error);
        }
      }
    } catch (error) {
      logger.error('[sync] could not schedule account imports', error);
    } finally {
      this.polling = false;
    }
  }

  async syncAccount(accountId: string): Promise<void> {
    if (this.shuttingDown || this.inFlight.has(accountId)) return;
    this.inFlight.add(accountId);
    this.lastAccountAttempt.set(accountId, Date.now());
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
          await this.blockMissingChannel(accountId, batch.account.channelId, error);
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
      this.recordFailure(accountId);
      throw error;
    } finally {
      this.inFlight.delete(accountId);
    }
  }

  private recordFailure(accountId: string): void {
    this.failures++;
    this.failingAccounts.add(accountId);
    this.lastFailureAt = new Date().toISOString();
  }

  private async blockMissingChannel(accountId: string, channelId: string, error: unknown): Promise<void> {
    if (!(error instanceof CQAImportError) || !error.targetGone) return;
    const reason = `CQA rejected imports for channel ${channelId} (${error.code}); the channel was probably deleted. Reconnect from CQA or delete this gateway account.`;
    await this.store.setAccountRuntime(accountId, {
      syncBlockedReason: reason,
      syncBlockedAt: new Date().toISOString(),
      lastError: reason,
    });
    this.lastAccountAttempt.delete(accountId);
    logger.warn(`[sync] account=${accountId} paused: ${reason}`);
  }
}

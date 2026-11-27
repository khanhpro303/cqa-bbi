import type { FastifyInstance } from 'fastify';
import type { GatewayStore } from './store/file-store.js';
import type { SyncHealthSnapshot, SyncService } from './sync/sync-service.js';
import type { PoolHealthSnapshot, ZaloAccountPool } from './zalo/account-pool.js';

export interface GatewayRuntimeState {
  started: boolean;
  shuttingDown: boolean;
}

interface AccountHealthSnapshot {
  syncBlockedAccounts: number;
  pendingConversations: number;
  pendingMessages: number;
}

async function accountHealthSnapshot(store: GatewayStore): Promise<AccountHealthSnapshot> {
  const accounts = await store.listAccounts();
  let syncBlockedAccounts = 0;
  let pendingConversations = 0;
  let pendingMessages = 0;
  for (const account of accounts) {
    if (account.syncBlockedReason) syncBlockedAccounts++;
    for (const conversation of Object.values(account.threads)) {
      if (!conversation.pending) continue;
      const unsyncedMessages = conversation.messageOrder.filter((id) => !conversation.messages[id]?.syncedAt).length;
      if (unsyncedMessages === 0) continue;
      pendingConversations++;
      pendingMessages += unsyncedMessages;
    }
  }
  return { syncBlockedAccounts, pendingConversations, pendingMessages };
}

export async function operationalHealth(
  store: GatewayStore,
  pool: Pick<ZaloAccountPool, 'healthSnapshot'>,
  syncService: Pick<SyncService, 'healthSnapshot'>,
  runtime: GatewayRuntimeState,
): Promise<{
  ready: boolean;
  accounts: AccountHealthSnapshot;
  pool: PoolHealthSnapshot;
  sync: SyncHealthSnapshot;
}> {
  const [accounts, poolHealth] = await Promise.all([
    accountHealthSnapshot(store),
    pool.healthSnapshot(),
  ]);
  const sync = syncService.healthSnapshot();
  const ready = runtime.started && !runtime.shuttingDown && !poolHealth.shuttingDown && sync.running &&
    poolHealth.connectedAccounts === poolHealth.totalAccounts &&
    !sync.shuttingDown && accounts.syncBlockedAccounts === 0 && poolHealth.disconnectedAccounts === 0 &&
    poolHealth.connectingAccounts === 0 && poolHealth.qrPendingAccounts === 0 && sync.failingAccounts === 0 &&
    poolHealth.unknownChatbotTurns === 0 && poolHealth.failedChatbotTurns === 0;
  return { ready, accounts, pool: poolHealth, sync };
}

export function registerHealthRoutes(
  app: FastifyInstance,
  store: GatewayStore,
  pool: Pick<ZaloAccountPool, 'healthSnapshot'>,
  syncService: Pick<SyncService, 'healthSnapshot'>,
  runtime: GatewayRuntimeState,
): void {
  app.get('/health', async () => ({
    status: 'ok',
    service: 'personal-zalo-gateway',
    timestamp: new Date().toISOString(),
  }));

  app.get('/ready', async (_request, reply) => {
    try {
      const operational = await operationalHealth(store, pool, syncService, runtime);
      return reply.status(operational.ready ? 200 : 503).send({
        status: operational.ready ? 'ok' : 'degraded',
        service: 'personal-zalo-gateway',
        timestamp: new Date().toISOString(),
        ...operational,
      });
    } catch {
      return reply.status(503).send({
        status: 'unavailable',
        service: 'personal-zalo-gateway',
        timestamp: new Date().toISOString(),
        ready: false,
      });
    }
  });
}

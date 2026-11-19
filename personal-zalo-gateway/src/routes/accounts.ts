import { randomUUID } from 'node:crypto';
import type { FastifyInstance } from 'fastify';
import type { GatewayStore } from '../store/file-store.js';
import type { SyncService } from '../sync/sync-service.js';
import type { ZaloAccountPool } from '../zalo/account-pool.js';

interface AccountRouteDeps {
  store: GatewayStore;
  syncService: SyncService;
  pool: ZaloAccountPool;
}

export async function registerAccountRoutes(app: FastifyInstance, deps: AccountRouteDeps): Promise<void> {
  app.get('/api/v1/accounts', async () => {
    return {
      accounts: await deps.store.listAccounts(),
    };
  });

  app.get('/api/v1/accounts/:accountId', async (request, reply) => {
    const { accountId } = request.params as { accountId: string };
    const account = await deps.store.getAccount(accountId);
    if (!account) {
      return reply.status(404).send({ error: 'account_not_found' });
    }
    return account;
  });

  app.post('/api/v1/accounts', async (request, reply) => {
    const body = request.body as {
      tenantId?: string;
      channelId?: string;
      importEndpoint?: string;
      importSecret?: string;
      accountExternalId?: string;
      displayName?: string;
    };

    if (!body?.tenantId || !body?.channelId || !body?.importEndpoint || !body?.importSecret) {
      return reply.status(400).send({
        error: 'tenantId, channelId, importEndpoint, importSecret are required',
      });
    }

    const account = await deps.store.saveAccount(
      {
        tenantId: body.tenantId,
        channelId: body.channelId,
        importEndpoint: body.importEndpoint,
        importSecret: body.importSecret,
        accountExternalId: body.accountExternalId,
        displayName: body.displayName,
      },
      randomUUID(),
    );

    return reply.status(201).send(account);
  });

  app.put('/api/v1/accounts/:accountId', async (request, reply) => {
    const { accountId } = request.params as { accountId: string };
    const current = await deps.store.getAccount(accountId);
    if (!current) {
      return reply.status(404).send({ error: 'account_not_found' });
    }

    const body = request.body as {
      tenantId?: string;
      channelId?: string;
      importEndpoint?: string;
      importSecret?: string;
      accountExternalId?: string;
      displayName?: string;
    };

    const account = await deps.store.saveAccount(
      {
        tenantId: body.tenantId || current.tenantId,
        channelId: body.channelId || current.channelId,
        importEndpoint: body.importEndpoint || current.importEndpoint,
        importSecret: body.importSecret || current.importSecret,
        accountExternalId: body.accountExternalId || current.accountExternalId,
        displayName: body.displayName || current.displayName,
      },
      accountId,
    );

    return account;
  });

  // Called by CQA when it deletes the channel an account imports into, so the
  // account stops listening to Zalo and stops posting to a channel that no
  // longer exists.
  app.delete('/api/v1/accounts/:accountId', async (request, reply) => {
    const { accountId } = request.params as { accountId: string };
    deps.pool.disconnect(accountId);
    const deleted = await deps.store.deleteAccount(accountId);
    if (!deleted) {
      return reply.status(404).send({ error: 'account_not_found' });
    }
    return { status: 'deleted' };
  });

  app.post('/api/v1/accounts/:accountId/login/qr', async (request, reply) => {
    const { accountId } = request.params as { accountId: string };
    const account = await deps.store.getAccount(accountId);
    if (!account) {
      return reply.status(404).send({ error: 'account_not_found' });
    }
    void deps.pool.loginQR(accountId).catch(async (error) => {
      await deps.store.setAccountRuntime(accountId, {
        status: 'disconnected',
        lastError: String(error),
      });
    });
    return reply.status(202).send({ status: 'qr_pending' });
  });

  app.post('/api/v1/accounts/:accountId/reconnect', async (request, reply) => {
    const { accountId } = request.params as { accountId: string };
    const account = await deps.store.getAccount(accountId);
    if (!account?.sessionData?.imei) {
      return reply.status(400).send({ error: 'session_not_found' });
    }
    void deps.pool.reconnect(accountId, account.sessionData);
    return { status: 'connecting' };
  });

  app.post('/api/v1/accounts/:accountId/sync', async (request, reply) => {
    const { accountId } = request.params as { accountId: string };
    try {
      await deps.syncService.syncAccount(accountId);
      return reply.status(202).send({ status: 'queued' });
    } catch (error) {
      return reply.status(500).send({ error: String(error) });
    }
  });
}

import Fastify from 'fastify';
import cors from '@fastify/cors';
import { config, stateFilePath } from './config.js';
import { registerHealthRoutes, type GatewayRuntimeState } from './health.js';
import { logger } from './logger.js';
import { registerAccountRoutes } from './routes/accounts.js';
import { GatewayStore } from './store/file-store.js';
import { SyncService } from './sync/sync-service.js';
import { ZaloAccountPool } from './zalo/account-pool.js';

async function bootstrap(): Promise<void> {
  const app = Fastify({ logger: false });
  const store = new GatewayStore(stateFilePath());
  await store.init();

  const syncService = new SyncService(
    store,
    config.syncIntervalMs,
    config.maxBatchConversations,
    config.maxMessagesPerConversation,
    config.requestTimeoutMs,
  );
  const pool = new ZaloAccountPool(store);
  const runtime: GatewayRuntimeState = { started: false, shuttingDown: false };
  let shutdownPromise: Promise<void> | null = null;
  const shutdown = () => {
    if (shutdownPromise) return shutdownPromise;
    runtime.shuttingDown = true;
    shutdownPromise = (async () => {
      const [syncDrained] = await Promise.all([
        syncService.shutdown(config.shutdownTimeoutMs),
        pool.shutdown(config.shutdownTimeoutMs),
      ]);
      if (!syncDrained) logger.warn('shutdown timed out while imports were still active');
      await app.close();
      process.exit(0);
    })();
    return shutdownPromise;
  };
  const requestShutdown = () => {
    void shutdown().catch((error) => {
      logger.error('gateway shutdown failed', error);
      process.exit(1);
    });
  };
  process.on('SIGINT', () => {
    requestShutdown();
  });
  process.on('SIGTERM', () => {
    requestShutdown();
  });

  await app.register(cors, { origin: true });
  await registerAccountRoutes(app, { store, syncService, pool });
  registerHealthRoutes(app, store, pool, syncService, runtime);

  app.setErrorHandler((error, _request, reply) => {
    logger.error('request failed', error);
    reply.status(500).send({
      error: error instanceof Error ? error.message : 'internal_error',
    });
  });

  await app.listen({ host: config.host, port: config.port });
  logger.info(`personal-zalo-gateway listening on http://${config.host}:${config.port}`);

  if (runtime.shuttingDown) return;
  await pool.bootstrapReconnects();
  if (runtime.shuttingDown) return;
  syncService.start();
  runtime.started = true;
}

void bootstrap();

import assert from 'node:assert/strict';
import test from 'node:test';
import Fastify from 'fastify';

delete process.env.PERSONAL_ZALO_GATEWAY_ADMIN_SECRET;
const { registerAccountRoutes } = await import('../src/routes/accounts.js');

test('management routes fail closed when the admin secret is not configured', async () => {
  const app = Fastify({ logger: false });
  await registerAccountRoutes(app, {
    store: {} as never,
    syncService: {} as never,
    pool: {} as never,
  });
  app.get('/health', async () => ({ status: 'ok' }));
  try {
    const response = await app.inject({ method: 'GET', url: '/api/v1/accounts' });
    assert.equal(response.statusCode, 503);
    assert.deepEqual(response.json(), { error: 'gateway_admin_auth_unavailable' });
    const health = await app.inject({ method: 'GET', url: '/health' });
    assert.equal(health.statusCode, 200);
  } finally {
    await app.close();
  }
});

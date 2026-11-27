import type { FastifyInstance } from 'fastify';
import type { GatewayStore } from '../store/file-store.js';

// Register in the authenticated management scope. Never return message or reply bodies.
export function registerDeliveryAttentionRoutes(app: FastifyInstance, store: GatewayStore): void {
  const path = '/api/v1/accounts/:accountId/delivery-issues';
  app.get<{ Params: { accountId: string } }>(path, async ({ params }) => {
    const turns = await store.listChatbotTurns(params.accountId);
    return {
      issues: turns.filter(([, turn]) => turn.status === 'unknown' || turn.status === 'failed' || turn.status === 'expired')
        .map(([key, turn]) => ({
          key, status: turn.status, threadId: turn.message.threadId, threadType: turn.message.threadType,
          messageId: turn.message.msgId, updatedAt: turn.updatedAt,
        })),
    };
  });
  app.post<{ Params: { accountId: string; key: string } }>(
    path + '/:key/acknowledge',
    async ({ params }, reply) => {
      if (await store.acknowledgeChatbotIssue(params.accountId, params.key)) return reply.code(204).send();
      return reply.code(404).send({ error: 'delivery_issue_not_found' });
    },
  );
}

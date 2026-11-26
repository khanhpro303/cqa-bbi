import path from 'node:path';

function readInt(name: string, fallback: number): number {
  const raw = process.env[name];
  if (!raw) return fallback;
  const value = Number.parseInt(raw, 10);
  return Number.isFinite(value) ? value : fallback;
}

export const config = {
  host: process.env.PERSONAL_ZALO_GATEWAY_HOST || process.env.HOST || '127.0.0.1',
  port: readInt('PERSONAL_ZALO_GATEWAY_PORT', readInt('PORT', 3100)),
  dataDir: process.env.PERSONAL_ZALO_GATEWAY_DATA_DIR || '/var/lib/personal-zalo-gateway',
  syncIntervalMs: readInt('PERSONAL_ZALO_GATEWAY_SYNC_INTERVAL_SEC', 600) * 1000,
  maxBatchConversations: readInt('PERSONAL_ZALO_GATEWAY_MAX_BATCH_CONVERSATIONS', 20),
  maxMessagesPerConversation: readInt('PERSONAL_ZALO_GATEWAY_MAX_MESSAGES_PER_CONVERSATION', 200),
  requestTimeoutMs: readInt('PERSONAL_ZALO_GATEWAY_REQUEST_TIMEOUT_MS', 20000),
  appUrl: process.env.PERSONAL_ZALO_GATEWAY_APP_URL || '',
  nodeEnv: process.env.NODE_ENV || 'development',
};

export interface PersonalZaloChatbotConfig {
  baseUrl: string;
  secret: string;
}

export function personalZaloChatbotConfig(): PersonalZaloChatbotConfig | null {
  const baseUrl = (process.env.PERSONAL_ZALO_CHATBOT_BASE_URL || '').trim();
  const secret = process.env.PERSONAL_ZALO_CHATBOT_SECRET || '';
  if (!baseUrl || secret.length < 32) return null;
  return { baseUrl, secret };
}

export function stateFilePath(): string {
  return path.join(config.dataDir, 'gateway-state.json');
}

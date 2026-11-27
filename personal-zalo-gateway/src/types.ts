export type ThreadType = 'user' | 'group';
export type AccountStatus = 'connected' | 'disconnected' | 'qr_pending' | 'connecting';
export type SenderType = 'customer' | 'agent' | 'system';

export interface SessionData {
  cookie: unknown;
  imei: string;
  userAgent: string;
}

export interface GatewayAttachment {
  type: string;
  url?: string;
  name?: string;
  localPath?: string;
}

export interface GatewayMessageRecord {
  externalId: string;
  senderType: SenderType;
  senderName: string;
  content: string;
  contentType: string;
  attachments: GatewayAttachment[];
  sentAt: string;
  rawData: Record<string, unknown>;
  syncedAt?: string;
  deletedAt?: string;
  historyOnly?: boolean;
}

export interface GatewayConversationRecord {
  externalId: string;
  threadType: ThreadType;
  externalUserId: string;
  customerName: string;
  lastMessageAt: string;
  metadata: Record<string, unknown>;
  messages: Record<string, GatewayMessageRecord>;
  messageOrder: string[];
  pending: boolean;
  lastSyncedAt?: string;
}

export interface GatewayAccountRecord {
  id: string;
  tenantId: string;
  channelId: string;
  importEndpoint: string;
  importSecret: string;
  accountExternalId: string;
  status: AccountStatus;
  displayName: string;
  avatarUrl?: string;
  zaloUid?: string;
  qrImage?: string;
  qrGeneratedAt?: string;
  sessionData?: SessionData;
  lastError?: string;
  lastImportedAt?: string;
  // Set when CQA rejects imports for this account's tenant/channel (the
  // channel was deleted or is no longer a personal Zalo import channel).
  // Scheduled sync skips the account until it is saved again (re-pointed by
  // CQA) or a manual sync succeeds.
  syncBlockedReason?: string;
  syncBlockedAt?: string;
  createdAt: string;
  updatedAt: string;
  threads: Record<string, GatewayConversationRecord>;
  chatbotTurns?: Record<string, GatewayChatbotTurnRecord>;
}

export type GatewayChatbotTurnStatus = 'captured' | 'pending' | 'processing' | 'ready' | 'sending' | 'unknown' | 'failed' | 'expired';

export interface GatewayChatbotReplyImage {
  filename: string;
  mediaType: 'image/png' | 'image/jpeg';
  width: number;
  height: number;
  dataBase64: string;
  deliveryToken?: string;
}

export interface GatewayChatbotReply {
  reply: string;
  deliveryToken?: string;
  images?: GatewayChatbotReplyImage[];
  deliveryReceipt?: string;
}

export interface GatewayChatbotTurnRecord {
  message: IncomingGatewayMessage;
  tenantId: string;
  channelId: string;
  accountExternalId: string;
  status: GatewayChatbotTurnStatus;
  reply?: GatewayChatbotReply;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
}

export interface GatewayStoreShape {
  accounts: Record<string, GatewayAccountRecord>;
}

export interface IncomingGatewayMessage {
  senderUid: string;
  senderName: string;
  content: string;
  contentType: string;
  msgId: string;
  timestamp: number;
  isSelf: boolean;
  threadId: string;
  threadType: ThreadType;
  groupName?: string;
  mentions?: { uid: string; pos: number; len: number }[];
  attachments?: GatewayAttachment[];
  rawData?: Record<string, unknown>;
  sourceContentIsString?: boolean;
  sourceTimestampIsValid?: boolean;
}

export interface SyncBatchMessage {
  external_id: string;
  sender_type: SenderType;
  sender_name: string;
  content: string;
  content_type: string;
  attachments: GatewayAttachment[];
  sent_at: string;
  raw_data: Record<string, unknown>;
}

export interface SyncBatchConversation {
  conversation: {
    external_id: string;
    thread_type: ThreadType;
    external_user_id: string;
    customer_name: string;
    last_message_at: string;
    metadata: Record<string, unknown>;
  };
  messages: SyncBatchMessage[];
  threadKey: string;
}

export interface AccountSyncBatch {
  account: GatewayAccountRecord;
  conversations: SyncBatchConversation[];
}

export interface UpsertAccountInput {
  tenantId: string;
  channelId: string;
  importEndpoint: string;
  importSecret: string;
  accountExternalId?: string;
  displayName?: string;
}

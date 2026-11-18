package config

import "strings"

// ChatMemoryConfig controls the chat-memory pipeline (see package chatmemory).
//
// MySQL holds every chat turn and the derived session summaries and customer
// profiles. The vector index only stores one embedding per session summary
// and can be rebuilt from MySQL at any time, so switching VectorStore between
// "astra" and "vectorize" is a configuration change, not a data migration.
type ChatMemoryConfig struct {
	// Enabled turns on live recording of chat turns into MySQL and the
	// session summarizer. Default ON (CHAT_MEMORY_ENABLED=false disables).
	Enabled bool
	// SummarizeIdleMinutes is how long a session must be idle before it is
	// summarized. 0 means "session timeout + 10 minutes".
	SummarizeIdleMinutes int
	// Whether the bot reads the profile card and whether session issues are
	// alerted are per-channel settings (channel metadata), see
	// chatmemory.ChannelSettingsFrom.

	// VectorStore is "astra", "vectorize" or "none". Empty means "astra"
	// when Astra credentials exist, else "none".
	VectorStore string
	// VectorRetentionDays deletes summary vectors older than this many days
	// from the index (the MySQL summary is kept). 0 disables deletion.
	VectorRetentionDays int

	AstraCollection string

	VectorizeAccountID string
	VectorizeAPIToken  string
	VectorizeIndex     string

	// Embedding endpoint (OpenAI-compatible /v1/embeddings). Empty values fall
	// back to the tenant OpenAI key / LANGFLOW_EMBEDDING_* settings. For
	// Cloudflare Workers AI use base URL
	// https://api.cloudflare.com/client/v4/accounts/<id>/ai and model
	// @cf/baai/bge-m3 (1024 dimensions).
	EmbeddingAPIKey     string
	EmbeddingModel      string
	EmbeddingBaseURL    string
	EmbeddingDimensions int
}

func loadChatMemoryConfig() ChatMemoryConfig {
	return ChatMemoryConfig{
		Enabled:              !strings.EqualFold(getEnv("CHAT_MEMORY_ENABLED", "true"), "false"),
		SummarizeIdleMinutes: getEnvInt("CHAT_MEMORY_SUMMARIZE_IDLE_MINUTES", 0),
		VectorStore:          strings.ToLower(strings.TrimSpace(getEnv("CHAT_MEMORY_VECTOR_STORE", ""))),
		VectorRetentionDays:  getEnvInt("CHAT_MEMORY_VECTOR_RETENTION_DAYS", 365),
		AstraCollection:      getEnv("CHAT_MEMORY_ASTRA_COLLECTION", "zalo_session_summaries"),
		VectorizeAccountID:   getEnv("CHAT_MEMORY_VECTORIZE_ACCOUNT_ID", ""),
		VectorizeAPIToken:    getEnv("CHAT_MEMORY_VECTORIZE_API_TOKEN", ""),
		VectorizeIndex:       getEnv("CHAT_MEMORY_VECTORIZE_INDEX", "zalo-session-summaries"),
		EmbeddingAPIKey:      getEnv("CHAT_MEMORY_EMBEDDING_API_KEY", ""),
		EmbeddingModel:       getEnv("CHAT_MEMORY_EMBEDDING_MODEL", "text-embedding-3-small"),
		EmbeddingBaseURL:     strings.TrimRight(getEnv("CHAT_MEMORY_EMBEDDING_BASE_URL", ""), "/"),
		EmbeddingDimensions:  getEnvInt("CHAT_MEMORY_EMBEDDING_DIMENSIONS", 1536),
	}
}

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	// Server
	ServerPort         string
	ServerHost         string
	AppInternalBaseURL string

	// Database
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	// Security
	JWTSecret            string
	EncryptionKey        string // 32 bytes for AES-256-GCM
	InternalImportSecret string

	// Rate limiting
	RateLimitPerIP   int // requests per minute
	RateLimitPerUser int

	// AI
	AIMaxTokens int // max tokens for AI responses

	// Environment
	Env string // "development" | "production"

	// Sidecars
	PersonalZaloGatewayBaseURL string

	// Langflow Integration
	LangflowAPIURL       string
	LangflowAPIKey       string
	LangflowFlowID       string
	LangflowPublicFlowID string

	// Redis Task Queue
	RedisURL string

	// Postgres Connection URL
	PostgresURL string

	// Astra DB Integration
	AstraDBAPIEndpoint       string
	AstraDBToken             string
	AstraDBKeyspace          string
	AstraDBCollection        string
	AstraDBProductCollection string

	// Session Gateway Defaults
	ChatbotSessionKeyword        string
	ChatbotSessionEndKeyword     string
	ChatbotSessionWelcomeMessage string
	ChatbotSessionGoodbyeMessage string
	ChatbotSessionTimeout        int
	ChatbotSessionTimeoutMessage string
	ChatbotHistoryRetentionDays  int
}

func Load() (*Config, error) {
	cfg := &Config{
		ServerPort:                 getEnv("SERVER_PORT", "8080"),
		ServerHost:                 getEnv("SERVER_HOST", "127.0.0.1"),
		AppInternalBaseURL:         getEnv("APP_INTERNAL_BASE_URL", ""),
		DBHost:                     getEnv("DB_HOST", "localhost"),
		DBPort:                     getEnv("DB_PORT", "3306"),
		DBUser:                     getEnv("DB_USER", "cqa"),
		DBPassword:                 getEnv("DB_PASSWORD", ""),
		DBName:                     getEnv("DB_NAME", "cqa"),
		JWTSecret:                  getEnv("JWT_SECRET", ""),
		EncryptionKey:              getEnv("ENCRYPTION_KEY", ""),
		InternalImportSecret:       getEnv("INTERNAL_IMPORT_SECRET", ""),
		RateLimitPerIP:             getEnvInt("RATE_LIMIT_PER_IP", 500),
		RateLimitPerUser:           getEnvInt("RATE_LIMIT_PER_USER", 1000),
		AIMaxTokens:                getEnvInt("AI_MAX_TOKENS", 16384),
		Env:                        getEnv("APP_ENV", "development"),
		PersonalZaloGatewayBaseURL: strings.TrimRight(getEnv("PERSONAL_ZALO_GATEWAY_BASE_URL", ""), "/"),
		LangflowAPIURL:             strings.TrimRight(getEnv("LANGFLOW_API_URL", ""), "/"),
		LangflowAPIKey:             getEnv("LANGFLOW_API_KEY", ""),
		LangflowFlowID:             getEnv("LANGFLOW_FLOW_ID", ""),
		LangflowPublicFlowID:       getEnv("LANGFLOW_PUBLIC_FLOW_ID", ""),
		RedisURL:                   getEnv("REDIS_URL", "redis://localhost:6379/0"),
		PostgresURL:                getEnv("POSTGRES_URL", "postgresql://bbikhai:Kdoan4801@103.130.218.163:5432/bbi"),
		AstraDBAPIEndpoint:         strings.TrimRight(getEnv("ASTRA_DB_API_ENDPOINT", ""), "/"),
		AstraDBToken:               getEnv("ASTRA_DB_TOKEN", ""),
		AstraDBKeyspace:            getEnv("ASTRA_DB_KEYSPACE", ""),
		AstraDBCollection:          getEnv("ASTRA_DB_COLLECTION", "zalo_chat_history"),
		AstraDBProductCollection:   getEnv("ASTRA_DB_PRODUCT_COLLECTION", "erp_product_bbi"),
		ChatbotSessionKeyword:        getEnv("CHATBOT_SESSION_KEYWORD", "chào bull"),
		ChatbotSessionEndKeyword:     getEnv("CHATBOT_SESSION_END_KEYWORD", "tạm biệt"),
		ChatbotSessionWelcomeMessage: getEnv("CHATBOT_SESSION_WELCOME_MESSAGE", "Chào bạn, tôi đã sẵn sàng!"),
		ChatbotSessionGoodbyeMessage: getEnv("CHATBOT_SESSION_GOODBYE_MESSAGE", "Phiên hỗ trợ đã kết thúc. Hẹn gặp lại bạn!"),
		ChatbotSessionTimeout:        getEnvInt("CHATBOT_SESSION_TIMEOUT", 30),
		ChatbotSessionTimeoutMessage: getEnv("CHATBOT_SESSION_TIMEOUT_MESSAGE", "Nếu bạn không cần mình hỗ trợ gì thêm, mình xin phép dừng cuộc trò chuyện tại đây, nếu cần thì bạn biết kiếm mình sao rồi đó ;)"),
		ChatbotHistoryRetentionDays:  getEnvInt("CHATBOT_HISTORY_RETENTION_DAYS", 30),
	}

	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	if len(cfg.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 characters for HS256 security")
	}
	if cfg.EncryptionKey == "" {
		return nil, fmt.Errorf("ENCRYPTION_KEY is required")
	}
	if len(cfg.EncryptionKey) != 32 {
		return nil, fmt.Errorf("ENCRYPTION_KEY must be exactly 32 bytes for AES-256-GCM")
	}
	if cfg.DBPassword == "" {
		return nil, fmt.Errorf("DB_PASSWORD is required")
	}
	if cfg.InternalImportSecret == "" {
		// Phase 1 fallback to keep existing deployments bootable while allowing a narrower secret.
		cfg.InternalImportSecret = cfg.JWTSecret
	}

	return cfg, nil
}

func (c *Config) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
}

func (c *Config) ListenAddr() string {
	return fmt.Sprintf("%s:%s", c.ServerHost, c.ServerPort)
}

func (c *Config) IsProduction() bool {
	return c.Env == "production"
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

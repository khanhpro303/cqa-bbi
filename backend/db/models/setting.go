package models

import "time"

type AppSetting struct {
	ID             string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID       string    `gorm:"type:char(36);not null;uniqueIndex:idx_setting_tenant_key" json:"tenant_id"`
	SettingKey     string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_setting_tenant_key" json:"setting_key"`
	ValueEncrypted []byte    `gorm:"type:varbinary(2048)" json:"-"`
	ValuePlain     string    `gorm:"type:text" json:"value_plain,omitempty"`
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt      time.Time `gorm:"not null" json:"updated_at"`
}

func (AppSetting) TableName() string {
	return "app_settings"
}

type NotificationLog struct {
	ID           string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID     string    `gorm:"type:char(36);not null;index:idx_notiflog_tenant_sent" json:"tenant_id"`
	JobID        string    `gorm:"type:char(36)" json:"job_id"`
	JobRunID     string    `gorm:"type:char(36)" json:"job_run_id"`
	ChannelType  string    `gorm:"type:varchar(20);not null" json:"channel_type"` // telegram | email | zalo | zalo_oa
	// Source distinguishes the origin of the notification: "job" (AI task output
	// dispatched via notifications.Dispatcher) or "campaign" (CRM campaign failure
	// alert). Defaults to "job" so pre-existing rows keep their original meaning
	// after migration; campaign rows are backfilled in AutoMigrate.
	Source       string    `gorm:"type:varchar(20);default:'job';index:idx_notiflog_tenant_source" json:"source"`
	Recipient    string    `gorm:"type:varchar(500);not null" json:"recipient"`
	Subject      string    `gorm:"type:varchar(500)" json:"subject"`
	Body         string    `gorm:"type:text;not null" json:"body"`
	Status       string    `gorm:"type:varchar(20);not null" json:"status"` // sent | failed
	ErrorMessage string    `gorm:"type:text" json:"error_message,omitempty"`
	SentAt       time.Time `gorm:"not null;index:idx_notiflog_tenant_sent" json:"sent_at"`
	CreatedAt    time.Time `gorm:"not null" json:"created_at"`
}

type AIUsageLog struct {
	ID           string `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID     string `gorm:"type:char(36);not null;index:idx_aiusage_tenant_created;index:idx_aiusage_tenant_source_created" json:"tenant_id"`
	JobID        string `gorm:"type:char(36)" json:"job_id"`
	JobRunID     string `gorm:"type:char(36)" json:"job_run_id"`
	// Source distinguishes where the AI spend came from: "job" (QC/classification
	// analysis) or "chatbot" (Zalo OA live chatbot). Defaults to "job" so existing
	// rows keep their original meaning after migration.
	Source string `gorm:"type:varchar(20);default:'job';index:idx_aiusage_tenant_source_created" json:"source"`
	// ChannelID is the OA channel that incurred the cost (empty for job spend).
	ChannelID string `gorm:"type:char(36);index:idx_aiusage_channel" json:"channel_id"`
	// SenderExternalID is the Zalo user ID of the employee/customer that triggered
	// a chatbot turn — used to break cost down per employee.
	SenderExternalID string `gorm:"type:varchar(100);index:idx_aiusage_sender" json:"sender_external_id"`
	// SenderName is the cached display name (e.g. whitelist employee name).
	SenderName string `gorm:"type:varchar(255)" json:"sender_name"`
	// AgentType is "public" (group GMF) | "private" (1-1 whitelist) | "" (job).
	AgentType string `gorm:"type:varchar(20)" json:"agent_type"`
	// GroupID links to the CRMGroup when the turn happened in a GMF group chat.
	GroupID      string    `gorm:"type:char(36)" json:"group_id"`
	Provider     string    `gorm:"type:varchar(20)" json:"provider"`
	Model        string    `gorm:"type:varchar(100)" json:"model"`
	InputTokens  int       `gorm:"default:0" json:"input_tokens"`
	OutputTokens int       `gorm:"default:0" json:"output_tokens"`
	CostUSD      float64   `gorm:"type:decimal(10,6)" json:"cost_usd"`
	CreatedAt    time.Time `gorm:"not null;index:idx_aiusage_tenant_created;index:idx_aiusage_tenant_source_created" json:"created_at"`
}

type OAuthClient struct {
	ID               string    `gorm:"type:char(36);primaryKey" json:"id"`
	ClientID         string    `gorm:"type:varchar(255);uniqueIndex;not null" json:"client_id"`
	ClientSecretHash string    `gorm:"type:varchar(255);not null" json:"-"`
	Name             string    `gorm:"type:varchar(255)" json:"name"`
	RedirectURIs     string    `gorm:"type:json" json:"redirect_uris"`
	Scopes           string    `gorm:"type:json" json:"scopes"`
	UserID           string    `gorm:"type:char(36)" json:"user_id"`
	CreatedAt        time.Time `gorm:"not null" json:"created_at"`
}

type OAuthAuthorizationCode struct {
	ID                  string    `gorm:"type:char(36);primaryKey" json:"id"`
	Code                string    `gorm:"type:varchar(255);uniqueIndex;not null" json:"code"`
	ClientID            string    `gorm:"type:varchar(255);not null;index" json:"client_id"`
	UserID              string    `gorm:"type:char(36);not null" json:"user_id"`
	RedirectURI         string    `gorm:"type:varchar(1024)" json:"redirect_uri"`
	Scopes              string    `gorm:"type:json" json:"scopes"`
	CodeChallenge       string    `gorm:"type:varchar(255)" json:"code_challenge"`
	CodeChallengeMethod string    `gorm:"type:varchar(10)" json:"code_challenge_method"`
	ExpiresAt           time.Time `gorm:"not null;index" json:"expires_at"`
	Used                bool      `gorm:"not null;default:false" json:"used"`
	CreatedAt           time.Time `gorm:"not null" json:"created_at"`
}

type OAuthToken struct {
	ID               string    `gorm:"type:char(36);primaryKey" json:"id"`
	ClientID         string    `gorm:"type:varchar(255);not null" json:"client_id"`
	UserID           string    `gorm:"type:char(36);not null" json:"user_id"`
	AccessTokenHash  string    `gorm:"type:varchar(255);not null;index:idx_oauth_access" json:"-"`
	RefreshTokenHash string    `gorm:"type:varchar(255)" json:"-"`
	Scopes           string    `gorm:"type:json" json:"scopes"`
	ExpiresAt        time.Time `gorm:"not null" json:"expires_at"`
	CreatedAt        time.Time `gorm:"not null" json:"created_at"`
}

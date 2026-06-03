package db

import (
	"fmt"
	"log"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func Connect(dsn string, isProduction bool) error {
	logLevel := logger.Info
	if isProduction {
		logLevel = logger.Warn
	}

	var err error
	DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	sqlDB, err := DB.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}

	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)

	log.Println("Database connected successfully")
	return nil
}

func AutoMigrate() error {
	// Drop old crm_group_employees table if it still has the deprecated 'user_id' column
	if DB.Migrator().HasTable("crm_group_employees") && DB.Migrator().HasColumn("crm_group_employees", "user_id") {
		log.Println("[db] dropping deprecated table crm_group_employees to rebuild with zalo_whitelist_id")
		if err := DB.Migrator().DropTable("crm_group_employees"); err != nil {
			log.Printf("[db] failed to drop table crm_group_employees: %v", err)
		}
	}

	err := DB.AutoMigrate(
		&models.User{},
		&models.Tenant{},
		&models.UserTenant{},
		&models.Channel{},
		&models.Conversation{},
		&models.Message{},
		&models.Job{},
		&models.JobRun{},
		&models.JobResult{},
		&models.AppSetting{},
		&models.CachedProduct{},
		&models.ERPRawProduct{},
		&models.CachedCustomer{},
		&models.ERPParentSKUExclusion{},
		&models.ERPCustomerCodeExclusion{},
		&models.NotificationLog{},
		&models.AIUsageLog{},
		&models.OAuthClient{},
		&models.OAuthAuthorizationCode{},
		&models.OAuthToken{},
		&models.ActivityLog{},
		&models.ZaloWhitelist{},
		&models.ERPEndpoint{},
		&models.ZaloCustomer{},
		&models.CRMGroup{},
		&models.CRMGroupEmployee{},
		&models.CRMGroupCustomer{},
		&models.ERPAuditLog{},
		&models.ERPGroupRateLimit{},
		&models.Campaign{},
		&models.CampaignSegment{},
		&models.CampaignRun{},
	)
	if err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}

	// Clean up deprecated database schema from older versions
	cleanupDeprecatedSchema()

	// Add unique constraints that GORM can't express directly
	addUniqueConstraints()

	log.Println("Database migration completed")
	return nil
}

func cleanupDeprecatedSchema() {
	// Drop old unique index if exists
	if DB.Migrator().HasIndex(&models.ERPEndpoint{}, "idx_erp_ep_tenant_agent_res") {
		if err := DB.Migrator().DropIndex(&models.ERPEndpoint{}, "idx_erp_ep_tenant_agent_res"); err != nil {
			log.Printf("[db] failed to drop deprecated index idx_erp_ep_tenant_agent_res: %v", err)
		} else {
			log.Println("[db] successfully dropped deprecated index idx_erp_ep_tenant_agent_res from erp_endpoints")
		}
	}

	// Drop deprecated column 'agent_type' if exists
	if DB.Migrator().HasColumn(&models.ERPEndpoint{}, "agent_type") {
		if err := DB.Migrator().DropColumn(&models.ERPEndpoint{}, "agent_type"); err != nil {
			log.Printf("[db] failed to drop deprecated column agent_type: %v", err)
		} else {
			log.Println("[db] successfully dropped deprecated column agent_type from erp_endpoints")
		}
	}
}


func addUniqueConstraints() {
	constraints := []struct {
		table   string
		name    string
		columns string
	}{
		{"channels", "uq_channel_tenant_type_ext", "tenant_id, channel_type, external_id"},
		{"conversations", "uq_conv_tenant_channel_ext", "tenant_id, channel_id, external_conversation_id"},
		{"messages", "uq_msg_tenant_conv_ext", "tenant_id, conversation_id, external_message_id"},
	}

	for _, c := range constraints {
		exists, err := uniqueIndexExists(c.table, c.name)
		if err != nil {
			log.Printf("[db] failed to check index %s on %s: %v", c.name, c.table, err)
			continue
		}
		if exists {
			continue
		}

		sql := fmt.Sprintf(
			"ALTER TABLE `%s` ADD UNIQUE INDEX `%s` (%s)",
			c.table, c.name, c.columns,
		)
		if err := DB.Exec(sql).Error; err != nil {
			log.Printf("[db] failed to create unique index %s on %s: %v", c.name, c.table, err)
		}
	}
}

func uniqueIndexExists(tableName, indexName string) (bool, error) {
	var count int64
	err := DB.Raw(`
		SELECT COUNT(1)
		FROM information_schema.statistics
		WHERE table_schema = DATABASE()
		  AND table_name = ?
		  AND index_name = ?
	`, tableName, indexName).Scan(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func Close() {
	if DB != nil {
		sqlDB, _ := DB.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
}

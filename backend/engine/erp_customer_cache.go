package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// defaultCustomerEndpoint is the fallback Cloudify endpoint for the customer
// (khách hàng) list when no override is set in the Global HTTP Method config.
const defaultCustomerEndpoint = "khach_hang/search"

// runERPCustomerCacheJob connects to Cloudify ERP, pulls the customer list
// (khach_hang/search), and caches it to local MySQL (cached_customers) for fast
// lookup. It does NOT push anything to Astra DB.
func (a *Analyzer) runERPCustomerCacheJob(ctx context.Context, job models.Job) (*models.JobRun, error) {
	now := time.Now()
	run := models.JobRun{
		ID:        pkg.NewUUID(),
		JobID:     job.ID,
		TenantID:  job.TenantID,
		StartedAt: now,
		Status:    "running",
		Summary:   "{}",
		CreatedAt: now,
	}
	if err := db.DB.Create(&run).Error; err != nil {
		return nil, fmt.Errorf("failed to create job run: %w", err)
	}

	db.LogActivity(job.TenantID, "", "system", "job.run.started", "job", job.ID,
		fmt.Sprintf("Job '%s': started ERP customer cache sync", job.Name), "", "")

	// 1. Load Cloudify ERP credentials
	erpURL, erpDB, erpLogin, erpPassword, err := loadCloudifyCredentials(job.TenantID, a.cfg)
	if err != nil {
		return a.failRun(&run, fmt.Errorf("load ERP credentials: %w", err))
	}
	if erpURL == "" || erpLogin == "" || erpPassword == "" {
		return a.failRun(&run, fmt.Errorf("ERP integration is not configured or incomplete for this tenant"))
	}

	// 2. Initialize Cloudify client
	client := &pkg.CloudifyClient{
		BaseURL:  erpURL,
		DB:       erpDB,
		Login:    erpLogin,
		Password: erpPassword,
	}

	// 3. Resolve endpoint from the Global HTTP Method config (key "customers"),
	//    falling back to the official khach_hang/search endpoint.
	endpointPath := resolveCustomerEndpoint(job.TenantID)

	data, err := client.SearchCustomEndpoint(endpointPath, map[string]string{
		"limit": "50000",
	})
	if err != nil {
		return a.failRun(&run, fmt.Errorf("pull ERP customers from %s: %w", endpointPath, err))
	}

	// 4. Deduplicate by MA
	seenCodes := make(map[string]bool)
	customers := make([]models.CachedCustomer, 0, len(data))
	insertedNow := time.Now()
	for _, c := range data {
		ma := getStringVal(c, "MA")
		if ma == "" {
			ma = getStringVal(c, "ma")
		}
		if ma != "" {
			if seenCodes[ma] {
				continue
			}
			seenCodes[ma] = true
		}

		erpIDFloat, _ := getFloatVal(c, "id")
		if erpIDFloat == 0 {
			erpIDFloat, _ = getFloatVal(c, "ID")
		}

		customers = append(customers, models.CachedCustomer{
			ID:              pkg.NewUUID(),
			TenantID:        job.TenantID,
			ERP_ID:          int64(erpIDFloat),
			MA:              ma,
			HO_VA_TEN:       coalesceFalse(firstNonEmpty(getStringVal(c, "HO_VA_TEN"), getStringVal(c, "ho_va_ten"))),
			EMAIL:           coalesceFalse(firstNonEmpty(getStringVal(c, "EMAIL"), getStringVal(c, "email"))),
			DIA_CHI:         coalesceFalse(firstNonEmpty(getStringVal(c, "DIA_CHI"), getStringVal(c, "dia_chi"))),
			DIEN_THOAI:      coalesceFalse(firstNonEmpty(getStringVal(c, "DIEN_THOAI"), getStringVal(c, "dien_thoai"))),
			ERP_CREATE_DATE: coalesceFalse(firstNonEmpty(getStringVal(c, "create_date"), getStringVal(c, "CREATE_DATE"))),
			CreatedAt:       insertedNow,
			UpdatedAt:       insertedNow,
		})
	}

	log.Printf("[customer_cache] job %s: fetched %d khách hàng from Cloudify ERP", job.Name, len(customers))

	// 5. Clear existing tenant rows, then batch insert.
	if errDel := db.DB.Where("tenant_id = ?", job.TenantID).Delete(&models.CachedCustomer{}).Error; errDel != nil {
		log.Printf("[customer_cache] warn: failed to clear cached_customers: %v", errDel)
	}

	const chunkSQLSize = 100
	for i := 0; i < len(customers); i += chunkSQLSize {
		end := i + chunkSQLSize
		if end > len(customers) {
			end = len(customers)
		}
		if errCreate := db.DB.Create(customers[i:end]).Error; errCreate != nil {
			log.Printf("[customer_cache] warn: failed to batch insert to cached_customers: %v", errCreate)
		}
	}

	finishedAt := time.Now()
	runStatus := "success"
	summaryMsg := fmt.Sprintf("Đã đồng bộ %d khách hàng từ ERP", len(customers))
	summaryJSON, _ := json.Marshal(map[string]interface{}{
		"message":                summaryMsg,
		"conversations_analyzed": len(customers),
		"conversations_found":    len(customers),
		"conversations_passed":   len(customers),
	})

	if err := db.DB.Model(&run).Updates(map[string]interface{}{
		"status":      runStatus,
		"finished_at": &finishedAt,
		"summary":     string(summaryJSON),
	}).Error; err != nil {
		log.Printf("[customer_cache] failed to update job run status: %v", err)
	}

	db.DB.Model(&job).Updates(map[string]interface{}{
		"last_run_at":     &finishedAt,
		"last_run_status": runStatus,
		"updated_at":      finishedAt,
	})

	db.LogActivity(job.TenantID, "", "system", "job.run.completed", "job", job.ID,
		fmt.Sprintf("Job '%s': completed, %s", job.Name, summaryMsg), "", "")

	return &run, nil
}

// resolveCustomerEndpoint reads the tenant's Global HTTP Method config
// (erp_global_method_permissions, key "customers") and returns the configured
// path, falling back to the official khach_hang/search endpoint.
func resolveCustomerEndpoint(tenantID string) string {
	endpointPath := defaultCustomerEndpoint

	var setting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'erp_global_method_permissions'", tenantID).First(&setting).Error; err != nil || setting.ValuePlain == "" {
		return endpointPath
	}

	type endpointConfig struct {
		Get  bool   `json:"get"`
		Post bool   `json:"post"`
		Path string `json:"path"`
	}
	var perms map[string]endpointConfig
	if json.Unmarshal([]byte(setting.ValuePlain), &perms) != nil {
		return endpointPath
	}

	cust, ok := perms["customers"]
	if !ok || cust.Path == "" || cust.Path == "customers" {
		return endpointPath
	}

	path := strings.TrimPrefix(cust.Path, "/")
	path = strings.TrimPrefix(path, "rest_api/private/")
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return defaultCustomerEndpoint
	}
	return path
}

// coalesceFalse normalizes Cloudify's "empty" sentinels to "". Empty fields can
// come back as the boolean false (rendered "false") or the literal string
// "false"/"False".
func coalesceFalse(s string) string {
	t := strings.TrimSpace(s)
	if strings.EqualFold(t, "false") {
		return ""
	}
	return t
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

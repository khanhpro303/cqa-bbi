package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// runERPProductCacheJob connects to Cloudify ERP, pulls danhmucvattuhanghoa/search data,
// and caches it to Astra DB keyspace cache_product and collection erp_product_bbi.
func (a *Analyzer) runERPProductCacheJob(ctx context.Context, job models.Job) (*models.JobRun, error) {
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
		fmt.Sprintf("Job '%s': started ERP product cache sync", job.Name), "", "")

	// 1. Load Cloudify ERP Credentials
	erpURL, erpDB, erpLogin, erpPassword, err := loadCloudifyCredentials(job.TenantID, a.cfg)
	if err != nil {
		return a.failRun(&run, fmt.Errorf("load ERP credentials: %w", err))
	}
	if erpURL == "" || erpLogin == "" || erpPassword == "" {
		return a.failRun(&run, fmt.Errorf("ERP integration is not configured or incomplete for this tenant"))
	}

	// 2. Initialize Cloudify Client
	client := &pkg.CloudifyClient{
		BaseURL:  erpURL,
		DB:       erpDB,
		Login:    erpLogin,
		Password: erpPassword,
	}

	// 3. Fetch products from ERP using pagination
	var allProducts []map[string]interface{}
	limit := 1000
	offset := 0
	for {
		params := map[string]string{
			"limit":  fmt.Sprintf("%d", limit),
			"offset": fmt.Sprintf("%d", offset),
		}
		data, err := client.SearchCustomEndpoint("danhmucvattuhanghoa/search", params)
		if err != nil {
			return a.failRun(&run, fmt.Errorf("pull ERP products at offset %d: %w", offset, err))
		}
		if len(data) == 0 {
			break
		}
		allProducts = append(allProducts, data...)
		if len(data) < limit {
			break
		}
		offset += limit
		if offset > 50000 { // safeguard
			break
		}
	}

	log.Printf("[erp_cache] job %s: fetched %d products from Cloudify ERP", job.Name, len(allProducts))

	// 4. Map products to the cache structure
	cachedProducts := make([]map[string]interface{}, 0, len(allProducts))
	for _, p := range allProducts {
		listTenNhomVTHH := getStringVal(p, "list_ten_nhom_vthh")
		if listTenNhomVTHH == "" {
			listTenNhomVTHH = getStringVal(p, "LIST_TEN_NHOM_VTHH")
		}

		khosp := getStringVal(p, "khosp")
		if khosp == "" {
			khosp = getStringVal(p, "KHOSP")
		}

		dvtChinhID := getStringVal(p, "dvt_chinh_id")
		if dvtChinhID == "" {
			dvtChinhID = getStringVal(p, "DVT_CHINH_ID")
		}

		cachedProducts = append(cachedProducts, map[string]interface{}{
			"ma_hang":             getStringVal(p, "ma_hang"),
			"ten_hang":            getStringVal(p, "ten_hang"),
			"nhan_hieu_name":      getStringVal(p, "nhan_hieu_name"),
			"thuoc_tinh_1":        getStringVal(p, "thuoc_tinh_1"),
			"thuoc_tinh_2":        getStringVal(p, "thuoc_tinh_2"),
			"ten_dong_bo_web":     getStringVal(p, "ten_dong_bo_web"),
			"list_ten_nhom_vthh":  listTenNhomVTHH,
			"khosp":               khosp,
			"dvt_chinh_id":        dvtChinhID,
			"created_at":          time.Now().Unix(),
		})
	}

	// 5. Connect to Astra DB and cache the data
	apiEndpoint := a.cfg.AstraDBAPIEndpoint
	token := a.cfg.AstraDBToken
	keyspace := "cache_product"
	collection := "erp_product_bbi"

	// Fallback to setting values if configured on the tenant level
	var endpointSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", job.TenantID, "astradb_api_endpoint").First(&endpointSetting).Error; err == nil && endpointSetting.ValuePlain != "" {
		apiEndpoint = endpointSetting.ValuePlain
	}
	var tokenSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", job.TenantID, "astradb_token").First(&tokenSetting).Error; err == nil {
		if len(tokenSetting.ValueEncrypted) > 0 {
			if decrypted, err := pkg.Decrypt(tokenSetting.ValueEncrypted, a.cfg.EncryptionKey); err == nil {
				token = string(decrypted)
			}
		} else if tokenSetting.ValuePlain != "" {
			token = tokenSetting.ValuePlain
		}
	}

	if apiEndpoint == "" || token == "" {
		return a.failRun(&run, fmt.Errorf("Astra DB is not configured (missing endpoint or token)"))
	}

	// 5.1 Create Collection on Astra DB (if not exists)
	err = createAstraCollection(ctx, apiEndpoint, token, keyspace, collection)
	if err != nil {
		log.Printf("[erp_cache] warn: create Astra DB collection failed: %v", err)
	}

	// 5.2 Clear existing records (deleteMany with filter {})
	err = clearAstraCollection(ctx, apiEndpoint, token, keyspace, collection)
	if err != nil {
		return a.failRun(&run, fmt.Errorf("clear Astra DB cache collection: %w", err))
	}

	// 5.3 Batch insert products in chunks of 20
	err = insertAstraCollectionBatches(ctx, apiEndpoint, token, keyspace, collection, cachedProducts)
	if err != nil {
		return a.failRun(&run, fmt.Errorf("write products to Astra DB: %w", err))
	}

	finishedAt := time.Now()
	runStatus := "success"
	summaryMsg := fmt.Sprintf("Đã đồng bộ %d sản phẩm từ ERP vào Astra DB", len(cachedProducts))
	summaryJSON, _ := json.Marshal(map[string]interface{}{
		"message":                summaryMsg,
		"conversations_analyzed": len(cachedProducts),
		"conversations_found":    len(cachedProducts),
		"conversations_passed":   len(cachedProducts),
	})

	if err := db.DB.Model(&run).Updates(map[string]interface{}{
		"status":      runStatus,
		"finished_at": &finishedAt,
		"summary":     string(summaryJSON),
	}).Error; err != nil {
		log.Printf("[erp_cache] failed to update job run status: %v", err)
	}

	// Update job last run status
	db.DB.Model(&job).Updates(map[string]interface{}{
		"last_run_at":     &finishedAt,
		"last_run_status": runStatus,
		"updated_at":      finishedAt,
	})

	db.LogActivity(job.TenantID, "", "system", "job.run.completed", "job", job.ID,
		fmt.Sprintf("Job '%s': completed, %s", job.Name, summaryMsg), "", "")

	return &run, nil
}

// createAstraCollection sends createCollection command to keyspace endpoint.
func createAstraCollection(ctx context.Context, apiEndpoint, token, keyspace, collection string) error {
	url := fmt.Sprintf("%s/api/json/v1/%s", apiEndpoint, keyspace)
	payload := map[string]interface{}{
		"createCollection": map[string]interface{}{
			"name": collection,
		},
	}
	return sendAstraRequest(ctx, url, token, payload)
}

// clearAstraCollection sends deleteMany command to collection endpoint.
func clearAstraCollection(ctx context.Context, apiEndpoint, token, keyspace, collection string) error {
	url := fmt.Sprintf("%s/api/json/v1/%s/%s", apiEndpoint, keyspace, collection)
	payload := map[string]interface{}{
		"deleteMany": map[string]interface{}{
			"filter": map[string]interface{}{},
		},
	}
	return sendAstraRequest(ctx, url, token, payload)
}

// insertAstraCollectionBatches chunks products into batches of 20 and inserts them.
func insertAstraCollectionBatches(ctx context.Context, apiEndpoint, token, keyspace, collection string, products []map[string]interface{}) error {
	url := fmt.Sprintf("%s/api/json/v1/%s/%s", apiEndpoint, keyspace, collection)
	
	const batchSize = 20
	for i := 0; i < len(products); i += batchSize {
		end := i + batchSize
		if end > len(products) {
			end = len(products)
		}
		chunk := products[i:end]

		payload := map[string]interface{}{
			"insertMany": map[string]interface{}{
				"documents": chunk,
			},
		}

		err := sendAstraRequest(ctx, url, token, payload)
		if err != nil {
			return fmt.Errorf("batch insert at index %d: %w", i, err)
		}
	}
	return nil
}

func sendAstraRequest(ctx context.Context, url, token string, payload interface{}) error {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Token", token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("Astra DB API returned status code %d", resp.StatusCode)
	}

	// Parse body for errors field inside the JSON response
	var responseBody struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&responseBody); err == nil {
		if len(responseBody.Errors) > 0 {
			// Check if it's "Collection already exists" error, which we can safely ignore
			firstErrMsg := responseBody.Errors[0].Message
			if strings.Contains(strings.ToLower(firstErrMsg), "already exists") {
				return nil
			}
			return fmt.Errorf("Astra DB API error: %s", firstErrMsg)
		}
	}

	return nil
}

// loadCloudifyCredentials load ERP credentials from settings
func loadCloudifyCredentials(tenantID string, cfg *config.Config) (erpURL, erpDB, erpLogin, erpPassword string, err error) {
	settings := map[string]*string{
		"erp_api_url":      &erpURL,
		"erp_api_db":       &erpDB,
		"erp_api_username": &erpLogin,
	}
	for key, dst := range settings {
		var s models.AppSetting
		if e := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&s).Error; e == nil {
			*dst = strings.TrimSpace(s.ValuePlain)
		}
	}

	// Password is encrypted
	var pwSetting models.AppSetting
	if e := db.DB.Where("tenant_id = ? AND setting_key = 'erp_api_password'", tenantID).First(&pwSetting).Error; e == nil {
		if len(pwSetting.ValueEncrypted) > 0 {
			decrypted, decErr := pkg.Decrypt(pwSetting.ValueEncrypted, cfg.EncryptionKey)
			if decErr != nil {
				err = fmt.Errorf("decrypt ERP password: %w", decErr)
				return
			}
			erpPassword = string(decrypted)
		} else {
			erpPassword = pwSetting.ValuePlain
		}
	}

	return
}

func getStringVal(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok && val != nil {
		switch v := val.(type) {
		case string:
			return v
		case []interface{}:
			if len(v) > 1 {
				if s, ok := v[1].(string); ok {
					return s
				}
			}
			if len(v) > 0 {
				return fmt.Sprintf("%v", v[0])
			}
		default:
			return fmt.Sprintf("%v", val)
		}
	}
	return ""
}

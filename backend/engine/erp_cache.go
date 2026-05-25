package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
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

	// 3. Fetch products from ERP (requesting up to 50000 items)
	data, err := client.SearchCustomEndpoint("danhmucvattuhanghoa/search", map[string]string{
		"limit": "50000",
	})
	if err != nil {
		return a.failRun(&run, fmt.Errorf("pull ERP products: %w", err))
	}

	// Deduplicate by MA / ma_hang to be safe
	var allProducts []map[string]interface{}
	seenCodes := make(map[string]bool)
	for _, p := range data {
		ma := getStringVal(p, "MA")
		if ma == "" {
			ma = getStringVal(p, "ma")
		}
		if ma == "" {
			ma = getStringVal(p, "MA_HANG")
		}
		if ma == "" {
			ma = getStringVal(p, "ma_hang")
		}
		if ma != "" {
			if !seenCodes[ma] {
				seenCodes[ma] = true
				allProducts = append(allProducts, p)
			}
		} else {
			allProducts = append(allProducts, p)
		}
	}

	log.Printf("[erp_cache] job %s: fetched %d products from Cloudify ERP", job.Name, len(allProducts))

	// 4. Map products to the cache structure
	cachedProducts := make([]map[string]interface{}, 0, len(allProducts))
	for _, p := range allProducts {
		// 1. MA
		ma := getStringVal(p, "MA")
		if ma == "" {
			ma = getStringVal(p, "ma")
		}
		if ma == "" {
			ma = getStringVal(p, "MA_HANG")
		}
		if ma == "" {
			ma = getStringVal(p, "ma_hang")
		}

		// 2. TEN_DONG_BO_WEB
		tenDongBoWeb := getStringVal(p, "TEN_DONG_BO_WEB")
		if tenDongBoWeb == "" {
			tenDongBoWeb = getStringVal(p, "ten_dong_bo_web")
		}

		// 3. TEN
		ten := getStringVal(p, "TEN")
		if ten == "" {
			ten = getStringVal(p, "ten")
		}
		if ten == "" {
			ten = getStringVal(p, "TEN_HANG")
		}
		if ten == "" {
			ten = getStringVal(p, "ten_hang")
		}

		// 4. THUOC_TINH_1
		thuocTinh1 := getStringVal(p, "THUOC_TINH_1")
		if thuocTinh1 == "" {
			thuocTinh1 = getStringVal(p, "thuoc_tinh_1")
		}

		// 5. THUOC_TINH_2
		thuocTinh2 := getStringVal(p, "THUOC_TINH_2")
		if thuocTinh2 == "" {
			thuocTinh2 = getStringVal(p, "thuoc_tinh_2")
		}

		// 6. DON_GIA_BAN
		donGiaBanVal, _ := getFloatVal(p, "DON_GIA_BAN")
		if donGiaBanVal == 0 {
			donGiaBanVal, _ = getFloatVal(p, "don_gia_ban")
		}
		if donGiaBanVal == 0 {
			donGiaBanVal, _ = getFloatVal(p, "DON_GIA")
		}
		if donGiaBanVal == 0 {
			donGiaBanVal, _ = getFloatVal(p, "don_gia")
		}

		// 7. LINK_ANH
		linkAnh := getStringVal(p, "LINK_ANH")
		if linkAnh == "" {
			linkAnh = getStringVal(p, "link_anh")
		}

		// 8. NHAN_HIEU_NAME (lấy từ list NHAN_HIEU)
		nhanHieuName := getStringVal(p, "NHAN_HIEU")
		if nhanHieuName == "" {
			nhanHieuName = getStringVal(p, "nhan_hieu")
		}
		if nhanHieuName == "" {
			nhanHieuName = getStringVal(p, "NHAN_HIEU_NAME")
		}
		if nhanHieuName == "" {
			nhanHieuName = getStringVal(p, "nhan_hieu_name")
		}

		// 9. KHO (lấy từ list KHO_NGAM_DINH_ID)
		kho := getStringVal(p, "KHO_NGAM_DINH_ID")
		if kho == "" {
			kho = getStringVal(p, "kho_ngam_dinh_id")
		}
		if kho == "" {
			kho = getStringVal(p, "KHO")
		}
		if kho == "" {
			kho = getStringVal(p, "kho")
		}
		if kho == "" {
			kho = getStringVal(p, "KHOSP")
		}
		if kho == "" {
			kho = getStringVal(p, "khosp")
		}

		// 10. MA_CHA
		maCha := getStringVal(p, "MA_CHA")
		if maCha == "" {
			maCha = getStringVal(p, "ma_cha")
		}

		// 11. DVT (lấy từ list DVT_CHINH_ID)
		dvt := getStringVal(p, "DVT_CHINH_ID")
		if dvt == "" {
			dvt = getStringVal(p, "dvt_chinh_id")
		}
		if dvt == "" {
			dvt = getStringVal(p, "DVT")
		}
		if dvt == "" {
			dvt = getStringVal(p, "dvt")
		}

		// 12. LIST_TEN_NHOM_VTHH
		listTenNhomVthh := getStringVal(p, "LIST_TEN_NHOM_VTHH")
		if listTenNhomVthh == "" {
			listTenNhomVthh = getStringVal(p, "list_ten_nhom_vthh")
		}

		// Create combined vectorize string for Astra DB auto-embedding
		vectorizeParts := []string{}
		if ma != "" {
			vectorizeParts = append(vectorizeParts, fmt.Sprintf("Mã: %s", ma))
		}
		if ten != "" {
			vectorizeParts = append(vectorizeParts, fmt.Sprintf("Tên: %s", ten))
		}
		if tenDongBoWeb != "" {
			vectorizeParts = append(vectorizeParts, fmt.Sprintf("Đồng bộ web: %s", tenDongBoWeb))
		}
		if nhanHieuName != "" {
			vectorizeParts = append(vectorizeParts, fmt.Sprintf("Nhãn hiệu: %s", nhanHieuName))
		}
		if listTenNhomVthh != "" {
			vectorizeParts = append(vectorizeParts, fmt.Sprintf("Nhóm: %s", listTenNhomVthh))
		}
		if dvt != "" {
			vectorizeParts = append(vectorizeParts, fmt.Sprintf("Đơn vị tính: %s", dvt))
		}
		if kho != "" {
			vectorizeParts = append(vectorizeParts, fmt.Sprintf("Kho: %s", kho))
		}
		if thuocTinh1 != "" {
			vectorizeParts = append(vectorizeParts, fmt.Sprintf("Màu sắc: %s", thuocTinh1))
		}
		if thuocTinh2 != "" {
			vectorizeParts = append(vectorizeParts, fmt.Sprintf("Kích thước: %s", thuocTinh2))
		}
		vectorizeStr := strings.Join(vectorizeParts, ". ") + "."

		// Build the clean document with ONLY the requested fields
		doc := make(map[string]interface{})
		doc["MA"] = ma
		doc["TEN_DONG_BO_WEB"] = tenDongBoWeb
		doc["TEN"] = ten
		doc["THUOC_TINH_1"] = thuocTinh1
		doc["THUOC_TINH_2"] = thuocTinh2
		doc["DON_GIA_BAN"] = donGiaBanVal
		doc["LINK_ANH"] = linkAnh
		doc["NHAN_HIEU_NAME"] = nhanHieuName
		doc["LIST_TEN_NHOM_VTHH"] = listTenNhomVthh
		doc["KHO"] = kho
		doc["MA_CHA"] = maCha
		doc["DVT"] = dvt
		doc["$vectorize"] = vectorizeStr
		doc["created_at"] = time.Now().Unix()

		cachedProducts = append(cachedProducts, doc)
	}

	// Extract unique product groups and save to app_settings table
	var uniqueGroups []string
	groupMap := make(map[string]bool)
	for _, p := range allProducts {
		listTenNhomVthh := getStringVal(p, "LIST_TEN_NHOM_VTHH")
		if listTenNhomVthh == "" {
			listTenNhomVthh = getStringVal(p, "list_ten_nhom_vthh")
		}
		if listTenNhomVthh != "" {
			parts := strings.Split(listTenNhomVthh, ",")
			for _, part := range parts {
				trimmed := strings.TrimSpace(part)
				if trimmed != "" && !groupMap[trimmed] {
					groupMap[trimmed] = true
					uniqueGroups = append(uniqueGroups, trimmed)
				}
			}
		}
	}

	if len(uniqueGroups) > 0 {
		groupJSON, err := json.Marshal(uniqueGroups)
		if err == nil {
			var s models.AppSetting
			errFind := db.DB.Where("tenant_id = ? AND setting_key = 'list_ten_nhom_vthh'", job.TenantID).First(&s).Error
			if errFind == nil {
				db.DB.Model(&s).Updates(map[string]interface{}{
					"value_plain": string(groupJSON),
					"updated_at":  time.Now(),
				})
			} else {
				db.DB.Create(&models.AppSetting{
					ID:         pkg.NewUUID(),
					TenantID:   job.TenantID,
					SettingKey: "list_ten_nhom_vthh",
					ValuePlain: string(groupJSON),
					CreatedAt:  time.Now(),
					UpdatedAt:  time.Now(),
				})
			}
		}
	}

	// 5. Connect to Astra DB and cache the data
	apiEndpoint := a.cfg.AstraDBAPIEndpoint
	token := a.cfg.AstraDBToken

	keyspace := "cache_product"
	if a.cfg.AstraDBKeyspace != "" {
		keyspace = a.cfg.AstraDBKeyspace
	}

	collection := "erp_product_bbi"
	if a.cfg.AstraDBProductCollection != "" {
		collection = a.cfg.AstraDBProductCollection
	}

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
	var keyspaceSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", job.TenantID, "astradb_keyspace").First(&keyspaceSetting).Error; err == nil && keyspaceSetting.ValuePlain != "" {
		keyspace = keyspaceSetting.ValuePlain
	}
	var collectionSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", job.TenantID, "astradb_product_collection").First(&collectionSetting).Error; err == nil && collectionSetting.ValuePlain != "" {
		collection = collectionSetting.ValuePlain
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

	client := &http.Client{Timeout: 180 * time.Second}
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

func getFloatVal(m map[string]interface{}, key string) (float64, bool) {
	if val, ok := m[key]; ok && val != nil {
		switch v := val.(type) {
		case float64:
			return v, true
		case float32:
			return float64(v), true
		case int:
			return float64(v), true
		case int64:
			return float64(v), true
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

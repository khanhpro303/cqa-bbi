package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
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
	endpointPath := "danhmucvattuhanghoa/search" // default fallback
	var globalPermsSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'erp_global_method_permissions'", job.TenantID).First(&globalPermsSetting).Error; err == nil && globalPermsSetting.ValuePlain != "" {
		type EndpointConfig struct {
			Get  bool   `json:"get"`
			Post bool   `json:"post"`
			Path string `json:"path"`
		}
		var globalPerms map[string]EndpointConfig
		if errUnmarshal := json.Unmarshal([]byte(globalPermsSetting.ValuePlain), &globalPerms); errUnmarshal == nil {
			if prodConfig, exists := globalPerms["products"]; exists && prodConfig.Path != "" && prodConfig.Path != "products" {
				path := prodConfig.Path
				path = strings.TrimPrefix(path, "/")
				path = strings.TrimPrefix(path, "rest_api/private/")
				path = strings.TrimPrefix(path, "/")
				endpointPath = path
			}
		}
	}

	data, err := client.SearchCustomEndpoint(endpointPath, map[string]string{
		"limit": "50000",
	})
	if err != nil {
		return a.failRun(&run, fmt.Errorf("pull ERP products from %s: %w", endpointPath, err))
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

	// 5. Clear local MySQL erp_raw_products for this tenant (full crawl table)
	if errDel := db.DB.Where("tenant_id = ?", job.TenantID).Delete(&models.ERPRawProduct{}).Error; errDel != nil {
		log.Printf("[erp_cache] warn: failed to clear erp_raw_products: %v", errDel)
	}

	// 5.1 Batch insert raw crawl rows to erp_raw_products
	rawProducts := make([]models.ERPRawProduct, 0, len(cachedProducts))
	for _, p := range cachedProducts {
		var price float64
		if val, ok := p["DON_GIA_BAN"].(float64); ok {
			price = val
		}

		now := time.Now()
		rawProducts = append(rawProducts, models.ERPRawProduct{
			ID:                 pkg.NewUUID(),
			TenantID:           job.TenantID,
			MA:                 getStringVal(p, "MA"),
			TEN_DONG_BO_WEB:    getStringVal(p, "TEN_DONG_BO_WEB"),
			TEN:                getStringVal(p, "TEN"),
			THUOC_TINH_1:       getStringVal(p, "THUOC_TINH_1"),
			THUOC_TINH_2:       getStringVal(p, "THUOC_TINH_2"),
			DON_GIA_BAN:        price,
			LINK_ANH:           getStringVal(p, "LINK_ANH"),
			NHAN_HIEU_NAME:     getStringVal(p, "NHAN_HIEU_NAME"),
			LIST_TEN_NHOM_VTHH: getStringVal(p, "LIST_TEN_NHOM_VTHH"),
			KHO:                getStringVal(p, "KHO"),
			MA_CHA:             getStringVal(p, "MA_CHA"),
			DVT:                getStringVal(p, "DVT"),
			CreatedAt:          now,
			UpdatedAt:          now,
		})
	}

	const chunkSQLSize = 100
	for i := 0; i < len(rawProducts); i += chunkSQLSize {
		end := i + chunkSQLSize
		if end > len(rawProducts) {
			end = len(rawProducts)
		}
		if errCreate := db.DB.Create(rawProducts[i:end]).Error; errCreate != nil {
			log.Printf("[erp_cache] warn: failed to batch insert to erp_raw_products: %v", errCreate)
		}
	}

	// 5.2 Rebuild AI-facing cached_products from raw, applying exclusion filter
	cachedCount, errRebuild := RebuildCachedProductsFromRaw(job.TenantID)
	if errRebuild != nil {
		log.Printf("[erp_cache] warn: failed to rebuild cached_products from raw: %v", errRebuild)
	}

	// 5.3 Sync product embeddings to Astra DB (feature-flagged). Runs async so
	//     it does not block the job-run status write below. Idempotent.
	if errRebuild == nil && os.Getenv("ERP_EMBEDDING_FUZZY_ENABLED") == "true" {
		appCfg, cfgErr := config.Load()
		if cfgErr != nil {
			log.Printf("[erp_cache] embedding sync skipped (config load error): %v", cfgErr)
		} else {
			embedCfg := ProductEmbeddingConfig{
				AstraEndpoint:   appCfg.AstraDBAPIEndpoint,
				AstraToken:      appCfg.AstraDBToken,
				AstraKeyspace:   appCfg.AstraDBKeyspace,
				AstraCollection: appCfg.AstraDBProductCollection,
			}
			go func(tenantID string, embedCfg ProductEmbeddingConfig) {
				syncCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
				defer cancel()
				if _, _, _, err := SyncProductEmbeddingsToAstraDB(syncCtx, embedCfg, tenantID); err != nil {
					log.Printf("[erp_cache] embedding sync tenant=%s error: %v", tenantID, err)
				}
			}(job.TenantID, embedCfg)
		}
	}

	finishedAt := time.Now()
	runStatus := "success"
	summaryMsg := fmt.Sprintf("Đã đồng bộ %d sản phẩm từ ERP (cache AI: %d)", len(cachedProducts), cachedCount)
	summaryJSON, _ := json.Marshal(map[string]interface{}{
		"message":                summaryMsg,
		"conversations_analyzed": len(cachedProducts),
		"conversations_found":    len(cachedProducts),
		"conversations_passed":   cachedCount,
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

// RebuildCachedProductsFromRaw rewrites the AI-facing cached_products table
// for a tenant by streaming erp_raw_products and skipping rows whose MA_CHA
// appears in erp_parent_sku_exclusions. Returns the number of cached rows
// written.
func RebuildCachedProductsFromRaw(tenantID string) (int, error) {
	// Load exclusion set
	var exclusions []models.ERPParentSKUExclusion
	if err := db.DB.Where("tenant_id = ?", tenantID).Find(&exclusions).Error; err != nil {
		return 0, fmt.Errorf("load exclusions: %w", err)
	}
	excludedSet := make(map[string]struct{}, len(exclusions))
	for _, e := range exclusions {
		excludedSet[strings.TrimSpace(e.ParentSKU)] = struct{}{}
	}

	// Clear existing cache for this tenant
	if err := db.DB.Where("tenant_id = ?", tenantID).Delete(&models.CachedProduct{}).Error; err != nil {
		return 0, fmt.Errorf("clear cached_products: %w", err)
	}

	const insertChunk = 100
	buffer := make([]models.CachedProduct, 0, insertChunk)
	inserted := 0

	flush := func() error {
		if len(buffer) == 0 {
			return nil
		}
		if err := db.DB.Create(buffer).Error; err != nil {
			return fmt.Errorf("bulk insert cached_products: %w", err)
		}
		inserted += len(buffer)
		buffer = buffer[:0]
		return nil
	}

	var rawBatch []models.ERPRawProduct
	err := db.DB.Where("tenant_id = ?", tenantID).
		FindInBatches(&rawBatch, 500, func(_ *gorm.DB, _ int) error {
			now := time.Now()
			for _, r := range rawBatch {
				if _, skip := excludedSet[strings.TrimSpace(r.MA_CHA)]; skip {
					continue
				}
				buffer = append(buffer, models.CachedProduct{
					ID:                 pkg.NewUUID(),
					TenantID:           r.TenantID,
					MA:                 r.MA,
					TEN_DONG_BO_WEB:    r.TEN_DONG_BO_WEB,
					TEN:                r.TEN,
					THUOC_TINH_1:       r.THUOC_TINH_1,
					THUOC_TINH_2:       r.THUOC_TINH_2,
					DON_GIA_BAN:        r.DON_GIA_BAN,
					LINK_ANH:           r.LINK_ANH,
					NHAN_HIEU_NAME:     r.NHAN_HIEU_NAME,
					LIST_TEN_NHOM_VTHH: r.LIST_TEN_NHOM_VTHH,
					KHO:                r.KHO,
					MA_CHA:             r.MA_CHA,
					DVT:                r.DVT,
					CreatedAt:          now,
					UpdatedAt:          now,
				})
				if len(buffer) >= insertChunk {
					if err := flush(); err != nil {
						return err
					}
				}
			}
			return nil
		}).Error
	if err != nil {
		return inserted, fmt.Errorf("stream raw products: %w", err)
	}
	if err := flush(); err != nil {
		return inserted, err
	}
	return inserted, nil
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

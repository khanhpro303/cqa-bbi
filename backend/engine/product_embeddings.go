package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

const (
	productEmbeddingsCollection = "product_embeddings"
	embeddingDimension          = 1536
	similarityThreshold         = 0.75
	astraHTTPTimeout            = 30 * time.Second
	embedAPITimeout             = 10 * time.Second
	insertManyChunk             = 20
)

// ProductEmbeddingConfig groups the embedding provider settings (used by
// BuildTenantEmbedder) with the Astra DB Data API coordinates needed to
// upsert vectors and run vector search against the product_embeddings
// collection.
type ProductEmbeddingConfig struct {
	// Embedding (forwarded to BuildTenantEmbedder).
	FallbackAPIKey string
	Model          string
	BaseURL        string
	EncryptionKey  string

	// Astra DB Data API.
	AstraEndpoint string
	AstraToken    string
	AstraKeyspace string
}

func (c ProductEmbeddingConfig) toEmbeddingConfig() EmbeddingConfig {
	return EmbeddingConfig{
		FallbackAPIKey: c.FallbackAPIKey,
		Model:          c.Model,
		BaseURL:        c.BaseURL,
		EncryptionKey:  c.EncryptionKey,
	}
}

func (c ProductEmbeddingConfig) keyspace() string {
	if c.AstraKeyspace == "" {
		return "default_keyspace"
	}
	return c.AstraKeyspace
}

func (c ProductEmbeddingConfig) configured() bool {
	return c.AstraEndpoint != "" && c.AstraToken != ""
}

// SyncProductEmbeddingsToAstraDB rebuilds the product_embeddings collection
// for a tenant. It diffs the current cached_products grouped by ma_cha
// against what Astra already stores (compared by label_hash) and:
//   - embeds + insertMany for new ma_cha
//   - embeds + findOneAndReplace when the label changed
//   - deleteMany for ma_cha that disappeared from cached_products
//
// Idempotent and safe to call after every product cache rebuild. Returns
// counts of inserted, updated, and removed rows.
func SyncProductEmbeddingsToAstraDB(ctx context.Context, cfg ProductEmbeddingConfig, tenantID string) (added, updated, removed int, err error) {
	if !cfg.configured() {
		return 0, 0, 0, fmt.Errorf("product embedding sync: astra not configured")
	}
	embedder := BuildTenantEmbedder(tenantID, cfg.toEmbeddingConfig())
	if embedder == nil {
		return 0, 0, 0, fmt.Errorf("product embedding sync: no embedding api key for tenant %s", tenantID)
	}

	if err := ensureProductEmbeddingsCollection(ctx, cfg); err != nil {
		return 0, 0, 0, fmt.Errorf("ensure collection: %w", err)
	}

	desired, err := loadDesiredProductEmbeddings(ctx, tenantID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("load desired: %w", err)
	}

	existing, err := loadExistingProductEmbeddings(ctx, cfg, tenantID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("load existing: %w", err)
	}

	var toInsert []productEmbeddingDoc
	var toReplace []productEmbeddingDoc
	var toDelete []string

	for maCha, label := range desired {
		labelHash := shortHash(label)
		if existingHash, ok := existing[maCha]; ok {
			if existingHash == labelHash {
				continue
			}
			doc, embedErr := makeProductEmbeddingDoc(ctx, embedder, tenantID, maCha, label, labelHash)
			if embedErr != nil {
				log.Printf("[product_embed] embed replace tenant=%s ma_cha=%s failed: %v", tenantID, maCha, embedErr)
				continue
			}
			toReplace = append(toReplace, doc)
		} else {
			doc, embedErr := makeProductEmbeddingDoc(ctx, embedder, tenantID, maCha, label, labelHash)
			if embedErr != nil {
				log.Printf("[product_embed] embed insert tenant=%s ma_cha=%s failed: %v", tenantID, maCha, embedErr)
				continue
			}
			toInsert = append(toInsert, doc)
		}
	}
	for maCha := range existing {
		if _, ok := desired[maCha]; !ok {
			toDelete = append(toDelete, docID(tenantID, maCha))
		}
	}

	if len(toInsert) > 0 {
		if insertErr := astraInsertMany(ctx, cfg, toInsert); insertErr != nil {
			return 0, 0, 0, fmt.Errorf("insertMany: %w", insertErr)
		}
		added = len(toInsert)
	}
	for _, doc := range toReplace {
		if replErr := astraFindOneAndReplace(ctx, cfg, doc); replErr != nil {
			log.Printf("[product_embed] replace ma_cha=%s failed: %v", doc.MaCha, replErr)
			continue
		}
		updated++
	}
	if len(toDelete) > 0 {
		n, delErr := astraDeleteMany(ctx, cfg, toDelete)
		if delErr != nil {
			log.Printf("[product_embed] deleteMany failed: %v", delErr)
		}
		removed = n
	}

	log.Printf("[product_embed] sync tenant=%s done desired=%d existing=%d added=%d updated=%d removed=%d",
		tenantID, len(desired), len(existing), added, updated, removed)
	return added, updated, removed, nil
}

// FuzzyMatchMaChaWithEmbedding embeds keyword, runs a vector search against
// the tenant's product_embeddings rows, and returns the top match's ma_cha
// when similarity is above the threshold. Returns "" (no error) when no row
// passes the threshold so callers can fallback to the LLM-list matcher.
func FuzzyMatchMaChaWithEmbedding(ctx context.Context, cfg ProductEmbeddingConfig, tenantID, keyword string) (string, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" || !cfg.configured() {
		return "", nil
	}
	embedder := BuildTenantEmbedder(tenantID, cfg.toEmbeddingConfig())
	if embedder == nil {
		return "", nil
	}

	embedCtx, cancel := context.WithTimeout(ctx, embedAPITimeout)
	defer cancel()
	vec, err := embedder.Embed(embedCtx, keyword)
	if err != nil {
		return "", fmt.Errorf("embed keyword: %w", err)
	}

	results, err := astraVectorFind(ctx, cfg, tenantID, vec, 5)
	if err != nil {
		return "", fmt.Errorf("astra vector find: %w", err)
	}
	if len(results) == 0 {
		return "", nil
	}
	top := results[0]
	if top.Similarity < similarityThreshold {
		log.Printf("[product_embed] match below threshold (%.3f < %.3f) for keyword=%q tenant=%s top_ma_cha=%s",
			top.Similarity, similarityThreshold, keyword, tenantID, top.MaCha)
		return "", nil
	}
	log.Printf("[product_embed] match keyword=%q tenant=%s ma_cha=%s similarity=%.3f",
		keyword, tenantID, top.MaCha, top.Similarity)
	return top.MaCha, nil
}

// ---------------------------------------------------------------------------
// Document + label helpers
// ---------------------------------------------------------------------------

type productEmbeddingDoc struct {
	ID        string    `json:"_id"`
	TenantID  string    `json:"tenant_id"`
	MaCha     string    `json:"ma_cha"`
	Label     string    `json:"label"`
	LabelHash string    `json:"label_hash"`
	Vector    []float32 `json:"$vector"`
	UpdatedAt int64     `json:"updated_at"`
}

type productEmbeddingMatch struct {
	MaCha      string
	Similarity float32
}

func makeProductEmbeddingDoc(ctx context.Context, embedder embedClient, tenantID, maCha, label, labelHash string) (productEmbeddingDoc, error) {
	embedCtx, cancel := context.WithTimeout(ctx, embedAPITimeout)
	defer cancel()
	vec, err := embedder.Embed(embedCtx, label)
	if err != nil {
		return productEmbeddingDoc{}, err
	}
	return productEmbeddingDoc{
		ID:        docID(tenantID, maCha),
		TenantID:  tenantID,
		MaCha:     maCha,
		Label:     label,
		LabelHash: labelHash,
		Vector:    vec,
		UpdatedAt: time.Now().Unix(),
	}, nil
}

// embedClient narrows the BuildTenantEmbedder return type to what we use,
// keeping makeProductEmbeddingDoc testable with a fake.
type embedClient interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

func loadDesiredProductEmbeddings(ctx context.Context, tenantID string) (map[string]string, error) {
	type row struct {
		MaCha        string `gorm:"column:ma_cha"`
		TenDongBoWeb string `gorm:"column:ten_dong_bo_web"`
		Ten          string `gorm:"column:ten"`
	}
	var rows []row
	err := db.DB.WithContext(ctx).
		Table((&models.CachedProduct{}).TableName()).
		Select("ma_cha, MAX(ten_dong_bo_web) AS ten_dong_bo_web, MAX(ten) AS ten").
		Where("tenant_id = ? AND ma_cha != ''", tenantID).
		Group("ma_cha").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.MaCha] = buildProductEmbeddingLabel(r.MaCha, r.TenDongBoWeb, r.Ten)
	}
	return out, nil
}

func buildProductEmbeddingLabel(maCha, tenDongBoWeb, ten string) string {
	web := strings.TrimSpace(tenDongBoWeb)
	n := strings.TrimSpace(ten)
	label := web
	if label == "" {
		label = n
	}
	if web != "" && n != "" && !strings.EqualFold(web, n) {
		label = web + " — " + n
	}
	if label == "" {
		label = maCha
	}
	return label
}

func docID(tenantID, maCha string) string {
	return tenantID + "_" + maCha
}

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8]) // 16 hex chars
}

// ---------------------------------------------------------------------------
// Astra Data API helpers
// ---------------------------------------------------------------------------

func loadExistingProductEmbeddings(ctx context.Context, cfg ProductEmbeddingConfig, tenantID string) (map[string]string, error) {
	out := make(map[string]string)
	var nextPageState string
	for {
		options := map[string]interface{}{"limit": 1000}
		if nextPageState != "" {
			options["pageState"] = nextPageState
		}
		cmd := map[string]interface{}{
			"find": map[string]interface{}{
				"filter":     map[string]interface{}{"tenant_id": tenantID},
				"projection": map[string]interface{}{"ma_cha": 1, "label_hash": 1},
				"options":    options,
			},
		}
		var resp struct {
			Data struct {
				Documents     []map[string]interface{} `json:"documents"`
				NextPageState string                   `json:"nextPageState"`
			} `json:"data"`
			Errors []map[string]interface{} `json:"errors"`
		}
		if err := astraCollectionCall(ctx, cfg, productEmbeddingsCollection, cmd, &resp); err != nil {
			return nil, err
		}
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("find errors: %v", resp.Errors)
		}
		for _, d := range resp.Data.Documents {
			maCha, _ := d["ma_cha"].(string)
			labelHash, _ := d["label_hash"].(string)
			if maCha != "" {
				out[maCha] = labelHash
			}
		}
		if resp.Data.NextPageState == "" {
			break
		}
		nextPageState = resp.Data.NextPageState
	}
	return out, nil
}

func ensureProductEmbeddingsCollection(ctx context.Context, cfg ProductEmbeddingConfig) error {
	cmd := map[string]interface{}{
		"createCollection": map[string]interface{}{
			"name": productEmbeddingsCollection,
			"options": map[string]interface{}{
				"vector": map[string]interface{}{
					"dimension": embeddingDimension,
					"metric":    "cosine",
				},
			},
		},
	}
	var resp struct {
		Status map[string]interface{}   `json:"status"`
		Errors []map[string]interface{} `json:"errors"`
	}
	if err := astraNamespaceCall(ctx, cfg, cmd, &resp); err != nil {
		return err
	}
	for _, e := range resp.Errors {
		code, _ := e["errorCode"].(string)
		msg, _ := e["message"].(string)
		if code == "EXISTING_COLLECTION" || strings.Contains(msg, "already exists") {
			return nil
		}
		return fmt.Errorf("create collection error: code=%s msg=%s", code, msg)
	}
	return nil
}

func astraInsertMany(ctx context.Context, cfg ProductEmbeddingConfig, docs []productEmbeddingDoc) error {
	for i := 0; i < len(docs); i += insertManyChunk {
		end := i + insertManyChunk
		if end > len(docs) {
			end = len(docs)
		}
		cmd := map[string]interface{}{
			"insertMany": map[string]interface{}{
				"documents": docs[i:end],
			},
		}
		var resp struct {
			Status map[string]interface{}   `json:"status"`
			Errors []map[string]interface{} `json:"errors"`
		}
		if err := astraCollectionCall(ctx, cfg, productEmbeddingsCollection, cmd, &resp); err != nil {
			return err
		}
		if len(resp.Errors) > 0 {
			return fmt.Errorf("insertMany errors: %v", resp.Errors)
		}
	}
	return nil
}

func astraFindOneAndReplace(ctx context.Context, cfg ProductEmbeddingConfig, doc productEmbeddingDoc) error {
	cmd := map[string]interface{}{
		"findOneAndReplace": map[string]interface{}{
			"filter":      map[string]interface{}{"_id": doc.ID},
			"replacement": doc,
			"options":     map[string]interface{}{"upsert": true},
		},
	}
	var resp struct {
		Errors []map[string]interface{} `json:"errors"`
	}
	if err := astraCollectionCall(ctx, cfg, productEmbeddingsCollection, cmd, &resp); err != nil {
		return err
	}
	if len(resp.Errors) > 0 {
		return fmt.Errorf("findOneAndReplace errors: %v", resp.Errors)
	}
	return nil
}

func astraDeleteMany(ctx context.Context, cfg ProductEmbeddingConfig, docIDs []string) (int, error) {
	if len(docIDs) == 0 {
		return 0, nil
	}
	cmd := map[string]interface{}{
		"deleteMany": map[string]interface{}{
			"filter": map[string]interface{}{
				"_id": map[string]interface{}{"$in": docIDs},
			},
		},
	}
	var resp struct {
		Status struct {
			DeletedCount int `json:"deletedCount"`
		} `json:"status"`
		Errors []map[string]interface{} `json:"errors"`
	}
	if err := astraCollectionCall(ctx, cfg, productEmbeddingsCollection, cmd, &resp); err != nil {
		return 0, err
	}
	if len(resp.Errors) > 0 {
		return resp.Status.DeletedCount, fmt.Errorf("deleteMany errors: %v", resp.Errors)
	}
	return resp.Status.DeletedCount, nil
}

func astraVectorFind(ctx context.Context, cfg ProductEmbeddingConfig, tenantID string, vector []float32, limit int) ([]productEmbeddingMatch, error) {
	cmd := map[string]interface{}{
		"find": map[string]interface{}{
			"filter": map[string]interface{}{"tenant_id": tenantID},
			"sort":   map[string]interface{}{"$vector": vector},
			"options": map[string]interface{}{
				"limit":             limit,
				"includeSimilarity": true,
			},
		},
	}
	var resp struct {
		Data struct {
			Documents []struct {
				MaCha      string  `json:"ma_cha"`
				Similarity float32 `json:"$similarity"`
			} `json:"documents"`
		} `json:"data"`
		Errors []map[string]interface{} `json:"errors"`
	}
	if err := astraCollectionCall(ctx, cfg, productEmbeddingsCollection, cmd, &resp); err != nil {
		return nil, err
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("vectorFind errors: %v", resp.Errors)
	}
	out := make([]productEmbeddingMatch, 0, len(resp.Data.Documents))
	for _, d := range resp.Data.Documents {
		out = append(out, productEmbeddingMatch{MaCha: d.MaCha, Similarity: d.Similarity})
	}
	return out, nil
}

func astraNamespaceCall(ctx context.Context, cfg ProductEmbeddingConfig, cmd, out interface{}) error {
	url := fmt.Sprintf("%s/api/json/v1/%s", strings.TrimRight(cfg.AstraEndpoint, "/"), cfg.keyspace())
	return astraPost(ctx, url, cfg.AstraToken, cmd, out)
}

func astraCollectionCall(ctx context.Context, cfg ProductEmbeddingConfig, collection string, cmd, out interface{}) error {
	url := fmt.Sprintf("%s/api/json/v1/%s/%s", strings.TrimRight(cfg.AstraEndpoint, "/"), cfg.keyspace(), collection)
	return astraPost(ctx, url, cfg.AstraToken, cmd, out)
}

func astraPost(ctx context.Context, url, token string, cmd, out interface{}) error {
	body, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, astraHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Token", token)

	client := &http.Client{Timeout: astraHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("astra status %d: %s", resp.StatusCode, truncateAstraBody(raw))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func truncateAstraBody(b []byte) string {
	const max = 300
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}

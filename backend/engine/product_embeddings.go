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

// product_embeddings.go — server-side vectorize variant.
//
// The Astra collection `product_embeddings` MUST be provisioned with a
// vectorize service attached (e.g. OpenAI ada-002, key paid for and held
// by Astra). Every document carries a `$vectorize` text field; Astra
// computes and stores the vector itself on insert/replace. Queries pass
// the keyword as text via `sort.$vectorize` and Astra embeds it
// server-side before running the ANN search.
//
// This side therefore does NOT need an OpenAI API key and does not call
// the embeddings API directly.

const (
	defaultEmbeddingCollection = "erp_product_bbi"
	similarityThreshold        = 0.75
	// resolutionMargin is the similarity gap between the top SKU and the next
	// SKU sharing the same ma_cha that distinguishes a pinpoint SKU query
	// (e.g. "ff800 trắng L") from a vague family query (e.g. "storm 3"). A gap
	// above this means one variant clearly dominates → answer that SKU.
	resolutionMargin = 0.04
	astraHTTPTimeout = 30 * time.Second
	insertManyChunk  = 20
)

// ProductEmbeddingConfig holds the Astra DB Data API coordinates for the
// embedding collection. The collection's vectorize service is configured on
// the Astra side, not here.
type ProductEmbeddingConfig struct {
	AstraEndpoint   string
	AstraToken      string
	AstraKeyspace   string
	AstraCollection string
}

func (c ProductEmbeddingConfig) keyspace() string {
	if c.AstraKeyspace == "" {
		return "default_keyspace"
	}
	return c.AstraKeyspace
}

func (c ProductEmbeddingConfig) collection() string {
	if c.AstraCollection == "" {
		return defaultEmbeddingCollection
	}
	return c.AstraCollection
}

func (c ProductEmbeddingConfig) configured() bool {
	return c.AstraEndpoint != "" && c.AstraToken != ""
}

// SyncProductEmbeddingsToAstraDB upserts a `$vectorize`-bearing document per
// SKU (MA) for the tenant, diffing label_hash to skip unchanged rows and
// pruning SKUs that disappeared from cached_products. Each document carries
// both MA and its parent MaCha. Astra computes the vector via the collection's
// bound vectorize service.
//
// Idempotent and safe to call after every product cache rebuild. Returns
// counts of inserted, updated, and removed rows.
func SyncProductEmbeddingsToAstraDB(ctx context.Context, cfg ProductEmbeddingConfig, tenantID string) (added, updated, removed int, err error) {
	if !cfg.configured() {
		return 0, 0, 0, fmt.Errorf("product embedding sync: astra not configured")
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

	now := time.Now().Unix()
	for ma, d := range desired {
		labelHash := shortHash(d.Label)
		doc := productEmbeddingDoc{
			ID:        docID(tenantID, ma),
			TenantID:  tenantID,
			MA:        ma,
			MaCha:     d.MaCha,
			Label:     d.Label,
			LabelHash: labelHash,
			Vectorize: d.Label,
			UpdatedAt: now,
		}
		if existingHash, ok := existing[ma]; ok {
			if existingHash != labelHash {
				toReplace = append(toReplace, doc)
			}
		} else {
			toInsert = append(toInsert, doc)
		}
	}
	for ma := range existing {
		if _, ok := desired[ma]; !ok {
			toDelete = append(toDelete, docID(tenantID, ma))
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
			log.Printf("[product_embed] replace ma=%s failed: %v", doc.MA, replErr)
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

// ProductMatch is the result of an embedding fuzzy match. MaCha is always set
// when a match passes the threshold (used for family/price_range queries).
// When Specific is true the top SKU dominates its siblings, so MA pinpoints a
// single variant (e.g. "ff800 trắng L") and callers should return just that SKU.
type ProductMatch struct {
	MA       string
	MaCha    string
	Specific bool
}

// FuzzyMatchProductWithEmbedding runs a vectorize query against the tenant's
// per-SKU product_embeddings rows. Astra embeds the keyword server-side.
//
// Returns a ProductMatch with MaCha (and MA) set when the top row passes the
// similarity threshold; returns a zero-value match (no error) otherwise so
// callers can fall back to the LLM matcher.
//
// Specific is set when the top SKU's similarity beats the next SKU *sharing the
// same ma_cha* by more than resolutionMargin — i.e. the query pinpoints one
// variant rather than asking about the whole family.
func FuzzyMatchProductWithEmbedding(ctx context.Context, cfg ProductEmbeddingConfig, tenantID, keyword string) (ProductMatch, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" || !cfg.configured() {
		return ProductMatch{}, nil
	}

	results, err := astraVectorFind(ctx, cfg, tenantID, keyword, 5)
	if err != nil {
		return ProductMatch{}, fmt.Errorf("astra vector find: %w", err)
	}
	if len(results) == 0 {
		return ProductMatch{}, nil
	}
	top := results[0]
	if top.Similarity < similarityThreshold {
		log.Printf("[product_embed] match below threshold (%.3f < %.3f) for keyword=%q tenant=%s top_ma=%s ma_cha=%s",
			top.Similarity, similarityThreshold, keyword, tenantID, top.MA, top.MaCha)
		return ProductMatch{}, nil
	}

	specific := isSpecificSKUMatch(results)
	log.Printf("[product_embed] match keyword=%q tenant=%s ma=%s ma_cha=%s similarity=%.3f specific=%v",
		keyword, tenantID, top.MA, top.MaCha, top.Similarity, specific)
	return ProductMatch{MA: top.MA, MaCha: top.MaCha, Specific: specific}, nil
}

// isSpecificSKUMatch decides whether the top SKU clearly dominates the other
// variants of its own ma_cha. If the next result that shares the top's ma_cha
// trails by more than resolutionMargin, the query pinpoints a single SKU. If no
// sibling appears in the top-K, the top SKU is unambiguous → also specific.
func isSpecificSKUMatch(results []productEmbeddingMatch) bool {
	if len(results) == 0 {
		return false
	}
	top := results[0]
	for _, r := range results[1:] {
		if r.MaCha == top.MaCha {
			return top.Similarity-r.Similarity > resolutionMargin
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Document + label helpers
// ---------------------------------------------------------------------------

type productEmbeddingDoc struct {
	ID        string `json:"_id"`
	TenantID  string `json:"tenant_id"`
	MA        string `json:"ma"`
	MaCha     string `json:"ma_cha"`
	Label     string `json:"label"`
	LabelHash string `json:"label_hash"`
	Vectorize string `json:"$vectorize"`
	UpdatedAt int64  `json:"updated_at"`
}

type productEmbeddingMatch struct {
	MA         string
	MaCha      string
	Similarity float32
}

// desiredEmbedding is the per-SKU embedding state derived from cached_products,
// keyed by MA (variant code) so each variant gets its own vectorized document.
type desiredEmbedding struct {
	MaCha string
	Label string
}

func loadDesiredProductEmbeddings(ctx context.Context, tenantID string) (map[string]desiredEmbedding, error) {
	type row struct {
		MA           string `gorm:"column:ma"`
		MaCha        string `gorm:"column:ma_cha"`
		TenDongBoWeb string `gorm:"column:ten_dong_bo_web"`
		Ten          string `gorm:"column:ten"`
		ThuocTinh1   string `gorm:"column:thuoc_tinh_1"`
		ThuocTinh2   string `gorm:"column:thuoc_tinh_2"`
	}
	var rows []row
	err := db.DB.WithContext(ctx).
		Table((&models.CachedProduct{}).TableName()).
		Select("ma, ma_cha, ten_dong_bo_web, ten, thuoc_tinh_1, thuoc_tinh_2").
		Where("tenant_id = ? AND ma != ''", tenantID).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]desiredEmbedding, len(rows))
	for _, r := range rows {
		out[r.MA] = desiredEmbedding{
			MaCha: r.MaCha,
			Label: buildProductEmbeddingLabel(r.MA, r.TenDongBoWeb, r.Ten, r.ThuocTinh1, r.ThuocTinh2),
		}
	}
	return out, nil
}

// buildProductEmbeddingLabel assembles the text fed to Astra's vectorize service
// for a single SKU. It joins the web-synced name, ERP name, and the two variant
// attributes (màu/size) so a multilingual model can match queries like
// "ff800 trắng L" against a stored "FF800 — Gloss White — L".
func buildProductEmbeddingLabel(ma, tenDongBoWeb, ten, thuocTinh1, thuocTinh2 string) string {
	parts := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	for _, raw := range []string{tenDongBoWeb, ten, thuocTinh1, thuocTinh2} {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		parts = append(parts, v)
	}
	if len(parts) == 0 {
		return ma
	}
	return strings.Join(parts, " — ")
}

func docID(tenantID, ma string) string {
	return tenantID + "_" + ma
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
				"projection": map[string]interface{}{"ma": 1, "label_hash": 1},
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
		if err := astraCollectionCall(ctx, cfg, cfg.collection(), cmd, &resp); err != nil {
			return nil, err
		}
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("find errors: %v", resp.Errors)
		}
		for _, d := range resp.Data.Documents {
			ma, _ := d["ma"].(string)
			labelHash, _ := d["label_hash"].(string)
			if ma != "" {
				out[ma] = labelHash
			}
		}
		if resp.Data.NextPageState == "" {
			break
		}
		nextPageState = resp.Data.NextPageState
	}
	return out, nil
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
		if err := astraCollectionCall(ctx, cfg, cfg.collection(), cmd, &resp); err != nil {
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
	if err := astraCollectionCall(ctx, cfg, cfg.collection(), cmd, &resp); err != nil {
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
	if err := astraCollectionCall(ctx, cfg, cfg.collection(), cmd, &resp); err != nil {
		return 0, err
	}
	if len(resp.Errors) > 0 {
		return resp.Status.DeletedCount, fmt.Errorf("deleteMany errors: %v", resp.Errors)
	}
	return resp.Status.DeletedCount, nil
}

func astraVectorFind(ctx context.Context, cfg ProductEmbeddingConfig, tenantID, keyword string, limit int) ([]productEmbeddingMatch, error) {
	cmd := map[string]interface{}{
		"find": map[string]interface{}{
			"filter": map[string]interface{}{"tenant_id": tenantID},
			"sort":   map[string]interface{}{"$vectorize": keyword},
			"options": map[string]interface{}{
				"limit":             limit,
				"includeSimilarity": true,
			},
		},
	}
	var resp struct {
		Data struct {
			Documents []struct {
				MA         string  `json:"ma"`
				MaCha      string  `json:"ma_cha"`
				Similarity float32 `json:"$similarity"`
			} `json:"documents"`
		} `json:"data"`
		Errors []map[string]interface{} `json:"errors"`
	}
	if err := astraCollectionCall(ctx, cfg, cfg.collection(), cmd, &resp); err != nil {
		return nil, err
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("vectorFind errors: %v", resp.Errors)
	}
	out := make([]productEmbeddingMatch, 0, len(resp.Data.Documents))
	for _, d := range resp.Data.Documents {
		out = append(out, productEmbeddingMatch{MA: d.MA, MaCha: d.MaCha, Similarity: d.Similarity})
	}
	return out, nil
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

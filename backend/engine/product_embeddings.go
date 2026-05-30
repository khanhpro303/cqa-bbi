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

	// embeddingSchemaVersion is folded into label_hash so a single sync run
	// replaces every existing row exactly once whenever the stored document
	// shape changes. Bumped to "v2-lexical" when the $lexical field was added
	// (BM25 only indexes rows that carry it, and the diff skips rows whose
	// hash is unchanged — so a version bump is what forces the backfill).
	// Bump this again on any future doc-shape change.
	embeddingSchemaVersion = "v2-lexical"

	// rerankFloor is the accept-vs-LLM-fallback gate, applied to the reranker's
	// score for the top result. The nvidia cross-encoder reranker emits a
	// relevance logit whose natural relevant/irrelevant boundary is ~0:
	// genuine matches score clearly positive (observed +9..+15) while
	// no-real-match queries score negative (observed −6..−13), regardless of
	// query length or language. The raw $vector cosine is NOT a usable gate —
	// off-topic queries routinely land at 0.6+ cosine — so we trust the
	// reranker's own decision boundary instead. A negative top score routes to
	// the LLM matcher (which also normalises e.g. "Storm 3" → "Storm III").
	rerankFloor = 0.0

	// siblingCosineGap is the bounded cosine gap between the top SKU and the
	// next SKU sharing its ma_cha that distinguishes a pinpoint query
	// (e.g. "ff800 trắng L") from a vague family query (e.g. "storm 3"). Same
	// idea as the old resolutionMargin but expressed on $vector cosine (a
	// stable [0,1] quantity) instead of an unbounded reranker logit. Slightly
	// looser than the old 0.04 because hybrid reranking compresses cosine
	// spreads.
	siblingCosineGap = 0.05

	// hybridLimit is the number of reranked results requested; hybridCandidates
	// is how many each leg (lexical + vector) pulls before reranking.
	hybridLimit      = 5
	hybridCandidates = 30

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
		// Fold the schema version and the lexical text into the hash so a
		// doc-shape change (e.g. adding $lexical) forces a one-time full
		// re-push of every row; NUL separators keep the concatenation
		// unambiguous. Subsequent syncs see an unchanged hash and skip.
		labelHash := shortHash(embeddingSchemaVersion + "\x00" + d.Label + "\x00" + d.Lexical)
		doc := productEmbeddingDoc{
			ID:        docID(tenantID, ma),
			TenantID:  tenantID,
			MA:        ma,
			MaCha:     d.MaCha,
			Label:     d.Label,
			LabelHash: labelHash,
			Vectorize: d.Label,
			Lexical:   d.Lexical,
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
// when a match passes the relevance floor (used for family/price_range queries).
// When Specific is true the top SKU dominates its siblings, so MA pinpoints a
// single variant (e.g. "ff800 trắng L") and callers should return just that SKU.
type ProductMatch struct {
	MA       string
	MaCha    string
	Specific bool
}

// FuzzyMatchProductWithEmbedding runs a hybrid (BM25 lexical + vector) query,
// reranked server-side, against the tenant's per-SKU product_embeddings rows.
// Astra embeds the keyword and runs lexical matching server-side.
//
// Returns a ProductMatch with MaCha (and MA) set when the top row passes the
// relevance floor (see passesRelevanceFloor); returns a zero-value match (no
// error) otherwise so callers can fall back to the LLM matcher.
//
// Specific is set when the top SKU's $vector cosine beats the next SKU *sharing
// the same ma_cha* by more than siblingCosineGap — i.e. the query pinpoints one
// variant rather than asking about the whole family.
func FuzzyMatchProductWithEmbedding(ctx context.Context, cfg ProductEmbeddingConfig, tenantID, keyword string) (ProductMatch, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" || !cfg.configured() {
		return ProductMatch{}, nil
	}

	results, err := astraHybridFindAndRerank(ctx, cfg, tenantID, keyword, hybridLimit, hybridCandidates)
	if err != nil {
		return ProductMatch{}, fmt.Errorf("astra hybrid find: %w", err)
	}
	if len(results) == 0 {
		return ProductMatch{}, nil
	}
	top := results[0]
	if !passesRelevanceFloor(top) {
		log.Printf("[product_embed] below relevance floor for keyword=%q tenant=%s top_ma=%s ma_cha=%s vector=%.3f bm25=%v",
			keyword, tenantID, top.MA, top.MaCha, top.Vector, top.HasBM25)
		return ProductMatch{}, nil
	}

	specific := isSpecificSKUMatch(results)
	log.Printf("[product_embed] match keyword=%q tenant=%s ma=%s ma_cha=%s vector=%.3f rerank=%.3f bm25=%v specific=%v",
		keyword, tenantID, top.MA, top.MaCha, top.Vector, top.Rerank, top.HasBM25, specific)
	return ProductMatch{MA: top.MA, MaCha: top.MaCha, Specific: specific}, nil
}

// passesRelevanceFloor decides whether the top hybrid result is trustworthy
// enough to answer, or whether the caller should fall back to the LLM matcher.
// It trusts the reranker's own relevance boundary: the nvidia cross-encoder
// scores genuine matches clearly positive and off-topic queries negative, so a
// top score above rerankFloor (~0) means "real match". The $vector cosine is
// deliberately NOT used as a gate — unrelated queries routinely sit at 0.6+
// cosine, so a cosine floor would wrongly accept noise.
func passesRelevanceFloor(top productEmbeddingMatch) bool {
	return top.Rerank > rerankFloor
}

// isSpecificSKUMatch decides whether the top SKU clearly dominates the other
// variants of its own ma_cha. If the first reranked result that shares the
// top's ma_cha trails by more than siblingCosineGap on the bounded $vector
// cosine, the query pinpoints a single SKU (Specific). If no sibling appears in
// the top-K, the top SKU is unambiguous → also specific. The gap is measured on
// cosine (a stable [0,1] quantity), not on the unbounded reranker logit, so the
// specific-vs-family call generalises across arbitrary queries.
func isSpecificSKUMatch(results []productEmbeddingMatch) bool {
	if len(results) == 0 {
		return false
	}
	top := results[0]
	for _, r := range results[1:] {
		if r.MaCha == top.MaCha {
			return top.Vector-r.Vector > siblingCosineGap
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
	// Lexical is the BM25-indexed text. The collection has lexical enabled, but
	// BM25 only indexes documents that carry a $lexical field — without it the
	// hybrid query degenerates to pure vector. Carries codes + label so exact
	// tokens (ma, ma_cha, colour/size words) are searchable.
	Lexical   string `json:"$lexical"`
	UpdatedAt int64  `json:"updated_at"`
}

// productEmbeddingMatch holds the per-result signals returned by the hybrid
// findAndRerank query. Rerank is the (unbounded) ordering logit; Vector is the
// bounded [0,1] cosine used as an absolute relevance floor; HasBM25 marks an
// exact lexical token hit (the strongest, scale-free signal for code queries).
type productEmbeddingMatch struct {
	MA       string
	MaCha    string
	Rerank   float64 // $rerank logit — ordering only, never a threshold
	Vector   float32 // $vector cosine 0..1 — bounded relevance floor
	BM25Rank int     // -1 when $bm25Rank is null
	HasBM25  bool    // true when $bm25Rank != null (exact token overlap)
}

// desiredEmbedding is the per-SKU embedding state derived from cached_products,
// keyed by MA (variant code) so each variant gets its own vectorized document.
type desiredEmbedding struct {
	MaCha   string
	Label   string // $vectorize text
	Lexical string // $lexical text
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
		label := buildProductEmbeddingLabel(r.MA, r.TenDongBoWeb, r.Ten, r.ThuocTinh1, r.ThuocTinh2)
		out[r.MA] = desiredEmbedding{
			MaCha:   r.MaCha,
			Label:   label,
			Lexical: buildProductLexicalText(r.MA, r.MaCha, label),
		}
	}
	return out, nil
}

// buildProductLexicalText assembles the BM25-indexed ($lexical) text for a SKU.
// Codes (ma, ma_cha) lead so exact tokens like "SP459780" and the parent code
// are directly searchable by the "standard" analyzer (which lowercases and
// splits on non-alphanumerics, so "FF800" and "ff800" both tokenize to
// "ff800"). The semantic label follows so colour/size words ("White", "L",
// "Trắng") also feed BM25 alongside the vector leg. ma_cha is skipped when it
// equals ma to avoid a duplicate token.
func buildProductLexicalText(ma, maCha, label string) string {
	parts := make([]string, 0, 3)
	if s := strings.TrimSpace(ma); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(maCha); s != "" && !strings.EqualFold(s, strings.TrimSpace(ma)) {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(label); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
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

// astraHybridFindAndRerank runs a hybrid (lexical BM25 + vector) search and
// reranks with the collection's bound reranker. Astra returns the documents in
// data.documents and the per-result scores in status.documentResponses, as two
// parallel arrays aligned by index. Results come back already ordered by
// $rerank; we keep the rerank/vector/bm25 signals so callers can make
// scale-robust decisions without depending on the unbounded rerank logit.
func astraHybridFindAndRerank(ctx context.Context, cfg ProductEmbeddingConfig, tenantID, keyword string, limit, candidates int) ([]productEmbeddingMatch, error) {
	cmd := map[string]interface{}{
		"findAndRerank": map[string]interface{}{
			"filter":     map[string]interface{}{"tenant_id": tenantID},
			"sort":       map[string]interface{}{"$hybrid": keyword},
			"projection": map[string]interface{}{"ma": 1, "ma_cha": 1},
			"options": map[string]interface{}{
				"limit":         limit,
				"hybridLimits":  candidates,
				"includeScores": true,
			},
		},
	}
	var resp struct {
		Data struct {
			Documents []struct {
				MA    string `json:"ma"`
				MaCha string `json:"ma_cha"`
			} `json:"documents"`
		} `json:"data"`
		Status struct {
			DocumentResponses []struct {
				Scores struct {
					Rerank   *float64 `json:"$rerank"`
					Vector   *float32 `json:"$vector"`
					BM25Rank *int     `json:"$bm25Rank"`
				} `json:"scores"`
			} `json:"documentResponses"`
		} `json:"status"`
		Errors []map[string]interface{} `json:"errors"`
	}
	if err := astraCollectionCall(ctx, cfg, cfg.collection(), cmd, &resp); err != nil {
		return nil, err
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("findAndRerank errors: %v", resp.Errors)
	}

	docs := resp.Data.Documents
	scores := resp.Status.DocumentResponses
	out := make([]productEmbeddingMatch, 0, len(docs))
	for i, d := range docs {
		m := productEmbeddingMatch{MA: d.MA, MaCha: d.MaCha, BM25Rank: -1}
		if i < len(scores) {
			s := scores[i].Scores
			if s.Rerank != nil {
				m.Rerank = *s.Rerank
			}
			// A lexical-only candidate (no vector leg) comes back with a large
			// negative sentinel; clamp to 0 so the cosine-gap Specific check
			// treats it as "no vector signal" rather than a real low score.
			if s.Vector != nil && *s.Vector >= 0 {
				m.Vector = *s.Vector
			}
			if s.BM25Rank != nil {
				m.BM25Rank = *s.BM25Rank
				m.HasBM25 = true
			}
		}
		out = append(out, m)
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

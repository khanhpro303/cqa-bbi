package chatmemory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// VectorDoc is one session summary in the vector index.
type VectorDoc struct {
	ID       string
	Vector   []float32
	Text     string
	Metadata map[string]string
}

// VectorHit is one search result.
type VectorHit struct {
	ID       string
	Score    float64
	Text     string
	Metadata map[string]interface{}
}

// VectorIndex stores one vector per session summary. Everything in it can be
// rebuilt from chat_sessions, so implementations are interchangeable.
type VectorIndex interface {
	Name() string
	Upsert(ctx context.Context, docs []VectorDoc) error
	// Query returns the topK nearest summaries whose metadata equals every
	// key/value in filter.
	Query(ctx context.Context, vector []float32, filter map[string]string, topK int) ([]VectorHit, error)
	Delete(ctx context.Context, ids []string) error
}

var vectorHTTP = &http.Client{Timeout: 30 * time.Second}

// ---------------------------------------------------------------- AstraDB --

// AstraIndex keeps summaries in an AstraDB Data API collection with a
// client-supplied $vector.
type AstraIndex struct {
	Endpoint   string
	Token      string
	Keyspace   string
	Collection string
	Dimensions int

	ensureMu sync.Mutex
	ensured  bool
}

func (a *AstraIndex) Name() string { return "astra" }

func (a *AstraIndex) keyspace() string {
	if a.Keyspace == "" {
		return "default_keyspace"
	}
	return a.Keyspace
}

func (a *AstraIndex) call(ctx context.Context, url string, cmd map[string]interface{}, out interface{}) error {
	body, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Token", a.Token)
	resp, err := vectorHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("astra status %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var envelope struct {
		Errors []struct {
			Message   string `json:"message"`
			ErrorCode string `json:"errorCode"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(raw, &envelope)
	if len(envelope.Errors) > 0 {
		e := envelope.Errors[0]
		return fmt.Errorf("astra error %s: %s", e.ErrorCode, e.Message)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (a *AstraIndex) collectionURL() string {
	return fmt.Sprintf("%s/api/json/v1/%s/%s", strings.TrimRight(a.Endpoint, "/"), a.keyspace(), a.Collection)
}

// ensureCollection creates the collection on first use. createCollection is
// idempotent for an existing collection with the same options.
func (a *AstraIndex) ensureCollection(ctx context.Context) error {
	a.ensureMu.Lock()
	defer a.ensureMu.Unlock()
	if a.ensured {
		return nil
	}
	dims := a.Dimensions
	if dims <= 0 {
		dims = 1536
	}
	url := fmt.Sprintf("%s/api/json/v1/%s", strings.TrimRight(a.Endpoint, "/"), a.keyspace())
	err := a.call(ctx, url, map[string]interface{}{
		"createCollection": map[string]interface{}{
			"name": a.Collection,
			"options": map[string]interface{}{
				"vector": map[string]interface{}{"dimension": dims, "metric": "cosine"},
			},
		},
	}, nil)
	if err != nil {
		return fmt.Errorf("create collection %s: %w", a.Collection, err)
	}
	a.ensured = true
	return nil
}

func (a *AstraIndex) Upsert(ctx context.Context, docs []VectorDoc) error {
	if err := a.ensureCollection(ctx); err != nil {
		return err
	}
	for _, d := range docs {
		meta := map[string]interface{}{}
		for k, v := range d.Metadata {
			meta[k] = v
		}
		err := a.call(ctx, a.collectionURL(), map[string]interface{}{
			"findOneAndReplace": map[string]interface{}{
				"filter": map[string]interface{}{"_id": d.ID},
				"replacement": map[string]interface{}{
					"_id":      d.ID,
					"$vector":  d.Vector,
					"text":     d.Text,
					"metadata": meta,
				},
				"options": map[string]interface{}{"upsert": true},
			},
		}, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *AstraIndex) Query(ctx context.Context, vector []float32, filter map[string]string, topK int) ([]VectorHit, error) {
	if topK <= 0 {
		topK = 3
	}
	f := map[string]interface{}{}
	for k, v := range filter {
		f["metadata."+k] = v
	}
	var resp struct {
		Data struct {
			Documents []struct {
				ID         string                 `json:"_id"`
				Text       string                 `json:"text"`
				Metadata   map[string]interface{} `json:"metadata"`
				Similarity float64                `json:"$similarity"`
			} `json:"documents"`
		} `json:"data"`
	}
	err := a.call(ctx, a.collectionURL(), map[string]interface{}{
		"find": map[string]interface{}{
			"filter":     f,
			"sort":       map[string]interface{}{"$vector": vector},
			"projection": map[string]interface{}{"text": 1, "metadata": 1},
			"options":    map[string]interface{}{"limit": topK, "includeSimilarity": true},
		},
	}, &resp)
	if err != nil {
		return nil, err
	}
	hits := make([]VectorHit, 0, len(resp.Data.Documents))
	for _, d := range resp.Data.Documents {
		hits = append(hits, VectorHit{ID: d.ID, Score: d.Similarity, Text: d.Text, Metadata: d.Metadata})
	}
	return hits, nil
}

func (a *AstraIndex) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return a.call(ctx, a.collectionURL(), map[string]interface{}{
		"deleteMany": map[string]interface{}{
			"filter": map[string]interface{}{"_id": map[string]interface{}{"$in": ids}},
		},
	}, nil)
}

// ------------------------------------------------------ Cloudflare Vectorize --

// VectorizeIndex keeps summaries in a Cloudflare Vectorize index through the
// REST API. Create it once with the same dimensions as the embedding model
// and metadata indexes for the filtered keys, e.g.:
//
//	wrangler vectorize create zalo-session-summaries --dimensions=1536 --metric=cosine
//	wrangler vectorize create-metadata-index zalo-session-summaries --property-name=tenant_id --type=string
//	wrangler vectorize create-metadata-index zalo-session-summaries --property-name=subject_id --type=string
type VectorizeIndex struct {
	AccountID string
	APIToken  string
	Index     string
	// BaseURL overrides https://api.cloudflare.com/client/v4 (tests).
	BaseURL string
}

func (v *VectorizeIndex) Name() string { return "vectorize" }

func (v *VectorizeIndex) url(op string) string {
	base := v.BaseURL
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	return fmt.Sprintf("%s/accounts/%s/vectorize/v2/indexes/%s/%s", strings.TrimRight(base, "/"), v.AccountID, v.Index, op)
}

func (v *VectorizeIndex) do(ctx context.Context, op, contentType string, body []byte, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.url(op), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+v.APIToken)
	req.Header.Set("Content-Type", contentType)
	resp, err := vectorHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var envelope struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("vectorize %s status %d: %s", op, resp.StatusCode, truncate(string(raw), 300))
	}
	if resp.StatusCode >= 300 || !envelope.Success {
		if len(envelope.Errors) > 0 {
			return fmt.Errorf("vectorize %s error %d: %s", op, envelope.Errors[0].Code, envelope.Errors[0].Message)
		}
		return fmt.Errorf("vectorize %s status %d", op, resp.StatusCode)
	}
	if out != nil && len(envelope.Result) > 0 {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

func (v *VectorizeIndex) Upsert(ctx context.Context, docs []VectorDoc) error {
	if len(docs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, d := range docs {
		meta := map[string]interface{}{}
		for k, val := range d.Metadata {
			meta[k] = val
		}
		// Vectorize metadata is capped at 10 KiB per vector.
		meta["text"] = truncate(d.Text, 4000)
		line, err := json.Marshal(map[string]interface{}{"id": d.ID, "values": d.Vector, "metadata": meta})
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return v.do(ctx, "upsert", "application/x-ndjson", buf.Bytes(), nil)
}

func (v *VectorizeIndex) Query(ctx context.Context, vector []float32, filter map[string]string, topK int) ([]VectorHit, error) {
	if topK <= 0 {
		topK = 3
	}
	req := map[string]interface{}{"vector": vector, "topK": topK, "returnMetadata": "all"}
	if len(filter) > 0 {
		f := map[string]interface{}{}
		for k, val := range filter {
			f[k] = val
		}
		req["filter"] = f
	}
	body, _ := json.Marshal(req)
	var result struct {
		Matches []struct {
			ID       string                 `json:"id"`
			Score    float64                `json:"score"`
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"matches"`
	}
	if err := v.do(ctx, "query", "application/json", body, &result); err != nil {
		return nil, err
	}
	hits := make([]VectorHit, 0, len(result.Matches))
	for _, m := range result.Matches {
		text, _ := m.Metadata["text"].(string)
		hits = append(hits, VectorHit{ID: m.ID, Score: m.Score, Text: text, Metadata: m.Metadata})
	}
	return hits, nil
}

func (v *VectorizeIndex) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	body, _ := json.Marshal(map[string]interface{}{"ids": ids})
	return v.do(ctx, "delete_by_ids", "application/json", body, nil)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

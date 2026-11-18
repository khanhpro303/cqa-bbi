package chatmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/channels/zalotoken"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

const (
	summarizeBatch       = 20
	maxSummaryAttempts   = 3
	maxTranscriptMsgs    = 200
	maxTranscriptChars   = 14000
	maxMessageChars      = 600
	maxCardRunes         = 500
	staleClaimAfter      = 30 * time.Minute
	vectorDeleteBatch    = 100
	promptContextMaxDays = 180
)

// Service runs the summarizer, the vector indexer and vector retention.
type Service struct {
	cfg   *config.Config
	index VectorIndex
	// summarize is the LLM call; replaced in tests.
	summarize func(ctx context.Context, tenantID, system, input string) (ai.AIResponse, string, string, error)
	// embed computes a summary vector; replaced in tests.
	embed func(ctx context.Context, tenantID, text string) ([]float32, error)
}

// NewService builds the service from config. The vector index is nil when no
// store is configured; summaries are still produced.
func NewService(cfg *config.Config) *Service {
	s := &Service{cfg: cfg, index: NewVectorIndex(cfg)}
	s.summarize = s.callTenantLLM
	s.embed = s.embedText
	return s
}

// NewVectorIndex returns the configured summary index, or nil.
func NewVectorIndex(cfg *config.Config) VectorIndex {
	mc := cfg.ChatMemory
	store := mc.VectorStore
	if store == "" {
		if cfg.AstraDBAPIEndpoint != "" && cfg.AstraDBToken != "" {
			store = "astra"
		} else {
			store = "none"
		}
	}
	switch store {
	case "astra":
		if cfg.AstraDBAPIEndpoint == "" || cfg.AstraDBToken == "" {
			log.Printf("[chat-memory] vector store astra selected but ASTRA_DB_* is not configured; index disabled")
			return nil
		}
		return &AstraIndex{
			Endpoint: cfg.AstraDBAPIEndpoint, Token: cfg.AstraDBToken, Keyspace: cfg.AstraDBKeyspace,
			Collection: mc.AstraCollection, Dimensions: mc.EmbeddingDimensions,
		}
	case "vectorize":
		if mc.VectorizeAccountID == "" || mc.VectorizeAPIToken == "" || mc.VectorizeIndex == "" {
			log.Printf("[chat-memory] vector store vectorize selected but CHAT_MEMORY_VECTORIZE_* is incomplete; index disabled")
			return nil
		}
		return &VectorizeIndex{AccountID: mc.VectorizeAccountID, APIToken: mc.VectorizeAPIToken, Index: mc.VectorizeIndex}
	case "none":
		return nil
	default:
		log.Printf("[chat-memory] unknown CHAT_MEMORY_VECTOR_STORE %q; index disabled", store)
		return nil
	}
}

// Index exposes the configured vector index (nil when none).
func (s *Service) Index() VectorIndex { return s.index }

// Start runs the background loops until ctx is cancelled.
func (s *Service) Start(ctx context.Context) {
	if !s.cfg.ChatMemory.Enabled {
		log.Printf("[chat-memory] disabled (CHAT_MEMORY_ENABLED=false)")
		return
	}
	indexName := "none"
	if s.index != nil {
		indexName = s.index.Name()
	}
	log.Printf("[chat-memory] started: idle=%s vector_store=%s retention=%dd profile_in_prompt=%v",
		s.idleThreshold(), indexName, s.cfg.ChatMemory.VectorRetentionDays, s.cfg.ChatMemory.ProfileInPrompt)
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		lastRetention := time.Time{}
		for {
			s.RunOnce(ctx)
			if time.Since(lastRetention) > 24*time.Hour {
				s.ApplyVectorRetention(ctx)
				lastRetention = time.Now()
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *Service) idleThreshold() time.Duration {
	if m := s.cfg.ChatMemory.SummarizeIdleMinutes; m > 0 {
		return time.Duration(m) * time.Minute
	}
	timeout := s.cfg.ChatbotSessionTimeout
	if timeout <= 0 {
		timeout = 30
	}
	return time.Duration(timeout+10) * time.Minute
}

// RunOnce summarizes due sessions and indexes summaries not yet indexed.
func (s *Service) RunOnce(ctx context.Context) {
	if db.DB == nil {
		return
	}
	cutoff := time.Now().Add(-s.idleThreshold())
	var due []models.ChatSession
	err := db.DB.WithContext(ctx).
		Where("(status = 'open' AND last_activity_at < ?) OR (status = 'summarizing' AND updated_at < ?)", cutoff, time.Now().Add(-staleClaimAfter)).
		Where("attempts < ?", maxSummaryAttempts).
		Order("last_activity_at ASC").Limit(summarizeBatch).Find(&due).Error
	if err != nil {
		log.Printf("[chat-memory] list due sessions: %v", err)
		return
	}
	for _, sess := range due {
		if ctx.Err() != nil {
			return
		}
		if !claim(ctx, sess) {
			continue
		}
		if err := s.SummarizeSession(ctx, sess.ID); err != nil {
			log.Printf("[chat-memory] summarize session %s: %v", sess.ID, err)
		}
	}
	s.indexPending(ctx)
}

func claim(ctx context.Context, sess models.ChatSession) bool {
	res := db.DB.WithContext(ctx).Model(&models.ChatSession{}).
		Where("id = ? AND status = ? AND updated_at = ?", sess.ID, sess.Status, sess.UpdatedAt).
		Updates(map[string]interface{}{"status": "summarizing", "attempts": gorm.Expr("attempts + 1"), "updated_at": time.Now()})
	return res.Error == nil && res.RowsAffected == 1
}

// SummaryResult is the JSON the summarizer asks the model for.
type SummaryResult struct {
	Summary   string           `json:"summary"`
	Intents   []string         `json:"intents"`
	Products  []string         `json:"products"`
	Outcome   string           `json:"outcome"`
	Sentiment string           `json:"sentiment"`
	Issues    []SummaryIssue   `json:"issues"`
	Profile   *json.RawMessage `json:"profile"`
	Card      string           `json:"card"`
}

// SummaryIssue is one problem found in a session.
type SummaryIssue struct {
	Type     string `json:"type"`
	Severity string `json:"severity"`
	Detail   string `json:"detail"`
	Evidence string `json:"evidence"`
}

// SummarizeSession summarizes one claimed session and updates its profile.
func (s *Service) SummarizeSession(ctx context.Context, sessionID string) error {
	var sess models.ChatSession
	if err := db.DB.WithContext(ctx).Where("id = ?", sessionID).First(&sess).Error; err != nil {
		return err
	}
	fail := func(err error) error {
		status := "open"
		if sess.Attempts+1 >= maxSummaryAttempts {
			status = "failed"
		}
		db.DB.Model(&models.ChatSession{}).Where("id = ?", sess.ID).Updates(map[string]interface{}{
			"status": status, "summary_error": truncate(err.Error(), 1000), "updated_at": time.Now(),
		})
		return err
	}

	msgs, err := sessionMessages(ctx, sess)
	if err != nil {
		return fail(err)
	}
	if customerTurns(msgs) < 2 {
		// Only the trigger word (or nothing): nothing to learn from.
		db.DB.Model(&models.ChatSession{}).Where("id = ?", sess.ID).Updates(map[string]interface{}{
			"status": "skipped", "updated_at": time.Now(),
		})
		return nil
	}

	var profile models.ChatCustomerProfile
	hasProfile := db.DB.WithContext(ctx).Where("tenant_id = ? AND channel_id = ? AND subject_type = ? AND subject_id = ?",
		sess.TenantID, sess.ChannelID, sess.SubjectType, sess.SubjectID).First(&profile).Error == nil

	loc := tenantLocation(sess.TenantID)
	input := BuildSummaryInput(sess, profile.Profile, msgs, loc)
	resp, provider, model, err := s.summarize(ctx, sess.TenantID, summarySystemPrompt, input)
	if err != nil {
		return fail(err)
	}
	result, err := ParseSummaryResult(resp.Content)
	if err != nil {
		return fail(err)
	}
	logUsage(sess, provider, model, resp)

	now := time.Now()
	intents, _ := json.Marshal(nonNil(result.Intents))
	products, _ := json.Marshal(nonNil(result.Products))
	issues, _ := json.Marshal(nonNilIssues(result.Issues))
	if err := db.DB.Model(&models.ChatSession{}).Where("id = ?", sess.ID).Updates(map[string]interface{}{
		"status": "summarized", "summarized_at": now, "summary": result.Summary,
		"outcome": truncate(result.Outcome, 30), "sentiment": truncate(result.Sentiment, 20),
		"intents": string(intents), "products": string(products), "issues": string(issues),
		"summary_model": model, "summary_error": "", "updated_at": now,
	}).Error; err != nil {
		return err
	}

	if err := upsertProfile(ctx, sess, profile, hasProfile, result, model); err != nil {
		log.Printf("[chat-memory] profile update for session %s: %v", sess.ID, err)
	}

	if s.cfg.ChatMemory.IssueAlerts && sess.AlertedAt == nil {
		s.alertIssues(ctx, sess, result)
	}
	return nil
}

func sessionMessages(ctx context.Context, sess models.ChatSession) ([]models.Message, error) {
	if sess.ConversationID == "" {
		return nil, errors.New("session has no conversation")
	}
	var msgs []models.Message
	err := db.DB.WithContext(ctx).
		Select("id, sender_type, sender_name, content, content_type, sent_at, raw_data").
		Where("tenant_id = ? AND conversation_id = ? AND sent_at BETWEEN ? AND ?",
			sess.TenantID, sess.ConversationID, sess.StartedAt.Add(-time.Minute), sess.LastActivityAt.Add(time.Minute)).
		Order("sent_at ASC, id ASC").Limit(maxTranscriptMsgs).Find(&msgs).Error
	return msgs, err
}

func customerTurns(msgs []models.Message) int {
	n := 0
	for _, m := range msgs {
		if m.SenderType == "customer" && strings.TrimSpace(m.Content) != "" {
			n++
		}
	}
	return n
}

// speakerLabel tells customer, chatbot and staff apart for the transcript.
func speakerLabel(m models.Message) string {
	if m.SenderType == "customer" {
		if m.SenderName != "" {
			return "Khách (" + m.SenderName + ")"
		}
		return "Khách"
	}
	if strings.Contains(m.RawData, `"cqa_source": "chatbot"`) || strings.Contains(m.RawData, `"cqa_source":"chatbot"`) {
		return "Bot"
	}
	return "Nhân viên"
}

// BuildSummaryInput renders the model input: the current profile and the
// session transcript, newest messages kept when it is too long.
func BuildSummaryInput(sess models.ChatSession, currentProfile string, msgs []models.Message, loc *time.Location) string {
	var lines []string
	for _, m := range msgs {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		if m.ContentType == "template" {
			text = "[tin dạng thẻ/danh sách] " + text
		}
		text = strings.ReplaceAll(truncate(text, maxMessageChars), "\n", " ")
		lines = append(lines, fmt.Sprintf("[%s] %s: %s", m.SentAt.In(loc).Format("02/01 15:04"), speakerLabel(m), text))
	}
	total := 0
	start := len(lines)
	for start > 0 && total+len(lines[start-1]) < maxTranscriptChars {
		start--
		total += len(lines[start])
	}
	lines = lines[start:]

	var b strings.Builder
	b.WriteString("LOẠI CHAT: ")
	if sess.SubjectType == "group" {
		b.WriteString("nhóm Zalo")
		if sess.CustomerName != "" {
			b.WriteString(" \"" + sess.CustomerName + "\"")
		}
	} else {
		b.WriteString("chat riêng 1-1")
	}
	if sess.AgentType == "private" {
		b.WriteString(" (bot nội bộ cho nhân viên)")
	}
	b.WriteString("\n\nHỒ SƠ HIỆN TẠI:\n")
	if strings.TrimSpace(currentProfile) == "" || currentProfile == "null" {
		b.WriteString("(chưa có)")
	} else {
		b.WriteString(currentProfile)
	}
	b.WriteString("\n\nPHIÊN CHAT:\n")
	b.WriteString(strings.Join(lines, "\n"))
	return b.String()
}

const summarySystemPrompt = `Bạn phân tích một phiên chat Zalo giữa khách hàng với chatbot (và đôi khi nhân viên) của một doanh nghiệp bán hàng. Trả về DUY NHẤT một đối tượng JSON theo đúng mẫu:
{
  "summary": "2-4 câu: khách hỏi gì, bot/nhân viên trả lời ra sao, kết quả",
  "intents": ["ý định chính, ví dụ: hỏi tồn kho, hỏi giá, tra đơn, tra công nợ, khiếu nại"],
  "products": ["mã hoặc tên sản phẩm được nhắc tới"],
  "outcome": "answered | partially_answered | unanswered | handover | order_intent | abandoned | other",
  "sentiment": "positive | neutral | negative",
  "issues": [{"type": "complaint | delivery | product_defect | price | payment | bot_failure | staff_request | other", "severity": "low | medium | high", "detail": "mô tả ngắn", "evidence": "trích nguyên văn lời khách, tối đa 200 ký tự"}],
  "profile": {
    "address_form": "cách khách xưng hô và muốn được gọi",
    "communication_style": "ngắn gọn/chi tiết, trang trọng/thân mật, hay dùng viết tắt...",
    "active_hours": "khung giờ hay nhắn nếu thấy rõ",
    "interests": ["dòng sản phẩm, nhu cầu thường hỏi"],
    "price_behavior": "hay hỏi giá, hay xin chiết khấu, nhạy cảm giá...",
    "service_notes": ["lưu ý khi phục vụ khách này"],
    "risk_flags": ["dấu hiệu cần chú ý: hay khiếu nại, công nợ quá hạn được nhắc tới..."]
  },
  "card": "tối đa 500 ký tự, viết cho chatbot đọc trước khi trả lời khách này"
}

Quy tắc:
- Chỉ dựa trên phiên chat và hồ sơ hiện tại. Không bịa. Mục nào không có thông tin thì để chuỗi rỗng hoặc mảng rỗng.
- "profile" là hồ sơ ĐÃ CẬP NHẬT: giữ các nhận định cũ còn đúng, bổ sung điều mới, bỏ điều phiên này cho thấy đã sai.
- Không suy đoán sức khoẻ, tôn giáo, chính trị, đời tư, giới tính. Không ghi số điện thoại, địa chỉ, số tài khoản, mật khẩu vào "profile" và "card". Không dùng từ ngữ chê bai khách.
- "issues" chỉ gồm vấn đề cần người xử lý: khiếu nại, hàng lỗi, giao sai hoặc chậm, sai tiền hoặc công nợ, bot trả lời sai hoặc không trả lời được câu quan trọng, khách đòi gặp nhân viên. Không có thì để [].
- severity "high" khi khách bức xúc, có nguy cơ mất đơn hoặc sai tiền, hoặc cần xử lý trong ngày; "medium" khi cần theo dõi; "low" cho góp ý nhỏ.
- "card" giọng trung tính, gồm cách xưng hô nên dùng, khách hay quan tâm gì, lưu ý khi trả lời. Không nhắc lại nội dung riêng tư.
- Với chat nhóm, hồ sơ là của cả nhóm (cửa hàng), không phải của một người.
- Viết bằng tiếng Việt. CHỈ trả về JSON.`

// ParseSummaryResult extracts and validates the model's JSON answer.
func ParseSummaryResult(content string) (SummaryResult, error) {
	var r SummaryResult
	body := strings.TrimSpace(content)
	if i := strings.Index(body, "{"); i >= 0 {
		if j := strings.LastIndex(body, "}"); j > i {
			body = body[i : j+1]
		}
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		return r, fmt.Errorf("summary is not valid JSON: %w", err)
	}
	r.Summary = strings.TrimSpace(r.Summary)
	if r.Summary == "" {
		return r, errors.New("summary is empty")
	}
	r.Card = truncate(strings.TrimSpace(r.Card), maxCardRunes)
	kept := r.Issues[:0]
	for _, is := range r.Issues {
		is.Severity = strings.ToLower(strings.TrimSpace(is.Severity))
		if strings.TrimSpace(is.Detail) == "" {
			continue
		}
		kept = append(kept, is)
	}
	r.Issues = kept
	return r, nil
}

func upsertProfile(ctx context.Context, sess models.ChatSession, existing models.ChatCustomerProfile, hasProfile bool, r SummaryResult, model string) error {
	profileJSON := existing.Profile
	if r.Profile != nil && len(*r.Profile) > 0 && string(*r.Profile) != "null" {
		profileJSON = string(*r.Profile)
	}
	if strings.TrimSpace(profileJSON) == "" {
		profileJSON = "{}"
	}
	card := r.Card
	if card == "" {
		card = existing.Card
	}
	now := time.Now()
	last := sess.LastActivityAt
	row := models.ChatCustomerProfile{
		ID:            pkg.NewUUID(),
		TenantID:      sess.TenantID,
		ChannelID:     sess.ChannelID,
		SubjectType:   sess.SubjectType,
		SubjectID:     sess.SubjectID,
		CustomerName:  sess.CustomerName,
		CustomerCode:  sess.CustomerCode,
		Profile:       profileJSON,
		Card:          card,
		SessionsCount: 1,
		LastSessionID: sess.ID,
		LastSessionAt: &last,
		Model:         model,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if hasProfile && existing.CustomerName != "" && row.CustomerName == "" {
		row.CustomerName = existing.CustomerName
	}
	if hasProfile && existing.CustomerCode != "" && row.CustomerCode == "" {
		row.CustomerCode = existing.CustomerCode
	}
	return db.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "channel_id"}, {Name: "subject_type"}, {Name: "subject_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"profile": profileJSON, "card": card, "customer_name": row.CustomerName, "customer_code": row.CustomerCode,
			"sessions_count": gorm.Expr("sessions_count + 1"), "last_session_id": sess.ID, "last_session_at": last,
			"model": model, "updated_at": now,
		}),
	}).Create(&row).Error
}

func (s *Service) alertIssues(ctx context.Context, sess models.ChatSession, r SummaryResult) {
	var serious []engine.SessionIssue
	for _, is := range r.Issues {
		if is.Severity == "high" {
			serious = append(serious, engine.SessionIssue{Type: is.Type, Severity: is.Severity, Detail: is.Detail, Evidence: is.Evidence})
		}
	}
	if len(serious) == 0 {
		return
	}
	var ch models.Channel
	if err := db.DB.WithContext(ctx).Where("id = ?", sess.ChannelID).First(&ch).Error; err != nil {
		log.Printf("[chat-memory] alert: load channel %s: %v", sess.ChannelID, err)
		return
	}
	adapter, err := channelAdapter(ch, s.cfg.EncryptionKey)
	if err != nil {
		log.Printf("[chat-memory] alert: adapter for channel %s: %v", ch.ID, err)
		return
	}
	groupName := ""
	if sess.SubjectType == "group" {
		groupName = sess.CustomerName
	}
	sent := engine.NotifySessionIssues(ctx, adapter, engine.SessionIssueAlert{
		TenantID: sess.TenantID, Channel: ch, SessionID: sess.ID, ZaloUserID: sess.ZaloUserID,
		CustomerName: sess.CustomerName, CustomerCode: sess.CustomerCode, ChatGroup: groupName,
		Summary: r.Summary, Issues: serious,
	})
	if sent {
		now := time.Now()
		db.DB.Model(&models.ChatSession{}).Where("id = ?", sess.ID).Update("alerted_at", now)
	}
}

func channelAdapter(ch models.Channel, encryptionKey string) (*channels.ZaloOAAdapter, error) {
	credBytes, err := pkg.Decrypt(ch.CredentialsEncrypted, encryptionKey)
	if err != nil {
		return nil, err
	}
	var creds channels.ZaloOACredentials
	if err := json.Unmarshal(credBytes, &creds); err != nil {
		return nil, err
	}
	return zalotoken.NewAdapter(ch.ID, creds, encryptionKey), nil
}

// indexPending embeds and indexes summaries that are not in the index yet.
// Setting indexed_at back to NULL re-indexes a session, which is how the
// index is rebuilt after switching CHAT_MEMORY_VECTOR_STORE.
func (s *Service) indexPending(ctx context.Context) {
	if s.index == nil {
		return
	}
	q := db.DB.WithContext(ctx).Where("status = 'summarized' AND indexed_at IS NULL")
	if days := s.cfg.ChatMemory.VectorRetentionDays; days > 0 {
		q = q.Where("summarized_at > ?", time.Now().AddDate(0, 0, -days))
	}
	var pending []models.ChatSession
	if err := q.Order("summarized_at ASC").Limit(summarizeBatch).Find(&pending).Error; err != nil {
		log.Printf("[chat-memory] list unindexed sessions: %v", err)
		return
	}
	for _, sess := range pending {
		text := SummaryIndexText(sess)
		vec, err := s.embed(ctx, sess.TenantID, text)
		if err != nil {
			log.Printf("[chat-memory] embed session %s: %v", sess.ID, err)
			return // embedding endpoint down: try the batch again next run
		}
		doc := VectorDoc{ID: sess.ID, Vector: vec, Text: text, Metadata: map[string]string{
			"tenant_id": sess.TenantID, "channel_id": sess.ChannelID, "subject_type": sess.SubjectType,
			"subject_id": sess.SubjectID, "session_id": sess.ID, "summarized_at": sess.SummarizedAt.UTC().Format(time.RFC3339),
		}}
		if err := s.index.Upsert(ctx, []VectorDoc{doc}); err != nil {
			log.Printf("[chat-memory] index session %s in %s: %v", sess.ID, s.index.Name(), err)
			return
		}
		now := time.Now()
		db.DB.Model(&models.ChatSession{}).Where("id = ?", sess.ID).Updates(map[string]interface{}{
			"vector_id": sess.ID, "indexed_at": now, "vector_deleted_at": nil,
		})
	}
}

// SummaryIndexText is the text embedded for a session.
func SummaryIndexText(sess models.ChatSession) string {
	var b strings.Builder
	b.WriteString(sess.Summary)
	var list []string
	if json.Unmarshal([]byte(sess.Intents), &list) == nil && len(list) > 0 {
		b.WriteString("\nÝ định: " + strings.Join(list, ", "))
	}
	list = nil
	if json.Unmarshal([]byte(sess.Products), &list) == nil && len(list) > 0 {
		b.WriteString("\nSản phẩm: " + strings.Join(list, ", "))
	}
	if sess.Outcome != "" {
		b.WriteString("\nKết quả: " + sess.Outcome)
	}
	return b.String()
}

// ApplyVectorRetention removes summary vectors older than the retention
// period from the index. The summaries stay in MySQL.
func (s *Service) ApplyVectorRetention(ctx context.Context) {
	days := s.cfg.ChatMemory.VectorRetentionDays
	if s.index == nil || days <= 0 || db.DB == nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	for {
		var old []models.ChatSession
		if err := db.DB.WithContext(ctx).Select("id, vector_id").
			Where("indexed_at IS NOT NULL AND vector_deleted_at IS NULL AND indexed_at < ?", cutoff).
			Limit(vectorDeleteBatch).Find(&old).Error; err != nil || len(old) == 0 {
			return
		}
		ids := make([]string, 0, len(old))
		sessionIDs := make([]string, 0, len(old))
		for _, o := range old {
			sessionIDs = append(sessionIDs, o.ID)
			if o.VectorID != "" {
				ids = append(ids, o.VectorID)
			}
		}
		if err := s.index.Delete(ctx, ids); err != nil {
			log.Printf("[chat-memory] vector retention delete: %v", err)
			return
		}
		db.DB.Model(&models.ChatSession{}).Where("id IN ?", sessionIDs).Update("vector_deleted_at", time.Now())
		log.Printf("[chat-memory] removed %d summary vectors older than %d days", len(ids), days)
		if len(old) < vectorDeleteBatch {
			return
		}
	}
}

// PromptContext returns what the chatbot may know about this chat from past
// sessions: the profile card and the latest session summary. Empty when
// nothing is known.
func PromptContext(ctx context.Context, tenantID, channelID, subjectType, subjectID string) string {
	if db.DB == nil || subjectID == "" {
		return ""
	}
	var p models.ChatCustomerProfile
	hasCard := db.DB.WithContext(ctx).Where("tenant_id = ? AND channel_id = ? AND subject_type = ? AND subject_id = ?",
		tenantID, channelID, subjectType, subjectID).First(&p).Error == nil && strings.TrimSpace(p.Card) != ""
	var last models.ChatSession
	hasLast := db.DB.WithContext(ctx).Where("tenant_id = ? AND channel_id = ? AND subject_type = ? AND subject_id = ? AND status = 'summarized' AND summarized_at > ?",
		tenantID, channelID, subjectType, subjectID, time.Now().AddDate(0, 0, -promptContextMaxDays)).
		Order("last_activity_at DESC").First(&last).Error == nil
	if !hasCard && !hasLast {
		return ""
	}
	var b strings.Builder
	b.WriteString("THÔNG TIN TỪ CÁC LẦN TRÒ CHUYỆN TRƯỚC (chỉ để tham khảo cách phục vụ; không đọc lại cho khách, dữ liệu tồn kho/giá/đơn/công nợ luôn phải tra bằng công cụ):\n")
	if hasCard {
		b.WriteString(truncate(p.Card, maxCardRunes))
		b.WriteString("\n")
	}
	if hasLast {
		b.WriteString(fmt.Sprintf("Lần gần nhất (%s): %s", last.LastActivityAt.In(tenantLocation(tenantID)).Format("02/01/2006"), truncate(last.Summary, 400)))
	}
	return strings.TrimSpace(b.String())
}

func (s *Service) callTenantLLM(ctx context.Context, tenantID, system, input string) (ai.AIResponse, string, string, error) {
	resolved, err := engine.ResolveTenantAIProvider(tenantID, s.cfg)
	if err != nil {
		return ai.AIResponse{}, "", "", err
	}
	if resolved.Client == nil {
		return ai.AIResponse{}, "", "", errors.New("no AI API key configured for tenant")
	}
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var resp ai.AIResponse
	if jp, ok := resolved.Client.(ai.JSONProvider); ok {
		resp, err = jp.AnalyzeJSON(callCtx, system, input)
	} else {
		resp, err = resolved.Client.AnalyzeChat(callCtx, system, input)
	}
	provider, model := resolved.Provider, resolved.Model
	if resp.Provider != "" {
		provider = resp.Provider
	}
	if resp.Model != "" {
		model = resp.Model
	}
	return resp, provider, model, err
}

func (s *Service) embedText(ctx context.Context, tenantID, text string) ([]float32, error) {
	mc := s.cfg.ChatMemory
	key := mc.EmbeddingAPIKey
	if key == "" && strings.Contains(mc.EmbeddingBaseURL, "cloudflare.com") {
		key = mc.VectorizeAPIToken
	}
	var client *ai.EmbeddingsClient
	if key != "" {
		client = ai.NewEmbeddingsClient(key, mc.EmbeddingModel, mc.EmbeddingBaseURL)
	} else {
		client = engine.BuildTenantEmbedder(tenantID, engine.EmbeddingConfig{
			FallbackAPIKey: s.cfg.LangflowEmbeddingAPIKey,
			Model:          mc.EmbeddingModel,
			BaseURL:        mc.EmbeddingBaseURL,
			EncryptionKey:  s.cfg.EncryptionKey,
		})
	}
	if client == nil {
		return nil, errors.New("no embedding API key configured")
	}
	vec, err := client.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	if mc.EmbeddingDimensions > 0 && len(vec) != mc.EmbeddingDimensions {
		return nil, fmt.Errorf("embedding has %d dimensions, CHAT_MEMORY_EMBEDDING_DIMENSIONS is %d", len(vec), mc.EmbeddingDimensions)
	}
	return vec, nil
}

func logUsage(sess models.ChatSession, provider, model string, resp ai.AIResponse) {
	if resp.InputTokens == 0 && resp.OutputTokens == 0 {
		return
	}
	db.DB.Create(&models.AIUsageLog{
		ID:               pkg.NewUUID(),
		TenantID:         sess.TenantID,
		Source:           "job",
		ChannelID:        sess.ChannelID,
		SenderExternalID: sess.ZaloUserID,
		SenderName:       sess.CustomerName,
		Provider:         provider,
		Model:            model,
		InputTokens:      resp.InputTokens,
		OutputTokens:     resp.OutputTokens,
		CostUSD:          ai.CalculateCostUSD(provider, model, resp.InputTokens, resp.OutputTokens),
		CreatedAt:        time.Now(),
	})
}

func tenantLocation(tenantID string) *time.Location {
	loc, err := time.LoadLocation(engine.TenantTimezone(tenantID))
	if err != nil || loc == nil {
		return time.FixedZone("ICT", 7*3600)
	}
	return loc
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilIssues(v []SummaryIssue) []SummaryIssue {
	if v == nil {
		return []SummaryIssue{}
	}
	return v
}

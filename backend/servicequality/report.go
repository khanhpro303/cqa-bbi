package servicequality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

const PolicyKey = "messenger_service_policy"

var ErrReportTooLarge = errors.New("báo cáo vượt giới hạn 5.000 hội thoại hoặc 100.000 tin nhắn; hãy lọc một Fanpage. Nếu một Fanpage vẫn vượt giới hạn, cần bổ sung tổng hợp dữ liệu trước khi báo cáo")

type Page struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	LastSyncAt     *time.Time `json:"last_sync_at"`
	LastSyncStatus string     `json:"last_sync_status"`
	IsActive       bool       `json:"is_active"`
}

type Row struct {
	ConversationID string          `json:"conversation_id"`
	CustomerName   string          `json:"customer_name"`
	ChannelID      string          `json:"channel_id"`
	ChannelName    string          `json:"channel_name"`
	LastMessageAt  *time.Time      `json:"last_message_at"`
	Status         string          `json:"status"`
	Waiting        *Turn           `json:"waiting,omitempty"`
	Turns          []Turn          `json:"turns"`
	Insight        json.RawMessage `json:"insight,omitempty"`
	InsightAt      *time.Time      `json:"insight_at,omitempty"`
	InsightJobID   string          `json:"insight_job_id,omitempty"`
	InsightStale   bool            `json:"insight_stale"`
	ResolutionNote string          `json:"resolution_note,omitempty"`
	HistoryFrom    *time.Time      `json:"history_from,omitempty"`
}

type Report struct {
	Policy               Policy    `json:"policy"`
	GeneratedAt          time.Time `json:"generated_at"`
	From                 time.Time `json:"from"`
	To                   time.Time `json:"to"`
	Pages                []Page    `json:"pages"`
	Summary              Summary   `json:"summary"`
	Rows                 []Row     `json:"rows"`
	InvalidTimestamps    int       `json:"invalid_timestamps"`
	ConversationsScanned int       `json:"conversations_scanned"`
	HistoryComplete      bool      `json:"history_complete"`
}

// IsInsightStale reports whether a stored insight no longer represents the
// latest synced message in its conversation. Legacy insights without a source
// timestamp are stale by definition.
func IsInsightStale(detail string, lastMessageAt *time.Time) bool {
	var source struct {
		LastMessageAt *time.Time `json:"source_last_message_at"`
	}
	if json.Unmarshal([]byte(detail), &source) != nil || source.LastMessageAt == nil {
		return true
	}
	return lastMessageAt != nil && lastMessageAt.After(*source.LastMessageAt)
}

func LoadPolicy(database *gorm.DB, tenantID string) (Policy, error) {
	p := DefaultPolicy()
	var setting models.AppSetting
	err := database.Where("tenant_id = ? AND setting_key = ?", tenantID, PolicyKey).First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(setting.ValuePlain), &p); err != nil {
		return p, fmt.Errorf("cấu hình CSKH bị lỗi: %w", err)
	}
	return p, p.Validate()
}

// DateWindow is inclusive of the local start date and exclusive of the day after
// the local end date. Empty defaults cover the last seven local calendar days.
func DateWindow(from, to string, now time.Time, p Policy) (time.Time, time.Time, error) {
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	local := now.In(loc)
	end := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -7)
	if from != "" {
		start, err = time.ParseInLocation("2006-01-02", from, loc)
		if err != nil {
			return start, end, fmt.Errorf("ngày bắt đầu không hợp lệ")
		}
	}
	if to != "" {
		end, err = time.ParseInLocation("2006-01-02", to, loc)
		if err != nil {
			return start, end, fmt.Errorf("ngày kết thúc không hợp lệ")
		}
		end = end.AddDate(0, 0, 1)
	}
	if !start.Before(end) || end.After(start.AddDate(0, 0, 90)) || start.After(now) {
		return start, end, fmt.Errorf("chọn khoảng ngày hợp lệ, tối đa 90 ngày")
	}
	return start, end, nil
}

// Build reads complete synced histories for matching Pages. The live queue is
// independent of dates; only report samples are windowed. Caps reject partial
// results explicitly, rather than silently displaying plausible incorrect KPIs.
func Build(ctx context.Context, database *gorm.DB, tenantID, channelID string, now, from, to time.Time, policy Policy) (*Report, error) {
	database = database.WithContext(ctx)
	r := &Report{Policy: policy, GeneratedAt: now, From: from, To: to, Pages: []Page{}, Rows: []Row{}}
	var pages []models.Channel
	q := database.Where("tenant_id = ? AND channel_type = ?", tenantID, "facebook")
	if err := q.Order("name ASC").Find(&pages).Error; err != nil {
		return nil, err
	}
	ids := []string{}
	names := map[string]string{}
	for _, p := range pages {
		if channelID == "" || p.ID == channelID {
			ids = append(ids, p.ID)
		}
		names[p.ID] = p.Name
		r.Pages = append(r.Pages, Page{p.ID, p.Name, p.LastSyncAt, p.LastSyncStatus, p.IsActive})
	}
	if len(ids) == 0 {
		if channelID != "" {
			return nil, gorm.ErrRecordNotFound
		}
		return r, nil
	}
	var convs []models.Conversation
	if err := database.Where("tenant_id = ? AND channel_id IN ?", tenantID, ids).Order("id ASC").Limit(5001).Find(&convs).Error; err != nil {
		return nil, err
	}
	if len(convs) > 5000 {
		return r, ErrReportTooLarge
	}
	r.ConversationsScanned = len(convs)
	timelines := []Timeline{}
	messageCount := 0
	for offset := 0; offset < len(convs); offset += 100 {
		end := offset + 100
		if end > len(convs) {
			end = len(convs)
		}
		batch := convs[offset:end]
		cids := []string{}
		for _, c := range batch {
			cids = append(cids, c.ID)
		}
		var messages []models.Message
		if err := database.Select("id, conversation_id, sender_type, content, content_type, sent_at").Where("tenant_id = ? AND conversation_id IN ?", tenantID, cids).Order("sent_at ASC, id ASC").Limit(100001 - messageCount).Find(&messages).Error; err != nil {
			return nil, err
		}
		messageCount += len(messages)
		if messageCount > 100000 {
			return r, ErrReportTooLarge
		}
		byConv := map[string][]models.Message{}
		for _, m := range messages {
			byConv[m.ConversationID] = append(byConv[m.ConversationID], m)
		}
		var resolutions []models.ServiceResolution
		if err := database.Where("tenant_id = ? AND conversation_id IN ?", tenantID, cids).Order("resolved_at ASC").Find(&resolutions).Error; err != nil {
			return nil, err
		}
		resolved := map[string][]models.ServiceResolution{}
		for _, v := range resolutions {
			resolved[v.ConversationID] = append(resolved[v.ConversationID], v)
		}
		// Fetch only the latest structured analysis per conversation, including an
		// id tie-break so reruns cannot duplicate aggregation counts.
		type insightRecord struct {
			models.JobResult
			InsightJobID string `gorm:"column:insight_job_id"`
		}
		var insights []insightRecord
		if err := database.Table("job_results AS j").Select("j.*, jr.job_id AS insight_job_id").Joins("LEFT JOIN job_runs AS jr ON jr.id = j.job_run_id").Where("j.tenant_id = ? AND j.conversation_id IN ? AND j.result_type = ?", tenantID, cids, "conversation_insight").Where(`NOT EXISTS (SELECT 1 FROM job_results newer WHERE newer.tenant_id = j.tenant_id AND newer.conversation_id = j.conversation_id AND newer.result_type = j.result_type AND (newer.created_at > j.created_at OR (newer.created_at = j.created_at AND newer.id > j.id)))`).Scan(&insights).Error; err != nil {
			return nil, err
		}
		latest := map[string]insightRecord{}
		for _, v := range insights {
			latest[v.ConversationID] = v
		}
		for _, c := range batch {
			timeline := Calculate(byConv[c.ID], resolved[c.ID], now, policy)
			timelines = append(timelines, timeline)
			r.InvalidTimestamps += timeline.InvalidTimestamps
			row := Row{ConversationID: c.ID, CustomerName: c.CustomerName, ChannelID: c.ChannelID, ChannelName: names[c.ChannelID], LastMessageAt: c.LastMessageAt, Status: timeline.Status, Waiting: timeline.Pending, Turns: []Turn{}}
			inWindow := false
			for _, m := range byConv[c.ID] {
				if !m.SentAt.IsZero() && !m.SentAt.After(now) && (row.HistoryFrom == nil || m.SentAt.Before(*row.HistoryFrom)) {
					t := m.SentAt
					row.HistoryFrom = &t
				}
			}
			for _, t := range timeline.Turns {
				if !t.StartedAt.Before(from) && t.StartedAt.Before(to) {
					row.Turns = append(row.Turns, t)
					inWindow = true
				}
			}
			if !inWindow && timeline.Pending == nil {
				continue
			}
			if v, ok := latest[c.ID]; ok && json.Valid([]byte(v.Detail)) {
				row.Insight = json.RawMessage(v.Detail)
				row.InsightJobID = v.InsightJobID
				t := v.CreatedAt
				row.InsightAt = &t
				row.InsightStale = IsInsightStale(v.Detail, c.LastMessageAt)
			}
			if v := resolved[c.ID]; len(v) > 0 {
				row.ResolutionNote = v[len(v)-1].Note
			}
			r.Rows = append(r.Rows, row)
		}
	}
	r.Summary = Summarize(timelines, from, to, policy)
	sort.SliceStable(r.Rows, func(i, j int) bool {
		a, b := r.Rows[i].Waiting, r.Rows[j].Waiting
		if a != nil && b != nil {
			return a.Seconds > b.Seconds
		}
		if a != nil {
			return true
		}
		if b != nil {
			return false
		}
		return r.Rows[i].ConversationID < r.Rows[j].ConversationID
	})
	return r, nil
}

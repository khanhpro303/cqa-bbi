// Package servicequality derives Page response metrics from synced message history.
package servicequality

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/vietbui/chat-quality-agent/db/models"
)

type Policy struct {
	Timezone       string `json:"timezone"`
	WorkStart      string `json:"work_start"`
	WorkEnd        string `json:"work_end"`
	AllDay         bool   `json:"all_day"`
	TargetMinutes  int    `json:"target_minutes"`
	OverdueMinutes int    `json:"overdue_minutes"`
}

func DefaultPolicy() Policy {
	return Policy{Timezone: "Asia/Ho_Chi_Minh", WorkStart: "08:00", WorkEnd: "22:00", TargetMinutes: 5, OverdueMinutes: 15}
}

func (p Policy) Validate() error {
	if strings.TrimSpace(p.Timezone) == "" {
		return fmt.Errorf("cần chọn múi giờ")
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil {
		return fmt.Errorf("múi giờ không hợp lệ")
	}
	a, e1 := time.Parse("15:04", p.WorkStart)
	b, e2 := time.Parse("15:04", p.WorkEnd)
	if e1 != nil || e2 != nil || !a.Before(b) {
		return fmt.Errorf("giờ trực phải theo HH:mm, bắt đầu trước kết thúc")
	}
	if p.TargetMinutes < 1 || p.OverdueMinutes < p.TargetMinutes || p.OverdueMinutes > 1440 {
		return fmt.Errorf("ngưỡng phải từ 1 đến 1440 phút và quá hạn không nhỏ hơn mục tiêu")
	}
	return nil
}

// WorkingSeconds counts only the overlapping daily work interval. Calendar days
// (rather than 24-hour additions) preserve timezone/DST boundaries.
func WorkingSeconds(start, end time.Time, p Policy) float64 {
	if !end.After(start) {
		return 0
	}
	if p.AllDay {
		return end.Sub(start).Seconds()
	}
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil {
		return 0
	}
	a, e1 := time.Parse("15:04", p.WorkStart)
	b, e2 := time.Parse("15:04", p.WorkEnd)
	if e1 != nil || e2 != nil {
		return 0
	}
	s := start.In(loc)
	day := time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, loc)
	total := 0.0
	for day.Before(end) {
		lo := time.Date(day.Year(), day.Month(), day.Day(), a.Hour(), a.Minute(), 0, 0, loc)
		hi := time.Date(day.Year(), day.Month(), day.Day(), b.Hour(), b.Minute(), 0, 0, loc)
		if start.After(lo) {
			lo = start
		}
		if end.Before(hi) {
			hi = end
		}
		if hi.After(lo) {
			total += hi.Sub(lo).Seconds()
		}
		day = day.AddDate(0, 0, 1)
	}
	return total
}

type Turn struct {
	StartedAt        time.Time  `json:"started_at"`
	RepliedAt        *time.Time `json:"replied_at,omitempty"`
	LastCustomerID   string     `json:"last_customer_id"`
	LastCustomerAt   time.Time  `json:"last_customer_at"`
	Seconds          float64    `json:"seconds"`
	CustomerMessages int        `json:"customer_messages"`
	First            bool       `json:"first"`
	Status           string     `json:"status"` // answered | waiting | overdue | resolved
}

type Timeline struct {
	Turns             []Turn
	Status            string
	Pending           *Turn
	InvalidTimestamps int
}

func IsClosing(m models.Message) bool {
	if m.ContentType == "sticker" {
		return true
	}
	s := strings.TrimFunc(strings.ToLower(strings.TrimSpace(m.Content)), func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
	switch s {
	case "ok", "okay", "oke", "dạ", "vâng", "cảm ơn", "cám ơn", "cảm ơn shop", "cảm ơn bạn", "thanks", "thank you", "👍", "❤️":
		return true
	}
	return false
}

// Calculate is independent of report dates: an unanswered turn can age without
// further messages. Resolutions are events, not replies, and never add a sample.
func Calculate(messages []models.Message, resolutions []models.ServiceResolution, now time.Time, p Policy) Timeline {
	type event struct {
		at         time.Time
		msg        *models.Message
		resolution *models.ServiceResolution
	}
	events := make([]event, 0, len(messages)+len(resolutions))
	out := Timeline{Status: "no_request", Turns: []Turn{}}
	for i := range messages {
		m := &messages[i]
		if m.SentAt.IsZero() || m.SentAt.After(now) {
			out.InvalidTimestamps++
			continue
		}
		events = append(events, event{at: m.SentAt, msg: m})
	}
	for i := range resolutions {
		if !resolutions[i].ResolvedAt.After(now) {
			events = append(events, event{at: resolutions[i].ResolvedAt, resolution: &resolutions[i]})
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].at.Equal(events[j].at) {
			return events[i].at.Before(events[j].at)
		}
		if events[i].msg == nil {
			return false
		}
		if events[j].msg == nil {
			return true
		}
		// Facebook timestamp precision is seconds. For ties a customer turn is
		// opened before the Page reply; this is a zero-second Page response.
		if events[i].msg.SenderType != events[j].msg.SenderType {
			return events[i].msg.SenderType == "customer"
		}
		return events[i].msg.ID < events[j].msg.ID
	})
	var pending *Turn
	var lastPageReplyAt time.Time
	finish := func(status string, at time.Time) {
		pending.Status = status
		pending.Seconds = WorkingSeconds(pending.StartedAt, at, p)
		if status == "answered" {
			t := at
			pending.RepliedAt = &t
		}
		out.Turns = append(out.Turns, *pending)
		pending = nil
		out.Status = status
	}
	for _, e := range events {
		if e.resolution != nil {
			r := e.resolution
			if pending != nil && (pending.LastCustomerAt.Before(r.ThroughAt) || (pending.LastCustomerAt.Equal(r.ThroughAt) && pending.LastCustomerID == r.ThroughMessageID)) {
				finish("resolved", e.at)
			}
			continue
		}
		m := e.msg
		switch m.SenderType {
		case "customer":
			if pending == nil {
				ackAfterReply := out.Status == "answered" && !lastPageReplyAt.IsZero() && m.SentAt.Sub(lastPageReplyAt) <= 30*time.Minute
				initialSticker := len(out.Turns) == 0 && m.ContentType == "sticker"
				if (ackAfterReply || initialSticker) && IsClosing(*m) {
					continue
				}
				pending = &Turn{StartedAt: m.SentAt, First: len(out.Turns) == 0, Status: "waiting"}
			}
			pending.CustomerMessages++
			pending.LastCustomerID = m.ID
			pending.LastCustomerAt = m.SentAt
		case "agent":
			lastPageReplyAt = m.SentAt
			if pending != nil {
				finish("answered", m.SentAt)
			}
		}
	}
	if pending != nil {
		pending.Seconds = WorkingSeconds(pending.StartedAt, now, p)
		pending.Status = "waiting"
		if pending.Seconds >= float64(p.OverdueMinutes*60) {
			pending.Status = "overdue"
		}
		out.Pending = pending
		out.Status = pending.Status
		out.Turns = append(out.Turns, *pending)
	}
	return out
}

type Summary struct {
	Answered           int      `json:"answered"`
	OnTime             int      `json:"on_time"`
	OnTimePercent      *float64 `json:"on_time_percent"`
	MedianSeconds      *float64 `json:"median_seconds"`
	P90Seconds         *float64 `json:"p90_seconds"`
	FirstMedianSeconds *float64 `json:"first_median_seconds"`
	Waiting            int      `json:"waiting"`
	Overdue            int      `json:"overdue"`
	Resolved           int      `json:"resolved"`
}

func Summarize(timelines []Timeline, from, to time.Time, p Policy) Summary {
	s := Summary{}
	var durations, first []float64
	for _, timeline := range timelines {
		if timeline.Status == "waiting" {
			s.Waiting++
		}
		if timeline.Status == "overdue" {
			s.Overdue++
		}
		for _, t := range timeline.Turns {
			if t.StartedAt.Before(from) || !t.StartedAt.Before(to) {
				continue
			}
			if t.Status == "resolved" {
				s.Resolved++
			}
			if t.Status != "answered" {
				continue
			}
			s.Answered++
			durations = append(durations, t.Seconds)
			if t.First {
				first = append(first, t.Seconds)
			}
			if t.Seconds <= float64(p.TargetMinutes*60) {
				s.OnTime++
			}
		}
	}
	if s.Answered > 0 {
		rate := float64(s.OnTime) * 100 / float64(s.Answered)
		s.OnTimePercent = &rate
	}
	s.MedianSeconds = percentile(durations, 0.5)
	s.P90Seconds = percentile(durations, 0.9)
	s.FirstMedianSeconds = percentile(first, 0.5)
	return s
}

func percentile(values []float64, quantile float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sort.Float64s(values)
	i := int(math.Ceil(quantile*float64(len(values)))) - 1
	x := values[i]
	return &x
}

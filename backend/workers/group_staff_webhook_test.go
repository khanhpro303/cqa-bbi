package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestStaffGroupWebhookAnswersInOpeningTurn(t *testing.T) {
	const (
		key         = "0123456789abcdef0123456789abcdef"
		question    = "Bull ơi, Giày LS2 Zoe Man - Black H-V Yellow - 44 còn tồn khả dụng không?"
		tagQuestion = "Giày LS2 Zoe Man - Black H-V Yellow - 44 còn tồn khả dụng không?"
		answer      = "Dạ, em chưa tra được tồn của mẫu giày này."
		welcome     = "Bull xin chào."
	)
	database, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "fixture@tcp(127.0.0.1:3306)/fixture",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	previousDB, previousRedis := db.DB, db.RedisClient
	db.DB, db.RedisClient = database, nil
	t.Cleanup(func() {
		db.DB, db.RedisClient = previousDB, previousRedis
		_ = pool.Close()
	})

	creds, err := pkg.Encrypt([]byte(`{"access_token":"fixture","oa_id":"oa"}`), key)
	if err != nil {
		t.Fatal(err)
	}
	group := models.CRMGroup{ID: "crm", TenantID: "tenant", ChannelID: "channel", ZaloGroupID: "group", CustomerCode: "S084"}
	channel := models.Channel{ID: group.ChannelID, TenantID: group.TenantID, AutoReplyEnabled: true, CredentialsEncrypted: creds,
		Metadata: `{"session_keyword":"Chào Bull; Hi Bull","group_staff_call_only":true,"group_bot_names":"Bull;bot"}`}
	err = database.Callback().Query().After("gorm:query").Register("test:staff-session", func(tx *gorm.DB) {
		switch row := tx.Statement.Dest.(type) {
		case *[]models.Channel:
			*row = []models.Channel{channel}
		case *models.CRMGroup:
			*row = group
		case *[]models.CRMGroup:
			*row = []models.CRMGroup{group}
		case *models.ZaloWhitelist:
			*row = models.ZaloWhitelist{ID: "whitelist", Name: "Fixture Staff"}
		case *models.ZaloCustomer:
			_ = tx.AddError(gorm.ErrRecordNotFound)
		case *int64:
			if tx.Statement.Table == "crm_group_employees" {
				*row = 1
			}
		case *[]models.ERPEndpoint:
			*row = []models.ERPEndpoint{{Resource: "inventory", IsEnabled: true, ScopeType: "own", ProductGroups: "Nguyên Đầu"}}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	usageDone := make(chan struct{}, 1)
	err = database.Callback().Create().After("gorm:create").Register("test:staff-session-usage", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*models.AIUsageLog); ok {
			usageDone <- struct{}{}
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	var replies []string
	flowCalls := 0
	wantFlowInput := question
	transport := http.DefaultTransport
	http.DefaultTransport = stockTestTransport(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Host + req.URL.Path {
		case "langflow.test/api/v1/run/public":
			flowCalls++
			var input struct {
				Text      string `json:"input_value"`
				SessionID string `json:"session_id"`
			}
			err := json.NewDecoder(req.Body).Decode(&input)
			inputStartsWithQuestion := input.Text == wantFlowInput || strings.HasPrefix(input.Text, wantFlowInput+"\n")
			if err != nil || !inputStartsWithQuestion || strings.Contains(input.Text, "[@oa]") || input.SessionID == "" {
				t.Errorf("opening turn payload = %+v, %v", input, err)
			}
			permissions, err := engine.VerifyPermissionToken(req.Header.Get("X-Langflow-Global-Var-ERP_PERMISSION_TOKEN"), key)
			if err != nil || permissions.AgentType != "public" || permissions.ZaloGroupID != group.ZaloGroupID {
				t.Errorf("opening turn group permissions = %+v, %v", permissions, err)
			} else if inventory := permissions.GetGroupSpecificPermission(group.ID, "inventory"); inventory == nil ||
				inventory.ScopeType != "own" || len(inventory.ProductGroups) != 1 || inventory.ProductGroups[0] != "nguyên đầu" {
				t.Errorf("opening turn inventory scope = %+v", inventory)
			}
			body = fmt.Sprintf(`{"provider":"Anthropic","outputs":[{"outputs":[{"results":{"message":{"text":%q,"properties":{"usage":{"input_tokens":11,"output_tokens":7,"model_name":"claude-haiku-4-5"}}}}}]}]}`, answer)
		case "openapi.zalo.me/v3.0/oa/group/message":
			var reply struct {
				Message struct{ Text string }
			}
			if err := json.NewDecoder(req.Body).Decode(&reply); err != nil {
				return nil, err
			}
			replies = append(replies, reply.Message.Text)
			body = `{"error":0}`
		default:
			return nil, fmt.Errorf("unexpected fixture request: %s %s", req.Method, req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	cfg := &config.Config{EncryptionKey: key, LangflowAPIURL: "https://langflow.test",
		LangflowPublicFlowID: "public", ChatbotSessionWelcomeMessage: welcome}
	handle := HandleZaloWebhookTask(cfg, engine.NewLangflowClient(cfg), nil)
	for _, tc := range []struct {
		text, reply, flowInput string
		flowCalls              int
		unsigned               bool
		incomingOAID           string
		staffCallDisabled      bool
	}{
		{"Chào Bull", welcome, "", 0, false, "oa", false},
		{"Hi Bull", welcome, "", 0, false, "oa", false},
		{question, answer, question, 1, false, "oa", false},
		{"[@oa] " + tagQuestion, answer, tagQuestion, 1, false, "oa", false},
		{"[@oa] " + tagQuestion, answer, tagQuestion, 1, false, "oa", true},
		{"[@oa]", welcome, "", 0, false, "oa", false},
		{"[@another-account] " + tagQuestion, "", "", 0, false, "oa", false},
		{"[@oa] " + tagQuestion, "", "", 0, true, "oa", false},
		{"[@oa] " + tagQuestion, "", "", 0, false, "conflicting-oa", false},
	} {
		if tc.staffCallDisabled {
			channel.Metadata = `{"session_keyword":"Chào Bull; Hi Bull","group_staff_call_only":false,"group_bot_names":"Bull;bot"}`
		} else {
			channel.Metadata = `{"session_keyword":"Chào Bull; Hi Bull","group_staff_call_only":true,"group_bot_names":"Bull;bot"}`
		}
		payload := ZaloWebhookPayload{EventName: "user_send_group_text"}
		payload.Sender.ID, payload.Recipient.ID, payload.Message.Text = "sender", group.ZaloGroupID, tc.text
		payload.OAID, payload.SignatureVerified = tc.incomingOAID, !tc.unsigned
		task, err := NewZaloWebhookTask(payload)
		if err != nil {
			t.Fatal(err)
		}
		replies = nil
		flowCalls = 0
		wantFlowInput = tc.flowInput
		err = handle(context.Background(), task)
		if flowCalls > 0 && err == nil {
			select {
			case <-usageDone:
			case <-time.After(3 * time.Second):
				t.Fatal("chatbot usage write did not finish")
			}
		}
		wantReplies := 0
		if tc.reply != "" {
			wantReplies = 1
		}
		if err != nil || flowCalls != tc.flowCalls || len(replies) != wantReplies || (tc.reply != "" && replies[0] != tc.reply) {
			t.Fatalf("opening with %q: got replies %v, flow calls %d, error %v", tc.text, replies, flowCalls, err)
		}
	}
}

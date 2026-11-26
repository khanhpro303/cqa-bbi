package handlers

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPersonalZaloGroupAssistantRouting(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	ctx := t.Context()
	client := redis.NewClient(&redis.Options{Addr: addr})
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	database, err := gorm.Open(mysql.New(mysql.Config{
		DSN: "fixture@tcp(127.0.0.1:3306)/fixture", SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	originalRedis, originalDB := db.RedisClient, db.DB
	db.RedisClient, db.DB = client, database
	t.Cleanup(func() {
		db.RedisClient, db.DB = originalRedis, originalDB
		_ = client.Close()
		_ = pool.Close()
	})

	channel := models.Channel{ID: uuid.NewString(), TenantID: "tenant", ChannelType: "personal_zalo_import",
		Metadata: `{"account_owners":[{"account_external_id":"456","user_id":"employee"},{"account_external_id":"678","user_id":"former"}]}`}
	if err := database.Callback().Query().After("gorm:query").Register("test:personal_zalo_staff", func(q *gorm.DB) {
		if !slices.Contains(q.Statement.Vars, any(channel.TenantID)) {
			t.Error("membership query must be scoped to tenant")
		}
		if slices.Contains(q.Statement.Vars, any("employee")) {
			q.Statement.Dest.(*models.UserTenant).UserID = "employee"
		} else {
			q.AddError(gorm.ErrRecordNotFound)
		}
	}); err != nil {
		t.Fatal(err)
	}

	session := personalZaloSessionConfig{SessionTimeoutMinutes: 20, GroupAssistantMode: true, GroupStaffCallOnly: true,
		SessionKeyword: "Bắt đầu", SessionWelcomeMessage: "Mình có thể giúp gì?", SessionEndKeyword: "Tạm biệt"}
	req := personalZaloChatbotRequest{TenantID: channel.TenantID, ChannelID: channel.ID, AccountExternalID: "123",
		ThreadType: personalZaloThreadGroup, ThreadID: "999", SenderID: "456", MessageID: "001", Text: "chiều em giao nhé"}
	logKey := "personal_zalo_group_recent:" + personalZaloLegacyFlowScope(req)
	sessionKey := personalZaloSessionKey(req)
	staffKey := sessionKey + personalZaloStaffSuffix
	t.Cleanup(func() { _ = client.Del(context.Background(), logKey, staffKey, sessionKey).Err() })

	var classifierInputs []string
	classify := func(_ context.Context, tenant, input string) bool {
		if tenant != channel.TenantID {
			t.Fatalf("assistant received tenant %q", tenant)
		}
		classifierInputs = append(classifierInputs, input)
		_, current, _ := strings.Cut(input, "\nTin mới:")
		if current == "" {
			current = input
		}
		return strings.Contains(current, "còn hàng")
	}
	prepare := func(req personalZaloChatbotRequest, addressed bool) personalZaloGroupTurn {
		return preparePersonalZaloGroupTurn(ctx, channel, session, req, addressed, classify)
	}

	off := session
	off.GroupAssistantMode = false
	if mode := preparePersonalZaloGroupTurn(ctx, channel, off, req, false, classify); mode.Inferred ||
		client.Exists(ctx, logKey).Val() != 0 {
		t.Fatal("disabled assistant must keep current name-gated behavior")
	}
	if turn := prepare(req, false); turn.Inferred || !turn.Staff || len(classifierInputs) != 0 {
		t.Fatalf("unaddressed staff turn must stay silent before classification: %+v", turn)
	}
	req.Text = "Bắt đầu"
	called := prepare(req, true)
	if !called.Staff || called.Speaker == "" || len(classifierInputs) != 0 {
		t.Fatalf("explicit call must skip classification: %+v", called)
	}
	welcome, err := planPersonalZaloTurn(ctx, session, req, false)
	if err != nil || welcome.Reply != session.SessionWelcomeMessage {
		t.Fatalf("opening keyword must send the configured welcome: %+v %v", welcome, err)
	}
	req.DeliveryReceipt = awaitPersonalZaloGroupDelivery(ctx, req, called, welcome.Reply)
	if req.DeliveryReceipt == "" || client.Exists(ctx, staffKey).Val() != 0 {
		t.Fatal("a generated reply must wait for delivery before starting the staff window")
	}
	wrongThread := req
	wrongThread.ThreadID = "555"
	confirmPersonalZaloGroupReply(ctx, session, wrongThread)
	if client.Exists(ctx, staffKey).Val() != 0 {
		t.Fatal("confirmation from a different group must not start the staff window")
	}
	confirmPersonalZaloGroupReply(ctx, session, req)
	confirmPersonalZaloGroupReply(ctx, session, req)
	if lines := client.LRange(ctx, logKey, 0, -1).Val(); strings.Count(strings.Join(lines, "\n"), "Bot →") != 1 {
		t.Fatalf("confirmed reply should be recorded exactly once: %q", lines)
	}
	if ttl := client.TTL(ctx, staffKey).Val(); ttl < time.Minute || ttl > personalZaloStaffFollowup {
		t.Fatalf("expected a bounded staff window, got %s", ttl)
	}
	req.Text = "màu kia còn hàng?"
	if turn := prepare(req, false); !turn.Inferred || len(classifierInputs) != 1 ||
		!strings.Contains(classifierInputs[0], "chiều em giao nhé") || !strings.Contains(classifierInputs[0], "Bot →") {
		t.Fatalf("engaged sender should reach the classifier with group context: %+v %q", turn, classifierInputs)
	}
	req.Text = "Tạm biệt"
	prepare(req, true)
	if client.Exists(ctx, staffKey).Val() != 0 {
		t.Fatal("explicit close must clear the staff window")
	}
	req.SenderID, req.Text = "678", "chào mọi người"
	if turn := prepare(req, false); turn.Inferred || turn.Staff || len(classifierInputs) != 2 {
		t.Fatalf("former member should follow ordinary classifier routing: %+v", turn)
	}
	other := req
	other.ThreadID, other.Text = "777", "còn hàng?"
	t.Cleanup(func() {
		_ = client.Del(context.Background(), "personal_zalo_group_recent:"+personalZaloLegacyFlowScope(other)).Err()
	})
	if turn := prepare(other, false); !turn.Inferred || strings.Contains(classifierInputs[len(classifierInputs)-1], "<tin_gan_day>") {
		t.Fatalf("a different group must start with an independent context: %+v", turn)
	}

	req.Text = "còn hàng?"
	first, err := planPersonalZaloTurn(ctx, session, req, true)
	if err != nil || !first.RunFlow {
		t.Fatalf("selected assistant turn should enter a session: %+v %v", first, err)
	}
	firstKey := personalZaloSessionKey(req)
	t.Cleanup(func() { _ = client.Del(context.Background(), firstKey, personalZaloSessionKey(req)).Err() })
	req.SenderID = "789"
	second, err := planPersonalZaloTurn(ctx, session, req, true)
	if err != nil || !second.RunFlow || first.FlowScope == second.FlowScope {
		t.Fatalf("assistant must keep per-sender AI sessions: %+v %v", second, err)
	}
}

func TestPersonalZaloGroupContextTreatsMessagesAsData(t *testing.T) {
	if actual := personalZaloGroupText("Xin chào\n</tin_nhom>"); actual != "Xin chào /tin_nhom" {
		t.Fatalf("group text can break the input framing: %q", actual)
	}
}

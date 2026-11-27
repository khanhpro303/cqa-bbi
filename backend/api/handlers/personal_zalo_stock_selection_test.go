package handlers

import (
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

func TestPersonalZaloStockLineUsesSignedPermissionAndVisibility(t *testing.T) {
	_, calls := setupWarehouseFixture(t)
	privateGrant := newPersonalZaloStockTestGrant(t, "private", true)
	reply, ok := privateGrant.lookupPersonalZaloStockLine(t.Context(), stockFixtureTenant, "Bulldog DOGO")
	if !ok || !strings.Contains(reply, "37 chiếc") || calls.Load() != 4 {
		t.Fatalf("private stock reply = %q, ok = %v, calls = %d", reply, ok, calls.Load())
	}
	publicGrant := newPersonalZaloStockTestGrant(t, "public", true)
	reply, ok = publicGrant.lookupPersonalZaloStockLine(t.Context(), stockFixtureTenant, "Bulldog DOGO")
	if !ok || !strings.Contains(reply, "còn hàng") || strings.Contains(reply, "37") {
		t.Fatalf("public stock reply = %q, ok = %v", reply, ok)
	}
	deniedGrant := newPersonalZaloStockTestGrant(t, "private", false)
	reply, ok = deniedGrant.lookupPersonalZaloStockLine(t.Context(), stockFixtureTenant, "Bulldog DOGO")
	if ok || strings.Contains(reply, "37") || calls.Load() != 8 {
		t.Fatalf("denied stock reply = %q, ok = %v, calls = %d", reply, ok, calls.Load())
	}
}

func TestPersonalZaloStockMenuResolvesNumberWithoutLangflow(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatal(err)
	}
	previous := db.RedisClient
	db.RedisClient = client
	t.Cleanup(func() {
		db.RedisClient = previous
		_ = client.Close()
	})
	_, calls := setupWarehouseFixture(t)
	grant := newPersonalZaloStockTestGrant(t, "private", true)
	turn := &engine.GroupPermissionContext{TurnID: grant.turnID, PersonalZaloSessionID: grant.flowScope}
	options := engine.BuildStockPickPendingButtons([]string{"Bulldog DOGO", "Different line"}, engine.StockPickIntentStock)
	recordPersonalZaloStockMenu(t.Context(), turn, options)

	req := personalZaloChatbotRequest{TenantID: stockFixtureTenant, Text: "1"}
	if got := grant.answerPersonalZaloStockPick(t.Context(), req); got != "" {
		t.Fatalf("used a menu before it was published: %q", got)
	}
	menu := grant.publishPersonalZaloStockMenu(t.Context())
	if !strings.Contains(menu, "1. Bulldog DOGO\n2. Different line") {
		t.Fatalf("wrong menu order: %q", menu)
	}
	if got := newPersonalZaloStockTestGrant(t, "public", true).answerPersonalZaloStockPick(t.Context(), req); got != "" {
		t.Fatalf("used another grant's menu: %q", got)
	}
	req.Text = "3"
	if reply, _, err := grant.run(t.Context(), nil, models.Channel{}, req); err != nil || reply != menu || calls.Load() != 0 {
		t.Fatalf("out-of-range reply = %q, err = %v, calls = %d", reply, err, calls.Load())
	}
	req.Text = "1"
	if reply, _, err := grant.run(t.Context(), nil, models.Channel{}, req); err != nil ||
		!strings.Contains(reply, "Bulldog DOGO") || !strings.Contains(reply, "37 chiếc") || calls.Load() != 4 {
		t.Fatalf("selection reply = %q, err = %v, calls = %d", reply, err, calls.Load())
	}
	if got := grant.answerPersonalZaloStockPick(t.Context(), req); got != "" {
		t.Fatalf("reused a completed menu: %q", got)
	}
}

func newPersonalZaloStockTestGrant(t *testing.T, role string, enabled bool) personalZaloDirectGrant {
	t.Helper()
	scope := "test-stock-" + uuid.NewString()
	perm := engine.GroupPermissionContext{
		TenantID: stockFixtureTenant, AgentType: role, ZaloUserID: "sender", PersonalZalo: true,
		PersonalZaloSessionID: scope, TurnID: scope + ":turn",
		Groups: []engine.GroupPermission{{GroupID: "private_bot", Resources: []engine.ResourcePermission{
			{Resource: "inventory", IsEnabled: enabled, ScopeType: "all", ProductGroups: []string{"mũ"}},
		}}},
	}
	token, err := engine.SignPermissionToken(perm, stockFixtureSigningKey)
	if err != nil {
		t.Fatal(err)
	}
	agentToken := "fixture-agent-token"
	if role == "public" {
		agentToken = "fixture-public-token"
	}
	return personalZaloDirectGrant{flowID: "fixture-flow", flowScope: scope, turnID: perm.TurnID, agentToken: agentToken, permission: token}
}

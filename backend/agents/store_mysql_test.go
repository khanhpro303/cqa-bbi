package agents

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// Needs MySQL 8, e.g.
//
//	docker run -d -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=cqa -p 3307:3306 mysql:8.0
//	CQA_TEST_MYSQL_DSN='root:root@tcp(127.0.0.1:3307)/cqa?parseTime=true&loc=UTC&charset=utf8mb4' go test ./agents
func connectTestMySQL(t *testing.T) {
	t.Helper()
	dsn := os.Getenv("CQA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CQA_TEST_MYSQL_DSN not set")
	}
	if db.DB == nil {
		if err := db.Connect(dsn, false); err != nil {
			t.Fatal(err)
		}
		if err := db.AutoMigrate(); err != nil {
			t.Fatal(err)
		}
	}
}

func seedTenant(t *testing.T, publicPrompt, internalPrompt string) (tenantID string, channels []string) {
	t.Helper()
	now := time.Now()
	tenantID = uuid.NewString()
	must(t, db.DB.Create(&models.Tenant{ID: tenantID, Name: "Agents fixture", Slug: "agents-" + tenantID[:8], Settings: "{}", CreatedAt: now, UpdatedAt: now}).Error)
	for i, kind := range []string{"zalo_oa", "personal_zalo_import", "facebook"} {
		id := uuid.NewString()
		must(t, db.DB.Create(&models.Channel{ID: id, TenantID: tenantID, ChannelType: kind, Name: "Kênh " + string(rune('A'+i)),
			CredentialsEncrypted: []byte{}, Metadata: "{}", CreatedAt: now, UpdatedAt: now}).Error)
		channels = append(channels, id)
	}
	for key, value := range map[string]string{LegacyPublicPromptKey: publicPrompt, LegacyInternalPromptKey: internalPrompt} {
		must(t, db.DB.Create(&models.AppSetting{ID: uuid.NewString(), TenantID: tenantID, SettingKey: key, ValuePlain: value, CreatedAt: now, UpdatedAt: now}).Error)
	}
	t.Cleanup(func() {
		for _, m := range []any{&models.AITeamMember{}, &models.AITeam{}, &models.AIAgentRevision{}, &models.AIAgent{}, &models.AppSetting{}, &models.Channel{}} {
			db.DB.Where("tenant_id = ?", tenantID).Delete(m)
		}
		db.DB.Where("id = ?", tenantID).Delete(&models.Tenant{})
	})
	return tenantID, channels
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentsLifecycleMySQL(t *testing.T) {
	connectTestMySQL(t)
	ctx := context.Background()
	const public, internal = "Prompt khách hàng\n  giữ nguyên khoảng trắng  ", "Prompt nội bộ"
	tenantID, channels := seedTenant(t, public, internal)
	oa, personal, facebook := channels[0], channels[1], channels[2]

	// Off: the AI Engine path answers.
	if plan, err := PlanTurn(ctx, tenantID, oa, TierPublic); err != nil || plan.Mode != ModeLegacy {
		t.Fatalf("plan before enabling = %+v %v", plan, err)
	}

	// Turning on copies the AI Engine prompts, unchanged, into two agents.
	must(t, SetEnabled(ctx, tenantID, "admin", true))
	if created, err := MigrateLegacy(ctx, tenantID, "admin"); err != nil || created {
		t.Fatalf("second migration = %v %v", created, err)
	}
	for tier, want := range map[string]struct{ key, prompt string }{
		TierPublic:  {LegacyCustomerAgentKey, public},
		TierPrivate: {LegacyStaffAgentKey, internal},
	} {
		for _, ch := range []string{oa, personal} {
			plan, err := PlanTurn(ctx, tenantID, ch, tier)
			if err != nil || plan.Mode != ModeAgent || plan.Selection.Agent.Key != want.key {
				t.Fatalf("%s plan on %s = %+v %v", tier, ch, plan, err)
			}
			if got := plan.BasePrompt("unused"); got != want.prompt {
				t.Errorf("%s prompt changed by migration: %q", tier, got)
			}
			if len(plan.Selection.Agent.Tools) != len(AllToolNames()) || plan.Selection.Agent.LangflowFlowID != "" {
				t.Errorf("legacy agent must keep every tool and the AI Engine flow: %+v", plan.Selection.Agent)
			}
		}
	}

	// A debt specialist answering customers through its own team.
	debt, err := SaveAgent(ctx, tenantID, "admin", Agent{Key: "cong-no", Name: "Bot công nợ", Audience: AudienceCustomer,
		SystemPrompt: "Chỉ trả lời về công nợ.", Tools: []string{"lookup_debt", "find_customer"}, IsActive: true})
	must(t, err)
	if _, err := SaveAgent(ctx, tenantID, "admin", Agent{Key: "cong-no", Name: "Trùng", Audience: AudienceCustomer}); !isValidation(err, "key") {
		t.Errorf("duplicate key = %v", err)
	}
	team, err := SaveTeam(ctx, tenantID, "admin", TeamInput{Name: "Công nợ", PersonaPrompt: "Xưng em.",
		DefaultAgentID: debt.ID, Members: []MemberInput{{AgentID: debt.ID, Enabled: true}}})
	must(t, err)
	must(t, SetChannelTeam(ctx, tenantID, personal, team.ID))
	if err := SetChannelTeam(ctx, tenantID, facebook, team.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("team on a non-chat channel = %v", err)
	}

	plan, err := PlanTurn(ctx, tenantID, personal, TierPublic)
	if err != nil || plan.Mode != ModeAgent || plan.Selection.Agent.Key != "cong-no" ||
		plan.BasePrompt("") != "Xưng em.\n\nChỉ trả lời về công nợ." || plan.Allows(ToolComplaintRecord) {
		t.Errorf("debt plan = %+v %v", plan, err)
	}
	// The debt team has no staff agent: staff on that account get people.
	if plan, err := PlanTurn(ctx, tenantID, personal, TierPrivate); err != nil || plan.Mode != ModeSilent {
		t.Errorf("staff plan on the debt account = %+v %v", plan, err)
	}
	// The OA still answers through the default team.
	if plan, _ := PlanTurn(ctx, tenantID, oa, TierPublic); plan.Selection.Agent.Key != LegacyCustomerAgentKey {
		t.Errorf("OA plan = %+v", plan)
	}

	teams, err := ListTeams(ctx, tenantID)
	must(t, err)
	if len(teams) != 2 || !teams[0].IsDefault || len(teams[0].Channels) != 1 || teams[0].Channels[0].ID != oa ||
		len(teams[1].Channels) != 1 || teams[1].Channels[0].ID != personal || len(teams[1].Silent) != 1 || teams[1].Silent[0] != AudienceStaff {
		t.Errorf("teams = %+v", teams)
	}
	// The UI tells a channel's own team from the default it falls back to.
	if teams[0].Channels[0].AITeamID != "" || teams[1].Channels[0].AITeamID != team.ID {
		t.Errorf("channel team ids = %q %q", teams[0].Channels[0].AITeamID, teams[1].Channels[0].AITeamID)
	}
	for _, m := range teams[0].Members {
		if m.SystemPrompt != "" {
			t.Error("team list must not carry prompts")
		}
	}

	// In use: neither the agent nor the team can go while referenced.
	if err := DeleteAgent(ctx, tenantID, debt.ID); !errors.Is(err, ErrInUse) {
		t.Errorf("delete member agent = %v", err)
	}
	if err := DeleteTeam(ctx, tenantID, team.ID); !errors.Is(err, ErrInUse) {
		t.Errorf("delete used team = %v", err)
	}
	if err := DeleteTeam(ctx, tenantID, teams[0].ID); !errors.Is(err, ErrInUse) {
		t.Errorf("delete default team = %v", err)
	}
	off := false
	if _, err := SaveTeam(ctx, tenantID, "admin", TeamInput{ID: teams[0].ID, Name: "Mặc định", IsActive: &off}); !isValidation(err, "is_active") {
		t.Errorf("deactivate default team = %v", err)
	}

	// Keys never change; every save is a revision.
	debt.Key, debt.SystemPrompt = "doi-ma", "Prompt mới."
	updated, err := SaveAgent(ctx, tenantID, "admin", debt)
	must(t, err)
	if updated.Key != "cong-no" {
		t.Errorf("key changed to %s", updated.Key)
	}
	revs, err := ListRevisions(ctx, tenantID, debt.ID)
	must(t, err)
	if len(revs) != 2 || revs[0].SystemPrompt != "Prompt mới." {
		t.Errorf("revisions = %+v", revs)
	}

	// The test console loads a team by id with its active members, and
	// never another tenant's.
	byID, err := TeamByID(ctx, tenantID, team.ID)
	if err != nil || byID.Name != "Công nợ" || len(byID.Members) != 1 || byID.Members[0].Key != "cong-no" {
		t.Errorf("team by id = %+v %v", byID, err)
	}

	// Tenant isolation.
	otherTenant, _ := seedTenant(t, "", "")
	if _, err := TeamByID(ctx, otherTenant, team.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant team by id = %v", err)
	}
	if _, err := GetAgent(ctx, otherTenant, debt.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant read = %v", err)
	}
	if _, err := SaveTeam(ctx, otherTenant, "x", TeamInput{Name: "Lấn", Members: []MemberInput{{AgentID: debt.ID, Enabled: true}}}); !isValidation(err, "members") {
		t.Errorf("cross-tenant member = %v", err)
	}
	if err := SetChannelTeam(ctx, otherTenant, personal, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant channel = %v", err)
	}

	// AI Engine prompt edited after the copy: drift, then refresh.
	must(t, db.DB.Model(&models.AppSetting{}).Where("tenant_id = ? AND setting_key = ?", tenantID, LegacyPublicPromptKey).
		Update("value_plain", "Prompt khách hàng v2").Error)
	drift, err := LegacyDrift(ctx, tenantID)
	must(t, err)
	if len(drift) != 1 || drift[0] != LegacyCustomerAgentKey {
		t.Errorf("drift = %v", drift)
	}
	refreshed, err := RefreshLegacyPrompts(ctx, tenantID, "admin")
	must(t, err)
	if len(refreshed) != 1 {
		t.Errorf("refreshed = %v", refreshed)
	}
	if drift, _ := LegacyDrift(ctx, tenantID); len(drift) != 0 {
		t.Errorf("drift after refresh = %v", drift)
	}

	// Back to the default team, then the team can go; off returns to legacy.
	must(t, SetChannelTeam(ctx, tenantID, personal, ""))
	must(t, DeleteTeam(ctx, tenantID, team.ID))
	must(t, DeleteAgent(ctx, tenantID, debt.ID))
	if revs, _ := ListRevisions(ctx, tenantID, debt.ID); len(revs) != 0 {
		t.Errorf("revisions left after delete: %d", len(revs))
	}
	must(t, db.DB.Model(&models.AITeam{}).Where("tenant_id = ?", tenantID).Update("is_active", false).Error)
	if plan, err := PlanTurn(ctx, tenantID, personal, TierPublic); err != nil || plan.Mode != ModeSilent || plan.Reason != "no_team" {
		t.Errorf("plan without an active team = %+v, err = %v", plan, err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if plan, err := PlanTurn(canceled, tenantID, personal, TierPublic); err == nil || plan.Mode != ModeSilent || plan.Reason != "enabled_lookup_failed" {
		t.Errorf("plan with a failed settings lookup = %+v, err = %v", plan, err)
	}

	must(t, SetEnabled(ctx, tenantID, "admin", false))
	if plan, _ := PlanTurn(ctx, tenantID, personal, TierPublic); plan.Mode != ModeLegacy {
		t.Errorf("plan after turning off = %+v", plan)
	}
}

func isValidation(err error, field string) bool {
	var verr *ValidationError
	return errors.As(err, &verr) && verr.Field == field
}

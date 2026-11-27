package engine

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestResolvePersonalZaloERPPermissions(t *testing.T) {
	database, err := gorm.Open(mysql.New(mysql.Config{
		DSN: "fixture@tcp(127.0.0.1:3306)/fixture", SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	saved := db.DB
	db.DB = database
	t.Cleanup(func() {
		db.DB = saved
		pool, _ := database.DB()
		_ = pool.Close()
	})

	dbFailure := errors.New("ERP permission read failed")
	failPrivateLookup := false
	err = database.Callback().Query().After("gorm:query").Register("fixture:personal-zalo-permissions", func(query *gorm.DB) {
		args := query.Statement.Vars
		sql := query.Statement.SQL.String()
		if query.Statement.Table == "crm_group_customers" {
			// GORM invokes callbacks while generating the embedded subquery.
			// The outer group query below asserts the assembled predicates.
			return
		}
		switch dest := query.Statement.Dest.(type) {
		case *models.ZaloCustomer:
			if len(args) < 3 || args[1] != "tenant" || args[2] != "approved" || strings.Contains(sql, "zalo_user_id") {
				t.Fatalf("customer must resolve by approved CRM record in tenant, got %s %v", sql, args)
			}
			switch args[0] {
			case "crm-customer":
				*dest = models.ZaloCustomer{ID: "crm-customer", CustomerCode: "OLD-CODE"}
			case "approved-no-code":
				*dest = models.ZaloCustomer{ID: "approved-no-code"}
			default:
				query.AddError(gorm.ErrRecordNotFound)
			}
		case *[]string:
			if len(args) != 2 || args[0] != "tenant" || !strings.Contains(sql, "zalo_customer_id") {
				t.Fatalf("customer code lookup is not scoped to the CRM record, got %s %v", sql, args)
			}
			if args[1] == "crm-customer" {
				*dest = []string{"CUST-A", "CUST-B"}
			}
		case *[]models.CRMGroup:
			if len(args) != 2 || args[0] != "tenant" || args[1] != "crm-customer" ||
				!strings.Contains(sql, "crm_group_customers") || !strings.Contains(sql, "archived_at") {
				t.Fatalf("permissions require active groups linked to this tenant's customer, got %s %v", sql, args)
			}
			*dest = []models.CRMGroup{{ID: "group-a", Name: "Group A"}}
		case *[]models.ERPEndpoint:
			if len(args) != 2 || args[0] != "tenant" {
				t.Fatalf("endpoint lookup is not scoped to tenant and one group, got %s %v", sql, args)
			}
			if args[1] == "private_bot" {
				if failPrivateLookup {
					query.AddError(dbFailure)
					return
				}
				*dest = []models.ERPEndpoint{
					{GroupID: "private_bot", Resource: "inventory", IsEnabled: true, ScopeType: "all", ProductGroups: "Mũ", Brands: "BBI"},
					{GroupID: "private_bot", Resource: "orders", IsEnabled: false, ScopeType: "all"},
				}
			} else if args[1] == "group-a" {
				*dest = []models.ERPEndpoint{{GroupID: "group-a", Resource: "orders", IsEnabled: true, ScopeType: "all"}}
			} else {
				t.Fatalf("unexpected endpoint group, got %s %v", sql, args)
			}
		default:
			t.Fatalf("unexpected query %s %v", sql, args)
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	resolve := func(agentType, customerID string) (GroupPermissionContext, error) {
		return ResolvePersonalZaloERPPermissions(t.Context(), "tenant", "personal-channel", "12345", agentType,
			models.ZaloCustomer{ID: customerID, CustomerCode: "FORGED"})
	}
	public, err := resolve("public", "crm-customer")
	if err != nil || !public.PersonalZalo || public.ChannelID != "personal-channel" || public.ZaloUserID != "12345" ||
		public.CustomerCode != "CUST-A" || !reflect.DeepEqual(public.CustomerCodes, []string{"CUST-A", "CUST-B"}) {
		t.Fatalf("unexpected public permission: %+v, err=%v", public, err)
	}
	if allowed, scope, _ := public.IsResourceAllowed("orders"); !allowed || scope != "own" {
		t.Fatalf("public orders permission = %v/%s, want own", allowed, scope)
	}
	if missingCodes, err := resolve("public", "approved-no-code"); err != nil || len(missingCodes.Groups) != 0 || len(missingCodes.CustomerCodes) != 0 {
		t.Fatalf("missing own codes must deny reads: %+v, err=%v", missingCodes, err)
	}
	if _, err := resolve("public", "other-tenant-or-unapproved"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("unapproved or cross-tenant customer must fail: %v", err)
	}
	private, err := resolve("private", "")
	if err != nil || private.AgentType != "private" || !private.PersonalZalo {
		t.Fatalf("unexpected private permission: %+v, err=%v", private, err)
	}
	if allowed, scope, groups := private.IsResourceAllowed("inventory"); !allowed || scope != "all" || !reflect.DeepEqual(groups, []string{"mũ"}) {
		t.Fatalf("private inventory = %v/%s/%v, want configured all-scope", allowed, scope, groups)
	}
	if allowed, _, _ := private.IsResourceAllowed("orders"); allowed {
		t.Fatal("private order lookup should remain disabled")
	}
	failPrivateLookup = true
	if _, err := resolve("private", ""); !errors.Is(err, dbFailure) {
		t.Fatalf("failed private permission lookup must fail closed: %v", err)
	}
}

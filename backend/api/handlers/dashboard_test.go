package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestDashboardDateRangeVietnam(t *testing.T) {
	now := time.Date(2026, 9, 16, 3, 0, 0, 0, pkg.VNLocation)
	for _, dates := range [][2]string{{"", ""}, {"2026-09-16", "2026-09-16"}} {
		from, to, err := dashboardDateRange(now, dates[0], dates[1], pkg.VNLocation)
		if err != nil {
			t.Fatal(err)
		}
		if from.Format(time.RFC3339) != "2026-09-15T17:00:00Z" || to.Format(time.RFC3339) != "2026-09-16T17:00:00Z" {
			t.Fatalf("unexpected boundaries: %s to %s", from, to)
		}
	}
	for _, dates := range [][2]string{{"bad", "2026-09-16"}, {"2026-09-16", "bad"}, {"2026-09-17", "2026-09-16"}} {
		if _, _, err := dashboardDateRange(now, dates[0], dates[1], pkg.VNLocation); err == nil {
			t.Fatalf("accepted invalid dates: %v", dates)
		}
	}
}

func TestDashboardDateRangeRespectsTenantDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	from, to, err := dashboardDateRange(time.Now(), "2026-03-08", "2026-03-08", loc)
	if err != nil {
		t.Fatal(err)
	}
	if to.Sub(from) != 23*time.Hour {
		t.Fatalf("DST day should be 23h: %v", to.Sub(from))
	}
}

func TestDashboardQCCountScope(t *testing.T) {
	database, err := gorm.Open(mysql.New(mysql.Config{DSN: "test:test@tcp(localhost:1)/test", SkipInitializeWithVersion: true}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 15, 17, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	var count int64
	stmt := dashboardQCResults(database, "tenant-a", from, to).Count(&count).Statement
	query := stmt.SQL.String()
	if !strings.Contains(query, "count(*)") || !strings.Contains(query, "tenant_id = ? AND result_type = ? AND created_at >= ? AND created_at < ?") {
		t.Fatalf("count must isolate QC violations and use exclusive upper bound: %s", query)
	}
	if len(stmt.Vars) != 4 || stmt.Vars[0] != "tenant-a" || stmt.Vars[1] != "qc_violation" || stmt.Vars[2] != from || stmt.Vars[3] != to {
		t.Fatalf("wrong QC scope: %v", stmt.Vars)
	}
}

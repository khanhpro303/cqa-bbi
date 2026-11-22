package engine

import (
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestIsDKCopy(t *testing.T) {
	cases := []struct {
		ma, maCha string
		want      bool
	}{
		{"SP000556_DK", "SP000557_DK", true},
		{"5039910980050_DK", "SP000557", true},
		{"SP001435", " sp000557_dk ", true},
		{"SP001435", "SP000557", false},
		{"DKSP1", "SP_DKX", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := IsDKCopy(c.ma, c.maCha); got != c.want {
			t.Errorf("IsDKCopy(%q, %q) = %v; want %v", c.ma, c.maCha, got, c.want)
		}
	}
}

func TestRealParentOf(t *testing.T) {
	for in, want := range map[string]string{"SP000557_DK": "SP000557", " SP000557_dk ": "SP000557", "SP000557": "SP000557", "": ""} {
		if got := RealParentOf(in); got != want {
			t.Errorf("RealParentOf(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestDKCopyParentOf(t *testing.T) {
	for in, want := range map[string]string{"SP000557": "SP000557_DK", " SP000557 ": "SP000557_DK", "SP000557_DK": "SP000557_DK", "": ""} {
		if got := DKCopyParentOf(in); got != want {
			t.Errorf("DKCopyParentOf(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestExcludeDKCopiesSQL(t *testing.T) {
	gdb, err := gorm.Open(mysql.New(mysql.Config{DSN: "u:p@tcp(127.0.0.1:1)/x", SkipInitializeWithVersion: true}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open dry-run db: %v", err)
	}
	var rows []models.CachedProduct
	stmt := gdb.Scopes(ExcludeDKCopies).Where("tenant_id = ?", "t").Limit(10).Find(&rows).Statement
	sql := stmt.SQL.String()
	want := "UPPER(TRIM(COALESCE(ma, ''))) NOT LIKE ? ESCAPE '!' AND UPPER(TRIM(COALESCE(ma_cha, ''))) NOT LIKE ? ESCAPE '!'"
	if !strings.Contains(sql, want) {
		t.Fatalf("SQL = %s; want it to contain %s", sql, want)
	}
	n := 0
	for _, v := range stmt.Vars {
		if v == "%!_DK" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("vars = %#v; want the escaped _DK pattern twice", stmt.Vars)
	}
}

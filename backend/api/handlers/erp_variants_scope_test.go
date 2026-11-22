package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/db"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// The products price pivot narrows the variant read to the matched web line
// in SQL, before LIMIT, and always leaves "_DK" copies out.
func TestSearchVariantsByAttributesAppliesLineScopeBeforeLimit(t *testing.T) {
	database, err := gorm.Open(mysql.New(mysql.Config{DSN: "fixture@tcp(127.0.0.1:3306)/fixture", SkipInitializeWithVersion: true}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true})
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
	var sql string
	if err := database.Callback().Query().After("gorm:query").Register("fixture:variants", func(q *gorm.DB) {
		sql = q.Statement.SQL.String()
	}); err != nil {
		t.Fatal(err)
	}

	line := func(tx *gorm.DB) *gorm.DB { return tx.Where("ten_dong_bo_web = ?", "LS2 FF399") }
	if _, err := searchVariantsByAttributes(context.Background(), "t", "SP000557", "Black", "L", "", 1, line); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ten_dong_bo_web = ?", "NOT LIKE ? ESCAPE '!'", "LIMIT"} {
		if !strings.Contains(sql, want) {
			t.Errorf("variant SQL %q lacks %q", sql, want)
		}
	}
	if strings.Index(sql, "ten_dong_bo_web = ?") > strings.Index(sql, "LIMIT") {
		t.Errorf("line scope must come before LIMIT: %s", sql)
	}
}

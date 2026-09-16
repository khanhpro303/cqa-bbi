package models

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestMessengerIntakeAttributionPSIDColumnName(t *testing.T) {
	parsed, err := schema.Parse(&MessengerIntakeAttribution{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}

	field := parsed.LookUpField("PSID")
	if field == nil {
		t.Fatal("PSID field not found")
	}
	if field.DBName != "psid" {
		t.Fatalf("PSID column = %q, want psid", field.DBName)
	}
}

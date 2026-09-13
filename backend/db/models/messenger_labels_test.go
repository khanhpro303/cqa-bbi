package models

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestMessengerLabelSchemaCascadesAndHasStableIdentity(t *testing.T) {
	for _, model := range []interface{}{&MessengerLabelState{}, &MessengerLabelSnapshot{}} {
		s, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatal(err)
		}
		if len(s.PrimaryFields) != 1 {
			t.Fatalf("missing unique identity on %s", s.Table)
		}
		for _, relation := range s.Relationships.Relations {
			constraint := relation.ParseConstraint()
			if constraint == nil || constraint.OnDelete != "CASCADE" {
				t.Fatalf("missing cascade on %s", s.Table)
			}
		}
		if field := s.FieldsByName["PSID"]; field != nil && field.DBName != "psid" {
			t.Fatalf("upsert column mismatch: %s", field.DBName)
		}
	}
}

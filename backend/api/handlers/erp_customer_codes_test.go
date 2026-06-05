package handlers

import (
	"reflect"
	"sort"
	"testing"

	"github.com/vietbui/chat-quality-agent/engine"
)

// TestResolveOwnCustomerCodes covers the token-priority branches: the signed
// CustomerCodes set (owners of multiple shops) wins; otherwise the single
// CustomerCode is used. The CRM-group fallback needs the DB and is exercised by
// integration tests.
func TestResolveOwnCustomerCodes(t *testing.T) {
	tests := []struct {
		name string
		ctx  engine.GroupPermissionContext
		want []string
	}{
		{
			"multi-code token wins",
			engine.GroupPermissionContext{CustomerCodes: []string{"S002", "S001"}, CustomerCode: "S002"},
			[]string{"S001", "S002"}, // dedupeCustomerCodes sorts
		},
		{
			"single code falls back",
			engine.GroupPermissionContext{CustomerCode: "S001"},
			[]string{"S001"},
		},
		{
			"blank single code yields nothing (no DB groups)",
			engine.GroupPermissionContext{},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveOwnCustomerCodes(&tt.ctx, "tenant-x")
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("resolveOwnCustomerCodes() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestOwnCustomerCodesLeading verifies labels ("CODE - Name") are stripped to
// bare codes for per-item authorization comparisons.
func TestOwnCustomerCodesLeading(t *testing.T) {
	ctx := engine.GroupPermissionContext{CustomerCodes: []string{"S001 - Shop A", "S002 - Shop B"}}
	got := ownCustomerCodesLeading(&ctx, "tenant-x")
	sort.Strings(got)
	want := []string{"S001", "S002"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ownCustomerCodesLeading() = %v, want %v", got, want)
	}
}

// TestMergeRequestCustomerCodes covers combining the legacy single field with the
// list form: the single code becomes primary (first), blanks drop, dupes fold.
func TestMergeRequestCustomerCodes(t *testing.T) {
	tests := []struct {
		name   string
		single string
		list   []string
		want   []string
	}{
		{"single only", "S001", nil, []string{"S001"}},
		{"list only", "", []string{"S001", "S002"}, []string{"S001", "S002"}},
		{"single is primary, dedup against list", "S002", []string{"S001", "S002"}, []string{"S002", "S001"}},
		{"drops blanks", "  ", []string{"S001", "", "  "}, []string{"S001"}},
		{"case-insensitive dedup", "s001", []string{"S001"}, []string{"s001"}},
		{"empty", "", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeRequestCustomerCodes(tt.single, tt.list)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeRequestCustomerCodes(%q, %v) = %v, want %v", tt.single, tt.list, got, tt.want)
			}
		})
	}
}

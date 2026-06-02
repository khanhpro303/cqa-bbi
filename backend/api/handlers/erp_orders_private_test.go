package handlers

import (
	"reflect"
	"testing"
)

func TestTokenizeOrdersCustomerQuery(t *testing.T) {
	tests := []struct {
		name   string
		search string
		want   []string
	}{
		{"name after order words", "đơn hàng của khách Huy", []string{"Huy"}},
		{"code with status tail", "đơn hàng của S001 thế nào rồi", []string{"S001"}},
		{"code and name", "đơn hàng S001 Huy", []string{"S001", "Huy"}},
		{"generic only", "đơn hàng", []string{}},
		{"generic with customer word", "đơn hàng của khách hàng", []string{}},
		{"bare code reply", "S001", []string{"S001"}},
		{"empty", "", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenizeOrdersCustomerQuery(tt.search)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tokenizeOrdersCustomerQuery(%q) = %v, want %v", tt.search, got, tt.want)
			}
		})
	}
}

func TestIsPrivateOrdersCustomerQuery(t *testing.T) {
	tests := []struct {
		name   string
		search string
		want   bool
	}{
		{"generic", "đơn hàng", true},
		{"generic of a customer", "đơn hàng của khách hàng", true},
		{"named customer", "đơn hàng của S001", false},
		{"order code", "đơn ĐH000016 sao rồi", false},
		{"date window", "đơn hàng 7 ngày gần đây", false},
		{"all customers reply", "tất cả", false},
		{"empty", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPrivateOrdersCustomerQuery(tt.search); got != tt.want {
				t.Errorf("isPrivateOrdersCustomerQuery(%q) = %v, want %v", tt.search, got, tt.want)
			}
		})
	}
}

func TestIsAllCustomersReply(t *testing.T) {
	tests := []struct {
		name   string
		search string
		want   bool
	}{
		{"tat ca diacritics", "tất cả", true},
		{"toan bo", "toàn bộ", true},
		{"all english", "all", true},
		{"inside longer reply", "cho mình xem tất cả", true},
		{"specific code", "S001", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAllCustomersReply(tt.search); got != tt.want {
				t.Errorf("isAllCustomersReply(%q) = %v, want %v", tt.search, got, tt.want)
			}
		})
	}
}

// TestScopeApprovedOrdersCodes is the core scope-safety test: a named customer
// must be intersected with the staff member's scope so an assigned employee can
// never reach a customer outside their groups, while an all-scope employee can.
func TestScopeApprovedOrdersCodes(t *testing.T) {
	allowed := []string{"S052 - Phượt 4P", "EG05"}
	tests := []struct {
		name      string
		resolved  []string
		scopeType string
		ownCode   string
		allowed   []string
		want      []string
	}{
		{"all keeps resolved", []string{"S001", "S002"}, "all", "", nil, []string{"S001", "S002"}},
		{"all strips labels + dedupes", []string{"S001 - A", "S001"}, "all", "", nil, []string{"S001"}},
		{"assigned in group", []string{"S052"}, "assigned", "", allowed, []string{"S052"}},
		{"assigned out of group", []string{"S999"}, "assigned", "", allowed, []string{}},
		{"assigned mixed keeps only in-group", []string{"S052", "S999", "EG05"}, "assigned", "", allowed, []string{"EG05", "S052"}},
		{"assigned case-insensitive", []string{"s052"}, "assigned", "", allowed, []string{"s052"}},
		{"own matches own", []string{"S001"}, "own", "S001", nil, []string{"S001"}},
		{"own rejects other", []string{"S002"}, "own", "S001", nil, []string{}},
		{"unknown scope denies", []string{"S001"}, "weird", "", allowed, []string{}},
		{"empty resolved", nil, "all", "", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scopeApprovedOrdersCodes(tt.resolved, tt.scopeType, tt.ownCode, tt.allowed)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("scopeApprovedOrdersCodes(%v, %q) = %v, want %v", tt.resolved, tt.scopeType, got, tt.want)
			}
		})
	}
}

func TestCodesContain(t *testing.T) {
	codes := []string{"S001", "EG05 - Tên"}
	cases := []struct {
		target string
		want   bool
	}{
		{"S001", true},
		{"s001", true},
		{"EG05", true},
		{"EG05 - Tên", true},
		{"S999", false},
		{"", false},
	}
	for _, c := range cases {
		if got := codesContain(codes, c.target); got != c.want {
			t.Errorf("codesContain(%v, %q) = %v, want %v", codes, c.target, got, c.want)
		}
	}
}

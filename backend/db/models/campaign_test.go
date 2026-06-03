package models

import (
	"reflect"
	"testing"
)

func TestJSONStringSliceScan(t *testing.T) {
	tests := []struct {
		name    string
		input   any
		want    JSONStringSlice
		wantErr bool
	}{
		{name: "nil", input: nil, want: nil},
		{name: "empty bytes", input: []byte(""), want: nil},
		{name: "empty array bytes", input: []byte("[]"), want: JSONStringSlice{}},
		{name: "array bytes", input: []byte(`["a/1.jpg","b/2.png"]`), want: JSONStringSlice{"a/1.jpg", "b/2.png"}},
		{name: "array string", input: `["x.webp"]`, want: JSONStringSlice{"x.webp"}},
		{name: "bad json", input: []byte("{not json"), wantErr: true},
		{name: "unsupported type", input: 42, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got JSONStringSlice
			err := got.Scan(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestJSONStringSliceValue(t *testing.T) {
	tests := []struct {
		name string
		in   JSONStringSlice
		want string
	}{
		{name: "nil -> empty array", in: nil, want: "[]"},
		{name: "empty -> empty array", in: JSONStringSlice{}, want: "[]"},
		{name: "values", in: JSONStringSlice{"a.jpg", "b.png"}, want: `["a.jpg","b.png"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.in.Value()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.(string) != tt.want {
				t.Fatalf("got %q, want %q", v, tt.want)
			}
		})
	}
}

func TestCampaignImages(t *testing.T) {
	tests := []struct {
		name string
		c    Campaign
		want []string
	}{
		{
			name: "new array preferred",
			c:    Campaign{MessageImages: JSONStringSlice{"new/1.jpg"}, MessageImageName: "legacy.jpg"},
			want: []string{"new/1.jpg"},
		},
		{
			name: "legacy fallback when array empty",
			c:    Campaign{MessageImageName: "legacy.jpg"},
			want: []string{"legacy.jpg"},
		},
		{
			name: "none",
			c:    Campaign{},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.Images(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

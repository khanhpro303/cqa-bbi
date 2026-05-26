package workers

import (
	"encoding/json"
	"testing"
)

func TestParseClickPayload(t *testing.T) {
	payloadStr := `{"MA_CHA":"FF800","WEB_NAME":"Áo gió"}`
	var parsed struct {
		MaCha   string `json:"MA_CHA"`
		WebName string `json:"WEB_NAME"`
	}
	err := json.Unmarshal([]byte(payloadStr), &parsed)
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}
	if parsed.MaCha != "FF800" {
		t.Errorf("Expected MA_CHA FF800, got %s", parsed.MaCha)
	}
	if parsed.WebName != "Áo gió" {
		t.Errorf("Expected WEB_NAME Áo gió, got %s", parsed.WebName)
	}
}

func TestMockInventoryAggregation(t *testing.T) {
	// Simple test to ensure mapping and key retrieval logic works
	item := map[string]interface{}{
		"code": "FF800-R-L",
		"ton":  15.0,
	}

	code := getMapString(item, "code", "ma_hang", "ma")
	stock := getMapFloat(item, "stock", "ton", "ton_kho")

	if code != "FF800-R-L" {
		t.Errorf("getMapString failed, got: %s", code)
	}
	if stock != 15.0 {
		t.Errorf("getMapFloat failed, got: %f", stock)
	}
}

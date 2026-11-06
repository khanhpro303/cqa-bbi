package messengerjourney

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestValidateOrderPayloadRejectsFeedPoison(t *testing.T) {
	now := time.Now().UTC()
	valid := map[string]any{"order_id": "order-1", "order_ref": "O-1", "value_vnd": 100000, "status": "confirmed", "confidence": 0.9, "ordered_at": now.Add(-time.Minute).Format(time.RFC3339Nano), "evidence_message_ids": []string{"message-1"}}
	tests := []struct {
		name, key string
		value     any
	}{
		{"unknown status", "status", "paid"}, {"missing reference", "order_ref", ""}, {"reference too long", "order_ref", strings.Repeat("x", 161)},
		{"ID too long", "order_id", strings.Repeat("x", 129)}, {"negative amount", "value_vnd", -1}, {"large amount", "value_vnd", 1_000_000_000_001},
		{"bad confidence", "confidence", 1.1}, {"null confidence", "confidence", nil}, {"future purchase", "ordered_at", now.Add(time.Hour).Format(time.RFC3339Nano)}, {"bad date", "ordered_at", "invalid"},
		{"invalid evidence", "evidence_message_ids", []string{strings.Repeat("x", 201)}}, {"confirmed missing amount", "value_vnd", nil},
	}
	body, _ := json.Marshal(valid)
	if err := ValidateOrderPayload(body, now); err != nil {
		t.Fatal("valid payload rejected", err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := map[string]any{}
			for k, v := range valid {
				value[k] = v
			}
			value[test.key] = test.value
			body, _ := json.Marshal(value)
			if ValidateOrderPayload(body, now) == nil {
				t.Fatal("malformed order accepted")
			}
		})
	}
	valid["status"] = "review"
	valid["value_vnd"] = nil
	valid["evidence_message_ids"] = []string{}
	body, _ = json.Marshal(valid)
	if err := ValidateOrderPayload(body, now); err != nil {
		t.Fatal("review incorrectly rejected", err)
	}
}

func TestReferralNaturalIdentityNormalizesSourcesWithoutDroppingContext(t *testing.T) {
	base := RecordInput{BaseInput: BaseInput{TenantID: "tenant", PageID: "123", PSID: "456", OccurredAt: time.Now().UTC()}, Kind: KindReferral, Payload: json.RawMessage(`{"ad_id":"9","ref":null,"source":"ADS","type":"OPEN_THREAD","message_id":"m1","ads_context":{"post_id":"post-1"}}`)}
	first := *referralNaturalKey(base)
	base.Payload = json.RawMessage(`{"ad_id":"9","ref":"","source":"ADS","referral_type":"OPEN_THREAD","message_id":"m1","referral":{"ads_context_data":{"post_id":"post-1"}}}`)
	if *referralNaturalKey(base) != first {
		t.Fatal("same source fields produced different identities")
	}
	base.Payload = json.RawMessage(`{"ad_id":"9","ref":null,"source":"ADS","type":"OPEN_THREAD","message_id":"m1","ads_context":{"post_id":"post-2"}}`)
	if *referralNaturalKey(base) == first {
		t.Fatal("distinct ad context collapsed")
	}
}

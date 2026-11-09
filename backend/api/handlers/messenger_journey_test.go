package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerjourney"
)

func stringPointer(value string) *string { return &value }

func validRelayFixture() relayReferralEvent {
	return relayReferralEvent{
		EventID: "referral:abc-123", PageID: "100", PSID: "200", AdID: stringPointer("300"),
		Source: stringPointer("ADS"), Type: stringPointer("OPEN_THREAD"), MessageID: stringPointer("mid.1"),
		AdsContext: json.RawMessage(`{"ad_title":"Helmet","post_id":"400"}`),
		ReferredAt: "2026-11-06T01:02:03Z", ReceivedAt: "2026-11-06T01:02:04Z",
	}
}

func TestValidateRelayReferralAcceptsSanitizedPayload(t *testing.T) {
	event := validRelayFixture()
	_, _, payload, ok := validateRelayReferral(event)
	if !ok || !json.Valid(payload) {
		t.Fatalf("valid relay rejected: ok=%v payload=%s", ok, payload)
	}
}

func TestValidateRelayReferralRejectsUnsanitizedOrMeaninglessPayload(t *testing.T) {
	for name, mutate := range map[string]func(*relayReferralEvent){
		"unknown ads field":    func(e *relayReferralEvent) { e.AdsContext = json.RawMessage(`{"customer_phone":"0909"}`) },
		"non-string ads field": func(e *relayReferralEvent) { e.AdsContext = json.RawMessage(`{"post_id":123}`) },
		"same page and psid":   func(e *relayReferralEvent) { e.PSID = e.PageID },
		"bad ad id":            func(e *relayReferralEvent) { e.AdID = stringPointer("ad-300") },
		"no ADS or ref":        func(e *relayReferralEvent) { e.Source = stringPointer("OTHER") },
		"whitespace source":    func(e *relayReferralEvent) { e.Source = stringPointer(" ADS ") },
	} {
		t.Run(name, func(t *testing.T) {
			event := validRelayFixture()
			mutate(&event)
			if _, _, _, ok := validateRelayReferral(event); ok {
				t.Fatal("unsafe relay was accepted")
			}
		})
	}
}

func TestJourneyCursorIsBoundToSortedPageScope(t *testing.T) {
	scope := journeyScope("tenant-a", []string{"200", "100"}, "events")
	if scope != journeyScope("tenant-a", []string{"100", "200"}, "events") {
		t.Fatal("scope depends on caller page ordering")
	}
	raw := encodeJourneyCursor(journeyCursor{Version: 1, Scope: scope, Sequence: 42}, "cursor-secret")
	cursor, ok := decodeJourneyCursor(raw, scope, "cursor-secret")
	if !ok || cursor.Sequence != 42 {
		t.Fatalf("cursor roundtrip failed: %#v ok=%v", cursor, ok)
	}
	if _, ok := decodeJourneyCursor(raw, journeyScope("tenant-a", []string{"100"}, "events"), "cursor-secret"); ok {
		t.Fatal("cursor was accepted after narrowing Page scope")
	}
	if _, ok := decodeJourneyCursor(raw, journeyScope("tenant-b", []string{"100", "200"}, "events"), "cursor-secret"); ok {
		t.Fatal("cursor crossed tenant scope")
	}
	parts := strings.Split(raw, ".")
	var decoded journeyCursor
	payload, _ := base64.RawURLEncoding.DecodeString(parts[0])
	_ = json.Unmarshal(payload, &decoded)
	decoded.Watermark = 999999
	forged, _ := json.Marshal(decoded)
	if _, ok := decodeJourneyCursor(base64.RawURLEncoding.EncodeToString(forged)+"."+parts[1], scope, "cursor-secret"); ok {
		t.Fatal("cursor watermark was forgeable")
	}
}

func TestLabelDisplayNamesDoesNotExposeMetaObjects(t *testing.T) {
	got := labelDisplayNames(`[{"id":"1","page_label_name":"Potential"},{"id":"2","page_label_name":"Qualified"}]`)
	if len(got) != 2 || got[0] != "Potential" || got[1] != "Qualified" {
		t.Fatalf("unexpected names: %#v", got)
	}
	if got := labelDisplayNames(`{"bad":true}`); len(got) != 0 {
		t.Fatalf("invalid labels were exposed: %#v", got)
	}
}

func TestJourneyEventCanonicalWireContract(t *testing.T) {
	base := models.MessengerJourneyEvent{
		Sequence: 7, EventID: "event-7", PageID: "100", SubjectKey: "opaque-subject", PSID: "200",
		Source: "cqa", OccurredAt: time.Unix(1, 0).UTC(), ObservedAt: time.Unix(2, 0).UTC(),
	}
	referral := base
	referral.Kind = messengerjourney.KindReferral
	referral.Payload = `{"ad_id":null,"type":"OPEN_THREAD","source":"ADS","ads_context":{}}`
	labels := base
	labels.Kind = messengerjourney.KindLabels
	labels.Payload = `[{"id":"1","page_label_name":"Potential"}]`
	classification := base
	classification.Kind = messengerjourney.KindClassification
	classification.PolicyVersion = "policy-2"
	classification.Payload = `{"classification":"potential","status":"success"}`
	order := base
	order.Kind = messengerjourney.KindOrder
	order.EntityID = "order-1"
	order.Payload = `{"order_id":"order-1","order_ref":"ORD-1","status":"confirmed","value_vnd":120000,"confidence":0.9,"evidence_message_ids":["mid.1"]}`
	deletion := base
	deletion.Kind = messengerjourney.KindDeletion
	deletion.PSID = ""
	deletion.Payload = `{}`

	fixtures := map[string]journeyEventResponse{
		"referral": journeyEvent(referral), "labels": journeyEvent(labels),
		"classification": journeyEvent(classification), "order": journeyEvent(order), "deletion": journeyEvent(deletion),
	}
	for name, event := range fixtures {
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("%s marshal: %v", name, err)
		}
		text := string(encoded)
		if !strings.Contains(text, `"kind":"`+event.Kind+`"`) || !strings.Contains(text, `"subject_id":"opaque-subject"`) || !strings.Contains(text, `"version":7`) || strings.Contains(text, `"type":`) || strings.Contains(text, `"subject_key"`) {
			t.Fatalf("%s is not canonical: %s", name, text)
		}
		if name == "referral" && !strings.Contains(text, `"referral_type":"OPEN_THREAD"`) {
			t.Fatalf("referral type was not normalized: %s", text)
		}
		if name == "labels" && (!strings.Contains(text, `"labels":[`) || !strings.Contains(text, `"status":"success"`)) {
			t.Fatalf("label payload is not an object: %s", text)
		}
		if name == "labels" && (!strings.Contains(text, `"name":"Potential"`) || strings.Contains(text, `page_label_name`)) {
			t.Fatalf("label fields were not normalized: %s", text)
		}
		if name == "deletion" && (strings.Contains(text, `"psid"`) || !strings.Contains(text, `"payload":{}`)) {
			t.Fatalf("deletion exposed identifiers or omitted tombstone payload: %s", text)
		}
	}

	scrubbed := base
	scrubbed.PSID = ""
	scrubbed.Kind = messengerjourney.KindReferral
	if exportableJourneyEvent(scrubbed, legacyJourneyKinds) {
		t.Fatal("scrubbed pre-deletion event would block downstream ingestion")
	}
	scrubbed.Kind = messengerjourney.KindDeletion
	if !exportableJourneyEvent(scrubbed, legacyJourneyKinds) {
		t.Fatal("privacy tombstone was suppressed")
	}
	coverage := base
	coverage.Kind = messengerjourney.KindCoverage
	if exportableJourneyEvent(coverage, legacyJourneyKinds) {
		t.Fatal("coverage event escaped into the canonical business-event feed")
	}
	insight := base
	insight.Kind = messengerjourney.KindInsight
	if exportableJourneyEvent(insight, legacyJourneyKinds) {
		t.Fatal("insight event reached a replica that did not ask for it")
	}
	if !exportableJourneyEvent(insight, []string{messengerjourney.KindDeletion, messengerjourney.KindInsight}) {
		t.Fatal("insight event was hidden from a replica that asked for it")
	}
}

func TestJourneyKindsQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	read := func(query string) ([]string, bool) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/events"+query, nil)
		return journeyKinds(c)
	}
	if kinds, ok := read(""); !ok || strings.Join(kinds, ",") != "referral,labels,classification,order,deletion" {
		t.Fatalf("default kinds = %v %v", kinds, ok)
	}
	if kinds, ok := read("?kinds=insight"); !ok || strings.Join(kinds, ",") != "deletion,insight" {
		t.Fatalf("insight-only kinds must still carry deletions: %v %v", kinds, ok)
	}
	if kinds, ok := read("?kinds=referral,insight,referral"); !ok || strings.Join(kinds, ",") != "deletion,referral,insight" {
		t.Fatalf("duplicate kinds = %v %v", kinds, ok)
	}
	for _, bad := range []string{"?kinds=coverage", "?kinds=insight,", "?kinds=chat"} {
		if _, ok := read(bad); ok {
			t.Fatalf("%s must be rejected", bad)
		}
	}
}

func TestCoverageFreshnessUsesAnalyzedSourceMessage(t *testing.T) {
	want := time.Date(2026, 11, 6, 1, 2, 3, 0, time.UTC)
	item := models.MessengerJourneyCoverage{ObservedAt: want.Add(time.Hour), Payload: `{"status":"complete","source_last_message_at":"2026-11-06T01:02:03Z","source_last_message_id":"message-a","source_message_count":2}`}
	got, ok := coverageSourceCursor(item)
	if !ok || got.SourceLastMessageAt == nil || !got.SourceLastMessageAt.Equal(want) || got.SourceLastMessageID != "message-a" || got.SourceMessageCount != 2 {
		t.Fatalf("got %#v ok=%v", got, ok)
	}
	if coverageMatchesSource(got, journeyMessageCursor{SourceLastMessageAt: &want, SourceLastMessageID: "message-b", SourceMessageCount: 2}) {
		t.Fatal("coverage with the same timestamp but a newer deterministic message ID was treated as complete")
	}
	if coverageMatchesSource(got, journeyMessageCursor{SourceLastMessageAt: &want, SourceLastMessageID: "message-a", SourceMessageCount: 3}) {
		t.Fatal("coverage with a different message count was treated as complete")
	}
	if !coverageMatchesSource(got, journeyMessageCursor{SourceLastMessageAt: &want, SourceLastMessageID: "message-a", SourceMessageCount: 2}) {
		t.Fatal("exact source cursor was not treated as complete")
	}
}

func TestMySQLRelayRetryAfterErasureDoesNotRecreateLegacyIntake(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(
		&models.Conversation{}, &models.MessengerLabelSnapshot{}, &models.MessengerIntakeAttribution{}, &models.MessengerJourneySequence{},
		&models.MessengerJourneyReceipt{}, &models.MessengerJourneyEvent{},
		&models.MessengerJourneyCustomer{}, &models.MessengerJourneyOrder{},
		&models.MessengerJourneyCoverage{}, &models.MessengerJourneyErasure{},
	); err != nil {
		t.Fatal(err)
	}
	createFacebookOAuthTenant(t, database, "tenant-relay")
	now := time.Now().UTC().Truncate(time.Millisecond)
	channel := models.Channel{
		ID: "channel-relay", TenantID: "tenant-relay", ChannelType: "facebook", Name: "Page",
		ExternalID: "100", IsActive: true, CredentialsEncrypted: []byte("x"), Metadata: "{}",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}

	event := validRelayFixture()
	event.ReferredAt = now.Add(-time.Minute).Format(time.RFC3339Nano)
	event.ReceivedAt = now.Format(time.RFC3339Nano)
	body, err := json.Marshal(relayReferralRequest{Events: []relayReferralEvent{event}})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("tenant_id", "tenant-relay") })
	router.POST("/api/internal/messenger/referral-events", IngestMessengerReferralEvents)
	post := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/internal/messenger/referral-events", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		return recorder
	}

	if response := post(); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), event.EventID) {
		t.Fatalf("initial relay response=%d %s", response.Code, response.Body.String())
	}
	var initial int64
	if err := database.Model(&models.MessengerIntakeAttribution{}).Where("tenant_id = ? AND psid = ?", "tenant-relay", event.PSID).Count(&initial).Error; err != nil || initial != 1 {
		t.Fatalf("initial intake rows=%d error=%v", initial, err)
	}
	if _, err := messengerjourney.EraseSubject(context.Background(), database, messengerjourney.EraseInput{
		EventID: "privacy-request", TenantID: "tenant-relay", PageID: event.PageID,
		PSID: event.PSID, Source: "test", ObservedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Where("tenant_id = ? AND psid = ?", "tenant-relay", event.PSID).Delete(&models.MessengerIntakeAttribution{}).Error; err != nil {
		t.Fatal(err)
	}

	if response := post(); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), event.EventID) {
		t.Fatalf("erased retry response=%d %s", response.Code, response.Body.String())
	}
	var recreated int64
	if err := database.Model(&models.MessengerIntakeAttribution{}).Where("tenant_id = ? AND psid = ?", "tenant-relay", event.PSID).Count(&recreated).Error; err != nil || recreated != 0 {
		t.Fatalf("erased retry recreated legacy intake rows=%d error=%v", recreated, err)
	}
	var piiEvents int64
	if err := database.Model(&models.MessengerJourneyEvent{}).Where("tenant_id = ? AND psid = ?", "tenant-relay", event.PSID).Count(&piiEvents).Error; err != nil || piiEvents != 0 {
		t.Fatalf("erased retry recreated journey PII rows=%d error=%v", piiEvents, err)
	}
}

func TestMySQLJourneyMessageCursorUsesDeterministicLatestID(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.Conversation{}, &models.Message{}); err != nil {
		t.Fatal(err)
	}
	createFacebookOAuthTenant(t, database, "tenant-cursor")
	now := time.Now().UTC().Truncate(time.Millisecond)
	channel := models.Channel{ID: "channel-cursor", TenantID: "tenant-cursor", ChannelType: "facebook", Name: "Page", ExternalID: "500", IsActive: true, CredentialsEncrypted: []byte("x"), Metadata: "{}", CreatedAt: now, UpdatedAt: now}
	conversation := models.Conversation{ID: "conversation-cursor", TenantID: "tenant-cursor", ChannelID: channel.ID, ExternalConversationID: "external-cursor", Metadata: "{}", CreatedAt: now, UpdatedAt: now}
	if err := database.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"message-a", "message-b"} {
		message := models.Message{ID: id, TenantID: "tenant-cursor", ConversationID: conversation.ID, ExternalMessageID: "external-" + id, SenderType: "customer", Attachments: "[]", RawData: "{}", SentAt: now, CreatedAt: now}
		if err := database.Create(&message).Error; err != nil {
			t.Fatal(err)
		}
	}
	cursors, err := loadJourneyMessageCursors(context.Background(), database, "tenant-cursor", []string{conversation.ID})
	if err != nil {
		t.Fatal(err)
	}
	cursor := cursors[conversation.ID]
	if cursor.SourceMessageCount != 2 || cursor.SourceLastMessageID != "message-b" || cursor.SourceLastMessageAt == nil || !cursor.SourceLastMessageAt.Equal(now) {
		t.Fatalf("unexpected deterministic source cursor: %#v", cursor)
	}
}

func TestFacebookReferralSourceSecondsAndMilliseconds(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, raw := range []int64{now.Unix(), now.UnixMilli()} {
		got := facebookReferralTime(raw, now)
		if got.Year() != now.Year() || now.Sub(got) > time.Second {
			t.Fatalf("source time lost units: %v", got)
		}
	}
	for _, raw := range []int64{0, -1, now.Add(24 * time.Hour).UnixMilli(), 1} {
		if !facebookReferralTime(raw, now).Equal(now) {
			t.Fatal("invalid source time should use observed receipt")
		}
	}
}

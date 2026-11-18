package cloudifyorders

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

// sampleRow is the documented saorders/search row shape.
func sampleRow(id int, code, status string) map[string]any {
	raw := `{
		"id": ` + itoa(id) + `,
		"MA_HOA_DON": "` + code + `",
		"THOI_GIAN_TAO": "2026-11-18",
		"MA_KHACH_HANG": [395, "KH125 - Shop A"],
		"TEN_KHACH_HANG": "Shop A",
		"SO_DIEN_THOAI_KH": false,
		"NGUOI_BAN": [3, "Administrator"],
		"TRANG_THAI": "` + status + `",
		"GHI_CHU": false,
		"GIAM_GIA_HOA_DON": 10000.0,
		"DON_DAT_HANG_CHI_TIET": [
			{"MA_HANG": [834, "AQ12-7.5"], "TEN_HANG": "Bình ắc quy", "DVT": [39, "Bình"], "SO_LUONG": 6.0, "DON_GIA": 485000.0}
		]
	}`
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		panic(err)
	}
	return m
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestParseOrder(t *testing.T) {
	order, ok := ParseOrder(sampleRow(16, "ĐH000016", "1"))
	if !ok {
		t.Fatal("expected a valid order")
	}
	if order.ID != 16 || order.Code != "ĐH000016" || order.CustomerCode != "KH125" || order.Status != "1" || order.Date != "2026-11-18" {
		t.Fatalf("unexpected order: %+v", order)
	}
	if order.Total != 6*485000-10000 {
		t.Fatalf("total = %v", order.Total)
	}
	if _, ok := ParseOrder(map[string]any{"MA_HOA_DON": "x"}); ok {
		t.Fatal("row without id must be rejected")
	}
}

func TestFingerprintStableAndSensitive(t *testing.T) {
	a := Fingerprint(sampleRow(1, "ĐH1", "1"))
	if a != Fingerprint(sampleRow(1, "ĐH1", "1")) {
		t.Fatal("same row must hash the same")
	}
	edited := sampleRow(1, "ĐH1", "1")
	edited["DON_DAT_HANG_CHI_TIET"].([]any)[0].(map[string]any)["SO_LUONG"] = 7.0
	if a == Fingerprint(edited) {
		t.Fatal("a quantity edit must change the fingerprint")
	}
}

func parsed(t *testing.T, row map[string]any) Order {
	t.Helper()
	o, ok := ParseOrder(row)
	if !ok {
		t.Fatal("bad row")
	}
	return o
}

func TestDiffKinds(t *testing.T) {
	same := parsed(t, sampleRow(1, "ĐH1", "1"))
	status := parsed(t, sampleRow(2, "ĐH2", "3"))
	edited := parsed(t, sampleRow(3, "ĐH3", "1"))
	edited.Fingerprint = "changed"
	fresh := parsed(t, sampleRow(4, "ĐH4", "1"))
	prev := map[int64]Previous{
		1: {Status: "1", Fingerprint: same.Fingerprint},
		2: {Status: "1", Fingerprint: "old2"},
		3: {Status: "1", Fingerprint: "old3"},
	}
	changes, updates := Diff(prev, []Order{same, status, edited, fresh, fresh}, DiffOptions{})
	if len(updates) != 3 {
		t.Fatalf("updates = %d, want 3 (unchanged and duplicate rows skipped)", len(updates))
	}
	want := map[int64]string{2: KindStatusChanged, 3: KindUpdated, 4: KindCreated}
	if len(changes) != len(want) {
		t.Fatalf("changes = %+v", changes)
	}
	for _, c := range changes {
		if want[c.Order.ID] != c.Kind {
			t.Fatalf("order %d kind %s, want %s", c.Order.ID, c.Kind, want[c.Order.ID])
		}
	}
	if changes[0].PrevStatus != "1" || changes[0].PrevFingerprint != "old2" {
		t.Fatalf("previous state not carried: %+v", changes[0])
	}
}

func TestDiffSeedingAndCutoff(t *testing.T) {
	order := parsed(t, sampleRow(1, "ĐH1", "1"))
	changes, updates := Diff(nil, []Order{order}, DiffOptions{Seeding: true})
	if len(changes) != 0 || len(updates) != 1 {
		t.Fatal("seeding must store without events")
	}
	cutoff := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	changes, _ = Diff(nil, []Order{order}, DiffOptions{CreatedCutoff: cutoff})
	if len(changes) != 0 {
		t.Fatal("an order dated before the cutoff is backfill, not new")
	}
	changes, _ = Diff(nil, []Order{order}, DiffOptions{CreatedCutoff: cutoff.AddDate(0, 0, -2)})
	if len(changes) != 1 || changes[0].Kind != KindCreated {
		t.Fatal("an order dated on/after the cutoff is new")
	}
	order.Date = "garbage"
	changes, _ = Diff(nil, []Order{order}, DiffOptions{CreatedCutoff: cutoff})
	if len(changes) != 1 {
		t.Fatal("unparseable dates must not drop a new order")
	}
}

func TestDedupKeyPerTransition(t *testing.T) {
	order := parsed(t, sampleRow(1, "ĐH1", "2"))
	a := DedupKey("t1", Change{Order: order, PrevFingerprint: "x"})
	if a != DedupKey("t1", Change{Order: order, PrevFingerprint: "x", Kind: KindUpdated}) {
		t.Fatal("same transition must share a key")
	}
	if a == DedupKey("t1", Change{Order: order, PrevFingerprint: "y"}) || a == DedupKey("t2", Change{Order: order, PrevFingerprint: "x"}) {
		t.Fatal("different transitions or tenants must differ")
	}
}

func TestParseSettingsDefaultsAndLimits(t *testing.T) {
	s := ParseSettings(map[string]string{})
	if s.WatchEnabled || s.NotifyEnabled {
		t.Fatal("everything must be off by default")
	}
	if !s.NotifyKinds[KindCreated] || !s.NotifyKinds[KindStatusChanged] || s.NotifyKinds[KindUpdated] {
		t.Fatalf("default kinds = %v", s.NotifyKinds)
	}
	if s.WindowDays != DefaultWindowDays || s.PollInterval != 30*time.Second {
		t.Fatalf("defaults = %+v", s)
	}
	s = ParseSettings(map[string]string{KeyWindowDays: "999", KeyPollSeconds: "1", KeyNotifyKinds: "none", KeyWatchEnabled: "true"})
	if s.WindowDays != MaxWindowDays || s.PollInterval != 15*time.Second || len(s.NotifyKinds) != 0 || !s.WatchEnabled {
		t.Fatalf("limits not applied: %+v", s)
	}
}

func TestRender(t *testing.T) {
	event := models.CloudifyOrderEvent{OrderCode: "ĐH000016", CustomerName: "Shop A", Status: "3", PrevStatus: "1", Total: 2900000, OrderDate: "2026-11-18"}
	got := Render("{ma_don}|{khach_hang}|{trang_thai_cu}->{trang_thai}|{tong_tien}|{ngay}", event)
	want := "ĐH000016|Shop A|Đang thực hiện->Đang giao|2.900.000đ|18/11/2026"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if FormatVND(999) != "999đ" || FormatVND(1000) != "1.000đ" || FormatVND(-1234567) != "-1.234.567đ" {
		t.Fatal("FormatVND")
	}
}

func TestOrdersEndpoint(t *testing.T) {
	cases := map[string]string{
		"":                             DefaultOrdersEndpoint,
		"not json":                     DefaultOrdersEndpoint,
		`{"orders":{"path":"orders"}}`: DefaultOrdersEndpoint,
		`{"orders":{"path":"/rest_api/private/custom/so"}}`: "custom/so",
	}
	for raw, want := range cases {
		if got := ordersEndpoint(raw); got != want {
			t.Fatalf("ordersEndpoint(%q) = %q want %q", raw, got, want)
		}
	}
}

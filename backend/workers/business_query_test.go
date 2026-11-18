package workers

import "testing"

func TestIsObviousBusinessQuery(t *testing.T) {
	yes := []string{"FF805 còn tồn bao nhiêu á", "ff805", "giá LS2 FF901 bao nhiêu", "SP000123", "công nợ tháng này", "đơn hàng của tôi"}
	no := []string{"ok", "dạ", "cảm ơn", "hello", "chào bull", ""}
	for _, s := range yes {
		if !isObviousBusinessQuery(s) {
			t.Errorf("expected business query: %q", s)
		}
	}
	for _, s := range no {
		if isObviousBusinessQuery(s) {
			t.Errorf("expected NOT business query: %q", s)
		}
	}
}

func TestIsLangflowConfigError(t *testing.T) {
	if !isLangflowConfigError(errString(`langflow api error (status 403): {"detail":"Invalid or missing API key"}`)) {
		t.Error("403 should be a config error")
	}
	if isLangflowConfigError(errString("langflow api error (status 502): bad gateway")) {
		t.Error("502 should be retryable")
	}
	if isLangflowConfigError(nil) {
		t.Error("nil is not an error")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

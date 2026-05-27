package handlers

import (
	"fmt"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/pkg"
)

// isGenericDebtSearch reports whether the search string is one of the
// catch-all "show me my debt" phrases (no specific period or customer
// code). Folds the accent-stripped variants Zalo customers commonly type.
func isGenericDebtSearch(search string) bool {
	s := strings.ToLower(strings.TrimSpace(search))
	if s == "" || s == "công nợ" || s == "cong no" || s == "xem công nợ" || s == "xem cong no" || s == "check công nợ" || s == "check cong no" || s == "tra cứu công nợ" || s == "tra cuu cong no" || s == "đối chiếu công nợ" || s == "doi chieu cong no" || s == "nợ" || s == "no" {
		return true
	}
	return false
}

// parseDebtPeriodFromSearch turns a natural-language window ("tháng này",
// "tháng trước", "quý này", accent-stripped variants) into ISO date
// bounds. Returns ok=false when no window is mentioned so the caller can
// apply its own default.
func parseDebtPeriodFromSearch(search string) (tuNgay, denNgay string, ok bool) {
	s := strings.ToLower(search)
	now := time.Now()

	if strings.Contains(s, "tháng này") || strings.Contains(s, "thang nay") {
		tuNgay = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
		denNgay = now.Format("2006-01-02")
		return tuNgay, denNgay, true
	}
	if strings.Contains(s, "tháng trước") || strings.Contains(s, "thang truoc") {
		firstOfThisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		lastMonth := firstOfThisMonth.AddDate(0, -1, 0)
		tuNgay = time.Date(lastMonth.Year(), lastMonth.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
		denNgay = firstOfThisMonth.AddDate(0, 0, -1).Format("2006-01-02")
		return tuNgay, denNgay, true
	}
	if strings.Contains(s, "quý này") || strings.Contains(s, "quy nay") {
		currentMonth := int(now.Month())
		startMonth := ((currentMonth-1)/3)*3 + 1
		tuNgay = time.Date(now.Year(), time.Month(startMonth), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
		denNgay = now.Format("2006-01-02")
		return tuNgay, denNgay, true
	}

	return "", "", false
}

// mapDebtItemForLLM projects a raw ERP partner-ledger row into the canonical
// shape the LLM consumes: customer code/name + opening/closing balances (VND
// and original currency). Alias keys are folded so the LLM never has to know
// the legacy NO_TRUOC / NO_SAU names. All other ERP fields are preserved as
// pass-through to keep debugging/aux data available.
func mapDebtItemForLLM(item map[string]interface{}) map[string]interface{} {
	noDuDauKy := getMapFloat(item, "NO_SO_DU_DAU_KY", "no_so_du_dau_ky", "NO_TRUOC", "no_truoc")
	noDuCuoiKy := getMapFloat(item, "NO_SO_DU_CUOI_KY", "no_so_du_cuoi_ky", "NO_SAU", "no_sau")
	noDuCuoiKyNT := getMapFloat(item, "NO_SO_DU_CUOI_KY_NGUYEN_TE", "no_so_du_cuoi_ky_nguyen_te")
	mapped := map[string]interface{}{
		"MA_KHACH_HANG":              getMapString(item, "MA_KHACH_HANG", "ma_khach_hang", "MA_KH", "ma_kh"),
		"TEN_KHACH_HANG":             getMapString(item, "TEN_KHACH_HANG", "ten_khach_hang", "TEN_KH", "ten_kh"),
		"NO_SO_DU_DAU_KY":            noDuDauKy,
		"no_so_du_dau_ky":            noDuDauKy,
		"NO_SO_DU_CUOI_KY":           noDuCuoiKy,
		"no_so_du_cuoi_ky":           noDuCuoiKy,
		"NO_SO_DU_CUOI_KY_NGUYEN_TE": noDuCuoiKyNT,
		"no_so_du_cuoi_ky_nguyen_te": noDuCuoiKyNT,
	}
	for k, v := range item {
		if _, exists := mapped[k]; !exists {
			mapped[k] = v
		}
	}
	return mapped
}

// resolveCustomerCodeFromPartnerID looks a partner up by its ERP ID and
// returns the customer code (MA) used by debt / order queries. Tries an
// exact ID match first; falls back to the first row when ERP returns
// fuzzy results so a near-match still resolves a code.
func resolveCustomerCodeFromPartnerID(client *pkg.CloudifyClient, partnerID string) (string, error) {
	if partnerID == "" {
		return "", fmt.Errorf("partner ID is empty")
	}
	partners, err := client.SearchPartners(partnerID, 5)
	if err != nil {
		return "", err
	}
	for _, p := range partners {
		idVal := getMapString(p, "ID", "id")
		if idVal == partnerID {
			maVal := getMapString(p, "MA", "code", "ma")
			if maVal != "" {
				return maVal, nil
			}
		}
	}
	if len(partners) > 0 {
		maVal := getMapString(partners[0], "MA", "code", "ma")
		if maVal != "" {
			return maVal, nil
		}
	}
	return "", fmt.Errorf("partner %s not found on ERP", partnerID)
}

# Spec: Numbered-reply cho bước chọn dòng (`ten_dong_bo_web`) trong nhánh inventory

**Ngày:** 2026-05-29
**Phạm vi:** `backend/workers/tasks.go` (handler `HandleZaloWebhookTask`), tài liệu `docs/admin/inventory-query-flow.md`.

## Bối cảnh

Khi khách hỏi tồn mơ hồ (vd `"FF901 tồn bao nhiêu"`) và chọn "theo dòng",
handler `#choose_flow_type:dongsp:` (`tasks.go:730`) gọi `RankProductWebGroups`
rồi **luôn** bắn danh sách option `ten_dong_bo_web` dưới dạng text đánh số
(`BuildButtonOptionsAsText` → `1. … / 2. …`). Nhưng:

- Zalo OA list template trả `-233` nên không còn dùng nút interactive — đã fallback text.
- Khách gõ `1`/`2` thì **không có gì map lại postback** → danh sách vô dụng.
- Kể cả khi chỉ có **1 dòng** khớp vẫn bắt khách chọn.

## Mục tiêu

1. Chỉ 1 dòng khớp → **sum ngay**, không hỏi.
2. Nhiều dòng khớp → vẫn hỏi, nhưng khách **gõ số `1/2/3`** là resolve được → chạy
   thẳng `sumInventoryByMaChaAndWebName` → tổng tồn + chi tiết theo biến thể.

Chỉ áp dụng cho bước option `ten_dong_bo_web` trong nhánh `dongsp`.

## Thiết kế

### 1. Helper thuần (testable)

```go
// resolveNumericSelection trả về postback đã lưu nếu text là một số trần nằm
// trong [1..len(options)]. matched=false nếu không phải số hoặc list rỗng;
// inRange=false nếu là số nhưng ngoài khoảng (để caller nhắn chọn lại).
func resolveNumericSelection(text string, options []string) (payload string, matched bool, inRange bool)
```

- `text` được `strings.TrimSpace`; chỉ khớp regex `^\d+$`.
- `matched=true` khi text là số và `len(options) > 0`.
- `inRange=true` + `payload=options[n-1]` khi `1 <= n <= len(options)`.

### 2. Sửa handler `#choose_flow_type:dongsp:`

- Build danh sách postback như hiện tại (`#show_macha_options_by_web:<webName>`
  hoặc fallback `#show_macha_options:<parentCode>`).
- `len == 1` → gán `userText = postbacks[0]`, **không gửi list, không return** →
  fall-through xuống handler `#show_macha_options_by_web:` (`:878`) → sum.
- `len > 1` → gửi list đánh số (như cũ) **+ lưu `postbacks` vào Redis**
  `<sessionKey>:pending_options` (JSON, TTL = session timeout) → return.

### 3. Bước chặn numeric-reply (đặt trước các handler `#...`)

- `userText` là số trần **và** Redis có `pending_options`:
  - Trong khoảng → `userText = options[n-1]`, **xoá** key pending, fall-through.
  - Ngoài khoảng → nhắn *"Vui lòng chọn một số trong danh sách."*,
    **giữ pending** để khách gõ lại, return.
- Không có pending → bỏ qua, đi luồng Langflow bình thường.

### Không đụng tới

Bước "dòng vs sku" (`#choose_flow_type`), nhánh `skucuthe`,
`#check_stock_webname`, `#xem_mau_size`, và logic `sumInventoryByMaChaAndWebName`.

## Test

- Unit (table-driven) cho `resolveNumericSelection`: số hợp lệ / ngoài khoảng /
  không phải số / list rỗng / có khoảng trắng thừa.
- `go build ./...` + `go test ./backend/...` xanh.

## Rủi ro

- Fallback `#show_macha_options:<parentCode>` (sản phẩm không có web name) khi
  `len==1` sẽ rơi vào handler `:789`, có thể hiện thêm list `#check_stock_webname`
  nếu parent đó có >1 web name — chấp nhận (edge case, không có web name).
- `pending_options` per-session: TTL theo session timeout, tự hết hạn.

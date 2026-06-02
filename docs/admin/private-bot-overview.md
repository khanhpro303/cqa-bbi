# Bot private (nhân viên) — lớp khác biệt so với bot public

Tài liệu này mô tả **những điểm khác nhau cốt lõi** giữa **bot private (nhân
viên nội bộ)** và **bot public (khách hàng)**. Nó là bản **cắt ngang (cross-cutting)**:
mỗi tài liệu phân hệ ([`debt`](./debt-query-flow.md), [`orders`](./order-query-flow.md),
[`products`](./product-query-flow.md), [`inventory`](./inventory-query-flow.md))
mô tả luồng từ góc **public**, rồi tham chiếu về đây cho phần **delta private**
trong mục cuối "Bot private (nhân viên)".

> 🗣️ **Hiểu nhanh (không kỹ thuật):** public là **khách hàng** — bot chỉ cho họ
> xem dữ liệu **của chính họ**. Private là **nhân viên** — bot cho họ tra cứu dữ
> liệu của **nhiều khách** (theo nhóm được giao, hoặc toàn bộ), và họ tra **theo
> mã/tên khách** chứ không phải "của tôi".

> ⚠️ **Vì sao KHÔNG có 5 tài liệu private riêng:** private và public **dùng chung
> 90% xương sống** (webhook → worker → Langflow → ERP gateway → Cloudify) và cùng
> business logic mỗi phân hệ (debt tính số dư, orders tổng hợp, products pivot…).
> Khác biệt private **không nằm ở từng phân hệ** mà ở **3 lớp cắt ngang** dưới đây.

---

## A. Định danh & kích hoạt (erp.go)

| Khía cạnh | Public | Private |
|---|---|---|
| Token agent | `ai_agent_erp_token_public` | `ai_agent_erp_token_private` (legacy `ai_agent_erp_token` → coi là private) |
| Hàm phân giải | `resolveAgentType` (`erp.go:929`) | nt |
| Cờ bật ERP | fallback `erp_integration_active` | `erp_private_active` (`isERPActive`, `erp.go:956`) |
| Cho phép resource | `isResourcePermitted` tra ERPEndpoint theo nhóm khách (`erp.go:984`) | **luôn `true`** — gate quyền chuyển sang scope (xem B) |

---

## B. Quyền & scope (permission_context.go)

Public và private nạp quyền bằng **hai nhánh khác nhau** trong
`ResolvePermissionsWithGroup`:

```
                 ResolvePermissionsWithGroup (permission_context.go)
                                   │
        ┌──────────────────────────┴──────────────────────────┐
   agentType == "private"                              agentType == "public"
   (permission_context.go:104-135)                     (permission_context.go:141-198)
        │                                                      │
   nạp ERPEndpoint của group "private_bot"            map ZaloUserID → ZaloCustomer
   → 1 GroupPermission {GroupID:"private_bot"}        → CRMGroupCustomer → các group
        │                                              → ERPEndpoint của từng group
        ▼                                                      ▼
   IsResourceAllowed (permission_context.go:270-299)   IsResourceAllowed (union các group)
   • private_bot có cấu hình resource → dùng scope đó  • scope hợp nhất, ưu tiên
     (own/assigned/all)                                  all > assigned > own
   • private_bot CHƯA cấu hình → full access "all"
```

**Ý nghĩa scope** (giống nhau cho mọi phân hệ):

- `own` — chỉ dữ liệu của **chính mình** (điển hình bot public/khách lẻ).
- `assigned` — dữ liệu của **nhóm khách được giao** (`resolveGroupCustomerCodes`).
- `all` — **toàn bộ** khách (điển hình bot private/nhân viên khi `private_bot`
  chưa giới hạn).

> 🔑 Hệ quả: cùng một câu hỏi, **public khách lẻ** đi nhánh `scope == "own"`, còn
> **private nhân viên** đi nhánh `else` (`assigned`/`all`). Mọi logic riêng của
> private nằm ở nhánh `else` này.

---

## C. Xác định khách hàng cần tra (điểm khác lớn nhất)

| | Public (own) | Private (nhân viên) |
|---|---|---|
| "Ai đang hỏi?" | `ZaloUserID → zalo_customers → CustomerCode` (`tasks.go:526`) | nt (để biết nhóm/nhân viên), nhưng dữ liệu trả về **không** giới hạn về 1 khách |
| "Tra của khách nào?" | luôn là **chính khách đó** | **theo mã/tên khách nhân viên gõ** (vd `S001`, `Huy`, `S001 Huy`) |
| Nguồn resolve | — | **cache MySQL `cached_customers`** (LIKE tokenized) → live ERP `SearchPartners` fallback |

Cache khách hàng: bảng `cached_customers` (tenant-scoped, index `idx_cc_ma` +
`idx_cc_name`), đồng bộ hằng ngày từ Cloudify qua job `erp_customer_cache`
(`engine/erp_customer_cache.go`). Helper resolve dùng chung cho private:
`resolveCustomerCodesFromCache` / `tokenizeCustomerQuery` /
`isPrivateDebtCustomerQuery` / `sendDebtCustomerPrompt` trong
`api/handlers/erp_debt_private.go`.

> Hiện **`debt`** đã dùng cache-resolve + prompt mã/tên (xem
> [delta debt](./debt-query-flow.md#bot-private-nhân-viên)). Các phân hệ khác có
> thể tái dùng cùng helper khi cần tra theo khách.

---

## D. Cơ chế prompt + marker (dùng chung public & private)

Khi backend cần **hỏi lại** (kỳ kế toán, khoảng ngày, hoặc **mã/tên khách**), nó
**tự gửi tin text** qua `adapter.SendMessage`/`SendGroupMessage` rồi trả JSON:

```json
{ "status":"success", "is_debt_prompt":true,
  "message":"zalo_rich_message_sent_directly", "data":[], "count":0 }
```

Agent đọc → trả sentinel `[RICH_MESSAGE_SENT]`. Worker thấy sentinel → lưu marker
`awaiting_followup` (`tasks.go:1335-1342`, helper `engine.StoreAwaitingFollowup`,
`session_options.go:127-160`). **Lượt kế tiếp**, worker gọi
`engine.TakeAwaitingFollowup` (`tasks.go:716`) → ép `IN_SCOPE` đúng một lượt, **bỏ
qua classifier** nên câu trả lời ngắn (vd `S001`, `tháng này`) **không bị nuốt**
thành CASUAL. Marker phủ chung debt/orders/inventory — không phụ thuộc phân hệ.

> ⚠️ Backend phải **tự gửi** prompt; nếu chỉ trả payload trong JSON thì không ai
> gửi (worker/ERPGatewayCaller không đọc field thô). Đây là lý do mọi prompt
> dùng đúng shape ở trên.

---

## E. Bảng tham chiếu file:line (private)

| Khía cạnh | File:Line |
|---|---|
| Phân giải agent type | `backend/api/handlers/erp.go:929` `resolveAgentType` |
| Cờ bật ERP private | `backend/api/handlers/erp.go:956` `isERPActive` (`erp_private_active`) |
| Cho phép resource (private→true) | `backend/api/handlers/erp.go:984` `isResourcePermitted` |
| Nạp quyền private | `backend/engine/permission_context.go:104-135` |
| Scope private | `backend/engine/permission_context.go:270-299` `IsResourceAllowed` |
| Resolve khách từ cache | `backend/api/handlers/erp_debt_private.go` |
| Cache khách hàng (job) | `backend/engine/erp_customer_cache.go` |
| Marker prompt → followup | `backend/workers/tasks.go:716, 1335-1342` · `backend/engine/session_options.go:127-160` |

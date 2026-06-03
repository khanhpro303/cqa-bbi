# Luồng truy vấn đơn hàng khi khách hỏi bot

Tài liệu này mô tả end-to-end luồng xử lý khi khách hàng nhắn cho bot một câu hỏi
**trạng thái đơn hàng** trên Zalo OA — ví dụ điển hình: **"đơn hàng của tôi tới
đâu rồi?"**. Tài liệu bám sát **code hiện tại** (nhánh `case "orders"` bên trong
`respondWithLiveDataV2` của `erp.go` + các hàm helper trong `erp_orders.go`).

> 📎 Tài liệu này là bản song song của [`inventory-query-flow.md`](./inventory-query-flow.md)
> cho resource **`orders`**. Dùng chung cùng một xương sống vận chuyển
> (webhook → worker → Langflow → ERP gateway → Cloudify) nhưng nhánh xử lý bên
> trong handler khác hẳn: orders **không** fuzzy-match mã sản phẩm. Nó rẽ **3
> chế độ**: (1) mơ hồ → hỏi khoảng thời gian; (2) có **mã đơn** (`ĐH/DH`+chữ số)
> → tra đúng 1 đơn (lọc server-side theo `SO_DON_HANG`) + kiểm quyền sở hữu;
> (3) **cửa sổ ngày** → kéo toàn bộ đơn trong khoảng rồi lọc **scope khách hàng**
> và **tổng hợp theo trạng thái**.

Ba câu hỏi mẫu được trace đầy đủ:

1. **Mơ hồ:** `"đơn hàng của tôi tới đâu rồi?"` (không kèm khoảng thời gian) →
   bot gửi **một tin nhắn plain text** hỏi khoảng thời gian (3 / 5 / 7 ngày),
   KHÔNG trả số liệu ngay.
2. **Khoảng ngày:** `"đơn hàng 7 ngày gần đây"` (khách bấm nút, hoặc tự gõ) → query ERP,
   lọc theo scope khách, tổng hợp `orders_summary`, agent đọc và trả lời.
3. **Một đơn cụ thể:** `"đơn ĐH000016 sao rồi?"` (kèm **mã đơn** `ĐH/DH`+chữ số) →
   backend gọi `saorders/search` lọc server-side theo `SO_DON_HANG`, **kiểm tra
   mã đơn có thuộc khách không**: thuộc → trả chi tiết 1 đơn; không thuộc / không
   tìm thấy → **HTTP 400 + message tiếng Việt** để agent báo lại khách.

> Sơ đồ dùng ASCII monospace. Khi xem trong VitePress, đặt trong code block để giữ
> căn lề.

---

## A. Swim-lane tổng (full end-to-end)

```
 Customer    Zalo Cloud    Backend HTTP    Asynq Worker      Langflow        Backend ERP API      Cloudify ERP
 (Zalo App)   (OA)          (Gin)           (tasks.go)       (RAG flow)      (erp.go handler)     (HTTP REST)
     │           │              │               │                │                  │                   │
  1. "đơn của ─► │              │               │                │                  │                   │
     tôi tới  2. POST           │               │                │                  │                   │
     đâu rồi?"   /webhooks/zalo►│               │                │                  │                   │
     │           │           3. ZaloWebhookHandler              │                  │                   │
     │           │              (handlers/webhooks.go:17)       │                  │                   │
     │           │◄── 200 OK    • ack 200 ngay                  │                  │                   │
     │           │              • Enqueue NewZaloWebhookTask ──►│                  │                   │
     │           │              │            4. HandleZaloWebhookTask (workers/tasks.go:131)            │
     │           │              │               • match OA/channel, resolve customer + permission       │
     │           │              │                 (ZaloUserID → zalo_customers → CustomerCode)           │
     │           │              │               • session (Redis), lưu user msg (Astra)                 │
     │           │              │               • classifyMessageIntent → IN_SCOPE                      │
     │           │              │               • ResolvePermissionsWithGroup                           │
     │           │              │                 (engine/permission_context.go:58) → scope(orders)     │
     │           │              │               • mã KH = CustomerCode gán cho CRM group (GMF)          │
     │           │              │               • SignPermissionToken (HMAC, kèm CustomerCode)          │
     │           │              │            5. langflowClient.RunFlowWithCustomer ──────────────────► │
     │           │              │               (engine/langflow_client.go) input_value = câu hỏi       │
     │           │              │            6. Langflow ToolCallingAgent quyết định gọi tool           │
     │           │              │               ERPGatewayCaller(resource="orders") (xem mục B)         │
     │           │              │            7. POST {gateway}/erp/query                                │
     │           │              │               Headers: X-Agent-Token, X-Permission-Token             │
     │           │              │               Body: {resource:"orders", search, zalo_user_id} ──────► │
     │           │              │                                                       8. ERPQuery     │
     │           │              │                                                          (erp.go:103) │
     │           │              │                              respondWithLiveDataV2 → case "orders"     │
     │           │              │                                                       (erp.go:2111)   │
     │           │              │                                          ── saorders/search (POST) ──► │
     │           │              │                                                       9. JSON resp  ◄─┤
     │           │              │                                          lọc scope + buildOrdersSummary
     │           │              │           10. Agent đọc orders_summary ◄──────────────│              │
     │           │              │               format text reply                       │              │
     │           │              │           11. save assistant msg (Astra) + ZaloOAAdapter.SendMessage │
     │      13.  │◄─── deliver ─┤◄── 12. reply text ───────────────────────────────────│              │
   "Anh có 9 đơn từ 20/05–27/05…"
```

> Nếu câu hỏi **mơ hồ** (mục C, nhánh `else` — không phải mã đơn, không phải
> khoảng ngày) thì ở bước 8 handler **tự gửi** tin text hỏi khoảng thời gian tới
> Zalo (qua `adapter.SendMessage` / `SendGroupMessage`, giống debt/inventory) rồi
> trả về `is_orders_prompt=true` + `message="zalo_rich_message_sent_directly"` +
> `data=[]`, `count=0`. Agent trả `[RICH_MESSAGE_SENT]`. Khách gõ lại (vd:
> `"7 ngày gần đây"`) → quay lại bước 1 với `search="đơn hàng N ngày gần đây"`.
>
> ⚠️ **Backend gửi trực tiếp, KHÔNG để Langflow gửi.** Trước đây handler chỉ trả
> `zalo_rich_message` trong JSON nhưng KHÔNG ai gửi nó (worker không đọc field này,
> ERPGatewayCaller cũng không) → khách không bao giờ nhận câu hỏi khoảng thời gian.
> Nay handler gọi adapter gửi tin trực tiếp như inventory. ERPGatewayCaller nhận
> `is_orders_prompt` (hoặc `message="zalo_rich_message_sent_directly"`) → trả
> `[RICH_MESSAGE_SENT]`.
>
> ⚠️ **Đã bỏ template list + buttons của Zalo.** Tin hỏi khoảng thời gian là văn
> bản thuần; các lựa chọn (3 / 5 / 7 ngày) nằm ngay trong nội dung text để khách tự
> nhắn lại. Text hiện tại (nhánh `else`, erp.go:2281):
>
> > *Bạn muốn xem các đơn hàng phát sinh trong khoảng thời gian nào? Vui lòng
> > nhắn: "3 ngày gần đây", "5 ngày gần đây" hoặc "7 ngày gần đây".*

---

## B. Bộ quy tắc agent cho `resource="orders"` (nguồn: `docs/admin/system prompt.txt`)

Tool `ERPGatewayCaller` nhận `resource ∈ {inventory, products, product_variants,
orders, customers, debt}`. Với đơn hàng:

| Kịch bản | Ý định khách | Tool call |
|---|---|---|
| **O1** | "đơn của tôi tới đâu / xem đơn hàng" (mơ hồ) | `orders(search="đơn hàng")` → backend rơi vào nhánh `else` → **tự gửi** tin text hỏi khoảng thời gian (`adapter.SendMessage`), trả `is_orders_prompt` + `zalo_rich_message_sent_directly` |
| **O2** | "đơn 3/5/7 ngày gần đây" / "đơn tuần này" | `orders(search="đơn hàng 7 ngày gần đây")` → backend trả `orders_summary` |
| **O3** | "đơn ĐH000016 sao rồi" (kèm **mã đơn** `ĐH/DH`+chữ số) | `orders(search="ĐH000016")` → backend tra 1 đơn + check quyền sở hữu → trả chi tiết hoặc **400** |

> 🧠 **Agent tự nhận biết, KHÔNG cần match search term.** Chỉ cần phân biệt: có mã
> đơn (regex `ĐH/DH`+chữ số) → gửi đúng mã vào `search`; không có → gửi generic
> `"đơn hàng"`. Backend **không** dò danh sách cụm từ generic nữa: bất cứ `search`
> nào không phải mã đơn và không phải khoảng ngày đều rơi vào nhánh `else` → hỏi
> 3/5/7 ngày. Mọi việc lọc/kiểm quyền do backend lo.

> 🔢 **LUẬT CỨNG:** Khi trả lời cho O2, agent **PHẢI** dùng `orders_summary` (đã cộng
> sẵn count / total / quantity theo từng trạng thái) để nêu con số. **KHÔNG** tự đếm
> từ mảng `orders[]` — mảng này đã bị cắt còn tối đa **20 dòng gần nhất**
> (`trimOrdersForLLM`), nên đếm tay sẽ sai trên danh sách dài.

---

## C. Sơ đồ quyết định bên trong `case "orders"` (erp.go:2111)

```
        ERPQuery → respondWithLiveDataV2 → case "orders" (erp.go:2111)
                                 │
                                 ▼
                  extractOrderCode(search)?   (erp_orders.go:21)
                  regex (?i)[ĐD]H\d+   vd "ĐH000016"
                                 │
                   ┌── CÓ MÃ ────┴──── KHÔNG ──┐
                   ▼                            ▼
        ┌──────────────────────┐    parseDaysFromSearch(search)  (erp_orders.go)
        │ 1 ĐƠN (mục D′):       │    "3"→3  "5"→5  "7"/"1 tuần"→7  else→0
        │ POST {SO_DON_HANG:mã} │              │
        │ → lọc server-side     │     ┌────────┴─────────┐
        │ data rỗng/err → 400   │  days>0            days==0 (else)
        │ "Không tìm thấy…"     │     │                  │
        │ check quyền sở hữu:   │     ▼                  ▼
        │  isOrderAuthorized    │ ┌──────────────┐ ┌──────────────────────────┐
        │  sai → 400 "không     │ │ Cửa sổ ngày: │ │ MƠ HỒ → backend TỰ GỬI    │
        │  thuộc tài khoản"     │ │ POST {TU_NGAY│ │ tin text hỏi 3/5/7 ngày   │
        │ đúng → normalizeOrder │ │ ,DEN_NGAY}   │ │ qua adapter.SendMessage   │
        │  Record → JSON count:1│ │ (không lọc   │ │ is_orders_prompt=true +   │
        │ (order_code, orders[])│ │  MA_KHACH_   │ │ sent_directly → [RICH_     │
        └──────────────────────┘ │  HANG)       │ │ MESSAGE_SENT]             │
                                  │              │ └──────────────────────────┘
                                  └──────┬───────┘
                                         ▼
                       Lọc scope: isOrderAuthorized (mục D)
                       normalizeOrderRecord mỗi đơn
                       buildOrdersSummary (erp_orders.go:270)
                       trimOrdersForLLM(…, 20)
                                         ▼
                       JSON: orders_summary + orders[]
```

> Không còn `isGenericOrderSearch` (danh sách cụm từ cứng) lẫn nhánh keyword
> fallback. LLM đã phân loại theo system prompt nên backend chỉ cần: mã đơn →
> 1 đơn; `days > 0` → cửa sổ ngày; **mọi thứ còn lại → hỏi 3/5/7 ngày**.

> `ordersEndpoint` mặc định là `saorders/search` (POST, JSON body); tenant có
> thể override qua setting `erp_global_method_permissions` (key `orders.path`) —
> xem `erp.go:2115`.
>
> - **Nhánh 1-đơn:** POST body `{SO_DON_HANG: mã}` — endpoint **lọc server-side**,
>   trả đúng đơn đó (mục D′).
> - **Nhánh cửa sổ ngày:** POST body `{TU_NGAY, DEN_NGAY}`. Endpoint **không**
>   nhận `MA_KHACH_HANG` làm input và **không** giới hạn `limit`: backend lấy về
>   toàn bộ đơn trong cửa sổ ngày rồi tự lọc theo mã khách ở phía Go (mục D).

---

## D. Lọc theo scope khách hàng

`scopeType` lấy từ `IsResourceAllowed("orders")`
(`permission_context.go:269`). Việc lọc đơn dựa trên **mã khách hàng**, được
phân giải khác nhau theo scope.

**Mã khách của chính người hỏi (scope `own`, `resolveOwnCustomerCode`,
erp.go:1590):** mã KH gán cho **CRM group (GMF)** là nguồn chân lý — mỗi group
ứng với đúng **một** `CustomerCode` khớp mã trên Cloudify.

1. Ưu tiên `permCtx.CustomerCode` — worker đã ký mã này vào permission token
   ở bước 4.
2. Nếu rỗng (call không kèm token), fallback đọc trực tiếp
   `resolveGroupCustomerCode(tenantID, groupIDs)` → `CRMGroup.CustomerCode`
   (erp.go:1570).

Cuối cùng đưa qua `leadingCustomerCode` để về **mã trần** trước khi so khớp.
Cả nhánh 1-đơn (mục D′) lẫn nhánh cửa sổ ngày đều gọi `resolveOwnCustomerCode`.

> ⚠️ **Đã bỏ cổng `SearchPartners` cho scope `own`** (commit `e385f3f`).
> Trước đây handler gọi `SearchPartners` để xác thực partner trước khi trả
> đơn; nay tin tưởng thẳng mã KH của group, không round-trip thêm sang
> Cloudify. (`SearchPartners` / `isPartnerInAllowedCodes` vẫn còn cho các
> nhánh khác.)

> ⚠️ **Trích mã khách từ `MA_KHACH_HANG`:** endpoint `saorders/search` trả
> `MA_KHACH_HANG` dạng **mảng** `[id, "MÃ - Tên"]` (ví dụ
> `[2273, "S052 - Phượt 4P"]`). Hàm `orderCustomerCode` (erp_orders.go:185) chuẩn
> hoá về **mã trần** (`S052`) bằng cách lấy phần tử `[1]` rồi cắt trước `" - "`
> (`leadingCustomerCode`, erp_orders.go:172); vẫn fallback chuỗi phẳng /
> `MA_KH`/`MA_DT` cho payload kiểu cũ.

```
   Mỗi đơn ERP → itemCustCode = orderCustomerCode(item)
                 (MA_KHACH_HANG[mảng/chuỗi] → mã trần; fallback MA_KH/MA_DT)
                                   │
        ┌──────────────────────────┼───────────────────────────┐
   scope = "own"             scope = "assigned"            scope = "all"
        │                          │                            │
        ▼                          ▼                            ▼
 resolveOwnCustomer       resolveGroupCustomerCodes        giữ tất cả
   Code (erp.go:1590):    (erp.go:1601, join              (không lọc —
   permCtx.CustomerCode   crm_group_customers →            nội bộ/nhân viên)
   ?: resolveGroup        zalo_customers approved)
   CustomerCode (GMF)     → allowedCodes
        │                          │
        ▼                          │
 giữ đơn nếu                       ▼
 itemCustCode ==          giữ đơn nếu itemCustCode ∈ allowedCodes
 leadingCustomerCode      (so khớp qua leadingCustomerCode)
   (ownCode)              (nhân viên thấy đơn của nhóm khách được giao)
 (khách chỉ thấy
  đơn của mình)
```

> Cả nhánh khoảng-ngày lẫn nhánh 1-đơn đều gọi chung
> `isOrderAuthorized(itemCustCode, scopeType, ownCode, allowedCodes)`
> (erp_orders.go) — `own`: khớp `ownCode`; `assigned`: thuộc `allowedCodes`;
> `all`: luôn thấy; scope rỗng/khác → **từ chối** (an toàn).

### Scope `assigned` chi tiết

`assigned` = người hỏi là **nhân viên / CRM được giao phụ trách một hay nhiều
nhóm khách hàng**. Khác `own` (chỉ thấy đơn của chính mình, so khớp **1 mã**
`ownCode`), `assigned` thấy đơn của **mọi khách hàng thuộc các nhóm được giao**,
so khớp với **một danh sách mã** `allowedCodes`.

**Dựng `allowedCodes`** (`erp.go:2141` → `resolveGroupCustomerCodes`,
`erp.go:1601`):

```
permCtx.Groups → [group_id...]            (nhóm CRM người hỏi được giao)
        │  resolveGroupCustomerCodes(tenantID, groupIDs)
        ▼
   SQL JOIN  zalo_customers
               ⋈ crm_group_customers ON zalo_customer_id = zalo_customers.id
             WHERE crm_group_customers.group_id IN (groupIDs)
               AND zalo_customers.tenant_id = tenantID
               AND zalo_customers.status   = 'approved'   ← chỉ KH đã duyệt
        │  Pluck zalo_customers.customer_code
        ▼
   allowedCodes = ["S052", "S081", "S133", …]   (mã Cloudify của cả nhóm)
```

`crm_group_customers` là **bảng nối n–n** giữa nhóm CRM và khách
(`zalo_customers`). Một nhóm có nhiều khách → nhân viên phụ trách nhóm "thừa
hưởng" toàn bộ mã khách (đã `approved`) trong nhóm.

**So khớp từng đơn** (`isOrderAuthorized`, erp_orders.go:33-39): với mỗi đơn,
bóc `itemCustCode` từ `MA_KHACH_HANG` rồi quét `allowedCodes`; khớp **bất kỳ**
mã nào (qua `EqualFold(leadingCustomerCode(ac), itemCustCode)`) là giữ; không
khớp mã nào → loại.

> ⚠️ **Lưu ý vận hành cho `assigned`:**
> 1. **Chỉ tính KH `status='approved'`** — khách chưa duyệt trong nhóm không vào
>    `allowedCodes`, nên đơn của họ bị ẩn với nhân viên cho tới khi được duyệt.
> 2. **`allowedCodes` rỗng → nhân viên thấy 0 đơn** (vòng lặp không khớp gì →
>    `false`). Thường do nhóm chưa gán khách hoặc khách chưa `approved`, **không
>    phải** lỗi ERP.
> 3. **Lỗi SQL bị nuốt:** `allowedCodes, _ = resolveGroupCustomerCodes(...)`
>    (erp.go:2141) bỏ qua `error`. Nếu query lỗi, `allowedCodes` rỗng → nhân
>    viên thấy 0 đơn một cách âm thầm thay vì báo lỗi rõ ràng.

---

## D′. Tra cứu một đơn theo mã (nhánh `extractOrderCode`)

Khi `extractOrderCode(search)` bắt được mã `ĐH/DH`+chữ số:

1. **Query server-side:** `SearchCustomEndpointWithBody(ordersEndpoint,
   {"SO_DON_HANG": <mã in hoa>})`. Endpoint **lọc thẳng** theo `SO_DON_HANG`
   và trả đúng đơn đó (khác nhánh khoảng-ngày phải kéo cả cửa sổ rồi lọc).
2. **Không thấy:** `err != nil` hoặc `len(data) == 0` → **HTTP 400**
   `{"status":"error","message":"Không tìm thấy đơn hàng <mã>."}`.
3. **Kiểm quyền sở hữu:** `itemCustCode = orderCustomerCode(data[0])`; nếu
   `!isOrderAuthorized(...)` → **HTTP 400**
   `{"status":"error","message":"Đơn hàng này không thuộc tài khoản của bạn."}`.
4. **Khớp:** `normalizeOrderRecord(data[0])` → JSON 200 `count:1` + `order_code`.

> 🔌 Component Langflow `ERPGatewayCaller` khi gặp **non-200** tự đọc
> `data.message` và trả `"Không thể lấy dữ liệu ERP: <message>"` cho agent →
> message tiếng Việt tới được khách mà **không phải sửa component**. System
> prompt (mục Orders luật 8) dặn agent relay nguyên ý, không thử lại.

---

## E. Hình dạng dữ liệu

### Field ERP thô → field chuẩn hoá (`normalizeOrderRecord`, erp_orders.go)

Cả hai nhánh (1 đơn và khoảng-ngày) đều dùng chung `normalizeOrderRecord`.
Theo đúng response chính thức của `saorders/search`:

| Field chuẩn hoá | Khoá ERP thô (thử lần lượt) |
|---|---|
| `order_id` | `SO_DON_HANG`, `MA_HOA_DON`, `MA_SO`, `MA`, `order_id`, `name`, `id` (kèm biến thể chữ thường) |
| `customer_name` | `TEN_KHACH_HANG`, `TEN_KH`, `TEN_DT`, `customer_name` |
| `customer_code` | `orderCustomerCode(item)` ← `MA_KHACH_HANG` (mảng `[id,"MÃ - Tên"]`) / fallback `MA_KH`,`MA_DT` |
| `status` / `trang_thai` | `TRANG_THAI` |
| `status_name` | `orderStatusDisplayName(status)` — nhãn tiếng Việt (Đang giao / Đang thực hiện / Hoàn thành / Hủy) |
| `ghi_chu` | `GHI_CHU` (ghi chú đơn) |
| `total` | **tính từ dòng hàng**: `Σ(SO_LUONG × DON_GIA)` − `GIAM_GIA_HOA_DON` (`computeOrderTotal`, erp_orders.go). Endpoint **không** trả field tổng. |
| `date` | `THOI_GIAN_TAO`, `NGAY_LAP`, `date` |
| `don_dat_hang_chi_tiet` | `DON_DAT_HANG_CHI_TIET` (mảng dòng hàng; `SO_LUONG` cộng cho quantity, `DON_GIA` cho total) |

> 🔎 **`SO_DON_HANG` đứng đầu** danh sách `order_id` vì đó là mã đơn khách quote
> (vd `ĐH000016`) và là field nhánh 1-đơn lọc server-side.

> `buildOrdersSummary` (erp_orders.go:282) cộng `value` từ field `total` **đã
> chuẩn hoá** ở trên (`getMapFloat(item,"total",…)`), tức là tổng từ
> `computeOrderTotal` — không đọc lại field tổng từ ERP thô.

### Nhãn trạng thái (erp_orders.go:145-170)

| `TRANG_THAI` | Nhãn | Thứ tự báo cáo |
|---|---|---|
| `3` | Đang giao | 1 |
| `1` | Đang thực hiện | 2 |
| `2` | Hoàn thành | 3 |
| `0` | Hủy | 4 |

> Mã ngoài tập trên → `"Khác (mã X)"`; rỗng → `"Không xác định"`.

### Ví dụ response JSON — khoảng ngày (erp.go:2230-2243)

```json
{
  "status": "success",
  "source": "cloudify_live",
  "resource": "orders",
  "scope": "own",
  "customer_code": "S052",
  "range_days": 7,
  "from": "20/05/2026",
  "to": "27/05/2026",
  "count": 9,
  "orders_summary": {
    "total_orders": 9,
    "total_value": 35420000,
    "total_quantity": 45.5,
    "by_status": [
      { "status": "3", "status_name": "Đang giao",       "count": 3, "quantity": 12,  "value": 12000000 },
      { "status": "1", "status_name": "Đang thực hiện",   "count": 5, "quantity": 25,  "value": 18500000 },
      { "status": "2", "status_name": "Hoàn thành",       "count": 1, "quantity": 8.5, "value": 4920000 }
    ]
  },
  "orders": [ "… tối đa 20 đơn gần nhất …" ],
  "data":   [ "… giống orders …" ]
}
```

### Ví dụ response 1 đơn — khớp (HTTP 200, nhánh D′, erp.go:2179-2190)

```json
{
  "status": "success",
  "source": "cloudify_live",
  "resource": "orders",
  "scope": "own",
  "customer_code": "S052",
  "order_code": "ĐH000016",
  "count": 1,
  "orders": [
    {
      "order_id": "ĐH000016",
      "customer_name": "Phượt 4P",
      "customer_code": "S052",
      "status": "3",
      "status_name": "Đang giao",
      "total": 1290000,
      "date": "26/05/2026",
      "don_dat_hang_chi_tiet": [ "… dòng hàng …" ]
    }
  ],
  "data": [ "… giống orders …" ]
}
```

### Ví dụ response 1 đơn — không hợp lệ (HTTP 400)

```json
{ "status": "error", "resource": "orders", "message": "Đơn hàng này không thuộc tài khoản của bạn." }
```

```json
{ "status": "error", "resource": "orders", "message": "Không tìm thấy đơn hàng ĐH999999." }
```

### Ví dụ câu trả lời bot

Khoảng ngày (O2):

```
Anh có 9 đơn từ 20/05 đến 27/05, tổng 35.420.000₫;
trong đó 3 đơn đang giao, 5 đơn đang thực hiện, 1 đơn hoàn thành.
```

Một đơn (O3, khớp):

```
Đơn ĐH000016 đang giao, tổng 1.290.000₫, đặt ngày 26/05.
```

Một đơn không thuộc khách / không tìm thấy:

```
Đơn hàng này không thuộc tài khoản của anh ạ.
```

Không có đơn (O2):

```
Anh không có đơn hàng nào trong 7 ngày gần đây.
```

---

## F. Bảng tham chiếu file:line

| Bước | File:Line | Hàm / nhánh |
|---|---|---|
| Webhook nhận tin | `backend/api/handlers/webhooks.go:17` | `ZaloWebhookHandler` |
| Worker xử lý | `backend/workers/tasks.go:131` | `HandleZaloWebhookTask` |
| Phân loại ý định | `backend/workers/tasks.go` | `classifyMessageIntent` → `IN_SCOPE` |
| Resolve quyền | `backend/engine/permission_context.go:58` | `ResolvePermissionsWithGroup` |
| Kiểm tra resource | `backend/engine/permission_context.go:269` | `IsResourceAllowed("orders")` → scope |
| Gọi Langflow | `backend/engine/langflow_client.go` | `RunFlowWithCustomer` |
| Handler ERP | `backend/api/handlers/erp.go:103` | `ERPQuery` (auth, ERP active, verify token, method check) |
| Nhánh orders | `backend/api/handlers/erp.go:2111` (trong `respondWithLiveDataV2`) | định tuyến: 1-đơn / khoảng-ngày / mơ hồ |
| **Nhận diện mã đơn** | `backend/api/handlers/erp_orders.go:21` | `extractOrderCode` (regex `(?i)[ĐD]H\d+`) |
| **Tra 1 đơn (server-side)** | `backend/api/handlers/erp.go:2152` | `SearchCustomEndpointWithBody({SO_DON_HANG: mã})` → 400 nếu rỗng/err |
| **Kiểm quyền sở hữu** | `backend/api/handlers/erp_orders.go:29` | `isOrderAuthorized` (own/assigned/all; mặc định từ chối) |
| Parse cửa sổ ngày | `backend/api/handlers/erp_orders.go:73` | `parseDaysFromSearch` |
| Gọi ERP (khoảng-ngày) | `backend/api/handlers/erp.go:2205` (nhánh `days > 0`) | `SearchCustomEndpointWithBody(TU_NGAY,DEN_NGAY)` — không limit, không mã KH |
| Cấu hình endpoint | `backend/api/handlers/erp.go:2115` | mặc định `saorders/search` (POST body) |
| Câu mơ hồ → backend tự gửi prompt 3/5/7 | `backend/api/handlers/erp.go:2246-2290` (nhánh `else`) | gửi text qua `adapter.SendMessage`/`SendGroupMessage` → `is_orders_prompt=true` + `message="zalo_rich_message_sent_directly"`, `data=[]`, `count=0` |
| Mã KH của người hỏi | `backend/api/handlers/erp.go:1590` | `resolveOwnCustomerCode` → `permCtx.CustomerCode` ?: `resolveGroupCustomerCode` (GMF) |
| Mã KH scope `assigned` | `backend/api/handlers/erp.go:1601` | `resolveGroupCustomerCodes` (join `crm_group_customers`) |
| Trích mã KH từ đơn | `backend/api/handlers/erp_orders.go:185`, `:172` | `orderCustomerCode` / `leadingCustomerCode` (mảng `MA_KHACH_HANG`) |
| Chuẩn hoá field (chung 2 nhánh) | `backend/api/handlers/erp_orders.go:53` | `normalizeOrderRecord` (order_id ← `SO_DON_HANG`; `status_name`; `computeOrderTotal`) |
| Tính total từ dòng hàng | `backend/api/handlers/erp_orders.go:216` | `computeOrderTotal` (Σ `SO_LUONG`×`DON_GIA` − `GIAM_GIA_HOA_DON`) |
| Tổng hợp | `backend/api/handlers/erp_orders.go:270` | `buildOrdersSummary` |
| Cộng số lượng | `backend/api/handlers/erp_orders.go:243` | `sumOrderLineQuantity` |
| Nhãn trạng thái | `backend/api/handlers/erp_orders.go:159` | `orderStatusDisplayName` |
| Cắt cho LLM | `backend/api/handlers/erp_orders.go:318` | `trimOrdersForLLM` (max 20) |
| Response JSON 1-đơn | `backend/api/handlers/erp.go:2179` | `count:1` + `order_code` + `orders[]` |
| Response JSON khoảng-ngày | `backend/api/handlers/erp.go:2230` (nhánh `days > 0`) | `orders_summary` + `orders[]` |
| Mock (dev) | `backend/api/handlers/erp.go:2655` (`respondWithMockDataV2`); prompt mơ hồ tự gửi ở `:2731` | cùng định tuyến: `days<=0` → tự gửi prompt; `days>0` → lọc theo ngày |
| **Private: flow theo khách** | `backend/api/handlers/erp_orders_private.go` | `isPrivateOrdersCustomerQuery` / `resolveOrdersCustomerCodes` / `scopeApprovedOrdersCodes` / `tokenizeOrdersCustomerQuery` / `isAllCustomersReply` / `codesContain` / prompt + store helpers |
| **Private: pre-routing trong case orders** | `backend/api/handlers/erp.go` (trong `case "orders"`, ngay sau khối `allowedCodes`) | take state → pick/timerange; nêu khách → resolve+scope; gán `targetCodes` |
| **Private: state qua lượt** | `backend/engine/session_options.go` | `OrderCustomerState{Stage,Codes}` · `StoreOrderCustomerState` / `TakeOrderCustomerState` · key `:awaiting_order_customer` |
| **Private: lọc membership** | `backend/api/handlers/erp.go` (nhánh `days > 0`) | `len(targetCodes)>0` → `codesContain(targetCodes, …)`; ngược lại `isOrderAuthorized` |

---

## Bot private (nhân viên)

> 📎 Phần chung (agent type, scope, marker) ở [`private-bot-overview.md`](./private-bot-overview.md).
> Dưới đây chỉ là **delta riêng của `orders`**.

Tài liệu trên trace bot **public** (khách lẻ, `scope == "own"`: chỉ thấy đơn của
chính mình). Bot **private** (nhân viên) khác ở **hai chỗ**: (1) lọc theo
`scopeType`, và (2) một **flow hỏi-đơn-theo-khách** nhiều lượt (mới) cho phép nhân
viên tra đơn của **một khách cụ thể** (`đơn hàng của S001`).

### B1. Lọc theo scope (như cũ)

- `scope == "assigned"` → `allowedCodes = resolveGroupCustomerCodes(...)`
  (`erp.go:2353`); chỉ giữ đơn của **nhóm khách được giao** (`isOrderAuthorized`).
- `scope == "all"` → **không lọc** theo mã khách (khi không nêu khách); nhân viên
  thấy **mọi đơn** trong cửa sổ.
- Tra **một mã đơn** (`ĐH…`): kiểm quyền sở hữu cũng theo `scopeType` — `all`/
  `assigned` cho phép xem đơn ngoài "own" miễn trong phạm vi.

### B2. Flow hỏi-đơn-theo-khách (mới — chỉ private)

Mirror flow debt-theo-khách (xem [`private-bot-overview.md` mục C](./private-bot-overview.md#c-xác-định-khách-hàng-cần-tra-điểm-khác-lớn-nhất)).
Helper nằm ở `backend/api/handlers/erp_orders_private.go`; trạng thái qua lượt lưu ở
Redis key `:awaiting_order_customer` (payload `{stage, codes}`, `engine.OrderCustomerState`).

> 🔑 **An toàn scope:** mã khách được **giao với scope NGAY khi resolve** (lượt nêu
> khách) qua `scopeApprovedOrdersCodes`, rồi mới lưu vào state. Nên dù scope là
> `all`, query nêu khách vẫn **chỉ** trả đơn của đúng khách đó (KHÔNG dump toàn bộ);
> còn `assigned` chỉ chấp nhận khách **trong nhóm được giao**. Lượt trả time range
> về sau chỉ replay `codes` đã-duyệt → không thể nới quyền.

> 🔑 **Giữ nhãn MA (2026-06-03):** `scopeApprovedOrdersCodes` (qua
> `dedupeCustomerCodes`/`intersectCustomerCodes`) **gộp & giao theo leading code
> nhưng giữ nguyên chuỗi MA đầy đủ** `"<mã> - <tên>"`. Orders lọc đơn client-side
> qua `codesContain` (so theo leading code) nên hành vi không đổi; việc giữ nhãn là
> để dùng chung với debt — nơi chuỗi MA đầy đủ được gửi thẳng lên `DS_KHACH_HANG`
> (xem [debt-query-flow](./debt-query-flow.md#h-bot-private-nhân-viên)).

```
Lượt 1 "đơn hàng" (generic, chưa có khách)         → hỏi "mã/tên khách hàng?"   (ordersCustomerPromptText)
Lượt 1 "đơn hàng của S001" (nêu khách, chưa ngày)   → resolveOrdersCustomerCodes → scopeApprovedOrdersCodes:
        ├ 0  → 400 trung tính "Không tìm thấy đơn hàng của khách này trong phạm vi của bạn."
        ├ 1  → lưu {timerange,[code]} + hỏi 3/5/7 ngày
        └ ≥2 → lưu {pick,[codes]} + hỏi MÃ khách cụ thể / "tất cả"  (ordersCustomerPickByCodeText — KHÔNG liệt kê danh sách dài)
Lượt sau {pick}:  "S005" → resolve+scope TRỰC TIẾP (KHÔNG ràng buộc vào danh sách đã chào, nên mã hợp-scope nào cũng chọn được);
                  1 khách → {timerange,[code]} + hỏi 3/5/7 ngày;  vẫn ≥2 → hỏi lại mã cụ thể;  "tất cả" → giữ toàn bộ state.Codes
                  → chuyển {timerange,[codes]} + hỏi 3/5/7 ngày
Lượt sau {timerange}: "7 ngày gần đây" → days=7, targetCodes=state.Codes
                  → kéo cửa sổ ngày, lọc membership ∈ targetCodes, orders_summary  ✅
                  (chưa có ngày → nhắc lại prompt, giữ state;
                   gõ mã đơn ĐH… → nhường nhánh 1-đơn)
"đơn hàng của S001 7 ngày gần đây" (1 tin)          → resolve+scope 1 khách + days → trả luôn, targetCodes=[S001]
"tất cả" / "toàn bộ" ở bước hỏi khách               → scope-wide (targetCodes rỗng) → lọc isOrderAuthorized như cũ
Lượt 1 "đơn ĐH000016" (mã đơn)                       → nhánh 1-đơn cũ (đã scope-check) — KHÔNG đổi
```

- **Lọc đơn:** nhánh `days > 0`, nếu `len(targetCodes) > 0` → giữ đơn khi
  `codesContain(targetCodes, orderCustomerCode(item))`; ngược lại dùng
  `isOrderAuthorized(... scope ...)` như cũ. Response thêm `customer_codes` (mảng)
  khi nêu khách; `orders_summary` vẫn **phẳng** (gộp tất cả khách đã chọn — đúng ý
  khi nhân viên trả "tất cả").
- Mọi prompt vẫn tự gửi qua `adapter.SendMessage/SendGroupMessage`, trả
  `is_orders_prompt=true` + `message="zalo_rich_message_sent_directly"`; worker lưu
  `awaiting_followup` → lượt sau ép `IN_SCOPE`. **Agent Langflow không cần sửa** —
  vẫn forward text vào `orders(search=...)` và relay `[RICH_MESSAGE_SENT]`.
- **Bot public (own) không đổi:** flow theo-khách chỉ chạy khi
  `permCtx.AgentType == "private"`; khách lẻ vẫn "đơn hàng của tôi" → hỏi 3/5/7 ngày.

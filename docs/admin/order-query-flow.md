# Luồng truy vấn đơn hàng khi khách hỏi bot

Tài liệu này mô tả end-to-end luồng xử lý khi khách hàng nhắn cho bot một câu hỏi
**trạng thái đơn hàng** trên Zalo OA — ví dụ điển hình: **"đơn hàng của tôi tới
đâu rồi?"**. Tài liệu bám sát **code hiện tại** (handler `ERPQuery` nhánh
`case "orders"` trong `erp.go` + các hàm helper trong `erp_orders.go`).

> 📎 Tài liệu này là bản song song của [`inventory-query-flow.md`](./inventory-query-flow.md)
> cho resource **`orders`**. Dùng chung cùng một xương sống vận chuyển
> (webhook → worker → Langflow → ERP gateway → Cloudify) nhưng nhánh xử lý bên
> trong handler khác hẳn: orders **không** fuzzy-match mã sản phẩm; nó lọc theo
> **cửa sổ ngày** + **scope khách hàng** rồi **tổng hợp theo trạng thái**.

Hai câu hỏi mẫu được trace đầy đủ:

1. **Mơ hồ:** `"đơn hàng của tôi tới đâu rồi?"` (không kèm khoảng thời gian) →
   bot gửi **một tin nhắn plain text** hỏi khoảng thời gian (3 / 5 / 7 ngày),
   KHÔNG trả số liệu ngay.
2. **Cụ thể:** `"đơn hàng 7 ngày gần đây"` (khách bấm nút, hoặc tự gõ) → query ERP,
   lọc theo scope khách, tổng hợp `orders_summary`, agent đọc và trả lời.

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
     │           │              │               • SignPermissionToken (HMAC)                            │
     │           │              │            5. langflowClient.RunFlowWithCustomer ──────────────────► │
     │           │              │               (engine/langflow_client.go) input_value = câu hỏi       │
     │           │              │            6. Langflow ToolCallingAgent quyết định gọi tool           │
     │           │              │               ERPGatewayCaller(resource="orders") (xem mục B)         │
     │           │              │            7. POST {gateway}/erp/query                                │
     │           │              │               Headers: X-Agent-Token, X-Permission-Token             │
     │           │              │               Body: {resource:"orders", search, zalo_user_id} ──────► │
     │           │              │                                                       8. ERPQuery     │
     │           │              │                                                          (erp.go:36)  │
     │           │              │                                          case "orders" (erp.go:1913)  │
     │           │              │                                          ── saorders/search (POST) ──► │
     │           │              │                                                       9. JSON resp  ◄─┤
     │           │              │                                          lọc scope + buildOrdersSummary
     │           │              │           10. Agent đọc orders_summary ◄──────────────│              │
     │           │              │               format text reply                       │              │
     │           │              │           11. save assistant msg (Astra) + ZaloOAAdapter.SendMessage │
     │      13.  │◄─── deliver ─┤◄── 12. reply text ───────────────────────────────────│              │
   "Anh có 9 đơn từ 20/05–27/05…"
```

> Nếu câu hỏi **mơ hồ** (mục C, nhánh trái) thì ở bước 8 handler trả về
> `is_orders_prompt=true` + `zalo_rich_message`; agent trả `[RICH_MESSAGE_SENT]`
> và bot gửi **một tin nhắn plain text** hỏi khoảng thời gian. Khách gõ lại
> (vd: `"7 ngày gần đây"`) → quay lại bước 1 với `search="đơn hàng N ngày gần đây"`.
>
> ⚠️ **Đã bỏ template list + buttons của Zalo.** `zalo_rich_message` giờ chỉ chứa
> `recipient` + `message.text` (không còn `attachment.type="template"` / `buttons`),
> nên webhook bắn về Zalo là tin nhắn văn bản thuần. Phần liệt kê lựa chọn
> (3 / 5 / 7 ngày) nằm ngay trong nội dung text để khách tự nhắn lại.

---

## B. Bộ quy tắc agent cho `resource="orders"` (nguồn: `docs/admin/system prompt.txt`)

Tool `ERPGatewayCaller` nhận `resource ∈ {inventory, products, product_variants,
orders, customers, debt}`. Với đơn hàng:

| Kịch bản | Ý định khách | Tool call |
|---|---|---|
| **O1** | "đơn của tôi tới đâu / xem đơn hàng" (mơ hồ) | `orders(search="đơn hàng")` → backend tự gửi tin text hỏi khoảng thời gian |
| **O2** | "đơn 3/5/7 ngày gần đây" / "đơn tuần này" | `orders(search="đơn hàng 7 ngày gần đây")` → backend trả `orders_summary` |

> 🔢 **LUẬT CỨNG:** Khi trả lời, agent **PHẢI** dùng `orders_summary` (đã cộng sẵn
> count / total / quantity theo từng trạng thái) để nêu con số. **KHÔNG** tự đếm
> từ mảng `orders[]` — mảng này đã bị cắt còn tối đa **20 dòng gần nhất**
> (`trimOrdersForLLM`), nên đếm tay sẽ sai trên danh sách dài.

---

## C. Sơ đồ quyết định bên trong `case "orders"` (erp.go:1913)

```
                       ERPQuery → case "orders" (erp.go:1913)
                                       │
                       ┌───────────────┴───────────────┐
              isGenericOrderSearch(search)?      (erp_orders.go:14)
            "" | "đơn hàng" | "xem đơn hàng" | "tra cứu đơn hàng" | …
                       │                               │
                    YES│                            NO │
                       ▼                               ▼
        ┌──────────────────────────┐      parseDaysFromSearch(search)   (erp_orders.go:26)
        │ Trả zalo_rich_message     │      "3 ngày"→3  "5 ngày"→5
        │ (PLAIN TEXT, không buttons)│     "7 ngày"/"1 tuần"→7   else→0
        │ text: hỏi 3/5/7 ngày      │                  │
        │ is_orders_prompt = true   │      ┌───────────┴────────────┐
        │ (erp.go:1914-1928)        │   days > 0                 days == 0
        │ → agent: [RICH_MESSAGE_    │      │                         │
        │   SENT], bot gửi tin text │      ▼                         ▼
                                  ┌─────────────────────────┐   ┌──────────────────────┐
                                  │ Cửa sổ ngày (JSON body): │   │ Fallback keyword      │
                                  │ TU_NGAY = now-days       │   │ search (erp.go:2030)  │
                                  │ DEN_NGAY = now           │   │ params={keyword,limit,│
                                  │ KHÔNG gửi limit / mã KH  │   │   partner_id?}        │
                                  │ (saorders/search không   │   │ → SearchCustomEndpoint│
                                  │  lọc theo MA_KHACH_HANG) │   └──────────────────────┘
                                  │ SearchCustomEndpointWith │
                                  │   Body(ordersEndpoint)   │
                                  │ (erp.go:1960-1972)       │
                                  └─────────┬────────────────┘
                                            ▼
                              Lọc theo scope (erp.go:1974-1992) — xem mục D
                                            ▼
                              Chuẩn hoá field (erp.go:1994-2007)
                              buildOrdersSummary (erp_orders.go:223)
                              trimOrdersForLLM(…, 20) (erp_orders.go:271)
                                            ▼
                              JSON: orders_summary + orders[] (erp.go:2016-2029)
```

> `ordersEndpoint` mặc định là `saorders/search` (POST, JSON body
> `{TU_NGAY, DEN_NGAY}`); tenant có thể override qua setting
> `erp_global_method_permissions` (key `orders.path`) — xem `erp.go:1931-1949`.
> Endpoint **không** nhận `MA_KHACH_HANG` làm input và **không** giới hạn
> `limit`: backend lấy về toàn bộ đơn trong cửa sổ ngày rồi tự lọc theo mã
> khách ở phía Go (mục D).

---

## D. Lọc theo scope khách hàng (erp.go:1974-1992)

Định danh khách: **ZaloUserID** (người gửi) → tra `zalo_customers`
(`status='approved'`) → **CustomerCode**. `scopeType` lấy từ
`IsResourceAllowed("orders")` (erp.go:202, `permission_context.go:269`).

> ⚠️ **Trích mã khách từ `MA_KHACH_HANG`:** endpoint `saorders/search` trả
> `MA_KHACH_HANG` dạng **mảng** `[id, "MÃ - Tên"]` (ví dụ
> `[2273, "S052 - Phượt 4P"]`). Hàm `orderCustomerCode` (erp_orders.go:138) chuẩn
> hoá về **mã trần** (`S052`) bằng cách lấy phần tử `[1]` rồi cắt trước `" - "`
> (`leadingCustomerCode`, erp_orders.go:125); vẫn fallback chuỗi phẳng /
> `MA_KH`/`MA_DT` cho payload kiểu cũ. So khớp với `CustomerCode` cũng được
> đưa qua `leadingCustomerCode` để cùng dạng.

```
   Mỗi đơn ERP → itemCustCode = orderCustomerCode(item)
                 (MA_KHACH_HANG[mảng/chuỗi] → mã trần; fallback MA_KH/MA_DT)
                                   │
        ┌──────────────────────────┼───────────────────────────┐
   scope = "own"             scope = "assigned"            scope = "all"
        │                          │                            │
        ▼                          ▼                            ▼
 giữ đơn nếu             resolveGroupCustomerCodes        giữ tất cả
 itemCustCode ==         (theo CRM group) → allowedCodes  (nội bộ/nhân viên)
 CustomerCode            giữ đơn nếu itemCustCode ∈ allowedCodes
 (khách chỉ thấy         (so khớp qua leadingCustomerCode)
  đơn của mình)           (nhân viên thấy đơn của nhóm khách được giao)
```

---

## E. Hình dạng dữ liệu

### Field ERP thô → field chuẩn hoá (erp.go:1994-2007)

Theo đúng response chính thức của `saorders/search`:

| Field chuẩn hoá | Khoá ERP thô (thử lần lượt) |
|---|---|
| `order_id` | `MA_HOA_DON`, `MA_SO`, `MA`, `order_id`, `name`, `id` |
| `customer_name` | `TEN_KHACH_HANG`, `TEN_KH`, `TEN_DT`, `customer_name` |
| `customer_code` | `orderCustomerCode(item)` ← `MA_KHACH_HANG` (mảng `[id,"MÃ - Tên"]`) / fallback `MA_KH`,`MA_DT` |
| `status` / `trang_thai` | `TRANG_THAI` |
| `total` | **tính từ dòng hàng**: `Σ(SO_LUONG × DON_GIA)` − `GIAM_GIA_HOA_DON` (`computeOrderTotal`, erp_orders.go:169). Endpoint **không** trả field tổng. |
| `date` | `THOI_GIAN_TAO`, `NGAY_LAP`, `date` |
| `don_dat_hang_chi_tiet` | `DON_DAT_HANG_CHI_TIET` (mảng dòng hàng; `SO_LUONG` cộng cho quantity, `DON_GIA` cho total) |

### Nhãn trạng thái (erp_orders.go:98-120)

| `TRANG_THAI` | Nhãn | Thứ tự báo cáo |
|---|---|---|
| `3` | Đang giao | 1 |
| `1` | Đang thực hiện | 2 |
| `2` | Hoàn thành | 3 |
| `0` | Hủy | 4 |

> Mã ngoài tập trên → `"Khác (mã X)"`; rỗng → `"Không xác định"`.

### Ví dụ response JSON (erp.go:2016-2029)

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

### Ví dụ câu trả lời bot

```
Anh có 9 đơn từ 20/05 đến 27/05, tổng 35.420.000₫;
trong đó 3 đơn đang giao, 5 đơn đang thực hiện, 1 đơn hoàn thành.
```

Không có đơn:

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
| Handler ERP | `backend/api/handlers/erp.go:36` | `ERPQuery` (auth, ERP active, verify token, method check) |
| Nhánh orders | `backend/api/handlers/erp.go:1913` | `case "orders"` |
| Generic detect | `backend/api/handlers/erp_orders.go:14` | `isGenericOrderSearch` |
| Prompt khoảng thời gian (plain text) | `backend/api/handlers/erp.go:1914-1928` (mock: `2503-2517`) | `is_orders_prompt`, `zalo_rich_message` (không còn `attachment`/buttons) |
| Cấu hình endpoint | `backend/api/handlers/erp.go:1931-1949` | mặc định `saorders/search` (POST body) |
| Parse cửa sổ ngày | `backend/api/handlers/erp_orders.go:26` | `parseDaysFromSearch` |
| Gọi ERP | `backend/api/handlers/erp.go:1960-1972` | `SearchCustomEndpointWithBody(TU_NGAY,DEN_NGAY)` — không limit, không mã KH |
| Trích mã KH | `backend/api/handlers/erp_orders.go:138`, `:125` | `orderCustomerCode` / `leadingCustomerCode` (mảng `MA_KHACH_HANG`) |
| Lọc theo scope | `backend/api/handlers/erp.go:1974-1992` | own / assigned / all |
| Chuẩn hoá field | `backend/api/handlers/erp.go:1994-2007` | `getMapString` + `computeOrderTotal` |
| Tính total từ dòng hàng | `backend/api/handlers/erp_orders.go:169` | `computeOrderTotal` (Σ `SO_LUONG`×`DON_GIA` − `GIAM_GIA_HOA_DON`) |
| Tổng hợp | `backend/api/handlers/erp_orders.go:223` | `buildOrdersSummary` |
| Cộng số lượng | `backend/api/handlers/erp_orders.go:196` | `sumOrderLineQuantity` |
| Nhãn trạng thái | `backend/api/handlers/erp_orders.go:112-120` | `orderStatusDisplayName` |
| Cắt cho LLM | `backend/api/handlers/erp_orders.go:271` | `trimOrdersForLLM` (max 20) |
| Response JSON | `backend/api/handlers/erp.go:2016-2029` | `orders_summary` + `orders[]` |
| Fallback keyword | `backend/api/handlers/erp.go:2032-2041` | `{keyword,limit,partner_id?}` |

# Luồng truy vấn công nợ khi khách hỏi bot

Tài liệu này mô tả end-to-end luồng xử lý khi khách hàng nhắn cho bot một câu hỏi
**công nợ** trên Zalo OA — ví dụ điển hình: **"công nợ của tôi bao nhiêu?"**. Tài
liệu bám sát **code hiện tại** (handler `ERPQuery` → hàm `respondWithLiveDataV2`
nhánh `case "debt"` ở `erp.go:2361` + các hàm helper trong `erp_debt.go`).

> 🗣️ **Hiểu nhanh (không kỹ thuật):** khách nhắn "công nợ" → bot **chưa** trả số
> ngay mà hỏi lại "xem kỳ nào?" (tháng này / tháng trước / quý này). Khi khách
> chốt kỳ, bot mới biết khách là ai (qua Zalo), lấy đúng mã khách của họ, gọi báo
> cáo công nợ phải thu của ERP rồi đọc ra "đầu kỳ – cuối kỳ nợ bao nhiêu".

> 📎 Tài liệu này là bản song song của [`order-query-flow.md`](./order-query-flow.md)
> và [`inventory-query-flow.md`](./inventory-query-flow.md) cho resource **`debt`**.
> Dùng chung cùng một xương sống vận chuyển (webhook → worker → Langflow → ERP
> gateway → Cloudify) nhưng nhánh xử lý bên trong handler khác hẳn: debt **không**
> đếm/tổng hợp như orders; nó hỏi **kỳ kế toán** (Tháng này / Tháng trước / Quý
> này), xác định **danh sách mã khách** theo scope rồi gọi báo cáo **công nợ phải
> thu** và chuẩn hoá số dư đầu/cuối kỳ.

Hai câu hỏi mẫu được trace đầy đủ:

1. **Mơ hồ:** `"công nợ của tôi bao nhiêu?"` / `"công nợ"` (không kèm kỳ) →
   bot gửi **một tin nhắn plain text** hỏi kỳ (Tháng này / Tháng trước / Quý này),
   KHÔNG trả lời số liệu ngay.
2. **Cụ thể:** `"công nợ tháng này"` (khách bấm nút, hoặc tự gõ) → query ERP,
   xác định mã khách theo scope, lấy số dư công nợ, agent đọc và trả lời.

> Sơ đồ dùng ASCII monospace. Khi xem trong VitePress, đặt trong code block để giữ
> căn lề.

---

## A. Swim-lane tổng (full end-to-end)

```
 Customer    Zalo Cloud    Backend HTTP    Asynq Worker      Langflow        Backend ERP API      Cloudify ERP
 (Zalo App)   (OA)          (Gin)           (tasks.go)       (RAG flow)      (erp.go handler)     (HTTP REST)
     │           │              │               │                │                  │                   │
  1. "công nợ ─► │              │               │                │                  │                   │
     của tôi  2. POST           │               │                │                  │                   │
     bao nhiêu?" /webhooks/zalo►│               │                │                  │                   │
     │           │           3. ZaloWebhookHandler              │                  │                   │
     │           │              (handlers/webhooks.go:17)       │                  │                   │
     │           │◄── 200 OK    • ack 200 ngay                  │                  │                   │
     │           │              • Enqueue NewZaloWebhookTask ──►│                  │                   │
     │           │              │            4. HandleZaloWebhookTask (workers/tasks.go:184)            │
     │           │              │               • match OA/channel, resolve customer + permission       │
     │           │              │                 (ZaloUserID → zalo_customers → CustomerCode)           │
     │           │              │                 (workers/tasks.go:526)                                 │
     │           │              │               • session (Redis), lưu user msg (Astra)                 │
     │           │              │               • classifyMessageIntent → IN_SCOPE                      │
     │           │              │               • ResolvePermissionsWithGroup                           │
     │           │              │                 (engine/permission_context.go:58) → scope(debt)       │
     │           │              │               • SignPermissionToken (HMAC)                            │
     │           │              │            5. langflowClient.RunFlowWithCustomer ──────────────────► │
     │           │              │               (engine/langflow_client.go) input_value = câu hỏi       │
     │           │              │            6. Langflow ToolCallingAgent quyết định gọi tool           │
     │           │              │               ERPGatewayCaller(resource="debt") (xem mục B)           │
     │           │              │            7. POST {gateway}/erp/query                                │
     │           │              │               Headers: X-Agent-Token, X-Permission-Token             │
     │           │              │               Body: {resource:"debt", search, zalo_user_id} ────────► │
     │           │              │                                                       8. ERPQuery     │
     │           │              │                                                          (erp.go:103) │
     │           │              │                                   respondWithLiveDataV2 / case "debt"   │
     │           │              │                                            (erp.go:1724 / erp.go:2361) │
     │           │              │                                   ── th_cong_no_phai_thu/search ────► │
     │           │              │                                                       9. JSON resp  ◄─┤
     │           │              │                                   mapDebtItemForLLM (chuẩn hoá số dư)
     │           │              │           10. Agent đọc data[] ◄───────────────────────│              │
     │           │              │               format text reply                       │              │
     │           │              │           11. save assistant msg (Astra) + ZaloOAAdapter.SendMessage │
     │      13.  │◄─── deliver ─┤◄── 12. reply text ───────────────────────────────────│              │
   "Công nợ của EG05 (EGO Store) từ 01/05–29/05: đầu kỳ 1.050.000₫, cuối kỳ 1.050.000₫."
```

> Nếu câu hỏi **mơ hồ** (mục C, nhánh trái) thì ở bước 8 handler **tự gửi** tin
> text hỏi kỳ tới Zalo (qua `adapter.SendMessage` / `SendGroupMessage`, giống
> picker dòng-vs-SKU của inventory) rồi trả về `is_debt_prompt=true` +
> `message="zalo_rich_message_sent_directly"` + `data=[]`, `count=0`. Agent trả
> `[RICH_MESSAGE_SENT]`. Khách gõ lại (vd: `"tháng này"`) → quay lại bước 1 với
> `search="công nợ tháng này"` (hoặc "tháng trước" / "quý này").
>
> 🔑 **Lượt gõ lại KHÔNG bị bộ phân loại ý định nuốt (2026-06-02).** Lượt gõ lại
> (`"tháng này"`...) vẫn đi qua worker và `classifyMessageIntent`
> (`tasks.go:1654`). Vì câu này không có từ khoá nghiệp vụ, classifier từng gán
> nó là **CASUAL** → worker đóng phiên + bỏ tin (`tasks.go` nhánh `intent ==
> "CASUAL"`) → **bot im lặng**. Nay khi worker chặn câu trả lời vì backend đã đẩy
> rich message (sentinel `[RICH_MESSAGE_SENT]`, `tasks.go:1277`), nó **ghi một
> marker dùng-một-lần** `engine.StoreAwaitingFollowup` (`tasks.go:1284`;
> key `…:awaiting_followup`, `engine/session_options.go:129`). Lượt kế tiếp,
> trước khi phân loại, worker gọi `engine.TakeAwaitingFollowup` (`tasks.go:691`);
> nếu marker còn → ép `IN_SCOPE`, **bỏ qua classifier** đúng một lượt rồi xoá
> marker. Marker này phủ chung cho cả debt (kỳ), orders (3/5/7 ngày) và picker
> dòng-vs-SKU của inventory — không phụ thuộc cách khách diễn đạt (`"nợ quý này"`,
> `"tháng 5"`, nút bấm…). Câu xã giao ở các lượt sau (không ngay sau prompt) vẫn
> được phân loại và đóng phiên như cũ.
>
> ⚠️ **Backend gửi trực tiếp, KHÔNG để Langflow gửi.** Trước đây handler chỉ trả
> `zalo_rich_message` trong JSON nhưng KHÔNG ai gửi nó (worker không đọc field này,
> ERPGatewayCaller cũng không) → khách không bao giờ nhận câu hỏi kỳ. Nay handler
> gọi adapter gửi tin trực tiếp như inventory. ERPGatewayCaller nhận `is_debt_prompt`
> (hoặc `message="zalo_rich_message_sent_directly"`) → trả `[RICH_MESSAGE_SENT]`.
>
> ⚠️ **Đã bỏ template list + buttons của Zalo.** Tin hỏi kỳ là văn bản thuần; các
> lựa chọn (Tháng này / Tháng trước / Quý này) nằm ngay trong nội dung text để
> khách tự nhắn lại.

---

## B. Bộ quy tắc agent cho `resource="debt"` (nguồn: `docs/admin/system prompt.txt`)

Tool `ERPGatewayCaller` nhận `resource ∈ {inventory, products, product_variants,
orders, customers, debt}`. Với công nợ:

| Kịch bản | Ý định khách | Tool call |
|---|---|---|
| **D1** | "công nợ của tôi / xem công nợ / tra cứu công nợ / nợ" (mơ hồ) | `debt(search="công nợ")` → backend tự gửi tin text hỏi kỳ |
| **D2** | "công nợ tháng này / tháng trước / quý này" | `debt(search="công nợ tháng này")` → backend trả `data[]` số dư |

> 🔢 **LUẬT CỨNG:** Khi trả lời, agent **PHẢI** đọc các field chuẩn hoá
> `MA_KHACH_HANG`, `TEN_KHACH_HANG`, `NO_SO_DU_DAU_KY` (đầu kỳ), `NO_SO_DU_CUOI_KY`
> (cuối kỳ). **KHÔNG** dùng tên cũ `NO_TRUOC` / `NO_SAU` — chúng đã được gấp về tên
> chuẩn bởi `mapDebtItemForLLM`. Nếu response có `is_debt_prompt=true` → agent trả
> đúng `[RICH_MESSAGE_SENT]` (bot tự gửi tin text hỏi kỳ, không trả số liệu). Nếu `data` rỗng →
> "Không có dữ liệu công nợ trong khoảng anh hỏi."

---

## C. Sơ đồ quyết định bên trong `case "debt"` (erp.go:2361)

> 🗣️ **Hiểu nhanh:** đây là "ngã ba" của handler. Nếu câu hỏi mơ hồ (chỉ có chữ
> "công nợ", không kèm kỳ) → đi nhánh **TRÁI**: bot hỏi lại kỳ. Nếu đã có kỳ → đi
> nhánh **PHẢI**: tìm mã khách, chốt khoảng ngày, gọi ERP, chuẩn hoá số liệu rồi
> trả về.

```
                  respondWithLiveDataV2 → case "debt" (erp.go:2361)
                                       │
                       ┌───────────────┴───────────────┐
               isGenericDebtSearch(search)?      (erp_debt.go:14)
            "" | "công nợ" | "xem công nợ" | "check công nợ" |
            "tra cứu công nợ" | "đối chiếu công nợ" | "nợ" | "no" | … (bỏ dấu)
                       │                               │
                    YES│                            NO │
                       ▼                               ▼
        ┌───────────────────────────┐     1) Resolve targetCustomerCodes (mục D)
        │ Backend TỰ GỬI tin text    │         (erp.go:2406-2445)
        │ hỏi kỳ tới Zalo qua        │     2) parseDebtPeriodFromSearch(search)
        │ adapter.SendMessage /      │         (erp_debt.go:26) — mặc định "tháng này"
        │ SendGroupMessage           │         (erp.go:2449-2451)
        │ (PLAIN TEXT, không buttons)│     3) branchName ← setting erp_branch_name
        │ → trả is_debt_prompt=true  │         (mặc định "BBI NỘI BỘ", erp.go:2454-2458)
        │   + message=               │     4) debtEndpoint + GET/POST ←
        │   "zalo_rich_message_sent_ │         erp_global_method_permissions.debt
        │   directly", data=[],count=0│        (path/post, erp.go:2460-2482)
        │ (erp.go:2362-2403)         │                  │
        │ → agent: [RICH_MESSAGE_     │     ┌────────────┴────────────┐
        │   SENT]                    │  usePost = true          usePost = false
        └───────────────────────────┘     │                         │
                                          ▼                         ▼
                                ┌────────────────────┐   ┌──────────────────────┐
                                │ SearchCustomEndpoint│   │ SearchCustomEndpoint  │
                                │ WithBody(endpoint,  │   │ (endpoint, params)    │
                                │  bodyPayload)       │   │ params: query-string  │
                                │ (erp.go:2495)       │   │ (erp.go:2504)         │
                                └─────────┬───────────┘   └───────────┬──────────┘
                                          └────────────┬──────────────┘
                                                       ▼
                              mapDebtItemForLLM mỗi dòng (erp_debt.go:58)
                              gấp alias → field chuẩn (erp.go:2510)
                                                       ▼
                              JSON dùng chung: {status,data,source,resource,count}
```

> ⚠️ **Backend gửi câu hỏi kỳ trực tiếp** (nhánh YES) qua `adapter.SendMessage` /
> `SendGroupMessage` — giống picker dòng-vs-SKU của inventory. Agent CHỈ nhận
> `is_debt_prompt` / `message="zalo_rich_message_sent_directly"` (kèm `data=[]`,
> `count=0`) rồi trả `[RICH_MESSAGE_SENT]`. KHÔNG còn trả `zalo_rich_message` thô
> trong JSON để "ai đó" gửi — worker không đọc field đó nên trước đây khách không
> bao giờ nhận được câu hỏi kỳ.
>
> `debtEndpoint` mặc định là `th_cong_no_phai_thu/search`; tenant có thể override
> qua setting `erp_global_method_permissions` (key `debt.path`) và chọn POST qua
> `debt.post` — xem `erp.go:2460-2482`. Endpoint sau khi load được strip các tiền
> tố `/`, `rest_api/private/`; nếu `path` rỗng hoặc đúng bằng `"debt"` thì giữ
> nguyên endpoint mặc định.
>
> ⚠️ Nhánh `case "debt"` ở `erp.go:2361` nằm trong hàm **`respondWithLiveDataV2`**
> (đường ERP thật, `source="cloudify_live"`). Còn `case "debt"` ở `erp.go:2859` là
> đường **mock** (`source="mock_erp"`) dùng khi ERP chưa bật — tài liệu này trace
> đường thật.

---

## D. Định danh & xác định danh sách mã khách (erp.go:2406-2445)

> 🗣️ **Hiểu nhanh:** bot phải biết "ai đang hỏi" để chỉ trả nợ của đúng người đó.
> - **Khách lẻ (own):** chỉ thấy nợ của **chính mình**. Nếu chưa map được mã khách
>   riêng, bot thử lấy mã khách gắn với **nhóm** của họ.
> - **Nhân viên (assigned/all):** có thể tra nợ theo **tên khách** họ gõ, hoặc theo
>   **đối tượng khách (partner)**; nếu không có thì lấy toàn bộ khách trong nhóm
>   được giao.

Định danh khách: **ZaloUserID** (người gửi) → tra `zalo_customers`
(`status='approved'`, `workers/tasks.go:526`) → **CustomerCode**, nạp vào
`permCtx.CustomerCode`. `scopeType` lấy từ `IsResourceAllowed("debt")`
(`permission_context.go:269`). Khác với orders (lọc từng đơn sau khi query), debt
xác định **trước** danh sách mã khách rồi đẩy vào tham số `DS_KHACH_HANG`.

```
                               scope khách cho "debt"
                                       │
        ┌──────────────────────────────┼────────────────────────────────┐
   scope = "own"                  scope = "assigned" / "all"        (đặc biệt)
   (erp.go:2406-2417)            (erp.go:2418-2445)
        │                              │
        ▼                              ▼
 ownCode = permCtx.CustomerCode  ┌─ partnerID != "" ────────────────────────────┐
        │                        │  resolveCustomerCodeFromPartnerID(partnerID)  │
 nếu ownCode == "":              │  (erp_debt.go:84) → SearchPartners, lấy "MA"  │
   resolveGroupCustomerCode(...) │  → [code]                                     │
   (erp.go:1570, số ÍT)          ├─ else search != "" & KHÔNG phải kỳ ──────────┤
   → mã khách của nhóm           │  SearchPartners(search,5) → gom mọi "MA"      │
        │                        │  (tra công nợ theo tên/đối tượng khách)       │
 targetCustomerCodes =           └───────────────────────────────────────────────┘
   [ownCode] (nếu có)                    │
   (khách chỉ thấy nợ            nếu vẫn rỗng & scope=="assigned":
    của chính mình)               resolveGroupCustomerCodes(tenantID, groupIDs)
        │                         (erp.go:1601, số NHIỀU — nhân viên thấy nợ
        │                          của cả nhóm khách được giao)
        └──────────────┬───────────────┘
                       ▼
        dsKhachHang = strings.Join(targetCustomerCodes, ",")   (erp.go:2484)
        → tham số DS_KHACH_HANG gửi Cloudify
```

> Với scope `own`: ưu tiên `permCtx.CustomerCode`; nếu rỗng (chưa map được mã khách
> riêng) thì **mới đây bổ sung** bước thử `resolveGroupCustomerCode` (số ít,
> `erp.go:1570`, lấy 1 mã từ nhóm). Nếu cả hai đều rỗng thì `targetCustomerCodes`
> rỗng → `DS_KHACH_HANG=""`; báo cáo ERP sẽ không bị giới hạn theo mã khách, nên
> việc map ZaloUserID → CustomerCode ở worker vẫn là điều kiện tiên quyết để khách
> chỉ thấy nợ của mình.

---

## E. Parse kỳ kế toán (erp_debt.go:26)

> 🗣️ **Hiểu nhanh:** hàm này dịch câu chữ của khách ("tháng này", "quý này"...)
> thành cặp ngày **TỪ NGÀY – ĐẾN NGÀY** để ERP biết lấy số liệu trong khoảng nào.
> Nếu khách không nói rõ kỳ, bot mặc định lấy **tháng này** (từ ngày 1 đến hôm nay).

`parseDebtPeriodFromSearch` chuyển ngôn ngữ tự nhiên thành cặp ngày ISO
(`2006-01-02`). Nếu không khớp kỳ nào → `ok=false`, handler tự áp mặc định
**"công nợ tháng này"** (erp.go:2449-2451).

| Input (có/không dấu) | `TU_NGAY` | `DEN_NGAY` |
|---|---|---|
| `"tháng này"` / `"thang nay"` | Ngày 1 tháng hiện tại | Hôm nay |
| `"tháng trước"` / `"thang truoc"` | Ngày 1 tháng trước | Ngày cuối tháng trước |
| `"quý này"` / `"quy nay"` | Ngày 1 của quý hiện tại | Hôm nay |
| (rỗng / không khớp) | → mặc định "tháng này" | → mặc định "tháng này" |

---

## F. Hình dạng dữ liệu

### Field ERP thô → field chuẩn hoá (`mapDebtItemForLLM`, erp_debt.go:58)

| Field chuẩn hoá | Khoá ERP thô (thử lần lượt) | Ý nghĩa |
|---|---|---|
| `MA_KHACH_HANG` | `MA_KHACH_HANG`, `ma_khach_hang`, `MA_KH`, `ma_kh` | Mã khách (vd `EG05`) |
| `TEN_KHACH_HANG` | `TEN_KHACH_HANG`, `ten_khach_hang`, `TEN_KH`, `ten_kh` | Tên khách (vd `EGO Store`) |
| `NO_SO_DU_DAU_KY` | `NO_SO_DU_DAU_KY`, `no_so_du_dau_ky`, `NO_TRUOC`, `no_truoc` | Số dư nợ **đầu kỳ** (VND) |
| `NO_SO_DU_CUOI_KY` | `NO_SO_DU_CUOI_KY`, `no_so_du_cuoi_ky`, `NO_SAU`, `no_sau` | Số dư nợ **cuối kỳ** (VND) |
| `NO_SO_DU_CUOI_KY_NGUYEN_TE` | `NO_SO_DU_CUOI_KY_NGUYEN_TE`, `no_so_du_cuoi_ky_nguyen_te` | Số dư cuối kỳ (nguyên tệ) |

> Mỗi field chuẩn được xuất ra **cả hai dạng** UPPER và lower (`NO_SO_DU_DAU_KY` +
> `no_so_du_dau_ky`) để LLM bắt được dù gọi tên nào. Mọi field ERP khác được giữ
> nguyên (pass-through) phục vụ debug.

### Request gửi Cloudify (erp.go:2486-2504)

| Tham số | Giá trị | Nguồn |
|---|---|---|
| `CHI_NHANH` | `branchName` | setting `erp_branch_name`, mặc định `BBI NỘI BỘ` |
| `TU_NGAY` | đầu kỳ (ISO) | `parseDebtPeriodFromSearch` |
| `DEN_NGAY` | cuối kỳ (ISO) | `parseDebtPeriodFromSearch` |
| `BAO_GOM_SO_LIEU_CHI_NHANH_PHU_THUOC` | `true` | cố định (gồm cả chi nhánh phụ thuộc) |
| `DS_KHACH_HANG` | mã khách nối bằng dấu phẩy | mục D (`dsKhachHang`) |

> POST gửi `bodyPayload` (bool `true`); GET gửi `params` query-string (chuỗi
> `"true"`). Chọn POST/GET qua `erp_global_method_permissions.debt.post`.

### Ví dụ prompt kỳ (mơ hồ → backend gửi trực tiếp)

Handler gọi `adapter.SendMessage`/`SendGroupMessage` gửi câu hỏi kỳ tới Zalo TRƯỚC,
rồi trả JSON báo "đã gửi" cho agent:

```json
{
  "status": "success",
  "is_debt_prompt": true,
  "message": "zalo_rich_message_sent_directly",
  "data": [],
  "count": 0
}
```

> Tin gửi tới khách là văn bản thuần: "Bạn muốn xem đối chiếu công nợ trong khoảng
> thời gian nào? Vui lòng nhắn: "tháng này", "tháng trước" hoặc "quý này"." Agent chỉ
> nhận `is_debt_prompt`/`zalo_rich_message_sent_directly` → trả `[RICH_MESSAGE_SENT]`.

### Ví dụ response JSON (có kỳ → data, nhánh response dùng chung cuối `respondWithLiveDataV2`)

```json
{
  "status": "success",
  "source": "cloudify_live",
  "resource": "debt",
  "count": 1,
  "data": [
    {
      "MA_KHACH_HANG": "EG05",
      "TEN_KHACH_HANG": "EGO Store",
      "NO_SO_DU_DAU_KY": 1050000,
      "no_so_du_dau_ky": 1050000,
      "NO_SO_DU_CUOI_KY": 1050000,
      "no_so_du_cuoi_ky": 1050000,
      "NO_SO_DU_CUOI_KY_NGUYEN_TE": 1050000,
      "no_so_du_cuoi_ky_nguyen_te": 1050000
    }
  ]
}
```

> Khác với orders, debt **không** có `orders_summary` / `range_days` / `scope` trong
> response — nó đi qua đúng nhánh response dùng chung ở cuối `respondWithLiveDataV2`:
> `{status, data, source, resource, count}`.

### Ví dụ câu trả lời bot

```
Công nợ của EG05 (EGO Store) từ 01/05 đến 29/05:
đầu kỳ 1.050.000₫, cuối kỳ 1.050.000₫.
```

Không có dữ liệu:

```
Không có dữ liệu công nợ trong khoảng anh hỏi.
```

---

## G. Bảng tham chiếu file:line

| Bước | File:Line | Hàm / nhánh |
|---|---|---|
| Webhook nhận tin | `backend/api/handlers/webhooks.go:17` | `ZaloWebhookHandler` |
| Worker xử lý | `backend/workers/tasks.go:184` | `HandleZaloWebhookTask` |
| Map ZaloUserID → mã khách | `backend/workers/tasks.go:526` | tra `zalo_customers` (`status='approved'`) |
| Phân loại ý định | `backend/workers/tasks.go:1654` | `classifyMessageIntent` → `IN_SCOPE` (bị bỏ qua khi marker awaiting-followup còn) |
| Ghi marker lượt gõ lại | `backend/workers/tasks.go:1284` | `engine.StoreAwaitingFollowup` (đặt khi chặn sentinel `[RICH_MESSAGE_SENT]`, `:1277`) |
| Tiêu thụ marker (bỏ qua classifier) | `backend/workers/tasks.go:691` | `engine.TakeAwaitingFollowup` → ép `IN_SCOPE` 1 lượt |
| Helper marker (Redis, dùng-một-lần) | `backend/engine/session_options.go:129` | `AwaitingFollowupSuffix` / `StoreAwaitingFollowup` / `TakeAwaitingFollowup` |
| Resolve quyền | `backend/engine/permission_context.go:58` | `ResolvePermissionsWithGroup` |
| Kiểm tra resource | `backend/engine/permission_context.go:269` | `IsResourceAllowed("debt")` → scope |
| Gọi Langflow | `backend/engine/langflow_client.go:209` | `RunFlowWithCustomer` |
| Handler ERP | `backend/api/handlers/erp.go:103` | `ERPQuery` (auth, ERP active, verify token, method check) |
| Hàm dữ liệu thật | `backend/api/handlers/erp.go:1724` | `respondWithLiveDataV2` (đường `cloudify_live`) |
| Nhánh debt (thật) | `backend/api/handlers/erp.go:2361` | `case "debt"` |
| Nhánh debt (mock) | `backend/api/handlers/erp.go:2859` | `case "debt"` đường `mock_erp` (ERP chưa bật) |
| Generic detect | `backend/api/handlers/erp_debt.go:14` | `isGenericDebtSearch` (gồm cả "check công nợ") |
| Prompt kỳ (backend tự gửi) | `backend/api/handlers/erp.go:2362-2403` | gửi text qua `adapter.SendMessage`/`SendGroupMessage` → trả `is_debt_prompt` + `message="zalo_rich_message_sent_directly"`, `data=[]`, `count=0` |
| Resolve mã khách | `backend/api/handlers/erp.go:2406-2445` | own / assigned / all |
| Own + fallback nhóm | `backend/api/handlers/erp.go:2406-2417` | `permCtx.CustomerCode` → `resolveGroupCustomerCode` (số ít) |
| Partner theo ID | `backend/api/handlers/erp_debt.go:84` | `resolveCustomerCodeFromPartnerID` (erp.go:2421) |
| Partner theo keyword | `backend/api/handlers/erp.go:2429` | `SearchPartners(search,5)` → gom `MA` |
| Nhóm khách (1 mã) | `backend/api/handlers/erp.go:1570` | `resolveGroupCustomerCode` (own fallback) |
| Nhóm khách (assigned) | `backend/api/handlers/erp.go:1601` / `:2445` | `resolveGroupCustomerCodes` |
| Parse kỳ kế toán | `backend/api/handlers/erp_debt.go:26` | `parseDebtPeriodFromSearch` (mặc định "tháng này") |
| Tên chi nhánh | `backend/api/handlers/erp.go:2454-2458` | setting `erp_branch_name` (mặc định `BBI NỘI BỘ`) |
| Cấu hình endpoint/POST | `backend/api/handlers/erp.go:2460-2482` | `erp_global_method_permissions.debt` (path/post; bỏ qua path rỗng / `"debt"`) |
| Nối DS_KHACH_HANG | `backend/api/handlers/erp.go:2484` | `strings.Join(targetCustomerCodes, ",")` |
| Gọi ERP (POST) | `backend/api/handlers/erp.go:2495` | `SearchCustomEndpointWithBody(debtEndpoint, bodyPayload)` |
| Gọi ERP (GET) | `backend/api/handlers/erp.go:2504` | `SearchCustomEndpoint(debtEndpoint, params)` |
| Chuẩn hoá field | `backend/api/handlers/erp_debt.go:58` | `mapDebtItemForLLM` (gấp alias → chuẩn, erp.go:2510) |
| Response JSON | nhánh response dùng chung cuối `respondWithLiveDataV2` | `{status,data,source,resource,count}` |
| Tests | `backend/api/handlers/erp_test.go:312` | `TestIsGenericDebtSearch` |
| | `backend/api/handlers/erp_test.go:334` | `TestParseDebtPeriodFromSearch` |
| | `backend/api/handlers/erp_test.go:383` | `TestMapDebtItemForLLM` |

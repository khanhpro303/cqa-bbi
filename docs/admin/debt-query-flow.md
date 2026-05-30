# Luồng truy vấn công nợ khi khách hỏi bot

Tài liệu này mô tả end-to-end luồng xử lý khi khách hàng nhắn cho bot một câu hỏi
**công nợ** trên Zalo OA — ví dụ điển hình: **"công nợ của tôi bao nhiêu?"**. Tài
liệu bám sát **code hiện tại** (handler `ERPQuery` → hàm `respondWithLiveDataV2`
nhánh `case "debt"` ở `erp.go:2119` + các hàm helper trong `erp_debt.go`).

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
     │           │              │                                                          (erp.go:36)  │
     │           │              │                                   respondWithLiveDataV2 / case "debt"   │
     │           │              │                                            (erp.go:1610 / erp.go:2119) │
     │           │              │                                   ── th_cong_no_phai_thu/search ────► │
     │           │              │                                                       9. JSON resp  ◄─┤
     │           │              │                                   mapDebtItemForLLM (chuẩn hoá số dư)
     │           │              │           10. Agent đọc data[] ◄───────────────────────│              │
     │           │              │               format text reply                       │              │
     │           │              │           11. save assistant msg (Astra) + ZaloOAAdapter.SendMessage │
     │      13.  │◄─── deliver ─┤◄── 12. reply text ───────────────────────────────────│              │
   "Công nợ của EG05 (EGO Store) từ 01/05–29/05: đầu kỳ 1.050.000₫, cuối kỳ 1.050.000₫."
```

> Nếu câu hỏi **mơ hồ** (mục C, nhánh trái) thì ở bước 8 handler trả về
> `is_debt_prompt=true` + `zalo_rich_message`; agent trả `[RICH_MESSAGE_SENT]`
> và bot gửi **một tin nhắn plain text** hỏi kỳ. Khách gõ lại (vd: `"tháng này"`)
> → quay lại bước 1 với `search="công nợ tháng này"` (hoặc "tháng trước" / "quý này").
>
> ⚠️ **Đã bỏ template list + buttons của Zalo.** `zalo_rich_message` giờ chỉ chứa
> `recipient` + `message.text` (không còn `attachment.type="template"` / `buttons`),
> nên webhook bắn về Zalo là tin nhắn văn bản thuần. Các lựa chọn kỳ (Tháng này /
> Tháng trước / Quý này) nằm ngay trong nội dung text để khách tự nhắn lại.

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

## C. Sơ đồ quyết định bên trong `case "debt"` (erp.go:2119)

> 🗣️ **Hiểu nhanh:** đây là "ngã ba" của handler. Nếu câu hỏi mơ hồ (chỉ có chữ
> "công nợ", không kèm kỳ) → đi nhánh **TRÁI**: bot hỏi lại kỳ. Nếu đã có kỳ → đi
> nhánh **PHẢI**: tìm mã khách, chốt khoảng ngày, gọi ERP, chuẩn hoá số liệu rồi
> trả về.

```
                  respondWithLiveDataV2 → case "debt" (erp.go:2119)
                                       │
                       ┌───────────────┴───────────────┐
               isGenericDebtSearch(search)?      (erp_debt.go:14)
            "" | "công nợ" | "xem công nợ" | "check công nợ" |
            "tra cứu công nợ" | "đối chiếu công nợ" | "nợ" | "no" | … (bỏ dấu)
                       │                               │
                    YES│                            NO │
                       ▼                               ▼
        ┌──────────────────────────┐      1) Resolve targetCustomerCodes (mục D)
        │ Trả zalo_rich_message     │         (erp.go:2137-2178)
        │ (PLAIN TEXT, không buttons)│     2) parseDebtPeriodFromSearch(search)
        │ text: hỏi Tháng này /     │         (erp_debt.go:26) — mặc định "tháng này"
        │ Tháng trước / Quý này     │         (erp.go:2180-2183)
        │ is_debt_prompt = true     │      3) branchName ← setting erp_branch_name
        │ (erp.go:2120-2135)        │         (mặc định "BBI NỘI BỘ", erp.go:2185-2189)
        │ → agent: [RICH_MESSAGE_    │     4) debtEndpoint + GET/POST ←
        │   SENT], bot gửi tin text │         erp_global_method_permissions.debt
        │                           │         (path/post, erp.go:2191-2213)
        └──────────────────────────┘                  │
                                          ┌────────────┴────────────┐
                                       usePost = true          usePost = false
                                          │                         │
                                          ▼                         ▼
                                ┌────────────────────┐   ┌──────────────────────┐
                                │ SearchCustomEndpoint│   │ SearchCustomEndpoint  │
                                │ WithBody(endpoint,  │   │ (endpoint, params)    │
                                │  bodyPayload)       │   │ params: query-string  │
                                │ (erp.go:2217-2225)  │   │ (erp.go:2226-2235)    │
                                └─────────┬───────────┘   └───────────┬──────────┘
                                          └────────────┬──────────────┘
                                                       ▼
                              mapDebtItemForLLM mỗi dòng (erp_debt.go:58)
                              gấp alias → field chuẩn (erp.go:2237-2243)
                                                       ▼
                              JSON dùng chung: {status,data,source,resource,count}
                              (erp.go:2301-2307)
```

> `debtEndpoint` mặc định là `th_cong_no_phai_thu/search`; tenant có thể override
> qua setting `erp_global_method_permissions` (key `debt.path`) và chọn POST qua
> `debt.post` — xem `erp.go:2191-2213`. Endpoint sau khi load được strip các tiền
> tố `/`, `rest_api/private/`; nếu `path` rỗng hoặc đúng bằng `"debt"` thì giữ
> nguyên endpoint mặc định.
>
> ⚠️ Nhánh `case "debt"` ở `erp.go:2119` nằm trong hàm **`respondWithLiveDataV2`**
> (đường ERP thật, `source="cloudify_live"`). Còn `case "debt"` ở `erp.go:2611` là
> đường **mock** (`source="mock_erp"`) dùng khi ERP chưa bật — tài liệu này trace
> đường thật.

---

## D. Định danh & xác định danh sách mã khách (erp.go:2137-2178)

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
   (erp.go:2138-2149)            (erp.go:2150-2178)
        │                              │
        ▼                              ▼
 ownCode = permCtx.CustomerCode  ┌─ partnerID != "" ────────────────────────────┐
        │                        │  resolveCustomerCodeFromPartnerID(partnerID)  │
 nếu ownCode == "":              │  (erp_debt.go:84) → SearchPartners, lấy "MA"  │
   resolveGroupCustomerCode(...) │  → [code]                                     │
   (erp.go:1471, số ÍT)          ├─ else search != "" & KHÔNG phải kỳ ──────────┤
   → mã khách của nhóm           │  SearchPartners(search,5) → gom mọi "MA"      │
        │                        │  (tra công nợ theo tên/đối tượng khách)       │
 targetCustomerCodes =           └───────────────────────────────────────────────┘
   [ownCode] (nếu có)                    │
   (khách chỉ thấy nợ            nếu vẫn rỗng & scope=="assigned":
    của chính mình)               resolveGroupCustomerCodes(tenantID, groupIDs)
        │                         (erp.go:1487, số NHIỀU — nhân viên thấy nợ
        │                          của cả nhóm khách được giao)
        └──────────────┬───────────────┘
                       ▼
        dsKhachHang = strings.Join(targetCustomerCodes, ",")   (erp.go:2215)
        → tham số DS_KHACH_HANG gửi Cloudify
```

> Với scope `own`: ưu tiên `permCtx.CustomerCode`; nếu rỗng (chưa map được mã khách
> riêng) thì **mới đây bổ sung** bước thử `resolveGroupCustomerCode` (số ít,
> `erp.go:1471`, lấy 1 mã từ nhóm). Nếu cả hai đều rỗng thì `targetCustomerCodes`
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
**"công nợ tháng này"** (erp.go:2180-2183).

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

### Request gửi Cloudify (erp.go:2217-2235)

| Tham số | Giá trị | Nguồn |
|---|---|---|
| `CHI_NHANH` | `branchName` | setting `erp_branch_name`, mặc định `BBI NỘI BỘ` |
| `TU_NGAY` | đầu kỳ (ISO) | `parseDebtPeriodFromSearch` |
| `DEN_NGAY` | cuối kỳ (ISO) | `parseDebtPeriodFromSearch` |
| `BAO_GOM_SO_LIEU_CHI_NHANH_PHU_THUOC` | `true` | cố định (gồm cả chi nhánh phụ thuộc) |
| `DS_KHACH_HANG` | mã khách nối bằng dấu phẩy | mục D (`dsKhachHang`) |

> POST gửi `bodyPayload` (bool `true`); GET gửi `params` query-string (chuỗi
> `"true"`). Chọn POST/GET qua `erp_global_method_permissions.debt.post`.

### Ví dụ prompt kỳ (mơ hồ → plain text, erp.go:2120-2135)

```json
{
  "status": "success",
  "is_debt_prompt": true,
  "zalo_rich_message": {
    "recipient": { "user_id": "<ZaloUserID>" },
    "message": {
      "text": "Bạn muốn xem đối chiếu công nợ trong khoảng thời gian nào? Vui lòng nhắn: \"tháng này\", \"tháng trước\" hoặc \"quý này\"."
    }
  }
}
```

> Đã bỏ `attachment.type="template"` + `buttons`. Payload chỉ còn `recipient` +
> `message.text` → webhook bắn về Zalo là tin nhắn văn bản thuần.

### Ví dụ response JSON (có kỳ → data, erp.go:2301-2307)

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
> response — nó đi qua đúng nhánh response dùng chung ở cuối `respondWithLiveDataV2`
> (erp.go:2301-2307): `{status, data, source, resource, count}`.

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
| Phân loại ý định | `backend/workers/tasks.go` | `classifyMessageIntent` → `IN_SCOPE` |
| Resolve quyền | `backend/engine/permission_context.go:58` | `ResolvePermissionsWithGroup` |
| Kiểm tra resource | `backend/engine/permission_context.go:269` | `IsResourceAllowed("debt")` → scope |
| Gọi Langflow | `backend/engine/langflow_client.go:209` | `RunFlowWithCustomer` |
| Handler ERP | `backend/api/handlers/erp.go:36` | `ERPQuery` (auth, ERP active, verify token, method check) |
| Hàm dữ liệu thật | `backend/api/handlers/erp.go:1610` | `respondWithLiveDataV2` (đường `cloudify_live`) |
| Nhánh debt (thật) | `backend/api/handlers/erp.go:2119` | `case "debt"` |
| Nhánh debt (mock) | `backend/api/handlers/erp.go:2611` | `case "debt"` đường `mock_erp` (ERP chưa bật) |
| Generic detect | `backend/api/handlers/erp_debt.go:14` | `isGenericDebtSearch` (gồm cả "check công nợ") |
| Prompt kỳ (plain text) | `backend/api/handlers/erp.go:2120-2135` | `is_debt_prompt`, `zalo_rich_message` (không còn `attachment`/buttons) |
| Resolve mã khách | `backend/api/handlers/erp.go:2137-2178` | own / assigned / all |
| Own + fallback nhóm | `backend/api/handlers/erp.go:2138-2149` | `permCtx.CustomerCode` → `resolveGroupCustomerCode` (số ít) |
| Partner theo ID | `backend/api/handlers/erp_debt.go:84` | `resolveCustomerCodeFromPartnerID` (erp.go:2152) |
| Partner theo keyword | `backend/api/handlers/erp.go:2159` | `SearchPartners(search,5)` → gom `MA` |
| Nhóm khách (1 mã) | `backend/api/handlers/erp.go:1471` | `resolveGroupCustomerCode` (own fallback) |
| Nhóm khách (assigned) | `backend/api/handlers/erp.go:1487` / `:2176` | `resolveGroupCustomerCodes` |
| Parse kỳ kế toán | `backend/api/handlers/erp_debt.go:26` | `parseDebtPeriodFromSearch` (mặc định "tháng này") |
| Tên chi nhánh | `backend/api/handlers/erp.go:2185-2189` | setting `erp_branch_name` (mặc định `BBI NỘI BỘ`) |
| Cấu hình endpoint/POST | `backend/api/handlers/erp.go:2191-2213` | `erp_global_method_permissions.debt` (path/post; bỏ qua path rỗng / `"debt"`) |
| Nối DS_KHACH_HANG | `backend/api/handlers/erp.go:2215` | `strings.Join(targetCustomerCodes, ",")` |
| Gọi ERP (POST) | `backend/api/handlers/erp.go:2217-2225` | `SearchCustomEndpointWithBody(debtEndpoint, bodyPayload)` |
| Gọi ERP (GET) | `backend/api/handlers/erp.go:2226-2235` | `SearchCustomEndpoint(debtEndpoint, params)` |
| Chuẩn hoá field | `backend/api/handlers/erp_debt.go:58` | `mapDebtItemForLLM` (gấp alias → chuẩn, erp.go:2237-2243) |
| Response JSON | `backend/api/handlers/erp.go:2301-2307` | `{status,data,source,resource,count}` |
| Tests | `backend/api/handlers/erp_test.go:312` | `TestIsGenericDebtSearch` |
| | `backend/api/handlers/erp_test.go:334` | `TestParseDebtPeriodFromSearch` |
| | `backend/api/handlers/erp_test.go:383` | `TestMapDebtItemForLLM` |

# Luồng truy vấn tồn kho khi khách hỏi bot

Tài liệu này mô tả end-to-end luồng xử lý khi khách hàng nhắn cho bot một câu
hỏi **tồn kho** trên Zalo OA. Tài liệu bám sát **code hiện tại** (nhánh
`respondWithLiveDataV2`, erp.go:1698 + resource `product_variants`).

> ⚠️ **Tài liệu này thay thế mô hình cũ.** Bản trước mô tả nhánh tồn kho theo
> "Phase B web-groups / Phase C1-embedding/LLM". Mô hình web-group đó **vẫn còn
> nhưng đã chuyển sang resource `products`** và **handler disambiguation
> `dongsp`** trong `tasks.go` — KHÔNG nằm trong nhánh `inventory`.
>
> ✅ **Cập nhật:** Embedding fuzzy (`FuzzyMatchProductWithEmbedding`) **giờ phục vụ
> CẢ `inventory`** thông qua helper dùng chung `resolveMaChaFuzzy` (erp.go:3023).
> Trước đây nhánh `inventory` chỉ có LLM fuzzy; nay nó resolve dòng sản phẩm bằng
> **embedding trước → LLM sau**, giống hệt resource `products`. Cùng một cờ
> `ERP_EMBEDDING_FUZZY_ENABLED` bật/tắt cả hai.
>
> 🛡️ **Guard SKU cụ thể:** `resolveMaChaFuzzy` trả `(ma, ma_cha, specific)`. Khi
> embedding pinpoint đúng **một** SKU (`specific=true`, vd Agent truyền mã SKU con
> đã resolve), `inventory` đọc tồn của **đúng SKU đó** — KHÔNG mở rộng về cả dòng,
> KHÔNG hỏi lại dòng-vs-SKU. Resource `products` thì cố tình bỏ qua `specific`
> (luôn trả cả dòng cho `price_range`).

> 📌 **Lưu ý đặt tên:** Các helper đọc cache giờ mang hậu tố `...FromCache`
> (`searchProductsByWebNameFromCache`, `getProductsByMaChaFromCache`,
> `searchProductsFromCacheWithFilter`, cả bản trong `erp.go` lẫn `tasks.go`) →
> **đọc cache MySQL nội bộ** (`models.CachedProduct` qua `db.DB`), KHÔNG gọi Astra.
> Hậu tố `...FromAstraDB` chỉ còn ở hàm thật sự chạm Astra Data API
> (`SyncProductEmbeddingsToAstraDB`, `getProductBySkuFromAstraDB`).

Hai câu hỏi mẫu được trace đầy đủ:

1. **Mơ hồ:** `"FF901 tồn bao nhiêu"` (không màu/size) → disambiguation hoặc tồn cả dòng.
2. **Cụ thể:** `"FF901 đỏ đen size L tồn bao nhiêu"` → resolve 1 SKU rồi đọc tồn live.

> Sơ đồ dùng ASCII monospace. Khi xem trong VitePress, đặt trong code block để
> giữ căn lề.

---

## A. Swim-lane tổng (full end-to-end)

```
 Customer    Zalo Cloud    Backend HTTP    Asynq Worker      Langflow        Backend ERP API      Cloudify ERP
 (Zalo App)   (OA)          (Gin)           (tasks.go)       (RAG flow)      (erp.go handler)     (HTTP REST)
     │           │              │               │                │                  │                   │
  1. câu hỏi ──► │              │               │                │                  │                   │
     │        2. POST           │               │                │                  │                   │
     │           /webhooks/zalo►│               │                │                  │                   │
     │           │           3. ZaloWebhookHandler              │                  │                   │
     │           │              (handlers/webhooks.go:17)       │                  │                   │
     │           │◄── 200 OK    • ack 200 ngay                  │                  │                   │
     │           │              • Enqueue NewZaloWebhookTask ──►│                  │                   │
     │           │              │            4. HandleZaloWebhookTask (workers/tasks.go:185)            │
     │           │              │               • match OA/channel, resolve customer + permission       │
     │           │              │               • session (Redis), lưu user msg (Astra)                 │
     │           │              │               • classifyMessageIntent → IN_SCOPE                      │
     │           │              │               • numeric-reply intercept (tasks.go:797, xem mục C):    │
     │           │              │                   "1"/"2"/"3" khớp pending_options → rewrite userText  │
     │           │              │               • SHORTCUT intercept (xem mục C):                       │
     │           │              │                   #choose_flow_type / #show_macha_options /           │
     │           │              │                   #show_macha_options_by_web → xử lý TRỰC TIẾP,       │
     │           │              │                   KHÔNG gọi Langflow                                  │
     │           │              │               • SignPermissionToken (HMAC)                            │
     │           │              │            5. langflowClient.RunFlowWithCustomer ──────────────────► │
     │           │              │               (engine/langflow_client.go) input_value = câu hỏi       │
     │           │              │            6. Langflow ToolCallingAgent quyết định gọi tool           │
     │           │              │               ERPGatewayCaller (xem mục B cho bộ quy tắc)             │
     │           │              │            7. POST {gateway}/erp/query                                │
     │           │              │               Headers: X-Agent-Token, X-Permission-Token             │
     │           │              │               Body: {resource, search, parent_code, color,           │
     │           │              │                      size, brand, zalo_user_id, limit} ─────────────► │
     │           │              │                                                       8. ERPQuery     │
     │           │              │                                                          (erp.go:103) │
     │           │              │                                                          ── live ───► │
     │           │              │                                                       9. JSON resp  ◄─┤
     │           │              │           10. Agent format text reply ◄───────────────│              │
     │           │              │           11. save assistant msg (Astra) + ZaloOAAdapter.SendMessage │
     │      13.  │◄─── deliver ─┤◄── 12. reply text ───────────────────────────────────│              │
   "… còn N cái"
```

---

## B. Bộ quy tắc điều phối của Agent (nguồn: `docs/admin/system prompt.txt`)

Tool `ERPGatewayCaller` nhận: `resource` ∈ {`inventory`, `products`,
`product_variants`, `orders`, `customers`, `debt`}, `search`, `parent_code`,
`color`, `size`, `brand`, `limit`.

> 🔒 **LUẬT CỨNG:** KHÔNG BAO GIỜ truyền chuỗi `"<màu> <size>"` thô vào `search`
> của `resource="inventory"`. Nhánh inventory **không** fuzzy-match màu/size.
> Bắt buộc resolve `MA` qua `product_variants` trước.

| Kịch bản | Ý định | Chuỗi tool calls |
|---|---|---|
| **A** | Tồn, mô tả tự nhiên, chưa có `parent_code` | `products` (resolve MA_CHA) → `inventory(search=MA_CHA)` |
| **B** | Tồn của **variant cụ thể** (có màu/size) | `products` → `product_variants(parent_code,color,size,brand)` → đọc `data[0].ma` → `inventory(search=MA)` → đọc `ton_kho` |
| **C** | **Giá** của variant cụ thể | Như B nhưng **DỪNG** ở `product_variants`, đọc `price`. Không gọi inventory |
| **D** | Tồn **cả dòng** (đã biết MA_CHA, không màu/size) | `inventory(search=MA_CHA)` — backend tự lặp các SKU con và cộng tồn live |

---

## C. Kịch bản 1 — "FF901 tồn bao nhiêu" (mơ hồ, không màu/size)

Câu hỏi không có màu/size → ý định STOCK mở. Agent thường gọi
`inventory(search="FF901")` (hoặc `products` trước rồi `inventory(search=MA_CHA)`).
Tại backend, "FF901" thường LIKE-match nhiều dòng ⇒ rơi vào **disambiguation**.

```
Agent → ERPQuery: resource="inventory", search="FF901", parent_code="", no màu/size
                                  │
                                  ▼
        respondWithLiveDataV2 → case "inventory" (erp.go:1705)
                                  │  parentCode=="" → bỏ qua Branch-1 (filtered)
                                  ▼
 ┌──────────────────────────────────────────────────────────────────────────┐
 │ ⮕ TRA CỨU MỘT LẦN  (erp.go:1785)                                          │
 │   searchProductsByWebNameFromCache (:3060, đọc MySQL cache)                │
 │   LIKE ten_dong_bo_web "%FF901%" → LIKE ten → resolveMaChaFuzzy (:3023):   │
 │       embedding fuzzy → LLM fuzzy (fuzzyMatchMaChaWithLLM)                 │
 │   • specific=true → trả specificSKU → single-SKU (bỏ qua phân loại dưới)   │
 │   ⇒ matchedProducts   (dùng lại cho CẢ disambiguation LẪN phân loại dưới)  │
 └──────────────────────────────────────────────────────────────────────────┘
                                  │
        ┌──────────────────────────┼───────────────────────────┐
    len > 1                     len == 1                     len == 0 (miss)
   (đa biến thể)               (1 dòng khớp)                 (không khớp)
        │                          │                             │
        ▼                  collapse: search = SKU đó             │
 ╔═══════════════╗                 │                             │
 ║ DISAMBIGUATION║                 └──────────────┬──────────────┘
 ║ (erp.go:1812) ║                                ▼
 ║ Đẩy Zalo nút: ║   classifyDominantMaCha(matchedProducts)  (erp.go:1873 → :2993)
 ║ 📦 dòng SP    ║     • filterProductsByGroups → dominantMaCha (:2965)
 ║   dongsp:FF901║     • xác nhận dòng có >1 biến thể (getProductsByMaChaFromCache :2920)
 ║ 🔍 SKU cụ thể ║     *** DÙNG LẠI matchedProducts — KHÔNG tra cứu/LLM lại ***
 ║   skucuthe:.. ║                                │
 ║ → is_inventory║                     ┌──────────┴──────────┐
 ║   _rich,      ║                isMaCha=true          isMaCha=false
 ║   data=[]     ║                     │                     │
 ║ → return      ║                     ▼                     ▼
 ╚══════╤════════╝          getProductsByMaCha       single-SKU live call
        │                   FromCache (:2920)        (erp.go:1924, xem mục F)
        │                   loop mỗi con →                   │
        │                   fetchInventoryStockForSKU        ▼
        │                          │              lay_ton_kho_san_pham
        │                          ▼              → totalStockFromInventoryItems
        │               data=[{MA, THUOC_TINH_1,     (chỉ "Kho Tổng")
        │                 THUOC_TINH_2, TON_KHO},…] → {MA, TON_KHO}
        │               (tồn từng biến thể của dòng = Kịch bản D)
        ▼
   ────────── Khách chọn dòng / SKU ──────────────────────────────────────
   #choose_flow_type:dongsp:FF901  (tasks.go:821)
       searchProductsByWebNameFromCache → RankProductWebGroups (:832)
       → dựng tối đa 3 option theo TEN_DONG_BO_WEB:
            #show_macha_options_by_web:<TEN_DONG_BO_WEB>   (build tasks.go:843)
            #show_macha_options:<MA_CHA>  (fallback, build tasks.go:841)
       │
       ├─ len == 1 (1 dòng khớp, KHÔNG mơ hồ)
       │     userText ← postback[0]  → fall-through thẳng xuống
       │     #show_macha_options_by_web → sumInventoryByMaChaAndWebName
       │     → tổng tồn + chi tiết theo biến thể  (KHÔNG hỏi lại)
       │
       └─ len > 1 (nhiều dòng, còn mơ hồ)
             BuildButtonOptionsAsText → tin nhắn đánh số "1. … / 2. …"
             + storePendingOptions → Redis <sessionKey>:pending_options
                                     (TTL = session timeout)
             → return, CHỜ khách gõ số

   ────────── Khách gõ số "1"/"2"/"3" ────────────────────────────────────
   numeric-reply intercept (tasks.go:797, ngay sau permCtx, trước các handler #…)
       Redis GET <sessionKey>:pending_options → resolveNumericSelection (:56)
       • số hợp lệ  → DEL pending; userText ← postback đã lưu → fall-through
                       #show_macha_options_by_web → sumInventoryByMaChaAndWebName
       • số ngoài khoảng → "Vui lòng chọn một số trong danh sách."
                       (GIỮ pending để gõ lại) → return
       • không phải số / không có pending → bỏ qua, đi luồng Langflow

   #choose_flow_type:skucuthe:FF901  (tasks.go:876)
       Bot hỏi: "… màu và size nào? (Ví dụ: FF901 màu đỏ size L)"
       → khách trả lời màu+size → chuyển sang KỊCH BẢN 2
```

**Tóm tắt Kịch bản 1:** một mã/keyword trần như "FF901" gần như luôn kích hoạt
bộ chọn *dòng sản phẩm vs SKU cụ thể*. Khi khách chọn **dòng**: nếu chỉ **một**
dòng (`TEN_DONG_BO_WEB`) khớp thì backend cộng tồn và trả thẳng chi tiết biến thể
ngay; nếu **nhiều** dòng thì bot gửi danh sách đánh số và lưu `pending_options`,
khách **gõ số** (`1`/`2`/`3`) là chạy thẳng `sumInventoryByMaChaAndWebName` —
không còn bắn Zalo list-template button, cũng không round-trip qua Langflow. Ngoài
ra khi `search` thu hẹp về một `MA_CHA` rõ ràng (nhánh `classifyDominantMaCha=true`,
Kịch bản D) backend trả danh sách tồn từng biến thể ngay trong lượt đầu.

---

## D. Kịch bản 2 — "FF901 đỏ đen size L tồn bao nhiêu" (cụ thể)

Có màu + size ⇒ Kịch bản B. Agent thực hiện **3 bước** (không bao giờ nhồi
"đỏ đen size L" vào `search` của inventory):

```
Bước 1 — resolve MA_CHA (nếu history chưa có)
   resource="products", search="FF901"
   → erp.go:274 products path → searchProductWebGroupsFromCache / embedding / LLM
   → MA_CHA = "FF901"
                                  │
                                  ▼
Bước 2 — resolve 1 SKU theo thuộc tính
   resource="product_variants", parent_code="FF901", color="đỏ đen", size="L"
   → erp.go:477 → searchVariantsByAttributes (erp_variants.go:25)
        WHERE tenant_id=? AND ma_cha="FF901"
          AND LOWER(thuoc_tinh_1) LIKE LOWER('%đỏ đen%')   ← màu: substring
          AND LOWER(thuoc_tinh_2) = LOWER('L')             ← size: KHỚP CHÍNH XÁC
                                                             (normalizeSizeFilter bỏ "size ")
   → slimVariantsForLLM → data=[{ma, name, color, size, price}]  (KHÔNG có tồn)
        source="astradb_cache_variants"  (erp.go:494)
                                  │
            ┌──────────────────────┴───────────────────────┐
       data có kết quả                            data rỗng (erp.go:563)
            │                                               │
            ▼                                               ▼
   đọc data[0].ma = MA biến thể         collectAvailableAttributes +
   (vd "FF901-RED-L")                   fuzzyMatchAttributesWithLLM (song ngữ
            │                            "đỏ đen"→"Gloss Black"…) → retry 1 lần
            │                                               │
            │                            ┌──────────────────┴───────────┐
            │                       retry có KQ                    vẫn rỗng
            │                       (echo bilingual_match)    surface available_colors/
            │                            │                    sizes/brands → Agent hỏi
            │                            ▼                    khách chọn lại; KHÔNG gọi
            └────────────┬───────── data[0].ma               inventory với MA rỗng
                         ▼
Bước 3 — đọc tồn live của đúng SKU đó
   resource="inventory", search="FF901-RED-L"
   → respondWithLiveDataV2 case "inventory"
   → search là mã SKU con → LIKE miss → resolveMaChaFuzzy: embedding BM25 trúng
     đúng SKU đó, specific=true → searchProductsByWebNameFromCache trả specificSKU
   → matchedProducts=nil → classifyDominantMaCha=false → single-SKU live call
     (erp.go:1924).  🛡️ Guard: KHÔNG collapse về cả dòng dù embedding khớp.
   → lay_ton_kho_san_pham → totalStockFromInventoryItems (chỉ "Kho Tổng")
   → data=[{MA:"FF901-RED-L", TON_KHO: 12, ton_kho: 12}]
```

**Reply mẫu:** *"FF901 (Đỏ đen, size L) hiện còn 12 cái."* Nếu Bước 2 trả
`bilingual_match`, Agent nêu cả tên chuẩn lẫn cách khách gọi: *"Gloss Black –
đỏ đen, size L: còn 12 cái."*

---

## E. Chi tiết nhánh `inventory` backend (`respondWithLiveDataV2`, erp.go:1698)

Cấu hình endpoint (đầu `case "inventory"`, erp.go:1705):
- Mặc định `danhmucvattuhanghoa/lay_ton_kho_san_pham` (POST) — đường tồn kho chính.
- Tenant có thể override qua setting `erp_global_method_permissions` sang một
  endpoint tùy biến khác (custom path).
- Hằng số: `inventoryTotalStockEndpoint` (erp.go:2419),
  `inventoryTotalWarehouseName = "Kho Tổng"` (erp.go:2423).

Cây quyết định 3 nhánh:

```
case "inventory":
 ├─ Branch-1  parentCode != "" && search != ""        (erp.go:1732)
 │     searchProductsFromCacheWithFilter (erp.go:1071)
 │     → loop con → fetchInventoryStockForSKU
 │     → source = "cloudify_live_filtered"
 │
 ├─ Branch-2  search != ""                             (erp.go:1785)
 │     searchProductsByWebNameFromCache (erp.go:3060)  ← tra cứu MỘT lần
 │       LIKE ten_dong_bo_web → LIKE ten → resolveMaChaFuzzy (:3023)
 │                                          (embedding fuzzy → LLM fuzzy)
 │       • specific=true → trả specificSKU → search=SKU đó, matchedProducts=nil
 │                          → single-SKU (🛡️ guard, bỏ qua phân loại dưới)
 │       • len>1  → disambiguation buttons, is_inventory_rich, return (mục C)
 │       • len==1 → search = SKU đó
 │
 └─ classifyDominantMaCha(matchedProducts) (erp.go:2993) ← dùng lại rows trên, KHÔNG query lại
       • true  → getProductsByMaChaFromCache → loop con →
                 fetchInventoryStockForSKU → tồn từng biến thể (Kịch bản D)
       • false → single-SKU live call (erp.go:1924, mục F)
```

---

## F. Đọc tồn kho thực — Cloudify ERP

```
fetchInventoryStockForSKU(sku)  (erp.go:2478)
   │
   ├─ cache.Get(tenant, sku) HIT → return        ← InventoryStockCache (in-process)
   │
   └─ MISS → inventoryStockRequestBody(endpoint, sku)  (erp.go:2434)
              • lay_ton_kho_san_pham (default) → {"MA_HANG": sku}
              • custom endpoint                → {"limit": n, "MA_HANG": sku}
         → client.SearchCustomEndpoint[WithBody]  → POST {Cloudify}/api/v1/…
         → parse:
              • lay_ton_kho_san_pham → totalStockFromInventoryItems (erp.go:2448)
                  CHỈ cộng SO_LUONG_TON của các dòng kho == "Kho Tổng"
                  (trong mảng TON_KHO_CHI_TIET, hoặc dòng phẳng);
                  BỎ QUA SO_LUONG_TON_TONG và mọi kho chi nhánh khác
              • custom endpoint → cộng stock/ton/ton_kho/SO_LUONG_TON_* các dòng
         → cache.Set(tenant, sku, total) → return total

Single-SKU call trực tiếp (erp.go:1924) dùng cùng inventoryStockRequestBody;
nếu endpoint == lay_ton_kho_san_pham thì gộp về 1 record qua
totalStockFromInventoryItems (erp.go:1946).

Response cuối (Backend → Langflow tool):
  {
    "status": "success",
    "source": "cloudify_live" | "cloudify_live_filtered",
    "resource": "inventory",
    "data": [ {"MA": "FF901-RED-L", "ma_cha": "FF901",
               "THUOC_TINH_1": "Gloss Black", "THUOC_TINH_2": "L",
               "TON_KHO": 12, "ton_kho": 12}, … ],
    "count": N
  }
```

---

## G. Cheat-sheet `file:line`

| Bước | File:Line | Function |
|---|---|---|
| Webhook entry | `backend/api/handlers/webhooks.go:17` | `ZaloWebhookHandler` |
| Worker entry | `backend/workers/tasks.go:185` | `HandleZaloWebhookTask` |
| Numeric-reply intercept | `backend/workers/tasks.go:797` | resolve "1/2/3" từ `pending_options` (sau `permCtx`) |
| Resolve số → postback | `backend/workers/tasks.go:56` | `resolveNumericSelection` |
| Lưu menu vào Redis | `backend/workers/tasks.go:77` | `storePendingOptions` (`<sessionKey>:pending_options`) |
| Shortcut `dongsp` | `backend/workers/tasks.go:821` | `#choose_flow_type:dongsp:` (len==1 → sum ngay; len>1 → lưu pending) |
| Shortcut `skucuthe` | `backend/workers/tasks.go:876` | `#choose_flow_type:skucuthe:` |
| Web-name ranking | `backend/workers/tasks.go:832` | `engine.RankProductWebGroups` |
| `#show_macha_options` | `backend/workers/tasks.go:890` | parent-code options |
| `#show_macha_options_by_web` | `backend/workers/tasks.go:979` | aggregate theo web name |
| Aggregate tồn theo dòng | `backend/workers/tasks.go:2034` | `sumInventoryByMaChaAndWebName` |
| ERPQuery entry | `backend/api/handlers/erp.go:103` | `ERPQuery` |
| product_variants inherit grant | `backend/api/handlers/erp.go:208` | `methodPermissionResource` → products |
| products resource path | `backend/api/handlers/erp.go:274` | products (web-group/embedding/LLM) |
| Embedding fuzzy (products) | `backend/api/handlers/erp.go:396` | gọi `resolveMaChaFuzzy` (bỏ qua `specific`) → `engine.FuzzyMatchProductWithEmbedding` |
| product_variants resource | `backend/api/handlers/erp.go:466` | attribute lookup |
| Variant attr search | `backend/api/handlers/erp_variants.go:25` | `searchVariantsByAttributes` |
| Bilingual attr fallback | `backend/api/handlers/erp.go:565` | `fuzzyMatchAttributesWithLLM` (def `erp_fuzzy.go:192`) |
| Live data dispatch | `backend/api/handlers/erp.go:1698` | `respondWithLiveDataV2` |
| Inventory Branch-1 (filtered) | `backend/api/handlers/erp.go:1732` | `searchProductsFromCacheWithFilter` (:1071) |
| Inventory Branch-2 (web-name) | `backend/api/handlers/erp.go:1785` | `searchProductsByWebNameFromCache` (:3060, MySQL cache) |
| Specific-SKU guard (inventory) | `backend/api/handlers/erp.go:1788` | `specificSKU != ""` → single-SKU, bỏ qua family/disambiguation |
| Resolver fuzzy dùng chung (embedding→LLM, trả `specific`) | `backend/api/handlers/erp.go:3023` | `resolveMaChaFuzzy` (products + inventory) |
| Disambiguation push | `backend/api/handlers/erp.go:1812` | flow-type buttons, `is_inventory_rich` |
| Phân loại dòng/SKU (dùng lại rows) | `backend/api/handlers/erp.go:2993` | `classifyDominantMaCha` (gọi `dominantMaCha` :2965) |
| Fetch by ma_cha | `backend/api/handlers/erp.go:2920` | `getProductsByMaChaFromCache` |
| Single-SKU live | `backend/api/handlers/erp.go:1924` | `inventoryStockRequestBody` (:2434) |
| Stock per SKU | `backend/api/handlers/erp.go:2478` | `fetchInventoryStockForSKU` |
| Kho Tổng aggregate | `backend/api/handlers/erp.go:2448` | `totalStockFromInventoryItems` |
| Endpoint constants | `backend/api/handlers/erp.go:2419,2423` | `inventoryTotalStockEndpoint`, `inventoryTotalWarehouseName` |
| Specific-SKU decision (đã có test) | `backend/engine/product_embeddings.go:251` | `isSpecificSKUMatch` (`TestIsSpecificSKUMatch`) |
| Embedding sync (offline) | `backend/engine/product_embeddings.go:108` | `SyncProductEmbeddingsToAstraDB` |
| Embedding matcher | `backend/engine/product_embeddings.go:207` | `FuzzyMatchProductWithEmbedding` |

---

## H. Ghi chú vận hành

- **`product_variants` thừa kế quyền của `products`** (`methodPermissionResource`,
  erp.go:208) — tenant không cần cấu hình resource thứ hai.
- **Helper cache giờ là `searchProductsByWebNameFromCache`** (đổi tên từ
  `…AstraDBNonVectorized` cho khớp nguồn dữ liệu) — đọc cache MySQL nội bộ
  (`models.CachedProduct` qua `db.DB`), không gọi Astra. Bản `erp.go` (:3060)
  tra tuần tự `ten_dong_bo_web` → `ten` → `resolveMaChaFuzzy` (embedding→LLM);
  bản `tasks.go` (:1976) gộp `ten_dong_bo_web OR ma OR ten OR ma_cha` trong một LIKE.
- **Tồn kho `lay_ton_kho_san_pham` chỉ lấy "Kho Tổng"** —
  `totalStockFromInventoryItems` bỏ qua `SO_LUONG_TON_TONG` và mọi kho chi nhánh.
  Đổi kho gốc → sửa hằng `inventoryTotalWarehouseName` (erp.go:2423).
- **Size khớp chính xác, màu/brand khớp substring** (erp_variants.go:38–44) — nên
  "L" không dính "XL"/"XXL"; `normalizeSizeFilter` (erp_variants.go:288) cắt tiền
  tố "size "/"cỡ ".
- **Fallback song ngữ** chỉ chạy khi `product_variants` trả rỗng và có ít nhất 1
  filter; retry đúng 1 lần, sau đó trả `available_colors/sizes/brands` cho Agent
  hỏi lại khách.
- **Cache tồn kho in-process** (`InventoryStockCache`): mỗi pod có cache riêng →
  burst traffic có thể nhân theo số pod. Cân nhắc khi scale.
- **Embedding fuzzy** (`ERP_EMBEDDING_FUZZY_ENABLED=true`) phục vụ **cả `products`
  lẫn `inventory`** qua helper dùng chung `resolveMaChaFuzzy` (erp.go:3023):
  embedding trước → LLM (`fuzzyMatchMaChaWithLLM`) sau, chỉ chạy khi cả hai LIKE
  pass của `searchProductsByWebNameFromCache` đều rỗng (vd query là mã SKU con).
  `resolveMaChaFuzzy` trả `(ma, ma_cha, specific)`; `specific` do
  `FuzzyMatchProductWithEmbedding`/`isSpecificSKUMatch` tự tính.
  🛡️ **Guard chống "kéo SKU rõ về cả dòng":** khi embedding pinpoint đúng 1 SKU
  (`specific=true`), `searchProductsByWebNameFromCache` trả thẳng `specificSKU` và
  KHÔNG mở rộng theo `ma_cha`; nhánh `inventory` để `matchedProducts=nil` → đi
  single-SKU live call với đúng SKU đó (bỏ qua `classifyDominantMaCha` và
  disambiguation). Resource `products` cố tình bỏ qua `specific` (luôn trả cả dòng
  cho `price_range`). Job sync embedding (`SyncProductEmbeddingsToAstraDB`) chỉ
  chạy theo chain sau khi rebuild product cache, không có schedule mặc định → có
  thể stale.
- **Zalo OA list template** trả `-233` trên `/message/cs` → tất cả nút disambiguation
  fallback về plain text đánh số (`channels.BuildButtonOptionsAsText`). Riêng bộ
  option `TEN_DONG_BO_WEB` của nhánh `dongsp` được lưu `pending_options` trong Redis
  để khách **gõ số** (`1`/`2`/`3`) chọn lại; chỉ 1 dòng khớp thì bỏ qua bước chọn,
  cộng tồn ngay (`resolveNumericSelection` + `storePendingOptions`).
- **`DEBUG_PUSH_FALLBACK_TO_ZALO=true`**: push payload trung gian
  (`exact_web`/`raw_like_groups`/`raw_like`/`slim`) của resource `products` tới
  admin Zalo để soi từng giai đoạn.

# Luồng truy vấn tồn kho khi khách hỏi bot

Tài liệu này mô tả end-to-end luồng xử lý khi khách hàng nhắn cho bot một câu
hỏi **tồn kho** trên Zalo OA. Tài liệu bám sát **code hiện tại** (nhánh
`respondWithLiveDataV2`, erp.go:1700 + resource `product_variants`).

> ⚠️ **Tài liệu này thay thế mô hình cũ.** Bản trước mô tả nhánh tồn kho theo
> "Phase B web-groups / Phase C1-embedding/LLM". Mô hình web-group đó **vẫn còn
> nhưng đã chuyển sang resource `products`** và **handler disambiguation
> `dongsp`** trong `tasks.go` — KHÔNG nằm trong nhánh `inventory`.
>
> ✅ **Cập nhật:** Embedding fuzzy (`FuzzyMatchProductWithEmbedding`) **giờ phục vụ
> CẢ `inventory`** thông qua helper dùng chung `resolveMaChaFuzzy` (erp.go:3131).
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
     │           │              │            4. HandleZaloWebhookTask (workers/tasks.go:132)            │
     │           │              │               • match OA/channel, resolve customer + permission       │
     │           │              │               • session (Redis), lưu user msg (Astra)                 │
     │           │              │               • classifyMessageIntent → IN_SCOPE                      │
     │           │              │               • numeric-reply intercept (tasks.go:744, xem mục C):    │
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

## B. Bộ quy tắc điều phối của Agent (nguồn: `docs/admin/system-prompt.md`)

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
        respondWithLiveDataV2 → case "inventory" (erp.go:1707)
                                  │  parentCode=="" → bỏ qua Branch-1 (filtered)
                                  ▼
 ┌──────────────────────────────────────────────────────────────────────────┐
 │ ⮕ TRA CỨU MỘT LẦN  (erp.go:1872)                                          │
 │   searchProductsByWebNameFromCache (:3168, đọc MySQL cache)                │
 │   LIKE ten_dong_bo_web "%FF901%" → LIKE ten → resolveMaChaFuzzy (:3131):   │
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
 ║ (erp.go:1900) ║                                ▼
 ║ Đẩy Zalo nút: ║   classifyDominantMaCha(matchedProducts)  (erp.go:1981 → :3101)
 ║ 📦 dòng SP    ║     • filterProductsByGroups → dominantMaCha (:3073)
 ║   dongsp:FF901║     • xác nhận dòng có >1 biến thể (getProductsByMaChaFromCache :3028)
 ║ 🔍 SKU cụ thể ║     *** DÙNG LẠI matchedProducts — KHÔNG tra cứu/LLM lại ***
 ║   skucuthe:.. ║                                │
 ║ → is_inventory║                     ┌──────────┴──────────┐
 ║   _rich,      ║                isMaCha=true          isMaCha=false
 ║   data=[]     ║                     │                     │
 ║ + Redis pend  ║                     ▼                     ▼
 ║   ing_options ║          getProductsByMaCha       single-SKU live call
 ║ → return      ║          FromCache (:3028)        (erp.go:2032, xem mục F)
 ╚══════╤════════╝          loop mỗi con →                   │
        │                   fetchInventoryStockForSKU        ▼
        │                          │              lay_ton_kho_san_pham
        │                          ▼              → totalStockFromInventoryItems
        │               data=[{MA, THUOC_TINH_1,     (chỉ "Kho Tổng")
        │                 THUOC_TINH_2, TON_KHO},…] → {MA, TON_KHO}
        │               (tồn từng biến thể của dòng = Kịch bản D)
        ▼
   ────────── Khách chọn dòng / SKU ──────────────────────────────────────
   #choose_flow_type:dongsp:FF901  (tasks.go:768)
       searchProductsByWebNameFromCache → RankProductWebGroups (:779)
       → dựng tối đa 3 option theo TEN_DONG_BO_WEB:
            #show_macha_options_by_web:<TEN_DONG_BO_WEB>   (build tasks.go:790)
            #show_macha_options:<MA_CHA>  (fallback, build tasks.go:788)
       │
       ├─ len == 1 (1 dòng khớp, KHÔNG mơ hồ)
       │     userText ← postback[0]  → fall-through thẳng xuống
       │     #show_macha_options_by_web → sumInventoryByMaChaAndWebName
       │     → tổng tồn + chi tiết theo biến thể  (KHÔNG hỏi lại)
       │
       └─ len > 1 (nhiều dòng, còn mơ hồ)
             BuildButtonOptionsAsText → tin nhắn đánh số "1. … / 2. …"
             + engine.StorePendingOptions → Redis <sessionKey>:pending_options
                                            (TTL = session timeout)
             → return, CHỜ khách gõ số

   ────────── Khách gõ số "1"/"2"/"3" ────────────────────────────────────
   numeric-reply intercept (tasks.go:744, ngay sau permCtx, trước các handler #…)
       Redis GET <sessionKey>:pending_options → engine.ResolveNumericSelection
       • số hợp lệ  → DEL pending; userText ← postback đã lưu → fall-through
                       #show_macha_options_by_web → sumInventoryByMaChaAndWebName
       • số ngoài khoảng → "Vui lòng chọn một số trong danh sách."
                       (GIỮ pending để gõ lại) → return
       • không phải số / không có pending → bỏ qua, đi luồng Langflow

   #choose_flow_type:skucuthe:FF901  (tasks.go:823)
       Bot hỏi: "… màu và size nào? (Ví dụ: FF901 màu đỏ size L)"
       → khách trả lời màu+size → chuyển sang KỊCH BẢN 2

   ────────── ⚠️ Level-1 cũng có pending_options ──────────────────────────
   Khi disambiguation đầu tiên ở erp.go:1900 đẩy 2 nút "📦 dòng SP / 🔍 SKU
   cụ thể", backend dùng chính sessionKey của worker (engine.BuildSessionKey)
   để engine.StorePendingOptions với 2 postback `#choose_flow_type:dongsp:<kw>`
   và `#choose_flow_type:skucuthe:<kw>`. Khách gõ "1" hay "2" trên message
   này được numeric-reply intercept (tasks.go:744) nuốt thẳng — không lên
   Langflow, không cần Agent đoán.
```

**Tóm tắt Kịch bản 1:** một mã/keyword trần như "FF901" gần như luôn kích hoạt
bộ chọn *dòng sản phẩm vs SKU cụ thể*. Khi khách chọn **dòng**: nếu chỉ **một**
dòng (`TEN_DONG_BO_WEB`) khớp thì backend cộng tồn và trả thẳng chi tiết biến thể
ngay; nếu **nhiều** dòng thì bot gửi danh sách đánh số và lưu `pending_options`,
khách **gõ số** (`1`/`2`/`3`) là chạy thẳng `sumInventoryByMaChaAndWebName` —
không còn bắn Zalo list-template button, cũng không round-trip qua Langflow. Ngoài
ra khi `search` thu hẹp về một `MA_CHA` rõ ràng (nhánh `classifyDominantMaCha=true`,
Kịch bản D) backend trả danh sách tồn từng biến thể ngay trong lượt đầu.

### C.1 — Lối vào THAY THẾ: products disambiguation → gõ số → exact-web (worker chặn)

Thực tế Agent thường KHÔNG gọi `inventory("FF901")` ngay mà gọi `products("FF901")`
trước (theo Product Intent Routing trong system-prompt). Khi đó luồng là:

```
1. Agent → products(search="FF901")
   → erp.go:336 trả source="astradb_cache_web_groups", data=[{web_name, parent_codes,
     variant_count}, …]
   ✅ MỚI (2026-06-01): backend ĐỒNG THỜI lưu pending_options dưới session key dùng
     chung (storePendingDisambiguationOptions, erp.go) với postback
     #stockpick_web:<web_name> cho TỪNG dòng — đúng thứ tự webGroups để index khớp.
     Cú "1" của khách KHÔNG còn do Agent xử lý.
2. Agent tự liệt kê "1. LS2 FF901 / 2. LS2 FF901 Carbon" rồi chờ khách
3. Khách gõ "1"
   → numeric-intercept của worker (tasks.go:744) GET pending_options → resolve thành
     #stockpick_web:LS2 FF901 → handler #stockpick_web (tasks.go) →
     engine.BuildExactWebStockPicker → đẩy EXACT-web dòng-vs-SKU picker + lưu pending
     mới [#show_macha_options_by_web:LS2 FF901, #choose_flow_type:skucuthe:LS2 FF901].
     KHÔNG lên Langflow; KHÔNG phụ thuộc Agent set exact_web_name hay trả
     [RICH_MESSAGE_SENT]; KHÔNG re-LIKE (nên "LS2 FF901" không dính "LS2 FF901 Carbon").
4. Khách gõ "1" (dòng) → #show_macha_options_by_web:LS2 FF901 → tổng tồn cả dòng
   Khách gõ "2" (SKU)  → #choose_flow_type:skucuthe:LS2 FF901 → hỏi màu/size (Kịch bản 2)
```

> 🔁 **Fallback Agent (chỉ khi worker KHÔNG chặn được số):** nếu pending_options hết
> hạn (TTL), Redis lỗi, hoặc khách trả bằng FREE-TEXT/mã `SP\d{6}` thay vì số trần →
> "1"/web-name lên thẳng Langflow và Agent dùng "Disambiguation Follow-up Rules"
> (map digit→web_name, gọi `inventory(exact_web_name=true)` → Branch-0 erp.go:1793,
> trả `[RICH_MESSAGE_SENT]`). Đường này giờ là DỰ PHÒNG, không phải đường chính.

> ⚖️ **Đánh đổi intent GIÁ:** mọi cú gõ-số trên danh sách products giờ deterministic
> rơi vào picker TỒN KHO. Câu hỏi GIÁ rõ ràng ("FF901 *giá* bao nhiêu") mà bị
> disambiguation rồi gõ "1" cũng ra picker tồn — khớp default "ambiguous → STOCK" của
> system-prompt, nhưng khác đường `price_range` cũ. Nếu cần giữ giá: thêm nút thứ 3
> hoặc nhận diện "giá" ở handler `#stockpick_web`.

> 🐞 **Bug đã sửa (2026-06-01):** trước đây Agent map "1" xong lại gọi
> `product_variants(parent_code)` → trả 10 biến thể KHÔNG kèm tồn → hỏi màu/size, KHÔNG
> bắn câu hỏi dòng-vs-SKU. Nguyên nhân: (a) anchor nhận diện disambiguation trong prompt
> dò đúng chuỗi "Tôi tìm thấy nhiều sản phẩm" trong khi bot phát ra "Tôi tìm thấy 2 dòng
> sản phẩm"; (b) luật "resolve MA qua product_variants trước" lấn át luật hẹp "stock-pick
> → inventory". Đã nới anchor + ép STOCK-pick gọi `inventory(exact_web_name=true)` + thêm
> Branch-0 exact-web (tránh LIKE "LS2 FF901" dính "LS2 FF901 Carbon").

> 🐞 **Bug tái phát + đã siết (2026-06-01) — sau khi chọn dòng, Agent gọi `product_variants`
> với `parent_code` bịa:** Agent map "1" → "LS2 FF901" nhưng KHÔNG gọi `inventory(exact_web_name=true)`;
> thay vào đó gọi `product_variants(parent_code="LS2-FF901", exact_web_name=true)` — `parent_code`
> bị **chế ra từ web_name** (thay space bằng gạch nối) thay vì copy `parent_codes[0]="SP458484"`
> từ response `products` trước đó (giá trị đúng NẰM sẵn trong history). Backend nhận `ma_cha="LS2-FF901"`
> → 0 dòng → rơi vào (0a) `resolveParentMaCha` chạy embedding fuzzy trên chuỗi bịa; guard
> `parentCodeInLabel` (normalize bỏ space/dash) khớp NHẦM một dòng phụ kiện FF900 có token "FF901"
> trong label → trả SKU sai SP458323 + giá 990k. **Root cause chính là tầng Agent/prompt** (sai tool
> + bịa parent_code), backend chỉ "đoán bừa" thay vì trả rỗng. **Đã siết hai tầng:**
> (a) **prompt** — HARD GUARDRAIL cấm gọi `product_variants` sau bước chọn dòng khi chưa có màu+size,
> bắt copy `parent_codes[]` VERBATIM, không bịa từ web_name (`system-prompt.md`);
> (b) **backend** — `product_variants` chỉ chạy embedding parent-resolution (erp.go:511) khi có
> **ít nhất một thuộc tính** color/size/brand; truy vấn variant không thuộc tính + parent_code không
> khớp ma_cha giờ trả `count=0` thay vì resolve sang dòng khác.

> 🐞 **Bug đã sửa (2026-06-01) — "Tổng tồn kho của dòng LS2 FF901: 0.0":** khi khách chọn
> **theo dòng**, worker gọi `sumInventoryByMaChaAndWebName` (và bản song sinh
> `sumInventoryByMaCha`). Trước đây hai hàm này gọi `client.SearchInventory(maCha)` → endpoint
> **cũ** `inventory_receipt/search`, vốn trả **HTTP 500** từ Cloudify. Vòng lặp
> `#show_macha_options_by_web` lại **nuốt lỗi** (`continue`) → `totalStock=0`, chi tiết rỗng →
> bot báo "0.0" như thể hết hàng. **Đã sửa:** (a) cả hai hàm sum giờ lặp từng SKU con và gọi
> `pkg.CloudifyClient.InventoryStock(endpoint, usePost, sku)` với **endpoint lấy từ cấu hình
> Global HTTP Method** (`erp_global_method_permissions`) qua `engine.ResolveInventoryEndpoint`
> — đúng nguồn mà luồng theo-SKU dùng, KHÔNG hardcode đường nào; (b) giữ **nguyên dạng (case)
> của MA_HANG** vì ERP phân biệt hoa/thường; (c) khi **mọi** SKU/mã cha đều lỗi, worker trả
> thông báo "Hệ thống tồn kho đang tạm thời gặp sự cố… thử lại sau" thay vì "0.0". Logic chọn
> "Kho Tổng" + hằng endpoint rút về **một nguồn chung** trong `backend/pkg/inventory_stock.go`;
> việc đọc cấu hình endpoint per-tenant rút về `engine.ResolveInventoryEndpoint`
> (`backend/engine/inventory_endpoint.go`), dùng chung cho cả handler lẫn worker; `erp.go`
> chỉ còn alias mỏng.

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
   → erp.go:479 → searchVariantsByAttributes (erp_variants.go:25)
        WHERE tenant_id=? AND ma_cha="FF901"
          AND LOWER(thuoc_tinh_1) LIKE LOWER('%đỏ đen%')   ← màu: substring
          AND LOWER(thuoc_tinh_2) = LOWER('L')             ← size: KHỚP CHÍNH XÁC
                                                             (normalizeSizeFilter bỏ "size ")
   → slimVariantsForLLM → data=[{ma, name, color, size, price}]  (KHÔNG có tồn)
        source="astradb_cache_variants"  (erp.go:496)
                                  │
            ┌──────────────────────┴───────────────────────┐
       data có kết quả                            data rỗng (erp.go:565)
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
     (erp.go:2032).  🛡️ Guard: KHÔNG collapse về cả dòng dù embedding khớp.
   → lay_ton_kho_san_pham → totalStockFromInventoryItems (chỉ "Kho Tổng")
   → data=[{MA:"FF901-RED-L", TON_KHO: 12, ton_kho: 12}]
```

**Reply mẫu:** *"FF901 (Đỏ đen, size L) hiện còn 12 cái."* Nếu Bước 2 trả
`bilingual_match`, Agent nêu cả tên chuẩn lẫn cách khách gọi: *"Gloss Black –
đỏ đen, size L: còn 12 cái."*

> 🐞 **Bug đã sửa (2026-06-01) — sau `skucuthe`, Agent dừng ở `product_variants` trả giá thay vì
> tồn:** khách bấm "🔍 xem theo mã SKU cụ thể" (nút nằm dưới luồng tồn kho) rồi nhập "nardo grey
> size XL". Agent chạy `products` → `product_variants` → resolve đúng `ma=SP458493`, đọc `price`
> rồi **DỪNG**, báo giá — bỏ Bước 3 `inventory`. Hai nguyên nhân cộng dồn: (a) hint của tool
> `product_variants` (`ERPGatewayCaller.component.py` `_format_variant_response`) hard-code "chốt
> giá cụ thể", không nhắc bước inventory; (b) tin nhắn màu/size không có chữ "tồn"/"còn" nên Agent
> đọc nhầm thành nhánh C (giá) dù history rõ là STOCK. **Đã siết hai tầng:** (a) **tool** — hint
> đổi thành intent-aware: "BƯỚC GIỮA, nếu intent là tồn → BẮT BUỘC gọi tiếp `inventory(search=<ma>)`,
> KHÔNG dừng ở giá vì `product_variants` không có tồn"; (b) **prompt** (`system-prompt.md`) — thêm
> HARD GUARDRAIL "STOCK-pick continuation": sau nhánh `skucuthe` + màu/size, intent MẶC ĐỊNH là
> STOCK, phải hoàn tất nhánh B 3 bước; chỉ chốt giá khi khách hỏi GIÁ rõ ràng.

> 🐞 **Bug đã sửa (2026-06-01) — sau `skucuthe`, đáp án trả tồn của CẢ HAI dòng anh em:** khách chọn
> dòng "LS2 FF901" (KHÔNG phải "LS2 FF901 Carbon") → bấm "🔍 mã SKU cụ thể" → nhập "nardo grey size XL".
> Bot trả "LS2 FF901: 25 / LS2 FF901 Carbon: 0" — tồn của cả hai dòng. **Root cause là quản lý context:**
> handler `#choose_flow_type:skucuthe:` (`tasks.go:824`) chỉ gửi câu hỏi màu/size rồi `return nil`,
> **KHÔNG lưu state** (khác `dongsp` và `#stockpick_web` đều `StorePendingOptions`). Tên dòng đã chọn chỉ
> nằm trong prose; lượt free-text màu/size kế tiếp rơi thẳng lên Langflow (tasks.go:~1230) không kèm
> ràng buộc dòng. Agent re-derive bare model code "FF901" → LIKE-trùng cả hai dòng. **Đã siết hai tầng:**
> (a) **backend** — skucuthe gọi `engine.StoreAwaitingVariantLine(sessionKey, web_name)` (single-use,
> TTL = session); ngay trước khi gọi Langflow, nếu userText là free-text (không bắt đầu `#`) và lock tồn
> tại → `engine.TakeAwaitingVariantLine` (GET+DEL) rồi **chèn cứng** `[DÒNG ĐÃ CHỌN: <web> …]` vào
> userText để Agent buộc scope đúng dòng dù bỏ qua prose history; (b) **prompt** (`system-prompt.md`) —
> "KHÓA ĐÚNG DÒNG ĐÃ CHỌN": Bước 1 phải `products(search=<tên dòng đã chọn>, exact_web_name=true)` để lấy
> `parent_codes[0]` DUY NHẤT, cấm search bare "FF901". Helpers ở `engine/session_options.go`
> (`StoreAwaitingVariantLine`/`TakeAwaitingVariantLine`, có test nil-safe + suffix-stable).

---

## E. Chi tiết nhánh `inventory` backend (`respondWithLiveDataV2`, erp.go:1700)

Cấu hình endpoint (đầu `case "inventory"`, erp.go:1707):
- Mặc định `danhmucvattuhanghoa/lay_ton_kho_san_pham` (POST) — đường tồn kho chính.
- Tenant có thể override qua setting `erp_global_method_permissions` sang một
  endpoint tùy biến khác (custom path).
- Hằng số: `inventoryTotalStockEndpoint` (erp.go:2527),
  `inventoryTotalWarehouseName = "Kho Tổng"` (erp.go:2531).

Cây quyết định 3 nhánh:

```
case "inventory":
 ├─ Branch-1  parentCode != "" && search != ""        (erp.go:1734)
 │     searchProductsFromCacheWithFilter (erp.go:1073)
 │     → loop con → fetchInventoryStockForSKU
 │     → source = "cloudify_live_filtered"
 │
 ├─ Branch-0  exactWebName == true && search != ""     (erp.go:1793)
 │     Agent đã chọn 1 web-name từ danh sách dòng SP (vd "LS2 FF901") và
 │     truyền exact_web_name=true để KHÔNG re-LIKE (LIKE "LS2 FF901" sẽ
 │     dính cả "LS2 FF901 Carbon" → disambiguation lặp).
 │     searchProductsByExactWebNameFromCache (khớp ten_dong_bo_web CHÍNH XÁC)
 │       • len>1 → đẩy Level-1 dòng-vs-SKU picker; nút "📦 dòng" trỏ thẳng
 │                 #show_macha_options_by_web:<web> (sum khớp web CHÍNH XÁC),
 │                 nút "🔍 SKU" trỏ #choose_flow_type:skucuthe:<web>;
 │                 StorePendingOptions → is_inventory_rich, return
 │       • len==1 → collapse search = SKU đó → rơi xuống single-SKU
 │       • len==0 → rơi xuống Branch-2 (LIKE/fuzzy)
 │
 ├─ Branch-2  search != "" (exactWebName==false)        (erp.go:1872)
 │     searchProductsByWebNameFromCache (erp.go:3168)  ← tra cứu MỘT lần
 │       LIKE ten_dong_bo_web → LIKE ten → resolveMaChaFuzzy (:3131)
 │                                          (embedding fuzzy → LLM fuzzy)
 │       • specific=true → trả specificSKU → search=SKU đó, matchedProducts=nil
 │                          → single-SKU (🛡️ guard, bỏ qua phân loại dưới)
 │       • len>1  → disambiguation buttons, is_inventory_rich, return (mục C)
 │       • len==1 → search = SKU đó
 │
 └─ classifyDominantMaCha(matchedProducts) (erp.go:3101) ← dùng lại rows trên, KHÔNG query lại
       • true  → getProductsByMaChaFromCache → loop con →
                 fetchInventoryStockForSKU → tồn từng biến thể (Kịch bản D)
       • false → single-SKU live call (erp.go:2032, mục F)
```

---

## F. Đọc tồn kho thực — Cloudify ERP

```
fetchInventoryStockForSKU(sku)  (erp.go:2586)
   │
   ├─ cache.Get(tenant, sku) HIT → return        ← InventoryStockCache (in-process)
   │
   └─ MISS → inventoryStockRequestBody(endpoint, sku)  (erp.go:2542)
              • lay_ton_kho_san_pham (default) → {"MA_HANG": sku}
              • custom endpoint                → {"limit": n, "MA_HANG": sku}
         → client.SearchCustomEndpoint[WithBody]  → POST {Cloudify}/api/v1/…
         → parse:
              • lay_ton_kho_san_pham → totalStockFromInventoryItems (erp.go:2556)
                  CHỈ cộng SO_LUONG_TON của các dòng kho == "Kho Tổng"
                  (trong mảng TON_KHO_CHI_TIET, hoặc dòng phẳng);
                  BỎ QUA SO_LUONG_TON_TONG và mọi kho chi nhánh khác
              • custom endpoint → cộng stock/ton/ton_kho/SO_LUONG_TON_* các dòng
         → cache.Set(tenant, sku, total) → return total

Single-SKU call trực tiếp (erp.go:2032) dùng cùng inventoryStockRequestBody;
nếu endpoint == lay_ton_kho_san_pham thì gộp về 1 record qua
totalStockFromInventoryItems (erp.go:2548).

> 📦 **Nguồn chung (single source of truth):**
> - **Chọn endpoint theo cấu hình:** `engine.ResolveInventoryEndpoint(ctx, tenantID)`
>   (`backend/engine/inventory_endpoint.go`) đọc setting `erp_global_method_permissions`
>   → trả `(endpoint, usePost)`; mặc định `lay_ton_kho_san_pham` + POST, có override thì
>   theo `path`/`post` của admin. **Cả handler lẫn worker đều gọi hàm này** — không nơi
>   nào hardcode đường dẫn.
> - **Gọi + parse:** `pkg.CloudifyClient.InventoryStock(endpoint, usePost, sku)` dựng body
>   `{"MA_HANG": sku}` (official) hoặc `{limit, MA_HANG}` (custom), gọi POST/GET theo
>   `usePost`. Endpoint official → `pkg.TotalStockFromInventoryItems` (chỉ "Kho Tổng");
>   custom → cộng `stock/ton/SO_LUONG_TON_*` các dòng.
> - **Hằng + parse "Kho Tổng":** `pkg.InventoryTotalStockEndpoint`,
>   `pkg.InventoryTotalWarehouseName`, `pkg.TotalStockFromInventoryItems` trong
>   `backend/pkg/inventory_stock.go`; `erp.go` để
>   `inventoryTotalStockEndpoint`/`totalStockFromInventoryItems` làm **alias mỏng**.
> - **Luồng worker theo-dòng** (`sumInventoryByMaCha`, `sumInventoryByMaChaAndWebName`)
>   resolve endpoint qua `engine.ResolveInventoryEndpoint` rồi gọi
>   `client.InventoryStock(...)` cho từng SKU con — KHÔNG còn `client.SearchInventory`
>   (`inventory_receipt/search`, đã hỏng 500). ERP lỗi luôn trả `error`, không bao giờ
>   biến thành tồn 0. Worker không dùng `InventoryStockCache` (cache chỉ ở luồng handler).

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
| Worker entry | `backend/workers/tasks.go:132` | `HandleZaloWebhookTask` |
| Numeric-reply intercept | `backend/workers/tasks.go:744` | resolve "1/2/3" từ `pending_options` (sau `permCtx`) — phục vụ cả Level-1 (dongsp/skucuthe) lẫn Level-2 (web-name) |
| Resolve số → postback | `backend/engine/session_options.go:43` | `engine.ResolveNumericSelection` |
| Lưu menu vào Redis | `backend/engine/session_options.go:65` | `engine.StorePendingOptions` (`<sessionKey>:pending_options`) |
| Build session key (dùng chung worker + handler) | `backend/engine/session_options.go:28` | `engine.BuildSessionKey` |
| Shortcut `dongsp` | `backend/workers/tasks.go:768` | `#choose_flow_type:dongsp:` (len==1 → sum ngay; len>1 → lưu pending) |
| Shortcut `skucuthe` | `backend/workers/tasks.go:823` | `#choose_flow_type:skucuthe:` |
| Shortcut `#stockpick_web` (MỚI) | `backend/workers/tasks.go` (sau `skucuthe`) | resolve cú gõ-số trên danh sách products → `engine.BuildExactWebStockPicker` (exact-web dòng-vs-SKU, không LIKE) |
| Products lưu pending (MỚI) | `backend/api/handlers/erp.go:336` | `storePendingDisambiguationOptions` + `engine.BuildStockPickPendingButtons` → postback `#stockpick_web:<web>` |
| Helper stockpick (MỚI) | `backend/engine/stock_disambiguation.go` | `StockPickWebPrefix`, `BuildStockPickPendingButtons`, `BuildExactWebStockPicker` (có `stock_disambiguation_test.go`) |
| Web-name ranking | `backend/workers/tasks.go:779` | `engine.RankProductWebGroups` |
| `#show_macha_options` | `backend/workers/tasks.go:837` | parent-code options |
| `#show_macha_options_by_web` | `backend/workers/tasks.go:926` | aggregate theo web name; mọi mã cha lỗi → báo "hệ thống tồn kho gặp sự cố" (tasks.go:967) |
| Aggregate tồn theo dòng (web) | `backend/workers/tasks.go:2008` | `sumInventoryByMaChaAndWebName` → `engine.ResolveInventoryEndpoint` + loop SKU con `client.InventoryStock` |
| Aggregate tồn theo dòng (mã cha) | `backend/workers/tasks.go:1342` | `sumInventoryByMaCha` → `engine.ResolveInventoryEndpoint` + loop SKU con `client.InventoryStock` |
| ERPQuery entry | `backend/api/handlers/erp.go:103` | `ERPQuery` |
| product_variants inherit grant | `backend/api/handlers/erp.go:208` | `methodPermissionResource` → products |
| products resource path | `backend/api/handlers/erp.go:274` | products (web-group/embedding/LLM) |
| Embedding fuzzy (products) | `backend/api/handlers/erp.go:398` | gọi `resolveMaChaFuzzy` (bỏ qua `specific`) → `engine.FuzzyMatchProductWithEmbedding` |
| product_variants resource | `backend/api/handlers/erp.go:468` | attribute lookup |
| Variant attr search | `backend/api/handlers/erp_variants.go:25` | `searchVariantsByAttributes` |
| Bilingual attr fallback | `backend/api/handlers/erp.go:567` | `fuzzyMatchAttributesWithLLM` (def `erp_fuzzy.go:192`) |
| Live data dispatch | `backend/api/handlers/erp.go:1700` | `respondWithLiveDataV2` (nhận thêm tham số `exactWebName bool`) |
| Inventory Branch-1 (filtered) | `backend/api/handlers/erp.go:1734` | `searchProductsFromCacheWithFilter` (:1073) |
| Inventory Branch-0 (exact-web) | `backend/api/handlers/erp.go:1793` | `searchProductsByExactWebNameFromCache` (:983) → Level-1 picker; nút dòng → `#show_macha_options_by_web` |
| Inventory Branch-0 store pending_options | `backend/api/handlers/erp.go:1836` | `engine.StorePendingOptions` cho exact-web dongsp/skucuthe |
| Inventory Branch-2 (web-name) | `backend/api/handlers/erp.go:1872` | `searchProductsByWebNameFromCache` (:3168, MySQL cache) |
| Specific-SKU guard (inventory) | `backend/api/handlers/erp.go:1875` | `specificSKU != ""` → single-SKU, bỏ qua family/disambiguation |
| Resolver fuzzy dùng chung (embedding→LLM, trả `specific`) | `backend/api/handlers/erp.go:3131` | `resolveMaChaFuzzy` (products + inventory) |
| Disambiguation push (Level-1, LIKE) | `backend/api/handlers/erp.go:1900` | flow-type buttons, `is_inventory_rich` |
| Level-1 store pending_options | `backend/api/handlers/erp.go:1935-1940` | `engine.BuildSessionKey` + `engine.StorePendingOptions` cho dongsp/skucuthe |
| Phân loại dòng/SKU (dùng lại rows) | `backend/api/handlers/erp.go:3101` | `classifyDominantMaCha` (gọi `dominantMaCha`) |
| Fetch by ma_cha | `backend/api/handlers/erp.go:3028` | `getProductsByMaChaFromCache` |
| Single-SKU live | `backend/api/handlers/erp.go:2014` | `inventoryStockRequestBody` |
| Stock per SKU | `backend/api/handlers/erp.go:2539` | `fetchInventoryStockForSKU` |
| Kho Tổng aggregate (alias) | `backend/api/handlers/erp.go:2530` | `totalStockFromInventoryItems` → `pkg.TotalStockFromInventoryItems` |
| Endpoint constants (alias) | `backend/api/handlers/erp.go:2508,2509` | `inventoryTotalStockEndpoint`, `inventoryTotalWarehouseName` → trỏ pkg |
| **Resolve endpoint theo config** | `backend/engine/inventory_endpoint.go:29` | `engine.ResolveInventoryEndpoint` (đọc `erp_global_method_permissions`; handler erp.go:1712 + worker dùng chung) |
| **Nguồn chung tồn kho** | `backend/pkg/inventory_stock.go:13,17,30,58,73` | `InventoryTotalStockEndpoint`, `InventoryTotalWarehouseName`, `TotalStockFromInventoryItems`, `InventoryStockRequestBody`, `CloudifyClient.InventoryStock` |
| Specific-SKU decision (đã có test) | `backend/engine/product_embeddings.go:251` | `isSpecificSKUMatch` (`TestIsSpecificSKUMatch`) |
| Embedding sync (offline) | `backend/engine/product_embeddings.go:108` | `SyncProductEmbeddingsToAstraDB` |
| Embedding matcher | `backend/engine/product_embeddings.go:207` | `FuzzyMatchProductWithEmbedding` |

---

## H. Ghi chú vận hành

- **`product_variants` thừa kế quyền của `products`** (`methodPermissionResource`,
  erp.go:208) — tenant không cần cấu hình resource thứ hai.
- **Helper cache giờ là `searchProductsByWebNameFromCache`** (đổi tên từ
  `…AstraDBNonVectorized` cho khớp nguồn dữ liệu) — đọc cache MySQL nội bộ
  (`models.CachedProduct` qua `db.DB`), không gọi Astra. Bản `erp.go` (:3168)
  tra tuần tự `ten_dong_bo_web` → `ten` → `resolveMaChaFuzzy` (embedding→LLM);
  bản `tasks.go` (:1934) gộp `ten_dong_bo_web OR ma OR ten OR ma_cha` trong một LIKE.
- **Tồn kho `lay_ton_kho_san_pham` chỉ lấy "Kho Tổng"** —
  `pkg.TotalStockFromInventoryItems` bỏ qua `SO_LUONG_TON_TONG` và mọi kho chi nhánh.
  Đổi kho gốc → sửa hằng `pkg.InventoryTotalWarehouseName`
  (`backend/pkg/inventory_stock.go`), KHÔNG còn ở erp.go (giờ chỉ là alias).
- **Luồng "theo dòng" và "theo SKU" giờ đọc CÙNG endpoint** — được resolve từ cấu hình
  Global HTTP Method (`erp_global_method_permissions`) qua `engine.ResolveInventoryEndpoint`,
  rồi gọi `pkg.CloudifyClient.InventoryStock(endpoint, usePost, sku)`. Mặc định
  `lay_ton_kho_san_pham` + POST; admin đổi `path`/`post` thì cả hai luồng đều theo — KHÔNG
  nơi nào hardcode. Endpoint cũ `inventory_receipt/search` (`client.SearchInventory`) đã
  ngừng dùng cho tồn kho (Cloudify trả HTTP 500); nếu mọi SKU lỗi, worker báo "hệ thống tồn
  kho gặp sự cố", không trả "0.0" gây hiểu nhầm hết hàng.
- **Size khớp chính xác, màu/brand khớp substring** (erp_variants.go:38–44) — nên
  "L" không dính "XL"/"XXL"; `normalizeSizeFilter` (erp_variants.go:288) cắt tiền
  tố "size "/"cỡ ".
- **Fallback song ngữ** chỉ chạy khi `product_variants` trả rỗng và có ít nhất 1
  filter; retry đúng 1 lần, sau đó trả `available_colors/sizes/brands` cho Agent
  hỏi lại khách.
- **Cache tồn kho in-process** (`InventoryStockCache`): mỗi pod có cache riêng →
  burst traffic có thể nhân theo số pod. Cân nhắc khi scale.
- **Embedding fuzzy** (`ERP_EMBEDDING_FUZZY_ENABLED=true`) phục vụ **cả `products`
  lẫn `inventory`** qua helper dùng chung `resolveMaChaFuzzy` (erp.go:3131):
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
  fallback về plain text đánh số (`channels.BuildButtonOptionsAsText`). Các cấp
  menu CÙNG lưu `pending_options` trong Redis qua `engine.StorePendingOptions`
  để numeric intercept (tasks.go:744) → `engine.ResolveNumericSelection` nuốt số
  trần:
    P. **Products disambiguation** (erp.go:336, MỚI 2026-06-01) — khi `products`
       trả >1 web-group, backend lưu `[#stockpick_web:<web1>, #stockpick_web:<web2>, …]`
       (đúng thứ tự danh sách Agent hiển thị). "1"/"2" → handler `#stockpick_web`
       (tasks.go) → `engine.BuildExactWebStockPicker` → đẩy picker dòng-vs-SKU EXACT-web
       (giống Branch-0). Đây là lý do cú gõ-số sau danh sách products KHÔNG còn lên
       Langflow — gỡ hẳn Agent khỏi vòng chọn.
    0. **Branch-0 exact-web** (erp.go:1793, sau khi khách chọn web-name từ danh
       sách products) — backend lưu `[#show_macha_options_by_web:<web>,
       #choose_flow_type:skucuthe:<web>]`. "1" → tổng tồn cả dòng (khớp web CHÍNH
       XÁC), "2" → hỏi màu/size. Khác Level-1: nút "dòng" trỏ thẳng sum theo web,
       không qua bước RankProductWebGroups (tránh LIKE prefix-collision).
    1. **Level-1** (erp.go:1900, dòng SP vs SKU cụ thể, nhánh LIKE) — backend lưu
       `[#choose_flow_type:dongsp:<kw>, #choose_flow_type:skucuthe:<kw>]`. "1"/"2"
       fall-through thẳng handler `#choose_flow_type:*` ở tasks.go:768/823.
    2. **Level-2** (tasks.go:804, danh sách `TEN_DONG_BO_WEB`) — worker lưu
       `[#show_macha_options_by_web:<web1>, …]`. "1"/"2"/"3" fall-through
       `#show_macha_options_by_web` → `sumInventoryByMaChaAndWebName`.
  Cả hai cấp dùng chung `engine.BuildSessionKey(channelID, zaloUserID, groupID)`
  nên handler và worker đọc/ghi cùng một key Redis. Chỉ 1 option khớp ở Level-2
  thì bỏ bước chọn, cộng tồn ngay.
- **`DEBUG_PUSH_FALLBACK_TO_ZALO=true`**: push payload trung gian
  (`exact_web`/`raw_like_groups`/`raw_like`/`slim`) của resource `products` tới
  admin Zalo để soi từng giai đoạn.

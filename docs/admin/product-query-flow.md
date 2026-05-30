# Luồng truy vấn sản phẩm khi khách hỏi bot

Tài liệu này mô tả end-to-end luồng xử lý khi khách hàng nhắn cho bot một câu hỏi
**giá / thông tin sản phẩm** trên Zalo OA — ví dụ điển hình: **"FF800 trắng, size
L giá bao nhiêu?"**. Tài liệu bám sát **code hiện tại** (block
`if req.Resource == "products"` ngay trong `ERPQuery` của `erp.go` + hybrid search
trong `engine/product_embeddings.go` + LLM fuzzy trong `erp_fuzzy.go`).

> 🗣️ **Hiểu nhanh (không kỹ thuật):** Khách gõ tên/mã sản phẩm (có thể sai chính
> tả, thiếu dấu, viết tắt thương hiệu). Bot phải đoán đúng sản phẩm rồi trả giá.
> Backend lần lượt thử: (1) dò **tên web** bằng SQL LIKE — nếu ra **nhiều dòng họ
> hàng** thì hỏi lại khách chọn; (2) nếu LIKE không ra gì thì dùng **tìm kiếm lai
> (hybrid) BM25 + vector trên Astra** để bắt biến thể chính tả; (3) bí quá thì hỏi
> **LLM** map mã. Tìm được rồi thì kéo dữ liệu từ **cache MySQL cục bộ** và tính
> khoảng giá.

> 📎 Tài liệu này là bản song song của [`order-query-flow.md`](./order-query-flow.md)
> và [`inventory-query-flow.md`](./inventory-query-flow.md). Dùng chung xương sống
> vận chuyển (webhook → worker → Langflow → ERP gateway) nhưng nhánh handler khác
> hẳn ở **hai điểm cốt lõi**:
> 1. **Products phục vụ HOÀN TOÀN từ cache cục bộ.** Nó xử lý ngay trong `ERPQuery`
>    (`erp.go:258`) rồi `return` — **không** đi qua `respondWithLiveDataV2`, **không**
>    bao giờ chạm Cloudify live. `source` trong response luôn là `astradb_cache*`.
> 2. **Tên hàm gây hiểu nhầm.** Mọi helper `...FromAstraDB`
>    (`searchProductWebGroupsFromAstraDB`, `getProductsByMaChaFromAstraDB`, …) thực
>    ra query **MySQL `cached_products`**. **Chỉ** `FuzzyMatchProductWithEmbedding`
>    mới gọi Astra Data API thật (collection `erp_product_bbi`, keyspace `cqa_bbi`).

Ba câu hỏi mẫu được trace đầy đủ:

1. **Mơ hồ / nhiều họ:** `"mũ bảo hiểm LS2"` → LIKE ra **>1 nhóm web** → backend trả
   `astradb_cache_web_groups` (danh sách lựa chọn) → agent hỏi khách chọn dòng nào.
2. **Pinpoint 1 biến thể:** `"FF800 trắng L"` → LIKE không ra → **hybrid** bắt đúng
   1 SKU (Specific) → trả **đúng 1 variant + giá** (`astradb_cache`, count=1).
3. **Cả họ sản phẩm:** `"storm 3"` → hybrid bắt được `ma_cha` nhưng **không** pinpoint
   1 SKU → trả **toàn bộ biến thể** để `price_range` phủ cả họ.

> Sơ đồ dùng ASCII monospace. Khi xem trong VitePress, đặt trong code block để giữ
> căn lề.

---

## A. Swim-lane tổng (full end-to-end)

```
 Customer    Zalo Cloud    Backend HTTP    Asynq Worker      Langflow        Backend ERP API        Astra / MySQL
 (Zalo App)   (OA)          (Gin)           (tasks.go)       (RAG flow)      (erp.go handler)       (cache + vector)
     │           │              │               │                │                  │                     │
  1. "FF800  ─►  │              │               │                │                  │                     │
     trắng L  2. POST           │               │                │                  │                     │
     bao nhiêu?" /webhooks/zalo►│               │                │                  │                     │
     │           │           3. ZaloWebhookHandler              │                  │                     │
     │           │              (handlers/webhooks.go:17)       │                  │                     │
     │           │◄── 200 OK    • ack 200 ngay                  │                  │                     │
     │           │              • Enqueue NewZaloWebhookTask ──►│                  │                     │
     │           │              │            4. HandleZaloWebhookTask (workers/tasks.go)                  │
     │           │              │               • resolve customer + permission (scope products)          │
     │           │              │               • SignPermissionToken (HMAC, kèm nhóm sản phẩm)           │
     │           │              │            5. langflowClient.RunFlowWithCustomer ──────────────────►   │
     │           │              │            6. ToolCallingAgent quyết định gọi tool                      │
     │           │              │               ERPGatewayCaller(resource="products", search=…) (mục B)   │
     │           │              │            7. POST {gateway}/erp/query                                  │
     │           │              │               Headers: X-Agent-Token, X-Permission-Token               │
     │           │              │               Body: {resource:"products", search:"FF800 trắng L"} ──►   │
     │           │              │                                            8. ERPQuery (erp.go:89)      │
     │           │              │                                     if Resource=="products" (erp.go:258)│
     │           │              │                                     │                                   │
     │           │              │                       B1. searchProductWebGroups ──── LIKE 2-pass ───►  │ MySQL cached_products
     │           │              │                          (erp.go:967)  ◄── 0 nhóm ──────────────────┤   │
     │           │              │                       B2. FuzzyMatchProductWithEmbedding ───────────►  │ ◄── ĐIỂM DUY NHẤT
     │           │              │                          (product_embeddings.go:204)                   │     chạm Astra:
     │           │              │                          findAndRerank $hybrid="FF800 trắng L" ─────►  │ Astra erp_product_bbi
     │           │              │                          ◄── top SKU (BM25+vector+rerank) ───────────┤   │ (server-side vectorize)
     │           │              │                       Specific? → matchedMA  (pinpoint 1 variant)      │
     │           │              │                       B-fetch: getProductByMaFromAstraDB ───────────►  │ MySQL cached_products
     │           │              │                          (erp.go:2849)   ◄── 1 row ──────────────────┤   │
     │           │              │                       filterByGroups → enrichPriceRanges → slim(≤5)    │
     │           │              │           9. Agent đọc data[] (name + price_range) ◄──────────────────│
     │           │              │               format text reply                       │                │
     │           │              │           10. save assistant msg + ZaloOAAdapter.SendMessage           │
     │      12.  │◄─── deliver ─┤◄── 11. reply text ───────────────────────────────────│                │
   "FF800 Trắng Bóng size L giá 1.290.000đ ạ."
```

> 📌 **Điểm chạm Astra** trong luồng `products` là bước B2 (embedding fuzzy). Mọi
> bước còn lại (B1 LIKE, B-fetch, enrich) đều đọc **MySQL `cached_products`**. Nếu
> `ERP_EMBEDDING_FUZZY_ENABLED` không bật, B2 bị bỏ qua và luồng nhảy thẳng xuống
> B3 (LLM fuzzy). Nhánh **`product_variants`** cũng dùng chính engine hybrid này ở
> bước (0) — xem mục G; cùng gate `ERP_EMBEDDING_FUZZY_ENABLED`.

---

## B. Bộ quy tắc agent cho `resource="products"` (nguồn: `docs/admin/system prompt.txt`)

Tool `ERPGatewayCaller` nhận `resource ∈ {inventory, products, product_variants,
orders, customers, debt}`. Với sản phẩm có **hai** resource liên quan:

| Kịch bản | Ý định khách | Tool call |
|---|---|---|
| **P1** | "mũ bảo hiểm LS2 / có nón gì" (mơ hồ, nhiều dòng) | `products(search="mũ bảo hiểm LS2")` → backend dò web-group, **>1** nhóm → trả `web_groups` để agent hỏi khách chọn |
| **P2** | Khách đã chọn 1 tên web cụ thể từ danh sách trước | `products(search="<tên web>", exact_web_name=true)` → backend khớp đúng web name, **không** đẩy lại danh sách |
| **P3** | "FF800 trắng L giá bao nhiêu" / "storm 3 bao nhiêu" | `products(search="FF800 trắng L")` → backend fuzzy (hybrid → LLM) → 1 SKU **hoặc** cả họ + `price_range` |
| **P4** | "FF901 **đen bóng size L** giá bao nhiêu" (đã biết mã cha, cần đúng 1 biến thể) | `product_variants(parent_code="FF901", color="đen bóng", size="L")` → trả đúng SKU + giá (mục G) |

> 🧠 **Agent KHÔNG cần tự match mã.** Chỉ cần phân biệt: đã biết mã cha + thuộc
> tính cụ thể → `product_variants` (P4); còn lại → `products` (P1/P3). Mọi việc dò
> tên / sửa chính tả / chọn biến thể do backend lo.

> 🔢 **LUẬT CỨNG:** Khi backend trả `astradb_cache_web_groups` (P1), agent **PHẢI**
> liệt kê các `web_name` cho khách chọn, **KHÔNG** tự đoán 1 dòng rồi trả giá. Khi
> khách đã chọn, gọi lại với `exact_web_name=true` (P2) để tránh lặp danh sách trên
> các tên trùng tiền tố (vd "FF901" vs "FF901 Carbon").

---

## C. Sơ đồ quyết định bên trong `case "products"` (erp.go:258)

```
        ERPQuery → if req.Resource == "products" (erp.go:258)   [cache-only, return sớm]
                                 │
                                 ▼
                    exact_web_name == true ?  (erp.go:263)
                                 │
                   ┌── CÓ ───────┴───────── KHÔNG ──┐
                   ▼                                ▼
   searchProductsByExactWebName      strings.TrimSpace(search) == "" ? ◄── check khách có hỏi gì không
     FromAstraDB (erp.go:930)                       │
   source=astradb_cache_exact_web    ┌── RỖNG ──────┴────── CÓ CHỮ ──┐
                                     ▼                               ▼
                        searchProductsFromAstraDB        B1. searchProductWebGroupsFromAstraDB
                        (erp.go:838) — list ≤ limit          (erp.go:967) LIKE 2-pass + rank
                                                                          │
                                          ┌───────────────┬──────────────┴──────────────┐
                                     >1 nhóm web      ==1 nhóm (parent_codes)        0 nhóm
                                          │                  │                          │
                                          ▼                  ▼                          ▼
                              source=astradb_cache_  getProductsByMaChaFrom    ── FUZZY (B2 → B3) ──
                                web_groups (return)   AstraDB(parent) trực tiếp          │
                              agent hỏi khách chọn    (KHÔNG fuzzy/LLM)                   │
                                                                          ┌──────────────┴──────────────┐
                                                                  B2. embedding fuzzy          (nếu B2 trống/tắt)
                                                                  FuzzyMatchProductWith        B3. fuzzyMatchMaCha
                                                                  Embedding (hybrid Astra)     WithLLM (erp_fuzzy.go:94)
                                                                          │                            │
                                                                  Specific? ── CÓ ─► matchedMA         │
                                                                          │                            ▼
                                                                          └── KHÔNG ─► matchedMaCha ◄── trả ma_cha
                                                                                       │
                                                  ┌────────────────────────────────────┴────────┐
                                          matchedMA != ""                              matchedMaCha != ""
                                                  ▼                                            ▼
                                   getProductByMaFromAstraDB              getProductsByMaChaFromAstraDB
                                     (erp.go:2849) đúng 1 SKU                (erp.go:2861) cả họ, LIMIT 100
                                                  └────────────────────┬───────────────────────┘
                                                                       ▼
                                            filterProductsByGroups (erp.go:1593)  — lọc quyền nhóm
                                            enrichProductsWithPriceRanges (erp.go:1142) — price_range
                                            slimProductsForLLM (erp.go:1067) — gộp theo tên, ≤ 5 dòng
                                                                       ▼
                                            JSON: { source:"astradb_cache", data[], count }
```

> Câu mẫu **"FF800 trắng L"** đi nhánh: `exact_web_name=false` → có chữ → B1 LIKE
> trả **0 nhóm** (chuỗi "FF800 trắng L" hiếm khi khớp nguyên văn cột tên) → **B2
> hybrid** bắt đúng SKU "FF800 — Trắng Bóng — L", `Specific=true` → `matchedMA` →
> `getProductByMaFromAstraDB` → **count=1**.

---

## D. Hybrid search trên Astra (B2 — `product_embeddings.go`)

`FuzzyMatchProductWithEmbedding` (`product_embeddings.go:204`) chạy **một** query
hybrid lên collection vector của tenant rồi đọc tín hiệu để quyết định.

### Document mỗi SKU (`productEmbeddingDoc`, `:267`)

Mỗi biến thể (MA) là **một** document, đồng bộ qua
`SyncProductEmbeddingsToAstraDB` (`:106`). Các field then chốt:

| Field | Vai trò |
|---|---|
| `$vectorize` | Text label được Astra **tự embed server-side** (collection gắn sẵn service OpenAI — **phía Go KHÔNG giữ API key**). Nội dung = `buildProductEmbeddingLabel`: `"FF800 — Gloss White — L"` (web name — ten — màu — size). |
| `$lexical` | Text cho BM25 (`buildProductLexicalText`, `:341`): **mã đứng trước** (`ma`, `ma_cha`) rồi tới label, để token chính xác như `FF800` / `trắng` / `L` tra được. BM25 **chỉ** index document có `$lexical`. |
| `$vector` | Cosine bounded `[0,1]` — dùng làm **sàn liên quan tuyệt đối**. |
| `$rerank` | Logit rerank **không bị chặn** — **chỉ** dùng để xếp thứ tự, **không bao giờ** làm ngưỡng. |
| `$bm25Rank` | `null` khi không có token khớp; khác `null` = có overlap token chính xác (`HasBM25`). |
| `label_hash` | Gập `embeddingSchemaVersion` (hiện `"v2-lexical"`, `:41`) + label + lexical → khi đổi shape doc, một lần sync sẽ backfill lại toàn bộ. |

### Truy vấn (`astraHybridFindAndRerank`, `:513`)

```
POST {AstraEndpoint}/api/json/v1/cqa_bbi/erp_product_bbi
{
  "findAndRerank": {
    "filter":     { "tenant_id": "<tenant>" },
    "sort":       { "$hybrid": "FF800 trắng L" },     ← Astra tự embed + BM25 leg
    "projection": { "ma": 1, "ma_cha": 1 },
    "options":    { "limit": 5, "hybridLimits": 30, "includeScores": true }
  }
}
```

Astra chạy **2 chân** (lexical BM25 trên `$lexical` + vector ANN trên `$vector`),
mỗi chân kéo `hybridLimits=30` ứng viên, rerank, trả về `limit=5` đã xếp hạng kèm
`scores` (`$rerank`, `$vector`, `$bm25Rank`) song song theo index.

### Hai cổng quyết định (đều scale-independent)

1. **`passesRelevanceFloor`** (`:239`) — nhận top nếu **một trong hai**:
   - `HasBM25 == true` (có token chính xác, vd "FF800" — tín hiệu mạnh nhất,
     không phụ thuộc ngôn ngữ); **hoặc**
   - `Vector >= vectorFloor` (= **0.55**, `:50`) cho truy vấn thuần ngữ nghĩa.
   - Không đạt → trả zero-match (không lỗi) để rơi xuống B3 (LLM).
2. **`isSpecificSKUMatch`** (`:250`) — phân biệt **pinpoint 1 SKU** vs **cả họ**:
   - Tìm SKU đầu tiên **cùng `ma_cha`** với top; nếu `top.Vector − r.Vector >
     siblingCosineGap` (= **0.05**, `:59`) → **Specific** (top vượt trội hẳn anh em
     → khách hỏi đúng 1 biến thể, vd "FF800 **trắng L**").
   - Không có sibling nào trong top-K → cũng coi là Specific (top đơn nhất).
   - Ngược lại → **không** Specific (vd "storm 3" — cả họ ngang nhau) → chỉ trả
     `ma_cha`, caller kéo toàn bộ biến thể để `price_range` phủ cả họ.

> 🔎 Gap đo trên **cosine `$vector`** (đại lượng `[0,1]` ổn định), **không** đo trên
> logit `$rerank` (không bị chặn) — nên ngưỡng tổng quát hoá được cho mọi truy vấn.

---

## E. LLM fuzzy fallback (B3 — `erp_fuzzy.go:94`)

Chỉ chạy khi **B2 không ra** (`matchedMA == "" && matchedMaCha == ""`) — do
embedding tắt, Astra lỗi, hoặc dưới sàn liên quan.

1. **Nạp ứng viên** (`:101`): `SELECT ma_cha, MAX(ten_dong_bo_web), MAX(ten) …
   GROUP BY ma_cha` từ `cached_products`. Cap **1500** dòng cho prompt (`:111`).
2. **Prompt** (`:141`): mỗi dòng `ma_cha | tên`. Quy tắc khớp:
   - Khớp cả **mã** lẫn **tên** (web/ERP).
   - Bỏ gạch ngang / dấu chấm / số 0 đầu khi so mã (`E05 = E-5 = E.5`).
   - Số La Mã ↔ Ả Rập (`Storm III = Storm 3`, `MK II = MK 2`).
   - Bỏ dấu tiếng Việt (`mu bao hiem = mũ bảo hiểm`).
   - Viết tắt thương hiệu chấp nhận (`LS2 FF818 ≈ FF818`).
   - Trả **chỉ** 1 `ma_cha`, hoặc `NONE`.
3. **Gọi LLM** (`getAIClient`, `:20`): provider theo setting tenant (mặc định
   `claude` / model `claude-haiku-4-5`), timeout 10s.
4. **Validate** (`:173`): kết quả phải tồn tại trong danh sách ứng viên mới nhận;
   ngược lại trả `""`.

> 📌 B3 **chỉ** ra `ma_cha` (không pinpoint MA) → luôn kéo **cả họ** qua
> `getProductsByMaChaFromAstraDB`. Pinpoint 1 SKU **chỉ** đến từ B2 (Specific).

---

## F. Hậu xử lý & hình dạng dữ liệu (erp.go:442-465)

Sau khi có `cachedData` (1 SKU hoặc cả họ), ba bước chung:

1. **`filterProductsByGroups`** (`:1593`) — lọc theo `productGroups` mà permission
   token cho phép (ẩn nhóm sản phẩm ngoài quyền).
2. **`enrichProductsWithPriceRanges`** (`:1142`) — với mỗi sản phẩm có `ma_cha`,
   nạp lại toàn bộ biến thể (`getProductsByMaChaFromAstraDB`),
   `calculateProductPriceRange` (`:1194`) lấy min/max, `formatProductPriceRange`
   (`:1218`) ra nhãn `"x đ – y đ"`. Thêm field `price_min`, `price_max`,
   `price_range`.
3. **`slimProductsForLLM`** (`:1067`) — gộp các biến thể cùng `ten_dong_bo_web` về
   **một** dòng, cắt còn **tối đa 5** (const `slimProductsForLLMLimit`, `:1065`),
   chỉ giữ field LLM cần.

### Ví dụ response — P1 (nhiều nhóm web, `astradb_cache_web_groups`, erp.go:339)

```json
{
  "status": "success",
  "source": "astradb_cache_web_groups",
  "resource": "products",
  "count": 2,
  "data": [
    { "web_name": "Mũ LS2 FF800 Storm II", "parent_codes": ["FF800"], "count": 12, "is_fallback": false },
    { "web_name": "Mũ LS2 FF800 Carbon",   "parent_codes": ["FF800C"], "count": 6, "is_fallback": false }
  ]
}
```

### Ví dụ response — P3 pinpoint 1 SKU (`astradb_cache`, count=1, erp.go:458)

```json
{
  "status": "success",
  "source": "astradb_cache",
  "resource": "products",
  "count": 1,
  "data": [
    {
      "name": "FF800 — Trắng Bóng — L",
      "price_range": "1.290.000đ",
      "nhan_hieu_name": "LS2",
      "list_ten_nhom_vthh": "Mũ bảo hiểm",
      "dvt": "cái"
    }
  ]
}
```

### Ví dụ response — P3 cả họ ("storm 3" → `price_range` phủ cả họ)

```json
{
  "status": "success",
  "source": "astradb_cache",
  "resource": "products",
  "count": 1,
  "data": [
    {
      "name": "Mũ LS2 Storm 3",
      "price_range": "990.000đ - 1.450.000đ",
      "nhan_hieu_name": "LS2",
      "list_ten_nhom_vthh": "Mũ bảo hiểm",
      "dvt": "cái"
    }
  ]
}
```

### Ví dụ câu trả lời bot

Pinpoint (P3, "FF800 trắng L"):

```
Dạ FF800 Trắng Bóng size L giá 1.290.000đ ạ.
```

Cả họ (P3, "storm 3"):

```
Mũ LS2 Storm 3 có giá từ 990.000đ đến 1.450.000đ tuỳ màu/size ạ.
```

Nhiều nhóm (P1, "mũ LS2 FF800"):

```
Mình có 2 dòng FF800 ạ: (1) FF800 Storm II, (2) FF800 Carbon.
Anh/chị muốn xem dòng nào ạ?
```

---

## G. Resource chị em `product_variants` (erp.go:472-583)

Khi khách đã rõ **mã cha + thuộc tính cụ thể** ("FF901 đen bóng size L giá bao
nhiêu?"), agent gọi `product_variants` thay vì `products` để lấy **đúng 1 SKU +
giá** (không phải khoảng giá):

1. **Bắt buộc `parent_code`** (`:474`); thiếu → HTTP 400.
2. **`searchVariantsByAttributes`** (`:483`) — lọc cache MySQL theo
   `parent_code` + `color` + `size` + `brand`. Lưu ý: `size` so khớp **bằng
   chính xác** (`LOWER(thuoc_tinh_2) = LOWER(size)`) còn `color` là substring
   `LIKE`. Đường này nhanh + chính xác khi kho lưu chuẩn, nhưng trượt khi
   `thuoc_tinh_2` lưu `"Size L"` / `"L (40)"` / có space thừa, hoặc khi khách
   gõ màu khác ngôn ngữ với giá trị lưu.
3. **Zero-result → (0) Astra hybrid** (`:522`, `searchVariantsByAttributes`
   trả rỗng): chạy **đúng engine của luồng products** —
   `hybridMatchVariant` (`erp_variants.go`) gọi `FuzzyMatchProductWithEmbedding`
   (BM25 `$lexical` + vector, mục D) với `keyword = "<parent> <color> <size>
   <brand>"`, gate bởi `ERP_EMBEDDING_FUZZY_ENABLED`. Index nhúng mỗi SKU dạng
   `"FF901 — Gloss White — L"` nên bắt được cả lỗi lưu size lẫn màu song ngữ.
   Có **guard `ma_cha == parent_code`** (vì hybrid filter chỉ scope `tenant_id`)
   để không rò SKU của dòng cha khác. Pinpoint được → `source =
   "astradb_hybrid_variants"`, bỏ qua bước 4–5.
4. **Vẫn rỗng → fallback song ngữ** (`:540`): nếu cache lưu "Gloss Black" mà
   khách gõ "đen bóng", `fuzzyMatchAttributesWithLLM` (`erp_fuzzy.go:192`) map
   color/size/brand về giá trị chuẩn (`collectAvailableAttributes` cung cấp danh
   sách hợp lệ) rồi **thử lại đúng 1 lần**.
5. **Vẫn rỗng** → trả `available_colors / available_sizes / available_brands` để
   agent hỏi khách chọn tổ hợp có thật.

> 🔌 **Mục 3 là điểm mới đấu Astra vào variant.** Trước đây nhánh
> `product_variants` chỉ dùng MySQL exact + LLM remap, **không bao giờ** chạm
> hybrid embedding — nên mọi cải tiến `$lexical`/vector chỉ phục vụ luồng
> `products`. Giờ variant tái dùng cùng `FuzzyMatchProductWithEmbedding`.

> `product_variants` dùng chung permission grant với `products` (`erp.go:221`:
> `permResource` map `product_variants → products`), nên tenant không cần cấu hình
> resource thứ hai.

---

## H. Bảng tham chiếu file:line

| Bước | File:Line | Hàm / nhánh |
|---|---|---|
| Webhook nhận tin | `backend/api/handlers/webhooks.go:17` | `ZaloWebhookHandler` |
| Worker xử lý | `backend/workers/tasks.go` | `HandleZaloWebhookTask` (resolve KH + quyền, ký token) |
| Gọi Langflow | `backend/engine/langflow_client.go` | `RunFlowWithCustomer` |
| Handler ERP | `backend/api/handlers/erp.go:89` | `ERPQuery` (auth, ERP active, verify token, method check) |
| **Nhánh products** | `backend/api/handlers/erp.go:258` | `if req.Resource == "products"` (cache-only, `return` sớm) |
| Exact web-name | `backend/api/handlers/erp.go:263` → `:930` | `searchProductsByExactWebNameFromAstraDB` (MySQL) |
| Liệt kê (search rỗng) | `backend/api/handlers/erp.go:417` → `:838` | `searchProductsFromAstraDB` |
| **B1 web-group LIKE** | `backend/api/handlers/erp.go:313` → `:967` | `searchProductWebGroupsFromAstraDB` (MySQL 2-pass: `ten_dong_bo_web`→`ten`) |
| Rank web-group | `backend/engine/product_grouping.go:25` | `RankProductWebGroups` / `WebGroupMatch` (`:14`) |
| Response >1 nhóm | `backend/api/handlers/erp.go:339` | `source=astradb_cache_web_groups` (disambiguation) |
| **B2 embedding fuzzy** | `backend/api/handlers/erp.go:380` → `engine/product_embeddings.go:204` | `FuzzyMatchProductWithEmbedding` (gated bởi `ERP_EMBEDDING_FUZZY_ENABLED`) |
| Astra hybrid call | `backend/engine/product_embeddings.go:513` | `astraHybridFindAndRerank` (`findAndRerank`, `sort.$hybrid`) |
| Build `$lexical` | `backend/engine/product_embeddings.go:341` | `buildProductLexicalText` (mã trước, label sau) |
| Build `$vectorize` | `backend/engine/product_embeddings.go:359` | `buildProductEmbeddingLabel` |
| Sàn liên quan | `backend/engine/product_embeddings.go:239` | `passesRelevanceFloor` (`HasBM25 \|\| Vector>=0.55`) |
| Pinpoint SKU | `backend/engine/product_embeddings.go:250` | `isSpecificSKUMatch` (gap cosine > 0.05) |
| Sync embeddings | `backend/engine/product_embeddings.go:106` | `SyncProductEmbeddingsToAstraDB` (diff `label_hash`, schema `v2-lexical`) |
| **B3 LLM fuzzy** | `backend/api/handlers/erp.go:391` → `erp_fuzzy.go:94` | `fuzzyMatchMaChaWithLLM` (chỉ khi B2 trống) |
| AI client | `backend/api/handlers/erp_fuzzy.go:20` | `getAIClient` (mặc định `claude-haiku-4-5`) |
| Fetch 1 SKU | `backend/api/handlers/erp.go:401` → `:2849` | `getProductByMaFromAstraDB` (MySQL `ma=`) |
| Fetch cả họ | `backend/api/handlers/erp.go:408` → `:2861` | `getProductsByMaChaFromAstraDB` (MySQL `ma_cha=`, LIMIT 100) |
| Lọc quyền nhóm | `backend/api/handlers/erp.go:442` → `:1593` | `filterProductsByGroups` |
| Enrich khoảng giá | `backend/api/handlers/erp.go:443` → `:1142` | `enrichProductsWithPriceRanges` |
| Tính / format giá | `backend/api/handlers/erp.go:1194`, `:1218` | `calculateProductPriceRange` / `formatProductPriceRange` |
| Slim cho LLM | `backend/api/handlers/erp.go:447` → `:1067` | `slimProductsForLLM` (gộp theo tên, max 5 — const `:1065`) |
| Response JSON | `backend/api/handlers/erp.go:458` | `source:"astradb_cache"` + `data[]` + `count` |
| Ghi audit | `backend/api/handlers/erp.go:457` | `writeAuditLog` |
| **product_variants** | `backend/api/handlers/erp.go:472` | nhánh variant theo `parent_code` + màu/size/brand |
| Attr search | `backend/api/handlers/erp.go:483` | `searchVariantsByAttributes` (MySQL, size exact) |
| **Variant Astra hybrid** | `backend/api/handlers/erp.go:522` → `erp_variants.go` | `hybridMatchVariant` (reuse `FuzzyMatchProductWithEmbedding`, guard `ma_cha=parent`, gate `ERP_EMBEDDING_FUZZY_ENABLED`) |
| Attr fuzzy song ngữ | `backend/api/handlers/erp.go:540` → `erp_fuzzy.go:192` | `fuzzyMatchAttributesWithLLM` ("đen bóng"→"Gloss Black") |
| Quyền dùng chung | `backend/api/handlers/erp.go:221` | `product_variants` → `products` (permResource map) |

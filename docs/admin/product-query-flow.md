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
>    (`erp.go:274`) rồi `return` — **không** đi qua `respondWithLiveDataV2`, **không**
>    bao giờ chạm Cloudify live. `source` trong response luôn là `astradb_cache*`.
> 2. **Tên hàm phản ánh đúng nguồn dữ liệu.** Các helper đọc cache giờ mang hậu tố
>    `...FromCache` (`searchProductWebGroupsFromCache`, `getProductsByMaChaFromCache`,
>    `searchProductsFromCache`, …) → query **MySQL `cached_products`**. Hậu tố
>    `...FromAstraDB` chỉ còn ở các hàm **thật sự** gọi Astra Data API:
>    `getProductBySkuFromAstraDB`, `fetchUniqueGroupsFromAstraDB`,
>    `SyncProductEmbeddingsToAstraDB`. Ngoài ra **chỉ** `FuzzyMatchProductWithEmbedding`
>    chạm Astra hybrid (collection `erp_product_bbi`, keyspace `cqa_bbi`).

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
     │           │              │                                            8. ERPQuery (erp.go:103)     │
     │           │              │                                     if Resource=="products" (erp.go:274)│
     │           │              │                                     │                                   │
     │           │              │                       B1. searchProductWebGroupsFromCache ─ LIKE 2-pass►  │ MySQL cached_products
     │           │              │                          (erp.go:1041) ◄── 0 nhóm ──────────────────┤   │
     │           │              │                       B2. FuzzyMatchProductWithEmbedding ───────────►  │ ◄── ĐIỂM DUY NHẤT
     │           │              │                          (product_embeddings.go:207)                   │     chạm Astra:
     │           │              │                          findAndRerank $hybrid="FF800 trắng L" ─────►  │ Astra erp_product_bbi
     │           │              │                          ◄── top SKU (BM25+vector+rerank) ───────────┤   │ (server-side vectorize)
     │           │              │                       Specific? → matchedMA  (pinpoint 1 variant)      │
     │           │              │                       B-fetch: getProductByMaFromCache ─────────────►  │ MySQL cached_products
     │           │              │                          (erp.go:2923)   ◄── 1 row ──────────────────┤   │
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

## C. Sơ đồ quyết định bên trong `case "products"` (erp.go:274)

```
        ERPQuery → if req.Resource == "products" (erp.go:274)   [cache-only, return sớm]
                                 │
                                 ▼
                    exact_web_name == true ?  (erp.go:279)
                                 │
                   ┌── CÓ ───────┴───────── KHÔNG ──┐
                   ▼                                ▼
   searchProductsByExactWebName      strings.TrimSpace(search) == "" ? ◄── check khách có hỏi gì không
     FromCache (erp.go:1004)                        │
   source=astradb_cache_exact_web    ┌── RỖNG ──────┴────── CÓ CHỮ ──┐
                                     ▼                               ▼
                   return data:[] + source=          B1. searchProductWebGroupsFromCache
                   empty_search_use_knowledge            (erp.go:1041) LIKE 2-pass + rank
                   (erp.go:434, return) → agent
                   chuyển sang Astra Retrieval (KB)
                                                                          │
                                          ┌───────────────┬──────────────┴──────────────┐
                                     >1 nhóm web      ==1 nhóm (parent_codes)        0 nhóm
                                          │                  │                          │
                                          ▼                  ▼                          ▼
                              source=astradb_cache_  getProductsByMaChaFrom    ── FUZZY (B2 → B3) ──
                                web_groups (return)   Cache(parent) trực tiếp            │
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
                                   getProductByMaFromCache                getProductsByMaChaFromCache
                                     (erp.go:2923) đúng 1 SKU                (erp.go:2935) cả họ, LIMIT 100
                                                  └────────────────────┬───────────────────────┘
                                                                       ▼
                                            filterProductsByGroups (erp.go:1667)  — lọc quyền nhóm
                                            enrichProductsWithPriceRanges (erp.go:1216) — price_range
                                            slimProductsForLLM (erp.go:1141) — gộp theo tên, ≤ 5 dòng
                                                                       ▼
                                            JSON: { source:"astradb_cache", data[], count }
```

> Câu mẫu **"FF800 trắng L"** đi nhánh: `exact_web_name=false` → có chữ → B1 LIKE
> trả **0 nhóm** (chuỗi "FF800 trắng L" hiếm khi khớp nguyên văn cột tên) → **B2
> hybrid** bắt đúng SKU "FF800 — Trắng Bóng — L", `Specific=true` → `matchedMA` →
> `getProductByMaFromCache` → **count=1**.

---

## D. Hybrid search trên Astra (B2 — `product_embeddings.go`)

`FuzzyMatchProductWithEmbedding` (`product_embeddings.go:207`) chạy **một** query
hybrid lên collection vector của tenant rồi đọc tín hiệu để quyết định.

### Document mỗi SKU (`productEmbeddingDoc`, `:268`)

Mỗi biến thể (MA) là **một** document, đồng bộ qua
`SyncProductEmbeddingsToAstraDB` (`:108`). Các field then chốt:

| Field | Vai trò |
|---|---|
| `$vectorize` | Text label được Astra **tự embed server-side** (collection gắn sẵn service OpenAI — **phía Go KHÔNG giữ API key**). Nội dung = `buildProductEmbeddingLabel`: `"FF800 — Gloss White — L"` (web name — ten — màu — size). |
| `$lexical` | Text cho BM25 (`buildProductLexicalText`, `:343`): **mã đứng trước** (`ma`, `ma_cha`) rồi tới label, để token chính xác như `FF800` / `trắng` / `L` tra được. BM25 **chỉ** index document có `$lexical`. |
| `$rerank` | Logit của reranker nvidia cross-encoder, **không bị chặn**. Đây là **cổng relevance**: top score > `rerankFloor` (≈0) = match thật; âm = không khớp → rơi xuống B3. |
| `$vector` | Cosine bounded `[0,1]`. **KHÔNG** dùng làm sàn relevance (query lạc đề vẫn hay đạt 0.6+ cosine), **chỉ** dùng đo gap anh em cùng `ma_cha` để phân biệt pinpoint vs cả họ. |
| `$bm25Rank` | `null` khi không có token khớp; khác `null` = có overlap token chính xác (`HasBM25`). Chỉ để log/quan sát, **không** còn là cổng nhận. |
| `label_hash` | Gập `embeddingSchemaVersion` (hiện `"v2-lexical"`, `:41`) + label + lexical → khi đổi shape doc, một lần sync sẽ backfill lại toàn bộ. |

### Truy vấn (`astraHybridFindAndRerank`, `:515`)

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

1. **`passesRelevanceFloor`** (`:240`) — nhận top **chỉ khi** `top.Rerank >
   rerankFloor` (= **0.0**, `:52`):
   - Reranker nvidia cross-encoder cho điểm match thật **dương rõ** (quan sát
     +9..+15) còn query lạc đề **âm** (−6..−13), bất kể độ dài / ngôn ngữ truy vấn.
   - **Không** dùng cosine `$vector` làm sàn: query lạc đề vẫn hay đạt 0.6+ cosine
     nên sàn cosine sẽ nhận nhầm nhiễu. `HasBM25` cũng **không** còn là cổng nhận.
   - Top score âm → trả zero-match (không lỗi) để rơi xuống B3 (LLM).
2. **`isSpecificSKUMatch`** (`:251`) — phân biệt **pinpoint 1 SKU** vs **cả họ**:
   - Tìm SKU đầu tiên **cùng `ma_cha`** với top; nếu `top.Vector − r.Vector >
     siblingCosineGap` (= **0.05**, `:61`) → **Specific** (top vượt trội hẳn anh em
     → khách hỏi đúng 1 biến thể, vd "FF800 **trắng L**").
   - Không có sibling nào trong top-K → cũng coi là Specific (top đơn nhất).
   - Ngược lại → **không** Specific (vd "storm 3" — cả họ ngang nhau) → chỉ trả
     `ma_cha`, caller kéo toàn bộ biến thể để `price_range` phủ cả họ.

> 🔎 **Hai tín hiệu, hai vai trò khác nhau:** cổng *relevance* (nhận/loại) đo trên
> **logit `$rerank`** (boundary tự nhiên ≈0 của cross-encoder); còn cổng *specific*
> (pinpoint vs cả họ) đo trên **cosine `$vector`** `[0,1]` ổn định. Cosine **không**
> dùng để chặn relevance, rerank **không** dùng để đo gap anh em.

---

## E. LLM fuzzy fallback (B3 — `erp_fuzzy.go:94`)

Chỉ chạy khi **B2 không ra** (`matchedMA == "" && matchedMaCha == ""`) — do
embedding tắt, Astra lỗi, hoặc dưới sàn liên quan.

1. **Nạp ứng viên** (`:105`): `SELECT ma_cha, MAX(ten_dong_bo_web), MAX(ten) …
   GROUP BY ma_cha` từ `cached_products`. Cap **1500** dòng cho prompt
   (`fuzzyCandidateCap`, `:111`).
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
> `getProductsByMaChaFromCache`. Pinpoint 1 SKU **chỉ** đến từ B2 (Specific).

---

## F. Hậu xử lý & hình dạng dữ liệu (erp.go:460-484)

Sau khi có `cachedData` (1 SKU hoặc cả họ), ba bước chung:

1. **`filterProductsByGroups`** (`:1667`) — lọc theo `productGroups` mà permission
   token cho phép (ẩn nhóm sản phẩm ngoài quyền).
2. **`enrichProductsWithPriceRanges`** (`:1216`) — với mỗi sản phẩm có `ma_cha`,
   nạp lại toàn bộ biến thể (`getProductsByMaChaFromCache`),
   `calculateProductPriceRange` (`:1268`) lấy min/max, `formatProductPriceRange`
   (`:1292`) ra nhãn `"x đ – y đ"`. Thêm field `price_min`, `price_max`,
   `price_range`.
3. **`slimProductsForLLM`** (`:1141`) — gộp các biến thể cùng `ten_dong_bo_web` về
   **một** dòng, cắt còn **tối đa 5** (const `slimProductsForLLMLimit`, `:1139`),
   chỉ giữ field LLM cần.

### Ví dụ response — P1 (nhiều nhóm web, `astradb_cache_web_groups`, erp.go:358)

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

### Ví dụ response — P3 pinpoint 1 SKU (`astradb_cache`, count=1, erp.go:479)

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

## G. Resource chị em `product_variants` (erp.go:490-660)

Khi khách đã rõ **mã cha + thuộc tính cụ thể** ("FF901 đen bóng size L giá bao
nhiêu?"), agent gọi `product_variants` thay vì `products` để lấy **đúng 1 SKU +
giá** (không phải khoảng giá):

1. **Bắt buộc `parent_code`** (`:493`); thiếu → HTTP 400 (`missing_parent_code`).
2. **`searchVariantsByAttributes`** (`:501` → `erp_variants.go:25`) — lọc cache
   MySQL theo `parent_code` + `color` + `size` + `brand`. Lưu ý: `size` so khớp
   **bằng chính xác** (`LOWER(thuoc_tinh_2) = LOWER(size)`) còn `color` là substring
   `LIKE`. Đường này nhanh + chính xác khi kho lưu chuẩn, nhưng trượt khi
   `thuoc_tinh_2` lưu `"Size L"` / `"L (40)"` / có space thừa, hoặc khi khách
   gõ màu khác ngôn ngữ với giá trị lưu.
3. **Zero-result → (0) Astra hybrid** (`:562`, khi `searchVariantsByAttributes`
   trả rỗng): chạy **đúng engine của luồng products** —
   `hybridMatchVariant` (`erp_variants.go:153`) gọi `FuzzyMatchProductWithEmbedding`
   (BM25 `$lexical` + vector, mục D) với keyword ghép từ `parent_code` + `color`
   + `size` + `brand`, gate bởi `ERP_EMBEDDING_FUZZY_ENABLED`. Index nhúng mỗi
   SKU dạng `"FF901 — Gloss White — L"` nên bắt được cả lỗi lưu size lẫn màu song ngữ.
   **Guard nhận diện qua label** (`variantBelongsToParent`, `erp_variants.go:217`)
   thay cho giả định cũ `ma_cha == parent_code`: vì agent gửi **tên mã cha**
   ("FF901") chứ không phải `ma_cha` nội bộ, filter hybrid chỉ scope `tenant_id`
   nên phải xác nhận **label của SKU có chứa mã cha** (qua `resolveParentMaCha` +
   `parentCodeInLabel`, `:93`) mới nhận — tránh rò SKU dòng cha khác. Pinpoint được
   → `source = "astradb_hybrid_variants"`, bỏ qua bước 4–5.
4. **Vẫn rỗng → fallback song ngữ** (`:589`): nếu cache lưu "Gloss Black" mà
   khách gõ "đen bóng", `fuzzyMatchAttributesWithLLM` (`erp_fuzzy.go:192`) map
   color/size/brand về giá trị chuẩn (`collectAvailableAttributes` cung cấp danh
   sách hợp lệ) rồi **thử lại đúng 1 lần**.
5. **Vẫn rỗng** → trả `available_colors / available_sizes / available_brands` để
   agent hỏi khách chọn tổ hợp có thật.

> 🔌 **Mục 3 là điểm đấu Astra vào variant.** Trước đây nhánh `product_variants`
> chỉ dùng MySQL exact + LLM remap, **không bao giờ** chạm hybrid embedding — nên
> mọi cải tiến `$lexical`/vector chỉ phục vụ luồng `products`. Giờ variant tái dùng
> cùng `FuzzyMatchProductWithEmbedding`, và mã cha được khớp qua **label identity**
> (hybrid search trên label) chứ không còn so `ma_cha == parent_code` (vốn luôn
> trượt vì `parent_code` là tên model, không phải `ma_cha`).

> `product_variants` dùng chung permission grant với `products`:
> `methodPermissionResource` (`erp.go:87`, áp dụng ở `:208`) map `product_variants
> → products`, nên tenant không cần cấu hình resource thứ hai.

---

## H. Bảng tham chiếu file:line

| Bước | File:Line | Hàm / nhánh |
|---|---|---|
| Webhook nhận tin | `backend/api/handlers/webhooks.go:17` | `ZaloWebhookHandler` |
| Worker xử lý | `backend/workers/tasks.go` | `HandleZaloWebhookTask` (resolve KH + quyền, ký token) |
| Gọi Langflow | `backend/engine/langflow_client.go` | `RunFlowWithCustomer` |
| Handler ERP | `backend/api/handlers/erp.go:103` | `ERPQuery` (auth, ERP active, verify token, method check) |
| **Nhánh products** | `backend/api/handlers/erp.go:274` | `if req.Resource == "products"` (cache-only, `return` sớm) |
| Exact web-name | `backend/api/handlers/erp.go:281` → `:1004` | `searchProductsByExactWebNameFromCache` (MySQL) |
| Search rỗng → KB | `backend/api/handlers/erp.go:434` | trả `data:[]` + `source=empty_search_use_knowledge` (return; agent chuyển sang Astra Retrieval) |
| **B1 web-group LIKE** | `backend/api/handlers/erp.go:329` → `:1041` | `searchProductWebGroupsFromCache` (MySQL 2-pass: `ten_dong_bo_web`→`ten`) |
| Rank web-group | `backend/engine/product_grouping.go:25` | `RankProductWebGroups` / `WebGroupMatch` (`:14`) |
| Response >1 nhóm | `backend/api/handlers/erp.go:358` | `source=astradb_cache_web_groups` (disambiguation) |
| **B2 embedding fuzzy** | `backend/api/handlers/erp.go:399` → `engine/product_embeddings.go:207` | `FuzzyMatchProductWithEmbedding` (gated bởi `ERP_EMBEDDING_FUZZY_ENABLED`) |
| Astra hybrid call | `backend/engine/product_embeddings.go:515` | `astraHybridFindAndRerank` (`findAndRerank`, `sort.$hybrid`) |
| Build `$lexical` | `backend/engine/product_embeddings.go:343` | `buildProductLexicalText` (mã trước, label sau) |
| Build `$vectorize` | `backend/engine/product_embeddings.go:361` | `buildProductEmbeddingLabel` |
| Sàn liên quan | `backend/engine/product_embeddings.go:240` | `passesRelevanceFloor` (`Rerank > rerankFloor≈0`, `:52`) |
| Pinpoint SKU | `backend/engine/product_embeddings.go:251` | `isSpecificSKUMatch` (gap cosine > `siblingCosineGap` 0.05) |
| Sync embeddings | `backend/engine/product_embeddings.go:108` | `SyncProductEmbeddingsToAstraDB` (diff `label_hash`, schema `v2-lexical`) |
| **B3 LLM fuzzy** | `backend/api/handlers/erp.go:409` → `erp_fuzzy.go:94` | `fuzzyMatchMaChaWithLLM` (chỉ khi B2 trống) |
| AI client | `backend/api/handlers/erp_fuzzy.go:20` | `getAIClient` (mặc định `claude-haiku-4-5`, `:60`) |
| Fetch 1 SKU | `backend/api/handlers/erp.go:419` → `:2923` | `getProductByMaFromCache` (MySQL `ma=`) |
| Fetch cả họ | `backend/api/handlers/erp.go:426` → `:2935` | `getProductsByMaChaFromCache` (MySQL `ma_cha=`, LIMIT 100) |
| Lọc quyền nhóm | `backend/api/handlers/erp.go:460` → `:1667` | `filterProductsByGroups` |
| Enrich khoảng giá | `backend/api/handlers/erp.go:461` → `:1216` | `enrichProductsWithPriceRanges` |
| Tính / format giá | `backend/api/handlers/erp.go:1268`, `:1292` | `calculateProductPriceRange` / `formatProductPriceRange` |
| Slim cho LLM | `backend/api/handlers/erp.go:465` → `:1141` | `slimProductsForLLM` (gộp theo tên, max 5 — const `:1139`) |
| Response JSON | `backend/api/handlers/erp.go:479` | `source:"astradb_cache"` + `data[]` + `count` |
| Ghi audit | `backend/api/handlers/erp.go:475` | `writeAuditLog` |
| **product_variants** | `backend/api/handlers/erp.go:490` | nhánh variant theo `parent_code` + màu/size/brand |
| Attr search | `backend/api/handlers/erp.go:501` → `erp_variants.go:25` | `searchVariantsByAttributes` (MySQL, size exact) |
| **Variant Astra hybrid** | `backend/api/handlers/erp.go:562` → `erp_variants.go:153` | `hybridMatchVariant` (reuse `FuzzyMatchProductWithEmbedding`, guard label identity `variantBelongsToParent` `:217`, gate `ERP_EMBEDDING_FUZZY_ENABLED`) |
| Resolve parent qua label | `backend/api/handlers/erp_variants.go:93` | `resolveParentMaCha` / `parentCodeInLabel` (model name → `ma_cha`) |
| Attr fuzzy song ngữ | `backend/api/handlers/erp.go:589` → `erp_fuzzy.go:192` | `fuzzyMatchAttributesWithLLM` ("đen bóng"→"Gloss Black") |
| Quyền dùng chung | `backend/api/handlers/erp.go:87` → `:208` | `methodPermissionResource`: `product_variants` → `products` |

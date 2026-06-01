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
2. **Cả họ sản phẩm:** `"storm 3"` → LIKE không ra → **hybrid** bắt được `ma_cha` →
   trả **toàn bộ biến thể** để `price_range` phủ cả họ.
3. **Tên web cụ thể (sau disambiguation):** `"Mũ LS2 FF800 Storm II"` +
   `exact_web_name=true` → backend khớp đúng tên web → trả họ đó + `price_range`.

> ⚠️ **`products` KHÔNG pinpoint 1 SKU.** Nó luôn resolve ở **mức họ (`ma_cha`)** và
> trả `price_range` phủ cả họ. Câu có **màu/size cụ thể** (vd "FF800 **trắng L**") là
> intent **SPECIFIC-VARIANT** → agent route sang resource **`product_variants`** để
> lấy đúng 1 SKU + giá đơn (mục G). Việc pinpoint 1 biến thể **chỉ** sống ở
> `product_variants`, không còn trong `products`.

> Sơ đồ dùng ASCII monospace. Khi xem trong VitePress, đặt trong code block để giữ
> căn lề.

---

## A. Swim-lane tổng (full end-to-end)

```
 Customer    Zalo Cloud    Backend HTTP    Asynq Worker      Langflow        Backend ERP API        Astra / MySQL
 (Zalo App)   (OA)          (Gin)           (tasks.go)       (RAG flow)      (erp.go handler)       (cache + vector)
     │           │              │               │                │                  │                     │
  1. "storm  ─►  │              │               │                │                  │                     │
     3 bao    2. POST           │               │                │                  │                     │
     nhiêu?"     /webhooks/zalo►│               │                │                  │                     │
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
     │           │              │               Body: {resource:"products", search:"storm 3"} ────────►   │
     │           │              │                                            8. ERPQuery (erp.go:103)     │
     │           │              │                                     if Resource=="products" (erp.go:274)│
     │           │              │                                     │                                   │
     │           │              │                       B1. searchProductWebGroupsFromCache ─ LIKE 2-pass►  │ MySQL cached_products
     │           │              │                          (erp.go:1038) ◄── 0 nhóm ──────────────────┤   │
     │           │              │                       B2. FuzzyMatchProductWithEmbedding ───────────►  │ ◄── ĐIỂM DUY NHẤT
     │           │              │                          (product_embeddings.go:207)                   │     chạm Astra:
     │           │              │                          findAndRerank $hybrid="storm 3" ───────────►  │ Astra erp_product_bbi
     │           │              │                          ◄── top row (BM25+vector+rerank) ───────────┤   │ (server-side vectorize)
     │           │              │                       chỉ lấy match.MaCha (LUÔN mức họ, KHÔNG pinpoint)│
     │           │              │                       B-fetch: getProductsByMaChaFromCache ─────────►  │ MySQL cached_products
     │           │              │                          (erp.go:2932)   ◄── N rows (cả họ) ─────────┤   │
     │           │              │                       filterByGroups → enrichPriceRanges → slim(≤5)    │
     │           │              │           9. Agent đọc data[] (name + price_range) ◄──────────────────│
     │           │              │               format text reply                       │                │
     │           │              │           10. save assistant msg + ZaloOAAdapter.SendMessage           │
     │      12.  │◄─── deliver ─┤◄── 11. reply text ───────────────────────────────────│                │
   "Mũ LS2 Storm 3 giá từ 990.000đ đến 1.450.000đ tuỳ màu/size ạ."
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
| **P3** | "storm 3 bao nhiêu" / "FF901 giá bao nhiêu" (mã/tên, **không** màu/size) | `products(search="storm 3")` → backend fuzzy (hybrid → LLM) → **cả họ** + `price_range` |
| **P4** | "FF901 **đen bóng size L** giá bao nhiêu" / "FF800 **trắng L**" (mã cha + thuộc tính cụ thể) | `product_variants(parent_code="FF901", color="đen bóng", size="L")` → trả đúng SKU + giá đơn (mục G) |

> 🧠 **Agent KHÔNG cần tự match mã.** Chỉ cần phân biệt theo **có màu/size hay
> không**: có màu/size cụ thể → `product_variants` (P4, pinpoint 1 SKU); chỉ mã/tên
> → `products` (P1/P3, trả cả họ + khoảng giá). Mọi việc dò tên / sửa chính tả /
> chọn biến thể do backend lo.

> ⚠️ **`products` chỉ trả mức họ.** Từ bản gỡ pinpoint, `products` **không** còn trả
> "1 SKU" — luôn là **cả họ + `price_range`**. Muốn đúng 1 biến thể + giá đơn thì
> phải qua `product_variants` (P4). Nếu agent lỡ gửi "FF800 trắng L" vào `products`,
> kết quả là cả họ FF800 (khoảng giá), không phải 1 cái.

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
     FromCache (erp.go:1001)                        │
   source=astradb_cache_exact_web    ┌── RỖNG ──────┴────── CÓ CHỮ ──┐
                                     ▼                               ▼
                   return data:[] + source=          B1. searchProductWebGroupsFromCache
                   empty_search_use_knowledge            (erp.go:1038) LIKE 2-pass + rank
                   (erp.go:437, return) → agent
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
                                                                  → chỉ lấy match.MaCha                │
                                                                          │                            ▼
                                                                          └──► matchedMaCha ◄────── trả ma_cha
                                                                                       │
                                                                          (products LUÔN ở mức họ —
                                                                           KHÔNG đọc match.Specific/MA)
                                                                                       ▼
                                                                          matchedMaCha != "" ?
                                                                                       │ CÓ
                                                                                       ▼
                                                          getProductsByMaChaFromCache (erp.go:2932)
                                                                  cả họ, LIMIT 100
                                                                                       ▼
                                            filterProductsByGroups (erp.go:1664)  — lọc quyền nhóm
                                            enrichProductsWithPriceRanges (erp.go:1213) — price_range
                                            slimProductsForLLM (erp.go:1138) — gộp theo tên, ≤ 5 dòng
                                                                       ▼
                                            JSON: { source:"astradb_cache", data[], count }
```

> Câu mẫu **"storm 3"** đi nhánh: `exact_web_name=false` → có chữ → B1 LIKE trả **0
> nhóm** → **B2 hybrid** bắt được `ma_cha` (qua `match.MaCha`) → `matchedMaCha` →
> `getProductsByMaChaFromCache` → **cả họ** → `price_range` phủ mọi màu/size.
>
> Câu có **màu/size** như **"FF800 trắng L"** KHÔNG dừng ở `products`: agent nhận ra
> SPECIFIC-VARIANT và gọi `product_variants` (mục G) để pinpoint 1 SKU + giá đơn.
> `products` ở đây — nếu bị gọi — chỉ trả **cả họ FF800 + `price_range`**, vì nhánh
> pinpoint (`match.Specific → matchedMA → getProductByMaFromCache`) **đã được gỡ
> khỏi `products`**; `match.Specific`/`match.MA` giờ **chỉ** phục vụ `product_variants`.

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
   - Ngược lại → **không** Specific (vd "storm 3" — cả họ ngang nhau).

> ⚠️ **Ai tiêu thụ `Specific`?** Engine **vẫn tính** `Specific` (và log nó ở `:228`),
> nhưng nhánh `products` của `ERPQuery` **không còn đọc** `match.Specific`/`match.MA`
> nữa — nó chỉ lấy `match.MaCha` và luôn trả cả họ. Tín hiệu `Specific` + `MA` giờ
> **chỉ** phục vụ resource **`product_variants`** (`hybridMatchVariant`, mục G), nơi
> pinpoint 1 SKU mới có ý nghĩa (khách đã cho màu/size).

> 🔎 **Hai tín hiệu, hai vai trò khác nhau:** cổng *relevance* (nhận/loại) đo trên
> **logit `$rerank`** (boundary tự nhiên ≈0 của cross-encoder); còn cổng *specific*
> (pinpoint vs cả họ) đo trên **cosine `$vector`** `[0,1]` ổn định. Cosine **không**
> dùng để chặn relevance, rerank **không** dùng để đo gap anh em.

---

## E. LLM fuzzy fallback (B3 — `erp_fuzzy.go:94`)

Chỉ chạy khi **B2 không ra** (`matchedMaCha == ""`) — do embedding tắt, Astra lỗi,
hoặc dưới sàn liên quan.

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

> 📌 B3 **chỉ** ra `ma_cha` (không pinpoint MA) → kéo **cả họ** qua
> `getProductsByMaChaFromCache`. Trong luồng `products` thì **B2 cũng vậy**: chỉ lấy
> `ma_cha`, luôn trả cả họ. Pinpoint 1 SKU không còn xảy ra trong `products` — nó là
> việc của `product_variants` (mục G).

---

## F. Hậu xử lý & hình dạng dữ liệu (erp.go:457-483)

Sau khi có `cachedData` (luôn ở mức **cả họ** với products), ba bước chung:

1. **`filterProductsByGroups`** (`:1664`) — lọc theo `productGroups` mà permission
   token cho phép (ẩn nhóm sản phẩm ngoài quyền).
2. **`enrichProductsWithPriceRanges`** (`:1213`) — với mỗi sản phẩm có `ma_cha`,
   nạp lại toàn bộ biến thể (`getProductsByMaChaFromCache`),
   `calculateProductPriceRange` (`:1265`) lấy min/max, `formatProductPriceRange`
   (`:1289`) ra nhãn `"x đ – y đ"`. Thêm field `price_min`, `price_max`,
   `price_range`.
3. **`slimProductsForLLM`** (`:1138`) — gộp các biến thể cùng `ten_dong_bo_web` về
   **một** dòng, cắt còn **tối đa 5** (const `slimProductsForLLMLimit`, `:1136`),
   chỉ giữ field LLM cần.

### Ví dụ response — P1 (nhiều nhóm web, `astradb_cache_web_groups`, erp.go:359)

```json
{
  "status": "success",
  "source": "astradb_cache_web_groups",
  "resource": "products",
  "count": 2,
  "data": [
    { "web_name": "Mũ LS2 FF800 Storm II", "parent_codes": ["FF800"], "variant_count": 12, "is_fallback": false },
    { "web_name": "Mũ LS2 FF800 Carbon",   "parent_codes": ["FF800C"], "variant_count": 6, "is_fallback": false }
  ]
}
```

> ⚠️ **`variant_count` ≠ tồn kho.** Đây là số biến thể (màu×size) của dòng,
> đếm từ cache `models.CachedProduct`. LLM tuyệt đối KHÔNG được trả như SL còn
> ("FF901: 32 con"). Muốn biết tồn → gọi `resource="inventory"` với
> `search=<web_name>` + `exact_web_name=true`. Field đổi tên từ `count` cũ
> (commit này) để tránh ngộ nhận; field `count` cấp top-level vẫn = số nhóm trả về.

> ℹ️ **Không còn ví dụ "P3 pinpoint 1 SKU" cho `products`.** Pinpoint 1 biến thể +
> giá đơn nay **chỉ** đến từ `product_variants` (mục G), với response mang `ma` +
> `price` (`source=astradb_hybrid_variants`). `products` luôn trả ở mức họ như ví dụ
> dưới.

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

Pinpoint 1 biến thể ("FF800 trắng L") — qua `product_variants` (mục G), KHÔNG qua `products`:

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

## G. Resource chị em `product_variants` (erp.go:487-650)

Khi khách đã rõ **mã cha + thuộc tính cụ thể** ("FF901 đen bóng size L giá bao
nhiêu?"), agent gọi `product_variants` thay vì `products` để lấy **đúng 1 SKU +
giá** (không phải khoảng giá):

1. **Bắt buộc `parent_code`** (`:489`); thiếu → HTTP 400 (`missing_parent_code`).
2. **`searchVariantsByAttributes`** (`:498` → `erp_variants.go:25`) — lọc cache
   MySQL theo `parent_code` + `color` + `size` + `brand`. Lưu ý: `size` so khớp
   **bằng chính xác** (`LOWER(thuoc_tinh_2) = LOWER(size)`) còn `color` là substring
   `LIKE`. Đường này nhanh + chính xác khi kho lưu chuẩn, nhưng trượt khi
   `thuoc_tinh_2` lưu `"Size L"` / `"L (40)"` / có space thừa, hoặc khi khách
   gõ màu khác ngôn ngữ với giá trị lưu.
3. **Zero-result → (0) Astra hybrid** (`:559`, khi `searchVariantsByAttributes`
   trả rỗng): chạy **cùng engine hybrid với luồng products** — và là **nơi DUY
   NHẤT còn dùng `match.MA`/`match.Specific` để pinpoint 1 SKU** (products đã gỡ).
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
4. **Vẫn rỗng → fallback song ngữ** (`:586`): nếu cache lưu "Gloss Black" mà
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
| Exact web-name | `backend/api/handlers/erp.go:281` → `:1001` | `searchProductsByExactWebNameFromCache` (MySQL) |
| Search rỗng → KB | `backend/api/handlers/erp.go:435` → `:437` | trả `data:[]` + `source=empty_search_use_knowledge` (return; agent chuyển sang Astra Retrieval) |
| **B1 web-group LIKE** | `backend/api/handlers/erp.go:330` → `:1038` | `searchProductWebGroupsFromCache` (MySQL 2-pass: `ten_dong_bo_web`→`ten`) |
| Rank web-group | `backend/engine/product_grouping.go:25` | `RankProductWebGroups` / `WebGroupMatch` (`:14`) |
| Response >1 nhóm | `backend/api/handlers/erp.go:359` | `source=astradb_cache_web_groups` (disambiguation) |
| 1 nhóm → fetch họ | `backend/api/handlers/erp.go:369` → `:2932` | `getProductsByMaChaFromCache(pc)` trực tiếp (KHÔNG fuzzy/LLM) |
| **B2 embedding fuzzy** | `backend/api/handlers/erp.go:403` → `engine/product_embeddings.go:207` | `FuzzyMatchProductWithEmbedding` — products **chỉ đọc `match.MaCha`** (gated `ERP_EMBEDDING_FUZZY_ENABLED`) |
| Astra hybrid call | `backend/engine/product_embeddings.go:515` | `astraHybridFindAndRerank` (`findAndRerank`, `sort.$hybrid`) |
| Build `$lexical` | `backend/engine/product_embeddings.go:343` | `buildProductLexicalText` (mã trước, label sau) |
| Build `$vectorize` | `backend/engine/product_embeddings.go:361` | `buildProductEmbeddingLabel` |
| Sàn liên quan | `backend/engine/product_embeddings.go:240` | `passesRelevanceFloor` (`Rerank > rerankFloor≈0`, `:52`) |
| Pinpoint SKU (chỉ variant) | `backend/engine/product_embeddings.go:251` | `isSpecificSKUMatch` (gap cosine > `siblingCosineGap` 0.05) — `Specific`/`MA` **chỉ** dùng bởi `product_variants`, **không** còn ở `products` |
| Sync embeddings | `backend/engine/product_embeddings.go:108` | `SyncProductEmbeddingsToAstraDB` (diff `label_hash`, schema `v2-lexical`) |
| **B3 LLM fuzzy** | `backend/api/handlers/erp.go:410` → `erp_fuzzy.go:94` | `fuzzyMatchMaChaWithLLM` (chỉ khi B2 trống; trả `ma_cha`) |
| AI client | `backend/api/handlers/erp_fuzzy.go:20` | `getAIClient` (mặc định `claude-haiku-4-5`, `:60`) |
| Fetch cả họ (products) | `backend/api/handlers/erp.go:419` → `:2932` | `getProductsByMaChaFromCache(matchedMaCha)` (MySQL `ma_cha=`, LIMIT 100) — **đường fetch duy nhất** của products |
| Fetch 1 SKU (chỉ variant) | `backend/api/handlers/erp_variants.go:195` → `erp.go:2920` | `getProductByMaFromCache` (MySQL `ma=`) — chỉ `hybridMatchVariant` gọi |
| Lọc quyền nhóm | `backend/api/handlers/erp.go:457` → `:1664` | `filterProductsByGroups` |
| Enrich khoảng giá | `backend/api/handlers/erp.go:458` → `:1213` | `enrichProductsWithPriceRanges` |
| Tính / format giá | `backend/api/handlers/erp.go:1265`, `:1289` | `calculateProductPriceRange` / `formatProductPriceRange` |
| Slim cho LLM | `backend/api/handlers/erp.go:462` → `:1138` | `slimProductsForLLM` (gộp theo tên, max 5 — const `:1136`) |
| Response JSON | `backend/api/handlers/erp.go:476` | `source:"astradb_cache"` + `data[]` + `count` |
| Ghi audit | `backend/api/handlers/erp.go:472` | `writeAuditLog` |
| **product_variants** | `backend/api/handlers/erp.go:487` | nhánh variant theo `parent_code` + màu/size/brand |
| Attr search | `backend/api/handlers/erp.go:498` → `erp_variants.go:25` | `searchVariantsByAttributes` (MySQL, size exact) |
| **Variant Astra hybrid** | `backend/api/handlers/erp.go:559` → `erp_variants.go:153` | `hybridMatchVariant` (reuse `FuzzyMatchProductWithEmbedding`, guard label identity `variantBelongsToParent` `:217`, gate `ERP_EMBEDDING_FUZZY_ENABLED`) |
| Resolve parent qua label | `backend/api/handlers/erp_variants.go:93` | `resolveParentMaCha` / `parentCodeInLabel` (model name → `ma_cha`) |
| Attr fuzzy song ngữ | `backend/api/handlers/erp.go:586` → `erp_fuzzy.go:192` | `fuzzyMatchAttributesWithLLM` ("đen bóng"→"Gloss Black") |
| Quyền dùng chung | `backend/api/handlers/erp.go:87` → `:208` | `methodPermissionResource`: `product_variants` → `products` |

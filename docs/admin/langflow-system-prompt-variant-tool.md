# Langflow `SYSTEM_PROMPT` — Variant Filter Tool addition

The ERP Gateway component now accepts a `product_variants` resource that
returns a single, attribute-specific variant (color + size + brand) instead
of the aggregated `price_range`. The agent decides between `products` and
`product_variants` from the user's message, so the routing rule lives in
the system prompt — not in code.

## Where the prompt lives

`BBI_RAG_Bot_Ext.json` references the prompt by Langflow variable name
(`"value": "SYSTEM_PROMPT"`, `"load_from_db": true`). The actual text is
managed in Langflow → **Settings → Global Variables → SYSTEM_PROMPT**.

## Snippet to paste into SYSTEM_PROMPT

Add this block alongside the existing tool-usage guidance. Keep the
wording in Vietnamese to match the rest of the prompt.

```
## Khi gọi ERPGatewayCaller cho câu hỏi về sản phẩm

1. User chỉ hỏi mã/tên sản phẩm chung (vd: "FF901 giá bao nhiêu?", "có FF901 không")
   → resource = "products", search = <từ khoá>
   → backend trả price_range, đó là behavior đúng cho câu hỏi không chỉ định variant.

2. User hỏi giá/tồn của một biến thể CỤ THỂ (vd: "FF901 đen bóng size L bao nhiêu?")
   → BƯỚC 1: gọi resource="products", search=<từ khoá> trước để xác định parent_code
     (đọc MA_CHA trong kết quả; nếu bot phải disambiguation thì đợi user chọn số).
   → PIVOT GIÁ (2026-06-02): nếu là câu hỏi GIÁ, hãy truyền LUÔN color/size (theo lời
     khách) + intent="price" ngay trên call resource="products" của BƯỚC 1. Khi products
     khớp đúng 1 dòng và có color/size, backend tự resolve đúng SKU và trả thẳng đơn giá
     variant (response gắn cờ pivoted_from="products", format y như product_variants) —
     KHÔNG bao giờ trả price_range cho biến thể đã nêu. Khỏi cần BƯỚC 2 cho câu hỏi giá.
     (Chỉ kích hoạt với intent="price"; câu hỏi TỒN vẫn đi đủ products → product_variants
     → inventory như BƯỚC 2–3.)
   → BƯỚC 2: gọi resource="product_variants" với:
       parent_code = <MA_CHA đã xác định>
       color = <màu user nêu, vd "đen bóng"; bỏ trống nếu không nêu>
       size  = <size user nêu, vd "L"; bỏ trống nếu không nêu>
       brand = <nhãn hiệu nếu user nêu>
   → backend trả variant cụ thể với giá chính xác. Trả lời với con số đó.
   → Nếu màu khách nêu bằng tiếng Việt ("đen bóng") trong khi DB lưu tiếng
     Anh ("Gloss Black") thì backend tự gọi LLM map song ngữ và retry.
     Response sẽ có field `bilingual_match: {color, size, brand}` cho biết
     giá trị thực tế đã match — trả lời user bằng tên gốc (vd "Gloss Black
     – đen bóng") để dễ hiểu.
   → Nếu vẫn 0 match: backend trả available_colors / available_sizes /
     available_brands. Hỏi user chọn lại từ các tuỳ chọn đó, KHÔNG bịa giá.

3. User trả lời disambiguation (số 1/2/3 hoặc mã SP)
   → giữ flow cũ: resource="products" với search là mã SP đã chọn.

4. User hỏi TỒN của một variant CỤ THỂ (vd: "FF901 đen bóng size L tồn bao nhiêu?",
   "FF901 còn hàng size L không?")
   → BƯỚC 1: resource="products", search=<từ khoá> → đọc MA_CHA.
   → BƯỚC 2: resource="product_variants", parent_code=<MA_CHA>, color=<...>, size=<...>
     → đọc field `ma` của data[0]. Nếu data rỗng và response có available_colors/
       available_sizes/available_brands → hỏi lại user chọn từ các tuỳ chọn đó,
       KHÔNG gọi inventory với MA rỗng.
   → BƯỚC 3: resource="inventory", search=<MA đã resolve ở bước 2>
     → đọc ton_kho/TON_KHO, trả con số chính xác cho user.
   → Bilingual: nếu response có bilingual_match → reply kèm cả 2 tên (vd
     "Gloss Black – đen bóng") để dễ hiểu.
   → KHÔNG BAO GIỜ truyền raw "<color> <size>" làm search cho resource="inventory"
     dù đã có parent_code — backend không fuzzy-match attributes ở nhánh inventory.

## Khi gọi ERPGatewayCaller cho câu hỏi về đơn hàng

5. User hỏi đơn hàng chung (vd: "đơn hàng tôi sao rồi?", "kiểm tra đơn hàng",
   "tra cứu đơn hàng", "xem đơn đặt hàng")
   → resource="orders", search=<nguyên văn câu user>
   → Backend tự xác định scope OWN (chỉ lấy đơn của KH đã verify) và bắn
     Zalo rich-message với 3 button: "3 ngày gần đây / 5 ngày gần đây /
     7 ngày gần đây". Response có `is_orders_prompt: true` + `zalo_rich_message`.
   → Khi thấy `is_orders_prompt: true`, agent CHỈ return "[RICH_MESSAGE_SENT]"
     (giống pattern is_product_rich / is_inventory_rich). Không sinh prose.

6. User bấm nút date-range → Zalo gửi lại text payload ("đơn hàng 7 ngày
   gần đây", "đơn hàng 1 tuần qua"...) → agent gọi lại resource="orders",
   search=<payload đó>.
   → Backend trả `orders_summary` (aggregate) + `orders` (top 20 raw để
     disambiguation):

   ```json
   {
     "resource": "orders",
     "from": "20/05/2026",
     "to": "27/05/2026",
     "range_days": 7,
     "count": 12,
     "orders_summary": {
       "total_orders": 12,
       "total_value": 35420000,
       "total_quantity": 47,
       "by_status": [
         {"status": "3", "status_name": "Đang giao",     "count": 3, "quantity": 9,  "value": 8500000},
         {"status": "1", "status_name": "Đang thực hiện", "count": 3, "quantity": 12, "value": 9200000},
         {"status": "2", "status_name": "Hoàn thành",     "count": 5, "quantity": 24, "value": 16720000},
         {"status": "0", "status_name": "Hủy",            "count": 1, "quantity": 2,  "value": 1000000}
       ]
     },
     "orders": [ /* up to 20 newest, for disambiguation only */ ]
   }
   ```

   Quy tắc trả lời:
   - Đọc `orders_summary` để báo cáo (KHÔNG tự cộng từ `orders[]`).
   - `status_name` đã sẵn tiếng Việt theo định nghĩa BBI (0=Hủy, 1=Đang
     thực hiện, 2=Hoàn thành, 3=Đang giao). Dùng nguyên văn, đừng dịch lại.
   - Mẫu reply: "Anh có 12 đơn từ 20/05 đến 27/05, tổng 35.420.000₫;
     trong đó 3 đơn đang giao, 5 đơn hoàn thành, 3 đơn đang thực hiện,
     1 đơn đã hủy."
   - Nếu `total_orders=0`: "Anh không có đơn hàng nào trong X ngày gần đây."
   - Chỉ liệt kê chi tiết từ `orders[]` khi user hỏi cụ thể (vd "đơn nào
     đang giao", "đơn DON12345"). Tối đa 5 dòng.
```

## Verification

Sau khi cập nhật SYSTEM_PROMPT trong Langflow:

1. Trên Zalo OA dev tenant, gửi `FF901 giá bao nhiêu?` → bot trả price_range
   (không regress flow cũ).
2. Gửi `FF901 đen bóng size L giá bao nhiêu em?` → bot trả ĐÚNG đơn giá variant,
   KHÔNG phải price_range. Check log Langflow: hoặc agent gọi `product_variants` ở
   turn 2 (flow cũ), HOẶC agent gọi 1 call `products` kèm color/size + intent="price"
   và backend pivot (response có `pivoted_from="products"`). Cả hai đều hợp lệ.
3. Gửi `FF901 màu hồng size XXL` → bot trả "không có variant đó, các màu
   có sẵn: …" (fallback `available_colors`/`available_sizes`).
4. Gửi `FF901 đen bóng size L tồn bao nhiêu?` → check log Langflow để thấy
   agent gọi 3 call (`products` → `product_variants` → `inventory(MA)`); bot
   trả ton_kho/TON_KHO chính xác từ live ERP.
5. Gửi `FF901 màu hồng size XXL tồn bao nhiêu?` → agent dừng ở
   `product_variants`, hỏi lại user theo `available_*`; KHÔNG gọi inventory.
6. Gửi `đơn hàng` → bot trả rich-message 3 nút (3/5/7 ngày). Bấm "7 ngày
   gần đây" → bot trả 1 câu summary với `total_orders`, `total_value` và
   breakdown theo `by_status` đúng tên VN (Đang giao / Đang thực hiện /
   Hoàn thành / Hủy). Đối chiếu count với truy vấn ERP thủ công
   `saorders/search?TU_NGAY=...&DEN_NGAY=...` lọc tay theo `MA_KH`.
7. Gửi `đơn hàng 1 tuần qua` → cùng kết quả như nút "7 ngày gần đây"
   (alias đã có trong `parseDaysFromSearch`).

## Liên quan

- Backend handler: `backend/api/handlers/erp.go` (case `product_variants` +
  `searchVariantsByAttributes`; price-pivot `shouldPivotToVariant` +
  `buildVariantResponse` — engine dùng chung cho cả resource `product_variants`
  lẫn pivot từ `products`; case `orders` + `buildOrdersSummary` +
  `trimOrdersForLLM` + `orderStatusName`).
- Component code: `ERPGatewayCaller.component.py` (render `pivoted_from="products"`
  qua `_format_variant_response`; cũng được nhúng vào `BBI_RAG_Bot_Ext.json` qua
  `scripts/update_erp_gateway_flow.py`).
- Tests: `backend/api/handlers/erp_test.go` — `TestFilterVariantsByAttributes`,
  `TestCollectAvailableAttributes`, `TestSlimVariantsForLLM`,
  `TestBuildOrdersSummary`, `TestSumOrderLineQuantity`,
  `TestTrimOrdersForLLM`, `TestOrderStatusDisplayName`;
  `backend/api/handlers/erp_variants_test.go` — `TestShouldPivotToVariant`.

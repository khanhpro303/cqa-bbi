# Loại trừ mã khách hàng khỏi cache

Tài liệu này mô tả tính năng cho phép quản trị viên **loại trừ một số mã khách
hàng** khỏi bảng cache khách hàng (`cached_customers`) — tương tự tính năng "Loại
trừ dòng SP" của sản phẩm — kèm **guard** không cho loại trừ mã đã link group GMF.

> 🗣️ **Hiểu nhanh (không kỹ thuật):** Job đồng bộ ERP kéo *toàn bộ* khách hàng về
> cache để bot tra cứu nhanh. Có những mã khách hàng nội bộ/rác không cần lưu —
> admin tick chọn để loại. Nhưng nếu một mã đã được gắn vào một **group GMF**, hệ
> thống **chặn** không cho loại trừ, vì cache đó là nguồn phân quyền của group.

## Vị trí trên giao diện

Mở **Jobs → job loại `erp_customer_cache` → tab "Loại trừ mã KH"**. Tab này có:

- Nút **Cấu hình thủ công** — modal danh sách checkbox các mã KH trong cache.
- Nút **Import Excel** — tải file mẫu (đã pre-fill) và upload danh sách T/F.
- Bảng các mã đang bị loại trừ + nút **Tải lại**.

Mã đã link group GMF hiển thị **biểu tượng khoá + chip "GMF"**, checkbox bị vô
hiệu hoá và không thể chọn để loại trừ.

## Cơ chế

Khác với sản phẩm (có bảng raw `erp_raw_products` để rebuild), khách hàng **không
có bảng raw** — job `erp_customer_cache` re-fetch ERP mỗi lần chạy. Do đó:

- **Khi đồng bộ:** job đọc danh sách loại trừ (`loadCustomerExclusionSet`) và bỏ
  qua các mã đó khi ghi vào `cached_customers`
  (`backend/engine/erp_customer_cache.go`).
- **Khi admin lưu:** handler gọi `engine.ApplyCustomerCodeExclusions(tenantID)` —
  xoá ngay các row bị loại khỏi `cached_customers` để có hiệu lực tức thì, trả về
  `cache_rows` còn lại.
- **Lưu ý un-exclude:** bỏ loại trừ một mã chỉ khôi phục khách hàng đó vào cache ở
  **lần đồng bộ ERP kế tiếp** (không có raw để rebuild lại). Job vốn chạy theo
  lịch nên điều này chấp nhận được; cần đồng bộ ngay thì chạy lại job thủ công.

Danh sách loại trừ lưu ở bảng `erp_customer_code_exclusions` (mỗi row = một mã bị
loại; xoá row = không còn loại). Khoá duy nhất theo `(tenant_id, customer_code)`.

## Guard GMF

Mã khách hàng được xem là "đã link GMF" nếu xuất hiện ở
`crm_groups.customer_code` của tenant (`gmfLinkedCustomerCodes`).

- **PUT** `/erp/customer-codes/exclusions` và **POST** `/erp/customer-codes/import`
  kiểm tra giao giữa danh sách yêu cầu loại trừ và tập GMF-linked **trước khi**
  ghi DB.
- Nếu có mã vi phạm → trả **HTTP 409** `{"error":"gmf_linked_blocked","codes":[...]}`
  và **không lưu/không import gì** (chặn toàn bộ). Giao diện hiển thị danh sách mã
  vi phạm để admin bỏ chọn.

## REST API

Tất cả yêu cầu quyền `settings` (đọc cho GET, ghi cho POST/PUT).

| Method | Path | Mô tả |
|--------|------|-------|
| GET | `/tenants/:tid/erp/customer-codes` | Danh sách mã KH ứng viên (cache + orphan), cờ `is_excluded`, `is_gmf_linked` |
| GET | `/tenants/:tid/erp/customer-codes/template` | Tải file Excel mẫu (mã GMF-linked ép F + ghi chú) |
| POST | `/tenants/:tid/erp/customer-codes/import` | Import Excel (cột A=Mã KH, B=T/F) |
| PUT | `/tenants/:tid/erp/customer-codes/exclusions` | Thay toàn bộ danh sách loại trừ: `{"excluded_customer_codes":[...]}` |

## File liên quan

- `backend/db/models/erp_customer_code_exclusion.go` — model + bảng.
- `backend/engine/erp_customer_cache.go` — filter trong job + `loadCustomerExclusionSet`, `ApplyCustomerCodeExclusions`.
- `backend/api/handlers/erp_customer_exclusions.go` — 4 handler + guard `gmfLinkedCustomerCodes`.
- `backend/api/router.go` — đăng ký route.
- `frontend/src/components/erp-exclusions/ExcludeCustomers{Manage,Import}Modal.vue` + `frontend/src/views/Jobs/JobDetail.vue` (tab `exclude_customers`).

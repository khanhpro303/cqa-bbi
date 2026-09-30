# Kết nối Facebook Messenger

CQA dùng **Page ID** và **Page Access Token** để lấy lịch sử tin nhắn Messenger. Bạn có thể kết nối qua Meta hoặc nhập thông tin thủ công.

## Kết nối qua Meta

1. Đăng nhập CQA bằng tài khoản có quyền chỉnh sửa kênh trong workspace.
2. Vào **Kênh chat → Kết nối kênh mới → Facebook → Kết nối Facebook Page**.
3. Đăng nhập Facebook và cấp quyền cho những Page cần kết nối trên màn hình Meta.
4. Khi trở lại CQA, chọn **một Page** trong danh sách và xác nhận kết nối. CQA không tự chọn Page đầu tiên.
5. Trong chi tiết kênh, bấm **Đồng bộ ngay**, rồi kiểm tra cuộc hội thoại và tin nhắn đã nhập.

Token được lưu mã hóa ở backend, không trả về trình duyệt trong luồng này. CQA chỉ lưu kênh sau khi bạn xác nhận Page và Meta chấp nhận đăng ký webhook. Kết nối lại cùng Page trong cùng workspace cập nhật kênh hiện có.

Nếu nút kết nối chưa khả dụng, quản trị viên cần hoàn tất cấu hình Facebook Login for Business cho quyền Page. Xem [chuẩn bị App Review](./facebook-app-review.md). Cách nhập token thủ công bên dưới vẫn được giữ lại.

::: info Hai kết nối khác nhau
Liên kết Facebook trong **Hồ sơ cá nhân** dùng để đăng nhập CQA. Kết nối Facebook Page ở **Kênh chat** dùng để truy cập dữ liệu fanpage trong workspace; không thay đổi cách đăng nhập hoặc quyền thành viên.
:::

## Bạn thuộc trường hợp nào?

| Trường hợp | Đặc điểm | Đi đến |
|------------|----------|--------|
| **Trường hợp 1** | Fanpage nằm trong Meta Business Suite (quản lý qua business.facebook.com) | [Trường hợp 1](#truong-hop-1-fanpage-trong-meta-business-suite) |
| **Trường hợp 2** | Fanpage do cá nhân quản lý, không nằm trong Meta Business Suite | [Trường hợp 2](#truong-hop-2-fanpage-ca-nhan) |

::: tip Không chắc mình thuộc trường hợp nào?
Truy cập [business.facebook.com](https://business.facebook.com). Nếu bạn thấy fanpage trong danh sách → Trường hợp 1. Nếu không thấy hoặc chưa từng dùng → Trường hợp 2.
:::

---

## Trường hợp 1: Fanpage trong Meta Business Suite

Dành cho fanpage đã được thêm vào một Business Portfolio trên Meta Business Suite.

### Bước 1.1: Tạo App trên Facebook Developers

1. Truy cập [developers.facebook.com](https://developers.facebook.com) → đăng nhập bằng tài khoản Facebook cá nhân (phải là admin của fanpage).

![Facebook Developers](/screenshots/facebook/Fb-fanpage-1.png)

2. Vào [developers.facebook.com/apps](https://developers.facebook.com/apps) → click **Tạo ứng dụng**.

![Danh sách Apps](/screenshots/facebook-meta/fb-fanpage-meta-business-1.png)

3. Nhập tên App (ví dụ: Chat Quality Agent) và email liên hệ → click **Tiếp**.

![Tạo App](/screenshots/facebook/Fb-fanpage-2.png)

4. Chọn trường hợp sử dụng: **Tương tác với khách hàng trên Messenger from Meta** → click **Tiếp**.

![Chọn use case](/screenshots/facebook/Fb-fanpage-3.png)

5. Ở bước Doanh nghiệp, **chọn hồ sơ doanh nghiệp** đang quản lý Fanpage cần kết nối → click **Tiếp**.

![Chọn doanh nghiệp](/screenshots/facebook-meta/fb-fanpage-meta-business-2.png)

6. Bước Yêu cầu — bỏ qua, click **Tiếp**. Xem lại tổng quan → click **Tạo ứng dụng**.

### Bước 1.2: Lấy Page ID

1. Truy cập [business.facebook.com](https://business.facebook.com) → đăng nhập.
2. Click **Settings** (biểu tượng bánh răng ở thanh bên trái).
3. Vào mục **Tài khoản** → **Trang**.
4. Chọn fanpage cần kết nối — **Page ID** hiển thị ngay bên phải, dưới tên fanpage.

![Business Settings - Pages](/screenshots/facebook-meta/fb-fanpage-meta-business-14.png)

::: tip Mẹo
Bạn cũng có thể thấy Page ID trên URL. Ví dụ: `https://business.facebook.com/latest/home?asset_id=123456789` — số `asset_id` chính là Page ID.
:::

### Bước 1.3: Tạo System User

System User là "tài khoản ảo" dùng để kết nối API, không gắn với tài khoản cá nhân nào.

1. Trong Business Settings, vào mục **Người dùng** → **Người dùng hệ thống**.
2. Click **+ Thêm**.

![Người dùng hệ thống](/screenshots/facebook-meta/fb-fanpage-meta-business-6.png)

3. Đặt tên (ví dụ: Chat Quality Agent) → chọn role **Admin** → click **Create System User**.

![Tạo System User](/screenshots/facebook-meta/fb-fanpage-meta-business-7.png)

### Bước 1.4: Gán quyền cho System User

1. Chọn System User vừa tạo → click **Add Assets**.
2. Chọn **Pages** → tìm và chọn fanpage cần kết nối.
3. Bật các quyền:
   - **Manage Page** (Quản lý trang)
   - **Read Page content** (Đọc nội dung trang)
   - **Manage and access Page conversations in Messenger** (Quản lý tin nhắn Messenger)
4. Click **Save Changes**.

### Bước 1.5: Tạo Access Token

1. Quay lại trang System User → click **Tạo mã**.

![System User](/screenshots/facebook-meta/fb-fanpage-meta-business-9.png)

2. Chọn App đã tạo ở Bước 1.1.

![Chọn App](/screenshots/facebook-meta/fb-fanpage-meta-business-10.png)

3. Chọn thời hạn token → chọn **Không bao giờ** để token không hết hạn.

![Thời hạn token](/screenshots/facebook-meta/fb-fanpage-meta-business-11.png)

4. Chọn các permissions:
   - `pages_show_list`
   - `pages_read_engagement`
   - `pages_messaging`
   - `pages_manage_metadata`

![Chọn permissions](/screenshots/facebook-meta/fb-fanpage-meta-business-12.png)

5. Click **Generate Token** → sao chép token và lưu lại.

![Token đã tạo](/screenshots/facebook-meta/fb-fanpage-meta-business-13.png)

::: info Lưu ý
Token của System User **không hết hạn** (trừ khi bạn xóa thủ công). Đây là cách ổn định nhất cho kết nối lâu dài.
:::

### Bước 1.6: Nhập vào CQA

Trong CQA, vào **Kênh chat** → **Kết nối kênh mới** → chọn **Facebook**:
- **Page ID**: số đã lấy ở Bước 1.2
- **Page Access Token**: token đã tạo ở Bước 1.5

![Nhập vào CQA](/screenshots/facebook/Fb-fanpage-13.png)

---

## Trường hợp 2: Fanpage cá nhân

Dành cho fanpage do cá nhân tạo và quản lý, không nằm trong Business Portfolio nào.

### Bước 2.1: Đăng ký tài khoản Developer

1. Truy cập [developers.facebook.com](https://developers.facebook.com).
2. Click **Get Started**.
3. Đăng nhập bằng tài khoản Facebook cá nhân — phải là admin của fanpage.
4. Hoàn tất đăng ký để tạo tài khoản Meta for Developers.

### Bước 2.2: Tạo App

1. Truy cập [developers.facebook.com/apps](https://developers.facebook.com/apps) → click **Tạo ứng dụng**.

![Tạo App](/screenshots/facebook/Fb-fanpage-2.png)

2. Nhập tên App (ví dụ: Chat Quality Agent) và email liên hệ → click **Tiếp**.
3. Chọn trường hợp sử dụng: **Tương tác với khách hàng trên Messenger from Meta** → click **Tiếp**.

![Chọn use case](/screenshots/facebook/Fb-fanpage-3.png)

4. Ở bước Doanh nghiệp, chọn **không kết nối hồ sơ doanh nghiệp** → click **Tiếp**.

![Không kết nối doanh nghiệp](/screenshots/facebook/Fb-fanpage-4.png)

5. Bước Yêu cầu — bỏ qua, click **Tiếp**. Xem lại tổng quan → click **Tạo ứng dụng**.

![Tổng quan App](/screenshots/facebook/Fb-fanpage-6.png)

### Bước 2.3: Lấy Page ID và Page Access Token

Đây là bước quan trọng nhất — bạn sẽ lấy được cả Page ID lẫn Token cùng lúc.

1. Truy cập **Graph API Explorer**: [developers.facebook.com/tools/explorer/](https://developers.facebook.com/tools/explorer/)

![Graph API Explorer](/screenshots/facebook/Fb-fanpage-7.png)

2. Ở dropdown **Ứng dụng trên Meta** → chọn App vừa tạo.
3. Ở dropdown **Người dùng hoặc Trang** → chọn **Lấy mã**.
4. Trong danh sách Quyền, thêm:
   - `pages_show_list`
   - `pages_messaging`
   - `pages_manage_metadata`
5. Click **Generate Access Token**.

6. Popup hiện ra → chọn fanpage cần kết nối → click **Tiếp tục**.

![Chọn Fanpage](/screenshots/facebook/Fb-fanpage-8.png)

7. Xem lại quyền truy cập → click **Lưu**.

![Xem lại quyền](/screenshots/facebook/Fb-fanpage-9.png)

8. Quay lại Graph API Explorer, trong ô query nhập: `me/accounts` → click **Gửi**.

9. Kết quả trả về danh sách fanpage:
   - `"id"`: đây là **Page ID**
   - `"access_token"`: đây là **Page Access Token**

![Kết quả me/accounts](/screenshots/facebook/Fb-fanpage-12.png)

Sao chép lại `id` và `access_token`.

### Bước 2.4: Đổi sang Token vĩnh viễn (bắt buộc)

Token vừa lấy ở trên chỉ có hạn khoảng 1-2 giờ. Cần đổi sang token vĩnh viễn.

#### Bước 2.4a: Lấy App ID và App Secret

1. Truy cập [developers.facebook.com/apps](https://developers.facebook.com/apps) → chọn App đã tạo.
2. Vào **Settings** → **Basic**.
3. Sao chép **App ID** và **App Secret** (click "Show" để hiện).

#### Bước 2.4b: Đổi thành Long-lived Token

Trong Graph API Explorer, nhập query:

```
oauth/access_token?grant_type=fb_exchange_token&client_id={APP_ID}&client_secret={APP_SECRET}&fb_exchange_token={TOKEN_Ở_BƯỚC_2.3}
```

Thay `{APP_ID}`, `{APP_SECRET}`, `{TOKEN_Ở_BƯỚC_2.3}` bằng giá trị thực.

Click **Submit**. Kết quả trả về `access_token` mới — đây là Long-lived User Token.

![Đổi token](/screenshots/facebook/Fb-fanpage-18.png)

### Bước 2.5: Nhập vào CQA

Trong CQA, vào **Kênh chat** → **Kết nối kênh mới** → chọn **Facebook**:
- **Page ID**: số `id` đã lấy ở Bước 2.3
- **Page Access Token**: Long-lived Token đã lấy ở Bước 2.4b

::: info CQA tự đổi sang Page Token
Bạn chỉ cần nhập Long-lived User Token. CQA sẽ tự động gọi Facebook API để đổi sang Page Token vĩnh viễn và lưu lại. Không cần đổi thủ công.
:::

![Nhập vào CQA](/screenshots/facebook/Fb-fanpage-13.png)

---

## Kiểm tra kết nối

Sau khi nhập Page ID và Token, CQA tự động kiểm tra kết nối. Nếu thành công, bạn sẽ thấy trạng thái **"Hoạt động"** cùng với tên fanpage.

![Kết nối thành công](/screenshots/facebook/Fb-fanpage-14.png)

Bấm **Đồng bộ ngay** để lấy tin nhắn từ Messenger.

![Tin nhắn đã đồng bộ](/screenshots/facebook/Fb-fanpage-15.png)

# Biến môi trường

Danh sách đầy đủ các biến môi trường trong file `.env`.

## Bắt buộc

| Biến | Mô tả | Ví dụ |
|------|-------|-------|
| `DB_PASSWORD` | Mật khẩu MySQL cho user CQA | `openssl rand -hex 16` |
| `MYSQL_ROOT_PASSWORD` | Mật khẩu root MySQL | `openssl rand -hex 16` |
| `JWT_SECRET` | Secret cho JWT tokens, tối thiểu 32 ký tự | `openssl rand -hex 32` |
| `ENCRYPTION_KEY` | Key 32 bytes cho mã hóa AES-256-GCM | `openssl rand -hex 16` |

## Server

| Biến | Mô tả | Mặc định |
|------|-------|----------|
| `SERVER_PORT` | Port của ứng dụng | `8080` |
| `SERVER_HOST` | Host bind | `0.0.0.0` |
| `APP_ENV` | Môi trường (`development` / `production`) | `production` |
| `APP_URL` | URL công khai (cho links trong notification) | |

## Database

| Biến | Mô tả | Mặc định |
|------|-------|----------|
| `DB_HOST` | MySQL host | `db` |
| `DB_PORT` | MySQL port | `3306` |
| `DB_USER` | MySQL username | `cqa` |
| `DB_PASSWORD` | MySQL password | |
| `DB_NAME` | Tên database | `cqa` |

## Rate Limiting

| Biến | Mô tả | Mặc định |
|------|-------|----------|
| `RATE_LIMIT_PER_IP` | Số request/phút cho mỗi IP | `100` |
| `RATE_LIMIT_PER_USER` | Số request/phút cho mỗi user | `300` |

## Đăng nhập Facebook (tùy chọn)

| Biến | Mô tả | Mặc định |
|------|-------|----------|
| `FACEBOOK_APP_ID` | App ID của ứng dụng Meta; được gửi ra trình duyệt để khởi tạo JavaScript SDK | _(trống = tắt nút đăng nhập)_ |
| `FACEBOOK_APP_SECRET` | App Secret dùng ở backend để xác minh access token; không bao giờ gửi ra trình duyệt | |
| `FACEBOOK_API_VERSION` | Phiên bản Facebook Graph API dùng cho SDK và backend | `v26.0` |
| `FACEBOOK_PAGE_LOGIN_CONFIG_ID` | ID cấu hình Facebook Login for Business loại User access token có các quyền Page; cần cho kết nối Page qua Meta | _(trống = tắt OAuth Page, vẫn giữ nhập thủ công)_ |

Đăng nhập Facebook chỉ áp dụng cho tài khoản CQA đã chủ động liên kết Facebook trong Hồ sơ cá nhân bằng xác nhận mật khẩu. Hệ thống không tự ghép tài khoản theo email, tự tạo user hoặc tự cấp quyền công ty từ tài khoản Facebook. Kết nối Page là luồng riêng của workspace; xem [chuẩn bị App Review](../usage/facebook-app-review.md).

Liên kết tài khoản tại **Cài đặt → Tài khoản Facebook** hoặc hồ sơ ở avatar: nhập mật khẩu CQA, bấm liên kết, xác nhận trên Facebook rồi quay về CQA. Luồng này chuyển trang trong cùng cửa sổ, không chờ callback popup của JavaScript SDK. Backend đổi authorization code và xác minh danh tính; state chỉ dùng một lần, hết hạn sau 10 phút và ràng buộc với trình duyệt/tài khoản khởi tạo. Không lưu mật khẩu hay access token trong phiên liên kết.

Callback cần đăng ký chính xác với Meta: `https://<tên-miền-CQA>/api/v1/channels/facebook/callback`. Với app Facebook Login for Business hiện có, hệ thống giữ `FACEBOOK_PAGE_LOGIN_CONFIG_ID` khi mở dialog; Meta yêu cầu cấu hình này có ít nhất một quyền business/asset ngoài `public_profile` và `email`. Không thể dùng riêng `public_profile` để bỏ qua yêu cầu này. Người dùng vẫn phải tự kiểm tra và chấp thuận các quyền Meta hiển thị; quyền chưa được App Review phê duyệt có thể hạn chế người dùng không thuộc vai trò của app. Nếu cần đăng nhập danh tính cá nhân không xin quyền Page/business, dùng app Facebook Login cổ điển phù hợp, không chuyển đổi app Business đang phục vụ kết nối Page chỉ để khắc phục lỗi popup. Xem [yêu cầu của Meta](https://developers.facebook.com/docs/facebook-login/facebook-login-for-business/#supported-permissions).

## SSL (tùy chọn)

| Biến | Mô tả | Mặc định |
|------|-------|----------|
| `LEGO_DOMAIN` | Domain cho SSL tự động (Let's Encrypt) | _(trống = HTTP mode)_ |
| `LEGO_EMAIL` | Email cho Let's Encrypt | |

::: tip
Để trống `LEGO_DOMAIN` nếu bạn không cần SSL hoặc đã có reverse proxy riêng (Cloudflare, Caddy...).
:::

## Personal Zalo Gateway (tùy chọn)

| Biến | Mô tả | Mặc định |
|------|-------|----------|
| `PERSONAL_ZALO_GATEWAY_SYNC_INTERVAL_SEC` | Chu kỳ flush batch từ gateway sang CQA | `900` |
| `PERSONAL_ZALO_GATEWAY_MAX_BATCH_CONVERSATIONS` | Số conversation tối đa trong một lần import | `20` |
| `PERSONAL_ZALO_GATEWAY_MAX_MESSAGES_PER_CONVERSATION` | Số message tối đa mỗi conversation trong một batch | `200` |
| `PERSONAL_ZALO_GATEWAY_REQUEST_TIMEOUT_MS` | Timeout gọi import endpoint nội bộ | `20000` |

## Tạo giá trị bảo mật

```bash
# Mật khẩu database
openssl rand -hex 16

# JWT secret (32+ ký tự)
openssl rand -hex 32

# Encryption key (đúng 32 bytes)
openssl rand -hex 16
```

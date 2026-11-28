# Agent chuyên môn và Bộ điều phối

Trước đây mỗi tầng có một bot làm tất cả: một prompt cho khách (bot public), một prompt cho nội bộ (bot private), Zalo OA và Zalo cá nhân dùng chung. Từ bản này, bot có thể chia thành nhiều **agent** chuyên môn (ví dụ bot công nợ, bot sản phẩm/kho/đơn, bot hỗ trợ marketing). Mỗi kênh Zalo OA hoặc Zalo cá nhân trả lời qua một **Bộ điều phối** (team), Bộ điều phối giữ danh sách agent được trả lời qua kênh đó.

## Khái niệm

- **Agent**: mã (`key`, không đổi sau khi tạo), tên, mô tả, **đối tượng** (`customer` khách hàng, `staff` nhân viên nội bộ, `all` cả hai), system prompt, danh sách **công cụ MCP** được gọi, nhãn tài liệu (`knowledge_tags`), runtime và flow Langflow riêng nếu có. Mỗi lần lưu là một phiên bản (giữ 30 phiên bản gần nhất).
- **Bộ điều phối**: tên, **giọng chung** (`persona_prompt`, đặt trước prompt của agent đang trả lời), danh sách agent theo thứ tự, agent mặc định. Có đúng một Bộ điều phối **mặc định**; kênh chưa gán Bộ điều phối nào thì trả lời qua nó.
- **Gán kênh**: mỗi kênh `zalo_oa` / `personal_zalo_import` trỏ tới một Bộ điều phối, hoặc để trống để dùng Bộ điều phối mặc định. Nhiều kênh có thể dùng chung một Bộ điều phối (một "bộ mặt" cho nhiều agent, hoặc nhiều bộ mặt cho cùng một nhóm agent).

## Ai trả lời một tin nhắn

1. **Tầng bảo mật vẫn do người nhắn quyết định**, như trước: nhân viên trong whitelist nhắn riêng là tầng `private` (đối tượng `staff`); khách hàng, khách chưa xác thực và mọi nhóm chat là tầng `public` (đối tượng `customer`). Quyền dữ liệu (mã KH, phạm vi ERP) vẫn nằm trong token ký, agent chỉ **thu hẹp** công cụ của tầng đó, không bao giờ mở rộng.
2. Tìm Bộ điều phối của kênh, lấy các agent đang bật phục vụ đối tượng đó.
3. Chọn agent: chỉ có một thì lấy agent đó; nhiều hơn thì lấy agent mặc định của Bộ điều phối nếu hợp, không thì agent đầu danh sách. (Bản tiếp theo sẽ cho Bộ điều phối tự chọn agent theo nội dung tin nhắn, giữ agent theo cuộc chat và cho agent chuyển lượt cho nhau.)
4. Không agent nào phục vụ đối tượng đó: bot **im lặng** với người này trên kênh đó, tin nhắn vẫn vào lịch sử chat để nhân viên trả lời. Ví dụ một Zalo cá nhân chỉ có bot công nợ cho khách thì nhân viên nhắn vào sẽ không được bot trả lời. Danh sách Bộ điều phối trả về `silent` để giao diện cảnh báo.

## Agent thay đổi gì trong một lượt chat

- **Prompt**: giọng chung của Bộ điều phối + prompt của agent, thay cho system prompt của AI Engine. Phần ngữ cảnh (trí nhớ chat, giỏ hàng, khiếu nại, hướng dẫn hệ thống) vẫn được nối thêm như trước.
- **Flow**: flow Langflow riêng của agent nếu có, không thì flow của AI Engine cho tầng đó. Agent phục vụ khách không được chạy trên flow nội bộ (API từ chối, và luồng chat cũng chặn).
- **Công cụ**: token ký mang `agent_key` và danh sách công cụ của agent. Máy chủ MCP `/mcp/erp/:tenantId` chỉ liệt kê và chỉ chạy các công cụ đó; gọi công cụ khác nhận lỗi `tool_not_allowed`. Agent không có công cụ nào thì không gọi được công cụ nào.
- **Việc backend làm thay bot** cũng theo công cụ của agent: ghi khiếu nại thành case chỉ khi agent có `complaint_record`; giỏ hàng chỉ khi agent có công cụ `order_cart_*`; tra đơn đang giao trả lời thẳng chỉ khi agent có `lookup_order`; ghi chú tư vấn sản phẩm và hướng dẫn tồn kho chỉ khi agent có công cụ nhóm sản phẩm.

## Bật agent và chuyển prompt cũ

- **Công tắc** `ai_agents_enabled` (Cài đặt → Agent). Tắt (mặc định): mọi thứ chạy như trước bằng prompt AI Engine.
- **Lần bật đầu tiên** chép hai system prompt của AI Engine thành hai agent: **Trợ lý khách hàng** (`tro-ly-khach-hang`, đối tượng khách) và **Trợ lý nội bộ** (`tro-ly-noi-bo`, đối tượng nhân viên), cả hai có mọi công cụ và đi theo flow của AI Engine, nằm trong Bộ điều phối **Mặc định** không có giọng chung. Nhờ vậy ngay sau khi bật, khách thấy y như cũ: cùng prompt (chép nguyên văn), cùng flow, cùng công cụ.
- Có thể **chép trước mà chưa bật** (`POST /settings/ai-agents/migrate`) để chia prompt thành các agent rồi mới bật.
- Khi agent đang bật, hai prompt chatbot ở trang AI Engine là **bản gốc để quay lại**: lưu trang AI Engine không đổi và không xoá chúng. Tắt công tắc là quay về bot cũ ngay.
- Prompt AI Engine đổi sau khi đã chép (ví dụ sửa lúc agent đang tắt): `GET /settings/ai-agents` trả `drift` là các agent lệch với bản gốc; `POST /settings/ai-agents/migrate` với `{"refresh": true}` chép lại vào các agent đó (ghi đè chỉnh sửa trong agent).

## API (theo tenant, `/api/v1/tenants/:tenantId`)

| Method | Đường dẫn | Quyền | Việc |
|---|---|---|---|
| GET | `/settings/ai-agents` | settings:r | `{enabled, has_agents, drift}` |
| PUT | `/settings/ai-agents` | owner/admin | `{enabled}`; bật lần đầu thì chép prompt |
| POST | `/settings/ai-agents/migrate` | owner/admin | chép prompt AI Engine (chưa bật), `{refresh}` để chép lại |
| GET | `/ai-agent-tools` | settings:r | danh mục công cụ (nhóm, tầng, có ghi dữ liệu không) và runtime cài được |
| GET/POST | `/ai-agents` | settings:r / settings:w | danh sách (không kèm prompt) / tạo |
| GET/PUT/DELETE | `/ai-agents/:agentId` | settings:r / w / w | xem (kèm prompt), lưu (gửi đủ mọi trường), xoá (chỉ khi không nằm trong Bộ điều phối nào) |
| GET | `/ai-agents/:agentId/revisions` | settings:r | các phiên bản đã lưu |
| GET/POST | `/ai-teams` | settings:r / w | danh sách kèm thành viên, kênh, `silent` / tạo |
| PUT/DELETE | `/ai-teams/:teamId` | settings:w | lưu (thay toàn bộ thành viên) / xoá (không xoá được Bộ điều phối mặc định hay đang có kênh dùng) |
| PUT | `/channels/:channelId/ai-team` | settings:w | `{team_id}`, rỗng là về Bộ điều phối mặc định |

Lỗi kiểm tra trả `400 {"error":"invalid_agent","field":...,"message":...}` để giao diện hiện cạnh ô nhập. Mọi thao tác ghi vào nhật ký hoạt động (prompt chỉ ghi mã băm, không ghi nội dung).

## Giới hạn hiện tại

- Runtime `native` (gọi thẳng nhà cung cấp AI với công cụ của agent, không qua Langflow) chưa có: API từ chối lưu agent `native` cho tới khi máy chủ cài runtime đó.
- Khách chưa xác thực nhắn Zalo cá nhân vẫn dùng bot tài liệu cho khách lạ như trước (chỉ đọc tài liệu công khai); agent áp dụng cho người đã xác thực và nhóm chat.
- Nhóm chat trên Zalo cá nhân chạy không có token quyền ERP (như trước), nên ở đó agent đổi prompt và flow nhưng danh sách công cụ không được máy chủ MCP lọc theo agent.
- `system_prompt` đặt trong metadata của từng kênh OA (không có trên giao diện) bị bỏ qua khi agent đang bật.
- Các nút bấm trong tin nhắn bot đã gửi (chọn mẫu, xem tồn) vẫn được backend trả lời như trước, kể cả khi agent hiện tại không có công cụ sản phẩm.

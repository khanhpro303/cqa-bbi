# Agent chuyên môn và Bộ điều phối

Trước đây mỗi tầng có một bot làm tất cả: một prompt cho khách (bot public), một prompt cho nội bộ (bot private), Zalo OA và Zalo cá nhân dùng chung. Từ bản này, bot có thể chia thành nhiều **agent** chuyên môn (ví dụ bot công nợ, bot sản phẩm/kho/đơn, bot hỗ trợ marketing). Mỗi kênh Zalo OA hoặc Zalo cá nhân trả lời qua một **Bộ điều phối** (team), Bộ điều phối giữ danh sách agent được trả lời qua kênh đó.

## Khái niệm

- **Agent**: mã (`key`, không đổi sau khi tạo), tên, mô tả, **đối tượng** (`customer` khách hàng, `staff` nhân viên nội bộ, `all` cả hai), system prompt, danh sách **công cụ MCP** được gọi, nhãn tài liệu (`knowledge_tags`), runtime và flow Langflow riêng nếu có. Mỗi lần lưu là một phiên bản (giữ 30 phiên bản gần nhất).
- **Bộ điều phối**: tên, **giọng chung** (`persona_prompt`, đặt trước prompt của agent đang trả lời), danh sách agent theo thứ tự, agent mặc định. Có đúng một Bộ điều phối **mặc định**; kênh chưa gán Bộ điều phối nào thì trả lời qua nó. Nút **Đặt làm mặc định** trên thẻ một Bộ điều phối đang bật chuyển cờ mặc định sang nó; Bộ điều phối mặc định cũ sau đó xoá được nếu không kênh nào trỏ tới.
- **Gán kênh**: mỗi kênh `zalo_oa` / `personal_zalo_import` trỏ tới một Bộ điều phối, hoặc để trống để dùng Bộ điều phối mặc định. Nhiều kênh có thể dùng chung một Bộ điều phối (một "bộ mặt" cho nhiều agent, hoặc nhiều bộ mặt cho cùng một nhóm agent).

## Ai trả lời một tin nhắn

1. **Tầng bảo mật vẫn do người nhắn quyết định**, như trước: nhân viên trong whitelist nhắn riêng là tầng `private` (đối tượng `staff`); khách hàng, khách chưa xác thực và mọi nhóm chat là tầng `public` (đối tượng `customer`). Quyền dữ liệu (mã KH, phạm vi ERP) vẫn nằm trong token ký, agent chỉ **thu hẹp** công cụ của tầng đó, không bao giờ mở rộng.
2. Tìm Bộ điều phối của kênh, lấy các agent đang bật phục vụ đối tượng đó.
3. Chọn agent: chỉ có một thì lấy agent đó. Nhiều hơn thì **Bộ điều phối chọn theo nội dung tin** (xem mục dưới); khi chưa cần hỏi hoặc hỏi không được thì dùng agent đang giữ cuộc chat, không có thì agent mặc định của Bộ điều phối nếu hợp, không thì agent đầu danh sách.
4. Không agent nào phục vụ đối tượng đó: bot **im lặng** với người này trên kênh đó, tin nhắn vẫn vào lịch sử chat để nhân viên trả lời. Ví dụ một Zalo cá nhân chỉ có bot công nợ cho khách thì nhân viên nhắn vào sẽ không được bot trả lời. Danh sách Bộ điều phối trả về `silent` để giao diện cảnh báo.
5. Khi không đọc được cấu hình hoặc không tìm thấy Bộ điều phối khả dụng sau khi bật agent, bot để nhân viên xử lý lượt chat.

## Bộ điều phối chọn agent theo nội dung

Chỉ chạy khi agent đang bật và Bộ điều phối của kênh có **từ 2 agent** cho đối tượng của người nhắn. Bộ điều phối chỉ có một agent cho đối tượng đó thì không tốn thêm lượt gọi AI nào.

- **Khi nào hỏi**: Zalo OA hỏi sau bước phân loại tin, chỉ với tin `IN_SCOPE`, và tin `HANDOVER` khi kênh bật Khiếu nại (để khiếu nại đến được agent có `complaint_record`). Tin xã giao (kể cả khi đang có giỏ hàng mở) và tin trả lời câu backend vừa hỏi (chọn kỳ đơn, chọn số) thì ở lại agent hiện tại, không gọi AI. Khách chưa xác thực trên OA (bot tài liệu) cũng được chọn agent. Zalo cá nhân hỏi với mọi tin có chữ gửi cho bot (nhắn riêng, hoặc gọi bot trong nhóm); tin chỉ có ảnh ở lại agent hiện tại.
- **Bộ điều phối đọc gì**: danh sách agent ứng viên (mã, tên, **mô tả**, đánh dấu agent mặc định), agent đang giữ cuộc chat, vài tin gần nhất (Zalo OA, như bước phân loại) và tin mới. Zalo cá nhân giữ lịch sử trong Langflow nên Bộ điều phối chỉ đọc tin mới. Tin của khách được đánh dấu là dữ liệu, không phải chỉ dẫn.
- **Prompt điều phối**: hướng dẫn chung ở AI Engine → Prompt cho các tác vụ AI → "Bộ điều phối: chọn agent trả lời" (`agent_router`). Bộ điều phối nào điền "Prompt điều phối" riêng thì dùng của nó thay cho hướng dẫn chung. Danh sách agent và cách trả kết quả (`{"agent": "mã", "reason": "..."}`) là phần cố định hệ thống tự thêm, không sửa được. Model cho tác vụ này chọn ở ô "Bộ điều phối chọn agent" (Cài đặt → AI, model theo tác vụ); để trống dùng model mặc định của tenant.
- **Giữ agent trong cuộc chat**: agent vừa được Bộ điều phối chọn (hoặc giữ) được nhớ theo cuộc chat (Redis, khoá theo Bộ điều phối + cuộc chat) trong "Giữ agent trong cuộc chat (phút)" kể từ lượt trả lời cuối; 0 là theo thời gian phiên chat. Lượt sau Bộ điều phối được báo agent đang giữ và chỉ đổi khi tin mới rõ ràng là việc của agent khác. Agent mặc định trả lời một tin xã giao đầu cuộc chat thì không được nhớ. Đổi Bộ điều phối của kênh thì các cuộc chat bắt đầu lại.
- **Không cho agent chuyển lượt**: agent đầu tiên được chọn giữ luôn cuộc chat tới khi hết thời gian giữ, không gọi Bộ điều phối nữa. Cuộc chat mới vẫn được chọn agent.
- **Khi lỗi**: Bộ điều phối quá 12 giây, báo lỗi, trả lời không đúng định dạng hoặc nêu agent không có trong danh sách thì agent đang giữ (hoặc agent mặc định) trả lời. Lỗi Bộ điều phối không bao giờ làm bot im lặng.
- **Đổi agent xảy ra trước khi trả lời**: agent được chọn trả lời luôn lượt đó với prompt, flow, công cụ của nó. Không có bước agent trả lời xong rồi chuyển cho agent khác.
- **Nhật ký**: mỗi lượt ghi dòng `[agents] ... coordinator <agent cũ> -> <agent mới> (<lý do>)` với lý do `router` (đổi), `router_kept` (giữ), `router_failed` (lỗi, giữ agent cũ), `sticky` (không cho chuyển lượt).

### Thử Bộ điều phối

Nút **Thử** trên thẻ Bộ điều phối (trang Agents AI, quyền sửa cài đặt) mở màn chạy thử: chọn người nhắn là khách hay nhân viên (nhân viên chỉ chủ và quản trị viên), agent đang giữ cuộc chat, gõ tin và xem Bộ điều phối chọn agent nào, vì sao, trong số agent nào. Bật "Cho agent trả lời thử" thì agent trả lời bằng prompt, flow (hoặc runtime native) và nhãn tài liệu của nó, kèm số token và chi phí ước tính. Bản thử ký token **dry run không có nhóm quyền ERP và không có mã khách**: tìm tài liệu được, dữ liệu ERP và mọi thao tác ghi bị máy chủ MCP từ chối. Không gửi gì vào cuộc chat thật. Chạy trên flow riêng của agent hoặc flow AI Engine mà tenant đã lưu cùng khoá Langflow của tenant; agent cho khách không bao giờ chạy trên flow nội bộ. Mỗi tin gửi xong, agent vừa trả lời thành agent đang giữ cho tin sau (như một cuộc chat), nút "Bắt đầu lại" xoá.

### Chi phí theo agent

Mỗi lượt chatbot Zalo OA ghi mã agent đã trả lời vào nhật ký chi phí AI (`ai_usage_logs.agent_key`). Biểu đồ chi phí ở trang chủ có thêm khung **Theo agent**: chi phí chatbot gộp theo agent (tên agent, agent đã xoá hiện bằng mã), lượt của bot AI Engine gộp vào một cột riêng. Chi phí gọi Bộ điều phối và bước phân loại tin không nằm trong nhật ký này (như trước). Zalo cá nhân chưa ghi chi phí chatbot.

## Agent thay đổi gì trong một lượt chat

- **Prompt**: giọng chung của Bộ điều phối + prompt của agent, thay cho system prompt của AI Engine. Phần ngữ cảnh (trí nhớ chat, giỏ hàng, khiếu nại, hướng dẫn hệ thống) vẫn được nối thêm như trước.
- **Flow**: flow Langflow riêng của agent nếu có, không thì flow của AI Engine cho tầng đó. Agent phục vụ khách không được chạy trên flow nội bộ (API từ chối, và luồng chat cũng chặn).
- **Công cụ**: token ký mang `agent_key` và danh sách công cụ của agent. Máy chủ MCP `/mcp/erp/:tenantId` chỉ liệt kê và chỉ chạy các công cụ đó; gọi công cụ khác nhận lỗi `tool_not_allowed`. Agent không có công cụ nào thì không gọi được công cụ nào.
- **Việc backend làm thay bot** cũng theo công cụ của agent: ghi khiếu nại thành case chỉ khi agent có `complaint_record`; giỏ hàng chỉ khi agent có công cụ `order_cart_*`; tra đơn đang giao trả lời thẳng chỉ khi agent có `lookup_order`; ghi chú tư vấn sản phẩm và hướng dẫn tồn kho chỉ khi agent có công cụ nhóm sản phẩm.
- **Tài liệu**: token mang `knowledge_tags` của agent. Agent có nhãn thì `search_knowledge` chỉ tra tài liệu mang ít nhất một nhãn đó (gắn nhãn ở trang Tài liệu chatbot, `GET /knowledge/tags` liệt kê nhãn đang dùng). Agent không có nhãn tra tài liệu mà tầng bảo mật của lượt chat được đọc.
- **Quy tắc hệ thống nối thêm** (thiếu dữ liệu ERP, tồn kho với khách, tra đơn, chuyển người, ảnh tài liệu, khách chưa xác thực, các phần của Zalo cá nhân) là prompt tác vụ trong AI Engine → Prompt cho các tác vụ AI, dùng chung cho mọi agent. Prompt nào có mã hệ thống đọc (ví dụ `[CQA_REQUEST_HUMAN_REVIEW]`) thì không lưu được nếu xoá mã đó.

## Runtime native (không qua Langflow)

Mỗi agent chọn **runtime**: `langflow` (mặc định, chạy trên flow Langflow như trước) hoặc `native` (backend gọi thẳng model của agent và tự gọi công cụ MCP, không cần flow Langflow).

- **Model**: agent native chọn nhà cung cấp và mã model như ô chọn model của từng tác vụ AI (Cài đặt → AI): `claude`, `gemini`, `openai`, `openrouter`, `9router`. Khoá API và Base URL lấy từ cài đặt AI của tenant cho đúng nhà cung cấp đó. Agent native bắt buộc có system prompt.
- **Một lượt chat**: prompt (giọng chung + prompt agent + ngữ cảnh như với Langflow) và hội thoại gần đây lấy từ trí nhớ chat (tối đa 20 tin, khoảng 10.000 ký tự; trong nhóm chỉ lấy tin của người đang hỏi và câu bot trả lời họ) được gửi cho model cùng danh sách công cụ của agent. Model gọi công cụ, backend chạy công cụ trên máy chủ MCP `/mcp/erp/:tenantId` ngay trong tiến trình, với đúng token agent và token quyền ký của lượt đó (nên vẫn lọc công cụ theo agent, giới hạn dữ liệu theo người nhắn, chặn công cụ ghi khi chạy thử), rồi gửi kết quả lại cho model tới khi model trả lời.
- **Công cụ được đưa cho model**: giao của các công cụ máy chủ MCP liệt kê cho token của lượt đó và công cụ của agent. Lượt không có token agent ERP (ví dụ tenant chưa cài token) thì model trả lời không có công cụ.
- **Tuỳ chọn** (`options`): `max_tool_calls` (1–20, để 0 là 8) giới hạn số lần gọi công cụ trong một lượt; hết lượt thì model phải trả lời bằng thông tin đã có. `temperature` (0–2, Claude tối đa 1); model không nhận temperature (một số model suy luận) thì backend gửi lại không kèm temperature.
- **Phần còn lại của lượt chat giữ nguyên**: xử lý `[RICH_MESSAGE_SENT]`, chuyển người hỗ trợ, ảnh tài liệu/sản phẩm, bỏ mã case, ghi chi phí AI (token cộng dồn mọi lần gọi model trong lượt, tính theo model thật của agent).
- **Thời gian**: một lượt native (mọi lần gọi model và công cụ, kể cả thử lại) tối đa 2 phút.
- **Lỗi**: thiếu khoá API, mã model sai, khoá bị từ chối thì bot trả lời câu "chưa trả lời được" ngay, không thử lại; lỗi tạm thời (quá tải, mạng) thì thử lại như với Langflow.
- **Phạm vi**: runtime native chạy cho Zalo OA (chat riêng và nhóm). Trên Zalo cá nhân, trí nhớ hội thoại vẫn nằm trong Langflow (lịch sử chat chỉ được nhập về sau vài phút), nên agent native ở đó vẫn trả lời qua flow Langflow của tầng với prompt và công cụ của agent.

## Bật agent và chuyển prompt cũ

- **Công tắc** `ai_agents_enabled` (trang **Agents AI**, chỉ chủ sở hữu hoặc quản trị viên đổi được). Tắt (mặc định): mọi thứ chạy như trước bằng prompt AI Engine.
- **Lần bật đầu tiên** chép hai system prompt của AI Engine thành hai agent: **Trợ lý khách hàng** (`tro-ly-khach-hang`, đối tượng khách) và **Trợ lý nội bộ** (`tro-ly-noi-bo`, đối tượng nhân viên), cả hai có mọi công cụ và đi theo flow của AI Engine, nằm trong Bộ điều phối **Mặc định** không có giọng chung. Nhờ vậy ngay sau khi bật, khách thấy y như cũ: cùng prompt (chép nguyên văn), cùng flow, cùng công cụ. Nếu đã xoá hết agent rồi chép lại, hai agent mới được thêm vào Bộ điều phối mặc định đang có (giữ giọng chung, tên), không tạo Bộ điều phối thứ hai.
- Có thể **chép trước mà chưa bật** (`POST /settings/ai-agents/migrate`) để chia prompt thành các agent rồi mới bật.
- Khi agent đang bật, hai prompt chatbot ở trang AI Engine là **bản gốc để quay lại**: lưu trang AI Engine không đổi và không xoá chúng. Tắt công tắc là quay về bot cũ ngay.
- Prompt AI Engine đổi sau khi đã chép (ví dụ sửa lúc agent đang tắt): `GET /settings/ai-agents` trả `drift` là các agent lệch với bản gốc; `POST /settings/ai-agents/migrate` với `{"refresh": true}` chép lại vào các agent đó (ghi đè chỉnh sửa trong agent).

## Trang Agents AI

Menu **Agents AI** (quyền settings) có công tắc ở đầu trang và ba tab:

- **Công tắc**: bật, tắt agent (hỏi lại trước khi đổi, cảnh báo Bộ điều phối nào sẽ im lặng với ai). Chưa có agent thì có nút chép prompt AI Engine thành agent mà chưa bật. Agent lệch với prompt gốc (`drift`) hiện thành thông báo kèm nút **Chép lại từ AI Engine** (hỏi lại vì ghi đè prompt agent; bản cũ vẫn còn trong lịch sử).
- **Agent chuyên môn**: danh sách agent (đối tượng, số công cụ, flow riêng hay flow AI Engine, nằm trong Bộ điều phối nào). Cửa sổ sửa agent có tên, mã (chỉ đặt lúc tạo, tự sinh từ tên), mô tả, đối tượng, bật/tắt, system prompt, danh sách công cụ MCP theo nhóm (đánh dấu công cụ ghi dữ liệu), runtime, flow Langflow riêng, nhãn tài liệu. Tab **Lịch sử phiên bản** so prompt của từng bản với bản đang soạn và đưa một bản cũ vào bản soạn (bấm Lưu mới áp dụng). Agent đang nằm trong Bộ điều phối thì không xoá được.
- **Bộ điều phối**: mỗi thẻ có giọng chung, agent theo thứ tự (ngôi sao là agent mặc định), ai trả lời khách và nhân viên, cảnh báo im lặng, các kênh đang dùng. Cửa sổ sửa cho đổi thứ tự, bật/tắt từng agent trong bộ, thêm/gỡ agent và xem ngay ai sẽ trả lời. Phần "Tự chọn agent theo nội dung" có prompt điều phối riêng của Bộ điều phối, số phút giữ agent và công tắc không cho chuyển lượt (xem mục Bộ điều phối chọn agent). Nút **Thử** mở màn chạy thử.
- **Kênh**: chọn Bộ điều phối cho từng kênh Zalo OA và Zalo cá nhân (hoặc theo mặc định), kèm agent sẽ trả lời khách và nhân viên trên kênh đó.

Khi agent đang bật, trang **AI Engines** hiện hai prompt chatbot ở chế độ chỉ xem kèm đường dẫn sang Agents AI.

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
| POST | `/ai-agents/test` | settings:w (nhân viên: owner/admin), 12 lần/phút | `{team_id, audience: customer\|staff, message, current_agent?, reply?, session_id?}` → `{agent, reason, router_reason, routed, candidates}`, kèm `{reply, runtime, usage, session_id}` khi `reply`; `{silent: true}` khi không agent nào phục vụ đối tượng |
| GET/POST | `/ai-teams` | settings:r / w | danh sách kèm thành viên, kênh (`ai_team_id` rỗng là kênh theo mặc định), `silent` / tạo |
| PUT/DELETE | `/ai-teams/:teamId` | settings:w | lưu (thay toàn bộ thành viên) / xoá (không xoá được Bộ điều phối mặc định hay đang có kênh dùng) |
| POST | `/ai-teams/:teamId/default` | settings:w | đặt Bộ điều phối (đang bật) làm mặc định, bỏ cờ ở bộ cũ |
| PUT | `/channels/:channelId/ai-team` | settings:w | `{team_id}`, rỗng là về Bộ điều phối mặc định |
| GET | `/analytics/ai-cost?group_by=agent` | như trang chủ | chi phí chatbot theo agent |

Lỗi kiểm tra trả `400 {"error":"invalid_agent","field":...,"message":...}` để giao diện hiện cạnh ô nhập. Mọi thao tác ghi vào nhật ký hoạt động (prompt chỉ ghi mã băm, không ghi nội dung).

## Giới hạn hiện tại

- Agent `native` trên Zalo cá nhân vẫn chạy qua flow Langflow của tầng (xem Runtime native).
- Khách chưa xác thực nhắn Zalo cá nhân vẫn dùng bot tài liệu cho khách lạ như trước (chỉ đọc tài liệu công khai); agent áp dụng cho người đã xác thực và nhóm chat.
- Nhóm chat trên Zalo cá nhân chạy không có token quyền ERP (như trước), nên ở đó agent đổi prompt và flow nhưng danh sách công cụ không được máy chủ MCP lọc theo agent.
- `system_prompt` đặt trong metadata của từng kênh OA (không có trên giao diện) bị bỏ qua khi agent đang bật.
- Zalo cá nhân: Bộ điều phối chỉ đọc tin mới (lịch sử ở Langflow), và lượt chatbot Zalo cá nhân chưa ghi chi phí AI nên chưa có trong biểu đồ theo agent.
- Agent chưa tự chuyển lượt giữa câu trả lời (không có mã chuyển agent trong câu trả lời); việc đổi agent do Bộ điều phối làm trước khi trả lời.
- Các nút bấm trong tin nhắn bot đã gửi (chọn mẫu, xem tồn) vẫn được backend trả lời như trước, kể cả khi agent hiện tại không có công cụ sản phẩm.

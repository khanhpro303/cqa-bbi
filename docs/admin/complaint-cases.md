# Khiếu nại thành case (Insights khách → Khiếu nại)

Khi khách báo một vấn đề cần nhân viên xử lý (hàng lỗi, giao sai/thiếu/chậm, sai tiền, lỗi chứng từ, phàn nàn, đòi gặp nhân viên), bot ghi lại thành một **case** để nhân viên theo dõi tới khi xong. Khách không bao giờ thấy mã case hay chữ "ticket": bot chỉ nói đã ghi nhận và chuyển bộ phận phụ trách.

## Luồng

1. **Khách gửi ảnh**: Zalo gửi sự kiện `user_send_image` / `user_send_group_image`. Worker tải ảnh từ CDN của Zalo (chỉ host `*.zdn.vn`, `*.zadn.vn`, `*.zalo.me`...), model AI của tenant đọc ảnh bằng prompt **Đọc ảnh khách gửi** (AI Engine → Prompt cho các tác vụ AI → Chatbot). Bot chính nhận nội dung dưới dạng `[Ảnh khách gửi: ...]`. Ảnh và nội dung đọc được lưu vào tin nhắn (bảng `messages`, cột `attachments`).
2. **Phân loại**: tin khiếu nại vẫn được bộ phân loại gắn nhãn `HANDOVER`. Khi kênh bật **Ghi khiếu nại thành case** (mặc định **tắt**) và AI Engine có system prompt, tin đó **đi tiếp vào bot** thay vì trả câu chuyển nhân viên soạn sẵn. Kênh chưa bật thì vẫn chạy như cũ.
3. **Bot gọi công cụ MCP `complaint_record`** (title, detail, evidence, type, severity). Cách bot dùng công cụ và trả lời khách nằm ở prompt **Bot ghi nhận khiếu nại và trả lời khách** (nhóm Khiếu nại), được nối vào system prompt cùng danh sách vấn đề khách đang được theo dõi.
4. **Decision model**: backend so vấn đề vừa báo với các case chưa xong (và case xử lý xong trong 14 ngày) của cùng cuộc chat hoặc cùng mã KH, bằng prompt **Quyết định: vấn đề cũ hay vấn đề mới**:
   - vấn đề mới → mở case mới, báo nhóm cảnh báo;
   - cùng vấn đề đang mở → thêm lượt "khách hỏi lại" vào case cũ, bot an ủi khách (nhắc nhân viên tối đa 1 lần mỗi 2 giờ);
   - cùng vấn đề đã xử lý xong → mở lại case, báo nhóm cảnh báo.
   Model lỗi thì mở case mới (trùng còn hơn mất khiếu nại). Các báo cáo của cùng cuộc chat hoặc cùng mã KH được xử lý lần lượt (khoá trong MySQL), nên hai tin đến cùng lúc không mở hai case cho một vấn đề.
5. **Lưới an toàn**: sau mỗi lượt, nếu tin `HANDOVER` mà bot không ghi case nào, hoặc bot ghi case nhưng cảnh báo không gửi được, nhóm cảnh báo nhận cảnh báo handover như trước (chỉ tính case của chính người nhắn trong lượt đó). Khi đó công cụ cũng dặn bot không hứa nhân viên sẽ liên hệ ngay. Khi tóm tắt phiên chat 1:1, vấn đề có `needs_case: true` (quy tắc ở prompt **Quy tắc phát hiện vấn đề và cảnh báo**) cũng đi qua decision model: phiên bot đã ghi thì chỉ gắn vào case cũ. Chỉ tạo case từ tóm tắt khi mọi tin của khách trong phiên đều có chữ ký Zalo hợp lệ. Phiên nhóm thì không tạo case từ tóm tắt (tóm tắt không biết vấn đề là của ai trong nhóm); vấn đề nặng vẫn vào cảnh báo phiên như cũ. Vấn đề chỉ bị bỏ khỏi cảnh báo phiên khi nhân viên đã được báo qua case.
6. **Không lộ mã**: bot không nhận mã case. Mã ticket, số phiếu viết trong tiêu đề hay **Cập nhật cho khách** bị lọc trước khi bot đọc, và câu trả lời của bot cũng được lọc lần cuối trước khi gửi khách.
7. **Khách trong nhóm của người khác**: khách đã duyệt nhưng không phải thành viên CRM của nhóm Zalo đang chat thì case của họ là của riêng họ (chỉ theo Zalo ID của họ), không xem hay gộp vào case của nhóm. Case đó được đánh dấu riêng tư: nhóm cũng không thấy nó dù trùng mã KH. Vì câu trả lời đi vào cả nhóm, bot không được đọc danh sách case, tiêu đề hay cập nhật của nhân viên cho người này, chỉ biết là đã ghi nhận.
9. **Tin gửi lại**: nếu một tin bị xử lý lại (Zalo gửi lại, lỗi mạng), bot không tính là khách hỏi lại lần nữa.
8. **Chỉ webhook đã xác thực**: bot chỉ ghi case khi tin nhắn có chữ ký Zalo hợp lệ (cần `ZALO_OA_SECRET_KEY`). Có secret thì webhook mặc định ở chế độ `enforce`.

Cảnh báo case gửi vào **nhóm cảnh báo handover** của kênh (Kênh → Nhóm Zalo), hoặc vào nhóm của luồng xử lý nếu kênh có chia luồng (xem dưới).

## Luồng xử lý (bảo hành, khách hàng, sản phẩm, marketing…)

Kênh → Nhóm Zalo, dưới công tắc **Ghi khiếu nại thành case**: thêm các luồng xử lý (bấm **Dùng luồng gợi ý** để có sẵn Bảo hành, Chăm sóc khách hàng, Sản phẩm, Marketing rồi sửa tên/mô tả). Mỗi luồng có:

- **Tên** và **Mô tả**: model quyết định (cùng lần gọi quyết định vấn đề cũ hay mới) đọc tên và mô tả để xếp vấn đề mới vào đúng một luồng. Mô tả càng cụ thể càng xếp đúng.
- **Nhóm nhận báo**: nhóm Zalo (CRM) nhận cảnh báo của luồng đó. Để trống thì báo vào nhóm cảnh báo handover. Nhóm của luồng bị xoá hoặc chưa liên kết Zalo thì cũng báo vào nhóm handover.

Case lưu luồng (`stream`, `stream_name`) và hiện trên danh sách và chi tiết case; cảnh báo có thêm dòng `Luồng: …`. Khách hỏi lại vấn đề cũ thì case giữ luồng ban đầu (case cũ chưa có luồng sẽ nhận luồng lần này). Model không chọn được luồng (hoặc lỗi) thì case không có luồng và báo vào nhóm handover. Không khai luồng nào thì mọi thứ chạy như trước.

Cấu hình nằm trong metadata kênh, khoá `complaint_streams`: `[{"key":"bao_hanh","name":"Bảo hành","description":"hàng lỗi, đổi trả","alert_group_id":"<id nhóm CRM>"}]`. `key` được tạo một lần từ tên và giữ nguyên khi đổi tên.

## Model AI: OpenRouter

Cài đặt → Cấu hình AI → Nhà cung cấp **OpenRouter (model free)**: dán API key OpenRouter, chọn model trong danh sách model free (id đuôi `:free`, lấy trực tiếp từ OpenRouter; model có ghi "đọc ảnh" mới đọc được ảnh khách gửi) hoặc gõ id model bất kỳ. Dùng chung code OpenAI (Chat Completions tại `https://openrouter.ai/api/v1`). Model này dùng cho mọi tác vụ AI nhỏ của tenant: quyết định case và luồng, đọc ảnh, tóm tắt phiên, phân loại ý định. Model free thường bị giới hạn lượt và có thể chậm; nếu đọc ảnh là quan trọng thì chọn model có vision.

## Nhân viên xử lý

Trang **Insights khách** (trước là "Trí nhớ khách"), tab **Khiếu nại**: lọc theo trạng thái (Mới, Đang xử lý, Đã xử lý, Đã đóng), loại, mức độ. Mở một case để:

- xem chi tiết, lời khách, ảnh khách gửi (kèm nội dung AI đọc được), lịch sử, và mở phiên chat gốc;
- đổi trạng thái, mức độ, loại, người phụ trách;
- ghi **Cập nhật cho khách**: bot được đọc nội dung này để báo khách khi khách hỏi lại;
- ghi cách đã xử lý và ghi chú nội bộ.

Hồ sơ khách (tab Hồ sơ khách) cũng liệt kê các case của khách đó.

## Bật lên sau khi deploy

1. Deploy bản mới (bảng `customer_cases`, `customer_case_events` được tạo tự động).
2. Trong Langflow, trên node **MCP Tools** của cả hai flow v2, bấm làm mới danh sách tool để agent thấy `complaint_record`.
3. AI Engine phải có system prompt (Ext và Int) thì phần hướng dẫn khiếu nại mới được nối vào. Không có system prompt thì kênh chạy như khi tắt.
4. Kênh → Nhóm Zalo: chọn nhóm cảnh báo handover (và nhóm cho từng luồng nếu chia luồng), rồi **bật** công tắc **Ghi khiếu nại thành case** (mặc định tắt). Công tắc được kiểm tra cả khi bot gọi công cụ: tắt rồi thì bot không ghi case được nữa.
5. Ảnh chỉ được tải từ máy chủ ảnh của Zalo (https, không trỏ về địa chỉ nội bộ); ảnh từ nơi khác không được lưu hay hiện cho nhân viên. Model AI của tenant phải đọc được ảnh (OpenAI gpt-4o-mini/gpt-4o, Gemini, Claude có vision). Nếu không đọc được, bot nhận `[Ảnh khách gửi: chưa đọc được nội dung ảnh]` và ảnh vẫn được gắn vào case.

## Bảng

- `customer_cases`: một dòng một case (cuộc chat, mã KH, loại, mức độ, tiêu đề, chi tiết, lời khách, ảnh, trạng thái, người phụ trách, cập nhật cho khách, cách xử lý, số lần khách hỏi lại).
- `customer_case_events`: lịch sử (mở, khách hỏi lại, mở lại, gắn từ tóm tắt, đổi trạng thái, cập nhật cho khách, phân công, ghi chú).

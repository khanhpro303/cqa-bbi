# Khiếu nại thành case (Insights khách → Khiếu nại)

Khi khách báo một vấn đề cần nhân viên xử lý (hàng lỗi, giao sai/thiếu/chậm, sai tiền, lỗi chứng từ, phàn nàn, đòi gặp nhân viên), bot ghi lại thành một **case** để nhân viên theo dõi tới khi xong. Khách không bao giờ thấy mã case hay chữ "ticket": bot chỉ nói đã ghi nhận và chuyển bộ phận phụ trách.

## Luồng

1. **Khách gửi ảnh**: Zalo gửi sự kiện `user_send_image` / `user_send_group_image`. Worker tải ảnh từ CDN của Zalo (chỉ host `*.zdn.vn`, `*.zadn.vn`, `*.zalo.me`...), model AI của tenant đọc ảnh bằng prompt **Đọc ảnh khách gửi** (AI Engine → Prompt cho các tác vụ AI → Chatbot). Bot chính nhận nội dung dưới dạng `[Ảnh khách gửi: ...]`. Ảnh và nội dung đọc được lưu vào tin nhắn (bảng `messages`, cột `attachments`).
2. **Phân loại**: tin khiếu nại vẫn được bộ phân loại gắn nhãn `HANDOVER`. Khi kênh bật **Ghi khiếu nại thành case** (mặc định bật), tin đó **đi tiếp vào bot** thay vì trả câu chuyển nhân viên soạn sẵn.
3. **Bot gọi công cụ MCP `complaint_record`** (title, detail, evidence, type, severity). Cách bot dùng công cụ và trả lời khách nằm ở prompt **Bot ghi nhận khiếu nại và trả lời khách** (nhóm Khiếu nại), được nối vào system prompt cùng danh sách vấn đề khách đang được theo dõi.
4. **Decision model**: backend so vấn đề vừa báo với các case chưa xong (và case xử lý xong trong 14 ngày) của cùng cuộc chat hoặc cùng mã KH, bằng prompt **Quyết định: vấn đề cũ hay vấn đề mới**:
   - vấn đề mới → mở case mới, báo nhóm cảnh báo;
   - cùng vấn đề đang mở → thêm lượt "khách hỏi lại" vào case cũ, bot an ủi khách (nhắc nhân viên tối đa 1 lần mỗi 2 giờ);
   - cùng vấn đề đã xử lý xong → mở lại case, báo nhóm cảnh báo.
   Model lỗi thì mở case mới (trùng còn hơn mất khiếu nại).
5. **Lưới an toàn**: nếu tin `HANDOVER` mà bot không ghi case nào, nhóm cảnh báo vẫn nhận cảnh báo handover như trước. Khi tóm tắt phiên, vấn đề có `needs_case: true` (quy tắc ở prompt **Quy tắc phát hiện vấn đề và cảnh báo**) cũng đi qua decision model: phiên bot đã ghi thì chỉ gắn vào case cũ, không báo lại.

Cảnh báo case gửi vào **nhóm cảnh báo handover** của kênh (Kênh → Nhóm Zalo).

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
3. AI Engine phải có system prompt (Ext và Int) thì phần hướng dẫn khiếu nại mới được nối vào.
4. Kênh → Nhóm Zalo: chọn nhóm cảnh báo handover, kiểm tra công tắc **Ghi khiếu nại thành case** đang bật.
5. Model AI của tenant phải đọc được ảnh (OpenAI gpt-4o-mini/gpt-4o, Gemini, Claude có vision). Nếu không đọc được, bot nhận `[Ảnh khách gửi: chưa đọc được nội dung ảnh]` và ảnh vẫn được gắn vào case.

## Bảng

- `customer_cases`: một dòng một case (cuộc chat, mã KH, loại, mức độ, tiêu đề, chi tiết, lời khách, ảnh, trạng thái, người phụ trách, cập nhật cho khách, cách xử lý, số lần khách hỏi lại).
- `customer_case_events`: lịch sử (mở, khách hỏi lại, mở lại, gắn từ tóm tắt, đổi trạng thái, cập nhật cho khách, phân công, ghi chú).

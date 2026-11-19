# Trợ lý trong nhóm khách

Bật trong trang kênh Zalo OA: **Sửa kênh → Trợ lý trong nhóm khách**. Mặc định tắt.
Chỉ áp dụng cho nhóm Zalo; chat 1:1 vẫn chạy theo phiên như cũ.

## Bot làm gì khi bật

- **Không cần từ khoá.** Tin đầu tiên trong nhóm mở phiên mà không gửi lời chào.
  Hết thời gian chờ thì phiên đóng mà không nhắn gì. Từ khoá mở/kết thúc phiên
  vẫn dùng được.
- **Đọc cả nhóm, chỉ lên tiếng khi cần.** Mỗi tin trong nhóm đi qua bộ lọc
  "Trợ lý nhóm: khi nào bot lên tiếng" kèm khoảng 12 tin gần nhất. Mọi người nói
  chuyện với nhau thì bot im. Có người hỏi bot, hỏi tồn kho, giá, đơn, công nợ,
  hoặc hỏi tiếp câu bot vừa trả lời thì bot trả lời. Khiếu nại thì chuyển nhân viên
  như trước.
- **Một cuộc hội thoại chung cho cả nhóm.** Bot nhớ các câu đã trả lời trong nhóm,
  không còn tách theo từng người. Các tin bot không trả lời được gửi kèm ở đầu câu
  hỏi kế tiếp trong thẻ `<tin_nhom>`, mỗi dòng ghi giờ, tên và vai trò:
  `[10:31] Minh (khách): đơn hôm qua chưa về`. Câu hỏi có dạng
  `[Hùng (khách)]: còn đơn kia thì sao?`, nên bot biết ai đang hỏi và gọi đúng tên.
- Vai trò: `khách` (khách đã duyệt), `nhân viên` (nhân viên trong whitelist),
  `chưa xác thực` (người trong nhóm chưa liên kết tài khoản; bot đọc tin của họ
  nhưng không trả lời họ).

## Không đổi

- Quyền tra cứu vẫn ký riêng cho từng người gửi, trong mã KH của nhóm.
- Menu số, câu hỏi kỳ công nợ, giỏ hàng, đơn nháp vẫn của riêng từng người. Đơn nháp
  chỉ người tạo chốt được bằng "1".
- Người có giỏ hàng đang dở: tin của họ vẫn được coi là đang đặt hàng và bot trả lời.

## Prompt sửa được trong AI Engine

- **Trợ lý nhóm: khi nào bot lên tiếng**: quyết định trả lời hay im lặng.
- **Trợ lý nhóm: cách trả lời trong nhóm**: nối vào system prompt của bot. Cần có
  system prompt trong AI Engine; nếu để trống, flow dùng prompt riêng của nó và
  phần này không được nối.

## Chi phí và lưu ý

- Mỗi tin trong nhóm tốn một lần gọi AI nhỏ để phân loại (dùng nhà cung cấp AI của
  tenant). Khi không gọi được AI, bot chỉ trả lời các câu có từ khoá nghiệp vụ rõ
  ràng (mã sản phẩm, tồn kho, giá, đơn, công nợ) và im với phần còn lại.
- Tin nhắn bot tự gửi ngoài câu trả lời của AI (menu, giỏ hàng, bản xác nhận đơn)
  chưa được đưa vào ngữ cảnh chung.
- Nên báo khách rằng trợ lý đọc tin trong nhóm.
- Thử trên một kênh hoặc một nhóm trước rồi mới bật rộng.

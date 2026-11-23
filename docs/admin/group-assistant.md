# Trợ lý trong nhóm khách

Trong danh sách **Kênh chat**, bấm nút bút chì trên thẻ kênh Zalo OA. Mở tab
**Nhóm Zalo**, bật **Bot là trợ lý trong nhóm Zalo của khách** và bấm **Lưu cài đặt**.
Chế độ này mặc định tắt.
Chỉ áp dụng cho nhóm khách: nhóm Zalo đã tạo trong CRM và có mã KH. Chat 1:1 và
các nhóm khác của OA (nhóm nhân viên, nhóm cảnh báo, nhóm chưa gắn mã KH) vẫn chạy
theo phiên có từ khoá như cũ.

## Bot làm gì khi bật

- **Không cần từ khoá.** Tin đầu tiên trong nhóm mở phiên mà không gửi lời chào.
  Hết thời gian chờ thì phiên đóng mà không nhắn gì. Từ khoá vẫn dùng được: gõ từ
  khoá mở phiên khi chưa có phiên thì bot gửi lời chào như cũ; gõ từ khoá kết thúc
  thì bot chào tạm biệt, đóng phiên và quên cuộc hội thoại chung, tin kế tiếp mở
  phiên mới.
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

## Nhân viên trong nhóm khách

Công tắc **Nhân viên trong nhóm khách: bot chỉ trả lời khi được gọi** (tab
**Nhóm Zalo**, mặc định bật) áp dụng cho nhóm khách (nhóm CRM có mã KH), cả khi
bật chế độ trợ lý nhóm lẫn khi dùng phiên có từ khoá.

- **Nhân viên nói với khách thì bot im.** Tin của nhân viên (người trong
  whitelist) không qua bộ lọc AI, nên không tốn lượt gọi AI. Ở chế độ trợ lý nhóm,
  tin đó vẫn được ghi vào `<tin_nhom>`, nên câu trả lời sau của bot biết nhân viên
  đã nói gì với khách.
- **Gọi bot bằng tên.** Tin có tên gọi bot đứng riêng thành một từ thì bot trả
  lời ngay: `Bull ơi FF399 còn không`, `@Bull tra đơn hôm qua`, `nhờ bot tra công
  nợ`. Tên gọi đặt ở ô **Tên gọi bot trong nhóm**, cách nhau bằng `;` (để trống thì
  dùng `bot`). Gõ đúng từ khoá mở phiên (`Chào Bull`) cũng tính là gọi bot.
- **Hỏi tiếp không cần gọi lại.** Sau khi bot trả lời một nhân viên, trong 5
  phút tin tiếp của chính nhân viên đó đi qua bộ lọc như bình thường, nên trả lời
  câu bot vừa hỏi ("kho HCM", "lấy 10 cái") vẫn được. Nhân viên đang có menu số
  hoặc giỏ hàng dở cũng vậy. Nhân viên khác trong nhóm vẫn phải gọi bot.
- **Từ khoá kết thúc** (`Bye Bull`) vẫn đóng phiên của cả nhóm như cũ.
- Tin của khách và người chưa xác thực không đổi. Nhóm nhân viên, nhóm cảnh báo,
  nhóm chưa có mã KH và chat 1:1 không đổi.
- Tắt công tắc để quay lại cách cũ: tin nhân viên đi qua bộ lọc như tin khách.

## Không đổi

- Quyền tra cứu vẫn ký riêng cho từng người gửi, trong mã KH của nhóm.
- Menu số, câu hỏi kỳ công nợ, giỏ hàng, đơn nháp vẫn của riêng từng người. Đơn nháp
  chỉ người tạo chốt được bằng "1".
- Người có giỏ hàng đang dở: tin của họ vẫn được coi là đang đặt hàng và bot trả lời.

## Prompt sửa được trong AI Engine

- **Trợ lý nhóm: khi nào bot lên tiếng**: quyết định trả lời hay im lặng. Mặc
  định có quy tắc: tin của nhân viên chỉ IN_SCOPE khi họ đang nói với bot. Tenant
  đã sửa prompt này trong AI Engine thì cần thêm quy tắc đó hoặc bấm khôi phục mặc
  định.
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

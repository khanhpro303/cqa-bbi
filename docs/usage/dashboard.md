# Dashboard

Dashboard là trang tổng quan khi bạn vào 1 công ty. Hiển thị các chỉ số quan trọng và hoạt động gần đây.

![Dashboard](/screenshots/dashboard.png)

## Thẻ thống kê

| Thẻ | Ý nghĩa |
|-----|---------|
| **Tổng cuộc hội thoại** | Tổng số cuộc chat đã đồng bộ |
| **Vi phạm QC trong kỳ** | Số bản ghi vi phạm QC được tạo trong khoảng ngày đang chọn; không tính đánh giá PASS/SKIP, nhãn phân loại hay insight |
| **Công việc đang chạy** | Số công việc đang thực thi |
| **Kênh hoạt động** | Số kênh chat đang kết nối |

## Thống kê kênh

Card vi phạm đếm từng vi phạm, không phải số cuộc hội thoại. Một cuộc hội thoại có thể có nhiều vi phạm. Kết quả QC được tạo thêm khi chạy lại vẫn được tính theo thời điểm phân tích; đây không phải số vấn đề chưa xử lý. Khoảng ngày được hiểu theo múi giờ công ty (mặc định Việt Nam), bao gồm toàn bộ ngày kết thúc. Preset 7/28 ngày bao gồm hôm nay.

Hiển thị số cuộc hội thoại theo từng loại kênh:
- Zalo OA: số cuộc chat
- Facebook: số cuộc chat
- Tổng tin nhắn
- Chi phí AI hôm nay (VND)

## Hoạt động gần đây

Danh sách các cảnh báo QC và kết quả phân loại mới nhất:

**Cảnh báo QC:**
- Badge mức độ (Nghiêm trọng / Cần cải thiện)
- Tên quy tắc vi phạm
- Bằng chứng
- Bấm vào để xem chi tiết cuộc chat

**Kết quả phân loại:**
- Tên khách hàng
- Nhãn phân loại
- Bấm vào để xem chi tiết

## Chi phí AI

- **Chi phí hôm nay**: Tổng chi phí AI trong ngày (VND)
- **Chi phí tháng này**: Tổng chi phí trong tháng
- **Bảng chi phí 7 ngày gần nhất**: Ngày, số token (input + output), chi phí VND

## Biểu đồ

### Tin nhắn theo ngày
Biểu đồ đường hiển thị 3 chỉ số theo thời gian:
- Tổng tin nhắn
- Số cuộc hội thoại
- Số tin nhắn trả lời từ nhân viên

### Chi phí theo ngày
Biểu đồ vùng hiển thị xu hướng chi phí AI theo thời gian.

## Bộ lọc thời gian

Bạn có thể lọc Dashboard theo khoảng thời gian:
- Hôm nay
- 7 ngày
- 28 ngày
- Tháng này
- Quý này
- Năm nay
- Tùy chỉnh (chọn ngày bắt đầu/kết thúc)

## Trạng thái hệ thống

Hiển thị trạng thái các dịch vụ:
- API Server
- Database
- Scheduler

Xanh = bình thường, Đỏ = có lỗi.

## Dữ liệu demo

Nếu công ty chưa có dữ liệu, Dashboard sẽ hiển thị banner mời import dữ liệu demo. Xem [Dữ liệu demo](/admin/demo-data) để biết thêm.

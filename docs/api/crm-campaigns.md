# CRM Campaigns API — Đặc tả cho backend (Chiến dịch CRM)

> Trạng thái: **chưa hiện thực**. Frontend (tab "Chiến dịch CRM" trong `frontend/src/views/CRM.vue`)
> hiện chạy bằng mock (`frontend/src/components/crm-campaigns/mockCampaigns.ts`).
> Khi backend hoàn tất các endpoint dưới đây, chỉ cần thay phần thân các hàm trong
> `mockCampaigns.ts` bằng `api.get/post/put/delete` — chữ ký giữ nguyên.

## Bối cảnh & nền tảng tái dùng

- **Nhóm GMF** = `CRMGroup` (`backend/db/models/crm_group.go`); gửi nhóm qua
  `ZaloOAAdapter.SendGroupMessage(ctx, group.ZaloGroupID, content)` (`backend/channels/zalo_oa.go`).
- **Lập lịch**: tái dùng scheduler `gocron` (`backend/engine/scheduler.go`) + pattern lịch của
  `Job` (`OutputCron` / `OutputAt`, `backend/db/models/job.go`). **Mỗi `segment` → 1 entry scheduler.**
- **Chunk text** 1.800 runes đã có sẵn (`chunkMessageText`, `zaloMaxTextRunes`). Không cần làm lại.
- Routes đặt dưới nhóm CRM hiện hữu: `backend/api/router.go:220-247`.

## Mô hình dữ liệu (đề xuất)

```
Campaign        { id, tenant_id, name, description, channel_id, status,
                  message_text, message_link, message_image_attachment_id,
                  sent_this_month (computed), created_at, updated_at }
CampaignSegment { id, campaign_id, group_id, schedule_kind('recurring'|'once'),
                  cron, run_at, next_run_at, last_run_at }
CampaignRun     { id, campaign_id, segment_id, started_at, sent_count,
                  fail_count, status('running'|'success'|'error') }
```
`status` của Campaign: `draft | active | paused | done`.

## Endpoints

Base: `/api/v1/tenants/:tenantId/crm/campaigns`

| Method | Path | Mục đích |
|---|---|---|
| GET | `/` | Liệt kê campaign + `next_run_at`, `sent_this_month` |
| POST | `/` | Tạo (body kèm `segments[]`) → trả Campaign vừa tạo (status `draft`) |
| PUT | `/:id` | Cập nhật (thay toàn bộ `segments[]`) |
| DELETE | `/:id` | Xoá campaign + segment + huỷ entry scheduler |
| POST | `/:id/activate` | Đặt `active`, đăng ký scheduler cho từng segment |
| POST | `/:id/pause` | Đặt `paused`, gỡ entry scheduler |
| POST | `/:id/send` | Gửi NGAY tất cả segment (bỏ qua lịch) → `{ sent: <int> }` |
| GET | `/:id/runs` | Lịch sử chạy (sent/fail/timestamp) |
| GET | `/stats?month=YYYY-MM` | Số liệu dashboard (xem schema dưới) |
| POST | `/upload-image` | **(PHASE SAU)** upload ảnh → trả `attachment_id` của Zalo |

### POST `/` — body
```json
{
  "name": "Flash Sale 6/6",
  "description": "Ưu đãi 6.6",
  "channel_id": "oa_bbi",
  "message": { "text": "...", "link": "https://...", "image_name": "flash.jpg" },
  "segments": [
    { "group_id": "grp_a", "schedule_kind": "recurring", "cron": "0 9 * * *" },
    { "group_id": "grp_c", "schedule_kind": "once", "run_at": "2026-06-06T20:00:00Z" }
  ]
}
```

### GET `/stats` — response
```json
{
  "campaigns_this_month": 8,
  "messages_sent_this_month": 12480,
  "success_rate": 98.2,
  "upcoming_runs": 5,
  "by_day": [ { "date": "2026-06-01", "sent": 420 } ],
  "recent": [ { "id": "...", "name": "...", "status": "active", "sent": 1240, "fail": 8, "last_run_at": "..." } ]
}
```

## Việc backend phải xử lý

1. **Một segment = một job lịch.** `recurring` → cron; `once` → chạy một lần tại `run_at` rồi
   đánh dấu xong. Khi `activate`/`pause`/`delete` phải đăng ký/gỡ entry tương ứng (tránh job mồ côi —
   xem pattern dọn "stuck runs" trong `scheduler.go`).
2. **Gửi**: với mỗi segment lấy `CRMGroup.ZaloGroupID` theo `group_id` rồi gọi `SendGroupMessage`.
   Ghi `CampaignRun` (sent/fail). Text > 1.800 runes đã được `chunkMessageText` tự chia.
3. **Link**: nối thẳng vào `message_text` (Zalo tự render preview) — không cần xử lý riêng.
4. **Ảnh (PHASE SAU)**: API Zalo v3.0 text **không** đính ảnh trực tiếp. Cần `upload-image` →
   `attachment_id`, rồi gửi bằng message template có attachment. Trước khi có, bỏ qua trường ảnh.
5. **Quyền**: gate theo `meta.perm` như các route CRM khác; chỉ nhân viên có quyền `settings`.
6. **`sent_this_month`** tính từ `CampaignRun` trong tháng hiện tại (không lưu cứng).

## Liên kết frontend

- Tab + nút header: `frontend/src/views/CRM.vue` (`currentTab === 'campaigns'`).
- Component: `frontend/src/components/crm-campaigns/` (List / FormDialog / SegmentScheduleRow /
  MessageComposer / DashboardModal). Bộ chọn lịch lặp dùng lại `frontend/src/components/CronPicker.vue`.

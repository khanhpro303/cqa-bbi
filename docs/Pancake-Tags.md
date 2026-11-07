# Pancake tags as a Messenger label source

Meta returns customer labels only to apps with `pages_messaging` Advanced Access. While CQA does not have it, Pancake tags give the same signal. The Meta label code stays in place; each Facebook Page uses one label source at a time.

## Data flow

1. **Reconcile (ETL).** `pancakelabels.RunScheduled` runs every minute and scans each enabled Page when due: incremental every 6 hours, full every 24 hours. It reads `GET /public_api/v1/pages/{id}/tags` and `GET /public_api/v2/pages/{id}/conversations?type=INBOX&order_by=updated_at` with the Page access token, at most 1 request per second per Page (Pancake allows 5).
2. **Realtime.** With `CQA_PANCAKE_SOCKET_ENABLED=true`, the backend keeps one Phoenix websocket per tenant to `wss://pancake.vn/socket/websocket?vsn=2.0.0`, joins `pages:<page_id>` for every enabled Page and applies `pages:tag_conversations` events. A database lease keeps one listener per tenant across replicas. Heartbeat 30 s, reconnect backoff 1–60 s.
3. **Storage.** `pancake_label_snapshots` keeps the latest tag set per Page + PSID. Events older than the stored Pancake timestamp are ignored.
4. **Journey.** When a Page is enabled and `CQA_MESSENGER_JOURNEY_ENABLED=true`, tag changes write `labels` events (source `pancake_tags`) and `classification` events (source `cqa_pancake_policy`). Labels use the Meta shape `{id, page_label_name}`, so AdBazooka reads them without parser changes. `/api/internal/abz/v2/status` reports `labels.source`.

## Setup

1. Service Quality → label panel → switch to **Pancake** → choose the Page → **Cài đặt**.
2. Save the Page access token (Pancake → Settings → Tools) and the User access token (Pancake → Account → Personal settings; expires within 90 days).
3. Fetch the tag catalog, map tags to Phù hợp / Tiềm năng / Chưa phù hợp, enable, save. Turn Meta labels off for that Page first.
4. Click **Đối soát tag** for the first full scan.

## Limits

- The websocket is the pancake.vn web app protocol, not a public API. It can change without notice; the reconcile scan keeps data correct if it breaks.
- Socket authentication sends the User access token as the `jwt` cookie and as an `access_token` connect parameter. Verify with a real tag change after setup.
- Tokens are encrypted with `ENCRYPTION_KEY`; the API never returns them and logs never print them.

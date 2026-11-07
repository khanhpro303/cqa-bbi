# Canonical Messenger journey and AdBazooka bridge

CQA owns observed referral, label, classification and validated order history. The existing AdBazooka public callback verifies Meta signatures and relays sanitized referrals; CQA still fetches messages and labels through its existing Meta integration. No transcript is exported to AdBazooka. This release does not enable additional paid analysis.

## Release boundary

Default `CQA_MESSENGER_JOURNEY_ENABLED=false`. Additive journey tables are registered in the existing startup migration. Deploying code is different from enabling writes, switching report sources, and accepting real CTM traffic. Record each step separately. Never run schema changes against a shared VPS before measuring capacity and verifying a compatible backup/restore point.

Use `CQA_MESSENGER_RELAY_SECRET` for inbound referrals, separately from `CQA_ABZ_EXPORT_SECRET`; server-configured `CQA_ABZ_EXPORT_TENANT_ID` owns the integration. Values belong in server secrets, not source or logs.

- POST `/api/internal/messenger/referral-events`: atomic batch of sanitized referrals, exact `accepted_event_ids` including previously accepted duplicates. Active Page mapping is required for new writes.
- GET `/api/internal/abz/v2/events?page_ids=...&after=...&limit=...`: immutable journal changes scoped to tenant and Pages.
- GET `/api/internal/abz/v2/snapshot?page_ids=...`: frozen journal range with a replay cursor; replay changes committed after its watermark before marking a replica complete.
- GET `/api/internal/abz/v2/status?page_ids=...`: synchronization and analysis coverage; missing proof means incomplete, never zero orders.
- Existing authenticated conversation detail provides paginated journey history. Disabled routes return 404 so the compact panel stays hidden.
- Legacy `/api/internal/abz/order-events` remains available for rollback.

All integration requests use `X-CQA-Timestamp`, `X-CQA-Request-Id`, `X-CQA-Signature`. GET signing retains the legacy canonical five lines: timestamp, request ID, method, escaped path, exact raw query. POST adds a sixth line containing lowercase SHA256 of the exact request body. HMAC-SHA256 uses the corresponding dedicated secret and accepts a five-minute clock window. A retry uses a fresh request ID and the same stable event IDs.

## History semantics

Identity is tenant + Page + PSID, never a global Facebook user. Label history records observations; changes between polls cannot be reconstructed. Successful unchanged reads refresh freshness but do not create business transitions. Errors keep the last successful labels and expose a stale/error state. Intake labels no longer globally exclude a label from explicit classification mapping; removed old mappings are not automatically restored.

Validated order history is independent of job results. Clearing a job does not erase its orders. Customer/channel/tenant erasure removes identifiers and payloads, and emits opaque tombstones. Erasure deny markers stop retries or old imports from recreating deleted customers. Journal sequence allocation and source/projection changes share one transaction, serialized per tenant.

CQA does not automatically age out this new journal. AdBazooka's 180-day retention uses source activity, never the date of backfill/retry. A current customer label is not automatically the customer's historical classification at purchase.

## Analysis coverage

`/v2/status` counts a conversation as waiting for AI only when it has stored messages and no coverage row matches its current message cursor (count + newest message). Conversations without stored messages are not counted. Readiness needs an active, healthy Messenger insight job, but no longer needs a job run after every 15-minute sync: matching coverage is the proof.

Coverage used to be written only by insights created after journey writes were enabled, so older insights stayed "waiting" while the job saw them as done. Each Messenger insight run now reconciles first, without an AI call: if the latest insight names the newest message it read, that message is still the newest, and every current message was stored before the insight, CQA records coverage (`source=cqa_insight_reconcile`). Otherwise the conversation is added to the AI work list (at most 50 extra conversations per run). At most 1,000 coverage rows are written per run.

## Insight events (ABZ-MSG-BI-01)

Kind `insight` exports one AI insight of a customer as fixed codes and counts. It never carries quotes, reasons, summaries or message text.

| Field | Values |
|---|---|
| `lead_level` | `high`, `medium`, `low`, `spam`, `unknown` |
| `need` | `price`, `advice`, `stock`, `order`, `warranty`, `complaint`, `other`, `unknown` |
| `need_source` | `ai` (field `need` in the insight prompt) or `keyword` (rules over old `intents`, version in `need_rules`) |
| `products` | at most 5 normalized model names, each at most 60 characters |
| `negative_feedback` | categories with negative or mixed feedback: `product`, `price`, `delivery`, `service`, `other` |
| `messages`, `customer_messages` | counts of messages the insight read |
| `first_reply_status` | `answered`, `waiting` or `no_request` for the first customer turn |
| `first_response_seconds`, `replied_within_target` | working seconds of the first reply under the CSKH policy, and whether it met `target_minutes`; null when not answered |

The `need` field is a mandatory prompt section, so tenant-edited prompts ask for it too. It adds no AI call. When it is missing or not a valid code, keyword rules decide (priority: complaint, warranty, order, price, stock, advice, other).

New analyses write the event in the same transaction as the insight (`source=cqa_ai_analysis`). Each Messenger insight run also exports the latest existing insight of up to 500 conversations without one (`source=cqa_insight_backfill`, 0 AI calls). The event ID is derived from tenant, conversation and insight ID, so a retry never duplicates. Erasure blanks the payload like every other event.

`/v2/events` and `/v2/snapshot` return `insight` only to a caller that asks with `?kinds=referral,labels,classification,order,deletion,insight`. Without `kinds`, the legacy five kinds are returned, so an older replica keeps working. `deletion` is always included.

## Labels are independent

`page.complete` in `/v2/status` means messages are current and every conversation with messages has matching AI coverage. Labels sync on their own and stay visible in `page.labels`, but do not block a Page. Reading a customer's labels (`GET /{PSID}/custom_labels`) needs `pages_messaging` Advanced Access; under Standard Access Meta refuses it with Graph code #100 (`unsupported`) for people without an app role. A refused read is retried after 24 hours, or sooner when the conversation gets a new message, instead of on every 15-minute sync.

## Message sync safety

Facebook sync reads every conversation updated since the last successful sync (minus one hour); there is no 100-conversation cap. A sync where any conversation fails is recorded as `error` and keeps `last_sync_at`, so the next run re-reads the same window; already stored messages are deduplicated. The scheduler backs off by the channel interval after a failed attempt. `/v2/status` reports such a Page as messages incomplete until a sync succeeds.

## Resumable CQA backfill

Deploy the additive schema and enable CQA journaling first. Run imports with the same server environment but **never** invoke a broad automatic schema migration from the importer. The command defaults to a read-only dry run:

```
go run ./cmd/messenger-journey-backfill --tenant TENANT --kind referrals --limit 100
go run ./cmd/messenger-journey-backfill --tenant TENANT --kind referrals --limit 100 --apply --checkpoint /secure/path/referrals.json
```

Run separately with `--kind labels` and `--kind orders`, each with its own checkpoint file. Optional `--channel CHANNEL` restricts a Page. Repeat while `may_have_more=true`. Checkpoints are bound to tenant/channel/kind, contain no PSID, and are atomically replaced after each successfully handled source row. Retry repeats stable IDs safely. Unresolvable rows are counted; investigate those before cutover. Erased subjects remain skipped. Do not edit a checkpoint to bypass a failed row without resolving it.

Only the saved first/latest labels are imported, not invented transitions between them. One current classification is derived using the policy at migration time and marked `cqa_backfill_derived`; this does not reconstruct historical classifications. Existing `conversation_insight` outcomes are imported without another AI call; outcomes missing amount or evidence remain review items. Metadata not present in old data stays unknown. Historical referral buffering in AdBazooka uses its separate resumable relay queue.

## Verification and cutover

Use disposable MySQL schemas via `MESSENGER_JOURNEY_TEST_DSN` and `MESSENGER_LABELS_TEST_DSN`. Tests create unique schemas and never migrate the database named in the DSN. Run `go test ./...`, affected race tests, `go vet ./...`, a server build, frontend Vitest and production build.

Release order: CQA schema/API -> durable relay -> both legacy backfills -> AdBazooka shadow feed -> compare Page-level event IDs/order revisions/timestamps/amounts -> observe at least 24 hours -> opt in one Page at a time. Real acceptance is CTM click by a non-role account, message, label change, validated order, then cancellation. Keep automatic optimization blocked until coverage and correctness are accepted. Rollback changes read flags; receiving and canonical history continue.

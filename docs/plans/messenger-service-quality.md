# Messenger service quality

User-approved scope: extend existing Messenger analysis with response timing,
waiting/overdue conversations, customer intent/product/feedback and lead quality.
Default business hours: 08:00–22:00 every day, Asia/Ho_Chi_Minh; target 5 minutes,
overdue 15 minutes, configurable. No new dependencies or production changes.

Implementation:
- Pure calendar/turn calculator using original message timestamps. Consecutive
  customer messages form one wait; system/bot messages cannot end it. Page replies
  are labelled as Page replies, not verified human responses. Exact standalone
  acknowledgements after an answer do not reopen a wait; unresolved questions are
  never cleared by a later acknowledgement.
- Tenant-scoped report and queue, original timestamps, sync freshness, latest AI
  insight per conversation. Reporting window selects customer turns, not arbitrary
  message slices. Queue is independent of that window and ages without new messages.
- Append-only manual resolution events bounded by the customer message reviewed;
  subsequent customer messages reopen automatically. No fabricated reply duration.
- In-app overdue alerts refresh while the application is open. External sends are
  not activated by installing the feature.
- Existing classification jobs gain an opt-in Messenger template and structured
  insights. Legacy rules remain valid; no guessed SKU or conflation of complaints
  with low buying potential. Existing QC transcript retains dates/timezone.
- One CSKH Messenger view: KPI, queue/history, evidence, topic/product/feedback/lead
  summaries, policy controls and links to existing messages/jobs.

Validation: calendar boundaries/cross-midnight; unanswered single-message chat;
multiple customer messages; system/bot and acknowledgements; manual close/reopen;
missing timestamps/history; report-window boundary; invalid policy/date; tenant
and permission isolation; old/new classification contracts; frontend build/tests;
Go tests/vet and browser smoke when environment permits. Query caps must fail
explicitly rather than silently report partial totals. History coverage is disclosed.

Tradeoff: derive metrics from synced history for a small reversible first release,
rather than introduce ingestion-maintained aggregates with backfill/drift risks.
Bound dataset sizes and expose sync freshness; materialized aggregates can follow
when observed scale requires them. Individual employee KPI needs an identity source.

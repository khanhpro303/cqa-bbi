# Tracking manual Meta Inbox labels

User approved custom Inbox labels on 2026-09-13 after confirming native Business
Suite lead stages have no verified public read API. Staff keep classifying inside
Meta; CQA reads labels, never writes labels or sends messages.

- Per-Fanpage opt-in configuration maps Meta label IDs to qualified, unqualified,
  potential. Other labels do not count; different matching categories conflict.
- Read Page label catalog and PSID labels through the documented Graph API.
  PSID must come from the non-Page conversation participant, not conversation ID.
  Pagination errors or permission failures never mean an empty label list.
- Store successful observations and error status per conversation. Only complete,
  recent successful reads with valid configuration can mean unclassified.
  Default freshness is 30 minutes; stale/error/unread become unknown.
- Manual refresh plus polling after normal enabled Facebook sync. Read all local
  conversations independently of message updated_time, since a label can change
  without a message. Page-level database lease prevents overlapping workers.
- An independent panel on CSKH shows counts, status filters, timestamped labels,
  a mapping dialog, and links to existing conversations. Scope is all locally
  synced conversations for the selected Page, independent of the date filter.
- Bound report at 5,000 conversations. Checkpoint each 100-conversation batch,
  renew a token-fenced five-minute lease, and resume after interruption on the
  next scheduled/manual run. Two Page sweeps per server process, four readers
  each, one-minute per-Page cooldown, two-hour maximum run.
- Reports use one repeatable-read transaction and publish classifications only
  after success/partial completion. Active/error/expired runs are unknown;
  individual observations still expire after 30 minutes. This avoids mixed
  in-progress counts and false unclassified alerts.
- Do not enable webhook subscriptions, create labels or update production config
  automatically. A real-Page create/change/remove label smoke remains needed.

Validation: Graph pagination/auth/errors/token handling/PSID; classification
truth table, stale/unknown/error/conflict; tenant/permissions and config input;
backend tests/vet/build; frontend types/tests/build and mocked browser flows.

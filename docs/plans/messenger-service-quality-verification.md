# Messenger service quality — local verification

Verified on 2026-09-13. User-selected hours: 08:00–22:00 daily,
Asia/Ho_Chi_Minh, target 5 minutes, overdue 15 minutes.

## Passed

- Backend: `GOCACHE=/tmp/cqa-go-cache go test ./ai ./engine ./servicequality ./api/... ./channels/... ./db/... ./notifications/... ./pkg/... ./workers/...`.
- Static analysis: `go vet` on the same package list.
- Backend build: `go build -o /tmp/cqa-messenger-build main.go`.
- Frontend: `npm run build` (includes `vue-tsc -b`); `npx vitest run` (25 tests, 5 files).
- Documentation: `npm run docs:build`.
- `git diff --check`.
- GitNexus `detect-changes --scope all`: medium overall impact, 5 flows in job execution/router. This command covers tracked diffs; the new metrics/report/API/UI files were inspected separately. Pre-existing AGENTS.md and CLAUDE.md modifications were left intact.

## Browser smoke

Used local Vite with intercepted API fixtures, never live customer data or live message sends.

- Desktop 1440px and mobile 390px: report, KPI cards, topic/product/feedback/lead summaries, sync metadata, and conversation list render. Card headings wrap on mobile; the conversation table scrolls horizontally inside its container.
- Queue filter and evidence detail display the selected conversation; links point to the existing messages screen with conversation/channel IDs.
- Policy dialog shows 08:00–22:00, 5/15 minutes and Asia/Ho_Chi_Minh.
- Manual resolution requires a note; submitting sends the reviewed last-customer-message ID and note to the correct tenant/conversation endpoint.
- Messenger job deep link prefills classification type, name and description. Rules/profile preservation is covered by frontend unit tests.
- A simulated report-cap 422 clears old KPI data, displays the error, and retains the Fanpage selector for recovery.

## Validation limits

- No local MySQL server or working Docker daemon was available. Migration, SQL reads/writes, tenant isolation against a real database, and end-to-end sync → analysis → report require integration verification with MySQL.
- Browser data was mocked. No production AI model was called; model quality and evidence fidelity require review on representative real conversations.
- Existing scratch files `backend/get_db_info.go` and `backend/list_collections.go` duplicate root main declarations, so build used `main.go` and tests named application packages rather than `go test ./...` at the backend root.
- Go HTTP fixture tests require permission to bind localhost; they passed when run with that permission.
- The frontend has no separate lint script. Type checking, build, unit tests and browser smoke were used; existing browser manifest/extension console errors were unrelated to this feature.
- No production deployment, commit, live job creation or external notification was performed.

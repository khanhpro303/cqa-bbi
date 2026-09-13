# Meta Inbox label tracking — local verification

Verified 2026-09-13. User approved custom labels as a data/counting/alert
workflow independent of AI. No Meta writes, native-stage inference or AI calls.

## Implemented

- Read-only Graph adapter: Page catalog, PSID labels and safe participant lookup;
  cursor pagination, sanitized errors, bearer authentication and redirect denial.
- Per-Page opt-in policy, ID-based mapping, six disjoint count states and local
  conversation scope. Missing/stale/errored reads never mean unclassified.
- Scheduled/manual synchronization with database ownership, resumable batches,
  lease renewal, stale-worker write fencing, per-Page cooldown and bounded
  process concurrency. Reports withhold classification while syncing and read
  status/snapshots in one repeatable-read transaction.
- Separate CSKH panel: settings, counts, filters, observed timestamps, links,
  unclassified warning and unknown/conflict handling. No AI dependency.

## Passed

- `GOCACHE=/tmp/cqa-go-cache go test -race ./messengerlabels ./servicequality ./api/handlers ./db/models ./channels`
- `go vet` across ai, engine, servicequality, messengerlabels, api, channels,
  db, notifications, pkg and workers packages.
- `go build -o /tmp/cqa-messenger-build main.go`
- Frontend `npm run build` including TypeScript; local `vitest run`: 28 tests.
- Documentation build and `git diff --check`.
- Mocked browser smoke: six count states; filter unclassified; conversation
  link; mapping save with exact label IDs; POST sync endpoint; syncing hides
  unclassified alert; mobile 390px has no horizontal document overflow.

## Explicit gaps

- No MySQL server/working Docker is available locally. The opt-in MySQL test is
  compiled but skipped. `MESSENGER_LABELS_TEST_DSN` enables a test that creates
  and drops its own uniquely named temporary schema, covering competing claims,
  resumable progress, stale-token writes/cleanup, tenant isolation, partial
  reports, cooldown and foreign-key deletion. It requires a test MySQL account
  allowed to create/drop schemas. It never migrates the DSN's named database.
- No live Meta Page/token calls, label changes, production deployment or
  production configuration activation. Real-Page add/change/remove-label checks
  and permission verification remain necessary before operational use.
- No benchmark for large Pages or multiple server replicas. Concurrency is
  bounded per process; Page ownership is shared across replicas. Long runs may
  legitimately leave observations older than 30 minutes unknown.
- Full backend HTTP-fixture suite was not rerun after these additions: automatic
  approval review rejected the localhost-permission request because the reviewer
  reached its usage limit. Targeted sandbox-compatible race tests, static
  checks and builds above passed. Earlier CSKH full-suite evidence is recorded
  separately in messenger-service-quality-verification.md.
- Browser data is intercepted fixtures. Existing manifest errors are unrelated.
- Existing scratch Go mains prevent a root `go test ./...`; named application
  packages and `main.go` were used. No new dependencies were introduced.

## Deployment preparation

The user subsequently authorized deployment. A clean release candidate excluding
local scratch mains and agent files passed full `go test ./...`, `go vet ./...`,
backend build, frontend build and 28 frontend tests. The earlier full-suite
permission restriction is resolved. The VPS workflow now gates rollout on a
MySQL 8 integration run and frontend/backend verification, saves a private SQL
backup and the previous app image, and checks health, all three new tables and
the frontend artifact after deployment. Rollback restores the previous app image;
new additive tables remain. Live Meta label smoke still requires a configured Page.

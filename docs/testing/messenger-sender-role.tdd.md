# Messenger sender-role hallucination — TDD evidence

## Source and user journey

No external plan file was provided. The journey was derived from the reported production conversation:

> As a CSKH reviewer, I want Messenger insights to treat only `customer` messages as customer evidence, so that a Fanpage-only conversation cannot be summarized as a customer request or disclosure.

## Task report

| Behavior | RED evidence | GREEN evidence | Guarantee |
| --- | --- | --- | --- |
| Mandatory role rules survive both default and tenant-custom Messenger prompts | `go test ./ai ./engine -run 'TestMessengerPromptAlwaysIncludesMandatorySenderRoleRules\|TestMessengerInsightsGuard' -count=1` failed because the prompt lacked the rules | The same targeted test set passed after the prompt guard was added | Tenant prompt overrides cannot remove the sender-role invariant |
| Agent-only Messenger conversations do not reach customer-intent analysis | The targeted test failed to compile because `messengerInsightsGuardResponse` did not exist | `TestMessengerInsightsGuardSkipsAgentOnlyConversation` passed | Agent-only transcripts produce an empty-insight `SKIP` payload with `lead_quality=unknown` |
| Existing behavior remains for conversations with a customer message, legacy classification, and QC | Covered by the new negative-control test | `TestMessengerInsightsGuardLeavesCustomerAndOtherJobsForAnalysis` passed | The deterministic guard is limited to the Messenger Insights profile |

## Verification

- `GOCACHE=/tmp/cqa-bbi-go-cache-full go test ./...` — PASS outside the filesystem/network sandbox because existing `httptest` suites require a loopback listener.
- `GOCACHE=/tmp/cqa-bbi-go-cache-race go test -race ./messengerlabels` — PASS.
- `GOCACHE=/tmp/cqa-bbi-go-cache-vet go vet ./...` — PASS.
- `GOCACHE=/tmp/cqa-bbi-go-cache-build CGO_ENABLED=0 go build -o /tmp/cqa-server-sender-role .` — PASS.
- `npx vitest run` — PASS, 18 files and 86 tests.
- `npm run build` — PASS.

## Coverage and known gaps

Targeted function coverage:

- `appendMandatorySenderRoleInstructions`: 100%
- `BuildClassificationPrompt`: 95%
- `messengerInsightsGuardResponse`: 90%

Existing package-wide coverage remains below the skill target (`ai`: 42.6%, `engine`: 29.9%). The new decision points are covered, but raising the two large legacy packages to 80% is outside this narrowly scoped production fix.

The deployment changes future analysis only. Previously stored incorrect evaluations require a full rerun of the relevant Messenger Insights job to be regenerated.

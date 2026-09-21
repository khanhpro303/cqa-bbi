# After-sync job coalescing: TDD evidence

## Behaviour under test

For a given AI job, concurrent `after_sync` triggers must not run overlapping
evaluations. A burst is coalesced into the active run plus at most one trailing
run, while different job IDs remain independent.

## RED

Command:

```sh
GOCACHE=/tmp/cqa-bbi-go-build go test ./engine -run '^TestJobRunCoordinator' -count=1
```

Expected failure observed before implementation:

```text
undefined: newJobRunCoordinator
FAIL github.com/vietbui/chat-quality-agent/engine [build failed]
```

Checkpoint: `d6408d2 test(scheduler): reproduce duplicate after-sync runs`

## GREEN

The coordinator now serializes a job by `job.ID`, records a pending rerun when
another trigger arrives, and releases ownership after completion or panic.

Verification:

```text
go test ./...                                      PASS
go test -race ./messengerlabels ./engine           PASS
go vet ./...                                       PASS
CGO_ENABLED=0 go build -o /tmp/cqa-server .        PASS
npx vitest run                                     20 files / 95 tests PASS
npm run build                                      PASS
```

Targeted coverage reports 100% statement coverage for
`newJobRunCoordinator` and `jobRunCoordinator.Run`.

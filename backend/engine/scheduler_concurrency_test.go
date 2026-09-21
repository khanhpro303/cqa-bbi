package engine

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestJobRunCoordinatorCoalescesDuplicateTriggersAndRunsPending(t *testing.T) {
	t.Parallel()

	coordinator := newJobRunCoordinator()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstFinished := make(chan struct{})
	var calls atomic.Int32

	run := func() {
		call := calls.Add(1)
		if call == 1 {
			close(firstEntered)
			<-releaseFirst
		}
	}

	go func() {
		defer close(firstFinished)
		if !coordinator.Run("job-a", run) {
			t.Errorf("first trigger should own the job execution")
		}
	}()

	<-firstEntered
	if coordinator.Run("job-a", run) {
		t.Fatal("concurrent duplicate trigger should be coalesced")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("runner called %d times before first run completed; want 1", got)
	}

	close(releaseFirst)
	<-firstFinished
	if got := calls.Load(); got != 2 {
		t.Fatalf("runner called %d times; want one initial run and one pending rerun", got)
	}

	if !coordinator.Run("job-a", run) {
		t.Fatal("job should be acquirable again after completion")
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("runner called %d times after a later trigger; want 3", got)
	}
}

func TestJobRunCoordinatorAllowsDifferentJobs(t *testing.T) {
	t.Parallel()

	coordinator := newJobRunCoordinator()
	jobAEntered := make(chan struct{})
	releaseJobA := make(chan struct{})
	jobAFinished := make(chan struct{})
	var jobBCalls atomic.Int32

	go func() {
		defer close(jobAFinished)
		coordinator.Run("job-a", func() {
			close(jobAEntered)
			<-releaseJobA
		})
	}()

	<-jobAEntered
	if !coordinator.Run("job-b", func() { jobBCalls.Add(1) }) {
		t.Fatal("a different job should run while job-a is active")
	}
	if got := jobBCalls.Load(); got != 1 {
		t.Fatalf("job-b runner called %d times; want 1", got)
	}

	close(releaseJobA)
	<-jobAFinished
}

func TestJobRunCoordinatorReleasesAfterPanic(t *testing.T) {
	t.Parallel()

	coordinator := newJobRunCoordinator()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected runner panic")
			}
		}()
		coordinator.Run("job-a", func() { panic("boom") })
	}()

	var calls atomic.Int32
	if !coordinator.Run("job-a", func() { calls.Add(1) }) {
		t.Fatal("job should be acquirable after a runner panic")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("runner called %d times after panic; want 1", got)
	}
}

func TestJobRunCoordinatorConcurrentDuplicatesOnlyQueueOneRerun(t *testing.T) {
	t.Parallel()

	coordinator := newJobRunCoordinator()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	ownerFinished := make(chan struct{})
	var calls atomic.Int32

	run := func() {
		if calls.Add(1) == 1 {
			close(firstEntered)
			<-releaseFirst
		}
	}

	go func() {
		defer close(ownerFinished)
		coordinator.Run("job-a", run)
	}()
	<-firstEntered

	const duplicateTriggers = 8
	var duplicates sync.WaitGroup
	duplicates.Add(duplicateTriggers)
	for range duplicateTriggers {
		go func() {
			defer duplicates.Done()
			if coordinator.Run("job-a", run) {
				t.Error("duplicate trigger unexpectedly became an owner")
			}
		}()
	}
	duplicates.Wait()

	close(releaseFirst)
	<-ownerFinished
	if got := calls.Load(); got != 2 {
		t.Fatalf("runner called %d times; want duplicate burst coalesced into exactly one rerun", got)
	}
}

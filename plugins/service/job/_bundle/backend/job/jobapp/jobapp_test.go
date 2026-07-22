package jobapp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestTaskRegistrySnapshotsHandlersAndRejectsInvalidComposition(t *testing.T) {
	first := &task{id: "alpha"}
	second := &task{id: "beta"}
	registry, err := NewTaskRegistry(second, first)
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.IDs(); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("registry IDs = %#v", got)
	}
	first.id = "changed"
	if got := registry.IDs(); got[0] != "alpha" {
		t.Fatalf("registry observed handler mutation: %#v", got)
	}

	_, err = NewTaskRegistry(first, first)
	assertCode(t, err, CodeTaskDuplicate)
	var typedNil *task
	_, err = NewTaskRegistry(typedNil)
	assertCode(t, err, CodeInvalidInput)
	_, err = NewTaskRegistry(&task{id: ""})
	assertCode(t, err, CodeInvalidInput)
}

func TestManualRunCopiesPayloadAndResultAndProjectsFailures(t *testing.T) {
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	store := newMemoryStore()
	worker := &task{id: "echo", run: func(_ context.Context, request TaskRequest) (TaskResult, error) {
		request.Payload[0] = 'x'
		return TaskResult{Output: request.Payload}, nil
	}}
	scheduler := newScheduler(t, fakeClock{now: now}, store, worker, 1)
	payload := []byte("abc")
	result, err := scheduler.Run(context.Background(), "echo", TaskRequest{RunID: "manual-1", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "abc" || string(result.Output) != "xbc" {
		t.Fatalf("payload=%q result=%q", payload, result.Output)
	}
	result.Output[0] = 'z'
	completion := store.completion("manual-1")
	if completion.Status != RunSucceeded || string(completion.Output) != "xbc" || completion.ErrorCode != "" {
		t.Fatalf("completion = %#v", completion)
	}

	_, err = scheduler.Run(context.Background(), "missing", TaskRequest{RunID: "unknown"})
	assertCode(t, err, CodeTaskUnknown)
	failed := &task{id: "failed", run: func(context.Context, TaskRequest) (TaskResult, error) {
		return TaskResult{}, errors.New("credential-secret")
	}}
	failedScheduler := newScheduler(t, fakeClock{now: now}, newMemoryStore(), failed, 1)
	_, err = failedScheduler.Run(context.Background(), "failed", TaskRequest{RunID: "failed-1"})
	assertCode(t, err, CodeTaskFailed)
	if got := err.Error(); got != "scheduler.run: task_failed" {
		t.Fatalf("task error leaked cause: %q", got)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = scheduler.Run(canceled, "echo", TaskRequest{RunID: "cancel"})
	assertCode(t, err, CodeCanceled)
}

func TestManualRunEnforcesConcurrencyLimitAndCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	blocking := &task{id: "blocking", run: func(ctx context.Context, _ TaskRequest) (TaskResult, error) {
		close(entered)
		select {
		case <-release:
			return TaskResult{}, nil
		case <-ctx.Done():
			return TaskResult{}, ctx.Err()
		}
	}}
	scheduler := newScheduler(t, fakeClock{now: time.Now()}, newMemoryStore(), blocking, 1)
	firstDone := make(chan error, 1)
	go func() {
		_, err := scheduler.Run(context.Background(), "blocking", TaskRequest{RunID: "first"})
		firstDone <- err
	}()
	<-entered
	_, err := scheduler.Run(context.Background(), "blocking", TaskRequest{RunID: "second"})
	assertCode(t, err, CodeConcurrencyLimit)
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestCronSchedulingLifecycleAndHealthAreDeterministic(t *testing.T) {
	clock := newManualClock(time.Date(2026, 7, 14, 8, 0, 30, 0, time.UTC))
	store := newMemoryStore()
	store.schedules = []ScheduleSpec{{ID: "every-minute", Cron: "* * * * *", TaskID: "scheduled", Payload: []byte("payload")}}
	ran := make(chan TaskRequest, 1)
	worker := &task{id: "scheduled", run: func(_ context.Context, request TaskRequest) (TaskResult, error) {
		ran <- request
		return TaskResult{Output: []byte("done")}, nil
	}}
	scheduler := newScheduler(t, clock, store, worker, 2)
	if health := scheduler.Health(context.Background()); health.Status != HealthReady || health.ActiveRuns != 0 {
		t.Fatalf("initial health = %#v", health)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if health := scheduler.Health(context.Background()); health.Status != HealthRunning {
		t.Fatalf("running health = %#v", health)
	}
	_, wait := clock.nextWait(t)
	clock.advance(time.Date(2026, 7, 14, 8, 1, 0, 0, time.UTC), wait)
	select {
	case request := <-ran:
		if request.RunID != "every-minute@2026-07-14T08:01:00Z" || string(request.Payload) != "payload" {
			t.Fatalf("scheduled request = %#v", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled task did not run")
	}
	store.waitCompletion(t, "every-minute@2026-07-14T08:01:00Z")
	if err := scheduler.Start(context.Background()); CodeOf(err) != CodeLifecycleConflict {
		t.Fatalf("restart error = %v", err)
	}
	if err := scheduler.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if health := scheduler.Health(context.Background()); health.Status != HealthStopped || health.ActiveRuns != 0 {
		t.Fatalf("closed health = %#v", health)
	}
}

func TestUnknownScheduledTaskIsRuntimeFailureNotStartupDependency(t *testing.T) {
	clock := newManualClock(time.Date(2026, 7, 14, 8, 0, 30, 0, time.UTC))
	store := newMemoryStore()
	store.schedules = []ScheduleSpec{{ID: "unknown", Cron: "* * * * *", TaskID: "consumer-task"}}
	scheduler := newScheduler(t, clock, store, &task{id: "registered"}, 1)
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("unknown task blocked startup: %v", err)
	}
	_, wait := clock.nextWait(t)
	clock.advance(time.Date(2026, 7, 14, 8, 1, 0, 0, time.UTC), wait)
	deadline := time.After(2 * time.Second)
	for {
		health := scheduler.Health(context.Background())
		if health.Status == HealthDegraded && health.LastErrorCode == CodeTaskUnknown {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("runtime task failure not projected: %#v", health)
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := scheduler.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRetentionAndStableStoreFailures(t *testing.T) {
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	store := newMemoryStore()
	store.deleteCount = 3
	scheduler := newScheduler(t, fakeClock{now: now}, store, &task{id: "task"}, 1)
	count, err := scheduler.Retain(context.Background(), RetentionPolicyFunc(func(value time.Time) time.Time {
		return value.Add(-24 * time.Hour)
	}))
	if err != nil || count != 3 || !store.cutoff.Equal(now.Add(-24*time.Hour)) {
		t.Fatalf("retention count=%d cutoff=%s err=%v", count, store.cutoff, err)
	}
	_, err = scheduler.Retain(context.Background(), nil)
	assertCode(t, err, CodeInvalidInput)

	store.scheduleErr = errors.New("database credential-secret")
	failed := newScheduler(t, fakeClock{now: now}, store, &task{id: "task"}, 1)
	err = failed.Start(context.Background())
	assertCode(t, err, CodeStoreFailure)
	if got := err.Error(); got != "scheduler.start: store_failure" {
		t.Fatalf("store error leaked cause: %q", got)
	}
}

func newScheduler(t *testing.T, clock Clock, store Store, worker Task, maxConcurrent int) *Scheduler {
	t.Helper()
	registry, err := NewTaskRegistry(worker)
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := NewScheduler(SchedulerOptions{Clock: clock, Registry: registry, Store: store, MaxConcurrent: maxConcurrent})
	if err != nil {
		t.Fatal(err)
	}
	return scheduler
}

func assertCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if got := CodeOf(err); got != code {
		t.Fatalf("error = %v, code=%q want=%q", err, got, code)
	}
}

type task struct {
	id  TaskID
	run func(context.Context, TaskRequest) (TaskResult, error)
}

func (t *task) ID() TaskID { return t.id }
func (t *task) Run(ctx context.Context, request TaskRequest) (TaskResult, error) {
	if t.run == nil {
		return TaskResult{}, nil
	}
	return t.run(ctx, request)
}

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time                     { return c.now }
func (fakeClock) After(time.Duration) <-chan time.Time { return make(chan time.Time) }

type manualClock struct {
	mu    sync.Mutex
	now   time.Time
	waits chan clockWait
}

type clockWait struct {
	duration time.Duration
	ready    chan time.Time
}

func newManualClock(now time.Time) *manualClock {
	return &manualClock{now: now, waits: make(chan clockWait, 8)}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) After(duration time.Duration) <-chan time.Time {
	ready := make(chan time.Time, 1)
	c.waits <- clockWait{duration: duration, ready: ready}
	return ready
}

func (c *manualClock) nextWait(t *testing.T) (time.Duration, chan time.Time) {
	t.Helper()
	select {
	case wait := <-c.waits:
		return wait.duration, wait.ready
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not request a clock wait")
		return 0, nil
	}
}

func (c *manualClock) advance(now time.Time, ready chan time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
	ready <- now
}

type memoryStore struct {
	mu           sync.Mutex
	schedules    []ScheduleSpec
	scheduleErr  error
	leases       map[string]struct{}
	runs         map[string]RunRecord
	completions  map[string]RunCompletion
	completionCh chan string
	deleteCount  int
	cutoff       time.Time
}

func newMemoryStore() *memoryStore {
	return &memoryStore{leases: map[string]struct{}{}, runs: map[string]RunRecord{}, completions: map[string]RunCompletion{}, completionCh: make(chan string, 16)}
}

func (s *memoryStore) Schedules(context.Context) ([]ScheduleSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scheduleErr != nil {
		return nil, s.scheduleErr
	}
	result := append([]ScheduleSpec(nil), s.schedules...)
	for index := range result {
		result[index].Payload = append([]byte(nil), result[index].Payload...)
	}
	return result, nil
}

func (s *memoryStore) AcquireLease(_ context.Context, lease Lease) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.leases[lease.Key]; exists {
		return false, nil
	}
	s.leases[lease.Key] = struct{}{}
	return true, nil
}

func (s *memoryStore) StartRun(_ context.Context, run RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.runs[run.ID]; exists {
		return ErrStoreConflict
	}
	run.Payload = append([]byte(nil), run.Payload...)
	s.runs[run.ID] = run
	return nil
}

func (s *memoryStore) FinishRun(_ context.Context, completion RunCompletion) error {
	s.mu.Lock()
	completion.Output = append([]byte(nil), completion.Output...)
	s.completions[completion.RunID] = completion
	s.mu.Unlock()
	s.completionCh <- completion.RunID
	return nil
}

func (s *memoryStore) DeleteRunsBefore(_ context.Context, cutoff time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cutoff = cutoff
	return s.deleteCount, nil
}

func (s *memoryStore) completion(runID string) RunCompletion {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completions[runID]
}

func (s *memoryStore) waitCompletion(t *testing.T, runID string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if completion := s.completion(runID); completion.RunID == runID {
			return
		}
		select {
		case <-s.completionCh:
		case <-deadline:
			t.Fatalf("run %q did not complete", runID)
		}
	}
}

var _ Task = (*task)(nil)
var _ Clock = (*manualClock)(nil)
var _ Store = (*memoryStore)(nil)

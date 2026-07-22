package kafka

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerPollRetryBudgetAndSuccessReset(t *testing.T) {
	t.Run("budget stops at configured attempt", func(t *testing.T) {
		var attemptsMu sync.Mutex
		var attempts []int
		var pollCalls atomic.Int32
		reader := &testReader{poll: func(context.Context) (Batch, error) {
			if pollCalls.Add(1) == 2 {
				return Batch{}, nil
			}
			return Batch{}, errors.New("poll")
		}}
		policy := RetryPolicyFunc(func(failure Failure) RetryDecision {
			attemptsMu.Lock()
			attempts = append(attempts, failure.Attempt())
			attemptsMu.Unlock()
			return RetryDecision{Retry: failure.Attempt() < 3}
		})
		manager := testManager(t, testSubscription(t, "poll-budget", nil), reader, policy)
		if err := manager.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := manager.Wait(context.Background()); !errors.Is(err, ErrReaderFailed) {
			t.Fatalf("Wait() = %v", err)
		}
		attemptsMu.Lock()
		defer attemptsMu.Unlock()
		if got := append([]int(nil), attempts...); !equalInts(got, []int{1, 2, 3}) {
			t.Fatalf("attempts = %v", got)
		}
	})

	t.Run("valid batch resets poll attempt", func(t *testing.T) {
		batch := testBatch(t, 1)
		var pollCalls atomic.Int32
		var attemptsMu sync.Mutex
		var attempts []int
		reader := &testReader{
			poll: func(context.Context) (Batch, error) {
				switch pollCalls.Add(1) {
				case 1, 3:
					return Batch{}, errors.New("poll")
				case 2:
					return batch, nil
				default:
					return Batch{}, errors.New("unexpected poll")
				}
			},
		}
		policy := RetryPolicyFunc(func(failure Failure) RetryDecision {
			attemptsMu.Lock()
			attempts = append(attempts, failure.Attempt())
			call := len(attempts)
			attemptsMu.Unlock()
			return RetryDecision{Retry: call == 1}
		})
		manager := testManager(t, testSubscription(t, "poll-reset", nil), reader, policy)
		if err := manager.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := manager.Wait(context.Background()); !errors.Is(err, ErrReaderFailed) {
			t.Fatalf("Wait() = %v", err)
		}
		attemptsMu.Lock()
		defer attemptsMu.Unlock()
		if got := append([]int(nil), attempts...); !equalInts(got, []int{1, 1}) {
			t.Fatalf("attempts = %v", got)
		}
	})
}

func TestManagerCommitRetryUsesIncreasingAttempts(t *testing.T) {
	batch := testBatch(t, 1)
	polled := atomic.Bool{}
	committed := make(chan struct{})
	var commitCalls atomic.Int32
	var attemptsMu sync.Mutex
	var attempts []int
	reader := &testReader{
		poll: func(ctx context.Context) (Batch, error) {
			if polled.CompareAndSwap(false, true) {
				return batch, nil
			}
			<-ctx.Done()
			return Batch{}, ctx.Err()
		},
		commit: func(context.Context) error {
			call := commitCalls.Add(1)
			if call < 3 {
				return errors.New("commit")
			}
			close(committed)
			return nil
		},
	}
	policy := RetryPolicyFunc(func(failure Failure) RetryDecision {
		if failure.Stage() != StageCommit {
			t.Errorf("stage = %q", failure.Stage())
		}
		attemptsMu.Lock()
		attempts = append(attempts, failure.Attempt())
		attemptsMu.Unlock()
		return RetryDecision{Retry: true}
	})
	manager := testManager(t, testSubscription(t, "commit-retry", nil), reader, policy)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	receiveSignal(t, committed)
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	attemptsMu.Lock()
	defer attemptsMu.Unlock()
	if !equalInts(attempts, []int{1, 2}) || commitCalls.Load() != 3 {
		t.Fatalf("attempts=%v commitCalls=%d", attempts, commitCalls.Load())
	}
}

func TestManagerCloseDuringStartWaitsForStartupCleanup(t *testing.T) {
	openStarted := make(chan struct{})
	openReturned := make(chan struct{})
	manager, err := NewManager(ManagerOptions{
		Subscriptions: []Subscription{testSubscription(t, "startup-close", nil)},
		ReaderFactory: testFactory{open: func(ctx context.Context, _ Subscription) (Reader, error) {
			close(openStarted)
			<-ctx.Done()
			close(openReturned)
			return nil, ctx.Err()
		}},
		RetryPolicy: NoRetry(),
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	startResult := make(chan error, 1)
	go func() { startResult <- manager.Start(context.Background()) }()
	receiveSignal(t, openStarted)
	closeResult := make(chan error, 1)
	go func() { closeResult <- manager.Close(context.Background()) }()
	receiveSignal(t, openReturned)
	if err := receiveError(t, closeResult); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if err := receiveError(t, startResult); !errors.Is(err, context.Canceled) || !errors.Is(err, ErrOperationCanceled) {
		t.Fatalf("Start() = %v", err)
	}
	if manager.State() != StateClosed {
		t.Fatalf("state = %q", manager.State())
	}
}

func TestManagerConcurrentCloseIsIdempotent(t *testing.T) {
	var closeCalls atomic.Int32
	readerClosed := make(chan struct{})
	reader := &testReader{
		poll:  func(ctx context.Context) (Batch, error) { <-readerClosed; return Batch{}, ctx.Err() },
		close: func() error { closeCalls.Add(1); close(readerClosed); return nil },
	}
	manager := testManager(t, testSubscription(t, "concurrent-close", nil), reader, NoRetry())
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() { results <- manager.Close(context.Background()) }()
	go func() { results <- manager.Close(context.Background()) }()
	for range 2 {
		if err := receiveError(t, results); err != nil {
			t.Fatalf("Close() = %v", err)
		}
	}
	if closeCalls.Load() != 1 || manager.State() != StateClosed {
		t.Fatalf("closeCalls=%d state=%q", closeCalls.Load(), manager.State())
	}
}

func TestManagerWaitHonorsCallerCancellation(t *testing.T) {
	reader := &testReader{poll: func(ctx context.Context) (Batch, error) { <-ctx.Done(); return Batch{}, ctx.Err() }}
	manager := testManager(t, testSubscription(t, "wait-cancel", nil), reader, NoRetry())
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait() = %v", err)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerCancellationStartsNoNewHandlerOrCommit(t *testing.T) {
	t.Run("valid Poll after cancellation does not Handle", func(t *testing.T) {
		batch := testBatch(t, 1)
		pollStarted := make(chan struct{})
		releasePoll := make(chan struct{})
		var handles atomic.Int32
		reader := &testReader{
			poll:  func(context.Context) (Batch, error) { close(pollStarted); <-releasePoll; return batch, nil },
			close: func() error { close(releasePoll); return nil },
		}
		manager := testManager(t, testSubscription(t, "cancel-after-poll", HandlerFunc(func(context.Context, Record) error { handles.Add(1); return nil })), reader, NoRetry())
		if err := manager.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		receiveSignal(t, pollStarted)
		if err := manager.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if handles.Load() != 0 {
			t.Fatalf("Handle calls = %d", handles.Load())
		}
	})

	t.Run("cancellation between records starts no next Handle or Commit", func(t *testing.T) {
		batch := testBatch(t, 1, 2)
		firstHandle := make(chan struct{})
		var handles, commits atomic.Int32
		reader := &testReader{
			poll:   func(context.Context) (Batch, error) { return batch, nil },
			commit: func(context.Context) error { commits.Add(1); return nil },
		}
		subscription := testSubscription(t, "cancel-between-handles", HandlerFunc(func(ctx context.Context, _ Record) error {
			if handles.Add(1) == 1 {
				close(firstHandle)
				<-ctx.Done()
			}
			return nil
		}))
		manager := testManager(t, subscription, reader, NoRetry())
		if err := manager.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		receiveSignal(t, firstHandle)
		if err := manager.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if handles.Load() != 1 || commits.Load() != 0 {
			t.Fatalf("handles=%d commits=%d", handles.Load(), commits.Load())
		}
	})
}

func TestManagerPollHandleCommitAndClose(t *testing.T) {
	batch := testBatch(t, 1, 2)
	events := make(chan string, 8)
	polled := false
	var pollMu sync.Mutex
	readerClosed := make(chan struct{})
	reader := &testReader{
		poll: func(ctx context.Context) (Batch, error) {
			pollMu.Lock()
			if !polled {
				polled = true
				pollMu.Unlock()
				events <- "poll"
				return batch, nil
			}
			pollMu.Unlock()
			<-readerClosed
			return Batch{}, ctx.Err()
		},
		commit: func(context.Context) error { events <- "commit"; return nil },
		close: func() error {
			close(readerClosed)
			events <- "close"
			return nil
		},
	}
	subscription := testSubscription(t, "orders", HandlerFunc(func(context.Context, Record) error {
		events <- "handle"
		return nil
	}))
	manager := testManager(t, subscription, reader, NoRetry())
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"poll", "handle", "handle", "commit"} {
		if got := receive(t, events); got != want {
			t.Fatalf("event = %q, want %q", got, want)
		}
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := receive(t, events); got != "close" {
		t.Fatalf("event = %q, want close", got)
	}
	if manager.State() != StateClosed {
		t.Fatalf("state = %q", manager.State())
	}
}

func TestManagerFailureIsTypedAndDoesNotExposeAdapterCause(t *testing.T) {
	raw := errors.New("broker secret")
	reader := &testReader{poll: func(context.Context) (Batch, error) { return Batch{}, raw }}
	manager := testManager(t, testSubscription(t, "failure", nil), reader, NoRetry())
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := manager.Wait(context.Background())
	var typed *Error
	if !errors.As(err, &typed) || typed.Code() != "reader_failed" || typed.Reason() != "poll_failed" || typed.SubscriptionID() != "failure" {
		t.Fatalf("terminal = %T %v", err, err)
	}
	if errors.Is(err, raw) || errors.Unwrap(err) != nil {
		t.Fatal("adapter cause escaped")
	}
	if manager.State() != StateFailed {
		t.Fatalf("state = %q", manager.State())
	}
}

func TestManagerRunsOneWorkerPerSubscription(t *testing.T) {
	handled := make(chan string, 2)
	subscriptions := []Subscription{
		testSubscription(t, "first", HandlerFunc(func(context.Context, Record) error { handled <- "first"; return nil })),
		testSubscription(t, "second", HandlerFunc(func(context.Context, Record) error { handled <- "second"; return nil })),
	}
	readers := map[string]*testReader{}
	for _, subscription := range subscriptions {
		batch := testBatch(t, 1)
		polled := false
		readers[subscription.ID()] = &testReader{poll: func(ctx context.Context) (Batch, error) {
			if !polled {
				polled = true
				return batch, nil
			}
			<-ctx.Done()
			return Batch{}, ctx.Err()
		}}
	}
	manager, err := NewManager(ManagerOptions{
		Subscriptions: subscriptions,
		ReaderFactory: testFactory{open: func(_ context.Context, subscription Subscription) (Reader, error) {
			return readers[subscription.ID()], nil
		}},
		RetryPolicy: NoRetry(),
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{receive(t, handled): true, receive(t, handled): true}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("workers handled = %#v", seen)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerClosesEveryReaderWhenOneCloseFails(t *testing.T) {
	subscriptions := []Subscription{testSubscription(t, "first-close", nil), testSubscription(t, "second-close", nil)}
	closed := map[string]chan struct{}{"first-close": make(chan struct{}), "second-close": make(chan struct{})}
	readers := map[string]*testReader{}
	for _, subscription := range subscriptions {
		id := subscription.ID()
		readers[id] = &testReader{
			poll: func(ctx context.Context) (Batch, error) {
				<-closed[id]
				return Batch{}, ctx.Err()
			},
			close: func() error {
				close(closed[id])
				if id == "first-close" {
					return errors.New("close failed")
				}
				return nil
			},
		}
	}
	manager, err := NewManager(ManagerOptions{
		Subscriptions: subscriptions,
		ReaderFactory: testFactory{open: func(_ context.Context, subscription Subscription) (Reader, error) {
			return readers[subscription.ID()], nil
		}},
		RetryPolicy: NoRetry(),
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err = manager.Close(ctx)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code() != "close_failed" || typed.SubscriptionID() != "first-close" {
		t.Fatalf("Close() = %T %v", err, err)
	}
	select {
	case <-closed["second-close"]:
	default:
		t.Fatal("second reader was not closed")
	}
}

func TestManagerRetriesHandlerBeforeCommit(t *testing.T) {
	batch := testBatch(t, 1)
	var mu sync.Mutex
	events := make([]string, 0, 5)
	reader := &testReader{
		poll: func(ctx context.Context) (Batch, error) {
			mu.Lock()
			first := len(events) == 0
			mu.Unlock()
			if first {
				return batch, nil
			}
			<-ctx.Done()
			return Batch{}, ctx.Err()
		},
		commit: func(context.Context) error {
			mu.Lock()
			events = append(events, "commit")
			mu.Unlock()
			return nil
		},
	}
	call := 0
	subscription := testSubscription(t, "retry", HandlerFunc(func(context.Context, Record) error {
		mu.Lock()
		defer mu.Unlock()
		call++
		events = append(events, "handle")
		if call == 1 {
			return errors.New("retry")
		}
		return nil
	}))
	policy := RetryPolicyFunc(func(failure Failure) RetryDecision {
		if failure.Stage() != StageHandle || failure.Attempt() != 1 {
			t.Fatalf("failure = stage:%q attempt:%d", failure.Stage(), failure.Attempt())
		}
		return RetryDecision{Retry: true}
	})
	manager := testManager(t, subscription, reader, policy)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		ready := len(events) >= 3
		mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retry and commit did not complete")
		}
		time.Sleep(time.Millisecond)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) < 3 || events[0] != "handle" || events[1] != "handle" || events[2] != "commit" {
		t.Fatalf("events = %#v", events)
	}
}

func TestManagerValidationAndLifecycle(t *testing.T) {
	reader := &testReader{poll: func(ctx context.Context) (Batch, error) { <-ctx.Done(); return Batch{}, ctx.Err() }}
	subscription := testSubscription(t, "valid", nil)
	manager := testManager(t, subscription, reader, NoRetry())
	if err := manager.Wait(context.Background()); !errors.Is(err, ErrLifecycleConflict) {
		t.Fatalf("Wait(new) = %v", err)
	}
	if err := manager.Close(context.Background()); err != nil || manager.State() != StateClosed {
		t.Fatalf("Close(new) = %v state %q", err, manager.State())
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	if _, err := NewManager(ManagerOptions{}); !errors.Is(err, ErrConfigurationInvalid) {
		t.Fatalf("NewManager(empty) = %v", err)
	}
}

type testReader struct {
	poll   func(context.Context) (Batch, error)
	commit func(context.Context) error
	close  func() error
}

func (r *testReader) Poll(ctx context.Context) (Batch, error) { return r.poll(ctx) }
func (r *testReader) Commit(ctx context.Context) error {
	if r.commit == nil {
		return nil
	}
	return r.commit(ctx)
}
func (r *testReader) Close() error {
	if r.close == nil {
		return nil
	}
	return r.close()
}

type testFactory struct {
	reader Reader
	open   func(context.Context, Subscription) (Reader, error)
}

func (f testFactory) Open(ctx context.Context, subscription Subscription) (Reader, error) {
	if f.open != nil {
		return f.open(ctx, subscription)
	}
	return f.reader, nil
}

func testManager(t *testing.T, subscription Subscription, reader Reader, policy RetryPolicy) *Manager {
	t.Helper()
	manager, err := NewManager(ManagerOptions{
		Subscriptions: []Subscription{subscription},
		ReaderFactory: testFactory{reader: reader},
		RetryPolicy:   policy,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func testSubscription(t *testing.T, id string, handler Handler) Subscription {
	t.Helper()
	if handler == nil {
		handler = HandlerFunc(func(context.Context, Record) error { return nil })
	}
	subscription, err := NewSubscription(SubscriptionSpec{ID: id, Group: "group", Topics: []string{"topic"}, Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	return subscription
}

func testBatch(t *testing.T, offsets ...int64) Batch {
	t.Helper()
	records := make([]Record, 0, len(offsets))
	for _, offset := range offsets {
		record, err := NewRecord(RecordSpec{Topic: "topic", Partition: 0, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	batch, err := NewBatch(records)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func receive(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for event")
		return ""
	}
}

func receiveSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for signal")
	}
}

func receiveError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for result")
		return nil
	}
}

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

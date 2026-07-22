package kafka

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"
)

// ManagerOptions are the dependencies and subscriptions owned by one Manager.
type ManagerOptions struct {
	Subscriptions []Subscription
	ReaderFactory ReaderFactory
	RetryPolicy   RetryPolicy
	Logger        *slog.Logger
}

type managerReader struct {
	subscription Subscription
	reader       Reader
	closeOnce    sync.Once
	closeErr     error
}

func (r *managerReader) close() error {
	r.closeOnce.Do(func() { r.closeErr = r.reader.Close() })
	return r.closeErr
}

// Manager runs one worker per subscription. Each worker performs Poll, Handle,
// and Commit sequentially and shares the Manager's cancellation context.
type Manager struct {
	mu sync.Mutex

	state         State
	subscriptions []Subscription
	readerFactory ReaderFactory
	retryPolicy   RetryPolicy
	logger        *slog.Logger
	readers       []*managerReader
	startupCancel context.CancelFunc
	startupDone   chan struct{}
	startupActive bool

	runCtx    context.Context
	runCancel context.CancelFunc
	workers   sync.WaitGroup

	terminal *Error
	done     chan struct{}
	doneOnce sync.Once
	cleanup  sync.Once
}

// NewManager validates and snapshots options without opening resources.
func NewManager(options ManagerOptions) (*Manager, error) {
	if len(options.Subscriptions) == 0 {
		return nil, configurationInvalid(errorKindSubscriptionsEmpty, "/subscriptions")
	}
	seen := make(map[string]struct{}, len(options.Subscriptions))
	for index, subscription := range options.Subscriptions {
		if !subscription.Valid() {
			return nil, configurationInvalid(errorKindSubscriptionInvalid, fmt.Sprintf("/subscriptions/%d", index))
		}
		if _, duplicate := seen[subscription.ID()]; duplicate {
			return nil, configurationInvalid(errorKindSubscriptionDuplicate, fmt.Sprintf("/subscriptions/%d/id", index))
		}
		seen[subscription.ID()] = struct{}{}
	}
	if nilReaderFactory(options.ReaderFactory) {
		return nil, configurationInvalid(errorKindReaderFactoryNil, "/readerFactory")
	}
	if nilRetryPolicy(options.RetryPolicy) {
		return nil, configurationInvalid(errorKindRetryPolicyNil, "/retryPolicy")
	}
	if options.Logger == nil {
		return nil, configurationInvalid(errorKindLoggerNil, "/logger")
	}
	return &Manager{
		state:         StateNew,
		subscriptions: append([]Subscription(nil), options.Subscriptions...),
		readerFactory: options.ReaderFactory,
		retryPolicy:   options.RetryPolicy,
		logger:        options.Logger,
		done:          make(chan struct{}),
		startupDone:   make(chan struct{}),
	}, nil
}

// State returns the current lifecycle state.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Start opens readers in subscription order, then launches one worker each.
func (m *Manager) Start(ctx context.Context) error {
	if ctx == nil {
		return configurationInvalid(errorKindContextNil, "/context")
	}
	m.mu.Lock()
	if m.state != StateNew {
		m.mu.Unlock()
		return newError(errorKindStartConflict, "/state", "", nil)
	}
	startupCtx, startupCancel := context.WithCancel(ctx)
	m.state = StateStarting
	m.startupCancel = startupCancel
	m.startupActive = true
	m.mu.Unlock()

	readers := make([]*managerReader, 0, len(m.subscriptions))
	for _, subscription := range m.subscriptions {
		if closeRequested := m.startupCloseRequested(); closeRequested {
			return m.finishFailedStart(readers, newError(errorKindStartupCanceled, "", subscription.ID(), context.Canceled), true)
		}
		if err := ctx.Err(); err != nil {
			return m.finishFailedStart(readers, newError(errorKindStartupCanceled, "", subscription.ID(), err), false)
		}
		reader, err := m.readerFactory.Open(startupCtx, subscription)
		if closeRequested := m.startupCloseRequested(); closeRequested {
			if err == nil && !nilReader(reader) {
				readers = append(readers, &managerReader{subscription: subscription, reader: reader})
			}
			return m.finishFailedStart(readers, newError(errorKindStartupCanceled, "", subscription.ID(), context.Canceled), true)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			if err == nil && !nilReader(reader) {
				readers = append(readers, &managerReader{subscription: subscription, reader: reader})
			}
			return m.finishFailedStart(readers, newError(errorKindStartupCanceled, "", subscription.ID(), ctxErr), false)
		}
		if err != nil {
			return m.finishFailedStart(readers, newError(errorKindOpenFailed, "", subscription.ID(), nil), false)
		}
		if nilReader(reader) {
			return m.finishFailedStart(readers, newError(errorKindOpenReaderInvalid, "", subscription.ID(), nil), false)
		}
		readers = append(readers, &managerReader{subscription: subscription, reader: reader})
	}
	if err := ctx.Err(); err != nil {
		return m.finishFailedStart(readers, newError(errorKindStartupCanceled, "", "", err), false)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if m.state != StateStarting {
		m.mu.Unlock()
		cancel()
		return m.finishFailedStart(readers, newError(errorKindStartupCanceled, "", "", context.Canceled), true)
	}
	m.readers = readers
	m.runCtx = runCtx
	m.runCancel = cancel
	m.state = StateRunning
	m.workers.Add(len(readers))
	m.mu.Unlock()
	for _, reader := range readers {
		go m.run(reader)
	}
	m.finishStartup()
	return nil
}

func (m *Manager) startupCloseRequested() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state == StateStopping
}

func (m *Manager) finishFailedStart(readers []*managerReader, startErr *Error, closeRequested bool) error {
	closeFailure := m.closeReaders(readers)
	var logged *Error

	m.mu.Lock()
	m.readers = readers
	if closeRequested && closeFailure == nil {
		m.state = StateClosed
	} else {
		if closeFailure != nil {
			m.terminal = closeFailure
		} else {
			m.terminal = startErr
		}
		m.state = StateFailed
		logged = m.terminal
	}
	m.mu.Unlock()
	if logged != nil {
		m.logTerminal(logged)
	}
	m.doneOnce.Do(func() { close(m.done) })
	m.finishStartup()
	return startErr
}

func (m *Manager) finishStartup() {
	m.mu.Lock()
	cancel := m.startupCancel
	m.startupCancel = nil
	m.startupActive = false
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case <-m.startupDone:
	default:
		close(m.startupDone)
	}
}

func (m *Manager) run(reader *managerReader) {
	defer m.workers.Done()
	pollAttempt := 0
	for {
		if m.runCtx.Err() != nil {
			return
		}
		batch, err := reader.reader.Poll(m.runCtx)
		if m.runCtx.Err() != nil {
			return
		}
		if err != nil {
			pollAttempt++
			if !m.retryAttempt(reader.subscription.ID(), StagePoll, pollAttempt, err, errorKindPollFailed) {
				return
			}
			continue
		}
		if !batch.Valid() || batch.Len() == 0 {
			pollAttempt++
			if !m.retryAttempt(reader.subscription.ID(), StagePoll, pollAttempt, newError(errorKindPollBatchInvalid, "", reader.subscription.ID(), nil), errorKindPollBatchInvalid) {
				return
			}
			continue
		}
		pollAttempt = 0

		for _, record := range batch.Records() {
			for attempt := 1; ; attempt++ {
				if m.runCtx.Err() != nil {
					return
				}
				err = reader.subscription.Handler().Handle(m.runCtx, record)
				if m.runCtx.Err() != nil {
					return
				}
				if err == nil {
					break
				}
				if m.runCtx.Err() != nil || !m.retryAttempt(reader.subscription.ID(), StageHandle, attempt, err, errorKindHandleFailed) {
					return
				}
			}
		}

		for attempt := 1; ; attempt++ {
			if m.runCtx.Err() != nil {
				return
			}
			err = reader.reader.Commit(m.runCtx)
			if m.runCtx.Err() != nil {
				return
			}
			if err == nil {
				break
			}
			if m.runCtx.Err() != nil || !m.retryAttempt(reader.subscription.ID(), StageCommit, attempt, err, errorKindCommitFailed) {
				return
			}
		}
	}
}

func (m *Manager) retryAttempt(subscriptionID string, stage Stage, attempt int, cause error, kind errorKind) bool {
	decision, panicked := safeRetryDecision(m.retryPolicy, Failure{stage: stage, subscriptionID: subscriptionID, attempt: attempt, cause: cause})
	if panicked {
		m.publishTerminal(newError(errorKindRetryPolicyPanic, "", subscriptionID, nil))
		return false
	}
	if !validRetryDecision(decision) {
		m.publishTerminal(newError(errorKindRetryDecisionInvalid, "", subscriptionID, nil))
		return false
	}
	if !decision.Retry {
		m.publishTerminal(newError(kind, "", subscriptionID, nil))
		return false
	}
	if decision.Delay == 0 {
		return m.runCtx.Err() == nil
	}
	timer := time.NewTimer(decision.Delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-m.runCtx.Done():
		return false
	}
}

func safeRetryDecision(policy RetryPolicy, failure Failure) (decision RetryDecision, panicked bool) {
	defer func() {
		if recover() != nil {
			decision = RetryDecision{}
			panicked = true
		}
	}()
	return policy.Decide(failure), false
}

func validRetryDecision(decision RetryDecision) bool {
	if decision.Retry {
		return decision.Delay >= 0
	}
	return decision.Delay == 0
}

func (m *Manager) publishTerminal(terminal *Error) {
	m.mu.Lock()
	if m.terminal != nil || (m.state != StateRunning && m.state != StateStopping) {
		m.mu.Unlock()
		return
	}
	m.terminal = terminal
	m.state = StateFailed
	cancel := m.runCancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.logTerminal(terminal)
	m.beginCleanup()
}

func (m *Manager) logTerminal(terminal *Error) {
	attrs := []slog.Attr{slog.String("code", terminal.Code()), slog.String("reason", terminal.Reason())}
	if stage, ok := terminal.Stage(); ok {
		attrs = append(attrs, slog.String("stage", string(stage)))
	}
	if subscriptionID := terminal.SubscriptionID(); subscriptionID != "" {
		attrs = append(attrs, slog.String("subscription_id", subscriptionID))
	}
	m.logger.LogAttrs(context.Background(), slog.LevelError, "kafka manager terminal", attrs...)
}

// Wait waits until Close or terminal worker failure completes cleanup.
func (m *Manager) Wait(ctx context.Context) error {
	if ctx == nil {
		return configurationInvalid(errorKindContextNil, "/context")
	}
	m.mu.Lock()
	if m.state == StateNew {
		m.mu.Unlock()
		return newError(errorKindWaitNotStarted, "/state", "", nil)
	}
	done := m.done
	m.mu.Unlock()
	select {
	case <-done:
		return m.completedResult()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close cancels workers, closes owned readers, and waits within ctx.
func (m *Manager) Close(ctx context.Context) error {
	if ctx == nil {
		return configurationInvalid(errorKindContextNil, "/context")
	}
	m.mu.Lock()
	if m.state == StateClosed {
		m.mu.Unlock()
		return nil
	}
	if m.state == StateNew {
		m.state = StateClosed
		m.mu.Unlock()
		m.doneOnce.Do(func() { close(m.done) })
		return nil
	}
	if m.state == StateStarting {
		m.state = StateStopping
		startupCancel := m.startupCancel
		startupDone := m.startupDone
		done := m.done
		m.mu.Unlock()
		if startupCancel != nil {
			startupCancel()
		}
		if err := waitManagerChannel(ctx, startupDone, true); err != nil {
			return err
		}
		return waitManagerResult(ctx, done, m.completedResult)
	}
	if m.state == StateStopping && m.startupActive {
		startupCancel := m.startupCancel
		startupDone := m.startupDone
		done := m.done
		m.mu.Unlock()
		if startupCancel != nil {
			startupCancel()
		}
		if err := waitManagerChannel(ctx, startupDone, true); err != nil {
			return err
		}
		return waitManagerResult(ctx, done, m.completedResult)
	}
	if m.state == StateRunning {
		m.state = StateStopping
	}
	cancel := m.runCancel
	done := m.done
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.beginCleanup()
	return waitManagerResult(ctx, done, m.completedResult)
}

func waitManagerChannel(ctx context.Context, done <-chan struct{}, closeCall bool) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		if closeCall {
			return newError(errorKindCloseCanceled, "", "", ctx.Err())
		}
		return ctx.Err()
	}
}

func waitManagerResult(ctx context.Context, done <-chan struct{}, result func() error) error {
	if err := waitManagerChannel(ctx, done, true); err != nil {
		return err
	}
	return result()
}

func (m *Manager) beginCleanup() {
	m.cleanup.Do(func() {
		go func() {
			m.mu.Lock()
			readers := append([]*managerReader(nil), m.readers...)
			m.mu.Unlock()
			closeFailure := m.closeReaders(readers)
			m.workers.Wait()
			m.mu.Lock()
			if m.terminal == nil && closeFailure != nil {
				m.terminal = closeFailure
				m.state = StateFailed
			} else if m.terminal == nil {
				m.state = StateClosed
			}
			m.mu.Unlock()
			if closeFailure != nil {
				m.logTerminal(closeFailure)
			}
			m.doneOnce.Do(func() { close(m.done) })
		}()
	})
}

func (m *Manager) closeReaders(readers []*managerReader) *Error {
	var firstFailure *Error
	for _, reader := range readers {
		if reader != nil && reader.reader != nil && reader.close() != nil {
			if firstFailure == nil {
				firstFailure = newError(errorKindReaderCloseFailed, "", reader.subscription.ID(), nil)
			}
		}
	}
	return firstFailure
}

func (m *Manager) completedResult() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.terminal == nil {
		return nil
	}
	return m.terminal
}

func nilReaderFactory(value ReaderFactory) bool { return nilInterface(value) }
func nilRetryPolicy(value RetryPolicy) bool     { return nilInterface(value) }
func nilReader(value Reader) bool               { return nilInterface(value) }

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

package jobapp

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type SchedulerOptions struct {
	Clock         Clock
	Registry      TaskRegistry
	Store         Store
	MaxConcurrent int
}

type scheduledEntry struct {
	spec     ScheduleSpec
	schedule cron.Schedule
}

type Scheduler struct {
	clock    Clock
	registry TaskRegistry
	store    Store
	slots    chan struct{}

	mu            sync.Mutex
	started       bool
	closed        bool
	cancel        context.CancelFunc
	done          chan struct{}
	healthStatus  HealthStatus
	lastErrorCode ErrorCode
	activeRuns    int
	runs          sync.WaitGroup
}

func NewScheduler(options SchedulerOptions) (*Scheduler, error) {
	if nilLike(options.Clock) || nilLike(options.Store) || !options.Registry.valid() || options.MaxConcurrent <= 0 {
		return nil, jobError("scheduler.new", CodeInvalidInput)
	}
	return &Scheduler{
		clock: options.Clock, registry: options.Registry, store: options.Store,
		slots: make(chan struct{}, options.MaxConcurrent), healthStatus: HealthReady,
	}, nil
}

func (s *Scheduler) Start(ctx context.Context) error {
	const operation = "scheduler.start"
	if ctx == nil {
		return jobError(operation, CodeInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return jobError(operation, CodeCanceled)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.closed {
		return jobError(operation, CodeLifecycleConflict)
	}
	entries, err := s.loadSchedules(ctx)
	if err != nil {
		return err
	}
	runContext, cancel := context.WithCancel(ctx)
	s.started = true
	s.cancel = cancel
	s.done = make(chan struct{})
	s.healthStatus = HealthRunning
	go func(done chan struct{}) {
		s.loop(runContext, entries)
		s.mu.Lock()
		s.closed = true
		s.healthStatus = HealthStopped
		s.mu.Unlock()
		s.runs.Wait()
		close(done)
	}(s.done)
	return nil
}

func (s *Scheduler) Close(ctx context.Context) error {
	const operation = "scheduler.close"
	if ctx == nil {
		return jobError(operation, CodeInvalidInput)
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.healthStatus = HealthStopped
		if s.cancel != nil {
			s.cancel()
		}
	}
	done := s.done
	s.mu.Unlock()
	if done == nil {
		done = make(chan struct{})
		go func() {
			s.runs.Wait()
			close(done)
		}()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return jobError(operation, CodeCanceled)
	}
}

func (s *Scheduler) beginRun() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.runs.Add(1)
	return true
}

func (s *Scheduler) loadSchedules(ctx context.Context) ([]scheduledEntry, error) {
	const operation = "scheduler.start"
	specs, err := s.store.Schedules(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, jobError(operation, CodeCanceled)
		}
		return nil, jobError(operation, CodeStoreFailure)
	}
	entries := make([]scheduledEntry, len(specs))
	seen := make(map[string]struct{}, len(specs))
	for index, spec := range specs {
		spec.ID = strings.TrimSpace(spec.ID)
		spec.Cron = strings.TrimSpace(spec.Cron)
		spec.TaskID = strings.TrimSpace(spec.TaskID)
		if spec.ID == "" || spec.Cron == "" || !taskIDPattern.MatchString(spec.TaskID) {
			return nil, jobError(operation, CodeScheduleInvalid)
		}
		if _, duplicate := seen[spec.ID]; duplicate {
			return nil, jobError(operation, CodeScheduleInvalid)
		}
		seen[spec.ID] = struct{}{}
		parsed, parseErr := cron.ParseStandard(spec.Cron)
		if parseErr != nil {
			return nil, jobError(operation, CodeScheduleInvalid)
		}
		spec.Payload = append([]byte(nil), spec.Payload...)
		entries[index] = scheduledEntry{spec: spec, schedule: parsed}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].spec.ID < entries[j].spec.ID })
	return entries, nil
}

func (s *Scheduler) loop(ctx context.Context, entries []scheduledEntry) {
	if len(entries) == 0 {
		<-ctx.Done()
		return
	}
	cursor := s.clock.Now()
	for {
		next := nextScheduled(entries, cursor)
		delay := next.Sub(s.clock.Now())
		if delay < 0 {
			delay = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-s.clock.After(delay):
		}
		now := s.clock.Now()
		if now.Before(next) {
			continue
		}
		for _, entry := range entries {
			for due := entry.schedule.Next(cursor); !due.After(now); due = entry.schedule.Next(due) {
				s.dispatch(ctx, entry, due)
			}
		}
		cursor = now
	}
}

func nextScheduled(entries []scheduledEntry, after time.Time) time.Time {
	next := entries[0].schedule.Next(after)
	for _, entry := range entries[1:] {
		candidate := entry.schedule.Next(after)
		if candidate.Before(next) {
			next = candidate
		}
	}
	return next
}

func (s *Scheduler) dispatch(ctx context.Context, entry scheduledEntry, due time.Time) {
	runID := entry.spec.ID + "@" + due.UTC().Format(time.RFC3339)
	next := entry.schedule.Next(due)
	acquired, err := s.store.AcquireLease(ctx, Lease{Key: runID, RunID: runID, ExpiresAt: next})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			s.backgroundFailure(jobError("scheduler.dispatch", CodeCanceled))
		} else {
			s.backgroundFailure(jobError("scheduler.dispatch", CodeStoreFailure))
		}
		return
	}
	if !acquired || !s.beginRun() {
		return
	}
	go func() {
		defer s.runs.Done()
		_, runErr := s.execute(ctx, entry.spec.ID, TaskID(entry.spec.TaskID), TaskRequest{RunID: runID, Payload: append([]byte(nil), entry.spec.Payload...)})
		if runErr != nil {
			s.backgroundFailure(runErr)
		}
	}()
}

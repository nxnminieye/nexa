package jobapp

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrStoreConflict = errors.New("job store: conflict")

type RunStatus string

const (
	RunRunning   RunStatus = "running"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
	RunCanceled  RunStatus = "canceled"
)

type ScheduleSpec struct {
	ID      string
	Cron    string
	TaskID  string
	Payload []byte
}

type Lease struct {
	Key       string
	RunID     string
	ExpiresAt time.Time
}

type RunRecord struct {
	ID         string
	ScheduleID string
	TaskID     TaskID
	Payload    []byte
	StartedAt  time.Time
}

type RunCompletion struct {
	RunID       string
	Status      RunStatus
	Output      []byte
	ErrorCode   ErrorCode
	CompletedAt time.Time
}

type Store interface {
	Schedules(context.Context) ([]ScheduleSpec, error)
	AcquireLease(context.Context, Lease) (bool, error)
	StartRun(context.Context, RunRecord) error
	FinishRun(context.Context, RunCompletion) error
	DeleteRunsBefore(context.Context, time.Time) (int, error)
}

func (s *Scheduler) Run(ctx context.Context, taskID TaskID, request TaskRequest) (TaskResult, error) {
	if ctx == nil {
		return TaskResult{}, jobError("scheduler.run", CodeInvalidInput)
	}
	if !s.beginRun() {
		return TaskResult{}, jobError("scheduler.run", CodeLifecycleConflict)
	}
	defer s.runs.Done()
	return s.execute(ctx, "", taskID, request)
}

func (s *Scheduler) execute(ctx context.Context, scheduleID string, taskID TaskID, request TaskRequest) (TaskResult, error) {
	const operation = "scheduler.run"
	if err := ctx.Err(); err != nil {
		return TaskResult{}, jobError(operation, CodeCanceled)
	}
	request.RunID = strings.TrimSpace(request.RunID)
	if request.RunID == "" || !taskIDPattern.MatchString(string(taskID)) {
		return TaskResult{}, jobError(operation, CodeInvalidInput)
	}
	handler, exists := s.registry.lookup(taskID)
	if !exists {
		return TaskResult{}, jobError(operation, CodeTaskUnknown)
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return TaskResult{}, jobError(operation, CodeCanceled)
	default:
		return TaskResult{}, jobError(operation, CodeConcurrencyLimit)
	}
	s.changeActive(1)
	defer s.changeActive(-1)

	payload := append([]byte(nil), request.Payload...)
	started := s.clock.Now()
	if err := s.store.StartRun(ctx, RunRecord{
		ID: request.RunID, ScheduleID: scheduleID, TaskID: taskID,
		Payload: append([]byte(nil), payload...), StartedAt: started,
	}); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return TaskResult{}, jobError(operation, CodeCanceled)
		}
		if errors.Is(err, ErrStoreConflict) {
			return TaskResult{}, jobError(operation, CodeRunConflict)
		}
		return TaskResult{}, jobError(operation, CodeStoreFailure)
	}

	result, taskErr := handler.Run(ctx, TaskRequest{RunID: request.RunID, Payload: payload})
	result.Output = append([]byte(nil), result.Output...)
	completion := RunCompletion{RunID: request.RunID, Output: append([]byte(nil), result.Output...), CompletedAt: s.clock.Now(), Status: RunSucceeded}
	var projected error
	if taskErr != nil {
		completion.Status = RunFailed
		completion.ErrorCode = CodeTaskFailed
		projected = jobError(operation, CodeTaskFailed)
		if errors.Is(taskErr, context.Canceled) || errors.Is(taskErr, context.DeadlineExceeded) || ctx.Err() != nil {
			completion.Status = RunCanceled
			completion.ErrorCode = CodeCanceled
			projected = jobError(operation, CodeCanceled)
		}
	}
	if err := s.store.FinishRun(ctx, completion); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return TaskResult{}, jobError(operation, CodeCanceled)
		}
		return TaskResult{}, jobError(operation, CodeStoreFailure)
	}
	if projected != nil {
		return TaskResult{}, projected
	}
	return TaskResult{Output: append([]byte(nil), result.Output...)}, nil
}

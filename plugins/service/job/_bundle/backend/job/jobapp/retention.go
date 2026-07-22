package jobapp

import (
	"context"
	"errors"
	"time"
)

type RetentionPolicy interface {
	Cutoff(time.Time) time.Time
}

type RetentionPolicyFunc func(time.Time) time.Time

func (f RetentionPolicyFunc) Cutoff(now time.Time) time.Time { return f(now) }

func (s *Scheduler) Retain(ctx context.Context, policy RetentionPolicy) (int, error) {
	const operation = "scheduler.retain"
	if ctx == nil || nilLike(policy) {
		return 0, jobError(operation, CodeInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return 0, jobError(operation, CodeCanceled)
	}
	now := s.clock.Now()
	cutoff := policy.Cutoff(now)
	if cutoff.IsZero() || cutoff.After(now) {
		return 0, jobError(operation, CodeInvalidInput)
	}
	count, err := s.store.DeleteRunsBefore(ctx, cutoff)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return 0, jobError(operation, CodeCanceled)
		}
		return 0, jobError(operation, CodeStoreFailure)
	}
	if count < 0 {
		return 0, jobError(operation, CodeStoreFailure)
	}
	return count, nil
}

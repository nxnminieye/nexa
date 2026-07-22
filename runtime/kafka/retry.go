package kafka

import "time"

// Stage identifies one closed Kafka runtime processing stage.
type Stage string

const (
	StageOpen        Stage = "open"
	StagePoll        Stage = "poll"
	StageHandle      Stage = "handle"
	StageCommit      Stage = "commit"
	StageClose       Stage = "close"
	StageRetryPolicy Stage = "retry-policy"
)

// Failure is an immutable synchronous input to a retry policy.
type Failure struct {
	stage          Stage
	subscriptionID string
	attempt        int
	cause          error
}

func (f Failure) Stage() Stage { return f.stage }

func (f Failure) SubscriptionID() string { return f.subscriptionID }

func (f Failure) Attempt() int { return f.attempt }

func (f Failure) Cause() error { return f.cause }

// RetryDecision is the result of a retry policy. A non-retry decision requires
// zero Delay; a retry decision permits any nonnegative Delay.
type RetryDecision struct {
	Retry bool
	Delay time.Duration
}

// RetryPolicy selects whether a synchronous processing failure is retried.
type RetryPolicy interface {
	Decide(Failure) RetryDecision
}

// RetryPolicyFunc adapts a function to RetryPolicy.
type RetryPolicyFunc func(Failure) RetryDecision

func (fn RetryPolicyFunc) Decide(failure Failure) RetryDecision {
	return fn(failure)
}

type noRetryPolicy struct{}

func (noRetryPolicy) Decide(Failure) RetryDecision {
	return RetryDecision{}
}

// NoRetry returns a policy that always returns the exact zero decision.
func NoRetry() RetryPolicy {
	return noRetryPolicy{}
}

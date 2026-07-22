package kafka

import (
	"errors"
	"testing"
	"time"
)

func TestFailurePackageOwnedImmutableAccessors(t *testing.T) {
	cause := errors.New("package fixture cause")
	for _, stage := range []Stage{StagePoll, StageHandle, StageCommit} {
		failure := Failure{stage: stage, subscriptionID: "sample.subscription", attempt: 3, cause: cause}
		if failure.Stage() != stage || failure.SubscriptionID() != "sample.subscription" || failure.Attempt() != 3 || failure.Cause() != cause {
			t.Fatalf("Failure = (%q,%q,%d,%v)", failure.Stage(), failure.SubscriptionID(), failure.Attempt(), failure.Cause())
		}
	}
	zero := Failure{}
	if zero.Stage() != "" || zero.SubscriptionID() != "" || zero.Attempt() != 0 || zero.Cause() != nil {
		t.Fatalf("zero Failure = (%q,%q,%d,%v)", zero.Stage(), zero.SubscriptionID(), zero.Attempt(), zero.Cause())
	}
}

func TestNoRetryReturnsExactZeroDecisionForReachableFailures(t *testing.T) {
	policy := NoRetry()
	if policy == nil {
		t.Fatal("NoRetry() = nil")
	}
	for _, stage := range []Stage{StagePoll, StageHandle, StageCommit} {
		failure := Failure{stage: stage, subscriptionID: "sample.subscription", attempt: 1, cause: errors.New("cause")}
		decision := policy.Decide(failure)
		if decision != (RetryDecision{Retry: false, Delay: 0}) {
			t.Fatalf("NoRetry(%q) = %#v", stage, decision)
		}
	}
}

func TestStageValuesAndRetryDecisionDTO(t *testing.T) {
	wantStages := map[Stage]string{
		StageOpen:        "open",
		StagePoll:        "poll",
		StageHandle:      "handle",
		StageCommit:      "commit",
		StageClose:       "close",
		StageRetryPolicy: "retry-policy",
	}
	for stage, want := range wantStages {
		if string(stage) != want {
			t.Fatalf("stage = %q, want %q", stage, want)
		}
	}

	validDomainExamples := []RetryDecision{
		{},
		{Retry: true, Delay: 0},
		{Retry: true, Delay: time.Nanosecond},
	}
	if validDomainExamples[0].Retry || validDomainExamples[0].Delay != 0 || !validDomainExamples[1].Retry || validDomainExamples[2].Delay != time.Nanosecond {
		t.Fatal("RetryDecision did not preserve the declared DTO values")
	}
}

var (
	_ RetryPolicy = RetryPolicyFunc(nil)
	_ RetryPolicy = NoRetry()
)

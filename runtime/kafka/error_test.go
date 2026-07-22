package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func requireConfigurationError(t *testing.T, err error, reason, pointer string) *Error {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil")
	}
	var kafkaError *Error
	if !errors.As(err, &kafkaError) || kafkaError == nil {
		t.Fatalf("error = %T %v, want *Error", err, err)
	}
	if kafkaError.Code() != "configuration_invalid" || kafkaError.Reason() != reason || kafkaError.Pointer() != pointer {
		t.Fatalf("error tuple = (%q, %q, %q)", kafkaError.Code(), kafkaError.Reason(), kafkaError.Pointer())
	}
	if kafkaError.Error() != ErrConfigurationInvalid.Error() {
		t.Fatalf("Error() = %q", kafkaError.Error())
	}
	if !errors.Is(err, ErrConfigurationInvalid) {
		t.Fatal("error does not match ErrConfigurationInvalid")
	}
	if errors.Unwrap(err) != nil {
		t.Fatalf("Unwrap() = %v, want nil", errors.Unwrap(err))
	}
	if stage, ok := kafkaError.Stage(); ok || stage != "" {
		t.Fatalf("Stage() = (%q, %t), want zero,false", stage, ok)
	}
	if kafkaError.SubscriptionID() != "" {
		t.Fatalf("SubscriptionID() = %q", kafkaError.SubscriptionID())
	}
	return kafkaError
}

func TestErrorNilAndZeroAreSafe(t *testing.T) {
	var nilError *Error
	if nilError.Error() != "" || nilError.Unwrap() != nil || nilError.Is(ErrConfigurationInvalid) || nilError.Is(nil) {
		t.Fatalf("nil error diagnostics = (%q, %v)", nilError.Error(), nilError.Unwrap())
	}
	if nilError.Code() != "" || nilError.Reason() != "" || nilError.Pointer() != "" || nilError.SubscriptionID() != "" {
		t.Fatalf("nil error accessors = (%q,%q,%q,%q)", nilError.Code(), nilError.Reason(), nilError.Pointer(), nilError.SubscriptionID())
	}
	if stage, ok := nilError.Stage(); ok || stage != "" {
		t.Fatalf("nil Stage() = (%q,%t)", stage, ok)
	}

	zero := &Error{}
	if zero.Error() != "runtime kafka error" || zero.Unwrap() != nil || zero.Is(ErrConfigurationInvalid) || zero.Is(context.Canceled) || zero.Is(nil) {
		t.Fatalf("zero error diagnostics = (%q, %v)", zero.Error(), zero.Unwrap())
	}
	if zero.Code() != "" || zero.Reason() != "" || zero.Pointer() != "" || zero.SubscriptionID() != "" {
		t.Fatalf("zero error accessors = (%q,%q,%q,%q)", zero.Code(), zero.Reason(), zero.Pointer(), zero.SubscriptionID())
	}
	if stage, ok := zero.Stage(); ok || stage != "" {
		t.Fatalf("zero Stage() = (%q,%t)", stage, ok)
	}
}

func TestErrorSentinelMatrixAndSafeDiagnostics(t *testing.T) {
	rawCause := errors.New("secret raw cause")
	vectors := []struct {
		name           string
		kind           errorKind
		code           string
		reason         string
		pointer        string
		sentinel       error
		stage          Stage
		subscriptionID string
		cause          error
	}{
		{name: "configuration", kind: errorKindTopicInvalid, code: "configuration_invalid", reason: "topic_invalid", pointer: "/topic", sentinel: ErrConfigurationInvalid, cause: rawCause},
		{name: "lifecycle", kind: errorKindStartConflict, code: "lifecycle_conflict", reason: "start_conflict", pointer: "/state", sentinel: ErrLifecycleConflict, cause: rawCause},
		{name: "reader", kind: errorKindPollFailed, code: "reader_failed", reason: "poll_failed", sentinel: ErrReaderFailed, stage: StagePoll, subscriptionID: "sample.subscription", cause: rawCause},
		{name: "handler", kind: errorKindHandleFailed, code: "handler_failed", reason: "handle_failed", sentinel: ErrHandlerFailed, stage: StageHandle, subscriptionID: "sample.subscription", cause: rawCause},
		{name: "commit", kind: errorKindCommitFailed, code: "commit_failed", reason: "commit_failed", sentinel: ErrCommitFailed, stage: StageCommit, subscriptionID: "sample.subscription", cause: rawCause},
		{name: "close", kind: errorKindReaderCloseFailed, code: "close_failed", reason: "reader_close_failed", sentinel: ErrCloseFailed, stage: StageClose, subscriptionID: "sample.subscription", cause: rawCause},
		{name: "retry policy", kind: errorKindRetryDecisionInvalid, code: "retry_policy_failed", reason: "retry_decision_invalid", sentinel: ErrRetryPolicyFailed, stage: StageRetryPolicy, subscriptionID: "sample.subscription", cause: rawCause},
		{name: "operation canceled", kind: errorKindPollCanceled, code: "operation_canceled", reason: "poll_canceled", sentinel: ErrOperationCanceled, stage: StagePoll, subscriptionID: "sample.subscription", cause: context.Canceled},
	}
	allSentinels := []error{
		ErrConfigurationInvalid,
		ErrLifecycleConflict,
		ErrReaderFailed,
		ErrHandlerFailed,
		ErrCommitFailed,
		ErrCloseFailed,
		ErrRetryPolicyFailed,
		ErrOperationCanceled,
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			errorValue := newError(vector.kind, vector.pointer, vector.subscriptionID, vector.cause)
			if errorValue.Code() != vector.code || errorValue.Reason() != vector.reason || errorValue.Pointer() != vector.pointer || errorValue.SubscriptionID() != vector.subscriptionID {
				t.Fatalf("typed accessors = (%q,%q,%q,%q)", errorValue.Code(), errorValue.Reason(), errorValue.Pointer(), errorValue.SubscriptionID())
			}
			stage, ok := errorValue.Stage()
			if ok != (vector.stage != "") || stage != vector.stage {
				t.Fatalf("Stage() = (%q,%t)", stage, ok)
			}
			if errorValue.Error() != vector.sentinel.Error() {
				t.Fatalf("Error() = %q, want %q", errorValue.Error(), vector.sentinel.Error())
			}
			for _, sentinel := range allSentinels {
				if got, want := errors.Is(errorValue, sentinel), sentinel == vector.sentinel; got != want {
					t.Fatalf("errors.Is(%v) = %t, want %t", sentinel, got, want)
				}
			}
			if vector.sentinel == ErrOperationCanceled {
				if errorValue.Unwrap() != vector.cause || !errors.Is(errorValue, vector.cause) {
					t.Fatalf("context cause = %v", errorValue.Unwrap())
				}
			} else if errorValue.Unwrap() != nil || errors.Is(errorValue, rawCause) || errors.Is(errorValue, context.Canceled) || errors.Is(errorValue, context.DeadlineExceeded) {
				t.Fatalf("unsafe cause escaped: unwrap=%v", errorValue.Unwrap())
			}
			for _, hidden := range []string{vector.reason, vector.pointer, vector.subscriptionID, rawCause.Error()} {
				if hidden != "" && strings.Contains(errorValue.Error(), hidden) {
					t.Fatalf("diagnostic leaked %q: %q", hidden, errorValue.Error())
				}
			}
		})
	}
}

func TestErrorOperationCanceledPreservesOnlyContextCause(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		errorValue := newError(errorKindPollCanceled, "", "sample.subscription", cause)
		if errorValue.Error() != ErrOperationCanceled.Error() || !errors.Is(errorValue, ErrOperationCanceled) || !errors.Is(errorValue, cause) {
			t.Fatalf("operation canceled identity = %q, cause=%v", errorValue.Error(), cause)
		}
		if errorValue.Unwrap() != cause {
			t.Fatalf("Unwrap() = %v, want exact context cause", errorValue.Unwrap())
		}
		other := context.Canceled
		if cause == context.Canceled {
			other = context.DeadlineExceeded
		}
		if errors.Is(errorValue, other) {
			t.Fatalf("error unexpectedly matched %v", other)
		}
	}

	raw := errors.New("secret cancellation lookalike")
	requireFailClosedError(t, newError(errorKindPollCanceled, "", "sample.subscription", raw))
}

func TestErrorSentinelDiagnosticsAreStable(t *testing.T) {
	want := map[error]string{
		ErrConfigurationInvalid: "runtime kafka configuration invalid",
		ErrLifecycleConflict:    "runtime kafka lifecycle conflict",
		ErrReaderFailed:         "runtime kafka reader failed",
		ErrHandlerFailed:        "runtime kafka handler failed",
		ErrCommitFailed:         "runtime kafka commit failed",
		ErrCloseFailed:          "runtime kafka close failed",
		ErrRetryPolicyFailed:    "runtime kafka retry policy failed",
		ErrOperationCanceled:    "runtime kafka operation canceled",
	}
	for sentinel, diagnostic := range want {
		if sentinel.Error() != diagnostic {
			t.Fatalf("sentinel diagnostic = %q, want %q", sentinel.Error(), diagnostic)
		}
	}
}

func TestErrorTask2ConfigurationKindsUseOnlyClosedTuples(t *testing.T) {
	vectors := []struct {
		kind    errorKind
		reason  string
		pointer string
	}{
		{kind: errorKindTopicInvalid, reason: "topic_invalid", pointer: "/topic"},
		{kind: errorKindTopicInvalid, reason: "topic_invalid", pointer: "/topics/3"},
		{kind: errorKindPartitionInvalid, reason: "partition_invalid", pointer: "/partition"},
		{kind: errorKindOffsetInvalid, reason: "offset_invalid", pointer: "/offset"},
		{kind: errorKindHeaderKeyInvalid, reason: "header_key_invalid", pointer: "/headers/4/key"},
		{kind: errorKindBatchEmpty, reason: "batch_empty", pointer: "/records"},
		{kind: errorKindBatchRecordInvalid, reason: "batch_record_invalid", pointer: "/records/2"},
		{kind: errorKindSubscriptionIDInvalid, reason: "subscription_id_invalid", pointer: "/id"},
		{kind: errorKindGroupInvalid, reason: "group_invalid", pointer: "/group"},
		{kind: errorKindTopicsEmpty, reason: "topics_empty", pointer: "/topics"},
		{kind: errorKindTopicDuplicate, reason: "topic_duplicate", pointer: "/topics/2"},
		{kind: errorKindHandlerNil, reason: "handler_nil", pointer: "/handler"},
	}
	for _, vector := range vectors {
		t.Run(vector.reason+vector.pointer, func(t *testing.T) {
			errorValue := configurationInvalid(vector.kind, vector.pointer)
			if errorValue.Code() != "configuration_invalid" || errorValue.Reason() != vector.reason || errorValue.Pointer() != vector.pointer || errorValue.SubscriptionID() != "" || errorValue.Unwrap() != nil || !errors.Is(errorValue, ErrConfigurationInvalid) {
				t.Fatalf("configuration tuple = (%q,%q,%q,%q,%v)", errorValue.Code(), errorValue.Reason(), errorValue.Pointer(), errorValue.SubscriptionID(), errorValue.Unwrap())
			}
			if stage, ok := errorValue.Stage(); ok || stage != "" {
				t.Fatalf("configuration Stage() = (%q,%t)", stage, ok)
			}
		})
	}
}

func TestErrorRejectsUnknownAndIncoherentInternalTuples(t *testing.T) {
	vectors := []struct {
		name       string
		errorValue *Error
	}{
		{
			name:       "unknown discriminant",
			errorValue: newError(errorKind(255), "", "", nil),
		},
		{
			name:       "wrong pointer for closed reason",
			errorValue: newError(errorKindTopicInvalid, "/topic-typo", "", nil),
		},
		{
			name:       "runtime tuple missing subscription",
			errorValue: newError(errorKindPollFailed, "", "", nil),
		},
		{
			name:       "configuration tuple cannot acquire runtime subscription or stage",
			errorValue: newError(errorKindTopicInvalid, "/topic", "sample.subscription", nil),
		},
		{
			name:       "configuration constructor rejects lifecycle kind",
			errorValue: configurationInvalid(errorKindStartConflict, "/state"),
		},
		{
			name:       "noncanonical indexed pointer",
			errorValue: newError(errorKindBatchRecordInvalid, "/records/01", "", nil),
		},
		{
			name:       "directly corrupted tuple",
			errorValue: &Error{kind: errorKindPollFailed, subscriptionID: "sample.subscription", contextCause: context.Canceled},
		},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			requireFailClosedError(t, vector.errorValue)
		})
	}
}

func requireFailClosedError(t *testing.T, errorValue *Error) {
	t.Helper()
	if errorValue == nil {
		t.Fatal("fail-closed error = nil")
	}
	if errorValue.Error() != "runtime kafka error" || errorValue.Code() != "" || errorValue.Reason() != "" || errorValue.Pointer() != "" || errorValue.SubscriptionID() != "" || errorValue.Unwrap() != nil {
		t.Fatalf("fail-closed state = (%q,%q,%q,%q,%q,%v)", errorValue.Error(), errorValue.Code(), errorValue.Reason(), errorValue.Pointer(), errorValue.SubscriptionID(), errorValue.Unwrap())
	}
	if stage, ok := errorValue.Stage(); ok || stage != "" {
		t.Fatalf("fail-closed Stage() = (%q,%t)", stage, ok)
	}
	for _, sentinel := range []error{ErrConfigurationInvalid, ErrLifecycleConflict, ErrReaderFailed, ErrHandlerFailed, ErrCommitFailed, ErrCloseFailed, ErrRetryPolicyFailed, ErrOperationCanceled, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(errorValue, sentinel) {
			t.Fatalf("fail-closed error matched %v", sentinel)
		}
	}
}

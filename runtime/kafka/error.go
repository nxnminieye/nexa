package kafka

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

var (
	ErrConfigurationInvalid = errors.New("runtime kafka configuration invalid")
	ErrLifecycleConflict    = errors.New("runtime kafka lifecycle conflict")
	ErrReaderFailed         = errors.New("runtime kafka reader failed")
	ErrHandlerFailed        = errors.New("runtime kafka handler failed")
	ErrCommitFailed         = errors.New("runtime kafka commit failed")
	ErrCloseFailed          = errors.New("runtime kafka close failed")
	ErrRetryPolicyFailed    = errors.New("runtime kafka retry policy failed")
	ErrOperationCanceled    = errors.New("runtime kafka operation canceled")
)

type errorCode uint8

const (
	errorCodeNone errorCode = iota
	errorCodeConfigurationInvalid
	errorCodeLifecycleConflict
	errorCodeReaderFailed
	errorCodeHandlerFailed
	errorCodeCommitFailed
	errorCodeCloseFailed
	errorCodeRetryPolicyFailed
	errorCodeOperationCanceled
)

func (code errorCode) value() string {
	switch code {
	case errorCodeConfigurationInvalid:
		return "configuration_invalid"
	case errorCodeLifecycleConflict:
		return "lifecycle_conflict"
	case errorCodeReaderFailed:
		return "reader_failed"
	case errorCodeHandlerFailed:
		return "handler_failed"
	case errorCodeCommitFailed:
		return "commit_failed"
	case errorCodeCloseFailed:
		return "close_failed"
	case errorCodeRetryPolicyFailed:
		return "retry_policy_failed"
	case errorCodeOperationCanceled:
		return "operation_canceled"
	default:
		return ""
	}
}

func (code errorCode) sentinel() error {
	switch code {
	case errorCodeConfigurationInvalid:
		return ErrConfigurationInvalid
	case errorCodeLifecycleConflict:
		return ErrLifecycleConflict
	case errorCodeReaderFailed:
		return ErrReaderFailed
	case errorCodeHandlerFailed:
		return ErrHandlerFailed
	case errorCodeCommitFailed:
		return ErrCommitFailed
	case errorCodeCloseFailed:
		return ErrCloseFailed
	case errorCodeRetryPolicyFailed:
		return ErrRetryPolicyFailed
	case errorCodeOperationCanceled:
		return ErrOperationCanceled
	default:
		return nil
	}
}

type errorStage uint8

const (
	errorStageNone errorStage = iota
	errorStageOpen
	errorStagePoll
	errorStageHandle
	errorStageCommit
	errorStageClose
	errorStageRetryPolicy
)

func (stage errorStage) public() (Stage, bool) {
	switch stage {
	case errorStageNone:
		return "", false
	case errorStageOpen:
		return StageOpen, true
	case errorStagePoll:
		return StagePoll, true
	case errorStageHandle:
		return StageHandle, true
	case errorStageCommit:
		return StageCommit, true
	case errorStageClose:
		return StageClose, true
	case errorStageRetryPolicy:
		return StageRetryPolicy, true
	default:
		return "", false
	}
}

// errorKind is the single owner discriminant for a complete code/reason/stage
// tuple. Callers cannot compose those public protocol fields independently.
type errorKind uint8

const (
	errorKindNone errorKind = iota
	errorKindTopicInvalid
	errorKindPartitionInvalid
	errorKindOffsetInvalid
	errorKindHeaderKeyInvalid
	errorKindBatchEmpty
	errorKindBatchRecordInvalid
	errorKindSubscriptionIDInvalid
	errorKindGroupInvalid
	errorKindTopicsEmpty
	errorKindTopicDuplicate
	errorKindHandlerNil
	errorKindContextNil
	errorKindSubscriptionsEmpty
	errorKindSubscriptionInvalid
	errorKindSubscriptionDuplicate
	errorKindReaderFactoryNil
	errorKindRetryPolicyNil
	errorKindLoggerNil
	errorKindStartConflict
	errorKindWaitNotStarted
	errorKindOpenFailed
	errorKindOpenReaderInvalid
	errorKindPollFailed
	errorKindPollBatchInvalid
	errorKindHandleFailed
	errorKindCommitFailed
	errorKindRetryDecisionInvalid
	errorKindRetryPolicyPanic
	errorKindReaderCloseFailed
	errorKindStartupCanceled
	errorKindPollCanceled
	errorKindHandleCanceled
	errorKindCommitCanceled
	errorKindCloseCanceled
)

type errorDefinition struct {
	code   errorCode
	reason string
	stage  errorStage
}

func (kind errorKind) definition() (errorDefinition, bool) {
	switch kind {
	case errorKindTopicInvalid:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "topic_invalid"}, true
	case errorKindPartitionInvalid:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "partition_invalid"}, true
	case errorKindOffsetInvalid:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "offset_invalid"}, true
	case errorKindHeaderKeyInvalid:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "header_key_invalid"}, true
	case errorKindBatchEmpty:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "batch_empty"}, true
	case errorKindBatchRecordInvalid:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "batch_record_invalid"}, true
	case errorKindSubscriptionIDInvalid:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "subscription_id_invalid"}, true
	case errorKindGroupInvalid:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "group_invalid"}, true
	case errorKindTopicsEmpty:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "topics_empty"}, true
	case errorKindTopicDuplicate:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "topic_duplicate"}, true
	case errorKindHandlerNil:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "handler_nil"}, true
	case errorKindContextNil:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "context_nil"}, true
	case errorKindSubscriptionsEmpty:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "subscriptions_empty"}, true
	case errorKindSubscriptionInvalid:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "subscription_invalid"}, true
	case errorKindSubscriptionDuplicate:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "subscription_duplicate"}, true
	case errorKindReaderFactoryNil:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "reader_factory_nil"}, true
	case errorKindRetryPolicyNil:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "retry_policy_nil"}, true
	case errorKindLoggerNil:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "logger_nil"}, true
	case errorKindStartConflict:
		return errorDefinition{code: errorCodeLifecycleConflict, reason: "start_conflict"}, true
	case errorKindWaitNotStarted:
		return errorDefinition{code: errorCodeLifecycleConflict, reason: "wait_not_started"}, true
	case errorKindOpenFailed:
		return errorDefinition{code: errorCodeReaderFailed, reason: "open_failed", stage: errorStageOpen}, true
	case errorKindOpenReaderInvalid:
		return errorDefinition{code: errorCodeReaderFailed, reason: "open_reader_invalid", stage: errorStageOpen}, true
	case errorKindPollFailed:
		return errorDefinition{code: errorCodeReaderFailed, reason: "poll_failed", stage: errorStagePoll}, true
	case errorKindPollBatchInvalid:
		return errorDefinition{code: errorCodeReaderFailed, reason: "poll_batch_invalid", stage: errorStagePoll}, true
	case errorKindHandleFailed:
		return errorDefinition{code: errorCodeHandlerFailed, reason: "handle_failed", stage: errorStageHandle}, true
	case errorKindCommitFailed:
		return errorDefinition{code: errorCodeCommitFailed, reason: "commit_failed", stage: errorStageCommit}, true
	case errorKindRetryDecisionInvalid:
		return errorDefinition{code: errorCodeRetryPolicyFailed, reason: "retry_decision_invalid", stage: errorStageRetryPolicy}, true
	case errorKindRetryPolicyPanic:
		return errorDefinition{code: errorCodeRetryPolicyFailed, reason: "retry_policy_panic", stage: errorStageRetryPolicy}, true
	case errorKindReaderCloseFailed:
		return errorDefinition{code: errorCodeCloseFailed, reason: "reader_close_failed", stage: errorStageClose}, true
	case errorKindStartupCanceled:
		return errorDefinition{code: errorCodeOperationCanceled, reason: "startup_canceled", stage: errorStageOpen}, true
	case errorKindPollCanceled:
		return errorDefinition{code: errorCodeOperationCanceled, reason: "poll_canceled", stage: errorStagePoll}, true
	case errorKindHandleCanceled:
		return errorDefinition{code: errorCodeOperationCanceled, reason: "handle_canceled", stage: errorStageHandle}, true
	case errorKindCommitCanceled:
		return errorDefinition{code: errorCodeOperationCanceled, reason: "commit_canceled", stage: errorStageCommit}, true
	case errorKindCloseCanceled:
		return errorDefinition{code: errorCodeOperationCanceled, reason: "close_canceled", stage: errorStageClose}, true
	default:
		return errorDefinition{}, false
	}
}

// Error is the stable, transport-neutral Kafka failure projection.
type Error struct {
	kind           errorKind
	pointer        string
	subscriptionID string
	contextCause   error
}

func (e *Error) Error() string {
	definition, ok := e.validDefinition()
	if e == nil {
		return ""
	}
	if !ok {
		return "runtime kafka error"
	}
	return definition.code.sentinel().Error()
}

// Unwrap exposes only an exact context cancellation cause.
func (e *Error) Unwrap() error {
	definition, ok := e.validDefinition()
	if !ok || definition.code != errorCodeOperationCanceled {
		return nil
	}
	return e.contextCause
}

// Is matches the error's code sentinel and its exact context cause, if any.
func (e *Error) Is(target error) bool {
	if target == nil {
		return false
	}
	definition, ok := e.validDefinition()
	if !ok {
		return false
	}
	return target == definition.code.sentinel() || target == e.contextCause
}

func (e *Error) Code() string {
	definition, ok := e.validDefinition()
	if !ok {
		return ""
	}
	return definition.code.value()
}

func (e *Error) Reason() string {
	definition, ok := e.validDefinition()
	if !ok {
		return ""
	}
	return definition.reason
}

func (e *Error) Pointer() string {
	if _, ok := e.validDefinition(); !ok {
		return ""
	}
	return e.pointer
}

func (e *Error) Stage() (Stage, bool) {
	definition, ok := e.validDefinition()
	if !ok {
		return "", false
	}
	return definition.stage.public()
}

func (e *Error) SubscriptionID() string {
	if _, ok := e.validDefinition(); !ok {
		return ""
	}
	return e.subscriptionID
}

func (e *Error) validDefinition() (errorDefinition, bool) {
	if e == nil {
		return errorDefinition{}, false
	}
	definition, ok := e.kind.definition()
	if !ok || !validErrorPointer(e.kind, e.pointer) || !validErrorSubscription(e.kind, e.subscriptionID) {
		return errorDefinition{}, false
	}
	if definition.code == errorCodeOperationCanceled {
		if e.contextCause != context.Canceled && e.contextCause != context.DeadlineExceeded {
			return errorDefinition{}, false
		}
	} else if e.contextCause != nil {
		return errorDefinition{}, false
	}
	return definition, true
}

func newError(kind errorKind, pointer, subscriptionID string, cause error) *Error {
	errorValue := &Error{kind: kind, pointer: pointer, subscriptionID: subscriptionID}
	definition, ok := kind.definition()
	if ok && definition.code == errorCodeOperationCanceled && (cause == context.Canceled || cause == context.DeadlineExceeded) {
		errorValue.contextCause = cause
	}
	if _, ok := errorValue.validDefinition(); !ok {
		return &Error{}
	}
	return errorValue
}

func configurationInvalid(kind errorKind, pointer string) *Error {
	definition, ok := kind.definition()
	if !ok || definition.code != errorCodeConfigurationInvalid || definition.stage != errorStageNone {
		return &Error{}
	}
	return newError(kind, pointer, "", nil)
}

func validErrorPointer(kind errorKind, pointer string) bool {
	switch kind {
	case errorKindTopicInvalid:
		return pointer == "/topic" || indexedPointer(pointer, "/topics/", "")
	case errorKindPartitionInvalid:
		return pointer == "/partition"
	case errorKindOffsetInvalid:
		return pointer == "/offset"
	case errorKindHeaderKeyInvalid:
		return indexedPointer(pointer, "/headers/", "/key")
	case errorKindBatchEmpty:
		return pointer == "/records"
	case errorKindBatchRecordInvalid:
		return indexedPointer(pointer, "/records/", "")
	case errorKindSubscriptionIDInvalid:
		return pointer == "/id"
	case errorKindGroupInvalid:
		return pointer == "/group"
	case errorKindTopicsEmpty:
		return pointer == "/topics"
	case errorKindTopicDuplicate:
		return indexedPointer(pointer, "/topics/", "")
	case errorKindHandlerNil:
		return pointer == "/handler"
	case errorKindContextNil:
		return pointer == "/context"
	case errorKindSubscriptionsEmpty:
		return pointer == "/subscriptions"
	case errorKindSubscriptionInvalid:
		return indexedPointer(pointer, "/subscriptions/", "")
	case errorKindSubscriptionDuplicate:
		return indexedPointer(pointer, "/subscriptions/", "/id")
	case errorKindReaderFactoryNil:
		return pointer == "/readerFactory"
	case errorKindRetryPolicyNil:
		return pointer == "/retryPolicy"
	case errorKindLoggerNil:
		return pointer == "/logger"
	case errorKindStartConflict, errorKindWaitNotStarted:
		return pointer == "/state"
	case errorKindOpenFailed, errorKindOpenReaderInvalid, errorKindPollFailed, errorKindPollBatchInvalid,
		errorKindHandleFailed, errorKindCommitFailed, errorKindRetryDecisionInvalid, errorKindRetryPolicyPanic,
		errorKindReaderCloseFailed, errorKindStartupCanceled, errorKindPollCanceled, errorKindHandleCanceled,
		errorKindCommitCanceled, errorKindCloseCanceled:
		return pointer == ""
	default:
		return false
	}
}

func validErrorSubscription(kind errorKind, subscriptionID string) bool {
	switch kind {
	case errorKindOpenFailed, errorKindOpenReaderInvalid, errorKindPollFailed, errorKindPollBatchInvalid,
		errorKindHandleFailed, errorKindCommitFailed, errorKindRetryDecisionInvalid, errorKindRetryPolicyPanic,
		errorKindReaderCloseFailed, errorKindPollCanceled, errorKindHandleCanceled, errorKindCommitCanceled:
		return validSubscriptionID(subscriptionID)
	case errorKindStartupCanceled:
		return subscriptionID == "" || validSubscriptionID(subscriptionID)
	case errorKindCloseCanceled, errorKindTopicInvalid, errorKindPartitionInvalid, errorKindOffsetInvalid,
		errorKindHeaderKeyInvalid, errorKindBatchEmpty, errorKindBatchRecordInvalid, errorKindSubscriptionIDInvalid,
		errorKindGroupInvalid, errorKindTopicsEmpty, errorKindTopicDuplicate, errorKindHandlerNil, errorKindContextNil,
		errorKindSubscriptionsEmpty, errorKindSubscriptionInvalid, errorKindSubscriptionDuplicate,
		errorKindReaderFactoryNil, errorKindRetryPolicyNil, errorKindLoggerNil, errorKindStartConflict,
		errorKindWaitNotStarted:
		return subscriptionID == ""
	default:
		return false
	}
}

func indexedPointer(pointer, prefix, suffix string) bool {
	if !strings.HasPrefix(pointer, prefix) || !strings.HasSuffix(pointer, suffix) {
		return false
	}
	index := strings.TrimSuffix(strings.TrimPrefix(pointer, prefix), suffix)
	if index == "" || (len(index) > 1 && index[0] == '0') {
		return false
	}
	_, err := strconv.ParseUint(index, 10, strconv.IntSize)
	return err == nil
}

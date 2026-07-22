// Package franz adapts franz-go clients to Nexa's transport-neutral Kafka ports.
package franz

import (
	"context"
	"errors"

	"github.com/nxnminieye/nexa/runtime/kafka"
)

var (
	ErrConfigurationInvalid = errors.New("franz configuration invalid")
	ErrOpenFailed           = errors.New("franz open failed")
	ErrReaderState          = errors.New("franz reader state invalid")
	ErrFetchFailed          = errors.New("franz fetch failed")
	ErrCommitFailed         = errors.New("franz commit failed")
	ErrProducerState        = errors.New("franz producer state invalid")
	ErrDeliveryFailed       = errors.New("franz delivery failed")
	ErrFlushFailed          = errors.New("franz flush failed")
	ErrOperationCanceled    = errors.New("franz operation canceled")
)

type errorCode uint8

const (
	errorCodeNone errorCode = iota
	errorCodeConfigurationInvalid
	errorCodeOpenFailed
	errorCodeReaderState
	errorCodeFetchFailed
	errorCodeCommitFailed
	errorCodeProducerState
	errorCodeDeliveryFailed
	errorCodeFlushFailed
	errorCodeOperationCanceled
)

func (code errorCode) value() string {
	switch code {
	case errorCodeConfigurationInvalid:
		return "franz_configuration_invalid"
	case errorCodeOpenFailed:
		return "franz_open_failed"
	case errorCodeReaderState:
		return "franz_reader_state"
	case errorCodeFetchFailed:
		return "franz_fetch_failed"
	case errorCodeCommitFailed:
		return "franz_commit_failed"
	case errorCodeProducerState:
		return "franz_producer_state"
	case errorCodeDeliveryFailed:
		return "franz_delivery_failed"
	case errorCodeFlushFailed:
		return "franz_flush_failed"
	case errorCodeOperationCanceled:
		return "franz_operation_canceled"
	default:
		return ""
	}
}

func (code errorCode) sentinel() error {
	switch code {
	case errorCodeConfigurationInvalid:
		return ErrConfigurationInvalid
	case errorCodeOpenFailed:
		return ErrOpenFailed
	case errorCodeReaderState:
		return ErrReaderState
	case errorCodeFetchFailed:
		return ErrFetchFailed
	case errorCodeCommitFailed:
		return ErrCommitFailed
	case errorCodeProducerState:
		return ErrProducerState
	case errorCodeDeliveryFailed:
		return ErrDeliveryFailed
	case errorCodeFlushFailed:
		return ErrFlushFailed
	case errorCodeOperationCanceled:
		return ErrOperationCanceled
	default:
		return nil
	}
}

type errorKind uint8

const (
	errorKindNone errorKind = iota
	errorKindBrokersEmpty
	errorKindBrokerInvalid
	errorKindBrokerDuplicate
	errorKindMaxPollRecordsInvalid
	errorKindClientOptionNil
	errorKindContextNil
	errorKindSubscriptionInvalid
	errorKindMessageRequired
	errorKindClientOpenFailed
	errorKindPollPending
	errorKindCommitWithoutPoll
	errorKindReaderClosed
	errorKindFetchFailed
	errorKindRecordInvalid
	errorKindCommitFailed
	errorKindProducerClosed
	errorKindDeliveryFailed
	errorKindFlushFailed
	errorKindContextCanceled
	errorKindContextDeadline
)

type errorDefinition struct {
	code   errorCode
	reason string
	stage  kafka.Stage
}

func (kind errorKind) definition() (errorDefinition, bool) {
	switch kind {
	case errorKindBrokersEmpty:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "brokers_empty"}, true
	case errorKindBrokerInvalid:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "broker_invalid"}, true
	case errorKindBrokerDuplicate:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "broker_duplicate"}, true
	case errorKindMaxPollRecordsInvalid:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "max_poll_records_invalid"}, true
	case errorKindClientOptionNil:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "client_option_nil"}, true
	case errorKindContextNil:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "context_nil"}, true
	case errorKindSubscriptionInvalid:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "subscription_invalid"}, true
	case errorKindMessageRequired:
		return errorDefinition{code: errorCodeConfigurationInvalid, reason: "message_required"}, true
	case errorKindClientOpenFailed:
		return errorDefinition{code: errorCodeOpenFailed, reason: "client_open_failed", stage: kafka.StageOpen}, true
	case errorKindPollPending:
		return errorDefinition{code: errorCodeReaderState, reason: "poll_pending", stage: kafka.StagePoll}, true
	case errorKindCommitWithoutPoll:
		return errorDefinition{code: errorCodeReaderState, reason: "commit_without_poll", stage: kafka.StageCommit}, true
	case errorKindReaderClosed:
		return errorDefinition{code: errorCodeReaderState, reason: "reader_closed"}, true
	case errorKindFetchFailed:
		return errorDefinition{code: errorCodeFetchFailed, reason: "fetch_failed", stage: kafka.StagePoll}, true
	case errorKindRecordInvalid:
		return errorDefinition{code: errorCodeFetchFailed, reason: "record_invalid", stage: kafka.StagePoll}, true
	case errorKindCommitFailed:
		return errorDefinition{code: errorCodeCommitFailed, reason: "commit_failed", stage: kafka.StageCommit}, true
	case errorKindProducerClosed:
		return errorDefinition{code: errorCodeProducerState, reason: "producer_closed"}, true
	case errorKindDeliveryFailed:
		return errorDefinition{code: errorCodeDeliveryFailed, reason: "delivery_failed"}, true
	case errorKindFlushFailed:
		return errorDefinition{code: errorCodeFlushFailed, reason: "flush_failed", stage: kafka.StageClose}, true
	case errorKindContextCanceled:
		return errorDefinition{code: errorCodeOperationCanceled, reason: "context_canceled"}, true
	case errorKindContextDeadline:
		return errorDefinition{code: errorCodeOperationCanceled, reason: "context_deadline"}, true
	default:
		return errorDefinition{}, false
	}
}

// Error is a safe adapter-local failure projection. It never exposes broker,
// authentication, fetch, delivery, or flush cause text.
type Error struct {
	kind         errorKind
	pointer      string
	stage        kafka.Stage
	contextCause error
}

func (e *Error) Error() string {
	definition, ok := e.definition()
	if !ok {
		return "franz adapter error"
	}
	return definition.code.sentinel().Error()
}

// Unwrap exposes only the exact standard context cancellation cause.
func (e *Error) Unwrap() error {
	definition, ok := e.definition()
	if !ok || definition.code != errorCodeOperationCanceled {
		return nil
	}
	return e.contextCause
}

// Is matches the stable code sentinel and the exact context cause when present.
func (e *Error) Is(target error) bool {
	definition, ok := e.definition()
	if !ok || target == nil {
		return false
	}
	return target == definition.code.sentinel() || target == e.contextCause
}

func (e *Error) Code() string {
	definition, ok := e.definition()
	if !ok {
		return ""
	}
	return definition.code.value()
}

func (e *Error) Reason() string {
	definition, ok := e.definition()
	if !ok {
		return ""
	}
	return definition.reason
}

func (e *Error) Pointer() string {
	if _, ok := e.definition(); !ok {
		return ""
	}
	return e.pointer
}

func (e *Error) Stage() (kafka.Stage, bool) {
	definition, ok := e.definition()
	if !ok {
		return "", false
	}
	stage := e.stage
	if stage == "" {
		stage = definition.stage
	}
	return stage, stage != ""
}

func (e *Error) definition() (errorDefinition, bool) {
	if e == nil {
		return errorDefinition{}, false
	}
	return e.kind.definition()
}

func adapterError(kind errorKind, pointer string, stage kafka.Stage, cause error) error {
	errorValue := &Error{kind: kind, pointer: pointer, stage: stage}
	if kind == errorKindContextCanceled {
		errorValue.contextCause = context.Canceled
	}
	if kind == errorKindContextDeadline {
		errorValue.contextCause = context.DeadlineExceeded
	}
	_ = cause
	return errorValue
}

func operationError(cause error, stage kafka.Stage, fallback errorKind) error {
	switch {
	case errors.Is(cause, context.Canceled):
		return adapterError(errorKindContextCanceled, "", stage, context.Canceled)
	case errors.Is(cause, context.DeadlineExceeded):
		return adapterError(errorKindContextDeadline, "", stage, context.DeadlineExceeded)
	default:
		return adapterError(fallback, "", "", cause)
	}
}

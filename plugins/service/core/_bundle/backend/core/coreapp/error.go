package coreapp

import (
	"errors"
)

var (
	ErrIdentityRejected        = errors.New("core identity: rejected")
	ErrTenantAdmissionRejected = errors.New("core tenant admission: rejected")
)

type ErrorCode string

const (
	CodeInvalidInput          ErrorCode = "invalid_input"
	CodeConflict              ErrorCode = "conflict"
	CodeInvalidCredentials    ErrorCode = "invalid_credentials"
	CodeSessionExpired        ErrorCode = "session_expired"
	CodeSessionReplayed       ErrorCode = "session_replayed"
	CodeCapabilityUnavailable ErrorCode = "capability_unavailable"
	CodeProviderFailure       ErrorCode = "provider_failure"
	CodeCanceled              ErrorCode = "canceled"
	CodeStoreFailure          ErrorCode = "store_failure"
)

type Error struct {
	Operation string
	Code      ErrorCode
}

func (e *Error) Error() string {
	if e.Operation == "" {
		return string(e.Code)
	}
	return e.Operation + ": " + string(e.Code)
}

func CodeOf(err error) ErrorCode {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

func coreError(operation string, code ErrorCode, _ error) error {
	return &Error{Operation: operation, Code: code}
}

func invalid(operation string) error {
	return coreError(operation, CodeInvalidInput, nil)
}

func canceled(operation string, err error) error {
	return coreError(operation, CodeCanceled, err)
}

func storeFailure(operation string, err error) error {
	return coreError(operation, CodeStoreFailure, err)
}

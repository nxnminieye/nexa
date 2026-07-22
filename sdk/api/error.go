package api

import (
	"errors"

	"github.com/nxnminieye/nexa/cli/protocol"
)

const sdkErrorDomain = "nexa.sdk.api"

const (
	codeClientInvalid           = "client_invalid"
	codeAPIManifestRequired     = "api_manifest_required"
	codeOperationNotFound       = "operation_not_found"
	codeRequestInvalid          = "request_invalid"
	codeCredentialProviderError = "credential_provider_error"
	codeTransportError          = "transport_error"
	codeRemoteProtocolError     = "remote_protocol_error"
	codeRemoteErrorUnmapped     = "remote_error_unmapped"
	codeOperationCanceled       = "operation_canceled"
)

const (
	credentialProviderFailureReason  = "provider_failed"
	credentialProviderFailureMessage = "credential provider failed"
)

var errSDKAPI = errors.New("runtime API error")

// Error is a stable SDK error projection.
type Error struct {
	code           string
	domain         string
	category       protocol.Category
	message        string
	retryable      bool
	apiOperationID string
	requestID      string
	traceID        string
	details        ErrorDetails
	sentinel       error
}

// ErrorDetails contains the closed set of structured SDK error details.
type ErrorDetails struct {
	reason       string
	pointer      string
	httpStatus   int
	remoteDomain string
	remoteCode   string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.sentinel
}

func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}

func (e *Error) Domain() string {
	if e == nil {
		return ""
	}
	return e.domain
}

func (e *Error) Category() protocol.Category {
	if e == nil {
		return ""
	}
	return e.category
}

func (e *Error) Retryable() bool { return e != nil && e.retryable }

func (e *Error) APIOperationID() string {
	if e == nil {
		return ""
	}
	return e.apiOperationID
}

func (e *Error) RequestID() string {
	if e == nil {
		return ""
	}
	return e.requestID
}

func (e *Error) TraceID() string {
	if e == nil {
		return ""
	}
	return e.traceID
}

func (e *Error) Details() ErrorDetails {
	if e == nil {
		return ErrorDetails{}
	}
	return e.details
}

func (d ErrorDetails) Reason() string       { return d.reason }
func (d ErrorDetails) Pointer() string      { return d.pointer }
func (d ErrorDetails) HTTPStatus() int      { return d.httpStatus }
func (d ErrorDetails) RemoteDomain() string { return d.remoteDomain }
func (d ErrorDetails) RemoteCode() string   { return d.remoteCode }

func newSDKError(code, domain string, category protocol.Category, message string, details ErrorDetails) *Error {
	return &Error{
		code:      code,
		domain:    domain,
		category:  category,
		message:   message,
		details:   details,
		sentinel:  errSDKAPI,
		retryable: false,
	}
}

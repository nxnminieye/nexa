package coreapp

type TransportError struct {
	Code    string
	Message string
}

func (e *TransportError) Error() string {
	if e == nil || e.Code == "" {
		return "internal_error"
	}
	return e.Code
}

func ProjectTransportError(err error) *TransportError {
	if err == nil {
		return nil
	}
	code := CodeOf(err)
	statusCode := "internal"
	switch code {
	case CodeInvalidInput:
		statusCode = "invalid_argument"
	case CodeInvalidCredentials, CodeSessionExpired, CodeSessionReplayed:
		statusCode = "unauthenticated"
	case CodePermissionDenied:
		statusCode = "permission_denied"
	case CodeNotFound:
		statusCode = "not_found"
	case CodeConflict, CodeConcurrentWrite:
		statusCode = "aborted"
	case CodeFailedPrecondition:
		statusCode = "failed_precondition"
	case CodeCapabilityUnavailable, CodeProviderFailure:
		statusCode = "unavailable"
	case CodeCanceled:
		statusCode = "canceled"
	}
	message := string(code)
	if message == "" {
		message = "internal_error"
	}
	return &TransportError{Code: statusCode, Message: message}
}

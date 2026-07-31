package api

import "net/http"

func ProjectRPCError(code string) error {
	httpStatus := http.StatusInternalServerError
	messageCode := "internal_error"
	switch code {
	case "InvalidArgument", "FailedPrecondition", "OutOfRange":
		httpStatus, messageCode = http.StatusBadRequest, "invalid_input"
	case "Unauthenticated":
		httpStatus, messageCode = http.StatusUnauthorized, "invalid_credentials"
	case "PermissionDenied":
		httpStatus, messageCode = http.StatusForbidden, "permission_denied"
	case "NotFound":
		httpStatus, messageCode = http.StatusNotFound, "not_found"
	case "AlreadyExists", "Aborted":
		httpStatus, messageCode = http.StatusConflict, "conflict"
	case "ResourceExhausted":
		httpStatus, messageCode = http.StatusTooManyRequests, "rate_limited"
	case "Canceled":
		httpStatus, messageCode = http.StatusRequestTimeout, "canceled"
	case "Unimplemented":
		httpStatus, messageCode = http.StatusNotImplemented, "not_implemented"
	case "Unavailable":
		httpStatus, messageCode = http.StatusServiceUnavailable, "service_unavailable"
	case "DeadlineExceeded":
		httpStatus, messageCode = http.StatusGatewayTimeout, "deadline_exceeded"
	}
	return &StatusError{Status: httpStatus, Code: messageCode, Message: messageCode}
}

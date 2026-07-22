package crudlogic

import (
	"errors"

	"google.golang.org/grpc/codes"
)

var errInvalid = errors.New("crud logic plan invalid")

type Error struct {
	reason, pointer string
	cause           error
}

func (e *Error) Error() string { return "crud logic: " + e.reason }
func (e *Error) Unwrap() error {
	if e.cause != nil {
		return errors.Join(errInvalid, e.cause)
	}
	return errInvalid
}
func (e *Error) Code() string    { return "crud_logic_invalid" }
func (e *Error) Stage() string   { return "plan" }
func (e *Error) Reason() string  { return e.reason }
func (e *Error) Pointer() string { return e.pointer }

func invalid(reason, pointer string, cause error) error {
	return &Error{reason: reason, pointer: pointer, cause: cause}
}

type statusProjection struct {
	Code    codes.Code
	Message string
}

func frozenErrorTable() []statusProjection {
	return []statusProjection{
		{codes.InvalidArgument, "invalid identity"},
		{codes.InvalidArgument, "invalid pagination"},
		{codes.InvalidArgument, "update_mask is required"},
		{codes.InvalidArgument, "update_mask contains unsupported field"},
		{codes.Unauthenticated, "tenant context is required"},
		{codes.NotFound, "entity not found"},
		{codes.InvalidArgument, "invalid field value"},
		{codes.FailedPrecondition, "constraint violation"},
		{codes.Internal, "crud operation failed"},
	}
}

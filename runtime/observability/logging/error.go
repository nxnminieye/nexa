// Package logging provides transport-neutral slog attributes and redaction.
package logging

// Error is the stable, safe failure projection for this package.
type Error struct {
	reason  string
	pointer string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return "logging invalid"
}

// Code returns the stable error category.
func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return "logging_invalid"
}

// Reason returns the stable error reason.
func (e *Error) Reason() string {
	if e == nil {
		return ""
	}
	return e.reason
}

// Pointer identifies the invalid input field.
func (e *Error) Pointer() string {
	if e == nil {
		return ""
	}
	return e.pointer
}

func invalid(reason, pointer string) *Error {
	return &Error{reason: reason, pointer: pointer}
}

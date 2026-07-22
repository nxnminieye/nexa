package artifact

import (
	"errors"
	"fmt"
)

var (
	errArtifactManifestInvalid = errors.New("artifact manifest invalid")
	errArtifactDigestMismatch  = errors.New("artifact digest mismatch")
)

type Error struct {
	code, reason, source, pointer string
	line, column                  int
	message                       string
	sentinel                      error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	location := e.source
	if e.line > 0 {
		location = fmt.Sprintf("%s:%d:%d", location, e.line, e.column)
	}
	if e.pointer != "" {
		location += e.pointer
	}
	if location == "" {
		return e.message
	}
	return location + ": " + e.message
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
func (e *Error) Reason() string {
	if e == nil {
		return ""
	}
	return e.reason
}
func (e *Error) Source() string {
	if e == nil {
		return ""
	}
	return e.source
}
func (e *Error) Pointer() string {
	if e == nil {
		return ""
	}
	return e.pointer
}
func (e *Error) Line() int {
	if e == nil {
		return 0
	}
	return e.line
}
func (e *Error) Column() int {
	if e == nil {
		return 0
	}
	return e.column
}

func newArtifactError(code, reason, source, pointer, message string) *Error {
	sentinel := errArtifactManifestInvalid
	if code == "artifact_digest_mismatch" {
		sentinel = errArtifactDigestMismatch
	}
	return &Error{code: code, reason: reason, source: source, pointer: pointer, message: message, sentinel: sentinel}
}

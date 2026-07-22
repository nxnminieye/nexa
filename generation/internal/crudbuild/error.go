package crudbuild

import "errors"

var (
	errBuild         = errors.New("invalid CRUD protocol build")
	errWire          = errors.New("invalid CRUD wire contract")
	errLock          = errors.New("invalid CRUD compatibility lock")
	errCompatibility = errors.New("CRUD wire compatibility failed")
	errRender        = errors.New("invalid CRUD Proto render")
	errCompile       = errors.New("CRUD Proto compilation failed")
)

type Error struct {
	code, stage, reason, pointer, source string
	sentinel                             error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.sentinel.Error()
}
func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}
func (e *Error) Stage() string {
	if e == nil {
		return ""
	}
	return e.stage
}
func (e *Error) Reason() string {
	if e == nil {
		return ""
	}
	return e.reason
}
func (e *Error) Pointer() string {
	if e == nil {
		return ""
	}
	return e.pointer
}
func (e *Error) Source() string {
	if e == nil {
		return ""
	}
	return e.source
}
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.sentinel
}

func buildError(reason, pointer string) *Error {
	return &Error{code: "crud_build_invalid", stage: "build", reason: reason, pointer: pointer, sentinel: errBuild}
}
func wireError(reason, pointer string) *Error {
	return &Error{code: "crud_wire_invalid", stage: "wire", reason: reason, pointer: pointer, sentinel: errWire}
}
func lockError(reason, pointer, source string) *Error {
	return &Error{code: "crud_lock_invalid", stage: "lock-decode", reason: reason, pointer: pointer, source: source, sentinel: errLock}
}
func compatibilityError(reason, pointer string) *Error {
	return &Error{code: "crud_compatibility_failed", stage: "compatibility", reason: reason, pointer: pointer, sentinel: errCompatibility}
}
func renderError(reason, pointer string) *Error {
	return &Error{code: "crud_render_invalid", stage: "render", reason: reason, pointer: pointer, sentinel: errRender}
}
func compileError() *Error {
	return &Error{code: "crud_proto_compile_failed", stage: "compile", reason: "proto_compile_failed", pointer: "/protoArtifact/bytes", sentinel: errCompile}
}

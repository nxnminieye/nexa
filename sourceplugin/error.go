package sourceplugin

type ErrorClass uint8

const (
	ErrManifestInvalid ErrorClass = iota + 1
	ErrProfileNotFound
	ErrProfileCycle
	ErrTreeInvalid
	ErrTreeLoadFailed
	ErrProviderInvalid
	ErrContractInternal
)

func (c ErrorClass) Error() string {
	switch c {
	case ErrManifestInvalid:
		return "source bundle manifest is invalid"
	case ErrProfileNotFound:
		return "source bundle profile was not found"
	case ErrProfileCycle:
		return "source bundle profile graph contains a cycle"
	case ErrTreeInvalid:
		return "source tree is invalid"
	case ErrTreeLoadFailed:
		return "source tree could not be loaded"
	case ErrProviderInvalid:
		return "source provider is invalid"
	case ErrContractInternal:
		return "source contract projection failed"
	default:
		return ""
	}
}

type Error struct {
	class   ErrorClass
	code    string
	reason  string
	source  string
	pointer string
	line    int
	column  int
	cycle   []string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.class.Error()
}

func (e *Error) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	class, ok := target.(ErrorClass)
	if !ok {
		return false
	}
	if e.class == class {
		return true
	}
	return (e.class == ErrProfileCycle && class == ErrManifestInvalid) ||
		(e.class == ErrTreeLoadFailed && class == ErrTreeInvalid)
}

func (e *Error) Class() ErrorClass {
	if e == nil {
		return 0
	}
	return e.class
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

func (e *Error) Cycle() []string {
	if e == nil || e.cycle == nil {
		return nil
	}
	return append([]string(nil), e.cycle...)
}

func newSourceError(code, reason, pointer string) *Error {
	class := ErrManifestInvalid
	if code == "source_profile_not_found" {
		class = ErrProfileNotFound
	} else if code == "source_profile_cycle" {
		class = ErrProfileCycle
	}
	return &Error{class: class, code: code, reason: reason, pointer: pointer}
}

func newTreeError(code, reason, pointer string) *Error {
	return &Error{class: ErrTreeInvalid, code: code, reason: reason, pointer: pointer}
}

func newTreeLoadError(reason, pointer string) *Error {
	return &Error{class: ErrTreeLoadFailed, code: "source_tree_load_failed", reason: reason, pointer: pointer}
}

func newProviderError(reason, pointer string) *Error {
	return &Error{class: ErrProviderInvalid, code: "source_provider_invalid", reason: reason, pointer: pointer}
}

func newContractInternal(reason, pointer string) *Error {
	return &Error{class: ErrContractInternal, code: "source_contract_internal", reason: reason, pointer: pointer}
}

func withLocation(err *Error, source string, line, column int) *Error {
	if err == nil {
		return nil
	}
	copyError := *err
	copyError.source = source
	copyError.line = line
	copyError.column = column
	copyError.cycle = append([]string(nil), err.cycle...)
	return &copyError
}

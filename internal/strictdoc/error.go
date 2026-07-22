package strictdoc

import "fmt"

type Error struct {
	Code    string
	Source  string
	Pointer string
	Line    int
	Column  int
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	location := e.Source
	if e.Line > 0 {
		location = fmt.Sprintf("%s:%d:%d", location, e.Line, e.Column)
	}
	if e.Pointer != "" {
		location += e.Pointer
	}
	return fmt.Sprintf("%s: %s", location, e.Message)
}

func documentError(code, source, pointer string, line, column int, message string) error {
	return &Error{
		Code:    code,
		Source:  source,
		Pointer: pointer,
		Line:    line,
		Column:  column,
		Message: message,
	}
}

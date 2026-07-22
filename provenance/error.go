package provenance

import "fmt"

type validationError struct {
	kind   string
	reason string
}

func (e *validationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.kind, e.reason)
}

func invalid(kind, reason string) error {
	return &validationError{kind: kind, reason: reason}
}

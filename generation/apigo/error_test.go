package apigo

import (
	"errors"
	"testing"
)

func TestFailurePreservesUnderlyingCauseWithoutRenderingIt(t *testing.T) {
	cause := &parserCause{secret: "/private/api-parser-input"}
	err := failure("verify", "artifact_invalid", Options{}, cause)
	var found *parserCause
	if !errors.Is(err, cause) || !errors.As(err, &found) || found != cause {
		t.Fatalf("cause chain = %#v", err)
	}
	if err.Error() == cause.Error() {
		t.Fatalf("safe error rendered cause: %q", err.Error())
	}
}

type parserCause struct{ secret string }

func (e *parserCause) Error() string { return e.secret }

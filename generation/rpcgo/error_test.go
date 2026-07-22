package rpcgo

import (
	"errors"
	"testing"
)

func TestFailurePreservesUnderlyingCauseWithoutRenderingIt(t *testing.T) {
	cause := &runnerCause{secret: "/private/rpc-runner-output"}
	err := failure("generate", "tool_failed", Options{}, cause)
	var found *runnerCause
	if !errors.Is(err, cause) || !errors.As(err, &found) || found != cause {
		t.Fatalf("cause chain = %#v", err)
	}
	if err.Error() == cause.Error() {
		t.Fatalf("safe error rendered cause: %q", err.Error())
	}
}

type runnerCause struct{ secret string }

func (e *runnerCause) Error() string { return e.secret }

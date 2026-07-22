package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/engine"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func TestProjectErrorUsesTypedClassesAndSafeAccessors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		exit     int
		category protocol.Category
	}{
		{"source input", wrapped(fakeSourceError{class: sourceplugin.ErrManifestInvalid}), 3, protocol.CategoryInput},
		{"source internal", wrapped(fakeSourceError{class: sourceplugin.ErrContractInternal}), 70, protocol.CategoryInternal},
		{"release input", wrapped(fakeReleaseError{class: release.ErrReleaseInput}), 3, protocol.CategoryInput},
		{"release unavailable", wrapped(fakeReleaseError{class: release.ErrReleaseUnavailable}), 6, protocol.CategoryUnavailable},
		{"release conflict", wrapped(fakeReleaseError{class: release.ErrReleaseConflict}), 13, protocol.CategoryConflict},
		{"release internal", wrapped(fakeReleaseError{class: release.ErrReleaseInternal}), 70, protocol.CategoryInternal},
		{"release canceled", wrapped(fakeReleaseError{class: release.ErrReleaseCanceled}), 130, protocol.CategoryCanceled},
		{"lock input", wrapped(fakeLockError{class: lock.ErrLockInput}), 3, protocol.CategoryInput},
		{"lock conflict", wrapped(fakeLockError{class: lock.ErrLockConflict}), 13, protocol.CategoryConflict},
		{"lock internal", wrapped(fakeLockError{class: lock.ErrLockInternal}), 70, protocol.CategoryInternal},
		{"engine input", wrapped(fakeEngineError{class: engine.ErrInput}), 3, protocol.CategoryInput},
		{"engine not managed", wrapped(fakeEngineError{class: engine.ErrNotManaged}), 3, protocol.CategoryInput},
		{"engine conflict", wrapped(fakeEngineError{class: engine.ErrConflict}), 13, protocol.CategoryConflict},
		{"engine unavailable", wrapped(fakeEngineError{class: engine.ErrUnavailable}), 6, protocol.CategoryUnavailable},
		{"engine external", wrapped(fakeEngineError{class: engine.ErrExternal}), 7, protocol.CategoryExternal},
		{"engine internal", wrapped(fakeEngineError{class: engine.ErrInternal}), 70, protocol.CategoryInternal},
		{"engine canceled", wrapped(fakeEngineError{class: engine.ErrCanceled}), 130, protocol.CategoryCanceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projected := projectError(test.err)
			var typed *protocol.Error
			if !errors.As(projected, &typed) {
				t.Fatalf("projection = %T, want *protocol.Error", projected)
			}
			payload := protocol.Project(projected)
			if payload.Category != test.category || protocol.ExitStatus(projected) != test.exit || payload.Domain != errorDomain {
				t.Fatalf("payload=%#v exit=%d", payload, protocol.ExitStatus(projected))
			}
			var details map[string]any
			if err := json.Unmarshal(payload.Details, &details); err != nil || details["reason"] != "safe_reason" {
				t.Fatalf("details=%s err=%v", payload.Details, err)
			}
			encoded, _ := json.Marshal(payload)
			for _, secret := range []string{"same error text", "private-secret", "/private/absolute"} {
				if stringContains(string(encoded), secret) {
					t.Fatalf("unsafe detail escaped: %s", encoded)
				}
			}
		})
	}
}

func TestSourceProjectionOwnedErrorsUseStableExitTaxonomy(t *testing.T) {
	tests := []struct {
		err  error
		exit int
	}{
		{unavailableProviderError(), 6},
		{malformedInputError("flag_invalid", "/provider"), 3},
		{errors.New("private raw error"), 70},
	}
	for _, test := range tests {
		projected := projectError(test.err)
		if protocol.ExitStatus(projected) != test.exit || stringContains(projected.Error(), "private raw error") {
			t.Fatalf("projected=%v exit=%d", projected, protocol.ExitStatus(projected))
		}
	}
}

func TestProjectErrorKeepsOuterEngineCategoryAndPrivateCauseChain(t *testing.T) {
	const unsafeCause = "D039-private-cause /private/staging/source.txt secret=consumer-bytes"
	nested := errors.Join(
		context.Canceled,
		protocol.NewError("nested_private", "private", protocol.CategoryInput, unsafeCause, ""),
	)
	cause := fmt.Errorf("%s: %w", unsafeCause, nested)
	err := fakeEngineError{class: engine.ErrExternal, cause: cause}

	projected := projectError(err)
	payload := protocol.Project(projected)
	if payload.Category != protocol.CategoryExternal || protocol.ExitStatus(projected) != 7 {
		t.Fatalf("payload=%#v exit=%d", payload, protocol.ExitStatus(projected))
	}
	if !errors.Is(projected, cause) || !errors.Is(projected, context.Canceled) {
		t.Fatalf("projected error does not retain private cause chain: %v", projected)
	}
	encoded, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, publicOutput := range []string{projected.Error(), string(encoded)} {
		if stringContains(publicOutput, unsafeCause) || stringContains(publicOutput, "/private/staging") || stringContains(publicOutput, "consumer-bytes") {
			t.Fatalf("unsafe cause escaped: %s", publicOutput)
		}
	}
}

type fakeSourceError struct{ class sourceplugin.ErrorClass }

func (fakeSourceError) Error() string                    { return "same error text private-secret" }
func (e fakeSourceError) Class() sourceplugin.ErrorClass { return e.class }
func (fakeSourceError) Code() string                     { return "safe_code" }
func (fakeSourceError) Reason() string                   { return "safe_reason" }
func (fakeSourceError) Source() string                   { return "/private/absolute" }
func (fakeSourceError) Pointer() string                  { return "/safe" }
func (fakeSourceError) Line() int                        { return 2 }
func (fakeSourceError) Column() int                      { return 3 }
func (fakeSourceError) Cycle() []string                  { return []string{"a", "b", "a"} }

type fakeReleaseError struct{ class release.ErrorClass }

func (fakeReleaseError) Error() string               { return "same error text private-secret" }
func (e fakeReleaseError) Class() release.ErrorClass { return e.class }
func (fakeReleaseError) Code() string                { return "safe_code" }
func (fakeReleaseError) Reason() string              { return "safe_reason" }
func (fakeReleaseError) Pointer() string             { return "/safe" }
func (fakeReleaseError) Stage() release.Stage        { return release.StageResolverStatic }

type fakeLockError struct{ class lock.ErrorClass }

func (fakeLockError) Error() string            { return "same error text private-secret" }
func (e fakeLockError) Class() lock.ErrorClass { return e.class }
func (fakeLockError) Code() string             { return "safe_code" }
func (fakeLockError) Reason() string           { return "safe_reason" }
func (fakeLockError) Source() string           { return "/private/absolute" }
func (fakeLockError) Pointer() string          { return "/safe" }
func (fakeLockError) Line() int                { return 2 }
func (fakeLockError) Column() int              { return 3 }
func (fakeLockError) Stage() lock.Stage        { return lock.StageVerify }

type fakeEngineError struct {
	class engine.ErrorClass
	cause error
}

func (fakeEngineError) Error() string              { return "same error text private-secret" }
func (e fakeEngineError) Class() engine.ErrorClass { return e.class }
func (fakeEngineError) Code() string               { return "safe_code" }
func (fakeEngineError) Reason() string             { return "safe_reason" }
func (fakeEngineError) Pointer() string            { return "/safe" }
func (fakeEngineError) Stage() string              { return "safe-stage" }
func (e fakeEngineError) Unwrap() error            { return e.cause }

func wrapped(err error) error { return fmt.Errorf("wrapper: %w", err) }

func stringContains(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}

package sourceplugin

import (
	"errors"
	"strings"
	"testing"
)

func TestSourceErrorHasClosedSafeClassesAndNilAccessors(t *testing.T) {
	_, err := Parse("safe/manifest.json", []byte(`{"apiVersion":"nexa.dev/source-bundle/v1","kind":"SourceBundle","identity":{"providerId":"sample.foundation","modulePath":"example.com/sample/foundation","packagePath":"example.com/sample/foundation/source","version":"v0.1.0"},"files":[],"profiles":[],"token":"credential-secret"}`))
	projected := assertSourceError(t, err, "source_manifest_invalid", "document_unknown_field", "")
	if !errors.Is(projected, ErrManifestInvalid) || errors.Is(projected, ErrProfileCycle) || errors.Is(projected, errors.New("source bundle manifest is invalid")) {
		t.Fatalf("unexpected errors.Is behavior: %v", projected)
	}
	if strings.Contains(projected.Error(), "token") || strings.Contains(projected.Error(), "credential") || strings.Contains(projected.Error(), projected.Source()) {
		t.Fatalf("Error exposes diagnostics: %q", projected.Error())
	}
	var nilError *Error
	if nilError.Error() != "" || nilError.Class() != 0 || nilError.Code() != "" || nilError.Reason() != "" || nilError.Source() != "" || nilError.Pointer() != "" || nilError.Line() != 0 || nilError.Column() != 0 || nilError.Cycle() != nil {
		t.Fatal("nil error accessors are not zero-safe")
	}
}

func TestSourceErrorTask2ClassLatticeIsClosed(t *testing.T) {
	tests := []struct {
		class   ErrorClass
		message string
		matches []ErrorClass
	}{
		{class: ErrTreeInvalid, message: "source tree is invalid", matches: []ErrorClass{ErrTreeInvalid}},
		{class: ErrTreeLoadFailed, message: "source tree could not be loaded", matches: []ErrorClass{ErrTreeLoadFailed, ErrTreeInvalid}},
		{class: ErrProviderInvalid, message: "source provider is invalid", matches: []ErrorClass{ErrProviderInvalid}},
		{class: ErrContractInternal, message: "source contract projection failed", matches: []ErrorClass{ErrContractInternal}},
	}
	all := []ErrorClass{ErrManifestInvalid, ErrProfileNotFound, ErrProfileCycle, ErrTreeInvalid, ErrTreeLoadFailed, ErrProviderInvalid, ErrContractInternal}
	for _, tt := range tests {
		err := &Error{class: tt.class, code: "safe", reason: "safe"}
		if tt.class.Error() != tt.message || err.Error() != tt.message {
			t.Fatalf("class %v message = %q/%q, want %q", tt.class, tt.class.Error(), err.Error(), tt.message)
		}
		for _, candidate := range all {
			want := false
			for _, match := range tt.matches {
				want = want || candidate == match
			}
			if errors.Is(err, candidate) != want {
				t.Fatalf("errors.Is(%v, %v) = %v, want %v", tt.class, candidate, errors.Is(err, candidate), want)
			}
		}
		if errors.Unwrap(err) != nil {
			t.Fatalf("class %v exposes a cause", tt.class)
		}
	}
}

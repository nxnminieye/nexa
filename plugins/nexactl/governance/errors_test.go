package governance

import (
	"errors"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
)

func TestIssueDetailsAreCanonicalAndBoundToErrorCode(t *testing.T) {
	details := issueDetails{
		errorCode: "skill_manifest_invalid",
		issues: []Issue{{
			Code:    "skill_name_mismatch",
			Object:  "router",
			Message: "skill name must match its directory",
		}},
	}
	if details.ErrorCode() != "skill_manifest_invalid" {
		t.Fatalf("ErrorCode() = %q", details.ErrorCode())
	}
	encoded, err := details.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"issues":[{"code":"skill_name_mismatch","message":"skill name must match its directory","object":"router"}]}`
	if string(encoded) != want {
		t.Fatalf("CanonicalJSON() = %s, want %s", encoded, want)
	}
}

func TestValidationErrorProjectsIndependentCanonicalDetails(t *testing.T) {
	err := validationError(
		"skill_manifest_invalid",
		"skill validation failed",
		"fix the reported manifest issues",
		[]Issue{{Code: "z", Object: "z", Message: "z"}, {Code: "a", Object: "a", Message: "a"}},
	)
	var projectedError *protocol.Error
	if !errors.As(err, &projectedError) {
		t.Fatalf("error = %T %v, want *protocol.Error", err, err)
	}
	first := protocol.Project(err)
	want := `{"issues":[{"code":"a","message":"a","object":"a"},{"code":"z","message":"z","object":"z"}]}`
	if string(first.Details) != want {
		t.Fatalf("details = %s, want %s", first.Details, want)
	}
	first.Details[2] = 'X'
	if second := protocol.Project(err); string(second.Details) != want {
		t.Fatalf("projected mutation leaked: %s", second.Details)
	}
}

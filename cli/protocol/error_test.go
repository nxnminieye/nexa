package protocol_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
)

func TestProjectTypedError(t *testing.T) {
	err := protocol.NewError(
		"generated_drift",
		"generation",
		protocol.CategoryDrift,
		"generated artifact differs from business facts",
		"run the write command and review the diff",
	)
	payload := protocol.Project(err)
	if payload.Code != "generated_drift" || payload.Domain != "generation" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if got := protocol.ExitStatus(err); got != 12 {
		t.Fatalf("exit status = %d, want 12", got)
	}
}

func TestProjectUnknownError(t *testing.T) {
	unknown := errors.New("database password leaked here")
	payload := protocol.Project(unknown)
	if payload.Code != "internal_error" || payload.Domain != "internal" || payload.Category != protocol.CategoryInternal ||
		payload.Retryable || strings.Contains(payload.Message, "password") {
		t.Fatalf("unsafe projection: %#v", payload)
	}
	if got := protocol.ExitStatus(unknown); got != 70 {
		t.Fatalf("exit status = %d, want 70", got)
	}
}

func TestNewErrorWithOptionsCategoryMatrix(t *testing.T) {
	categories := []protocol.Category{
		protocol.CategoryUsage,
		protocol.CategoryInput,
		protocol.CategoryReview,
		protocol.CategoryDrift,
		protocol.CategoryConflict,
		protocol.CategoryUnavailable,
		protocol.CategoryExternal,
		protocol.CategoryCanceled,
		protocol.CategoryInternal,
	}
	retryableCategories := map[protocol.Category]bool{
		protocol.CategoryUnavailable: true,
		protocol.CategoryExternal:    true,
	}

	for _, category := range categories {
		t.Run(string(category)+"/false", func(t *testing.T) {
			projectedError, err := protocol.NewErrorWithOptions(
				"test_error", "test", category, "test failure", "retry later",
				protocol.ErrorOptions{},
			)
			if err != nil {
				t.Fatal(err)
			}
			payload := protocol.Project(projectedError)
			if payload.Category != category || payload.Retryable {
				t.Fatalf("unexpected payload: %#v", payload)
			}
		})

		t.Run(string(category)+"/true", func(t *testing.T) {
			projectedError, err := protocol.NewErrorWithOptions(
				"test_error", "test", category, "test failure", "retry later",
				protocol.ErrorOptions{Retryable: true},
			)
			if retryableCategories[category] {
				if err != nil {
					t.Fatal(err)
				}
				if payload := protocol.Project(projectedError); payload.Category != category || !payload.Retryable {
					t.Fatalf("unexpected payload: %#v", payload)
				}
				return
			}
			if projectedError != nil || err == nil || err.Error() != "error options are invalid" {
				t.Fatalf("NewErrorWithOptions() = %#v, %v", projectedError, err)
			}
		})
	}
}

func TestNewErrorWithOptionsRejectsForgedCategory(t *testing.T) {
	for _, retryable := range []bool{false, true} {
		t.Run(fmt.Sprintf("retryable=%t", retryable), func(t *testing.T) {
			projectedError, err := protocol.NewErrorWithOptions(
				"forged_error", "forged", protocol.Category("forged"), "unsafe", "unsafe",
				protocol.ErrorOptions{Retryable: retryable},
			)
			if projectedError != nil || err == nil || err.Error() != "error options are invalid" {
				t.Fatalf("NewErrorWithOptions() = %#v, %v", projectedError, err)
			}
		})
	}
}

func TestLegacyNewErrorForgedCategoryUsesInternalFallback(t *testing.T) {
	projectedError := protocol.NewError(
		"forged_error", "forged", protocol.Category("forged"), "unsafe secret", "unsafe action",
	)
	payload := protocol.Project(projectedError)
	if payload.Code != "internal_error" || payload.Domain != "internal" || payload.Category != protocol.CategoryInternal ||
		payload.Message != "an internal error occurred" || payload.RecommendedAction != "" || payload.Retryable || len(payload.Details) != 0 {
		t.Fatalf("unsafe fallback: %#v", payload)
	}
	if got := protocol.ExitStatus(projectedError); got != 70 {
		t.Fatalf("exit status = %d, want 70", got)
	}
}

func TestLegacyNewErrorWithDetailsRejectsForgedCategory(t *testing.T) {
	projectedError, err := protocol.NewErrorWithDetails(
		"forged_error",
		"forged",
		protocol.Category("forged"),
		"unsafe secret",
		"unsafe action",
		&testDetailDocument{code: "forged_error", json: []byte(`{}`)},
	)
	if projectedError != nil || err == nil || err.Error() != "error options are invalid" {
		t.Fatalf("NewErrorWithDetails() = %#v, %v", projectedError, err)
	}
}

func TestErrorOptionsAreValidatedBeforeDetails(t *testing.T) {
	invalidDetails := &testDetailDocument{code: "other", err: errors.New("unsafe detail error")}
	tests := []struct {
		name     string
		category protocol.Category
		options  protocol.ErrorOptions
	}{
		{name: "non retryable category", category: protocol.CategoryInput, options: protocol.ErrorOptions{Retryable: true}},
		{name: "forged category", category: protocol.Category("forged"), options: protocol.ErrorOptions{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projectedError, err := protocol.NewErrorWithDetailsOptions(
				"test_error", "test", test.category, "test failure", "retry later", invalidDetails, test.options,
			)
			if projectedError != nil || err == nil || err.Error() != "error options are invalid" {
				t.Fatalf("NewErrorWithDetailsOptions() = %#v, %v", projectedError, err)
			}
		})
	}
}

func TestProjectTypedErrorWithStructuredDetails(t *testing.T) {
	details := &testDetailDocument{
		code: "skill_manifest_invalid",
		json: []byte(`{"issues":[{"code":"skill_name_mismatch","message":"skill name must match its directory","object":"router"}]}`),
	}
	projectedError, err := protocol.NewErrorWithDetails(
		"skill_manifest_invalid",
		"nexactl.governance",
		protocol.CategoryInput,
		"skill validation failed",
		"fix the reported manifest issues",
		details,
	)
	if err != nil {
		t.Fatal(err)
	}

	payload := protocol.Project(projectedError)
	if got := string(payload.Details); got != `{"issues":[{"code":"skill_name_mismatch","message":"skill name must match its directory","object":"router"}]}` {
		t.Fatalf("details = %s", got)
	}
}

func TestStructuredErrorDetailsDoNotAliasConstructorInput(t *testing.T) {
	canonical := []byte(`{"issues":[{"code":"original"}]}`)
	projectedError, err := protocol.NewErrorWithDetailsOptions(
		"skill_manifest_invalid",
		"nexactl.governance",
		protocol.CategoryUnavailable,
		"skill validation failed",
		"fix the reported manifest issues",
		&testDetailDocument{code: "skill_manifest_invalid", json: canonical},
		protocol.ErrorOptions{Retryable: true},
	)
	if err != nil {
		t.Fatal(err)
	}

	canonical[2] = 'X'
	projected := protocol.Project(projectedError)
	if got := string(projected.Details); got != `{"issues":[{"code":"original"}]}` {
		t.Fatalf("projected detail = %q, want original", got)
	}
	if !projected.Retryable {
		t.Fatal("projected detail error must remain retryable")
	}
}

func TestProjectReturnsIndependentStructuredDetails(t *testing.T) {
	projectedError, err := protocol.NewErrorWithDetailsOptions(
		"skill_manifest_invalid",
		"nexactl.governance",
		protocol.CategoryExternal,
		"skill validation failed",
		"fix the reported manifest issues",
		&testDetailDocument{code: "skill_manifest_invalid", json: []byte(`{"issues":[{"code":"original"}]}`)},
		protocol.ErrorOptions{Retryable: true},
	)
	if err != nil {
		t.Fatal(err)
	}

	first := protocol.Project(projectedError)
	first.Details[2] = 'X'
	second := protocol.Project(projectedError)
	if got := string(second.Details); got != `{"issues":[{"code":"original"}]}` {
		t.Fatalf("projected detail = %q, want original", got)
	}
}

func TestProjectStructuredDetailsCanBeMutatedConcurrently(t *testing.T) {
	projectedError, err := protocol.NewErrorWithDetailsOptions(
		"skill_manifest_invalid",
		"nexactl.governance",
		protocol.CategoryUnavailable,
		"skill validation failed",
		"fix the reported manifest issues",
		&testDetailDocument{code: "skill_manifest_invalid", json: []byte(`{"issues":[{"code":"original"}]}`)},
		protocol.ErrorOptions{Retryable: true},
	)
	if err != nil {
		t.Fatal(err)
	}

	var workers sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			projected := protocol.Project(projectedError)
			projected.Details[2] = 'X'
		}()
	}
	workers.Wait()

	projected := protocol.Project(projectedError)
	if got := string(projected.Details); got != `{"issues":[{"code":"original"}]}` {
		t.Fatalf("projected detail = %q, want original", got)
	}
}

func TestNewErrorWithDetailsRejectsInvalidDocuments(t *testing.T) {
	var typedNil *testDetailDocument
	tests := []struct {
		name      string
		details   protocol.DetailDocument
		errorCode string
	}{
		{name: "nil", details: nil, errorCode: "skill_manifest_invalid"},
		{name: "typed nil", details: typedNil, errorCode: "skill_manifest_invalid"},
		{name: "code mismatch", details: &testDetailDocument{code: "other", json: []byte(`{}`)}, errorCode: "skill_manifest_invalid"},
		{name: "document error", details: &testDetailDocument{code: "skill_manifest_invalid", err: errors.New("secret")}, errorCode: "skill_manifest_invalid"},
		{name: "invalid JSON", details: &testDetailDocument{code: "skill_manifest_invalid", json: []byte(`{"a":`)}, errorCode: "skill_manifest_invalid"},
		{name: "array", details: &testDetailDocument{code: "skill_manifest_invalid", json: []byte(`[]`)}, errorCode: "skill_manifest_invalid"},
		{name: "null", details: &testDetailDocument{code: "skill_manifest_invalid", json: []byte(`null`)}, errorCode: "skill_manifest_invalid"},
		{name: "non canonical order", details: &testDetailDocument{code: "skill_manifest_invalid", json: []byte(`{"b":1,"a":2}`)}, errorCode: "skill_manifest_invalid"},
		{name: "trailing whitespace", details: &testDetailDocument{code: "skill_manifest_invalid", json: []byte(`{"a":1} `)}, errorCode: "skill_manifest_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projected, err := protocol.NewErrorWithDetails(
				test.errorCode,
				"nexactl.governance",
				protocol.CategoryInput,
				"skill validation failed",
				"fix the reported manifest issues",
				test.details,
			)
			if err == nil || projected != nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("NewErrorWithDetails() = %#v, %v", projected, err)
			}
		})
	}
}

type testDetailDocument struct {
	code string
	json []byte
	err  error
}

func (d *testDetailDocument) ErrorCode() string {
	if d == nil {
		return ""
	}
	return d.code
}

func (d *testDetailDocument) CanonicalJSON() ([]byte, error) {
	if d == nil {
		return nil, fmt.Errorf("nil detail document")
	}
	return d.json, d.err
}

func TestExitStatusTaxonomy(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected int
	}{
		{name: "success", err: nil, expected: 0},
		{name: "usage", err: protocol.NewError("test", "test", protocol.CategoryUsage, "test", ""), expected: 2},
		{name: "input", err: protocol.NewError("test", "test", protocol.CategoryInput, "test", ""), expected: 3},
		{name: "review", err: protocol.NewError("test", "test", protocol.CategoryReview, "test", ""), expected: 5},
		{name: "drift", err: protocol.NewError("test", "test", protocol.CategoryDrift, "test", ""), expected: 12},
		{name: "conflict", err: protocol.NewError("test", "test", protocol.CategoryConflict, "test", ""), expected: 13},
		{name: "unavailable", err: protocol.NewError("test", "test", protocol.CategoryUnavailable, "test", ""), expected: 6},
		{name: "external", err: protocol.NewError("test", "test", protocol.CategoryExternal, "test", ""), expected: 7},
		{name: "internal", err: protocol.NewError("test", "test", protocol.CategoryInternal, "test", ""), expected: 70},
		{name: "canceled", err: protocol.NewError("test", "test", protocol.CategoryCanceled, "test", ""), expected: 130},
		{name: "unknown", err: errors.New("test"), expected: 70},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := protocol.ExitStatus(tt.err); got != tt.expected {
				t.Fatalf("exit status = %d, want %d", got, tt.expected)
			}
		})
	}
}

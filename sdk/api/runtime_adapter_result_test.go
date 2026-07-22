package api

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/cli/protocol"
	generationapi "github.com/nxnminieye/nexa/generation/api"
)

func TestRuntimeAdapterRequestDigestIsCanonicalAndNonReversible(t *testing.T) {
	body := `{"secret":"body-value"}`
	request := RuntimeAdapterRequest{
		Method: "POST",
		URL:    "https://user:password@api.example.test/samples?token=url-secret",
		Headers: []RuntimeAdapterHeader{
			{Name: "authorization", Value: "Bearer credential-secret"},
			{Name: "x-order", Value: "second"},
		},
		Body: &body,
	}
	digest, err := request.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := digest.String(), "sha256:fc2148e8ac123ed91833250630af8438965da51987d37a6c079d4abea37233e1"; got != want {
		t.Fatalf("Digest() = %q, want %q", got, want)
	}

	result, err := NewRuntimeAdapterResult([]RuntimeAdapterCaseResult{{
		Name: "secret-request", RequestDigest: digest.String(), Outcome: RuntimeAdapterOutcome{Error: &RuntimeAdapterError{
			Domain: "nexa.sdk.api", Code: "transport_error", Message: "API transport failed",
			Category: protocol.CategoryExternal, APIOperationID: "sample.call", Reason: "round_trip_failed",
			RemoteDetailsAbsent: true,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := result.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"user:password", "url-secret", "credential-secret", "body-value", `"request"`} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("result disclosed %q: %s", forbidden, encoded)
		}
	}
}

func TestRuntimeAdapterResultCanonicalClosedSchema(t *testing.T) {
	result, err := NewRuntimeAdapterResult([]RuntimeAdapterCaseResult{
		{
			Name:           "success",
			RequestDigest:  "sha256:890981d620a7814f2a902b261d20c040da66a14f19e34bf04f26373e21bbc1df",
			ProviderCalls:  1,
			TransportCalls: 1,
			BodyReadCalls:  1,
			BodyCloseCalls: 1,
			Outcome: RuntimeAdapterOutcome{Success: &RuntimeAdapterSuccess{
				APIOperationID: "sample.get", HTTPStatus: 200, ResponseBody: generationapi.ResponseBodyJSON,
				HasJSON: true, CanonicalJSON: `{"displayName":"Sample"}`,
			}},
		},
		{
			Name: "failure", RequestDigest: "absent",
			Outcome: RuntimeAdapterOutcome{Error: &RuntimeAdapterError{
				Domain: "nexa.sdk.api", Code: "request_invalid", Message: "API request is invalid",
				Category: protocol.CategoryInput, Retryable: false, APIOperationID: "sample.get",
				Reason: "field_required", Pointer: "/id", RemoteDetailsAbsent: true,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := result.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) == 0 || encoded[0] != '{' || encoded[len(encoded)-1] != '}' || bytes.Contains(encoded, []byte("\n")) {
		t.Fatalf("result is not one compact canonical object: %q", encoded)
	}
	parsed, err := ParseRuntimeAdapterResult(encoded)
	if err != nil {
		t.Fatalf("ParseRuntimeAdapterResult() error = %v", err)
	}
	if len(parsed.Cases()) != 2 {
		t.Fatalf("cases = %d", len(parsed.Cases()))
	}
	schema := RuntimeAdapterResultSchema()
	if len(schema) == 0 || !json.Valid(schema) {
		t.Fatal("RuntimeAdapterResultSchema() is not JSON")
	}
	schema[0] = '['
	if RuntimeAdapterResultSchema()[0] != '{' {
		t.Fatal("RuntimeAdapterResultSchema() mutation escaped")
	}

	for _, invalid := range [][]byte{
		bytes.Replace(encoded, []byte(`"apiVersion":`), []byte(`"unknown":true,"apiVersion":`), 1),
		bytes.Replace(encoded, []byte(`"retryable":false`), []byte(`"retryable":null`), 1),
		bytes.Replace(encoded, []byte(`"outcome":{"success":`), []byte(`"outcome":{"error":null,"success":`), 1),
	} {
		if _, err := ParseRuntimeAdapterResult(invalid); err == nil {
			t.Fatalf("ParseRuntimeAdapterResult() accepted %s", invalid)
		}
	}
	if _, err := NewRuntimeAdapterResult([]RuntimeAdapterCaseResult{{Name: "missing-outcome"}}); err == nil {
		t.Fatal("NewRuntimeAdapterResult() accepted missing outcome")
	}
}

func TestRuntimeAdapterResultRejectsImpossibleTypedOutcomes(t *testing.T) {
	validSuccess := RuntimeAdapterCaseResult{
		Name: "success", RequestDigest: "absent",
		Outcome: RuntimeAdapterOutcome{Success: &RuntimeAdapterSuccess{
			APIOperationID: "sample.get", HTTPStatus: 200, ResponseBody: generationapi.ResponseBodyJSON,
			HasJSON: true, CanonicalJSON: `{"displayName":"Sample"}`,
		}},
	}
	validError := RuntimeAdapterCaseResult{
		Name: "error", RequestDigest: "absent",
		Outcome: RuntimeAdapterOutcome{Error: &RuntimeAdapterError{
			Domain: "nexa.sdk.api", Code: "transport_error", Message: "API transport failed",
			Category: protocol.CategoryExternal, Reason: "round_trip_failed", RemoteDetailsAbsent: true,
		}},
	}
	for _, vector := range []struct {
		name string
		row  RuntimeAdapterCaseResult
	}{
		{name: "json without has-json", row: mutateRuntimeAdapterSuccess(validSuccess, func(success *RuntimeAdapterSuccess) { success.HasJSON = false })},
		{name: "json empty", row: mutateRuntimeAdapterSuccess(validSuccess, func(success *RuntimeAdapterSuccess) { success.CanonicalJSON = "" })},
		{name: "json invalid", row: mutateRuntimeAdapterSuccess(validSuccess, func(success *RuntimeAdapterSuccess) { success.CanonicalJSON = "not-json" })},
		{name: "json noncanonical", row: mutateRuntimeAdapterSuccess(validSuccess, func(success *RuntimeAdapterSuccess) { success.CanonicalJSON = `{ "displayName": "Sample" }` })},
		{name: "none with json", row: mutateRuntimeAdapterSuccess(validSuccess, func(success *RuntimeAdapterSuccess) {
			success.ResponseBody = generationapi.ResponseBodyNone
		})},
		{name: "none with bytes", row: mutateRuntimeAdapterSuccess(validSuccess, func(success *RuntimeAdapterSuccess) {
			success.ResponseBody = generationapi.ResponseBodyNone
			success.HasJSON = false
		})},
		{name: "error status below range", row: mutateRuntimeAdapterError(validError, func(failure *RuntimeAdapterError) { failure.HTTPStatus = 199 })},
		{name: "error status above range", row: mutateRuntimeAdapterError(validError, func(failure *RuntimeAdapterError) { failure.HTTPStatus = 600 })},
	} {
		t.Run(vector.name, func(t *testing.T) {
			if _, err := NewRuntimeAdapterResult([]RuntimeAdapterCaseResult{vector.row}); err == nil {
				t.Fatal("NewRuntimeAdapterResult() accepted impossible typed outcome")
			}
		})
	}
}

func TestRuntimeAdapterResultParserRejectsImpossibleOutcomes(t *testing.T) {
	result, err := NewRuntimeAdapterResult([]RuntimeAdapterCaseResult{{
		Name: "success", RequestDigest: "absent",
		Outcome: RuntimeAdapterOutcome{Success: &RuntimeAdapterSuccess{
			APIOperationID: "sample.get", HTTPStatus: 200, ResponseBody: generationapi.ResponseBodyJSON,
			HasJSON: true, CanonicalJSON: `{"displayName":"Sample"}`,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := result.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, vector := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "json without has-json", mutate: func(success map[string]any) { success["hasJSON"] = false }},
		{name: "json empty", mutate: func(success map[string]any) { success["canonicalJSON"] = "" }},
		{name: "json noncanonical", mutate: func(success map[string]any) { success["canonicalJSON"] = `{ "displayName": "Sample" }` }},
		{name: "none with json", mutate: func(success map[string]any) { success["responseBody"] = "none" }},
	} {
		t.Run(vector.name, func(t *testing.T) {
			var document map[string]any
			decoder := json.NewDecoder(bytes.NewReader(encoded))
			decoder.UseNumber()
			if err := decoder.Decode(&document); err != nil {
				t.Fatal(err)
			}
			success := document["cases"].([]any)[0].(map[string]any)["outcome"].(map[string]any)["success"].(map[string]any)
			vector.mutate(success)
			mutated, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := jcs.Transform(mutated)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseRuntimeAdapterResult(canonical); err == nil {
				t.Fatal("ParseRuntimeAdapterResult() accepted impossible outcome")
			}
		})
	}
}

func TestRuntimeAdapterResultParserRejectsInvalidErrorStatus(t *testing.T) {
	result, err := NewRuntimeAdapterResult([]RuntimeAdapterCaseResult{{
		Name: "error", RequestDigest: "absent",
		Outcome: RuntimeAdapterOutcome{Error: &RuntimeAdapterError{
			Domain: "nexa.sdk.api", Code: "transport_error", Message: "API transport failed",
			Category: protocol.CategoryExternal, Reason: "round_trip_failed", RemoteDetailsAbsent: true,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := result.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []int{1, 199, 600} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var document map[string]any
			decoder := json.NewDecoder(bytes.NewReader(encoded))
			decoder.UseNumber()
			if err := decoder.Decode(&document); err != nil {
				t.Fatal(err)
			}
			failure := document["cases"].([]any)[0].(map[string]any)["outcome"].(map[string]any)["error"].(map[string]any)
			failure["httpStatus"] = status
			mutated, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := jcs.Transform(mutated)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseRuntimeAdapterResult(canonical); err == nil {
				t.Fatal("ParseRuntimeAdapterResult() accepted invalid error status")
			}
		})
	}
}

func mutateRuntimeAdapterSuccess(input RuntimeAdapterCaseResult, mutate func(*RuntimeAdapterSuccess)) RuntimeAdapterCaseResult {
	success := *input.Outcome.Success
	mutate(&success)
	input.Outcome.Success = &success
	return input
}

func mutateRuntimeAdapterError(input RuntimeAdapterCaseResult, mutate func(*RuntimeAdapterError)) RuntimeAdapterCaseResult {
	failure := *input.Outcome.Error
	mutate(&failure)
	input.Outcome.Error = &failure
	return input
}

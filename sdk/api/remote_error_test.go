package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	generationapi "github.com/nxnminieye/nexa/generation/api"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestRemoteErrorRoundTripAndDefensiveDetails(t *testing.T) {
	details := []byte(`{"resource":"sample","available":false}`)
	remote, err := NewRemoteError(RemoteErrorSpec{
		Domain:      "sample",
		Code:        "not_found",
		Message:     "sample was not found",
		RequestID:   "remote-request",
		TraceID:     "remote-trace",
		DetailsJSON: details,
	})
	if err != nil {
		t.Fatal(err)
	}
	details[2] = 'X'
	if remote.Domain() != "sample" || remote.Code() != "not_found" || remote.Message() != "sample was not found" || remote.RequestID() != "remote-request" || remote.TraceID() != "remote-trace" {
		t.Fatalf("unexpected accessors: %#v", remote)
	}

	want := []byte(`{"apiVersion":"nexa.dev/remote-error/v1","code":"not_found","details":{"available":false,"resource":"sample"},"domain":"sample","message":"sample was not found","requestId":"remote-request","traceId":"remote-trace"}`)
	canonical, err := remote.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, want) {
		t.Fatalf("CanonicalJSON() = %s, want %s", canonical, want)
	}

	projected, ok := remote.DetailsJSON()
	if !ok || string(projected) != `{"available":false,"resource":"sample"}` {
		t.Fatalf("DetailsJSON() = %s, %t", projected, ok)
	}
	projected[2] = 'X'
	second, _ := remote.DetailsJSON()
	if string(second) != `{"available":false,"resource":"sample"}` {
		t.Fatalf("details mutation leaked: %s", second)
	}
	canonical[2] = 'X'
	reencoded, err := remote.CanonicalJSON()
	if err != nil || !bytes.Equal(reencoded, want) {
		t.Fatalf("canonical mutation leaked: %s, %v", reencoded, err)
	}

	parsed, err := ParseRemoteError(want)
	if err != nil {
		t.Fatal(err)
	}
	parsedJSON, err := parsed.CanonicalJSON()
	if err != nil || !bytes.Equal(parsedJSON, want) {
		t.Fatalf("round trip = %s, %v", parsedJSON, err)
	}
}

func TestRemoteErrorWithoutDetails(t *testing.T) {
	remote, err := NewRemoteError(RemoteErrorSpec{Domain: "sample", Code: "not_found", Message: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if details, ok := remote.DetailsJSON(); ok || details != nil {
		t.Fatalf("DetailsJSON() = %s, %t", details, ok)
	}
	if _, err := (RemoteError{}).CanonicalJSON(); err == nil {
		t.Fatal("zero RemoteError serialized")
	}
}

func TestRemoteErrorStructuralSchemaIsCompilableAndDefensive(t *testing.T) {
	first := RemoteErrorSchema()
	var document map[string]any
	if err := json.Unmarshal(first, &document); err != nil {
		t.Fatal(err)
	}
	if document["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v", document["additionalProperties"])
	}
	properties, ok := document["properties"].(map[string]any)
	if !ok || len(properties) != 7 {
		t.Fatalf("properties = %#v", document["properties"])
	}
	for _, name := range []string{"apiVersion", "domain", "code", "message", "requestId", "traceId", "details"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("schema property %q missing", name)
		}
	}
	required := document["required"].([]any)
	if fmt.Sprint(required) != "[apiVersion domain code message]" {
		t.Fatalf("required = %v", required)
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("https://nexa.dev/schemas/sdk/api/remote-error-v1.schema.json", document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("https://nexa.dev/schemas/sdk/api/remote-error-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal([]byte(`{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"not_found","message":"missing","details":{}}`), &instance); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(instance); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"not_found","message":"line\u000afeed"}`), &instance); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(instance); err == nil {
		t.Fatal("schema accepted a control character in message")
	}

	first[0] = 'X'
	if second := RemoteErrorSchema(); len(second) == 0 || second[0] != '{' {
		t.Fatalf("schema mutation leaked: %q", second)
	}
}

func TestRemoteErrorStructuralSchemaMatchesRuntimeControlGrammar(t *testing.T) {
	compiled := compileRemoteErrorStructuralSchema(t)
	tests := []struct {
		name    string
		field   string
		value   string
		reason  string
		pointer string
	}{
		{name: "domain trailing LF", field: "domain", value: "sample\n", reason: "domain_invalid", pointer: "/domain"},
		{name: "code trailing LF", field: "code", value: "not_found\n", reason: "code_invalid", pointer: "/code"},
		{name: "message trailing LF", field: "message", value: "missing\n", reason: "message_invalid", pointer: "/message"},
		{name: "request ID trailing LF", field: "requestId", value: "remote-request\n", reason: "request_id_invalid", pointer: "/requestId"},
		{name: "trace ID trailing LF", field: "traceId", value: "remote-trace\n", reason: "trace_id_invalid", pointer: "/traceId"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := map[string]any{
				"apiVersion": remoteErrorAPIVersion,
				"domain":     "sample",
				"code":       "not_found",
				"message":    "missing",
			}
			document[test.field] = test.value
			if err := compiled.Validate(document); err == nil {
				t.Fatalf("structural schema accepted control character in %s", test.field)
			}
			body, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseRemoteError(body)
			requireRemoteProtocolError(t, err, test.reason, test.pointer)
		})
	}
}

func TestRemoteErrorLimitsAreTypedImmutableAndDriveRuntimeValidation(t *testing.T) {
	limits := RemoteErrorLimits()
	want := RemoteErrorLimitSet{
		APIVersion:         RemoteErrorLimitsAPIVersion,
		IDBytes:            256,
		MessageBytes:       1024,
		DetailsBytes:       32 << 10,
		DetailsDepth:       16,
		DetailsMemberTotal: 256,
	}
	if RemoteErrorLimitsAPIVersion != "nexa.dev/remote-error-limits/v1" {
		t.Fatalf("RemoteErrorLimitsAPIVersion = %q", RemoteErrorLimitsAPIVersion)
	}
	if limits != want {
		t.Fatalf("RemoteErrorLimits() = %#v, want %#v", limits, want)
	}
	limits.DetailsBytes = 1
	if got := RemoteErrorLimits(); got != want {
		t.Fatalf("limit mutation leaked: %#v", got)
	}

	validID := strings.Repeat("a", want.IDBytes)
	if _, err := NewRemoteError(RemoteErrorSpec{Domain: validID, Code: "sample", Message: ""}); err != nil {
		t.Fatalf("ID at byte limit rejected: %v", err)
	}
	_, err := NewRemoteError(RemoteErrorSpec{Domain: validID + "a", Code: "sample", Message: ""})
	requireRemoteProtocolError(t, err, "domain_invalid", "/domain")

	validMessage := strings.Repeat("x", want.MessageBytes)
	if _, err := NewRemoteError(RemoteErrorSpec{Domain: "sample", Code: "sample", Message: validMessage}); err != nil {
		t.Fatalf("message at byte limit rejected: %v", err)
	}
	_, err = NewRemoteError(RemoteErrorSpec{Domain: "sample", Code: "sample", Message: validMessage + "x"})
	requireRemoteProtocolError(t, err, "message_invalid", "/message")
}

func TestRemoteErrorStructuralSchemaDefersIDByteLimitToTypedOwner(t *testing.T) {
	compiled := compileRemoteErrorStructuralSchema(t)
	oversizeID := strings.Repeat("a", RemoteErrorLimits().IDBytes+1)
	tests := []struct {
		name    string
		body    string
		spec    RemoteErrorSpec
		reason  string
		pointer string
	}{
		{
			name: "domain",
			body: remoteErrorBodyWithIdentity(oversizeID, "sample"),
			spec: RemoteErrorSpec{
				Domain:  oversizeID,
				Code:    "sample",
				Message: "missing",
			},
			reason:  "domain_invalid",
			pointer: "/domain",
		},
		{
			name: "code",
			body: remoteErrorBodyWithIdentity("sample", oversizeID),
			spec: RemoteErrorSpec{
				Domain:  "sample",
				Code:    oversizeID,
				Message: "missing",
			},
			reason:  "code_invalid",
			pointer: "/code",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var instance any
			if err := json.Unmarshal([]byte(test.body), &instance); err != nil {
				t.Fatal(err)
			}
			if err := compiled.Validate(instance); err != nil {
				t.Fatalf("structural schema rejected error ID governed by typed limits: %v", err)
			}

			_, err := ParseRemoteError([]byte(test.body))
			requireRemoteProtocolError(t, err, test.reason, test.pointer)
			_, err = NewRemoteError(test.spec)
			requireRemoteProtocolError(t, err, test.reason, test.pointer)
		})
	}
}

func TestRemoteErrorStructuralSchemaDefersSemanticLimitsToRuntimeOwner(t *testing.T) {
	compiled := compileRemoteErrorStructuralSchema(t)
	limits := RemoteErrorLimits()
	members := make([]string, limits.DetailsMemberTotal+1)
	for index := range members {
		members[index] = fmt.Sprintf(`"k%03d":true`, index)
	}
	tests := []struct {
		name   string
		body   string
		reason string
	}{
		{
			name:   "message UTF-8 bytes",
			body:   remoteErrorBody(`"message":"` + strings.Repeat("界", limits.MessageBytes/3+1) + `"`),
			reason: "message_invalid",
		},
		{
			name:   "request ID UTF-8 bytes",
			body:   remoteErrorBody(`"message":"missing","requestId":"` + strings.Repeat("界", limits.IDBytes/3+1) + `"`),
			reason: "request_id_invalid",
		},
		{
			name:   "details encoded bytes",
			body:   remoteErrorBody(`"message":"missing","details":{"value":"` + strings.Repeat("x", limits.DetailsBytes) + `"}`),
			reason: "details_size_limit_exceeded",
		},
		{
			name:   "details container depth",
			body:   remoteErrorBody(`"message":"missing","details":{"value":` + strings.Repeat("[", limits.DetailsDepth+1) + `true` + strings.Repeat("]", limits.DetailsDepth+1) + `}`),
			reason: "details_depth_limit_exceeded",
		},
		{
			name:   "details recursive members",
			body:   remoteErrorBody(`"message":"missing","details":{` + strings.Join(members, ",") + `}`),
			reason: "details_member_limit_exceeded",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var instance any
			if err := json.Unmarshal([]byte(test.body), &instance); err != nil {
				t.Fatal(err)
			}
			if err := compiled.Validate(instance); err != nil {
				t.Fatalf("structural schema rejected runtime-limit case: %v", err)
			}
			_, err := ParseRemoteError([]byte(test.body))
			requireRemoteProtocolError(t, err, test.reason, map[string]string{
				"message_invalid":               "/message",
				"request_id_invalid":            "/requestId",
				"details_size_limit_exceeded":   "/details",
				"details_depth_limit_exceeded":  "/details",
				"details_member_limit_exceeded": "/details",
			}[test.reason])
		})
	}
}

func compileRemoteErrorStructuralSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	var document any
	if err := json.Unmarshal(RemoteErrorSchema(), &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const resource = "https://nexa.dev/schemas/sdk/api/remote-error-v1.schema.json"
	if err := compiler.AddResource(resource, document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func remoteErrorBody(fields string) string {
	return `{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"sample",` + fields + `}`
}

func remoteErrorBodyWithIdentity(domain, code string) string {
	return `{"apiVersion":"nexa.dev/remote-error/v1","domain":"` + domain + `","code":"` + code + `","message":"missing"}`
}

func TestRemoteErrorRejectsInvalidDocuments(t *testing.T) {
	valid := `{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"not_found","message":"missing"}`
	tests := []struct {
		name, data, reason, pointer string
	}{
		{name: "wrong version", data: strings.Replace(valid, "remote-error/v1", "remote-error/v2", 1), reason: "version_unsupported", pointer: "/apiVersion"},
		{name: "unknown field", data: strings.TrimSuffix(valid, "}") + `,"secret":"do-not-leak"}`, reason: "document_unknown_field", pointer: ""},
		{name: "duplicate field", data: strings.TrimSuffix(valid, "}") + `,"code":"other"}`, reason: "duplicate_key", pointer: "/code"},
		{name: "trailing input", data: valid + ` {"secret":"do-not-leak"}`, reason: "trailing_input", pointer: ""},
		{name: "null details", data: strings.TrimSuffix(valid, "}") + `,"details":null}`, reason: "details_invalid", pointer: "/details"},
		{name: "array details", data: strings.TrimSuffix(valid, "}") + `,"details":[]}`, reason: "details_invalid", pointer: "/details"},
		{name: "invalid domain", data: strings.Replace(valid, `"sample"`, `"Sample"`, 1), reason: "domain_invalid", pointer: "/domain"},
		{name: "invalid code", data: strings.Replace(valid, `"not_found"`, `"not/found"`, 1), reason: "code_invalid", pointer: "/code"},
		{name: "message control", data: strings.Replace(valid, `"missing"`, `"missing\u000a"`, 1), reason: "message_invalid", pointer: "/message"},
		{name: "request id control", data: strings.TrimSuffix(valid, "}") + `,"requestId":"secret\u0000"}`, reason: "request_id_invalid", pointer: "/requestId"},
		{name: "request id empty", data: strings.TrimSuffix(valid, "}") + `,"requestId":""}`, reason: "request_id_invalid", pointer: "/requestId"},
		{name: "trace id too long", data: strings.TrimSuffix(valid, "}") + `,"traceId":"` + strings.Repeat("x", 257) + `"}`, reason: "trace_id_invalid", pointer: "/traceId"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseRemoteError([]byte(test.data))
			requireRemoteProtocolError(t, err, test.reason, test.pointer)
			if strings.Contains(err.Error(), "do-not-leak") || strings.Contains(err.Error(), test.data) {
				t.Fatalf("remote body leaked through error: %v", err)
			}
		})
	}
}

func TestRemoteErrorNeverProjectsUntrustedKeys(t *testing.T) {
	const secret = "credential-value-must-not-leak"
	tests := []struct {
		name    string
		parse   func() error
		reason  string
		pointer string
	}{
		{
			name: "unknown root key",
			parse: func() error {
				_, err := ParseRemoteError([]byte(`{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"not_found","message":"missing","` + secret + `":true}`))
				return err
			},
			reason: "document_unknown_field",
		},
		{
			name: "malformed details key",
			parse: func() error {
				_, err := ParseRemoteError([]byte(`{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"not_found","message":"missing","details":{"` + secret + `":}}`))
				return err
			},
			reason:  "invalid_json",
			pointer: "/details",
		},
		{
			name: "constructor details key",
			parse: func() error {
				_, err := NewRemoteError(RemoteErrorSpec{
					Domain:      "sample",
					Code:        "not_found",
					Message:     "missing",
					DetailsJSON: []byte(`{"` + secret + `":}`),
				})
				return err
			},
			reason:  "invalid_json",
			pointer: "/details",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			apiError := requireRemoteProtocolError(t, test.parse(), test.reason, test.pointer)
			assertRemoteErrorProjectionDoesNotContain(t, apiError, secret)
		})
	}
}

func assertRemoteErrorProjectionDoesNotContain(t *testing.T, apiError *Error, secret string) {
	t.Helper()
	details := apiError.Details()
	accessors := []string{
		apiError.Error(), apiError.Code(), apiError.Domain(), apiError.APIOperationID(),
		apiError.RequestID(), apiError.TraceID(), details.Reason(), details.Pointer(),
		details.RemoteDomain(), details.RemoteCode(),
	}
	for _, value := range accessors {
		if strings.Contains(value, secret) {
			t.Fatalf("secret reached SDK error accessor: %q", value)
		}
	}
	projectedError, err := protocol.NewErrorWithDetails(
		apiError.Code(), apiError.Domain(), apiError.Category(), apiError.Error(), "",
		remoteProtocolCLIDetails{code: apiError.Code(), reason: details.Reason(), pointer: details.Pointer()},
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(protocol.Project(projectedError))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("secret reached CLI projection: %s", encoded)
	}
}

type remoteProtocolCLIDetails struct {
	code    string
	reason  string
	pointer string
}

func (d remoteProtocolCLIDetails) ErrorCode() string { return d.code }

func (d remoteProtocolCLIDetails) CanonicalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Pointer string `json:"pointer"`
		Reason  string `json:"reason"`
	}{Pointer: d.pointer, Reason: d.reason})
}

func TestRemoteErrorRejectsDetailsLimits(t *testing.T) {
	members := make([]string, 257)
	for index := range members {
		members[index] = fmt.Sprintf(`"k%03d":true`, index)
	}
	tests := []struct {
		name, details, reason string
	}{
		{name: "size", details: `{"value":"` + strings.Repeat("x", 32<<10) + `"}`, reason: "details_size_limit_exceeded"},
		{name: "depth", details: `{"value":` + strings.Repeat("[", 17) + `true` + strings.Repeat("]", 17) + `}`, reason: "details_depth_limit_exceeded"},
		{name: "members", details: `{` + strings.Join(members, ",") + `}`, reason: "details_member_limit_exceeded"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewRemoteError(RemoteErrorSpec{Domain: "sample", Code: "not_found", Message: "missing", DetailsJSON: []byte(test.details)})
			requireRemoteProtocolError(t, err, test.reason, "/details")
		})
	}
}

func TestRemoteErrorDetailsLimitBoundaries(t *testing.T) {
	limits := RemoteErrorLimits()
	prefix, suffix := `{"value":"`, `"}`
	exact := prefix + strings.Repeat("x", limits.DetailsBytes-len(prefix)-len(suffix)) + suffix
	if _, err := NewRemoteError(RemoteErrorSpec{Domain: "sample", Code: "not_found", Message: "missing", DetailsJSON: []byte(exact)}); err != nil {
		t.Fatalf("exact size rejected: %v", err)
	}
	_, err := NewRemoteError(RemoteErrorSpec{Domain: "sample", Code: "not_found", Message: "missing", DetailsJSON: []byte(`{` + strings.Repeat(" ", limits.DetailsBytes) + `}`)})
	requireRemoteProtocolError(t, err, "details_size_limit_exceeded", "/details")
	body := `{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"not_found","message":"missing","details":{` + strings.Repeat(" ", limits.DetailsBytes) + `}}`
	_, err = ParseRemoteError([]byte(body))
	requireRemoteProtocolError(t, err, "details_size_limit_exceeded", "/details")

	depth16 := `{"value":` + strings.Repeat("[", 15) + `true` + strings.Repeat("]", 15) + `}`
	if _, err := NewRemoteError(RemoteErrorSpec{Domain: "sample", Code: "not_found", Message: "missing", DetailsJSON: []byte(depth16)}); err != nil {
		t.Fatalf("depth 16 rejected: %v", err)
	}
}

func TestRemoteErrorParserUsesRuntimeSafetyDepthAndNodeLimits(t *testing.T) {
	limits := RuntimeLimits()
	deepBody := remoteErrorBody(`"message":"missing","details":{"value":` + strings.Repeat("[", limits.JSONDepth) + `true` + strings.Repeat("]", limits.JSONDepth) + `}`)
	_, err := ParseRemoteError([]byte(deepBody))
	requireRemoteProtocolError(t, err, "depth_limit_exceeded", "/details")

	wideBody := remoteErrorBody(`"message":"missing","details":{"values":[` + strings.TrimSuffix(strings.Repeat("0,", limits.JSONNodes), ",") + `]}`)
	_, err = ParseRemoteError([]byte(wideBody))
	requireRemoteProtocolError(t, err, "node_limit_exceeded", "/details")
}

func TestRemoteErrorRecursiveMemberLimitCountsNestedObjectsThroughArrays(t *testing.T) {
	limits := RemoteErrorLimits()
	exact := nestedRemoteMemberDetails(128, limits.DetailsMemberTotal-129)
	if _, err := NewRemoteError(RemoteErrorSpec{Domain: "sample", Code: "not_found", Message: "missing", DetailsJSON: []byte(exact)}); err != nil {
		t.Fatalf("recursive member total at limit rejected: %v", err)
	}
	over := nestedRemoteMemberDetails(128, limits.DetailsMemberTotal-128)
	_, err := NewRemoteError(RemoteErrorSpec{Domain: "sample", Code: "not_found", Message: "missing", DetailsJSON: []byte(over)})
	requireRemoteProtocolError(t, err, "details_member_limit_exceeded", "/details")
}

func TestRemoteErrorRejectsMalformedBodyWithoutLeak(t *testing.T) {
	malformed := append([]byte(`{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"not_found","message":"secret:`), 0xff)
	_, err := ParseRemoteError(malformed)
	apiError := requireRemoteProtocolError(t, err, "invalid_utf8", "/message")
	if strings.Contains(apiError.Error(), "secret") || strings.Contains(apiError.Error(), string(malformed)) {
		t.Fatalf("malformed body leaked: %v", apiError)
	}
}

func TestRemoteErrorConformanceCorpus(t *testing.T) {
	corpus := loadRuntimeCorpusDocument(t)
	if corpus.APIVersion != "nexa.dev/runtime-api-conformance/v1" || len(corpus.Requests) == 0 || len(corpus.Credentials) == 0 || len(corpus.Responses) == 0 || len(corpus.RemoteErrorGrammar) != 5 || len(corpus.Errors) == 0 || len(corpus.RemoteErrorLimitRecipes) != 2 {
		t.Fatalf("incomplete corpus: %#v", corpus)
	}
	manifest, err := generationapi.Parse("runtime-api-v1.json#manifest", []byte(corpus.Manifest))
	if err != nil {
		t.Fatal(err)
	}
	canonicalManifest, err := manifest.CanonicalJSON()
	if err != nil || string(canonicalManifest) != corpus.Manifest {
		t.Fatalf("manifest is not canonical: %v", err)
	}
	sawUTF16Order := false
	for _, vector := range corpus.Requests {
		request, err := ParseRequest([]byte(vector.JSON))
		if vector.Valid {
			if err != nil || string(request.JSON()) != vector.Canonical {
				t.Fatalf("request vector %q = %s, %v", vector.Name, request.JSON(), err)
			}
			if vector.Name == "utf16-member-order" {
				sawUTF16Order = true
			}
			continue
		}
		apiError := requireSDKError(t, err)
		if apiError.Details().Reason() != vector.Error.Reason || apiError.Details().Pointer() != vector.Error.Pointer {
			t.Fatalf("request vector %q error = %#v", vector.Name, apiError.Details())
		}
	}
	if !sawUTF16Order {
		t.Fatal("shared corpus is missing the UTF-16 member-order vector")
	}
	sawRequiredEmpty := false
	for _, vector := range corpus.Credentials {
		if vector.Name != "primary-missing" {
			continue
		}
		sawRequiredEmpty = true
		if vector.Valid || len(vector.Values) != 0 || vector.Error.Code != "request_invalid" || vector.Error.Reason != "credential_count_invalid" || vector.Error.Pointer != "/credentials" {
			t.Fatalf("required empty credential vector = %#v", vector)
		}
	}
	if !sawRequiredEmpty {
		t.Fatal("shared corpus is missing required empty credential selection")
	}
	compiledRemoteErrorSchema := compileRemoteErrorStructuralSchema(t)
	seenGrammarFields := make(map[string]struct{}, len(corpus.RemoteErrorGrammar))
	for _, vector := range corpus.RemoteErrorGrammar {
		var instance any
		if err := json.Unmarshal([]byte(vector.Body), &instance); err != nil {
			t.Fatalf("remote grammar vector %q is invalid JSON: %v", vector.Name, err)
		}
		if err := compiledRemoteErrorSchema.Validate(instance); err == nil {
			t.Fatalf("remote grammar vector %q was accepted by structural schema", vector.Name)
		}
		_, err := ParseRemoteError([]byte(vector.Body))
		apiError := requireSDKError(t, err)
		if vector.Valid || vector.Error.Code != "remote_protocol_error" || apiError.Code() != vector.Error.Code || apiError.Details().Reason() != vector.Error.Reason || apiError.Details().Pointer() != vector.Error.Pointer {
			t.Fatalf("remote grammar vector %q error = %#v", vector.Name, apiError.Details())
		}
		seenGrammarFields[vector.Field] = struct{}{}
	}
	for _, field := range []string{"domain", "code", "message", "requestId", "traceId"} {
		if _, ok := seenGrammarFields[field]; !ok {
			t.Fatalf("shared corpus is missing control grammar for %s", field)
		}
	}
	sawJCSNumbers := false
	for _, vector := range corpus.Errors {
		remote, err := ParseRemoteError([]byte(vector.Body))
		if vector.Valid {
			if err != nil {
				t.Fatalf("error vector %q: %v", vector.Name, err)
			}
			if vector.Canonical != "" {
				canonical, encodeErr := remote.CanonicalJSON()
				if encodeErr != nil || string(canonical) != vector.Canonical {
					t.Fatalf("error vector %q canonical = %s, %v", vector.Name, canonical, encodeErr)
				}
			}
			if vector.Name == "rfc8785-number-canonicalization" {
				sawJCSNumbers = true
			}
			continue
		}
		apiError := requireSDKError(t, err)
		if apiError.Details().Reason() != vector.Error.Reason || apiError.Details().Pointer() != vector.Error.Pointer {
			t.Fatalf("error vector %q error = %#v", vector.Name, apiError.Details())
		}
	}
	if !sawJCSNumbers {
		t.Fatal("shared corpus is missing the RFC 8785 number vector")
	}
	for _, vector := range corpus.RemoteErrorLimitRecipes {
		if vector.Kind != "nested-object-members-through-array" {
			t.Fatalf("remote limit recipe %q kind = %q", vector.Name, vector.Kind)
		}
		details := nestedRemoteMemberDetails(vector.FirstObjectMembers, vector.SecondObjectMembers)
		_, err := NewRemoteError(RemoteErrorSpec{Domain: "sample", Code: "not_found", Message: "missing", DetailsJSON: []byte(details)})
		body := remoteErrorBody(`"message":"missing","details":` + details)
		_, parseErr := ParseRemoteError([]byte(body))
		if vector.Valid {
			if err != nil || parseErr != nil {
				t.Fatalf("remote limit recipe %q rejected: constructor=%v parser=%v", vector.Name, err, parseErr)
			}
			continue
		}
		for _, failure := range []error{err, parseErr} {
			apiError := requireSDKError(t, failure)
			if apiError.Code() != vector.Error.Code || apiError.Details().Reason() != vector.Error.Reason || apiError.Details().Pointer() != vector.Error.Pointer {
				t.Fatalf("remote limit recipe %q error = %#v", vector.Name, apiError.Details())
			}
		}
	}
}

func nestedRemoteMemberDetails(firstMembers, secondMembers int) string {
	object := func(prefix string, count int) string {
		members := make([]string, count)
		for index := range members {
			members[index] = fmt.Sprintf(`"%s%03d":true`, prefix, index)
		}
		return `{` + strings.Join(members, ",") + `}`
	}
	return `{"groups":[` + object("a", firstMembers) + `,` + object("b", secondMembers) + `]}`
}

func requireSDKError(t *testing.T, err error) *Error {
	t.Helper()
	var apiError *Error
	if !errors.As(err, &apiError) {
		t.Fatalf("error = %T %v, want *api.Error", err, err)
	}
	return apiError
}

func requireRemoteProtocolError(t *testing.T, err error, reason, pointer string) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("operation succeeded, want %s at %q", reason, pointer)
	}
	apiError := requireSDKError(t, err)
	details := apiError.Details()
	if apiError.Code() != "remote_protocol_error" || apiError.Domain() != "nexa.sdk.api" || apiError.Category() != protocol.CategoryExternal || apiError.Retryable() || details.Reason() != reason || details.Pointer() != pointer {
		t.Fatalf("error projection = (%q, %q, %q, %t, %q, %q)", apiError.Code(), apiError.Domain(), apiError.Category(), apiError.Retryable(), details.Reason(), details.Pointer())
	}
	return apiError
}

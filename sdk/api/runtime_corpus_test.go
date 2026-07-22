package api

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/cli/protocol"
)

func TestRuntimeCorpusLoadsClosedCompleteFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/runtime-api-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := ParseRuntimeCorpus(data)
	if err != nil {
		t.Fatalf("ParseRuntimeCorpus() error = %v", err)
	}
	if corpus.APIVersion() != RuntimeCorpusAPIVersion {
		t.Fatalf("API version = %q", corpus.APIVersion())
	}
	wantAdapterCases := []string{
		"success-json", "success-json-one-byte", "success-none-close-panic", "mapped-remote-error-close-failure",
		"request-field-required", "provider-failure", "transport-failure", "response-and-failure", "body-read-failure",
		"body-size-limit", "pre-call-canceled", "pre-call-deadline", "transport-canceled", "body-read-canceled",
		"invalid-response-header-name", "invalid-response-header-value", "required-response-body", "provider-panic",
		"provider-canceled", "provider-deadline", "transport-panic", "zero-response", "transport-deadline",
		"body-read-panic", "body-read-deadline",
	}
	adapterCases := corpus.AdapterCases()
	gotAdapterCases := make([]string, len(adapterCases))
	contextBehaviors := map[RuntimeAdapterContextBehavior]bool{}
	providerBehaviors := map[RuntimeAdapterProviderBehavior]bool{}
	transportBehaviors := map[RuntimeAdapterTransportBehavior]bool{}
	readBehaviors := map[RuntimeAdapterReadBehavior]bool{}
	closeBehaviors := map[RuntimeAdapterCloseBehavior]bool{}
	for index, test := range adapterCases {
		gotAdapterCases[index] = test.Name
		contextBehaviors[test.ContextBehavior] = true
		providerBehaviors[test.ProviderBehavior] = true
		transportBehaviors[test.Transport.Behavior] = true
		readBehaviors[test.Transport.ReadBehavior] = true
		closeBehaviors[test.Transport.CloseBehavior] = true
	}
	if strings.Join(gotAdapterCases, "\n") != strings.Join(wantAdapterCases, "\n") {
		t.Fatalf("adapter case roster = %q, want %q", gotAdapterCases, wantAdapterCases)
	}
	for behavior := range map[RuntimeAdapterContextBehavior]bool{
		RuntimeAdapterContextActive: true, RuntimeAdapterContextCanceled: true, RuntimeAdapterContextDeadline: true,
	} {
		if !contextBehaviors[behavior] {
			t.Fatalf("adapter cases do not execute context behavior %q", behavior)
		}
	}
	for behavior := range map[RuntimeAdapterProviderBehavior]bool{
		RuntimeAdapterProviderValues: true, RuntimeAdapterProviderFailure: true, RuntimeAdapterProviderPanic: true,
		RuntimeAdapterProviderCancel: true, RuntimeAdapterProviderDeadline: true,
	} {
		if !providerBehaviors[behavior] {
			t.Fatalf("adapter cases do not execute provider behavior %q", behavior)
		}
	}
	for behavior := range map[RuntimeAdapterTransportBehavior]bool{
		RuntimeAdapterTransportResponse: true, RuntimeAdapterTransportFailure: true, RuntimeAdapterTransportPanic: true,
		RuntimeAdapterTransportResponseAndFailure: true, RuntimeAdapterTransportZero: true,
		RuntimeAdapterTransportCancel: true, RuntimeAdapterTransportDeadline: true,
	} {
		if !transportBehaviors[behavior] {
			t.Fatalf("adapter cases do not execute transport behavior %q", behavior)
		}
	}
	for behavior := range map[RuntimeAdapterReadBehavior]bool{
		RuntimeAdapterReadAll: true, RuntimeAdapterReadOneByte: true, RuntimeAdapterReadFailure: true,
		RuntimeAdapterReadPanic: true, RuntimeAdapterReadCancel: true, RuntimeAdapterReadDeadline: true,
		RuntimeAdapterReadForbidden: true, RuntimeAdapterReadAbsent: true,
	} {
		if !readBehaviors[behavior] {
			t.Fatalf("adapter cases do not execute read behavior %q", behavior)
		}
	}
	for behavior := range map[RuntimeAdapterCloseBehavior]bool{
		RuntimeAdapterCloseSuccess: true, RuntimeAdapterCloseFailure: true, RuntimeAdapterClosePanic: true,
	} {
		if !closeBehaviors[behavior] {
			t.Fatalf("adapter cases do not execute close behavior %q", behavior)
		}
	}
	if len(corpus.ManifestJSON()) == 0 {
		t.Fatal("corpus Manifest projection is empty")
	}
	canonical, err := corpus.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	reparsed, err := ParseRuntimeCorpus(canonical)
	if err != nil {
		t.Fatalf("ParseRuntimeCorpus(canonical) error = %v", err)
	}
	again, err := reparsed.CanonicalJSON()
	if err != nil || !bytes.Equal(again, canonical) {
		t.Fatalf("canonical corpus is unstable: %v", err)
	}

	schema := RuntimeCorpusSchema()
	if len(schema) == 0 || !json.Valid(schema) {
		t.Fatal("RuntimeCorpusSchema() is not JSON")
	}
	schema[0] = '['
	if fresh := RuntimeCorpusSchema(); len(fresh) == 0 || fresh[0] != '{' {
		t.Fatal("RuntimeCorpusSchema() mutation escaped")
	}

	cases := corpus.AdapterCases()
	firstName := cases[0].Name
	cases[0] = RuntimeAdapterCase{}
	if corpus.AdapterCases()[0].Name != firstName {
		t.Fatal("AdapterCases() mutation escaped")
	}
	manifest := corpus.ManifestJSON()
	manifest[0] = '['
	if corpus.ManifestJSON()[0] != '{' {
		t.Fatal("ManifestJSON() mutation escaped")
	}
}

func TestRuntimeCorpusRejectsClosedDocumentViolations(t *testing.T) {
	data, err := os.ReadFile("testdata/runtime-api-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	mutate := func(fn func(map[string]any)) []byte {
		t.Helper()
		var value map[string]any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		fn(value)
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	vectors := []struct {
		name string
		data []byte
	}{
		{name: "top-level unknown", data: mutate(func(value map[string]any) { value["unknown"] = true })},
		{name: "missing section", data: mutate(func(value map[string]any) { delete(value, "adapterCases") })},
		{name: "null section", data: mutate(func(value map[string]any) { value["adapterCases"] = nil })},
		{name: "invalid behavior enum", data: mutate(func(value map[string]any) {
			cases := value["adapterCases"].([]any)
			cases[0].(map[string]any)["transport"].(map[string]any)["behavior"] = "invented"
		})},
		{name: "endpoint malformed", data: mutate(func(value map[string]any) {
			value["adapterCases"].([]any)[0].(map[string]any)["endpoint"] = "%"
		})},
		{name: "endpoint relative", data: mutate(func(value map[string]any) {
			value["adapterCases"].([]any)[0].(map[string]any)["endpoint"] = "/relative"
		})},
		{name: "endpoint userinfo", data: mutate(func(value map[string]any) {
			value["adapterCases"].([]any)[0].(map[string]any)["endpoint"] = "https://user:secret@api.example.test"
		})},
		{name: "endpoint fragment", data: mutate(func(value map[string]any) {
			value["adapterCases"].([]any)[0].(map[string]any)["endpoint"] = "https://api.example.test/#secret"
		})},
		{name: "endpoint query", data: mutate(func(value map[string]any) {
			value["adapterCases"].([]any)[0].(map[string]any)["endpoint"] = "https://api.example.test?secret=value"
		})},
		{name: "endpoint force query", data: mutate(func(value map[string]any) {
			value["adapterCases"].([]any)[0].(map[string]any)["endpoint"] = "https://api.example.test?"
		})},
		{name: "endpoint raw path", data: mutate(func(value map[string]any) {
			value["adapterCases"].([]any)[0].(map[string]any)["endpoint"] = "https://api.example.test/%2Fsecret"
		})},
		{name: "endpoint invalid prefix", data: mutate(func(value map[string]any) {
			value["adapterCases"].([]any)[0].(map[string]any)["endpoint"] = "https://api.example.test/a/../b"
		})},
		{name: "incomplete expected error", data: mutate(func(value map[string]any) {
			cases := value["adapterCases"].([]any)
			for _, raw := range cases {
				outcome := raw.(map[string]any)["expected"].(map[string]any)["outcome"].(map[string]any)
				if failure, ok := outcome["error"].(map[string]any); ok {
					delete(failure, "remoteDetailsAbsent")
					return
				}
			}
			t.Fatal("fixture has no expected error row")
		})},
	}
	duplicate := bytes.Replace(data, []byte(`"apiVersion":"`), []byte(`"apiVersion":"nexa.dev/runtime-api-conformance/v1","apiVersion":"`), 1)
	vectors = append(vectors, struct {
		name string
		data []byte
	}{name: "duplicate member", data: duplicate})
	for _, section := range []string{
		"requests", "credentials", "wireRequests", "responses", "responseLimitRecipes", "remoteErrorGrammar",
		"remoteErrorLimitRecipes", "errors", "runtimeContractBases", "runtimeContractCases", "adapterCases",
	} {
		section := section
		vectors = append(vectors,
			struct {
				name string
				data []byte
			}{name: section + " missing required case", data: mutate(func(value map[string]any) {
				rows := value[section].([]any)
				value[section] = rows[1:]
			})},
			struct {
				name string
				data []byte
			}{name: section + " duplicate case", data: mutate(func(value map[string]any) {
				rows := value[section].([]any)
				value[section] = append(rows, rows[0])
			})},
			struct {
				name string
				data []byte
			}{name: section + " extra case", data: mutate(func(value map[string]any) {
				rows := value[section].([]any)
				encoded, _ := json.Marshal(rows[0])
				var extra map[string]any
				_ = json.Unmarshal(encoded, &extra)
				extra["name"] = extra["name"].(string) + "-extra"
				value[section] = append(rows, extra)
			})},
			struct {
				name string
				data []byte
			}{name: section + " renamed case", data: mutate(func(value map[string]any) {
				rows := value[section].([]any)
				rows[0].(map[string]any)["name"] = rows[0].(map[string]any)["name"].(string) + "-renamed"
			})},
		)
	}
	vectors = append(vectors,
		struct {
			name string
			data []byte
		}{name: "request success union has error", data: mutate(func(value map[string]any) {
			value["requests"].([]any)[0].(map[string]any)["error"] = map[string]any{"code": "request_invalid", "reason": "invalid_json", "pointer": ""}
		})},
		struct {
			name string
			data []byte
		}{name: "credential success union has error", data: mutate(func(value map[string]any) {
			value["credentials"].([]any)[0].(map[string]any)["error"] = map[string]any{"code": "request_invalid", "reason": "credential_count_invalid", "pointer": "/credentials"}
		})},
		struct {
			name string
			data []byte
		}{name: "response success union has error", data: mutate(func(value map[string]any) {
			value["responses"].([]any)[0].(map[string]any)["error"] = map[string]any{"code": "remote_protocol_error", "reason": "response_body_empty", "pointer": "/body"}
		})},
		struct {
			name string
			data []byte
		}{name: "limit success union has error", data: mutate(func(value map[string]any) {
			value["responseLimitRecipes"].([]any)[0].(map[string]any)["error"] = map[string]any{"code": "remote_protocol_error", "reason": "response_depth_limit_exceeded", "pointer": "/body"}
		})},
		struct {
			name string
			data []byte
		}{name: "remote limit success union has error", data: mutate(func(value map[string]any) {
			value["remoteErrorLimitRecipes"].([]any)[0].(map[string]any)["error"] = map[string]any{"code": "remote_protocol_error", "reason": "details_member_limit_exceeded", "pointer": "/details"}
		})},
		struct {
			name string
			data []byte
		}{name: "remote error success union has error", data: mutate(func(value map[string]any) {
			value["errors"].([]any)[0].(map[string]any)["error"] = map[string]any{"code": "remote_protocol_error", "reason": "document_invalid", "pointer": ""}
		})},
		struct {
			name string
			data []byte
		}{name: "adapter outcome has success and error", data: mutate(func(value map[string]any) {
			outcome := value["adapterCases"].([]any)[0].(map[string]any)["expected"].(map[string]any)["outcome"].(map[string]any)
			outcome["error"] = map[string]any{"domain": "nexa.sdk.api", "code": "transport_error", "message": "API transport failed", "category": "external", "retryable": false, "apiOperationId": "sample.get", "requestId": "", "traceId": "", "reason": "round_trip_failed", "pointer": "", "httpStatus": 0, "remoteDomain": "", "remoteCode": "", "remoteDetailsAbsent": true}
		})},
	)
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			if _, err := ParseRuntimeCorpus(vector.data); err == nil {
				t.Fatal("ParseRuntimeCorpus() accepted invalid corpus")
			}
		})
	}
}

func TestRuntimeCorpusParserRequiresClosedBehaviorCoverage(t *testing.T) {
	data, err := RuntimeCorpusBytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, vector := range []struct {
		name   string
		mutate func(map[string]any) bool
	}{
		{name: "context", mutate: replaceLastAdapterBehavior("contextBehavior", "deadline", "active")},
		{name: "provider", mutate: replaceLastAdapterBehavior("providerBehavior", "panic", "values")},
		{name: "transport", mutate: replaceLastTransportBehavior("behavior", "zero", "response")},
		{name: "read", mutate: replaceLastTransportBehavior("readBehavior", "absent", "forbidden")},
		{name: "close", mutate: replaceLastTransportBehavior("closeBehavior", "failure", "success")},
	} {
		t.Run(vector.name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			if !vector.mutate(document) {
				t.Fatal("fixture does not contain target behavior")
			}
			encoded, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseRuntimeCorpus(encoded); err == nil {
				t.Fatal("ParseRuntimeCorpus() accepted incomplete closed behavior coverage")
			}
		})
	}
}

func TestRuntimeCorpusSchemaRejectsImpossibleAdapterOutcomes(t *testing.T) {
	data, err := RuntimeCorpusBytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, vector := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "json without has-json", mutate: func(document map[string]any) {
			success := document["adapterCases"].([]any)[0].(map[string]any)["expected"].(map[string]any)["outcome"].(map[string]any)["success"].(map[string]any)
			success["hasJSON"] = false
		}},
		{name: "none with json", mutate: func(document map[string]any) {
			success := document["adapterCases"].([]any)[2].(map[string]any)["expected"].(map[string]any)["outcome"].(map[string]any)["success"].(map[string]any)
			success["hasJSON"] = true
		}},
		{name: "invalid error status", mutate: func(document map[string]any) {
			failure := document["adapterCases"].([]any)[3].(map[string]any)["expected"].(map[string]any)["outcome"].(map[string]any)["error"].(map[string]any)
			failure["httpStatus"] = 199
		}},
	} {
		t.Run(vector.name, func(t *testing.T) {
			var document map[string]any
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			if err := decoder.Decode(&document); err != nil {
				t.Fatal(err)
			}
			vector.mutate(document)
			schema, err := runtimeCorpusDocumentSchema()
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(document); err == nil {
				t.Fatal("runtime corpus schema accepted impossible adapter outcome")
			}
		})
	}
}

func TestRuntimeCorpusParserRejectsNoncanonicalExpectedSuccess(t *testing.T) {
	data, err := RuntimeCorpusBytes()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	success := document["adapterCases"].([]any)[0].(map[string]any)["expected"].(map[string]any)["outcome"].(map[string]any)["success"].(map[string]any)
	success["canonicalJSON"] = `{ "displayName": "Sample" }`
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRuntimeCorpus(encoded); err == nil {
		t.Fatal("ParseRuntimeCorpus() accepted noncanonical expected success JSON")
	}
}

func replaceLastAdapterBehavior(field, target, replacement string) func(map[string]any) bool {
	return func(document map[string]any) bool {
		for _, raw := range document["adapterCases"].([]any) {
			row := raw.(map[string]any)
			if row[field] == target {
				row[field] = replacement
				return true
			}
		}
		return false
	}
}

func replaceLastTransportBehavior(field, target, replacement string) func(map[string]any) bool {
	return func(document map[string]any) bool {
		for _, raw := range document["adapterCases"].([]any) {
			transport := raw.(map[string]any)["transport"].(map[string]any)
			if transport[field] == target {
				transport[field] = replacement
				return true
			}
		}
		return false
	}
}

func TestRuntimeCorpusExpectedAdapterResultIsIndependent(t *testing.T) {
	data, err := os.ReadFile("testdata/runtime-api-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := ParseRuntimeCorpus(data)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := corpus.ExpectedAdapterResult()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := expected.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRuntimeAdapterResult(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Cases()) != len(corpus.AdapterCases()) {
		t.Fatalf("result cases = %d, corpus cases = %d", len(parsed.Cases()), len(corpus.AdapterCases()))
	}
}

func TestRuntimeCorpusBytesAreCanonicalAndDefensive(t *testing.T) {
	first, err := RuntimeCorpusBytes()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRuntimeCorpus(first)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := parsed.CanonicalJSON()
	if err != nil || !bytes.Equal(first, canonical) {
		t.Fatalf("RuntimeCorpusBytes() is not canonical: %v", err)
	}
	first[0] = '['
	second, err := RuntimeCorpusBytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(second) == 0 || second[0] != '{' {
		t.Fatal("RuntimeCorpusBytes() mutation escaped")
	}
}

func TestRuntimeCorpusRejectsRawInputOneOverOwnerLimit(t *testing.T) {
	data, err := RuntimeCorpusBytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) >= RuntimeCorpusRawBytes {
		t.Fatalf("owner corpus bytes = %d, limit = %d", len(data), RuntimeCorpusRawBytes)
	}
	exact := append(append([]byte(nil), data...), bytes.Repeat([]byte{' '}, RuntimeCorpusRawBytes-len(data))...)
	if _, err := ParseRuntimeCorpus(exact); err != nil {
		t.Fatalf("ParseRuntimeCorpus(exact) error = %v", err)
	}
	if _, err := ParseRuntimeCorpus(append(exact, ' ')); err == nil {
		t.Fatal("ParseRuntimeCorpus() accepted one-over raw input")
	}
}

func TestRuntimeConformanceSchemasCloseIntegerDomainsBelowBinary64Boundary(t *testing.T) {
	for name, schema := range map[string][]byte{
		"corpus": RuntimeCorpusSchema(),
		"result": RuntimeAdapterResultSchema(),
	} {
		t.Run(name, func(t *testing.T) {
			decoder := json.NewDecoder(bytes.NewReader(schema))
			decoder.UseNumber()
			var root any
			if err := decoder.Decode(&root); err != nil {
				t.Fatal(err)
			}
			integerSchemas := 0
			var visit func(any, string)
			visit = func(value any, pointer string) {
				switch typed := value.(type) {
				case map[string]any:
					if typed["type"] == "integer" {
						integerSchemas++
						maximum, ok := typed["maximum"].(json.Number)
						if !ok {
							t.Errorf("integer schema %s has no closed maximum", pointer)
						} else if value, err := maximum.Int64(); err != nil || value >= 1<<53 {
							t.Errorf("integer schema %s maximum = %q, want integer < 2^53", pointer, maximum)
						}
					}
					for key, child := range typed {
						visit(child, pointer+"/"+key)
					}
				case []any:
					for index, child := range typed {
						visit(child, pointer+"/"+strconv.Itoa(index))
					}
				}
			}
			visit(root, "")
			if integerSchemas == 0 {
				t.Fatal("schema has no integer domains")
			}
		})
	}
}

func TestRuntimeCorpusAndResultRejectLargeIntegerBeforeRounding(t *testing.T) {
	corpusData, err := RuntimeCorpusBytes()
	if err != nil {
		t.Fatal(err)
	}
	largeCorpus := bytes.Replace(corpusData, []byte(`"maxResponseBytes":1024`), []byte(`"maxResponseBytes":9007199254740992`), 1)
	if _, err := ParseRuntimeCorpus(largeCorpus); err == nil {
		t.Fatal("ParseRuntimeCorpus() accepted integer at 2^53")
	}

	result, err := NewRuntimeAdapterResult([]RuntimeAdapterCaseResult{{
		Name: "large-integer", RequestDigest: "absent", ProviderCalls: 1,
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
	largeResult := bytes.Replace(encoded, []byte(`"providerCalls":1`), []byte(`"providerCalls":9007199254740992`), 1)
	if _, err := ParseRuntimeAdapterResult(largeResult); err == nil {
		t.Fatal("ParseRuntimeAdapterResult() accepted integer at 2^53")
	}
}

func loadRuntimeCorpusDocument(t *testing.T) runtimeCorpusDocument {
	t.Helper()
	data, err := RuntimeCorpusBytes()
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := ParseRuntimeCorpus(data)
	if err != nil {
		t.Fatal(err)
	}
	return cloneRuntimeCorpusDocument(corpus.document)
}

func runtimeAdapterHeadersToHeaders(input []RuntimeAdapterHeader) []Header {
	result := make([]Header, len(input))
	for index, header := range input {
		result[index] = Header{Name: header.Name, Value: header.Value}
	}
	return result
}

func TestRuntimeCorpusNativeRelationAndPrecedenceCases(t *testing.T) {
	corpus := loadRuntimeCorpusDocument(t)
	bases := make(map[string][]byte, len(corpus.RuntimeContractBases))
	for _, base := range corpus.RuntimeContractBases {
		if _, duplicate := bases[base.Name]; duplicate {
			t.Fatalf("duplicate runtime contract base %q", base.Name)
		}
		if _, err := ParseRuntimeContract([]byte(base.Document)); err != nil {
			t.Fatalf("runtime contract base %q: %v", base.Name, err)
		}
		bases[base.Name] = []byte(base.Document)
	}
	want := []struct {
		name, reason, pointer string
	}{
		{"schema-index-invalid", "runtime_schema_index_invalid", "/schemas/1/fields/id/schema"},
		{"schema-duplicate", "runtime_schema_duplicate", "/schemas/4"},
		{"operation-schema-index-invalid", "runtime_operation_schema_index_invalid", "/operations/sample.get/request/schema"},
		{"request-schema-kind-invalid", "runtime_request_schema_kind_invalid", "/operations/sample.get/request/schema"},
		{"binding-field-unresolved", "runtime_binding_field_unresolved", "/operations/sample.get/request/bindings/other"},
		{"binding-field-missing", "runtime_binding_field_missing", "/operations/sample.get/request/bindings"},
		{"binding-schema-kind-invalid", "runtime_binding_schema_kind_invalid", "/operations/sample.call/request/bindings/payload"},
		{"path-field-optional", "runtime_path_field_optional", "/operations/sample.get/request/bindings/id"},
		{"path-binding-mismatch", "runtime_path_binding_mismatch", "/operations/sample.get/pathSegments"},
		{"path-binding-name-invalid", "runtime_path_binding_name_invalid", "/operations/sample.get/request/bindings/id/name"},
		{"path-invalid", "runtime_path_invalid", "/operations/sample.get/pathSegments"},
		{"binding-wire-target-duplicate", "runtime_binding_wire_target_duplicate", "/operations/sample.call/request/bindings/b/name"},
		{"header-name-reserved", "runtime_header_name_reserved", "/operations/sample.call/request/bindings/b/name"},
		{"credential-combination-invalid", "runtime_credential_combination_invalid", "/operations/sample.get/auth/credentials"},
		{"credential-wire-target-duplicate", "runtime_credential_wire_target_duplicate", "/operations/sample.call/auth/credentials/key2/name"},
		{"credential-binding-conflict", "runtime_credential_binding_conflict", "/operations/sample.call/auth/credentials/key1/name"},
		{"permission-auth-conflict", "runtime_permission_auth_conflict", "/operations/sample.get/permission"},
		{"capability-version-invalid", "runtime_capability_version_invalid", "/operations/sample.get/capability/apiVersion"},
		{"compound-schema-before-duplicate", "runtime_schema_index_invalid", "/schemas/3/fields/name/schema"},
		{"compound-request-before-binding", "runtime_operation_schema_index_invalid", "/operations/sample.get/request/schema"},
		{"compound-unresolved-before-missing", "runtime_binding_field_unresolved", "/operations/sample.get/request/bindings/other"},
		{"compound-scalar-before-wire", "runtime_binding_schema_kind_invalid", "/operations/sample.call/request/bindings/payload"},
		{"compound-auth-before-credential", "runtime_credential_combination_invalid", "/operations/sample.get/auth/credentials"},
		{"compound-permission-before-capability", "runtime_permission_auth_conflict", "/operations/sample.get/permission"},
		{"schema-array-items-invalid", "runtime_schema_index_invalid", "/schemas/2/items"},
		{"response-schema-index-invalid", "runtime_operation_schema_index_invalid", "/operations/sample.get/response/schema"},
		{"bearer-location-invalid", "runtime_credential_combination_invalid", "/operations/sample.get/auth/credentials/access/in"},
		{"bearer-name-invalid", "runtime_credential_combination_invalid", "/operations/sample.get/auth/credentials/access/name"},
		{"session-cookie-location-invalid", "runtime_credential_combination_invalid", "/operations/sample.call/auth/credentials/key1/in"},
		{"cookie-credential-binding-conflict", "runtime_credential_binding_conflict", "/operations/sample.call/auth/credentials/key1/name"},
		{"compound-response-before-binding", "runtime_operation_schema_index_invalid", "/operations/sample.get/response/schema"},
		{"compound-request-kind-before-binding", "runtime_request_schema_kind_invalid", "/operations/sample.get/request/schema"},
		{"compound-missing-before-scalar", "runtime_binding_field_missing", "/operations/sample.call/request/bindings"},
		{"compound-path-required-before-set", "runtime_path_field_optional", "/operations/sample.get/request/bindings/id"},
		{"compound-path-set-before-name", "runtime_path_binding_mismatch", "/operations/sample.get/pathSegments"},
		{"compound-path-name-before-shape", "runtime_path_binding_name_invalid", "/operations/sample.get/request/bindings/id/name"},
		{"compound-wire-before-reserved", "runtime_binding_wire_target_duplicate", "/operations/sample.call/request/bindings/b/name"},
		{"compound-reserved-before-credential", "runtime_header_name_reserved", "/operations/sample.call/request/bindings/b/name"},
		{"compound-credential-in-before-name", "runtime_credential_combination_invalid", "/operations/sample.get/auth/credentials/access/in"},
		{"compound-credential-combination-before-wire", "runtime_credential_combination_invalid", "/operations/sample.call/auth/credentials/key1/in"},
		{"compound-credential-wire-before-binding", "runtime_credential_wire_target_duplicate", "/operations/sample.call/auth/credentials/key2/name"},
		{"compound-binding-before-permission", "runtime_credential_binding_conflict", "/operations/sample.call/auth/credentials/key1/name"},
	}
	if len(corpus.RuntimeContractCases) != len(want) {
		t.Fatalf("runtime contract case count = %d, want %d", len(corpus.RuntimeContractCases), len(want))
	}
	for index, test := range corpus.RuntimeContractCases {
		expected := want[index]
		if test.Name != expected.name || test.Expected.Code != "runtime_contract_invalid" || test.Expected.Reason != expected.reason || test.Expected.Pointer != expected.pointer {
			t.Fatalf("runtime contract case %d = %q/%q/%q/%q, want %q/runtime_contract_invalid/%q/%q", index, test.Name, test.Expected.Code, test.Expected.Reason, test.Expected.Pointer, expected.name, expected.reason, expected.pointer)
		}
		t.Run(test.Name, func(t *testing.T) {
			base, ok := bases[test.Base]
			if !ok {
				t.Fatalf("unknown base %q", test.Base)
			}
			candidate := applyRuntimeContractCorpusMutations(t, base, test.Mutations)
			_, err := ParseRuntimeContract(candidate)
			apiError, ok := err.(*Error)
			if !ok || apiError == nil {
				t.Fatalf("error = %T %v", err, err)
			}
			actual := runtimeAdapterErrorFromSDK(apiError)
			if actual.Code != test.Expected.Code || actual.Reason != test.Expected.Reason || actual.Pointer != test.Expected.Pointer {
				t.Fatalf("error = %#v, want code/reason/pointer %#v", actual, test.Expected)
			}
		})
	}
}

func TestRuntimeCorpusEscapedFieldIsRejectedByClosedRuntimeContractSchema(t *testing.T) {
	corpus := loadRuntimeCorpusDocument(t)
	var relationBase []byte
	for _, base := range corpus.RuntimeContractBases {
		if base.Name == "relation" {
			relationBase = []byte(base.Document)
			break
		}
	}
	if len(relationBase) == 0 {
		t.Fatal("relation runtime contract base is missing")
	}
	candidate := applyRuntimeContractCorpusMutations(t, relationBase, []runtimeContractCorpusMutation{
		{Operation: runtimeContractMutationSet, Pointer: "/schemas/2/fields/a~1b~0c", ValueJSON: `{"required":true,"schema":1}`},
		{Operation: runtimeContractMutationSet, Pointer: "/operations/sample.call/request/bindings/a~1b~0c", ValueJSON: `{"in":"query","name":"qa"}`},
	})
	_, err := ParseRuntimeContract(candidate)
	apiError, ok := err.(*Error)
	if !ok || apiError == nil {
		t.Fatalf("error = %T %v", err, err)
	}
	if got := runtimeAdapterErrorFromSDK(apiError); got.Code != "runtime_contract_invalid" || got.Reason != "runtime_contract_schema_invalid" || got.Pointer != "/runtimeContract" {
		t.Fatalf("error = %#v", got)
	}
}

func applyRuntimeContractCorpusMutations(t *testing.T, base []byte, mutations []runtimeContractCorpusMutation) []byte {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(base))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range mutations {
		segments := strings.Split(strings.TrimPrefix(mutation.Pointer, "/"), "/")
		current := root
		for _, raw := range segments[:len(segments)-1] {
			segment := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
			switch value := current.(type) {
			case map[string]any:
				current = value[segment]
			case []any:
				index, err := strconv.Atoi(segment)
				if err != nil || index < 0 || index >= len(value) {
					t.Fatalf("invalid mutation pointer %q", mutation.Pointer)
				}
				current = value[index]
			default:
				t.Fatalf("invalid mutation parent %q", mutation.Pointer)
			}
		}
		leaf := strings.ReplaceAll(strings.ReplaceAll(segments[len(segments)-1], "~1", "/"), "~0", "~")
		parent, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("mutation parent is not object: %q", mutation.Pointer)
		}
		switch mutation.Operation {
		case runtimeContractMutationSet:
			valueDecoder := json.NewDecoder(strings.NewReader(mutation.ValueJSON))
			valueDecoder.UseNumber()
			var value any
			if err := valueDecoder.Decode(&value); err != nil {
				t.Fatal(err)
			}
			parent[leaf] = value
		case runtimeContractMutationRemove:
			delete(parent, leaf)
		case runtimeContractMutationAppend:
			array, ok := parent[leaf].([]any)
			if !ok {
				t.Fatalf("append target is not array: %q", mutation.Pointer)
			}
			valueDecoder := json.NewDecoder(strings.NewReader(mutation.ValueJSON))
			valueDecoder.UseNumber()
			var value any
			if err := valueDecoder.Decode(&value); err != nil {
				t.Fatal(err)
			}
			parent[leaf] = append(array, value)
		default:
			t.Fatalf("invalid mutation operation %q", mutation.Operation)
		}
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func runtimeAdapterErrorFromSDK(input *Error) RuntimeAdapterError {
	details := input.Details()
	return RuntimeAdapterError{
		Domain: input.Domain(), Code: input.Code(), Message: input.Error(), Category: input.Category(),
		Retryable: input.Retryable(), APIOperationID: input.APIOperationID(), RequestID: input.RequestID(),
		TraceID: input.TraceID(), Reason: details.Reason(), Pointer: details.Pointer(), HTTPStatus: details.HTTPStatus(),
		RemoteDomain: details.RemoteDomain(), RemoteCode: details.RemoteCode(), RemoteDetailsAbsent: true,
	}
}

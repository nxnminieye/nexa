package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

func TestRequestBindingsAcceptClosedLocationsAndCanonicalizeHeaders(t *testing.T) {
	spec := validWireSpec(t)
	manifest, err := api.NewManifest(spec)
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	operation, _ := manifest.Operation("sample.create")
	bindings := operation.RequestBindings()
	want := []struct {
		field    string
		location api.RequestBindingLocation
		name     string
	}{
		{field: "id", location: api.RequestBindingPath, name: "id"},
		{field: "keyword", location: api.RequestBindingQuery, name: "keyword"},
		{field: "payload", location: api.RequestBindingBody, name: "payload"},
		{field: "requestID", location: api.RequestBindingHeader, name: "x-request-id"},
		{field: "tags", location: api.RequestBindingBody, name: "tags"},
	}
	if len(bindings) != len(want) {
		t.Fatalf("bindings = %#v", bindings)
	}
	for index := range want {
		if bindings[index].Field() != want[index].field || bindings[index].Location() != want[index].location || bindings[index].Name() != want[index].name {
			t.Fatalf("binding[%d] = %#v, want %#v", index, bindings[index], want[index])
		}
	}
}

func TestInvalidOperationAndBindingSemantics(t *testing.T) {
	tests := []struct {
		name, reason, pointer string
		mutate                func(*api.ManifestSpec)
	}{
		{name: "operation id", reason: "operation_id_invalid", pointer: "/operations/0/id", mutate: func(s *api.ManifestSpec) { s.Operations[0].ID = "SampleCreate" }},
		{name: "operation duplicate", reason: "operation_duplicate", pointer: "/operations/1/id", mutate: func(s *api.ManifestSpec) { s.Operations = append(s.Operations, cloneOperationSpec(s.Operations[0])) }},
		{name: "method", reason: "method_invalid", pointer: "/operations/0/method", mutate: func(s *api.ManifestSpec) { s.Operations[0].Method = "TRACE" }},
		{name: "path query", reason: "path_invalid", pointer: "/operations/0/path", mutate: func(s *api.ManifestSpec) { s.Operations[0].Path = "/samples/{id}?x=1" }},
		{name: "path mixed template", reason: "path_invalid", pointer: "/operations/0/path", mutate: func(s *api.ManifestSpec) { s.Operations[0].Path = "/samples/id-{id}" }},
		{name: "route duplicate", reason: "route_duplicate", pointer: "/operations/1/path", mutate: func(s *api.ManifestSpec) {
			second := cloneOperationSpec(s.Operations[0])
			second.ID = "sample.duplicate"
			s.Operations = append(s.Operations, second)
		}},
		{name: "request ref invalid", reason: "request_schema_ref_invalid", pointer: "/operations/0/requestSchemaRef", mutate: func(s *api.ManifestSpec) { s.Operations[0].RequestSchemaRef = "Sample Request" }},
		{name: "request ref unresolved", reason: "request_schema_ref_unresolved", pointer: "/operations/0/requestSchemaRef", mutate: func(s *api.ManifestSpec) { s.Operations[0].RequestSchemaRef = "missing" }},
		{name: "request kind", reason: "request_schema_kind_invalid", pointer: "/operations/0/requestSchemaRef", mutate: func(s *api.ManifestSpec) { s.Operations[0].RequestSchemaRef = "scalar.string" }},
		{name: "response body", reason: "response_body_invalid", pointer: "/operations/0/responseBody", mutate: func(s *api.ManifestSpec) { s.Operations[0].ResponseBody = "stream" }},
		{name: "json response required", reason: "response_schema_ref_invalid", pointer: "/operations/0/responseSchemaRef", mutate: func(s *api.ManifestSpec) { s.Operations[0].ResponseSchemaRef = "" }},
		{name: "response unresolved", reason: "response_schema_ref_unresolved", pointer: "/operations/0/responseSchemaRef", mutate: func(s *api.ManifestSpec) { s.Operations[0].ResponseSchemaRef = "missing" }},
		{name: "none response ref forbidden", reason: "response_schema_ref_forbidden", pointer: "/operations/0/responseSchemaRef", mutate: func(s *api.ManifestSpec) { s.Operations[0].ResponseBody = api.ResponseBodyNone }},
		{name: "binding location", reason: "binding_location_invalid", pointer: "/operations/0/requestBindings/0/in", mutate: func(s *api.ManifestSpec) { s.Operations[0].RequestBindings[0].Location = "cookie" }},
		{name: "binding field invalid", reason: "binding_field_invalid", pointer: "/operations/0/requestBindings/0/field", mutate: func(s *api.ManifestSpec) { s.Operations[0].RequestBindings[0].Field = "bad-name" }},
		{name: "binding field unresolved", reason: "binding_field_unresolved", pointer: "/operations/0/requestBindings/1/field", mutate: func(s *api.ManifestSpec) { s.Operations[0].RequestBindings[0].Field = "missing" }},
		{name: "binding name", reason: "binding_name_invalid", pointer: "/operations/0/requestBindings/0/name", mutate: func(s *api.ManifestSpec) { s.Operations[0].RequestBindings[0].Name = "bad name" }},
		{name: "binding duplicate field", reason: "binding_duplicate", pointer: "/operations/0/requestBindings/1/field", mutate: func(s *api.ManifestSpec) { s.Operations[0].RequestBindings[1].Field = "id" }},
		{name: "binding duplicate wire", reason: "binding_wire_name_duplicate", pointer: "/operations/0/requestBindings/4/name", mutate: func(s *api.ManifestSpec) { s.Operations[0].RequestBindings[4].Name = "payload" }},
		{name: "binding schema kind", reason: "binding_schema_kind_invalid", pointer: "/operations/0/requestBindings/2/field", mutate: func(s *api.ManifestSpec) {
			s.Operations[0].RequestBindings[0].Field, s.Operations[0].RequestBindings[3].Field = "payload", "id"
		}},
		{name: "path optional", reason: "binding_path_required", pointer: "/operations/0/requestBindings/0/field", mutate: func(s *api.ManifestSpec) { s.Schemas[0].Fields[0].Required = false }},
		{name: "path mismatch", reason: "binding_path_mismatch", pointer: "/operations/0/path", mutate: func(s *api.ManifestSpec) { s.Operations[0].RequestBindings[0].Name = "sampleID" }},
		{name: "missing field binding", reason: "binding_field_unresolved", pointer: "/operations/0/requestBindings", mutate: func(s *api.ManifestSpec) { s.Operations[0].RequestBindings = s.Operations[0].RequestBindings[:4] }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validWireSpec(t)
			test.mutate(&spec)
			_, err := api.NewManifest(spec)
			manifestError := requireAPIError(t, err, test.reason)
			if manifestError.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", manifestError.Pointer(), test.pointer)
			}
		})
	}
}

func TestCredentialClosedCombinationsAndCanonicalNames(t *testing.T) {
	valid := []api.CredentialSpec{
		{ID: "bearer", Type: api.CredentialBearer, Location: api.CredentialLocationHeader, Name: "Authorization"},
		{ID: "header-key", Type: api.CredentialAPIKey, Location: api.CredentialLocationHeader, Name: "X-API-Key"},
		{ID: "query-key", Type: api.CredentialAPIKey, Location: api.CredentialLocationQuery, Name: "api_key"},
		{ID: "cookie-key", Type: api.CredentialAPIKey, Location: api.CredentialLocationCookie, Name: "APIKey"},
		{ID: "session", Type: api.CredentialSessionCookie, Location: api.CredentialLocationCookie, Name: "SessionID"},
	}
	for _, credential := range valid {
		spec := validWireSpec(t)
		spec.Operations[0].Auth.Credentials = []api.CredentialSpec{credential}
		manifest, err := api.NewManifest(spec)
		if err != nil {
			t.Fatalf("credential %#v rejected: %v", credential, err)
		}
		got := manifest.Operations()[0].Auth().Credentials()[0]
		wantName := credential.Name
		if credential.Location == api.CredentialLocationHeader {
			wantName = strings.ToLower(wantName)
		}
		if got.ID() != credential.ID || got.Type() != credential.Type || got.Location() != credential.Location || got.Name() != wantName {
			t.Fatalf("credential = %#v", got)
		}
	}
}

func TestInvalidCredentialAndBindingConflicts(t *testing.T) {
	tests := []struct {
		name, reason, pointer string
		mutate                func(*api.ManifestSpec)
	}{
		{name: "auth mode", reason: "auth_mode_invalid", pointer: "/operations/0/auth/mode", mutate: func(s *api.ManifestSpec) { s.Operations[0].Auth.Mode = "always" }},
		{name: "none has credential", reason: "credential_combination_invalid", pointer: "/operations/0/auth/credentials", mutate: func(s *api.ManifestSpec) { s.Operations[0].Auth.Mode = api.AuthNone }},
		{name: "required empty", reason: "credential_combination_invalid", pointer: "/operations/0/auth/credentials", mutate: func(s *api.ManifestSpec) { s.Operations[0].Auth.Credentials = nil }},
		{name: "credential id", reason: "credential_id_invalid", pointer: "/operations/0/auth/credentials/0/id", mutate: func(s *api.ManifestSpec) { s.Operations[0].Auth.Credentials[0].ID = "Primary" }},
		{name: "credential duplicate", reason: "credential_duplicate", pointer: "/operations/0/auth/credentials/1/id", mutate: func(s *api.ManifestSpec) {
			s.Operations[0].Auth.Credentials = append(s.Operations[0].Auth.Credentials, s.Operations[0].Auth.Credentials[0])
		}},
		{name: "credential type", reason: "credential_type_invalid", pointer: "/operations/0/auth/credentials/0/type", mutate: func(s *api.ManifestSpec) { s.Operations[0].Auth.Credentials[0].Type = "basic" }},
		{name: "credential location", reason: "credential_location_invalid", pointer: "/operations/0/auth/credentials/0/in", mutate: func(s *api.ManifestSpec) { s.Operations[0].Auth.Credentials[0].Location = "body" }},
		{name: "credential name", reason: "credential_name_invalid", pointer: "/operations/0/auth/credentials/0/name", mutate: func(s *api.ManifestSpec) { s.Operations[0].Auth.Credentials[0].Name = "bad name" }},
		{name: "bearer location", reason: "credential_combination_invalid", pointer: "/operations/0/auth/credentials/0/in", mutate: func(s *api.ManifestSpec) {
			s.Operations[0].Auth.Credentials[0].Location = api.CredentialLocationQuery
			s.Operations[0].Auth.Credentials[0].Name = "authorization"
		}},
		{name: "bearer name", reason: "credential_combination_invalid", pointer: "/operations/0/auth/credentials/0/name", mutate: func(s *api.ManifestSpec) { s.Operations[0].Auth.Credentials[0].Name = "x-token" }},
		{name: "session location", reason: "credential_combination_invalid", pointer: "/operations/0/auth/credentials/0/in", mutate: func(s *api.ManifestSpec) {
			s.Operations[0].Auth.Credentials[0] = api.CredentialSpec{ID: "session", Type: api.CredentialSessionCookie, Location: api.CredentialLocationHeader, Name: "session"}
		}},
		{name: "credential wire collision", reason: "credential_binding_conflict", pointer: "/operations/0/auth/credentials/1/name", mutate: func(s *api.ManifestSpec) {
			s.Operations[0].Auth.Credentials = append(s.Operations[0].Auth.Credentials, api.CredentialSpec{ID: "second", Type: api.CredentialAPIKey, Location: api.CredentialLocationHeader, Name: "Authorization"})
		}},
		{name: "credential ordinary header conflict", reason: "credential_binding_conflict", pointer: "/operations/0/auth/credentials/0/name", mutate: func(s *api.ManifestSpec) { s.Operations[0].RequestBindings[2].Name = "Authorization" }},
		{name: "cookie credential ordinary cookie header", reason: "credential_binding_conflict", pointer: "/operations/0/auth/credentials/0/name", mutate: func(s *api.ManifestSpec) {
			s.Operations[0].Auth.Credentials[0] = api.CredentialSpec{ID: "session", Type: api.CredentialSessionCookie, Location: api.CredentialLocationCookie, Name: "SessionID"}
			s.Operations[0].RequestBindings[2].Name = "Cookie"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validWireSpec(t)
			test.mutate(&spec)
			_, err := api.NewManifest(spec)
			manifestError := requireAPIError(t, err, test.reason)
			if manifestError.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", manifestError.Pointer(), test.pointer)
			}
		})
	}
}

func TestFrameworkReservedContentTypeHeaderIsRejectedAcrossAuthoringPaths(t *testing.T) {
	tests := []struct {
		name               string
		constructorPointer string
		parsePointer       string
		mutate             func(*api.ManifestSpec)
	}{
		{
			name:               "ordinary binding",
			constructorPointer: "/operations/0/requestBindings/3/name",
			parsePointer:       "/operations/0/requestBindings/3/name",
			mutate: func(spec *api.ManifestSpec) {
				spec.Operations[0].RequestBindings[2].Name = "Content-Type"
			},
		},
		{
			name:               "credential binding",
			constructorPointer: "/operations/0/auth/credentials/0/name",
			parsePointer:       "/operations/0/auth/credentials/0/name",
			mutate: func(spec *api.ManifestSpec) {
				spec.Operations[0].Auth.Credentials[0] = api.CredentialSpec{
					ID: "content", Type: api.CredentialAPIKey,
					Location: api.CredentialLocationHeader, Name: "CONTENT-TYPE",
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validWireSpec(t)
			test.mutate(&spec)
			_, err := api.NewManifest(spec)
			manifestError := requireAPIError(t, err, "header_name_reserved")
			if manifestError.Pointer() != test.constructorPointer {
				t.Fatalf("constructor Pointer() = %q, want %q", manifestError.Pointer(), test.constructorPointer)
			}

			valid := validWireSpec(t)
			manifest, err := api.NewManifest(valid)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := manifest.CanonicalJSON()
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(encoded, &document); err != nil {
				t.Fatal(err)
			}
			operation := document["operations"].([]any)[0].(map[string]any)
			if test.name == "ordinary binding" {
				operation["requestBindings"].([]any)[3].(map[string]any)["name"] = "Content-Type"
			} else {
				operation["auth"].(map[string]any)["credentials"] = []any{map[string]any{
					"id": "content", "type": "api-key", "in": "header", "name": "CONTENT-TYPE",
				}}
			}
			encoded, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range []string{"manifest.json", "manifest.yaml"} {
				_, err := api.Parse(source, encoded)
				manifestError := requireAPIError(t, err, "header_name_reserved")
				if manifestError.Pointer() != test.parsePointer {
					t.Fatalf("%s Pointer() = %q, want %q", source, manifestError.Pointer(), test.parsePointer)
				}
			}
		})
	}
}

func TestCookieCredentialHeaderConflictIsIndependentOfBindingOrder(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		spec := validWireSpec(t)
		spec.Operations[0].Auth.Credentials[0] = api.CredentialSpec{ID: "session", Type: api.CredentialSessionCookie, Location: api.CredentialLocationCookie, Name: "SessionID"}
		spec.Operations[0].RequestBindings[2].Name = "Cookie"
		if reverse {
			reverseBindings(spec.Operations[0].RequestBindings)
		}
		_, err := api.NewManifest(spec)
		manifestError := requireAPIError(t, err, "credential_binding_conflict")
		if manifestError.Pointer() != "/operations/0/auth/credentials/0/name" {
			t.Fatalf("reverse=%v Pointer() = %q", reverse, manifestError.Pointer())
		}
	}
}

func TestInvalidPermissionCapabilityAndErrorProjection(t *testing.T) {
	tests := []struct {
		name, reason, pointer string
		mutate                func(*api.ManifestSpec)
	}{
		{name: "permission", reason: "permission_invalid", pointer: "/operations/0/permission", mutate: func(s *api.ManifestSpec) { s.Operations[0].Permission = "Sample Read" }},
		{name: "permission without auth", reason: "permission_auth_conflict", pointer: "/operations/0/permission", mutate: func(s *api.ManifestSpec) {
			s.Operations[0].Auth = api.AuthSpec{Mode: api.AuthNone, Credentials: []api.CredentialSpec{}}
		}},
		{name: "capability id", reason: "capability_id_invalid", pointer: "/operations/0/capability/id", mutate: func(s *api.ManifestSpec) { s.Operations[0].Capability.ID = "Sample API" }},
		{name: "capability version", reason: "capability_version_invalid", pointer: "/operations/0/capability/apiVersion", mutate: func(s *api.ManifestSpec) { s.Operations[0].Capability.APIVersion = "nexa.dev/other/v1" }},
		{name: "capability incomplete", reason: "capability_incomplete", pointer: "/operations/0/capability/apiVersion", mutate: func(s *api.ManifestSpec) { s.Operations[0].Capability.APIVersion = "" }},
		{name: "error match domain", reason: "error_match_domain_invalid", pointer: "/operations/0/errorProjections/0/match/domain", mutate: func(s *api.ManifestSpec) { s.Operations[0].ErrorProjections[0].Match.Domain = "Sample Domain" }},
		{name: "error match code", reason: "error_match_code_invalid", pointer: "/operations/0/errorProjections/0/match/code", mutate: func(s *api.ManifestSpec) { s.Operations[0].ErrorProjections[0].Match.Code = "Not Found" }},
		{name: "error project domain", reason: "error_project_domain_invalid", pointer: "/operations/0/errorProjections/0/project/domain", mutate: func(s *api.ManifestSpec) { s.Operations[0].ErrorProjections[0].Project.Domain = "API Domain" }},
		{name: "error project code", reason: "error_project_code_invalid", pointer: "/operations/0/errorProjections/0/project/code", mutate: func(s *api.ManifestSpec) { s.Operations[0].ErrorProjections[0].Project.Code = "Not Found" }},
		{name: "error status", reason: "error_http_status_invalid", pointer: "/operations/0/errorProjections/0/project/httpStatus", mutate: func(s *api.ManifestSpec) { s.Operations[0].ErrorProjections[0].Project.HTTPStatus = 200 }},
		{name: "error duplicate", reason: "error_projection_duplicate", pointer: "/operations/0/errorProjections/1/match", mutate: func(s *api.ManifestSpec) {
			s.Operations[0].ErrorProjections = append(s.Operations[0].ErrorProjections, s.Operations[0].ErrorProjections[0])
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validWireSpec(t)
			test.mutate(&spec)
			_, err := api.NewManifest(spec)
			manifestError := requireAPIError(t, err, test.reason)
			if manifestError.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", manifestError.Pointer(), test.pointer)
			}
		})
	}
}

func TestCapabilityVersionGrammar(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		apiVersion string
		valid      bool
		reason     string
		pointer    string
	}{
		{name: "v1", id: "nexa.dev/sample-api", apiVersion: "nexa.dev/sample-api/v1", valid: true},
		{name: "extra version segment", id: "nexa.dev/sample-api", apiVersion: "nexa.dev/sample-api/extra/v1", reason: "capability_version_invalid", pointer: "/operations/0/capability/apiVersion"},
		{name: "zero version", id: "nexa.dev/sample-api", apiVersion: "nexa.dev/sample-api/v0", reason: "capability_version_invalid", pointer: "/operations/0/capability/apiVersion"},
		{name: "leading zero", id: "nexa.dev/sample-api", apiVersion: "nexa.dev/sample-api/v01", reason: "capability_version_invalid", pointer: "/operations/0/capability/apiVersion"},
		{name: "extra id slash", id: "nexa.dev/sample-api/extra", apiVersion: "nexa.dev/sample-api/extra/v1", reason: "capability_id_invalid", pointer: "/operations/0/capability/id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validWireSpec(t)
			spec.Operations[0].Capability = &api.CapabilitySpec{ID: test.id, APIVersion: test.apiVersion}
			_, err := api.NewManifest(spec)
			if test.valid {
				if err != nil {
					t.Fatalf("NewManifest() error = %v", err)
				}
				return
			}
			manifestError := requireAPIError(t, err, test.reason)
			if manifestError.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", manifestError.Pointer(), test.pointer)
			}
		})
	}
}

func TestParseRejectsCapabilityVersionExtraSegment(t *testing.T) {
	manifest, err := api.NewManifest(validWireSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	data, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	operation := document["operations"].([]any)[0].(map[string]any)
	operation["capability"].(map[string]any)["apiVersion"] = "nexa.dev/sample-api/extra/v1"
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"manifest.json", "manifest.yaml"} {
		t.Run(source, func(t *testing.T) {
			_, parseErr := api.Parse(source, data)
			manifestError := requireAPIError(t, parseErr, "capability_version_invalid")
			if manifestError.Pointer() != "/operations/0/capability/apiVersion" {
				t.Fatalf("Pointer() = %q", manifestError.Pointer())
			}
			if manifestError.Line() == 0 || manifestError.Column() == 0 {
				t.Fatalf("location = %d:%d", manifestError.Line(), manifestError.Column())
			}
		})
	}
}

func TestIdentifierGrammarsRemainSeparate(t *testing.T) {
	spec := validWireSpec(t)
	spec.Operations[0].ErrorProjections[0] = api.ErrorProjectionSpec{
		Match:   api.ErrorMatchSpec{Domain: "sample_domain", Code: "not_found"},
		Project: api.ErrorTargetSpec{Domain: "api_error", Code: "sample_not_found", HTTPStatus: 404},
	}
	if _, err := api.NewManifest(spec); err != nil {
		t.Fatalf("error snake_case rejected: %v", err)
	}

	spec = validWireSpec(t)
	spec.Operations[0].ID = "sample_create"
	_, err := api.NewManifest(spec)
	manifestError := requireAPIError(t, err, "operation_id_invalid")
	if manifestError.Pointer() != "/operations/0/id" {
		t.Fatalf("Pointer() = %q", manifestError.Pointer())
	}
}

func TestCanonicalIgnoresSetOrderAndHeaderCase(t *testing.T) {
	left := validWireSpec(t)
	right := validWireSpec(t)
	reverseSources(right.Sources)
	reverseSchemas(right.Schemas)
	reverseFields(right.Schemas)
	reverseBindings(right.Operations[0].RequestBindings)
	right.Operations[0].RequestBindings[2].Name = "x-request-id"
	right.Operations[0].Auth.Credentials[0].Name = "authorization"
	leftManifest, err := api.NewManifest(left)
	if err != nil {
		t.Fatal(err)
	}
	rightManifest, err := api.NewManifest(right)
	if err != nil {
		t.Fatal(err)
	}
	leftJSON, _ := leftManifest.CanonicalJSON()
	rightJSON, _ := rightManifest.CanonicalJSON()
	if string(leftJSON) != string(rightJSON) {
		t.Fatalf("canonical differs:\nleft: %s\nright: %s", leftJSON, rightJSON)
	}
}

func validWireSpec(t *testing.T) api.ManifestSpec {
	t.Helper()
	requestRef := mustRef(t, "repo:desc/core.api#SampleRequest")
	payloadRef := mustRef(t, "repo:desc/core.api#Payload")
	tagsRef := mustRef(t, "repo:desc/core.api#Tags")
	responseRef := mustRef(t, "repo:desc/core.api#SampleResponse")
	operationRef := mustRef(t, "repo:desc/core.api#CreateSample")
	return api.ManifestSpec{
		Sources: nodeSources(requestRef, payloadRef, tagsRef, responseRef, operationRef),
		Schemas: []api.SchemaSpec{
			{ID: "sample.request", Kind: api.SchemaObject, Provenance: canonicalNode(requestRef), Fields: []api.FieldSpec{{Name: "id", SchemaRef: "scalar.string", Required: true, Provenance: *canonicalNode(requestRef)}, {Name: "keyword", SchemaRef: "scalar.string", Provenance: *canonicalNode(requestRef)}, {Name: "requestID", SchemaRef: "scalar.string", Provenance: *canonicalNode(requestRef)}, {Name: "payload", SchemaRef: "sample.payload", Provenance: *canonicalNode(requestRef)}, {Name: "tags", SchemaRef: "sample.tags", Provenance: *canonicalNode(requestRef)}}},
			{ID: "sample.payload", Kind: api.SchemaObject, Provenance: canonicalNode(payloadRef), Fields: []api.FieldSpec{{Name: "name", SchemaRef: "scalar.string", Required: true, Provenance: *canonicalNode(payloadRef)}}},
			{ID: "sample.tags", Kind: api.SchemaArray, Provenance: canonicalNode(tagsRef), ItemSchemaRef: "scalar.string"},
			{ID: "sample.response", Kind: api.SchemaObject, Provenance: canonicalNode(responseRef), Fields: []api.FieldSpec{{Name: "id", SchemaRef: "scalar.string", Required: true, Provenance: *canonicalNode(responseRef)}}},
			{ID: "scalar.string", Kind: api.SchemaString},
		},
		Operations: []api.OperationSpec{{
			ID: "sample.create", Method: api.MethodPOST, Path: "/samples/{id}", Provenance: *canonicalNode(operationRef),
			RequestSchemaRef: "sample.request", ResponseBody: api.ResponseBodyJSON, ResponseSchemaRef: "sample.response",
			RequestBindings: []api.RequestBindingSpec{{Field: "id", Location: api.RequestBindingPath, Name: "id"}, {Field: "keyword", Location: api.RequestBindingQuery, Name: "keyword"}, {Field: "requestID", Location: api.RequestBindingHeader, Name: "X-Request-ID"}, {Field: "payload", Location: api.RequestBindingBody, Name: "payload"}, {Field: "tags", Location: api.RequestBindingBody, Name: "tags"}},
			Auth:            api.AuthSpec{Mode: api.AuthRequired, Credentials: []api.CredentialSpec{{ID: "primary", Type: api.CredentialBearer, Location: api.CredentialLocationHeader, Name: "Authorization"}}},
			Permission:      "sample.write", Capability: &api.CapabilitySpec{ID: "nexa.dev/sample-api", APIVersion: "nexa.dev/sample-api/v1"},
			ErrorProjections: []api.ErrorProjectionSpec{{Match: api.ErrorMatchSpec{Domain: "sample", Code: "not_found"}, Project: api.ErrorTargetSpec{Domain: "api", Code: "sample_not_found", HTTPStatus: 404}}},
		}},
	}
}

func cloneOperationSpec(input api.OperationSpec) api.OperationSpec {
	input.RequestBindings = append([]api.RequestBindingSpec(nil), input.RequestBindings...)
	input.Auth.Credentials = append([]api.CredentialSpec(nil), input.Auth.Credentials...)
	input.ErrorProjections = append([]api.ErrorProjectionSpec(nil), input.ErrorProjections...)
	if input.Capability != nil {
		capability := *input.Capability
		input.Capability = &capability
	}
	return input
}

func reverseSources(input []provenance.Source) {
	for left, right := 0, len(input)-1; left < right; left, right = left+1, right-1 {
		input[left], input[right] = input[right], input[left]
	}
}
func reverseSchemas(input []api.SchemaSpec) {
	for left, right := 0, len(input)-1; left < right; left, right = left+1, right-1 {
		input[left], input[right] = input[right], input[left]
	}
}
func reverseFields(input []api.SchemaSpec) {
	for index := range input {
		fields := input[index].Fields
		for left, right := 0, len(fields)-1; left < right; left, right = left+1, right-1 {
			fields[left], fields[right] = fields[right], fields[left]
		}
	}
}
func reverseBindings(input []api.RequestBindingSpec) {
	for left, right := 0, len(input)-1; left < right; left, right = left+1, right-1 {
		input[left], input[right] = input[right], input[left]
	}
}

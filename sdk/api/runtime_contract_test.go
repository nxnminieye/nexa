package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/cli/protocol"
	generationapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

func TestRuntimeContractBuildsCanonicalIndexedProjection(t *testing.T) {
	manifest := runtimeContractManifest(t)
	manifestJSON, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	contract, err := BuildRuntimeContract(manifest)
	if err != nil {
		t.Fatalf("BuildRuntimeContract() error = %v", err)
	}
	got, err := contract.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	want := fmt.Sprintf(
		`{"apiVersion":"nexa.dev/runtime-contract/v1","operations":{"sample.get":{"auth":{"credentials":{"access":{"in":"header","name":"authorization","type":"bearer"}},"mode":"required"},"capability":{"apiVersion":"sample/api/v1","id":"sample/api"},"errorProjections":{"sample":{"not_found":{"code":"not_found","domain":"nexa.sample","httpStatus":404}}},"method":"GET","pathSegments":[{"literal":"/items/"},{"field":"id"}],"permission":"sample.read","request":{"bindings":{"id":{"in":"path","name":"id"}},"schema":1},"response":{"body":"json","schema":3}}},"schemas":[{"kind":"integer"},{"fields":{"id":{"required":true,"schema":0}},"kind":"object"},{"kind":"string"},{"fields":{"name":{"required":true,"schema":2}},"kind":"object"}],"trace":{"apiManifestCanonicalDigest":%q,"apiManifestVersion":"nexa.dev/api-manifest/v1","sourceDigest":%q}}`,
		provenance.SHA256(manifestJSON).String(), manifest.SourceDigest().String(),
	)
	if string(got) != want {
		t.Fatalf("CanonicalJSON()\n got: %s\nwant: %s", got, want)
	}
	if canonical, err := jcs.Transform(got); err != nil || !bytes.Equal(canonical, got) {
		t.Fatalf("contract is not exact JCS: canonical=%s err=%v", canonical, err)
	}
	trace := contract.Trace()
	if trace.APIManifestVersion() != generationapi.APIVersion ||
		trace.APIManifestCanonicalDigest() != provenance.SHA256(manifestJSON) ||
		trace.SourceDigest() != manifest.SourceDigest() {
		t.Fatalf("Trace() = (%q,%q,%q)", trace.APIManifestVersion(), trace.APIManifestCanonicalDigest(), trace.SourceDigest())
	}

	var raw map[string]any
	if err := json.Unmarshal(got, &raw); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"sources", "sourceRef", "schemaRef", "provenance", "origin"} {
		if bytes.Contains(got, []byte(`"`+forbidden+`"`)) {
			t.Fatalf("authoring fact %q survived in runtime projection", forbidden)
		}
	}
	got[0] = '['
	again, err := contract.CanonicalJSON()
	if err != nil || len(again) == 0 || again[0] != '{' {
		t.Fatalf("contract byte mutation escaped: %s, %v", again, err)
	}
}

func TestRuntimeContractZeroAndEmptyManifest(t *testing.T) {
	_, err := BuildRuntimeContract(generationapi.Manifest{})
	requireRuntimeContractError(t, err, codeAPIManifestRequired, "API manifest is required", "manifest_required", "/manifest")

	empty, err := generationapi.NewManifest(generationapi.ManifestSpec{})
	if err != nil {
		t.Fatal(err)
	}
	contract, err := BuildRuntimeContract(empty)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := contract.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"schemas":[]`)) || !bytes.Contains(encoded, []byte(`"operations":{}`)) {
		t.Fatalf("empty contract = %s", encoded)
	}
	if _, err := (RuntimeContract{}).CanonicalJSON(); err == nil {
		t.Fatal("zero RuntimeContract CanonicalJSON succeeded")
	}
}

func TestRuntimeContractBuildsDepth24AndSharedDAGLinearly(t *testing.T) {
	depthManifest := runtimeContractDAGManifest(t, 24, 1)
	contract, err := BuildRuntimeContract(depthManifest)
	if err != nil {
		t.Fatalf("BuildRuntimeContract(depth24) error = %v", err)
	}
	encoded, err := contract.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Schemas []json.RawMessage `json:"schemas"`
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if got, want := len(document.Schemas), 25; got != want {
		t.Fatalf("unique schema rows = %d, want %d", got, want)
	}

	manifest := runtimeContractDAGManifest(t, 10, 4)
	contract, err = BuildRuntimeContract(manifest)
	if err != nil {
		t.Fatalf("BuildRuntimeContract(shared DAG) error = %v", err)
	}
	encoded, err = contract.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if got, want := len(document.Schemas), 11; got != want {
		t.Fatalf("shared DAG unique schema rows = %d, want %d", got, want)
	}
	if len(encoded) > 32<<10 {
		t.Fatalf("indexed shared DAG expanded unexpectedly: %d bytes", len(encoded))
	}

	second, err := BuildRuntimeContract(manifest)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := second.CanonicalJSON()
	if err != nil || !bytes.Equal(encoded, secondJSON) {
		t.Fatalf("build order is unstable: equal=%t err=%v", bytes.Equal(encoded, secondJSON), err)
	}
}

func TestRuntimeContractStructurallyDeduplicatesAndIgnoresAccessorAliases(t *testing.T) {
	manifest := runtimeContractDedupManifest(t)
	contract, err := BuildRuntimeContract(manifest)
	if err != nil {
		t.Fatal(err)
	}
	before, err := contract.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Schemas    []json.RawMessage `json:"schemas"`
		Operations map[string]struct {
			Request struct {
				Schema int `json:"schema"`
			} `json:"request"`
			Response struct {
				Schema int `json:"schema"`
			} `json:"response"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(before, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Schemas) != 1 || document.Operations["sample.call"].Request.Schema != 0 || document.Operations["sample.call"].Response.Schema != 0 {
		t.Fatalf("structural dedup projection = schemas %d request %d response %d",
			len(document.Schemas), document.Operations["sample.call"].Request.Schema, document.Operations["sample.call"].Response.Schema)
	}

	sources := manifest.Sources()
	sources[0] = provenance.Source{}
	schemas := manifest.Schemas()
	schemas[0] = generationapi.Schema{}
	operations := manifest.Operations()
	operations[0] = generationapi.Operation{}
	after, err := contract.CanonicalJSON()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("Manifest accessor alias changed contract: equal=%t err=%v", bytes.Equal(before, after), err)
	}
}

func TestRuntimeContractTraceIsNonAuthoringBreadcrumb(t *testing.T) {
	original := mustRuntimeContractJSON(t, runtimeContractManifest(t))
	changed := mutateRuntimeContract(t, original, func(root map[string]any) {
		trace := root["trace"].(map[string]any)
		trace["apiManifestCanonicalDigest"] = provenance.SHA256([]byte("other manifest")).String()
		trace["sourceDigest"] = provenance.SHA256([]byte("other sources")).String()
	})
	parsed, err := ParseRuntimeContract(changed)
	if err != nil {
		t.Fatalf("trace-only change was treated as authoring validation: %v", err)
	}
	encoded, err := parsed.CanonicalJSON()
	if err != nil || !bytes.Equal(encoded, changed) {
		t.Fatalf("trace-only contract did not round trip: equal=%t err=%v", bytes.Equal(encoded, changed), err)
	}
	if parsed.Trace().APIManifestCanonicalDigest() != provenance.SHA256([]byte("other manifest")) ||
		parsed.Trace().SourceDigest() != provenance.SHA256([]byte("other sources")) {
		t.Fatal("trace accessors recomputed authoring digests")
	}
}

func TestRuntimeContractParseStrictSchemaCanonicalAndRelations(t *testing.T) {
	valid, err := BuildRuntimeContract(runtimeContractManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := valid.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRuntimeContract(canonical)
	if err != nil {
		t.Fatalf("ParseRuntimeContract(valid) error = %v", err)
	}
	parsedJSON, err := parsed.CanonicalJSON()
	if err != nil || !bytes.Equal(parsedJSON, canonical) {
		t.Fatalf("parse changed canonical contract: equal=%t err=%v", bytes.Equal(parsedJSON, canonical), err)
	}

	withWhitespace := append([]byte(" "), canonical...)
	requireRuntimeContractError(t, mustRuntimeContractParseError(withWhitespace), codeRuntimeContractInvalid,
		"runtime contract is invalid", "runtime_contract_noncanonical", "/runtimeContract")

	unknown := mutateRuntimeContract(t, canonical, func(root map[string]any) { root["unknown"] = true })
	requireRuntimeContractError(t, mustRuntimeContractParseError(unknown), codeRuntimeContractInvalid,
		"runtime contract is invalid", "runtime_contract_schema_invalid", "/runtimeContract")

	duplicate := bytes.Replace(canonical, []byte(`"apiVersion":`), []byte(`"apiVersion":"nexa.dev/runtime-contract/v1","apiVersion":`), 1)
	requireRuntimeContractError(t, mustRuntimeContractParseError(duplicate), codeRuntimeContractInvalid,
		"runtime contract is invalid", "duplicate_key", "/runtimeContract")

	forward := mutateRuntimeContract(t, canonical, func(root map[string]any) {
		schemas := root["schemas"].([]any)
		fields := schemas[1].(map[string]any)["fields"].(map[string]any)
		fields["id"].(map[string]any)["schema"] = float64(1)
	})
	requireRuntimeContractError(t, mustRuntimeContractParseError(forward), codeRuntimeContractInvalid,
		"runtime contract is invalid", "runtime_schema_index_invalid", "/schemas/1/fields/id/schema")

	duplicateSchema := mutateRuntimeContract(t, canonical, func(root map[string]any) {
		schemas := root["schemas"].([]any)
		root["schemas"] = append(schemas, map[string]any{"kind": "integer"})
	})
	requireRuntimeContractError(t, mustRuntimeContractParseError(duplicateSchema), codeRuntimeContractInvalid,
		"runtime contract is invalid", "runtime_schema_duplicate", "/schemas/4")
}

func TestRuntimeContractLimitsAndSchemasAreDefensive(t *testing.T) {
	limits := RuntimeContractLimits()
	if limits != (RuntimeContractLimitSet{
		APIVersion: RuntimeContractLimitsAPIVersion,
		RawBytes:   16 << 20,
		JSONDepth:  16,
		JSONNodes:  262_144,
	}) {
		t.Fatalf("RuntimeContractLimits() = %#v", limits)
	}
	for name, get := range map[string]func() []byte{
		"runtime contract":        RuntimeContractSchema,
		"runtime contract limits": RuntimeContractLimitsSchema,
		"runtime limits":          RuntimeLimitsSchema,
		"remote error limits":     RemoteErrorLimitsSchema,
	} {
		t.Run(name, func(t *testing.T) {
			first := get()
			if !json.Valid(first) || len(first) == 0 {
				t.Fatalf("schema is invalid: %q", first)
			}
			first[0] = '['
			if second := get(); len(second) == 0 || second[0] != '{' {
				t.Fatal("schema mutation escaped")
			}
		})
	}

	rawOver := bytes.Repeat([]byte{' '}, limits.RawBytes+1)
	requireRuntimeContractError(t, mustRuntimeContractParseError(rawOver), codeRuntimeContractInvalid,
		"runtime contract is invalid", "size_limit_exceeded", "/runtimeContract")
}

func TestRuntimeContractNewAndNewRuntimeShareExecution(t *testing.T) {
	manifest := runtimeContractManifest(t)
	contract, err := BuildRuntimeContract(manifest)
	if err != nil {
		t.Fatal(err)
	}
	request, err := ParseRequest([]byte(`{"id":7}`))
	if err != nil {
		t.Fatal(err)
	}
	endpoint := &url.URL{Scheme: "https", Host: "api.example.test", Path: "/v1"}
	provider := NewStaticCredentialProvider([]CredentialValue{{ID: "access", Value: "secret"}})

	type observed struct {
		method  generationapi.HTTPMethod
		url     string
		headers []Header
		body    []byte
	}
	var calls []observed
	transport := TransportFunc(func(_ context.Context, request WireRequest) (WireResponse, error) {
		calls = append(calls, observed{request.Method(), request.URL().String(), request.Headers(), request.Body()})
		return NewWireResponse(200, []Header{{Name: "content-type", Value: "application/json"}}, io.NopCloser(strings.NewReader(`{"name":"sample"}`)))
	})
	manifestClient, err := New(Options{Manifest: manifest, Endpoint: endpoint, Transport: transport,
		CredentialProvider: provider, MaxResponseBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	runtimeClient, err := NewRuntime(RuntimeOptions{RuntimeContract: contract, Endpoint: endpoint, Transport: transport,
		CredentialProvider: provider, MaxResponseBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range []*Client{manifestClient, runtimeClient} {
		result, err := client.Call(context.Background(), "sample.get", request)
		if err != nil {
			t.Fatalf("Call() error = %v", err)
		}
		if body, ok := result.JSON(); !ok || string(body) != `{"name":"sample"}` {
			t.Fatalf("Result.JSON() = %s,%t", body, ok)
		}
	}
	if len(calls) != 2 || !reflect.DeepEqual(calls[0], calls[1]) {
		t.Fatalf("executors diverged: %#v", calls)
	}
}

func TestRuntimeContractAlternativeCookieCredentialsRemainSelectable(t *testing.T) {
	manifest := runtimeContractAlternativeCookieManifest(t)
	contract, err := BuildRuntimeContract(manifest)
	if err != nil {
		t.Fatalf("BuildRuntimeContract() error = %v", err)
	}
	canonical, err := contract.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRuntimeContract(canonical)
	if err != nil {
		t.Fatalf("ParseRuntimeContract() error = %v", err)
	}

	request, err := ParseRequest([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	endpoint := &url.URL{Scheme: "https", Host: "api.example.test"}
	var cookies []string
	transport := TransportFunc(func(_ context.Context, request WireRequest) (WireResponse, error) {
		cookie := ""
		for _, header := range request.Headers() {
			if header.Name == "cookie" {
				if cookie != "" {
					t.Fatal("request contained duplicate cookie headers")
				}
				cookie = header.Value
			}
		}
		cookies = append(cookies, cookie)
		return NewWireResponse(204, nil, io.NopCloser(strings.NewReader("")))
	})
	manifestClient, err := New(Options{Manifest: manifest, Endpoint: endpoint, Transport: transport, MaxResponseBytes: 1024})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runtimeClient, err := NewRuntime(RuntimeOptions{RuntimeContract: parsed, Endpoint: endpoint, Transport: transport, MaxResponseBytes: 1024})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	clients := []*Client{manifestClient, runtimeClient}
	credentials := []CredentialValue{{ID: "first", Value: "one"}, {ID: "second", Value: "two"}}
	for _, client := range clients {
		for _, credential := range credentials {
			_, err := client.Call(context.Background(), "sample.call", request,
				WithCredentialProvider(NewStaticCredentialProvider([]CredentialValue{credential})))
			if err != nil {
				t.Fatalf("Call(%s) error = %v", credential.ID, err)
			}
		}
	}
	if want := []string{"first_session=one", "second_session=two", "first_session=one", "second_session=two"}; !reflect.DeepEqual(cookies, want) {
		t.Fatalf("cookie selection = %#v, want %#v", cookies, want)
	}
}

func TestRuntimeContractNewRuntimeRequiresContractBeforeClientOptions(t *testing.T) {
	_, err := NewRuntime(RuntimeOptions{})
	requireRuntimeContractError(t, err, codeRuntimeContractInvalid, "runtime contract is invalid",
		"runtime_contract_schema_invalid", "/runtimeContract")
}

func runtimeContractManifest(t *testing.T) generationapi.Manifest {
	t.Helper()
	requestRef := runtimeContractRef(t, "repo:sdk/api/testdata/runtime.api#SampleRequest")
	responseRef := runtimeContractRef(t, "repo:sdk/api/testdata/runtime.api#SampleResponse")
	operationRef := runtimeContractRef(t, "repo:sdk/api/testdata/runtime.api#SampleGet")
	stringFieldRef := runtimeContractRef(t, "repo:sdk/api/testdata/runtime.api#SampleResponse.name")
	sources := []provenance.Source{
		{Ref: requestRef, Digest: provenance.SHA256([]byte("request"))},
		{Ref: responseRef, Digest: provenance.SHA256([]byte("response"))},
		{Ref: operationRef, Digest: provenance.SHA256([]byte("operation"))},
		{Ref: stringFieldRef, Digest: provenance.SHA256([]byte("field"))},
	}
	manifest, err := generationapi.NewManifest(generationapi.ManifestSpec{
		Sources: sources,
		Schemas: []generationapi.SchemaSpec{
			{ID: "sample.response", Kind: generationapi.SchemaObject, Provenance: runtimeContractProvenance(responseRef), Fields: []generationapi.FieldSpec{{
				Name: "name", SchemaRef: "scalar.string", Required: true,
				Provenance: generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{stringFieldRef}},
			}}},
			{ID: "scalar.string", Kind: generationapi.SchemaString},
			{ID: "sample.request", Kind: generationapi.SchemaObject, Provenance: runtimeContractProvenance(requestRef), Fields: []generationapi.FieldSpec{{
				Name: "id", SchemaRef: "scalar.integer", Required: true,
				Provenance: generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{requestRef}},
			}}},
			{ID: "scalar.integer", Kind: generationapi.SchemaInteger},
		},
		Operations: []generationapi.OperationSpec{{
			ID: "sample.get", Method: generationapi.MethodGET, Path: "/items/{id}",
			Provenance:       generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{operationRef}},
			RequestSchemaRef: "sample.request", ResponseBody: generationapi.ResponseBodyJSON, ResponseSchemaRef: "sample.response",
			RequestBindings: []generationapi.RequestBindingSpec{{Field: "id", Location: generationapi.RequestBindingPath, Name: "id"}},
			Auth: generationapi.AuthSpec{Mode: generationapi.AuthRequired, Credentials: []generationapi.CredentialSpec{{
				ID: "access", Type: generationapi.CredentialBearer, Location: generationapi.CredentialLocationHeader, Name: "Authorization",
			}}},
			Permission: "sample.read", Capability: &generationapi.CapabilitySpec{ID: "sample/api", APIVersion: "sample/api/v1"},
			ErrorProjections: []generationapi.ErrorProjectionSpec{{
				Match:   generationapi.ErrorMatchSpec{Domain: "sample", Code: "not_found"},
				Project: generationapi.ErrorTargetSpec{Domain: "nexa.sample", Code: "not_found", HTTPStatus: 404},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	return manifest
}

func runtimeContractDAGManifest(t *testing.T, depth, fanout int) generationapi.Manifest {
	t.Helper()
	ref := runtimeContractRef(t, "repo:sdk/api/testdata/runtime.api#SharedDAG")
	schemas := []generationapi.SchemaSpec{{ID: "scalar.string", Kind: generationapi.SchemaString}}
	child := "scalar.string"
	for level := depth - 1; level >= 0; level-- {
		id := fmt.Sprintf("sample.level.%d", level)
		fields := make([]generationapi.FieldSpec, fanout)
		for field := range fields {
			fields[field] = generationapi.FieldSpec{
				Name: fmt.Sprintf("f%d", field), SchemaRef: child, Required: true,
				Provenance: generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}},
			}
		}
		schemas = append(schemas, generationapi.SchemaSpec{ID: id, Kind: generationapi.SchemaObject, Provenance: runtimeContractProvenance(ref), Fields: fields})
		child = id
	}
	request, response := "sample.level.0", "sample.level.0"
	bindings := make([]generationapi.RequestBindingSpec, fanout)
	for index := range bindings {
		bindings[index] = generationapi.RequestBindingSpec{Field: fmt.Sprintf("f%d", index), Location: generationapi.RequestBindingBody, Name: fmt.Sprintf("f%d", index)}
	}
	manifest, err := generationapi.NewManifest(generationapi.ManifestSpec{
		Sources: []provenance.Source{{Ref: ref, Digest: provenance.SHA256([]byte("shared DAG"))}},
		Schemas: schemas,
		Operations: []generationapi.OperationSpec{{
			ID: "sample.call", Method: generationapi.MethodPOST, Path: "/sample",
			Provenance:       generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}},
			RequestSchemaRef: request, ResponseBody: generationapi.ResponseBodyJSON, ResponseSchemaRef: response,
			RequestBindings: bindings, Auth: generationapi.AuthSpec{Mode: generationapi.AuthNone}, ErrorProjections: []generationapi.ErrorProjectionSpec{},
		}},
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	return manifest
}

func runtimeContractDedupManifest(t *testing.T) generationapi.Manifest {
	t.Helper()
	ref := runtimeContractRef(t, "repo:sdk/api/testdata/runtime.api#Dedup")
	manifest, err := generationapi.NewManifest(generationapi.ManifestSpec{
		Sources: []provenance.Source{{Ref: ref, Digest: provenance.SHA256([]byte("dedup"))}},
		Schemas: []generationapi.SchemaSpec{
			{ID: "sample.request", Kind: generationapi.SchemaObject, Provenance: runtimeContractProvenance(ref), Fields: []generationapi.FieldSpec{}},
			{ID: "sample.response", Kind: generationapi.SchemaObject, Provenance: runtimeContractProvenance(ref), Fields: []generationapi.FieldSpec{}},
		},
		Operations: []generationapi.OperationSpec{{
			ID: "sample.call", Method: generationapi.MethodGET, Path: "/sample",
			Provenance:       generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}},
			RequestSchemaRef: "sample.request", ResponseBody: generationapi.ResponseBodyJSON, ResponseSchemaRef: "sample.response",
			RequestBindings: []generationapi.RequestBindingSpec{}, Auth: generationapi.AuthSpec{Mode: generationapi.AuthNone},
			ErrorProjections: []generationapi.ErrorProjectionSpec{},
		}},
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	return manifest
}

func runtimeContractAlternativeCookieManifest(t *testing.T) generationapi.Manifest {
	t.Helper()
	ref := runtimeContractRef(t, "repo:sdk/api/testdata/runtime.api#AlternativeCookies")
	manifest, err := generationapi.NewManifest(generationapi.ManifestSpec{
		Sources: []provenance.Source{{Ref: ref, Digest: provenance.SHA256([]byte("alternative cookies"))}},
		Schemas: []generationapi.SchemaSpec{{
			ID: "sample.request", Kind: generationapi.SchemaObject,
			Provenance: runtimeContractProvenance(ref), Fields: []generationapi.FieldSpec{},
		}},
		Operations: []generationapi.OperationSpec{{
			ID: "sample.call", Method: generationapi.MethodGET, Path: "/sample",
			Provenance:       generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}},
			RequestSchemaRef: "sample.request", ResponseBody: generationapi.ResponseBodyNone,
			RequestBindings: []generationapi.RequestBindingSpec{},
			Auth: generationapi.AuthSpec{Mode: generationapi.AuthRequired, Credentials: []generationapi.CredentialSpec{
				{ID: "first", Type: generationapi.CredentialSessionCookie, Location: generationapi.CredentialLocationCookie, Name: "first_session"},
				{ID: "second", Type: generationapi.CredentialSessionCookie, Location: generationapi.CredentialLocationCookie, Name: "second_session"},
			}},
			ErrorProjections: []generationapi.ErrorProjectionSpec{},
		}},
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	return manifest
}

func runtimeContractRef(t *testing.T, value string) provenance.SourceRef {
	t.Helper()
	ref, err := provenance.ParseSourceRef(value)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func runtimeContractProvenance(ref provenance.SourceRef) *generationapi.NodeProvenanceSpec {
	return &generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}}
}

func mutateRuntimeContract(t *testing.T, data []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		t.Fatal(err)
	}
	// Normal JSON numbers in these fixtures are small exact integers. Convert to
	// float-compatible values only at mutation sites and canonicalize afterward.
	mutate(root)
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

func mustRuntimeContractParseError(data []byte) error {
	_, err := ParseRuntimeContract(data)
	return err
}

func requireRuntimeContractError(t *testing.T, err error, code, message, reason, pointer string) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("operation succeeded, want %s/%s", code, reason)
	}
	var apiError *Error
	if !errors.As(err, &apiError) || apiError == nil {
		t.Fatalf("error = %T %v, want *api.Error", err, err)
	}
	if apiError.Code() != code || apiError.Domain() != sdkErrorDomain || apiError.Category() != protocol.CategoryInput || apiError.Retryable() {
		t.Fatalf("identity = (%q,%q,%q,%t)", apiError.Code(), apiError.Domain(), apiError.Category(), apiError.Retryable())
	}
	if apiError.Error() != message || apiError.Details().Reason() != reason || apiError.Details().Pointer() != pointer {
		t.Fatalf("payload = (%q,%q,%q), want (%q,%q,%q)", apiError.Error(), apiError.Details().Reason(), apiError.Details().Pointer(), message, reason, pointer)
	}
	return apiError
}

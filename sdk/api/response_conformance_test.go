package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	generationapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

type oneByteResponseReader struct {
	data   []byte
	offset int
}

func (r *oneByteResponseReader) Read(target []byte) (int, error) {
	if r.offset == len(r.data) {
		return 0, io.EOF
	}
	target[0] = r.data[r.offset]
	r.offset++
	return 1, nil
}

func TestResponseConformanceRuntimeJSONSemantics(t *testing.T) {
	limits := RuntimeLimits()
	if limits.JSONDepth != 64 || limits.JSONNodes != 65_536 {
		t.Fatalf("JSON limits = depth %d nodes %d", limits.JSONDepth, limits.JSONNodes)
	}
	semantics := limits.JSONSemantics()
	if semantics.RootDepth() != 0 || !semantics.Inclusive() || !semantics.CountsRoot() ||
		!semantics.CountsValues() || semantics.CountsMemberNames() {
		t.Fatalf("JSON semantics = root %d inclusive %t root %t values %t memberNames %t",
			semantics.RootDepth(), semantics.Inclusive(), semantics.CountsRoot(), semantics.CountsValues(), semantics.CountsMemberNames())
	}
	wantScopes := []JSONParserScope{ScopeParseRequest, ScopeParseRemoteError, ScopeClientSuccessResponse}
	if got := semantics.Scopes(); !reflect.DeepEqual(got, wantScopes) {
		t.Fatalf("Scopes() = %#v, want %#v", got, wantScopes)
	}
	mutated := semantics.Scopes()
	mutated[0] = "mutated"
	if got := semantics.Scopes(); !reflect.DeepEqual(got, wantScopes) {
		t.Fatalf("scope mutation escaped: %#v", got)
	}
	if got := RuntimeLimits().JSONSemantics().Scopes(); !reflect.DeepEqual(got, wantScopes) {
		t.Fatalf("RuntimeLimits semantics mutation escaped: %#v", got)
	}
	for _, vector := range []struct {
		name   string
		limits RuntimeLimitSet
	}{
		{name: "zero", limits: RuntimeLimitSet{}},
		{name: "foreign version", limits: RuntimeLimitSet{APIVersion: "nexa.dev/runtime-api-limits/v999"}},
	} {
		t.Run(vector.name, func(t *testing.T) {
			got := vector.limits.JSONSemantics()
			if got.RootDepth() != 0 || got.Inclusive() || got.CountsRoot() || got.CountsValues() || got.CountsMemberNames() || got.Scopes() != nil {
				t.Fatalf("non-owner JSON semantics = root %d inclusive %t root %t values %t memberNames %t scopes %#v",
					got.RootDepth(), got.Inclusive(), got.CountsRoot(), got.CountsValues(), got.CountsMemberNames(), got.Scopes())
			}
		})
	}
}

func TestResponseOneByteReaderAllocationBound(t *testing.T) {
	payload := []byte(strings.Repeat("x", 4096))
	var (
		got    []byte
		runErr error
	)
	allocations := testing.AllocsPerRun(5, func() {
		got, runErr = readBoundedResponse(
			context.Background(),
			&oneByteResponseReader{data: payload},
			int64(len(payload)),
			"sample.call",
			200,
		)
	})
	if runErr != nil {
		t.Fatalf("readBoundedResponse() error = %v", runErr)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("readBoundedResponse() bytes = %d, want %d", len(got), len(payload))
	}
	if allocations > 128 {
		t.Fatalf("one-byte response allocations = %.0f, want <= 128", allocations)
	}
}

func TestResponseConformanceContentTypeAndCanonicalResult(t *testing.T) {
	manifest := responseTestManifest(t, generationapi.ResponseBodyJSON)
	valid := []string{
		"application/json",
		"Application/JSON",
		"application/vnd.sample+json",
		"APPLICATION/VND.SAMPLE+JSON; CHARSET=UTF-8",
		"application/json; charset=utf-8",
	}
	for _, contentType := range valid {
		t.Run(contentType, func(t *testing.T) {
			body := newTrackedResponseBody(` { "displayName" : "Sample" } `)
			result, err := callResponse(t, manifest, 200, []Header{{Name: "content-type", Value: contentType}}, body)
			if err != nil {
				t.Fatalf("Call() error = %v", err)
			}
			if result.APIOperationID() != "sample.call" || result.HTTPStatus() != 200 || result.ResponseBody() != generationapi.ResponseBodyJSON {
				t.Fatalf("Result = (%q,%d,%q)", result.APIOperationID(), result.HTTPStatus(), result.ResponseBody())
			}
			first, ok := result.JSON()
			if !ok || string(first) != `{"displayName":"Sample"}` {
				t.Fatalf("Result.JSON() = %s, %t", first, ok)
			}
			first[0] = '['
			second, ok := result.JSON()
			if !ok || string(second) != `{"displayName":"Sample"}` {
				t.Fatalf("Result JSON mutation escaped: %s, %t", second, ok)
			}
			if body.closeCalls.Load() != 1 {
				t.Fatalf("Close calls = %d", body.closeCalls.Load())
			}
		})
	}

	invalid := []struct {
		name    string
		headers []Header
		reason  string
	}{
		{name: "missing", reason: "response_content_type_missing"},
		{name: "duplicate", headers: []Header{{Name: "content-type", Value: "application/json"}, {Name: "content-type", Value: "application/json"}}, reason: "response_content_type_duplicate"},
		{name: "malformed", headers: []Header{{Name: "content-type", Value: "application/json; charset"}}, reason: "response_content_type_malformed"},
		{name: "duplicate parameter", headers: []Header{{Name: "content-type", Value: "application/json; charset=utf-8; charset=utf-8"}}, reason: "response_content_type_malformed"},
		{name: "unsupported", headers: []Header{{Name: "content-type", Value: "text/json"}}, reason: "response_content_type_unsupported"},
		{name: "empty vendor prefix", headers: []Header{{Name: "content-type", Value: "application/+json"}}, reason: "response_content_type_unsupported"},
		{name: "wrong charset", headers: []Header{{Name: "content-type", Value: "application/json; charset=latin1"}}, reason: "response_content_type_parameter_invalid"},
		{name: "extended charset name", headers: []Header{{Name: "content-type", Value: "application/json; charset*=utf-8''utf-8"}}, reason: "response_content_type_parameter_invalid"},
		{name: "other parameter", headers: []Header{{Name: "content-type", Value: "application/json; profile=sample"}}, reason: "response_content_type_parameter_invalid"},
		{name: "multiple parameters", headers: []Header{{Name: "content-type", Value: "application/json; charset=utf-8; profile=sample"}}, reason: "response_content_type_parameter_invalid"},
	}
	for _, vector := range invalid {
		t.Run(vector.name, func(t *testing.T) {
			body := newTrackedResponseBody(`{"displayName":"Sample"}`)
			_, err := callResponse(t, manifest, 200, vector.headers, body)
			requireResponseFailureWithCode(t, err, codeRemoteProtocolError, "API response is invalid", vector.reason, "/headers/content-type", 200, "sample.call")
			if body.closeCalls.Load() != 1 {
				t.Fatalf("Close calls = %d", body.closeCalls.Load())
			}
		})
	}
}

func TestResponseConformanceCanonicalizesSuccessNumbers(t *testing.T) {
	manifest := customResponseManifest(t, "scalar.number", []generationapi.SchemaSpec{{ID: "scalar.number", Kind: generationapi.SchemaNumber}})
	result, err := callResponse(t, manifest, 200, []Header{{Name: "content-type", Value: "application/json"}}, newTrackedResponseBody(`1e-6`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, ok := result.JSON()
	if !ok || string(encoded) != `0.000001` {
		t.Fatalf("Result.JSON() = %s, %t", encoded, ok)
	}
}

func TestResponseConformanceBoundedBodyAndSchema(t *testing.T) {
	manifest := responseTestManifest(t, generationapi.ResponseBodyJSON)
	validBody := `{"displayName":"Sample"}`
	for _, maxBytes := range []int64{int64(len(validBody)), RuntimeLimits().ResponseBytesMax} {
		body := newTrackedResponseBody(validBody)
		result, err := callResponse(t, manifest, 200, []Header{{Name: "content-type", Value: "application/json"}}, body, WithMaxResponseBytes(maxBytes))
		if err != nil {
			t.Fatalf("exact/max response bound %d rejected: %v", maxBytes, err)
		}
		if encoded, ok := result.JSON(); !ok || string(encoded) != validBody {
			t.Fatalf("Result.JSON() = %s, %t", encoded, ok)
		}
	}

	body := newTrackedResponseBody(validBody)
	_, err := callResponse(t, manifest, 200, []Header{{Name: "content-type", Value: "application/json"}}, body, WithMaxResponseBytes(int64(len(validBody)-1)))
	requireResponseFailureWithCode(t, err, codeRemoteProtocolError, "API response is invalid", "response_body_too_large", "/body", 200, "sample.call")

	invalid := []struct {
		name   string
		body   string
		reason string
	}{
		{name: "empty", body: "", reason: "response_body_empty"},
		{name: "syntax", body: `{"displayName":`, reason: "response_document_invalid"},
		{name: "duplicate", body: `{"displayName":"a","displayName":"b"}`, reason: "response_duplicate_key"},
		{name: "trailing", body: `{"displayName":"a"} {}`, reason: "response_trailing_input"},
		{name: "missing field", body: `{}`, reason: "response_schema_invalid"},
		{name: "wrong type", body: `{"displayName":false}`, reason: "response_schema_invalid"},
		{name: "unknown field", body: `{"displayName":"a","secret":"must-not-project"}`, reason: "response_schema_invalid"},
		{name: "null", body: `{"displayName":null}`, reason: "response_schema_invalid"},
	}
	for _, vector := range invalid {
		t.Run(vector.name, func(t *testing.T) {
			candidate := newTrackedResponseBody(vector.body)
			_, err := callResponse(t, manifest, 204, []Header{{Name: "content-type", Value: "application/json"}}, candidate)
			apiError := requireResponseFailureWithCode(t, err, codeRemoteProtocolError, "API response is invalid", vector.reason, "/body", 204, "sample.call")
			assertSDKProjectionOmits(t, apiError, vector.body, "secret", "displayName")
			if candidate.closeCalls.Load() != 1 {
				t.Fatalf("Close calls = %d", candidate.closeCalls.Load())
			}
		})
	}
}

func TestResponseByteBoundarySemanticsPrecedeRemoteErrorParsing(t *testing.T) {
	limits := RuntimeLimits()
	semantics := limits.ResponseBytesSemantics()
	if semantics.Scope() != RuntimeBoundaryScopeClientCall || !semantics.BeforeRemoteErrorParse() {
		t.Fatalf("response-byte owner does not declare Client.Call precedence: %#v", semantics)
	}
	manifest := responseTestManifest(t, generationapi.ResponseBodyJSON)
	_, err := callResponse(
		t,
		manifest,
		500,
		[]Header{{Name: "content-type", Value: "application/json"}},
		newTrackedResponseBody("{{"),
		WithMaxResponseBytes(limits.ResponseBytesMin),
	)
	requireResponseFailureWithCode(t, err, codeRemoteProtocolError, "API response is invalid", "response_body_too_large", "/body", 500, "sample.call")
}

func TestResponseConformanceDoesNotReuseRequestRawByteLimit(t *testing.T) {
	bodyJSON := `{"displayName":"` + strings.Repeat("a", RuntimeLimits().RequestRawBytes) + `"}`
	result, err := callResponse(
		t,
		responseTestManifest(t, generationapi.ResponseBodyJSON),
		200,
		[]Header{{Name: "content-type", Value: "application/json"}},
		newTrackedResponseBody(bodyJSON),
		WithMaxResponseBytes(int64(len(bodyJSON))),
	)
	if err != nil {
		t.Fatalf("response above RequestRawBytes rejected: %v", err)
	}
	encoded, ok := result.JSON()
	if !ok || len(encoded) != len(bodyJSON) {
		t.Fatalf("Result.JSON() length = %d, %t, want %d", len(encoded), ok, len(bodyJSON))
	}
}

func TestResponseConformanceDepthAndNodeBoundaries(t *testing.T) {
	limits := RuntimeLimits()
	depthManifest := nestedArrayResponseManifest(t, limits.JSONDepth)
	exactDepth := strings.Repeat("[", limits.JSONDepth) + "true" + strings.Repeat("]", limits.JSONDepth)
	if _, err := callResponse(t, depthManifest, 200, []Header{{Name: "content-type", Value: "application/json"}}, newTrackedResponseBody(exactDepth)); err != nil {
		t.Fatalf("response at depth limit rejected: %v", err)
	}
	overDepth := "[" + exactDepth + "]"
	_, err := callResponse(t, depthManifest, 200, []Header{{Name: "content-type", Value: "application/json"}}, newTrackedResponseBody(overDepth))
	requireResponseFailureWithCode(t, err, codeRemoteProtocolError, "API response is invalid", "response_depth_limit_exceeded", "/body", 200, "sample.call")

	nodeManifest := arrayResponseManifest(t)
	exactNodes := "[" + strings.TrimSuffix(strings.Repeat("true,", limits.JSONNodes-1), ",") + "]"
	if _, err := callResponse(t, nodeManifest, 200, []Header{{Name: "content-type", Value: "application/json"}}, newTrackedResponseBody(exactNodes)); err != nil {
		t.Fatalf("response at node limit rejected: %v", err)
	}
	overNodes := "[" + strings.TrimSuffix(strings.Repeat("true,", limits.JSONNodes), ",") + "]"
	_, err = callResponse(t, nodeManifest, 200, []Header{{Name: "content-type", Value: "application/json"}}, newTrackedResponseBody(overNodes))
	requireResponseFailureWithCode(t, err, codeRemoteProtocolError, "API response is invalid", "response_node_limit_exceeded", "/body", 200, "sample.call")
}

type readCountingBody struct {
	reads      atomic.Int64
	closeCalls atomic.Int64
	reader     *strings.Reader
}

func (b *readCountingBody) Read(target []byte) (int, error) {
	b.reads.Add(1)
	return b.reader.Read(target)
}

func (b *readCountingBody) Close() error {
	b.closeCalls.Add(1)
	return nil
}

func TestResponseConformanceNoneDoesNotReadBody(t *testing.T) {
	body := &readCountingBody{reader: strings.NewReader("must-not-be-read")}
	result, err := callResponse(t, responseTestManifest(t, generationapi.ResponseBodyNone), 299, nil, body)
	if err != nil {
		t.Fatal(err)
	}
	if body.reads.Load() != 0 || body.closeCalls.Load() != 1 {
		t.Fatalf("none body calls = read %d close %d", body.reads.Load(), body.closeCalls.Load())
	}
	if result.ResponseBody() != generationapi.ResponseBodyNone {
		t.Fatalf("response mode = %q", result.ResponseBody())
	}
}

func TestResponseConformanceCorpus(t *testing.T) {
	corpus := loadRuntimeCorpusDocument(t)
	manifest, err := generationapi.Parse("runtime-api-v1.json#manifest", []byte(corpus.Manifest))
	if err != nil {
		t.Fatal(err)
	}
	canonicalManifest, err := manifest.CanonicalJSON()
	if err != nil || string(canonicalManifest) != corpus.Manifest {
		t.Fatalf("corpus Manifest is not the owner canonical projection: %v", err)
	}
	if len(corpus.Responses) == 0 {
		t.Fatal("response conformance corpus is empty")
	}
	seen := make(map[string]struct{}, len(corpus.Responses))
	seenReasons := make(map[string]struct{})
	seenMediaTypes := make(map[string]struct{})
	sawNone, sawDuplicateGeneral, sawLowerStatus, sawUpperStatus := false, false, false, false
	for _, vector := range corpus.Responses {
		t.Run(vector.Name, func(t *testing.T) {
			if _, duplicate := seen[vector.Name]; duplicate || vector.Name == "" {
				t.Fatalf("response vector name is empty or duplicate: %q", vector.Name)
			}
			seen[vector.Name] = struct{}{}
			if vector.Error != nil && vector.Error.Reason != "" {
				seenReasons[vector.Error.Reason] = struct{}{}
			}
			if vector.Status == 199 {
				sawLowerStatus = true
			}
			if vector.Status == 599 {
				sawUpperStatus = true
			}
			if vector.ResponseBody == generationapi.ResponseBodyNone && vector.Valid {
				sawNone = true
			}
			headerCounts := make(map[string]int)
			for _, header := range vector.Headers {
				headerCounts[header.Name]++
				if vector.Valid && header.Name == "content-type" {
					seenMediaTypes[header.Value] = struct{}{}
				}
			}
			for name, count := range headerCounts {
				if name != "content-type" && count > 1 && vector.Valid {
					sawDuplicateGeneral = true
				}
			}
			headers := runtimeAdapterHeadersToHeaders(vector.Headers)
			body := newTrackedResponseBody(vector.Body)
			response, constructErr := NewWireResponse(vector.Status, headers, body)
			if constructErr != nil {
				apiError := requireSDKError(t, constructErr)
				if vector.Valid || apiError.Code() != vector.Error.Code || apiError.Details().Reason() != vector.Error.Reason || apiError.Details().Pointer() != vector.Error.Pointer {
					t.Fatalf("constructor vector error = %s/%s %s", apiError.Code(), apiError.Details().Reason(), apiError.Details().Pointer())
				}
				if body.closeCalls.Load() != 0 {
					t.Fatalf("constructor failure closed caller body %d times", body.closeCalls.Load())
				}
				closeRecoverSafe(body)
				return
			}

			operationID := "sample.get"
			selectedManifest := manifest
			provider := NewStaticCredentialProvider([]CredentialValue{{ID: "primary", Value: "sample-token"}})
			requestJSON := `{"id":"sample-1"}`
			if vector.ResponseBody == generationapi.ResponseBodyNone {
				operationID = "sample.call"
				selectedManifest = responseTestManifest(t, generationapi.ResponseBodyNone)
				provider = nil
				requestJSON = `{}`
			}
			client, err := New(Options{
				Manifest:           selectedManifest,
				Endpoint:           &url.URL{Scheme: "https", Host: "api.example.test"},
				Transport:          TransportFunc(func(context.Context, WireRequest) (WireResponse, error) { return response, nil }),
				CredentialProvider: provider,
				MaxResponseBytes:   RuntimeLimits().ResponseBytesMax,
			})
			if err != nil {
				t.Fatal(err)
			}
			request, err := ParseRequest([]byte(requestJSON))
			if err != nil {
				t.Fatal(err)
			}
			var options []CallOption
			if vector.MaxResponseBytes != 0 {
				options = append(options, WithMaxResponseBytes(vector.MaxResponseBytes))
			}
			result, callErr := client.Call(context.Background(), operationID, request, options...)
			if vector.Valid {
				if callErr != nil {
					t.Fatalf("valid response vector failed: %v", callErr)
				}
				encoded, hasJSON := result.JSON()
				if vector.HasJSON == nil || hasJSON != *vector.HasJSON || string(encoded) != vector.Canonical {
					t.Fatalf("Result.JSON() = %s, %t, want %q, %v", encoded, hasJSON, vector.Canonical, vector.HasJSON)
				}
			} else {
				apiError := requireSDKError(t, callErr)
				if apiError.Code() != vector.Error.Code || apiError.Details().Reason() != vector.Error.Reason || apiError.Details().Pointer() != vector.Error.Pointer {
					t.Fatalf("response vector error = %s/%s %s", apiError.Code(), apiError.Details().Reason(), apiError.Details().Pointer())
				}
			}
			if body.closeCalls.Load() != 1 {
				t.Fatalf("response body closed %d times", body.closeCalls.Load())
			}
		})
	}
	for _, reason := range []string{
		"response_status_invalid",
		"response_content_type_missing",
		"response_content_type_duplicate",
		"response_content_type_malformed",
		"response_content_type_unsupported",
		"response_content_type_parameter_invalid",
		"response_body_empty",
		"response_body_too_large",
		"response_document_invalid",
		"response_duplicate_key",
		"response_trailing_input",
		"response_schema_invalid",
		"response_status_mismatch",
	} {
		if _, ok := seenReasons[reason]; !ok {
			t.Fatalf("response corpus does not execute %q", reason)
		}
	}
	for _, mediaType := range []string{"Application/JSON", "application/vnd.sample+json", "application/json; CHARSET=UTF-8"} {
		if _, ok := seenMediaTypes[mediaType]; !ok {
			t.Fatalf("response corpus does not execute media type %q", mediaType)
		}
	}
	if !sawNone || !sawDuplicateGeneral || !sawLowerStatus || !sawUpperStatus {
		t.Fatalf("response corpus boundary coverage = none:%t duplicate-general:%t lower:%t upper:%t", sawNone, sawDuplicateGeneral, sawLowerStatus, sawUpperStatus)
	}
	seenLimitKinds := make(map[string]int)
	for _, recipe := range corpus.ResponseLimitRecipes {
		t.Run(recipe.Name, func(t *testing.T) {
			var manifest generationapi.Manifest
			var body string
			switch recipe.Kind {
			case "nested-array-depth":
				manifest = nestedArrayResponseManifest(t, RuntimeLimits().JSONDepth)
				body = strings.Repeat("[", recipe.Amount) + "true" + strings.Repeat("]", recipe.Amount)
			case "array-value-count":
				manifest = arrayResponseManifest(t)
				body = "[" + strings.TrimSuffix(strings.Repeat("true,", recipe.Amount), ",") + "]"
			default:
				t.Fatalf("unknown response limit recipe kind %q", recipe.Kind)
			}
			seenLimitKinds[recipe.Kind]++
			_, err := callResponse(t, manifest, 200, []Header{{Name: "content-type", Value: "application/json"}}, newTrackedResponseBody(body))
			if recipe.Valid {
				if err != nil {
					t.Fatalf("valid response limit recipe failed: %v", err)
				}
				return
			}
			apiError := requireSDKError(t, err)
			if apiError.Code() != recipe.Error.Code || apiError.Details().Reason() != recipe.Error.Reason || apiError.Details().Pointer() != recipe.Error.Pointer {
				t.Fatalf("response limit recipe error = %s/%s %s", apiError.Code(), apiError.Details().Reason(), apiError.Details().Pointer())
			}
		})
	}
	if seenLimitKinds["nested-array-depth"] != 2 || seenLimitKinds["array-value-count"] != 2 {
		t.Fatalf("response limit recipe coverage = %#v", seenLimitKinds)
	}
}

func callResponse(t *testing.T, manifest generationapi.Manifest, status int, headers []Header, body io.ReadCloser, options ...CallOption) (Result, error) {
	t.Helper()
	response, err := NewWireResponse(status, headers, body)
	if err != nil {
		return Result{}, err
	}
	client := responseTestClient(t, manifest, TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
		return response, nil
	}), nil, RuntimeLimits().ResponseBytesMax)
	request, err := ParseRequest([]byte(`{}`))
	if err != nil {
		return Result{}, err
	}
	return client.Call(context.Background(), "sample.call", request, options...)
}

func nestedArrayResponseManifest(t *testing.T, depth int) generationapi.Manifest {
	t.Helper()
	schemas := []generationapi.SchemaSpec{{ID: "scalar.boolean", Kind: generationapi.SchemaBoolean}}
	for index := depth - 1; index >= 0; index-- {
		item := "scalar.boolean"
		if index+1 < depth {
			item = "sample.array." + strconv.Itoa(index+1)
		}
		schemas = append(schemas, generationapi.SchemaSpec{ID: "sample.array." + strconv.Itoa(index), Kind: generationapi.SchemaArray, ItemSchemaRef: item})
	}
	return customResponseManifest(t, "sample.array.0", schemas)
}

func arrayResponseManifest(t *testing.T) generationapi.Manifest {
	t.Helper()
	return customResponseManifest(t, "sample.array", []generationapi.SchemaSpec{
		{ID: "scalar.boolean", Kind: generationapi.SchemaBoolean},
		{ID: "sample.array", Kind: generationapi.SchemaArray, ItemSchemaRef: "scalar.boolean"},
	})
}

func customResponseManifest(t *testing.T, responseSchemaRef string, responseSchemas []generationapi.SchemaSpec) generationapi.Manifest {
	t.Helper()
	requestRef := mustResponseRef(t, "repo:sdk/api/testdata/runtime.api#type:LimitRequest")
	responseRef := mustResponseRef(t, "repo:sdk/api/testdata/runtime.api#type:LimitResponse")
	operationRef := mustResponseRef(t, "repo:sdk/api/testdata/runtime.api#route:LimitCall")
	usesResponseSource := false
	for index := range responseSchemas {
		if responseSchemas[index].Kind == generationapi.SchemaArray {
			responseSchemas[index].Provenance = provenancePointerForResponse(responseRef)
			usesResponseSource = true
		}
	}
	sources := []provenance.Source{
		{Ref: requestRef, Digest: provenance.SHA256([]byte("limit request owner"))},
		{Ref: operationRef, Digest: provenance.SHA256([]byte("limit operation owner"))},
	}
	if usesResponseSource {
		sources = append(sources, provenance.Source{Ref: responseRef, Digest: provenance.SHA256([]byte("limit response owner"))})
	}
	manifest, err := generationapi.NewManifest(generationapi.ManifestSpec{
		Sources: sources,
		Schemas: append([]generationapi.SchemaSpec{{
			ID: "sample.request", Kind: generationapi.SchemaObject,
			Provenance: provenancePointerForResponse(requestRef), Fields: []generationapi.FieldSpec{},
		}}, responseSchemas...),
		Operations: []generationapi.OperationSpec{{
			ID: "sample.call", Method: generationapi.MethodGET, Path: "/sample",
			Provenance:       generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{operationRef}},
			RequestSchemaRef: "sample.request", ResponseBody: generationapi.ResponseBodyJSON, ResponseSchemaRef: responseSchemaRef,
			RequestBindings: []generationapi.RequestBindingSpec{}, Auth: generationapi.AuthSpec{Mode: generationapi.AuthNone},
			ErrorProjections: []generationapi.ErrorProjectionSpec{},
		}},
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v; schemas=%s", err, fmt.Sprint(responseSchemas))
	}
	return manifest
}

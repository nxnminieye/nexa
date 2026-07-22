package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nxnminieye/nexa/cli/protocol"
	generationapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

func TestNewClientAcceptsValidatedManifestAndNormalizesNilCredentialProvider(t *testing.T) {
	tests := []struct {
		name     string
		endpoint *url.URL
		provider CredentialProvider
	}{
		{name: "empty prefix", endpoint: &url.URL{Scheme: "https", Host: "api.example.test"}},
		{name: "root prefix", endpoint: &url.URL{Scheme: "https", Host: "api.example.test", Path: "/"}},
		{name: "unreserved prefix and port", endpoint: &url.URL{Scheme: "http", Host: "api.example.test:8080", Path: "/api/v1~beta"}},
		{name: "nil provider", endpoint: &url.URL{Scheme: "https", Host: "api.example.test"}, provider: nil},
		{name: "typed nil provider", endpoint: &url.URL{Scheme: "https", Host: "api.example.test"}, provider: CredentialProviderFunc(nil)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, err := generationapi.NewManifest(generationapi.ManifestSpec{})
			if err != nil {
				t.Fatal(err)
			}
			transportCalls := 0
			client, err := New(Options{
				Manifest: manifest,
				Endpoint: test.endpoint,
				Transport: TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
					transportCalls++
					return WireResponse{}, nil
				}),
				CredentialProvider: test.provider,
				MaxResponseBytes:   RuntimeLimits().ResponseBytesMin,
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if client == nil {
				t.Fatal("New() returned a nil Client")
			}
			if transportCalls != 0 {
				t.Fatalf("transport calls = %d, want 0", transportCalls)
			}
		})
	}
}

func TestClientRejectsZeroManifest(t *testing.T) {
	options := validClientOptions(t)
	options.Manifest = generationapi.Manifest{}
	_, err := New(options)
	requireClientFailure(t, err, sdkFailure{
		code:     "api_manifest_required",
		category: protocol.CategoryInput,
		reason:   "manifest_required",
		pointer:  "/manifest",
	})
}

func TestClientRejectsInvalidEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint *url.URL
		reason   string
	}{
		{name: "nil", endpoint: nil, reason: "endpoint_required"},
		{name: "uppercase scheme", endpoint: &url.URL{Scheme: "HTTPS", Host: "api.example.test"}, reason: "endpoint_invalid"},
		{name: "unsupported scheme", endpoint: &url.URL{Scheme: "ftp", Host: "api.example.test"}, reason: "endpoint_invalid"},
		{name: "empty host", endpoint: &url.URL{Scheme: "https"}, reason: "endpoint_invalid"},
		{name: "opaque", endpoint: &url.URL{Scheme: "https", Opaque: "//api.example.test"}, reason: "endpoint_invalid"},
		{name: "user", endpoint: &url.URL{Scheme: "https", Host: "api.example.test", User: url.User("alice")}, reason: "endpoint_invalid"},
		{name: "query", endpoint: &url.URL{Scheme: "https", Host: "api.example.test", RawQuery: "secret=redacted"}, reason: "endpoint_invalid"},
		{name: "force query", endpoint: &url.URL{Scheme: "https", Host: "api.example.test", ForceQuery: true}, reason: "endpoint_invalid"},
		{name: "fragment", endpoint: &url.URL{Scheme: "https", Host: "api.example.test", Fragment: "fragment"}, reason: "endpoint_invalid"},
		{name: "raw fragment", endpoint: &url.URL{Scheme: "https", Host: "api.example.test", RawFragment: "fragment"}, reason: "endpoint_invalid"},
		{name: "raw path", endpoint: &url.URL{Scheme: "https", Host: "api.example.test", Path: "/api", RawPath: "/api"}, reason: "endpoint_invalid"},
		{name: "dot segment", endpoint: &url.URL{Scheme: "https", Host: "api.example.test", Path: "/api/./v1"}, reason: "endpoint_invalid"},
		{name: "dot dot segment", endpoint: &url.URL{Scheme: "https", Host: "api.example.test", Path: "/api/../v1"}, reason: "endpoint_invalid"},
		{name: "repeated slash", endpoint: &url.URL{Scheme: "https", Host: "api.example.test", Path: "/api//v1"}, reason: "endpoint_invalid"},
		{name: "trailing slash", endpoint: &url.URL{Scheme: "https", Host: "api.example.test", Path: "/api/"}, reason: "endpoint_invalid"},
		{name: "non unreserved prefix", endpoint: &url.URL{Scheme: "https", Host: "api.example.test", Path: "/api:value"}, reason: "endpoint_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := validClientOptions(t)
			options.Endpoint = test.endpoint
			_, err := New(options)
			requireClientFailure(t, err, sdkFailure{
				code:     "client_invalid",
				category: protocol.CategoryInput,
				reason:   test.reason,
				pointer:  "/endpoint",
			})
		})
	}
}

func TestClientRejectsNilTransport(t *testing.T) {
	tests := []struct {
		name      string
		transport Transport
	}{
		{name: "nil interface", transport: nil},
		{name: "typed nil function", transport: TransportFunc(nil)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := validClientOptions(t)
			options.Transport = test.transport
			_, err := New(options)
			requireClientFailure(t, err, sdkFailure{
				code:     "client_invalid",
				category: protocol.CategoryInput,
				reason:   "transport_required",
				pointer:  "/transport",
			})
		})
	}
}

func TestClientRejectsResponseLimitOutsideRuntimeLimits(t *testing.T) {
	limits := RuntimeLimits()
	for _, value := range []int64{limits.ResponseBytesMin - 1, limits.ResponseBytesMax + 1} {
		t.Run(strconv.FormatInt(value, 10), func(t *testing.T) {
			options := validClientOptions(t)
			options.MaxResponseBytes = value
			_, err := New(options)
			requireClientFailure(t, err, sdkFailure{
				code:     "client_invalid",
				category: protocol.CategoryInput,
				reason:   "max_response_bytes_invalid",
				pointer:  "/maxResponseBytes",
			})
		})
	}
}

func TestEndpointPathConformanceUsesOneCanonicalURLRepresentation(t *testing.T) {
	manifest := newRuntimeManifest(t, "/items/{value}", []generationapi.FieldSpec{
		{Name: "value", SchemaRef: "scalar.string", Required: true},
		{Name: "query", SchemaRef: "scalar.string"},
	}, []generationapi.RequestBindingSpec{
		{Field: "value", Location: generationapi.RequestBindingPath, Name: "value"},
		{Field: "query", Location: generationapi.RequestBindingQuery, Name: "q"},
	}, generationapi.AuthSpec{Mode: generationapi.AuthNone}, nil)

	tests := []struct {
		name, prefix, value, query, encodedPath, rawQuery string
	}{
		{name: "plain root", value: "plain", encodedPath: "/items/plain"},
		{name: "plain path keeps RawPath", prefix: "/api/v1", value: "plain", encodedPath: "/api/v1/items/plain"},
		{name: "percent", value: "%", encodedPath: "/items/%25"},
		{name: "unicode", value: "\u4e2d", encodedPath: "/items/%E4%B8%AD"},
		{name: "slash plus and space", value: "a b/+\u4e2d", encodedPath: "/items/a%20b%2F%2B%E4%B8%AD"},
		{name: "embedded traversal query fragment bytes", prefix: "/api", value: "../x?y#z", encodedPath: "/api/items/..%2Fx%3Fy%23z"},
		{name: "query coexistence", prefix: "/api", value: "%", query: "a b/+\u4e2d", encodedPath: "/api/items/%25", rawQuery: "q=a%20b%2F%2B%E4%B8%AD"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			endpoint := &url.URL{Scheme: "https", Host: "api.example.test:8443", Path: test.prefix}
			client, transportCalls := newRuntimeClient(t, manifest, endpoint, nil)
			requestJSON := `{"value":` + strconv.Quote(test.value)
			if test.query != "" {
				requestJSON += `,"query":` + strconv.Quote(test.query)
			}
			requestJSON += `}`
			wire, err := buildRuntimeWire(context.Background(), client, nil, requestJSON)
			if err != nil {
				t.Fatalf("buildWireRequest() error = %v", err)
			}
			gotURL := wire.URL()
			decodedPath, err := url.PathUnescape(test.encodedPath)
			if err != nil {
				t.Fatal(err)
			}
			if wire.Method() != generationapi.MethodPOST || gotURL.Scheme != "https" || gotURL.Host != "api.example.test:8443" {
				t.Fatalf("wire origin/method = %s %s://%s", wire.Method(), gotURL.Scheme, gotURL.Host)
			}
			if gotURL.Path != decodedPath || gotURL.RawPath != test.encodedPath || gotURL.EscapedPath() != test.encodedPath || gotURL.RawQuery != test.rawQuery {
				t.Fatalf("URL = Path %q RawPath %q EscapedPath %q RawQuery %q", gotURL.Path, gotURL.RawPath, gotURL.EscapedPath(), gotURL.RawQuery)
			}
			wantRequestURI := test.encodedPath
			if test.rawQuery != "" {
				wantRequestURI += "?" + test.rawQuery
			}
			if gotURL.RequestURI() != wantRequestURI {
				t.Fatalf("RequestURI() = %q, want %q", gotURL.RequestURI(), wantRequestURI)
			}
			if gotURL.RawPath == "" {
				t.Fatal("RawPath must be set for every path, including plain paths")
			}
			if wire.Body() != nil || len(wire.Headers()) != 0 || transportCalls.Load() != 0 {
				t.Fatalf("unexpected body/headers/transport calls: %q %#v %d", wire.Body(), wire.Headers(), transportCalls.Load())
			}
		})
	}
}

func TestEndpointPathConformanceClonesInputAndResultURLs(t *testing.T) {
	manifest := newRuntimeManifest(t, "/items/{value}", []generationapi.FieldSpec{
		{Name: "value", SchemaRef: "scalar.string", Required: true},
	}, []generationapi.RequestBindingSpec{
		{Field: "value", Location: generationapi.RequestBindingPath, Name: "value"},
	}, generationapi.AuthSpec{Mode: generationapi.AuthNone}, nil)
	endpoint := &url.URL{Scheme: "https", Host: "api.example.test", Path: "/original"}
	client, transportCalls := newRuntimeClient(t, manifest, endpoint, nil)
	endpoint.Scheme = "http"
	endpoint.Host = "mutated.example.test"
	endpoint.Path = "/mutated"
	endpoint.RawQuery = "secret=must-not-leak"

	wire, err := buildRuntimeWire(context.Background(), client, nil, `{"value":"one"}`)
	if err != nil {
		t.Fatal(err)
	}
	first := wire.URL()
	if first.String() != "https://api.example.test/original/items/one" || first.RawPath != "/original/items/one" {
		t.Fatalf("input URL mutation leaked: %#v", first)
	}
	first.Scheme = "ftp"
	first.Host = "result-mutated.example.test"
	first.Path = "/result-mutated"
	first.RawPath = "/result-mutated"
	first.RawQuery = "secret=must-not-leak"
	second := wire.URL()
	if second.String() != "https://api.example.test/original/items/one" || second.RawPath != "/original/items/one" {
		t.Fatalf("result URL mutation leaked: %#v", second)
	}
	if transportCalls.Load() != 0 {
		t.Fatalf("transport calls = %d, want 0", transportCalls.Load())
	}
}

func TestEndpointPathConformanceRejectsExactDotSegments(t *testing.T) {
	manifest := newRuntimeManifest(t, "/items/{value}", []generationapi.FieldSpec{
		{Name: "value", SchemaRef: "scalar.string", Required: true},
	}, []generationapi.RequestBindingSpec{
		{Field: "value", Location: generationapi.RequestBindingPath, Name: "value"},
	}, generationapi.AuthSpec{Mode: generationapi.AuthNone}, nil)
	client, transportCalls := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test"}, nil)
	for _, value := range []string{".", ".."} {
		t.Run(value, func(t *testing.T) {
			_, err := buildRuntimeWire(context.Background(), client, nil, `{"value":`+strconv.Quote(value)+`}`)
			requireClientFailure(t, err, sdkFailure{
				code:           "request_invalid",
				category:       protocol.CategoryInput,
				reason:         "value_invalid",
				pointer:        "/value",
				apiOperationID: "sample.call",
			})
		})
	}
	if transportCalls.Load() != 0 {
		t.Fatalf("transport calls = %d, want 0", transportCalls.Load())
	}
}

func TestWireConformanceSortsQueryHeadersAndCanonicalizesBody(t *testing.T) {
	manifest := newRuntimeManifest(t, "/items/{path}", []generationapi.FieldSpec{
		{Name: "path", SchemaRef: "scalar.string", Required: true},
		{Name: "queryZ", SchemaRef: "scalar.string"},
		{Name: "queryA", SchemaRef: "scalar.string"},
		{Name: "headerZ", SchemaRef: "scalar.string"},
		{Name: "headerA", SchemaRef: "scalar.string"},
		{Name: "bodyB", SchemaRef: "scalar.string"},
		{Name: "bodyA", SchemaRef: "scalar.number"},
	}, []generationapi.RequestBindingSpec{
		{Field: "path", Location: generationapi.RequestBindingPath, Name: "path"},
		{Field: "queryZ", Location: generationapi.RequestBindingQuery, Name: "z"},
		{Field: "queryA", Location: generationapi.RequestBindingQuery, Name: "a"},
		{Field: "headerZ", Location: generationapi.RequestBindingHeader, Name: "X-Z"},
		{Field: "headerA", Location: generationapi.RequestBindingHeader, Name: "X-A"},
		{Field: "bodyB", Location: generationapi.RequestBindingBody, Name: "b"},
		{Field: "bodyA", Location: generationapi.RequestBindingBody, Name: "a"},
	}, generationapi.AuthSpec{Mode: generationapi.AuthRequired, Credentials: []generationapi.CredentialSpec{
		{ID: "primary", Type: generationapi.CredentialBearer, Location: generationapi.CredentialLocationHeader, Name: "Authorization"},
	}}, nil)
	provider := NewStaticCredentialProvider([]CredentialValue{{ID: "primary", Value: "sample-token"}})
	client, transportCalls := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test", Path: "/api"}, provider)
	wire, err := buildRuntimeWire(context.Background(), client, provider, `{"path":"one","queryZ":"z z","queryA":"+","headerZ":"last","headerA":"inside\t space","bodyB":"text","bodyA":1e0}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := wire.URL().RequestURI(); got != "/api/items/one?a=%2B&z=z%20z" {
		t.Fatalf("RequestURI() = %q", got)
	}
	wantHeaders := []Header{
		{Name: "authorization", Value: "Bearer sample-token"},
		{Name: generationapi.RequestContentTypeHeader, Value: generationapi.RequestJSONMediaType},
		{Name: "x-a", Value: "inside\t space"},
		{Name: "x-z", Value: "last"},
	}
	if got := wire.Headers(); !reflect.DeepEqual(got, wantHeaders) {
		t.Fatalf("Headers() = %#v, want %#v", got, wantHeaders)
	}
	if got, want := wire.Body(), []byte(`{"a":1,"b":"text"}`); !bytes.Equal(got, want) {
		t.Fatalf("Body() = %s, want %s", got, want)
	}

	headers := wire.Headers()
	headers[0].Name = "mutated"
	headers[0].Value = "secret"
	body := wire.Body()
	body[0] = 'X'
	if !reflect.DeepEqual(wire.Headers(), wantHeaders) || !bytes.Equal(wire.Body(), []byte(`{"a":1,"b":"text"}`)) {
		t.Fatal("header or body accessor mutation leaked into WireRequest")
	}
	if transportCalls.Load() != 0 {
		t.Fatalf("transport calls = %d, want 0", transportCalls.Load())
	}
}

func TestWireConformanceEmitsEmptyBodyObjectWhenBindingsAreAbsent(t *testing.T) {
	manifest := newRuntimeManifest(t, "/items", []generationapi.FieldSpec{
		{Name: "optional", SchemaRef: "scalar.string"},
	}, []generationapi.RequestBindingSpec{
		{Field: "optional", Location: generationapi.RequestBindingBody, Name: "optional"},
	}, generationapi.AuthSpec{Mode: generationapi.AuthNone}, nil)
	client, _ := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test"}, nil)
	wire, err := buildRuntimeWire(context.Background(), client, nil, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wire.Body(), []byte(`{}`)) || !reflect.DeepEqual(wire.Headers(), []Header{{Name: generationapi.RequestContentTypeHeader, Value: generationapi.RequestJSONMediaType}}) {
		t.Fatalf("empty body wire = body %q headers %#v", wire.Body(), wire.Headers())
	}
}

func TestWireConformanceOmitsBodyAndContentTypeWithoutBodyBindings(t *testing.T) {
	manifest := newRuntimeManifest(t, "/items", []generationapi.FieldSpec{
		{Name: "query", SchemaRef: "scalar.string"},
	}, []generationapi.RequestBindingSpec{
		{Field: "query", Location: generationapi.RequestBindingQuery, Name: "query"},
	}, generationapi.AuthSpec{Mode: generationapi.AuthNone}, nil)
	client, _ := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test"}, nil)
	wire, err := buildRuntimeWire(context.Background(), client, nil, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if wire.Body() != nil || len(wire.Headers()) != 0 || wire.URL().RawQuery != "" {
		t.Fatalf("absent optional wire = body %q headers %#v query %q", wire.Body(), wire.Headers(), wire.URL().RawQuery)
	}
}

func TestWireConformanceRejectsInvalidOrdinaryHeaderValuesBeforeProvider(t *testing.T) {
	manifest := newRuntimeManifest(t, "/items", []generationapi.FieldSpec{
		{Name: "header", SchemaRef: "scalar.string", Required: true},
	}, []generationapi.RequestBindingSpec{
		{Field: "header", Location: generationapi.RequestBindingHeader, Name: "X-Value"},
	}, generationapi.AuthSpec{Mode: generationapi.AuthRequired, Credentials: []generationapi.CredentialSpec{
		{ID: "primary", Type: generationapi.CredentialBearer, Location: generationapi.CredentialLocationHeader, Name: "Authorization"},
	}}, nil)
	invalid := []string{" leading", "trailing ", "\tleading", "trailing\t", "line\nbreak", "delete\x7f", "\u4e2d"}
	for _, value := range invalid {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			encodedValue, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			providerCalls := 0
			provider := CredentialProviderFunc(func(context.Context, string) ([]CredentialValue, error) {
				providerCalls++
				return []CredentialValue{{ID: "primary", Value: "token"}}, nil
			})
			client, transportCalls := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test"}, provider)
			_, err := buildRuntimeWire(context.Background(), client, provider, `{"header":`+string(encodedValue)+`}`)
			requireClientFailure(t, err, sdkFailure{code: "request_invalid", category: protocol.CategoryInput, reason: "value_invalid", pointer: "/header", apiOperationID: "sample.call"})
			if providerCalls != 0 || transportCalls.Load() != 0 {
				t.Fatalf("calls = provider %d transport %d, want 0/0", providerCalls, transportCalls.Load())
			}
		})
	}

	provider := NewStaticCredentialProvider([]CredentialValue{{ID: "primary", Value: "token"}})
	client, _ := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test"}, provider)
	wire, err := buildRuntimeWire(context.Background(), client, provider, `{"header":"inside\t space"}`)
	if err != nil {
		t.Fatalf("internal whitespace rejected: %v", err)
	}
	if got := wire.Headers(); !reflect.DeepEqual(got, []Header{{Name: "authorization", Value: "Bearer token"}, {Name: "x-value", Value: "inside\t space"}}) {
		t.Fatalf("headers = %#v", got)
	}
}

func TestRequestConformanceFreezesLookupSchemaContextAndProviderPrecedence(t *testing.T) {
	manifest := newRuntimeManifest(t, "/items/{value}", []generationapi.FieldSpec{
		{Name: "value", SchemaRef: "scalar.string", Required: true},
	}, []generationapi.RequestBindingSpec{
		{Field: "value", Location: generationapi.RequestBindingPath, Name: "value"},
	}, generationapi.AuthSpec{Mode: generationapi.AuthRequired, Credentials: []generationapi.CredentialSpec{
		{ID: "primary", Type: generationapi.CredentialBearer, Location: generationapi.CredentialLocationHeader, Name: "Authorization"},
	}}, nil)
	providerCalls := 0
	provider := CredentialProviderFunc(func(context.Context, string) ([]CredentialValue, error) {
		providerCalls++
		return []CredentialValue{{ID: "primary", Value: "token"}}, nil
	})
	client, transportCalls := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test"}, provider)

	_, err := client.buildWireRequest(context.Background(), "secret-operation", Request{}, requestBuildOptions{credentialProvider: provider})
	unknown := requireClientFailure(t, err, sdkFailure{code: "operation_not_found", category: protocol.CategoryInput, reason: "operation_unknown", pointer: "/apiOperationId"})
	assertSDKProjectionOmits(t, unknown, "secret-operation")
	if providerCalls != 0 {
		t.Fatalf("provider calls after unknown operation = %d, want 0", providerCalls)
	}

	_, err = client.buildWireRequest(context.Background(), "sample.call", Request{}, requestBuildOptions{credentialProvider: provider})
	requireClientFailure(t, err, sdkFailure{code: "request_invalid", category: protocol.CategoryInput, reason: "request_required", pointer: "/request", apiOperationID: "sample.call"})
	if providerCalls != 0 {
		t.Fatalf("provider calls after zero request = %d, want 0", providerCalls)
	}

	schemaCases := []struct {
		name, requestJSON, reason, pointer string
	}{
		{name: "missing required", requestJSON: `{}`, reason: "field_required", pointer: "/value"},
		{name: "unknown field", requestJSON: `{"value":"ok","secretField":"must-not-leak"}`, reason: "field_unknown", pointer: "/secretField"},
		{name: "kind mismatch", requestJSON: `{"value":123}`, reason: "value_invalid", pointer: "/value"},
	}
	for _, test := range schemaCases {
		t.Run(test.name, func(t *testing.T) {
			request, parseErr := ParseRequest([]byte(test.requestJSON))
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			_, buildErr := client.buildWireRequest(context.Background(), "sample.call", request, requestBuildOptions{credentialProvider: provider})
			apiError := requireClientFailure(t, buildErr, sdkFailure{code: "request_invalid", category: protocol.CategoryInput, reason: test.reason, pointer: test.pointer, apiOperationID: "sample.call"})
			assertSDKProjectionOmits(t, apiError, "must-not-leak")
		})
	}
	if providerCalls != 0 {
		t.Fatalf("provider calls after schema failures = %d, want 0", providerCalls)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	missing, err := ParseRequest([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.buildWireRequest(canceled, "sample.call", missing, requestBuildOptions{credentialProvider: provider})
	requireClientFailure(t, err, sdkFailure{code: "request_invalid", category: protocol.CategoryInput, reason: "field_required", pointer: "/value", apiOperationID: "sample.call"})
	if providerCalls != 0 {
		t.Fatalf("provider calls after canceled invalid request = %d, want 0", providerCalls)
	}

	valid, err := ParseRequest([]byte(`{"value":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.buildWireRequest(canceled, "sample.call", valid, requestBuildOptions{credentialProvider: provider})
	requireClientFailure(t, err, sdkFailure{code: "operation_canceled", category: protocol.CategoryCanceled, reason: "context_canceled", apiOperationID: "sample.call"})

	deadline, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	_, err = client.buildWireRequest(deadline, "sample.call", valid, requestBuildOptions{credentialProvider: provider})
	requireClientFailure(t, err, sdkFailure{code: "operation_canceled", category: protocol.CategoryCanceled, reason: "context_deadline", apiOperationID: "sample.call"})
	if providerCalls != 0 || transportCalls.Load() != 0 {
		t.Fatalf("calls after precedence suite = provider %d transport %d, want 0/0", providerCalls, transportCalls.Load())
	}
}

func TestRequestConformanceCanonicalizesIntegerAndNumberAtEveryBinding(t *testing.T) {
	valid := []struct {
		name          string
		kind          generationapi.SchemaKind
		input, output string
	}{
		{name: "integer minimum", kind: generationapi.SchemaInteger, input: "-9223372036854775808", output: "-9223372036854775808"},
		{name: "integer negative zero", kind: generationapi.SchemaInteger, input: "-0", output: "0"},
		{name: "integer ordinary", kind: generationapi.SchemaInteger, input: "10", output: "10"},
		{name: "integer maximum", kind: generationapi.SchemaInteger, input: "9223372036854775807", output: "9223372036854775807"},
		{name: "number integer token", kind: generationapi.SchemaNumber, input: "10", output: "10"},
		{name: "number negative zero integer", kind: generationapi.SchemaNumber, input: "-0", output: "0"},
		{name: "number negative zero fraction", kind: generationapi.SchemaNumber, input: "-0.0", output: "0"},
		{name: "number fraction canonical integer", kind: generationapi.SchemaNumber, input: "1.0", output: "1"},
		{name: "number exponent canonical integer", kind: generationapi.SchemaNumber, input: "1e0", output: "1"},
		{name: "number finite underflow", kind: generationapi.SchemaNumber, input: "1e-4000", output: "0"},
		{name: "number large exponent", kind: generationapi.SchemaNumber, input: "1e30", output: "1e+30"},
	}
	locations := []generationapi.RequestBindingLocation{
		generationapi.RequestBindingPath,
		generationapi.RequestBindingQuery,
		generationapi.RequestBindingHeader,
		generationapi.RequestBindingBody,
	}
	for _, vector := range valid {
		for _, location := range locations {
			name := vector.name + "/" + string(location)
			t.Run(name, func(t *testing.T) {
				manifest := scalarBindingManifest(t, vector.kind, location)
				client, transportCalls := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test"}, nil)
				wire, err := buildRuntimeWire(context.Background(), client, nil, `{"value":`+vector.input+`}`)
				if err != nil {
					t.Fatalf("buildWireRequest() error = %v", err)
				}
				switch location {
				case generationapi.RequestBindingPath:
					want := "/values/" + rfc3986Encode(vector.output)
					if wire.URL().RawPath != want || wire.URL().RequestURI() != want {
						t.Fatalf("path wire = %q / %q, want %q", wire.URL().RawPath, wire.URL().RequestURI(), want)
					}
				case generationapi.RequestBindingQuery:
					want := "value=" + rfc3986Encode(vector.output)
					if wire.URL().RawQuery != want {
						t.Fatalf("query wire = %q, want %q", wire.URL().RawQuery, want)
					}
				case generationapi.RequestBindingHeader:
					if !reflect.DeepEqual(wire.Headers(), []Header{{Name: "x-value", Value: vector.output}}) {
						t.Fatalf("header wire = %#v", wire.Headers())
					}
				case generationapi.RequestBindingBody:
					if !bytes.Equal(wire.Body(), []byte(`{"value":`+vector.output+`}`)) {
						t.Fatalf("body wire = %s", wire.Body())
					}
				}
				if transportCalls.Load() != 0 {
					t.Fatalf("transport calls = %d, want 0", transportCalls.Load())
				}
			})
		}
	}
}

func TestRequestConformanceRejectsIntegerAndNumberDomainErrors(t *testing.T) {
	tests := []struct {
		name  string
		kind  generationapi.SchemaKind
		input string
	}{
		{name: "integer below minimum", kind: generationapi.SchemaInteger, input: "-9223372036854775809"},
		{name: "integer above maximum", kind: generationapi.SchemaInteger, input: "9223372036854775808"},
		{name: "integer fraction token", kind: generationapi.SchemaInteger, input: "1.0"},
		{name: "integer exponent token", kind: generationapi.SchemaInteger, input: "1e0"},
		{name: "integer string", kind: generationapi.SchemaInteger, input: `"1"`},
		{name: "number overflow", kind: generationapi.SchemaNumber, input: "1e309"},
		{name: "number string", kind: generationapi.SchemaNumber, input: `"1"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := scalarBindingManifest(t, test.kind, generationapi.RequestBindingBody)
			client, transportCalls := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test"}, nil)
			_, err := buildRuntimeWire(context.Background(), client, nil, `{"value":`+test.input+`}`)
			requireClientFailure(t, err, sdkFailure{code: "request_invalid", category: protocol.CategoryInput, reason: "value_invalid", pointer: "/value", apiOperationID: "sample.call"})
			if transportCalls.Load() != 0 {
				t.Fatalf("transport calls = %d, want 0", transportCalls.Load())
			}
		})
	}
}

func TestRequestConformanceCanonicalizesAndValidatesNestedBody(t *testing.T) {
	manifest := newRuntimeManifest(t, "/items", []generationapi.FieldSpec{
		{Name: "payload", SchemaRef: "sample.payload", Required: true},
	}, []generationapi.RequestBindingSpec{
		{Field: "payload", Location: generationapi.RequestBindingBody, Name: "payload"},
	}, generationapi.AuthSpec{Mode: generationapi.AuthNone}, []generationapi.SchemaSpec{
		{ID: "sample.payload", Kind: generationapi.SchemaObject, Fields: []generationapi.FieldSpec{
			{Name: "integer", SchemaRef: "scalar.integer", Required: true},
			{Name: "number", SchemaRef: "scalar.number", Required: true},
			{Name: "items", SchemaRef: "sample.numbers", Required: true},
		}},
		{ID: "sample.numbers", Kind: generationapi.SchemaArray, ItemSchemaRef: "scalar.number"},
	})
	client, transportCalls := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test"}, nil)
	wire, err := buildRuntimeWire(context.Background(), client, nil, `{"payload":{"number":1e-4000,"integer":-0,"items":[1.0,-0.0]}}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"payload":{"integer":0,"items":[1,0],"number":0}}`)
	if !bytes.Equal(wire.Body(), want) {
		t.Fatalf("nested body = %s, want %s", wire.Body(), want)
	}

	invalid := []struct {
		name, requestJSON, reason, pointer string
	}{
		{name: "nested required", requestJSON: `{"payload":{"number":1,"items":[]}}`, reason: "field_required", pointer: "/payload/integer"},
		{name: "nested unknown", requestJSON: `{"payload":{"integer":1,"number":1,"items":[],"unknown":"secret"}}`, reason: "field_unknown", pointer: "/payload/unknown"},
		{name: "array item type", requestJSON: `{"payload":{"integer":1,"number":1,"items":[1,"bad"]}}`, reason: "value_invalid", pointer: "/payload/items/1"},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			_, buildErr := buildRuntimeWire(context.Background(), client, nil, test.requestJSON)
			requireClientFailure(t, buildErr, sdkFailure{code: "request_invalid", category: protocol.CategoryInput, reason: test.reason, pointer: test.pointer, apiOperationID: "sample.call"})
		})
	}
	_, nullErr := ParseRequest([]byte(`{"payload":null}`))
	requireClientFailure(t, nullErr, sdkFailure{code: "request_invalid", category: protocol.CategoryInput, reason: "null_not_allowed", pointer: "/payload"})
	if transportCalls.Load() != 0 {
		t.Fatalf("transport calls = %d, want 0", transportCalls.Load())
	}
}

func TestCredentialSelectionCountsBeforeReadingIDsOrValues(t *testing.T) {
	credential := generationapi.CredentialSpec{ID: "primary", Type: generationapi.CredentialBearer, Location: generationapi.CredentialLocationHeader, Name: "Authorization"}
	tests := []struct {
		name        string
		mode        generationapi.AuthMode
		declared    []generationapi.CredentialSpec
		providerNil bool
		values      []CredentialValue
		valid       bool
		wantCalls   int
	}{
		{name: "required nil provider", mode: generationapi.AuthRequired, declared: []generationapi.CredentialSpec{credential}, providerNil: true, wantCalls: 0},
		{name: "required empty provider", mode: generationapi.AuthRequired, declared: []generationapi.CredentialSpec{credential}, values: []CredentialValue{}, wantCalls: 1},
		{name: "required mixed invalid multiple", mode: generationapi.AuthRequired, declared: []generationapi.CredentialSpec{credential}, values: []CredentialValue{{ID: "unknown", Value: ""}, {ID: "unknown", Value: "secret"}}, wantCalls: 1},
		{name: "optional nil provider", mode: generationapi.AuthOptional, declared: []generationapi.CredentialSpec{credential}, providerNil: true, valid: true, wantCalls: 0},
		{name: "optional empty provider", mode: generationapi.AuthOptional, declared: []generationapi.CredentialSpec{credential}, values: []CredentialValue{}, valid: true, wantCalls: 1},
		{name: "optional multiple", mode: generationapi.AuthOptional, declared: []generationapi.CredentialSpec{credential}, values: []CredentialValue{{ID: "primary", Value: "token"}, {ID: "unknown", Value: ""}}, wantCalls: 1},
		{name: "none nil provider", mode: generationapi.AuthNone, providerNil: true, valid: true, wantCalls: 0},
		{name: "none empty provider", mode: generationapi.AuthNone, values: []CredentialValue{}, valid: true, wantCalls: 1},
		{name: "none one", mode: generationapi.AuthNone, values: []CredentialValue{{ID: "unknown", Value: "secret"}}, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := newRuntimeManifest(t, "/items", nil, nil, generationapi.AuthSpec{Mode: test.mode, Credentials: test.declared}, nil)
			providerCalls := 0
			var provider CredentialProvider
			if !test.providerNil {
				provider = CredentialProviderFunc(func(_ context.Context, operationID string) ([]CredentialValue, error) {
					providerCalls++
					if operationID != "sample.call" {
						t.Fatalf("provider operation ID = %q", operationID)
					}
					return append([]CredentialValue(nil), test.values...), nil
				})
			}
			client, transportCalls := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test"}, provider)
			wire, err := buildRuntimeWire(context.Background(), client, provider, `{}`)
			if test.valid {
				if err != nil {
					t.Fatalf("buildWireRequest() error = %v", err)
				}
				if len(wire.Headers()) != 0 || wire.URL().RawQuery != "" {
					t.Fatalf("zero credential wire = headers %#v query %q", wire.Headers(), wire.URL().RawQuery)
				}
			} else {
				apiError := requireClientFailure(t, err, sdkFailure{code: "request_invalid", category: protocol.CategoryInput, reason: "credential_count_invalid", pointer: "/credentials", apiOperationID: "sample.call"})
				assertSDKProjectionOmits(t, apiError, "unknown", "secret")
			}
			if providerCalls != test.wantCalls || transportCalls.Load() != 0 {
				t.Fatalf("calls = provider %d transport %d, want %d/0", providerCalls, transportCalls.Load(), test.wantCalls)
			}
		})
	}
}

func TestCredentialBindsEachDeclaredTypeAndLocation(t *testing.T) {
	tests := []struct {
		name       string
		credential generationapi.CredentialSpec
		value      string
		headers    []Header
		rawQuery   string
	}{
		{
			name:       "bearer",
			credential: generationapi.CredentialSpec{ID: "primary", Type: generationapi.CredentialBearer, Location: generationapi.CredentialLocationHeader, Name: "Authorization"},
			value:      "token",
			headers:    []Header{{Name: "authorization", Value: "Bearer token"}},
		},
		{
			name:       "header API key",
			credential: generationapi.CredentialSpec{ID: "header-key", Type: generationapi.CredentialAPIKey, Location: generationapi.CredentialLocationHeader, Name: "X-API-Key"},
			value:      "key\twith space",
			headers:    []Header{{Name: "x-api-key", Value: "key\twith space"}},
		},
		{
			name:       "query API key",
			credential: generationapi.CredentialSpec{ID: "query-key", Type: generationapi.CredentialAPIKey, Location: generationapi.CredentialLocationQuery, Name: "api_key"},
			value:      "a b/+\u4e2d",
			rawQuery:   "api_key=a%20b%2F%2B%E4%B8%AD",
		},
		{
			name:       "cookie API key",
			credential: generationapi.CredentialSpec{ID: "cookie-key", Type: generationapi.CredentialAPIKey, Location: generationapi.CredentialLocationCookie, Name: "APIKey"},
			value:      "cookie-token",
			headers:    []Header{{Name: "cookie", Value: "APIKey=cookie-token"}},
		},
		{
			name:       "session cookie",
			credential: generationapi.CredentialSpec{ID: "session", Type: generationapi.CredentialSessionCookie, Location: generationapi.CredentialLocationCookie, Name: "SessionID"},
			value:      "session-token",
			headers:    []Header{{Name: "cookie", Value: "SessionID=session-token"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := newRuntimeManifest(t, "/items", nil, nil, generationapi.AuthSpec{Mode: generationapi.AuthRequired, Credentials: []generationapi.CredentialSpec{test.credential}}, nil)
			providerCalls := 0
			provider := CredentialProviderFunc(func(_ context.Context, operationID string) ([]CredentialValue, error) {
				providerCalls++
				if operationID != "sample.call" {
					t.Fatalf("provider operation ID = %q", operationID)
				}
				return []CredentialValue{{ID: test.credential.ID, Value: test.value}}, nil
			})
			client, transportCalls := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test"}, provider)
			wire, err := buildRuntimeWire(context.Background(), client, provider, `{}`)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(wire.Headers(), test.headers) || wire.URL().RawQuery != test.rawQuery {
				t.Fatalf("credential wire = headers %#v query %q", wire.Headers(), wire.URL().RawQuery)
			}
			if providerCalls != 1 || transportCalls.Load() != 0 {
				t.Fatalf("calls = provider %d transport %d, want 1/0", providerCalls, transportCalls.Load())
			}
		})
	}
}

func TestCredentialRejectsUnknownEmptyAndInvalidSingleValues(t *testing.T) {
	headerCredential := generationapi.CredentialSpec{ID: "primary", Type: generationapi.CredentialBearer, Location: generationapi.CredentialLocationHeader, Name: "Authorization"}
	queryCredential := generationapi.CredentialSpec{ID: "query-key", Type: generationapi.CredentialAPIKey, Location: generationapi.CredentialLocationQuery, Name: "api_key"}
	cookieCredential := generationapi.CredentialSpec{ID: "session", Type: generationapi.CredentialSessionCookie, Location: generationapi.CredentialLocationCookie, Name: "SessionID"}
	tests := []struct {
		name       string
		credential generationapi.CredentialSpec
		value      CredentialValue
		reason     string
	}{
		{name: "unknown ID", credential: headerCredential, value: CredentialValue{ID: "secret-unknown", Value: "secret-value"}, reason: "credential_id_unknown"},
		{name: "empty value", credential: headerCredential, value: CredentialValue{ID: "primary", Value: ""}, reason: "credential_value_empty"},
		{name: "bearer leading space", credential: headerCredential, value: CredentialValue{ID: "primary", Value: " leading"}, reason: "credential_value_invalid"},
		{name: "bearer trailing tab", credential: headerCredential, value: CredentialValue{ID: "primary", Value: "trailing\t"}, reason: "credential_value_invalid"},
		{name: "bearer control", credential: headerCredential, value: CredentialValue{ID: "primary", Value: "line\nbreak"}, reason: "credential_value_invalid"},
		{name: "bearer non ASCII", credential: headerCredential, value: CredentialValue{ID: "primary", Value: "\u4e2d"}, reason: "credential_value_invalid"},
		{name: "query invalid UTF-8", credential: queryCredential, value: CredentialValue{ID: "query-key", Value: string([]byte{0xff})}, reason: "credential_value_invalid"},
		{name: "cookie space", credential: cookieCredential, value: CredentialValue{ID: "session", Value: "bad value"}, reason: "credential_value_invalid"},
		{name: "cookie quote", credential: cookieCredential, value: CredentialValue{ID: "session", Value: `bad"value`}, reason: "credential_value_invalid"},
		{name: "cookie comma", credential: cookieCredential, value: CredentialValue{ID: "session", Value: "bad,value"}, reason: "credential_value_invalid"},
		{name: "cookie semicolon", credential: cookieCredential, value: CredentialValue{ID: "session", Value: "bad;value"}, reason: "credential_value_invalid"},
		{name: "cookie backslash", credential: cookieCredential, value: CredentialValue{ID: "session", Value: `bad\value`}, reason: "credential_value_invalid"},
		{name: "cookie non ASCII", credential: cookieCredential, value: CredentialValue{ID: "session", Value: "\u4e2d"}, reason: "credential_value_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := newRuntimeManifest(t, "/items", nil, nil, generationapi.AuthSpec{Mode: generationapi.AuthRequired, Credentials: []generationapi.CredentialSpec{test.credential}}, nil)
			providerCalls := 0
			provider := CredentialProviderFunc(func(context.Context, string) ([]CredentialValue, error) {
				providerCalls++
				return []CredentialValue{test.value}, nil
			})
			client, transportCalls := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test"}, provider)
			_, err := buildRuntimeWire(context.Background(), client, provider, `{}`)
			apiError := requireClientFailure(t, err, sdkFailure{code: "request_invalid", category: protocol.CategoryInput, reason: test.reason, pointer: "/credentials", apiOperationID: "sample.call"})
			assertSDKProjectionOmits(t, apiError, test.value.ID, test.value.Value, "secret")
			if providerCalls != 1 || transportCalls.Load() != 0 {
				t.Fatalf("calls = provider %d transport %d, want 1/0", providerCalls, transportCalls.Load())
			}
		})
	}
}

func TestCredentialStaticProviderIsPureAndDefensivelyCopies(t *testing.T) {
	input := []CredentialValue{
		{ID: "", Value: ""},
		{ID: "duplicate", Value: "first"},
		{ID: "duplicate", Value: "second"},
	}
	provider := NewStaticCredentialProvider(input)
	input[0] = CredentialValue{ID: "mutated", Value: "secret"}
	first, err := provider.Credentials(context.Background(), "ignored.operation")
	if err != nil {
		t.Fatalf("Credentials() error = %v", err)
	}
	want := []CredentialValue{{ID: "", Value: ""}, {ID: "duplicate", Value: "first"}, {ID: "duplicate", Value: "second"}}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("Credentials() = %#v, want %#v", first, want)
	}
	first[0] = CredentialValue{ID: "result-mutated", Value: "secret"}
	second, err := provider.Credentials(context.Background(), "ignored.operation")
	if err != nil || !reflect.DeepEqual(second, want) {
		t.Fatalf("second Credentials() = %#v, %v", second, err)
	}
}

func TestCredentialProviderFailurePanicAndCancellationAreSafe(t *testing.T) {
	credential := generationapi.CredentialSpec{ID: "primary", Type: generationapi.CredentialBearer, Location: generationapi.CredentialLocationHeader, Name: "Authorization"}
	manifest := newRuntimeManifest(t, "/items", nil, nil, generationapi.AuthSpec{Mode: generationapi.AuthRequired, Credentials: []generationapi.CredentialSpec{credential}}, nil)
	rawFailure := errors.New("provider raw secret: must-not-leak")
	tests := []struct {
		name       string
		makeCtx    func() (context.Context, context.CancelFunc)
		provider   func(context.Context, string) ([]CredentialValue, error)
		wantCode   string
		wantReason string
		category   protocol.Category
	}{
		{
			name:    "error",
			makeCtx: func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			provider: func(context.Context, string) ([]CredentialValue, error) {
				return nil, rawFailure
			},
			wantCode: "credential_provider_error", wantReason: "provider_failed", category: protocol.CategoryExternal,
		},
		{
			name:    "panic",
			makeCtx: func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			provider: func(context.Context, string) ([]CredentialValue, error) {
				panic("provider panic secret: must-not-leak")
			},
			wantCode: "credential_provider_error", wantReason: "provider_failed", category: protocol.CategoryExternal,
		},
		{
			name:     "provider cancellation wins over raw error",
			makeCtx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			provider: func(context.Context, string) ([]CredentialValue, error) { return nil, rawFailure },
			wantCode: "operation_canceled", wantReason: "context_canceled", category: protocol.CategoryCanceled,
		},
		{
			name: "provider observes deadline",
			makeCtx: func() (context.Context, context.CancelFunc) {
				return newProviderDeadlineContext(), func() {}
			},
			provider: func(ctx context.Context, _ string) ([]CredentialValue, error) {
				ctx.(*providerDeadlineContext).expire()
				return nil, ctx.Err()
			},
			wantCode: "operation_canceled", wantReason: "context_deadline", category: protocol.CategoryCanceled,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := test.makeCtx()
			defer cancel()
			providerCalls := 0
			providerFn := test.provider
			if test.name == "provider cancellation wins over raw error" {
				providerFn = func(context.Context, string) ([]CredentialValue, error) {
					cancel()
					return nil, rawFailure
				}
			}
			provider := CredentialProviderFunc(func(ctx context.Context, operationID string) ([]CredentialValue, error) {
				providerCalls++
				return providerFn(ctx, operationID)
			})
			client, transportCalls := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test"}, provider)
			_, err := buildRuntimeWire(ctx, client, provider, `{}`)
			apiError := requireClientFailure(t, err, sdkFailure{code: test.wantCode, category: test.category, reason: test.wantReason, apiOperationID: "sample.call"})
			assertSDKProjectionOmits(t, apiError, "must-not-leak", "provider raw secret", "provider panic secret")
			if errors.Is(apiError, rawFailure) {
				t.Fatal("provider raw error leaked through unwrap")
			}
			if providerCalls != 1 || transportCalls.Load() != 0 {
				t.Fatalf("calls = provider %d transport %d, want 1/0", providerCalls, transportCalls.Load())
			}
		})
	}
}

func TestSecretValuesNeverReachSDKErrorProjection(t *testing.T) {
	credential := generationapi.CredentialSpec{ID: "primary", Type: generationapi.CredentialBearer, Location: generationapi.CredentialLocationHeader, Name: "Authorization"}
	manifest := newRuntimeManifest(t, "/items", nil, nil, generationapi.AuthSpec{Mode: generationapi.AuthRequired, Credentials: []generationapi.CredentialSpec{credential}}, nil)
	provider := NewStaticCredentialProvider([]CredentialValue{{ID: "secret-credential-id", Value: "secret-credential-value"}})
	client, _ := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test"}, provider)
	_, err := buildRuntimeWire(context.Background(), client, provider, `{}`)
	apiError := requireClientFailure(t, err, sdkFailure{code: "request_invalid", category: protocol.CategoryInput, reason: "credential_id_unknown", pointer: "/credentials", apiOperationID: "sample.call"})
	assertSDKProjectionOmits(t, apiError, "secret-credential-id", "secret-credential-value")

	options := validClientOptions(t)
	options.Endpoint = &url.URL{Scheme: "https", Host: "api.example.test", RawQuery: "token=secret-endpoint-query"}
	_, err = New(options)
	endpointError := requireClientFailure(t, err, sdkFailure{code: "client_invalid", category: protocol.CategoryInput, reason: "endpoint_invalid", pointer: "/endpoint"})
	assertSDKProjectionOmits(t, endpointError, "secret-endpoint-query")
}

func TestRequestConformanceCorpusBuildsCanonicalLogicalWire(t *testing.T) {
	corpus := loadRuntimeCorpusDocument(t)
	if len(corpus.WireRequests) == 0 {
		t.Fatal("shared corpus contains no wire request vectors")
	}
	manifest, err := generationapi.Parse("runtime-api-v1.json#manifest", []byte(corpus.Manifest))
	if err != nil {
		t.Fatal(err)
	}
	for _, vector := range corpus.WireRequests {
		t.Run(vector.Name, func(t *testing.T) {
			endpoint, parseErr := url.Parse(vector.Endpoint)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			credentials := make([]CredentialValue, len(vector.Credentials))
			for index, credential := range vector.Credentials {
				credentials[index] = CredentialValue{ID: credential.ID, Value: credential.Value}
			}
			provider := NewStaticCredentialProvider(credentials)
			client, transportCalls := newRuntimeClient(t, manifest, endpoint, provider)
			request, parseErr := ParseRequest([]byte(vector.Request))
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			wire, buildErr := client.buildWireRequest(context.Background(), vector.APIOperationID, request, requestBuildOptions{credentialProvider: provider})
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			gotURL := wire.URL()
			if string(wire.Method()) != vector.Expected.Method || gotURL.Path != vector.Expected.Path || gotURL.RawPath != vector.Expected.RawPath || gotURL.EscapedPath() != vector.Expected.EscapedPath || gotURL.RequestURI() != vector.Expected.RequestURI {
				t.Fatalf("wire method/URL = %s %#v", wire.Method(), gotURL)
			}
			wantHeaders := runtimeAdapterHeadersToHeaders(vector.Expected.Headers)
			if !reflect.DeepEqual(wire.Headers(), wantHeaders) {
				t.Fatalf("headers = %#v, want %#v", wire.Headers(), wantHeaders)
			}
			if vector.Expected.Body == nil {
				if wire.Body() != nil {
					t.Fatalf("body = %q, want nil", wire.Body())
				}
			} else if string(wire.Body()) != *vector.Expected.Body {
				t.Fatalf("body = %q, want %q", wire.Body(), *vector.Expected.Body)
			}
			if transportCalls.Load() != 0 {
				t.Fatalf("transport calls = %d, want 0", transportCalls.Load())
			}
		})
	}
}

func TestConcurrentWireBuildsDoNotShareMutableState(t *testing.T) {
	manifest := newRuntimeManifest(t, "/items/{path}", []generationapi.FieldSpec{
		{Name: "path", SchemaRef: "scalar.string", Required: true},
		{Name: "query", SchemaRef: "scalar.string", Required: true},
		{Name: "body", SchemaRef: "scalar.number", Required: true},
	}, []generationapi.RequestBindingSpec{
		{Field: "path", Location: generationapi.RequestBindingPath, Name: "path"},
		{Field: "query", Location: generationapi.RequestBindingQuery, Name: "query"},
		{Field: "body", Location: generationapi.RequestBindingBody, Name: "body"},
	}, generationapi.AuthSpec{Mode: generationapi.AuthRequired, Credentials: []generationapi.CredentialSpec{
		{ID: "primary", Type: generationapi.CredentialBearer, Location: generationapi.CredentialLocationHeader, Name: "Authorization"},
	}}, nil)
	provider := NewStaticCredentialProvider([]CredentialValue{{ID: "primary", Value: "token"}})
	client, transportCalls := newRuntimeClient(t, manifest, &url.URL{Scheme: "https", Host: "api.example.test", Path: "/api"}, provider)
	request, err := ParseRequest([]byte(`{"path":"a b","query":"x/y","body":1e0}`))
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 16
	const iterations = 25
	failures := make(chan error, goroutines)
	var group sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				wire, buildErr := client.buildWireRequest(context.Background(), "sample.call", request, requestBuildOptions{credentialProvider: provider})
				if buildErr != nil {
					failures <- buildErr
					return
				}
				if got := wire.URL().RequestURI(); got != "/api/items/a%20b?query=x%2Fy" {
					failures <- fmt.Errorf("RequestURI = %q", got)
					return
				}
				if !reflect.DeepEqual(wire.Headers(), []Header{{Name: "authorization", Value: "Bearer token"}, {Name: generationapi.RequestContentTypeHeader, Value: generationapi.RequestJSONMediaType}}) || string(wire.Body()) != `{"body":1}` {
					failures <- fmt.Errorf("wire values = headers %#v body %q", wire.Headers(), wire.Body())
					return
				}
				u := wire.URL()
				u.Path = "/mutated"
				u.RawPath = "/mutated"
				headers := wire.Headers()
				headers[0].Value = "mutated"
				body := wire.Body()
				body[0] = 'X'
			}
		}()
	}
	group.Wait()
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
	if transportCalls.Load() != 0 {
		t.Fatalf("transport calls = %d, want 0", transportCalls.Load())
	}
}

type sdkFailure struct {
	code, reason, pointer, apiOperationID string
	category                              protocol.Category
}

func requireClientFailure(t *testing.T, err error, want sdkFailure) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("operation succeeded, want %s/%s at %q", want.code, want.reason, want.pointer)
	}
	var apiError *Error
	if !errors.As(err, &apiError) || apiError == nil {
		t.Fatalf("error = %T %v, want *api.Error", err, err)
	}
	if apiError.Code() != want.code || apiError.Domain() != sdkErrorDomain || apiError.Category() != want.category || apiError.Retryable() {
		t.Fatalf("error identity = (%q, %q, %q, %t), want (%q, %q, %q, false)", apiError.Code(), apiError.Domain(), apiError.Category(), apiError.Retryable(), want.code, sdkErrorDomain, want.category)
	}
	if apiError.Details().Reason() != want.reason || apiError.Details().Pointer() != want.pointer {
		t.Fatalf("error details = (%q, %q), want (%q, %q)", apiError.Details().Reason(), apiError.Details().Pointer(), want.reason, want.pointer)
	}
	if apiError.APIOperationID() != want.apiOperationID || apiError.RequestID() != "" || apiError.TraceID() != "" {
		t.Fatalf("error context = operation %q, request %q, trace %q", apiError.APIOperationID(), apiError.RequestID(), apiError.TraceID())
	}
	if apiError.Error() == "" || !errors.Is(apiError, errSDKAPI) || apiError.Unwrap() == nil || errors.Unwrap(apiError.Unwrap()) != nil {
		t.Fatalf("error does not expose exactly the stable SDK sentinel: %#v", apiError)
	}
	return apiError
}

func validClientOptions(t *testing.T) Options {
	t.Helper()
	manifest, err := generationapi.NewManifest(generationapi.ManifestSpec{})
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		Manifest: manifest,
		Endpoint: &url.URL{Scheme: "https", Host: "api.example.test", Path: "/api"},
		Transport: TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
			t.Fatal("pure request construction must not invoke Transport.RoundTrip")
			return WireResponse{}, nil
		}),
		MaxResponseBytes: RuntimeLimits().ResponseBytesMin,
	}
}

func newRuntimeManifest(
	t *testing.T,
	path string,
	fields []generationapi.FieldSpec,
	bindings []generationapi.RequestBindingSpec,
	auth generationapi.AuthSpec,
	nestedSchemas []generationapi.SchemaSpec,
) generationapi.Manifest {
	t.Helper()
	requestRef := mustRuntimeRef(t, "repo:sdk/api/testdata/runtime.api#SampleRequest")
	operationRef := mustRuntimeRef(t, "repo:sdk/api/testdata/runtime.api#SampleCall")
	requestFields := append([]generationapi.FieldSpec(nil), fields...)
	for index := range requestFields {
		if requestFields[index].Provenance.Kind == "" {
			requestFields[index].Provenance = generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{requestRef}}
		}
	}
	schemas := []generationapi.SchemaSpec{
		{ID: "sample.request", Kind: generationapi.SchemaObject, Provenance: &generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{requestRef}}, Fields: requestFields},
		{ID: "scalar.string", Kind: generationapi.SchemaString},
		{ID: "scalar.integer", Kind: generationapi.SchemaInteger},
		{ID: "scalar.number", Kind: generationapi.SchemaNumber},
		{ID: "scalar.boolean", Kind: generationapi.SchemaBoolean},
	}
	sourceRefs := []provenance.SourceRef{requestRef, operationRef}
	for _, schema := range nestedSchemas {
		if (schema.Kind == generationapi.SchemaObject || schema.Kind == generationapi.SchemaArray) && schema.Provenance == nil {
			ref := mustRuntimeRef(t, "repo:sdk/api/testdata/runtime.api#"+schema.ID)
			schema.Provenance = &generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}}
			sourceRefs = append(sourceRefs, ref)
			for index := range schema.Fields {
				if schema.Fields[index].Provenance.Kind == "" {
					schema.Fields[index].Provenance = generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}}
				}
			}
		}
		schemas = append(schemas, schema)
	}
	sources := make([]provenance.Source, len(sourceRefs))
	for index, ref := range sourceRefs {
		sources[index] = provenance.Source{Ref: ref, Digest: provenance.SHA256([]byte(ref.String()))}
	}
	manifest, err := generationapi.NewManifest(generationapi.ManifestSpec{
		Sources: sources,
		Schemas: schemas,
		Operations: []generationapi.OperationSpec{{
			ID:               "sample.call",
			Method:           generationapi.MethodPOST,
			Path:             path,
			Provenance:       generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{operationRef}},
			RequestSchemaRef: "sample.request",
			ResponseBody:     generationapi.ResponseBodyNone,
			RequestBindings:  append([]generationapi.RequestBindingSpec(nil), bindings...),
			Auth:             auth,
			ErrorProjections: []generationapi.ErrorProjectionSpec{},
		}},
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	return manifest
}

func mustRuntimeRef(t *testing.T, value string) provenance.SourceRef {
	t.Helper()
	ref, err := provenance.ParseSourceRef(value)
	if err != nil {
		t.Fatalf("ParseSourceRef(%q): %v", value, err)
	}
	return ref
}

func newRuntimeClient(t *testing.T, manifest generationapi.Manifest, endpoint *url.URL, provider CredentialProvider) (*Client, *atomic.Int64) {
	t.Helper()
	transportCalls := &atomic.Int64{}
	client, err := New(Options{
		Manifest: manifest,
		Endpoint: endpoint,
		Transport: TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
			transportCalls.Add(1)
			return WireResponse{}, errors.New("pure request construction invoked Transport.RoundTrip")
		}),
		CredentialProvider: provider,
		MaxResponseBytes:   RuntimeLimits().ResponseBytesMin,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client, transportCalls
}

func buildRuntimeWire(ctx context.Context, client *Client, provider CredentialProvider, requestJSON string) (WireRequest, error) {
	request, err := ParseRequest([]byte(requestJSON))
	if err != nil {
		return WireRequest{}, err
	}
	return client.buildWireRequest(ctx, "sample.call", request, requestBuildOptions{credentialProvider: provider})
}

func scalarBindingManifest(t *testing.T, kind generationapi.SchemaKind, location generationapi.RequestBindingLocation) generationapi.Manifest {
	t.Helper()
	schemaRef := map[generationapi.SchemaKind]string{
		generationapi.SchemaInteger: "scalar.integer",
		generationapi.SchemaNumber:  "scalar.number",
	}[kind]
	if schemaRef == "" {
		t.Fatalf("unsupported numeric schema kind %q", kind)
	}
	path := "/values"
	name := "value"
	if location == generationapi.RequestBindingPath {
		path += "/{value}"
	}
	if location == generationapi.RequestBindingHeader {
		name = "X-Value"
	}
	return newRuntimeManifest(t, path, []generationapi.FieldSpec{
		{Name: "value", SchemaRef: schemaRef, Required: true},
	}, []generationapi.RequestBindingSpec{
		{Field: "value", Location: location, Name: name},
	}, generationapi.AuthSpec{Mode: generationapi.AuthNone}, nil)
}

func rfc3986Encode(value string) string {
	const hex = "0123456789ABCDEF"
	var result strings.Builder
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-._~", rune(character)) {
			result.WriteByte(character)
			continue
		}
		result.WriteByte('%')
		result.WriteByte(hex[character>>4])
		result.WriteByte(hex[character&0x0f])
	}
	return result.String()
}

func assertSDKProjectionOmits(t *testing.T, apiError *Error, values ...string) {
	t.Helper()
	projection := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n%t\n%s\n%s\n%s\n%#v\n%v",
		apiError.Error(), apiError.Code(), apiError.Domain(), apiError.Category(), apiError.Retryable(),
		apiError.APIOperationID(), apiError.RequestID(), apiError.TraceID(), apiError.Details(), apiError.Unwrap(),
	)
	for _, value := range values {
		if value != "" && strings.Contains(projection, value) {
			t.Fatalf("SDK error projection leaked %q: %s", value, projection)
		}
	}
}

type providerDeadlineContext struct {
	context.Context
	done    chan struct{}
	expired atomic.Bool
	once    sync.Once
}

func newProviderDeadlineContext() *providerDeadlineContext {
	return &providerDeadlineContext{Context: context.Background(), done: make(chan struct{})}
}

func (c *providerDeadlineContext) Done() <-chan struct{} { return c.done }

func (c *providerDeadlineContext) Err() error {
	if c.expired.Load() {
		return context.DeadlineExceeded
	}
	return nil
}

func (c *providerDeadlineContext) expire() {
	c.expired.Store(true)
	c.once.Do(func() { close(c.done) })
}

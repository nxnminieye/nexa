package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	generationapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

type trackedResponseBody struct {
	reader     io.Reader
	closeCalls atomic.Int64
	panicClose bool
}

func newTrackedResponseBody(value string) *trackedResponseBody {
	return &trackedResponseBody{reader: strings.NewReader(value)}
}

func (b *trackedResponseBody) Read(target []byte) (int, error) {
	return b.reader.Read(target)
}

func (b *trackedResponseBody) Close() error {
	b.closeCalls.Add(1)
	if b.panicClose {
		panic("close detail must not escape")
	}
	return errors.New("close detail must not escape")
}

func TestWireResponseConstructorContract(t *testing.T) {
	for _, status := range []int{200, 599} {
		body := newTrackedResponseBody("unused")
		response, err := NewWireResponse(status, nil, body)
		if err != nil {
			t.Fatalf("NewWireResponse(%d) error = %v", status, err)
		}
		if response.StatusCode() != status {
			t.Fatalf("response status = %d", response.StatusCode())
		}
	}

	for _, status := range []int{-1, 0, 99, 100, 199, 600, 999} {
		body := newTrackedResponseBody("unused")
		_, err := NewWireResponse(status, nil, body)
		apiError := requireResponseFailure(t, err, "transport response is invalid", "response_status_invalid", "/statusCode", 0)
		if apiError.APIOperationID() != "" || body.closeCalls.Load() != 0 {
			t.Fatalf("status %d constructor changed ownership", status)
		}
	}

	valid := []Header{
		{Name: "x-z", Value: "z"},
		{Name: "content-type", Value: "application/json"},
		{Name: "x-z", Value: "a"},
	}
	body := newTrackedResponseBody("unused")
	response, err := NewWireResponse(204, valid, body)
	if err != nil {
		t.Fatal(err)
	}
	want := []Header{
		{Name: "content-type", Value: "application/json"},
		{Name: "x-z", Value: "a"},
		{Name: "x-z", Value: "z"},
	}
	if got := response.Headers(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Headers() = %#v, want %#v", got, want)
	}
	valid[0].Value = "mutated"
	copyOfHeaders := response.Headers()
	copyOfHeaders[0].Value = "mutated"
	if got := response.Headers(); !reflect.DeepEqual(got, want) {
		t.Fatalf("header mutation escaped: %#v", got)
	}

	invalidHeaders := []struct {
		name    string
		headers []Header
		reason  string
		pointer string
	}{
		{name: "empty name", headers: []Header{{Name: "", Value: "ok"}}, reason: "response_header_name_invalid", pointer: "/headers/0/name"},
		{name: "uppercase name", headers: []Header{{Name: "x-ok", Value: "ok"}, {Name: "X-Bad", Value: "ok"}}, reason: "response_header_name_invalid", pointer: "/headers/1/name"},
		{name: "separator name", headers: []Header{{Name: "bad:name", Value: "ok"}}, reason: "response_header_name_invalid", pointer: "/headers/0/name"},
		{name: "non ascii name", headers: []Header{{Name: "x-\u4e2d", Value: "ok"}}, reason: "response_header_name_invalid", pointer: "/headers/0/name"},
		{name: "newline value", headers: []Header{{Name: "x-ok", Value: "ok\n"}}, reason: "response_header_value_invalid", pointer: "/headers/0/value"},
		{name: "del value", headers: []Header{{Name: "x-ok", Value: string([]byte{0x7f})}}, reason: "response_header_value_invalid", pointer: "/headers/0/value"},
		{name: "caller order", headers: []Header{{Name: "z-bad", Value: "bad\n"}, {Name: "A-bad", Value: "ok"}}, reason: "response_header_value_invalid", pointer: "/headers/0/value"},
	}
	for _, vector := range invalidHeaders {
		t.Run(vector.name, func(t *testing.T) {
			candidate := newTrackedResponseBody("unused")
			_, err := NewWireResponse(299, vector.headers, candidate)
			requireResponseFailure(t, err, "transport response is invalid", vector.reason, vector.pointer, 299)
			if candidate.closeCalls.Load() != 0 {
				t.Fatalf("constructor failure closed body %d times", candidate.closeCalls.Load())
			}
		})
	}

	var nilBody *trackedResponseBody
	_, err = NewWireResponse(200, nil, nilBody)
	requireResponseFailure(t, err, "transport response is invalid", "response_body_required", "/body", 200)
}

func TestWireResponseHeaderByteDomains(t *testing.T) {
	for value := 0; value <= 0xff; value++ {
		character := byte(value)
		allowedName := character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character))
		t.Run(fmt.Sprintf("name-%02x", value), func(t *testing.T) {
			body := newTrackedResponseBody("unused")
			response, err := NewWireResponse(200, []Header{{Name: string([]byte{character}), Value: "ok"}}, body)
			if allowedName {
				if err != nil {
					t.Fatalf("allowed header-name byte rejected: %v", err)
				}
				response.close()
				return
			}
			requireResponseFailure(t, err, "transport response is invalid", "response_header_name_invalid", "/headers/0/name", 200)
			closeRecoverSafe(body)
		})

		allowedValue := character == '\t' || character >= 0x20 && character <= 0x7e
		t.Run(fmt.Sprintf("value-%02x", value), func(t *testing.T) {
			body := newTrackedResponseBody("unused")
			response, err := NewWireResponse(200, []Header{{Name: "x", Value: string([]byte{character})}}, body)
			if allowedValue {
				if err != nil {
					t.Fatalf("allowed header-value byte rejected: %v", err)
				}
				response.close()
				return
			}
			requireResponseFailure(t, err, "transport response is invalid", "response_header_value_invalid", "/headers/0/value", 200)
			closeRecoverSafe(body)
		})
	}
}

func TestCallOptionValidationAndPerCallIsolation(t *testing.T) {
	manifest := responseTestManifest(t, generationapi.ResponseBodyNone)
	request, err := ParseRequest([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	providerCalls := &atomic.Int64{}
	defaultProvider := CredentialProviderFunc(func(context.Context, string) ([]CredentialValue, error) {
		providerCalls.Add(1)
		return []CredentialValue{{ID: "unexpected", Value: "secret"}}, nil
	})
	transportCalls := &atomic.Int64{}
	transport := TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
		transportCalls.Add(1)
		return NewWireResponse(204, nil, newTrackedResponseBody("must not be read"))
	})
	client := responseTestClient(t, manifest, transport, defaultProvider, RuntimeLimits().ResponseBytesMin)

	limitOption := WithMaxResponseBytes(RuntimeLimits().ResponseBytesMax)
	providerOptions := []CallOption{
		WithCredentialProvider(nil),
		WithCredentialProvider(CredentialProviderFunc(nil)),
	}
	for _, providerOption := range providerOptions {
		result, err := client.Call(context.Background(), "sample.call", request, providerOption, limitOption)
		if err != nil {
			t.Fatalf("Call() error = %v", err)
		}
		if result.APIOperationID() != "sample.call" || result.HTTPStatus() != 204 || result.ResponseBody() != generationapi.ResponseBodyNone {
			t.Fatalf("Result = (%q, %d, %q)", result.APIOperationID(), result.HTTPStatus(), result.ResponseBody())
		}
		if body, ok := result.JSON(); ok || body != nil {
			t.Fatalf("none Result JSON = %q, %t", body, ok)
		}
	}
	if providerCalls.Load() != 0 || transportCalls.Load() != 2 {
		t.Fatalf("calls = provider %d transport %d", providerCalls.Load(), transportCalls.Load())
	}

	_, err = client.Call(context.Background(), "sample.call", request)
	requireResponseFailureWithCode(t, err, codeRequestInvalid, "API request is invalid", "credential_count_invalid", "/credentials", 0, "sample.call")
	if providerCalls.Load() != 1 || transportCalls.Load() != 2 {
		t.Fatalf("default call isolation = provider %d transport %d", providerCalls.Load(), transportCalls.Load())
	}

	var nilOption *callOption
	invalidOptions := []struct {
		name    string
		options []CallOption
		reason  string
		pointer string
	}{
		{name: "nil", options: []CallOption{nil}, reason: "call_option_nil", pointer: "/options/0"},
		{name: "typed nil", options: []CallOption{nilOption}, reason: "call_option_nil", pointer: "/options/0"},
		{name: "non-value internal option", options: []CallOption{&callOption{kind: callOptionCredentialProvider}}, reason: "call_option_invalid", pointer: "/options/0"},
		{name: "unknown", options: []CallOption{callOption{kind: callOptionKind(99)}}, reason: "call_option_invalid", pointer: "/options/0"},
		{name: "duplicate provider", options: []CallOption{WithCredentialProvider(nil), WithCredentialProvider(nil)}, reason: "call_option_duplicate", pointer: "/options/1"},
		{name: "duplicate limit", options: []CallOption{WithMaxResponseBytes(1), WithMaxResponseBytes(2)}, reason: "call_option_duplicate", pointer: "/options/1"},
		{name: "limit below", options: []CallOption{WithMaxResponseBytes(RuntimeLimits().ResponseBytesMin - 1)}, reason: "max_response_bytes_invalid", pointer: "/options/0"},
		{name: "limit above", options: []CallOption{WithMaxResponseBytes(RuntimeLimits().ResponseBytesMax + 1)}, reason: "max_response_bytes_invalid", pointer: "/options/0"},
	}
	for _, vector := range invalidOptions {
		t.Run(vector.name, func(t *testing.T) {
			_, err := client.Call(context.Background(), "sample.call", request, vector.options...)
			requireResponseFailureWithCode(t, err, codeClientInvalid, "API call configuration is invalid", vector.reason, vector.pointer, 0, "")
		})
	}

	var nilClient *Client
	_, err = nilClient.Call(context.Background(), "sample.call", request)
	requireResponseFailureWithCode(t, err, codeClientInvalid, "API call configuration is invalid", "client_nil", "", 0, "")
	_, err = client.Call(nil, "sample.call", request)
	requireResponseFailureWithCode(t, err, codeClientInvalid, "API call configuration is invalid", "context_required", "/context", 0, "")

	empty, err := generationapi.NewManifest(generationapi.ManifestSpec{})
	if err != nil {
		t.Fatal(err)
	}
	emptyClient := responseTestClient(t, empty, TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
		t.Fatal("unknown operation reached transport")
		return WireResponse{}, nil
	}), nil, RuntimeLimits().ResponseBytesMax)
	_, err = emptyClient.Call(context.Background(), "must-not-project", request)
	apiError := requireResponseFailureWithCode(t, err, codeOperationNotFound, "API operation was not found", "operation_unknown", "/apiOperationId", 0, "")
	assertSDKProjectionOmits(t, apiError, "must-not-project")
}

func TestResponseOwnershipIsCloseOnceAndRecoverSafe(t *testing.T) {
	manifest := responseTestManifest(t, generationapi.ResponseBodyNone)
	request, err := ParseRequest([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	for _, vector := range []struct {
		name         string
		transportErr error
		wantCode     string
	}{
		{name: "success", wantCode: ""},
		{name: "response and error", transportErr: errors.New("raw transport secret"), wantCode: codeTransportError},
	} {
		t.Run(vector.name, func(t *testing.T) {
			body := newTrackedResponseBody("must not be read")
			body.panicClose = true
			response, constructErr := NewWireResponse(204, nil, body)
			if constructErr != nil {
				t.Fatal(constructErr)
			}
			client := responseTestClient(t, manifest, TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
				return response, vector.transportErr
			}), nil, RuntimeLimits().ResponseBytesMin)
			_, callErr := client.Call(context.Background(), "sample.call", request)
			if vector.wantCode == "" {
				if callErr != nil {
					t.Fatalf("Call() error = %v", callErr)
				}
			} else {
				apiError := requireResponseFailureWithCode(t, callErr, vector.wantCode, "API transport failed", "round_trip_failed", "", 0, "sample.call")
				assertSDKProjectionOmits(t, apiError, "raw transport secret", "close detail")
			}
			if body.closeCalls.Load() != 1 {
				t.Fatalf("Close calls = %d, want 1", body.closeCalls.Load())
			}
		})
	}

	client := responseTestClient(t, manifest, TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
		return WireResponse{}, nil
	}), nil, RuntimeLimits().ResponseBytesMin)
	_, err = client.Call(context.Background(), "sample.call", request)
	requireResponseFailureWithCode(t, err, codeTransportError, "transport response is invalid", "response_invalid", "", 0, "sample.call")
}

func TestResponseOwnershipPanicCloseCannotReplaceProtocolCancelOrAdapterErrors(t *testing.T) {
	request, err := ParseRequest([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("protocol", func(t *testing.T) {
		body := newTrackedResponseBody(`{"displayName":"Sample"}`)
		body.panicClose = true
		response, err := NewWireResponse(200, nil, body)
		if err != nil {
			t.Fatal(err)
		}
		client := responseTestClient(t, responseTestManifest(t, generationapi.ResponseBodyJSON), TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
			return response, nil
		}), nil, RuntimeLimits().ResponseBytesMax)
		_, err = client.Call(context.Background(), "sample.call", request)
		requireResponseFailureWithCode(t, err, codeRemoteProtocolError, "API response is invalid", "response_content_type_missing", "/headers/content-type", 200, "sample.call")
		if body.closeCalls.Load() != 1 {
			t.Fatalf("Close calls = %d", body.closeCalls.Load())
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		body := newTrackedResponseBody("unused")
		body.panicClose = true
		client := responseTestClient(t, responseTestManifest(t, generationapi.ResponseBodyNone), TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
			response, err := NewWireResponse(204, nil, body)
			cancel()
			return response, err
		}), nil, RuntimeLimits().ResponseBytesMin)
		_, err := client.Call(ctx, "sample.call", request)
		requireResponseFailureWithCode(t, err, codeOperationCanceled, "API operation was canceled", "context_canceled", "", 204, "sample.call")
		if body.closeCalls.Load() != 1 {
			t.Fatalf("Close calls = %d", body.closeCalls.Load())
		}
	})

	t.Run("adapter conversion", func(t *testing.T) {
		body := newTrackedResponseBody("unused")
		body.panicClose = true
		adapter, err := NewHTTPTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 199, Body: body}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		_, err = adapter.RoundTrip(context.Background(), WireRequest{method: generationapi.MethodGET, target: url.URL{Scheme: "https", Host: "api.example.test"}})
		requireResponseFailure(t, err, "transport response is invalid", "response_status_invalid", "/statusCode", 0)
		if body.closeCalls.Load() != 1 {
			t.Fatalf("Close calls = %d", body.closeCalls.Load())
		}
	})
}

func TestCallOptionsAreConcurrentlyReusable(t *testing.T) {
	manifest := responseTestManifest(t, generationapi.ResponseBodyNone)
	request, err := ParseRequest([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	client := responseTestClient(t, manifest, TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
		return NewWireResponse(204, nil, newTrackedResponseBody("unused"))
	}), nil, RuntimeLimits().ResponseBytesMin)
	providerOption := WithCredentialProvider(nil)
	limitOption := WithMaxResponseBytes(RuntimeLimits().ResponseBytesMax)

	var group sync.WaitGroup
	failures := make(chan error, 32)
	for index := 0; index < 32; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := client.Call(context.Background(), "sample.call", request, providerOption, limitOption)
			if err != nil || result.HTTPStatus() != 204 {
				failures <- errors.New("concurrent Call did not preserve option state")
			}
		}()
	}
	group.Wait()
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
}

func responseTestManifest(t *testing.T, mode generationapi.ResponseBodyMode) generationapi.Manifest {
	t.Helper()
	requestRef := mustResponseRef(t, "repo:sdk/api/testdata/runtime.api#type:SampleRequest")
	responseRef := mustResponseRef(t, "repo:sdk/api/testdata/runtime.api#type:SampleResponse")
	fieldRef := mustResponseRef(t, "repo:sdk/api/testdata/runtime.api#field:SampleResponse.displayName")
	operationRef := mustResponseRef(t, "repo:sdk/api/testdata/runtime.api#route:SampleCall")
	sources := []provenance.Source{
		{Ref: requestRef, Digest: provenance.SHA256([]byte("request owner"))},
		{Ref: operationRef, Digest: provenance.SHA256([]byte("operation owner"))},
	}
	schemas := []generationapi.SchemaSpec{
		{ID: "sample.request", Kind: generationapi.SchemaObject, Provenance: provenancePointerForResponse(requestRef), Fields: []generationapi.FieldSpec{}},
		{ID: "scalar.string", Kind: generationapi.SchemaString},
	}
	responseSchemaRef := ""
	if mode == generationapi.ResponseBodyJSON {
		sources = append(sources,
			provenance.Source{Ref: responseRef, Digest: provenance.SHA256([]byte("response owner"))},
			provenance.Source{Ref: fieldRef, Digest: provenance.SHA256([]byte("field owner"))},
		)
		schemas = append(schemas, generationapi.SchemaSpec{
			ID: "sample.response", Kind: generationapi.SchemaObject,
			Provenance: provenancePointerForResponse(responseRef),
			Fields: []generationapi.FieldSpec{{
				Name: "displayName", SchemaRef: "scalar.string", Required: true,
				Provenance: generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{fieldRef}},
			}},
		})
		responseSchemaRef = "sample.response"
	}
	manifest, err := generationapi.NewManifest(generationapi.ManifestSpec{
		Sources: sources,
		Schemas: schemas,
		Operations: []generationapi.OperationSpec{{
			ID: "sample.call", Method: generationapi.MethodGET, Path: "/sample",
			Provenance:       generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{operationRef}},
			RequestSchemaRef: "sample.request", ResponseBody: mode, ResponseSchemaRef: responseSchemaRef,
			RequestBindings: []generationapi.RequestBindingSpec{}, Auth: generationapi.AuthSpec{Mode: generationapi.AuthNone},
			ErrorProjections: []generationapi.ErrorProjectionSpec{{
				Match:   generationapi.ErrorMatchSpec{Domain: "sample", Code: "not_found"},
				Project: generationapi.ErrorTargetSpec{Domain: "api", Code: "sample_not_found", HTTPStatus: 404},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	return manifest
}

func provenancePointerForResponse(ref provenance.SourceRef) *generationapi.NodeProvenanceSpec {
	return &generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}}
}

func mustResponseRef(t *testing.T, value string) provenance.SourceRef {
	t.Helper()
	raw := strings.TrimPrefix(value, "repo:")
	path, fragment, ok := strings.Cut(raw, "#")
	if !ok {
		t.Fatalf("source coordinate %q has no fragment", value)
	}
	ref, err := provenance.RepositoryRef(path, fragment)
	if err != nil {
		t.Fatalf("RepositoryRef(%q, %q): %v", path, fragment, err)
	}
	return ref
}

func responseTestClient(t *testing.T, manifest generationapi.Manifest, transport Transport, provider CredentialProvider, maxBytes int64) *Client {
	t.Helper()
	client, err := New(Options{
		Manifest:           manifest,
		Endpoint:           &url.URL{Scheme: "https", Host: "api.example.test"},
		Transport:          transport,
		CredentialProvider: provider,
		MaxResponseBytes:   maxBytes,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func requireResponseFailure(t *testing.T, err error, message, reason, pointer string, status int) *Error {
	t.Helper()
	return requireResponseFailureWithCode(t, err, codeTransportError, message, reason, pointer, status, "")
}

func requireResponseFailureWithCode(t *testing.T, err error, code, message, reason, pointer string, status int, operationID string) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("operation succeeded, want %s/%s", code, reason)
	}
	var apiError *Error
	if !errors.As(err, &apiError) || apiError == nil {
		t.Fatalf("error = %T %v, want *api.Error", err, err)
	}
	wantCategory := protocol.CategoryExternal
	if code == codeClientInvalid || code == codeOperationNotFound || code == codeRequestInvalid {
		wantCategory = protocol.CategoryInput
	}
	if code == codeOperationCanceled {
		wantCategory = protocol.CategoryCanceled
	}
	if apiError.Code() != code || apiError.Domain() != sdkErrorDomain || apiError.Category() != wantCategory || apiError.Retryable() || apiError.Error() != message {
		t.Fatalf("identity = (%q,%q,%q,%t,%q)", apiError.Code(), apiError.Domain(), apiError.Category(), apiError.Retryable(), apiError.Error())
	}
	if apiError.Details().Reason() != reason || apiError.Details().Pointer() != pointer || apiError.Details().HTTPStatus() != status {
		t.Fatalf("details = (%q,%q,%d), want (%q,%q,%d)", apiError.Details().Reason(), apiError.Details().Pointer(), apiError.Details().HTTPStatus(), reason, pointer, status)
	}
	if apiError.APIOperationID() != operationID || apiError.RequestID() != "" || apiError.TraceID() != "" || apiError.Details().RemoteDomain() != "" || apiError.Details().RemoteCode() != "" {
		t.Fatalf("unexpected context = operation %q request %q trace %q remote %q/%q", apiError.APIOperationID(), apiError.RequestID(), apiError.TraceID(), apiError.Details().RemoteDomain(), apiError.Details().RemoteCode())
	}
	if !errors.Is(apiError, errSDKAPI) || apiError.Unwrap() == nil || errors.Unwrap(apiError.Unwrap()) != nil {
		t.Fatalf("error unwrap = %v", apiError.Unwrap())
	}
	return apiError
}

func TestWireRequestCopiesRemainDefensiveDuringCall(t *testing.T) {
	manifest := responseTestManifest(t, generationapi.ResponseBodyNone)
	request, err := ParseRequest([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	client := responseTestClient(t, manifest, TransportFunc(func(_ context.Context, wire WireRequest) (WireResponse, error) {
		firstURL := wire.URL()
		firstHeaders := wire.Headers()
		firstBody := wire.Body()
		firstURL.Path = "/changed"
		if len(firstHeaders) > 0 {
			firstHeaders[0].Value = "changed"
		}
		if len(firstBody) > 0 {
			firstBody[0] = 'X'
		}
		if wire.URL().Path != "/sample" || !bytes.Equal(wire.Body(), nil) {
			t.Fatal("WireRequest mutation escaped")
		}
		return NewWireResponse(204, nil, newTrackedResponseBody("unused"))
	}), nil, RuntimeLimits().ResponseBytesMin)
	if _, err := client.Call(context.Background(), "sample.call", request); err != nil {
		t.Fatal(err)
	}
}

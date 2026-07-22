package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	generationapi "github.com/nxnminieye/nexa/generation/api"
)

func TestRemoteProjectionMappedUnmappedAndStatusMismatch(t *testing.T) {
	manifest := responseTestManifest(t, generationapi.ResponseBodyJSON)
	vectors := []struct {
		name             string
		status           int
		body             string
		wantCode         string
		wantDomain       string
		wantMessage      string
		wantReason       string
		wantPointer      string
		wantRequestID    string
		wantTraceID      string
		wantRemoteDomain string
		wantRemoteCode   string
	}{
		{
			name: "mapped", status: 404,
			body:     `{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"not_found","message":"missing","requestId":"remote-request","traceId":"remote-trace","details":{"secret":"discard"}}`,
			wantCode: "sample_not_found", wantDomain: "api", wantMessage: "missing",
			wantRequestID: "remote-request", wantTraceID: "remote-trace", wantRemoteDomain: "sample", wantRemoteCode: "not_found",
		},
		{
			name: "status mismatch", status: 500,
			body:     `{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"not_found","message":"missing","requestId":"remote-request","traceId":"remote-trace"}`,
			wantCode: codeRemoteProtocolError, wantDomain: sdkErrorDomain, wantMessage: "remote error status does not match API manifest",
			wantReason: "response_status_mismatch", wantPointer: "/statusCode",
			wantRequestID: "remote-request", wantTraceID: "remote-trace", wantRemoteDomain: "sample", wantRemoteCode: "not_found",
		},
		{
			name: "unmapped", status: 500,
			body:     `{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"other","message":"other","requestId":"remote-request","traceId":"remote-trace"}`,
			wantCode: codeRemoteErrorUnmapped, wantDomain: sdkErrorDomain, wantMessage: "remote API error is not mapped",
			wantReason: "remote_error_unmapped", wantRequestID: "remote-request", wantTraceID: "remote-trace", wantRemoteDomain: "sample", wantRemoteCode: "other",
		},
		{
			name: "status 300", status: 300,
			body:     `{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"other","message":"redirect is remote data"}`,
			wantCode: codeRemoteErrorUnmapped, wantDomain: sdkErrorDomain, wantMessage: "remote API error is not mapped",
			wantReason: "remote_error_unmapped", wantRemoteDomain: "sample", wantRemoteCode: "other",
		},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			body := newTrackedResponseBody(vector.body)
			_, err := callResponse(t, manifest, vector.status, []Header{{Name: "content-type", Value: "application/json"}}, body)
			apiError := requireProjectedResponseError(t, err, vector.wantCode, vector.wantDomain, vector.wantMessage, vector.wantReason, vector.wantPointer, vector.status)
			if apiError.RequestID() != vector.wantRequestID || apiError.TraceID() != vector.wantTraceID ||
				apiError.Details().RemoteDomain() != vector.wantRemoteDomain || apiError.Details().RemoteCode() != vector.wantRemoteCode {
				t.Fatalf("remote context = request %q trace %q remote %q/%q", apiError.RequestID(), apiError.TraceID(), apiError.Details().RemoteDomain(), apiError.Details().RemoteCode())
			}
			assertSDKProjectionOmits(t, apiError, "discard")
			if body.closeCalls.Load() != 1 {
				t.Fatalf("Close calls = %d", body.closeCalls.Load())
			}
		})
	}
}

func TestRemoteProjectionMalformedKeepsOwnerReasonWithoutPartialMetadata(t *testing.T) {
	manifest := responseTestManifest(t, generationapi.ResponseBodyJSON)
	vectors := []struct {
		name, body, reason, pointer string
	}{
		{name: "wrong version", body: `{"apiVersion":"nexa.dev/remote-error/v2","domain":"sample","code":"not_found","message":"missing","requestId":"partial"}`, reason: "version_unsupported", pointer: "/apiVersion"},
		{name: "unknown", body: `{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"not_found","message":"missing","requestId":"partial","secret":"must-not-project"}`, reason: "document_unknown_field", pointer: ""},
		{name: "duplicate", body: `{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","domain":"other","code":"not_found","message":"missing"}`, reason: "duplicate_key", pointer: "/domain"},
		{name: "trailing", body: `{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"not_found","message":"missing"} {}`, reason: "trailing_input", pointer: ""},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			_, err := callResponse(t, manifest, 500, []Header{{Name: "content-type", Value: "application/json"}}, newTrackedResponseBody(vector.body))
			apiError := requireProjectedResponseError(t, err, codeRemoteProtocolError, sdkErrorDomain, "remote error response is invalid", vector.reason, vector.pointer, 500)
			if apiError.RequestID() != "" || apiError.TraceID() != "" || apiError.Details().RemoteDomain() != "" || apiError.Details().RemoteCode() != "" {
				t.Fatalf("malformed response retained partial metadata: %#v", apiError)
			}
			assertSDKProjectionOmits(t, apiError, "partial", "must-not-project")
		})
	}
}

func TestRemoteProjectionAppliesContentTypeAndBodyGatesBeforeOwnerParser(t *testing.T) {
	manifest := responseTestManifest(t, generationapi.ResponseBodyJSON)
	remoteBody := `{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"other","message":"remote"}`
	vectors := []struct {
		name        string
		headers     []Header
		body        string
		maxBytes    int64
		wantReason  string
		wantPointer string
	}{
		{name: "missing content type", body: remoteBody, wantReason: "response_content_type_missing", wantPointer: "/headers/content-type"},
		{name: "duplicate content type", headers: []Header{{Name: "content-type", Value: "application/json"}, {Name: "content-type", Value: "application/json"}}, body: remoteBody, wantReason: "response_content_type_duplicate", wantPointer: "/headers/content-type"},
		{name: "malformed content type", headers: []Header{{Name: "content-type", Value: "application/json; charset"}}, body: remoteBody, wantReason: "response_content_type_malformed", wantPointer: "/headers/content-type"},
		{name: "unsupported content type", headers: []Header{{Name: "content-type", Value: "text/json"}}, body: remoteBody, wantReason: "response_content_type_unsupported", wantPointer: "/headers/content-type"},
		{name: "parameter invalid", headers: []Header{{Name: "content-type", Value: "application/json; charset=latin1"}}, body: remoteBody, wantReason: "response_content_type_parameter_invalid", wantPointer: "/headers/content-type"},
		{name: "empty body", headers: []Header{{Name: "content-type", Value: "application/json"}}, body: "", wantReason: "response_body_empty", wantPointer: "/body"},
		{name: "body too large", headers: []Header{{Name: "content-type", Value: "application/json"}}, body: remoteBody, maxBytes: 1, wantReason: "response_body_too_large", wantPointer: "/body"},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			options := []CallOption(nil)
			if vector.maxBytes != 0 {
				options = append(options, WithMaxResponseBytes(vector.maxBytes))
			}
			_, err := callResponse(t, manifest, 500, vector.headers, newTrackedResponseBody(vector.body), options...)
			apiError := requireProjectedResponseError(t, err, codeRemoteProtocolError, sdkErrorDomain, "API response is invalid", vector.wantReason, vector.wantPointer, 500)
			if apiError.RequestID() != "" || apiError.TraceID() != "" || apiError.Details().RemoteDomain() != "" || apiError.Details().RemoteCode() != "" {
				t.Fatalf("pre-parser failure retained remote metadata: %#v", apiError)
			}
		})
	}
}

func TestTransportFailureProjectionAndPreservePolicy(t *testing.T) {
	manifest := responseTestManifest(t, generationapi.ResponseBodyNone)
	request, err := ParseRequest([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	vectors := []struct {
		name        string
		transport   Transport
		wantMsg     string
		wantReason  string
		wantPointer string
		wantStatus  int
	}{
		{
			name: "external error",
			transport: TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
				return WireResponse{}, errors.New("network path secret")
			}),
			wantMsg: "API transport failed", wantReason: "round_trip_failed",
		},
		{
			name: "external panic",
			transport: TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
				panic("callback secret")
			}),
			wantMsg: "API transport failed", wantReason: "round_trip_failed",
		},
		{
			name: "zero response",
			transport: TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
				return WireResponse{}, nil
			}),
			wantMsg: "transport response is invalid", wantReason: "response_invalid",
		},
		{
			name: "SDK constructor error",
			transport: TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
				response, err := NewWireResponse(600, nil, newTrackedResponseBody("unused"))
				return response, err
			}),
			wantMsg: "transport response is invalid", wantReason: "response_status_invalid", wantPointer: "/statusCode",
		},
		{
			name: "wrapped SDK constructor error is external",
			transport: TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
				_, err := NewWireResponse(600, nil, newTrackedResponseBody("unused"))
				return WireResponse{}, fmt.Errorf("external wrapper: %w", err)
			}),
			wantMsg: "API transport failed", wantReason: "round_trip_failed",
		},
		{
			name: "hostile As error",
			transport: TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
				return WireResponse{}, hostileTransportError{}
			}),
			wantMsg: "API transport failed", wantReason: "round_trip_failed",
		},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			client := responseTestClient(t, manifest, vector.transport, nil, RuntimeLimits().ResponseBytesMin)
			_, err := client.Call(context.Background(), "sample.call", request)
			apiError := requireResponseFailureWithCode(t, err, codeTransportError, vector.wantMsg, vector.wantReason, vector.wantPointer, vector.wantStatus, "sample.call")
			assertSDKProjectionOmits(t, apiError, "network path secret", "callback secret")
		})
	}
}

func TestCancellationBeforeAndImmediatelyAfterTransport(t *testing.T) {
	request, err := ParseRequest([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	preCanceled, cancel := context.WithCancel(context.Background())
	cancel()
	calls := &atomic.Int64{}
	client := responseTestClient(t, responseTestManifest(t, generationapi.ResponseBodyNone), TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
		calls.Add(1)
		return NewWireResponse(204, nil, newTrackedResponseBody("unused"))
	}), nil, RuntimeLimits().ResponseBytesMin)
	_, err = client.Call(preCanceled, "sample.call", request)
	apiError := requireResponseFailureWithCode(t, err, codeOperationCanceled, "API operation was canceled", "context_canceled", "", 0, "sample.call")
	if apiError.Category() != protocol.CategoryCanceled || calls.Load() != 0 {
		t.Fatalf("pre-cancel = category %q calls %d", apiError.Category(), calls.Load())
	}

	for _, vector := range []struct {
		name     string
		mode     generationapi.ResponseBodyMode
		status   int
		zero     bool
		deadline bool
	}{
		{name: "none canceled", mode: generationapi.ResponseBodyNone, status: 204},
		{name: "json deadline", mode: generationapi.ResponseBodyJSON, status: 200, deadline: true},
		{name: "zero canceled", mode: generationapi.ResponseBodyNone, zero: true},
	} {
		t.Run(vector.name, func(t *testing.T) {
			ctx := newMutableErrorContext()
			var body *trackedResponseBody
			client := responseTestClient(t, responseTestManifest(t, vector.mode), TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
				failure := context.Canceled
				if vector.deadline {
					failure = context.DeadlineExceeded
				}
				ctx.fail(failure)
				if vector.zero {
					return WireResponse{}, errors.New("callback raw error")
				}
				body = newTrackedResponseBody(`{"displayName":"Sample"}`)
				return NewWireResponse(vector.status, []Header{{Name: "content-type", Value: "application/json"}}, body)
			}), nil, RuntimeLimits().ResponseBytesMax)
			_, err := client.Call(ctx, "sample.call", request)
			wantReason := "context_canceled"
			if vector.deadline {
				wantReason = "context_deadline"
			}
			wantStatus := vector.status
			if vector.zero {
				wantStatus = 0
			}
			apiError := requireResponseFailureWithCode(t, err, codeOperationCanceled, "API operation was canceled", wantReason, "", wantStatus, "sample.call")
			assertSDKProjectionOmits(t, apiError, "callback raw error")
			if body != nil && body.closeCalls.Load() != 1 {
				t.Fatalf("Close calls = %d", body.closeCalls.Load())
			}
		})
	}
}

type mutableErrorContext struct {
	context.Context
	done chan struct{}
	once sync.Once
	mu   sync.RWMutex
	err  error
}

func newMutableErrorContext() *mutableErrorContext {
	return &mutableErrorContext{Context: context.Background(), done: make(chan struct{})}
}

func (c *mutableErrorContext) Done() <-chan struct{} { return c.done }

func (c *mutableErrorContext) Err() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.err
}

func (c *mutableErrorContext) fail(err error) {
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
	c.once.Do(func() { close(c.done) })
}

type controlledReadBody struct {
	read       func([]byte) (int, error)
	closeCalls atomic.Int64
}

func (b *controlledReadBody) Read(target []byte) (int, error) { return b.read(target) }
func (b *controlledReadBody) Close() error {
	b.closeCalls.Add(1)
	return nil
}

func TestCancellationAndReadFailureProjection(t *testing.T) {
	manifest := responseTestManifest(t, generationapi.ResponseBodyJSON)
	vectors := []struct {
		name       string
		newContext func() (context.Context, func())
		read       func(func()) func([]byte) (int, error)
		wantCode   string
		wantReason string
	}{
		{
			name: "n plus canceled", newContext: cancelableContext,
			read: func(trigger func()) func([]byte) (int, error) {
				return func(target []byte) (int, error) { copy(target, "{"); trigger(); return 1, errors.New("reader secret") }
			},
			wantCode: codeOperationCanceled, wantReason: "context_canceled",
		},
		{
			name: "zero plus deadline", newContext: expirableDeadlineContext,
			read: func(trigger func()) func([]byte) (int, error) {
				return func([]byte) (int, error) { trigger(); return 0, errors.New("reader secret") }
			},
			wantCode: codeOperationCanceled, wantReason: "context_deadline",
		},
		{
			name: "EOF plus canceled", newContext: cancelableContext,
			read: func(trigger func()) func([]byte) (int, error) {
				return func([]byte) (int, error) { trigger(); return 0, io.EOF }
			},
			wantCode: codeOperationCanceled, wantReason: "context_canceled",
		},
		{
			name: "ordinary error", newContext: stableContext,
			read: func(func()) func([]byte) (int, error) {
				return func([]byte) (int, error) { return 0, errors.New("reader secret") }
			},
			wantCode: codeRemoteProtocolError, wantReason: "response_body_read_failed",
		},
		{
			name: "read panic", newContext: stableContext,
			read: func(func()) func([]byte) (int, error) {
				return func([]byte) (int, error) { panic("reader panic secret") }
			},
			wantCode: codeRemoteProtocolError, wantReason: "response_body_read_failed",
		},
		{
			name: "hostile Is error", newContext: stableContext,
			read: func(func()) func([]byte) (int, error) {
				return func([]byte) (int, error) { return 0, hostileReaderError{} }
			},
			wantCode: codeRemoteProtocolError, wantReason: "response_body_read_failed",
		},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			ctx, trigger := vector.newContext()
			body := &controlledReadBody{read: vector.read(trigger)}
			response, err := NewWireResponse(200, []Header{{Name: "content-type", Value: "application/json"}}, body)
			if err != nil {
				t.Fatal(err)
			}
			client := responseTestClient(t, manifest, TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
				return response, nil
			}), nil, RuntimeLimits().ResponseBytesMax)
			request, err := ParseRequest([]byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Call(ctx, "sample.call", request)
			message := "API response is invalid"
			if vector.wantCode == codeOperationCanceled {
				message = "API operation was canceled"
			}
			apiError := requireResponseFailureWithCode(t, err, vector.wantCode, message, vector.wantReason, responseReadPointer(vector.wantCode), 200, "sample.call")
			assertSDKProjectionOmits(t, apiError, "reader secret", "reader panic secret")
			if body.closeCalls.Load() != 1 {
				t.Fatalf("Close calls = %d", body.closeCalls.Load())
			}
		})
	}
}

func TestResponseReadFailurePrecedesSizeFailure(t *testing.T) {
	body := &controlledReadBody{read: func(target []byte) (int, error) {
		copy(target, "xx")
		return 2, errors.New("reader detail")
	}}
	response, err := NewWireResponse(200, []Header{{Name: "content-type", Value: "application/json"}}, body)
	if err != nil {
		t.Fatal(err)
	}
	client := responseTestClient(t, responseTestManifest(t, generationapi.ResponseBodyJSON), TransportFunc(func(context.Context, WireRequest) (WireResponse, error) {
		return response, nil
	}), nil, RuntimeLimits().ResponseBytesMin)
	request, err := ParseRequest([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Call(context.Background(), "sample.call", request)
	apiError := requireResponseFailureWithCode(t, err, codeRemoteProtocolError, "API response is invalid", "response_body_read_failed", "/body", 200, "sample.call")
	assertSDKProjectionOmits(t, apiError, "reader detail")
}

func TestResponseNoProgressReadBoundary(t *testing.T) {
	manifest := responseTestManifest(t, generationapi.ResponseBodyJSON)
	header := []Header{{Name: "content-type", Value: "application/json"}}

	t.Run("continuous limit", func(t *testing.T) {
		calls := 0
		secret := "no-progress reader secret"
		body := &controlledReadBody{read: func([]byte) (int, error) {
			calls++
			_ = secret
			return 0, nil
		}}
		_, err := callResponse(t, manifest, 200, header, body)
		apiError := requireResponseFailureWithCode(t, err, codeRemoteProtocolError, "API response is invalid", "response_body_read_failed", "/body", 200, "sample.call")
		assertSDKProjectionOmits(t, apiError, secret)
		if calls != 100 || body.closeCalls.Load() != 1 {
			t.Fatalf("read/close calls = %d/%d, want 100/1", calls, body.closeCalls.Load())
		}
	})

	t.Run("payload resets boundary", func(t *testing.T) {
		calls := 0
		payload := []byte(`{"displayName":"Sample"}`)
		body := &controlledReadBody{read: func(target []byte) (int, error) {
			calls++
			switch {
			case calls <= 99:
				return 0, nil
			case calls == 100:
				return copy(target, payload), nil
			case calls <= 199:
				return 0, nil
			default:
				return 0, io.EOF
			}
		}}
		result, err := callResponse(t, manifest, 200, header, body)
		if err != nil {
			t.Fatalf("Call() after reset error = %v", err)
		}
		encoded, ok := result.JSON()
		if !ok || string(encoded) != string(payload) {
			t.Fatalf("Result.JSON() = %s, %t", encoded, ok)
		}
		if calls != 200 || body.closeCalls.Load() != 1 {
			t.Fatalf("read/close calls = %d/%d, want 200/1", calls, body.closeCalls.Load())
		}
	})

	t.Run("EOF at boundary", func(t *testing.T) {
		calls := 0
		body := &controlledReadBody{read: func([]byte) (int, error) {
			calls++
			if calls <= 99 {
				return 0, nil
			}
			return 0, io.EOF
		}}
		_, err := callResponse(t, manifest, 200, header, body)
		requireResponseFailureWithCode(t, err, codeRemoteProtocolError, "API response is invalid", "response_body_empty", "/body", 200, "sample.call")
		if calls != 100 || body.closeCalls.Load() != 1 {
			t.Fatalf("read/close calls = %d/%d, want 100/1", calls, body.closeCalls.Load())
		}
	})
}

func responseReadPointer(code string) string {
	if code == codeRemoteProtocolError {
		return "/body"
	}
	return ""
}

func cancelableContext() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	return ctx, cancel
}

func expirableDeadlineContext() (context.Context, func()) {
	ctx := newProviderDeadlineContext()
	return ctx, ctx.expire
}

func stableContext() (context.Context, func()) { return context.Background(), func() {} }

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestHTTPTransportConstructorAndDeterministicHeaders(t *testing.T) {
	for _, roundTripper := range []http.RoundTripper{nil, (*typedNilRoundTripper)(nil)} {
		_, err := NewHTTPTransport(roundTripper)
		requireResponseFailureWithCode(t, err, codeClientInvalid, "HTTP transport configuration is invalid", "round_tripper_required", "/roundTripper", 0, "")
	}

	wire := WireRequest{
		method:  generationapi.MethodPOST,
		target:  url.URL{Scheme: "https", Host: "api.example.test", Path: "/sample", RawQuery: "a=1"},
		headers: []Header{{Name: "x-request", Value: "one"}, {Name: "x-request", Value: "two"}},
		body:    []byte(`{"value":true}`),
	}
	var calls atomic.Int64
	adapter, err := NewHTTPTransport(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.Method != "POST" || request.URL.String() != "https://api.example.test/sample?a=1" || string(readRequestBody(t, request)) != `{"value":true}` {
			t.Fatalf("HTTP request = %s %s body=%q", request.Method, request.URL, readRequestBody(t, request))
		}
		if !reflect.DeepEqual(request.Header.Values("x-request"), []string{"one", "two"}) || request.Header.Get("authorization") != "" {
			t.Fatalf("request headers = %#v", request.Header)
		}
		return &http.Response{
			StatusCode: 200,
			Header: http.Header{
				"X-Z":          {"z", "a"},
				"content-TYPE": {"application/json"},
				"X-Empty":      {},
			},
			Body: newTrackedResponseBody(`{"displayName":"Sample"}`),
		}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.RoundTrip(context.Background(), wire)
	if err != nil {
		t.Fatal(err)
	}
	wantHeaders := []Header{
		{Name: "content-type", Value: "application/json"},
		{Name: "x-z", Value: "a"},
		{Name: "x-z", Value: "z"},
	}
	if calls.Load() != 1 || response.StatusCode() != 200 || !reflect.DeepEqual(response.Headers(), wantHeaders) {
		t.Fatalf("adapter result = calls %d status %d headers %#v", calls.Load(), response.StatusCode(), response.Headers())
	}
	response.close()
}

func TestHTTPTransportCanonicalPointerIndependentOfMapOrder(t *testing.T) {
	makeHeader := func(reverse bool) http.Header {
		header := make(http.Header)
		if reverse {
			header["Z-Good"] = []string{"z", "a"}
			header["A-Bad"] = []string{"ok\n", "ok"}
		} else {
			header["A-Bad"] = []string{"ok", "ok\n"}
			header["Z-Good"] = []string{"a", "z"}
		}
		return header
	}
	for _, reverse := range []bool{false, true} {
		adapter, err := NewHTTPTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: makeHeader(reverse), Body: newTrackedResponseBody("unused")}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		_, err = adapter.RoundTrip(context.Background(), WireRequest{method: generationapi.MethodGET, target: url.URL{Scheme: "https", Host: "api.example.test"}})
		requireResponseFailure(t, err, "transport response is invalid", "response_header_value_invalid", "/headers/1/value", 200)
	}
}

func TestHTTPTransportClosesDiscardedBodiesAndFoldsRawFailures(t *testing.T) {
	vectors := []struct {
		name        string
		response    *http.Response
		err         error
		panicValue  any
		wantMessage string
		wantReason  string
		wantPointer string
		wantStatus  int
	}{
		{name: "network error", err: errors.New("network secret"), wantMessage: "API transport failed", wantReason: "round_trip_failed"},
		{name: "panic", panicValue: "round trip secret", wantMessage: "API transport failed", wantReason: "round_trip_failed"},
		{name: "response and error", response: &http.Response{StatusCode: 200, Body: newTrackedResponseBody("unused")}, err: errors.New("network secret"), wantMessage: "API transport failed", wantReason: "round_trip_failed"},
		{name: "status conversion", response: &http.Response{StatusCode: 199, Body: newTrackedResponseBody("unused")}, wantMessage: "transport response is invalid", wantReason: "response_status_invalid", wantPointer: "/statusCode"},
		{name: "body conversion", response: &http.Response{StatusCode: 200, Body: nil}, wantMessage: "transport response is invalid", wantReason: "response_body_required", wantPointer: "/body", wantStatus: 200},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			adapter, err := NewHTTPTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) {
				if vector.panicValue != nil {
					panic(vector.panicValue)
				}
				return vector.response, vector.err
			}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.RoundTrip(context.Background(), WireRequest{method: generationapi.MethodGET, target: url.URL{Scheme: "https", Host: "api.example.test"}})
			apiError := requireResponseFailureWithCode(t, err, codeTransportError, vector.wantMessage, vector.wantReason, vector.wantPointer, vector.wantStatus, "")
			assertSDKProjectionOmits(t, apiError, "network secret", "round trip secret")
			if vector.response != nil && vector.response.Body != nil {
				body := vector.response.Body.(*trackedResponseBody)
				if body.closeCalls.Load() != 1 {
					t.Fatalf("Close calls = %d", body.closeCalls.Load())
				}
			}
		})
	}
}

func TestHTTPTransportThroughClientPreservesOnlyClosedSafeFailures(t *testing.T) {
	request, err := ParseRequest([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	vectors := []struct {
		name        string
		roundTrip   roundTripperFunc
		wantMessage string
		wantReason  string
		wantPointer string
		wantStatus  int
	}{
		{
			name: "network",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("network secret")
			},
			wantMessage: "API transport failed", wantReason: "round_trip_failed",
		},
		{
			name: "header conversion",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 202, Header: http.Header{"X-Bad": []string{"secret\n"}}, Body: newTrackedResponseBody("unused")}, nil
			},
			wantMessage: "transport response is invalid", wantReason: "response_header_value_invalid", wantPointer: "/headers/0/value", wantStatus: 202,
		},
		{
			name: "body conversion",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 203, Body: nil}, nil
			},
			wantMessage: "transport response is invalid", wantReason: "response_body_required", wantPointer: "/body", wantStatus: 203,
		},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			adapter, err := NewHTTPTransport(vector.roundTrip)
			if err != nil {
				t.Fatal(err)
			}
			client := responseTestClient(t, responseTestManifest(t, generationapi.ResponseBodyNone), adapter, nil, RuntimeLimits().ResponseBytesMin)
			_, err = client.Call(context.Background(), "sample.call", request)
			apiError := requireResponseFailureWithCode(t, err, codeTransportError, vector.wantMessage, vector.wantReason, vector.wantPointer, vector.wantStatus, "sample.call")
			assertSDKProjectionOmits(t, apiError, "network secret", "secret")
		})
	}
}

func TestHTTPTransportUsesOneRoundTripWithoutRedirectOrRetry(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Location", "/other")
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusFound)
		_, _ = writer.Write([]byte(`{"apiVersion":"nexa.dev/remote-error/v1","domain":"sample","code":"other","message":"redirect"}`))
	}))
	defer server.Close()
	target, err := url.Parse(server.URL + "/start")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewHTTPTransport(http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.RoundTrip(context.Background(), WireRequest{method: generationapi.MethodGET, target: *target})
	if err != nil {
		t.Fatal(err)
	}
	defer response.close()
	if calls.Load() != 1 || response.StatusCode() != http.StatusFound {
		t.Fatalf("RoundTrip calls = %d status = %d", calls.Load(), response.StatusCode())
	}
}

type typedNilRoundTripper struct{}

func (*typedNilRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, nil }

type hostileTransportError struct{}

func (hostileTransportError) Error() string { return "hostile transport detail" }
func (hostileTransportError) As(any) bool   { panic("hostile transport As") }
func (hostileTransportError) Unwrap() error { panic("hostile transport Unwrap") }

type hostileReaderError struct{}

func (hostileReaderError) Error() string { return "hostile reader detail" }
func (hostileReaderError) Is(error) bool { panic("hostile reader Is") }

func readRequestBody(t *testing.T, request *http.Request) []byte {
	t.Helper()
	if request.Body == nil {
		return nil
	}
	data, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	request.Body = io.NopCloser(bytes.NewReader(data))
	return data
}

func requireProjectedResponseError(t *testing.T, err error, code, domain, message, reason, pointer string, status int) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("operation succeeded, want %s/%s", code, reason)
	}
	var apiError *Error
	if !errors.As(err, &apiError) || apiError == nil {
		t.Fatalf("error = %T %v, want *api.Error", err, err)
	}
	if apiError.Code() != code || apiError.Domain() != domain || apiError.Category() != protocol.CategoryExternal || apiError.Retryable() || apiError.Error() != message || apiError.APIOperationID() != "sample.call" {
		t.Fatalf("identity = (%q,%q,%q,%t,%q,%q)", apiError.Code(), apiError.Domain(), apiError.Category(), apiError.Retryable(), apiError.Error(), apiError.APIOperationID())
	}
	if apiError.Details().Reason() != reason || apiError.Details().Pointer() != pointer || apiError.Details().HTTPStatus() != status {
		t.Fatalf("details = (%q,%q,%d), want (%q,%q,%d)", apiError.Details().Reason(), apiError.Details().Pointer(), apiError.Details().HTTPStatus(), reason, pointer, status)
	}
	if !errors.Is(apiError, errSDKAPI) || errors.Unwrap(apiError.Unwrap()) != nil {
		t.Fatalf("unwrap = %v", apiError.Unwrap())
	}
	return apiError
}

package api

import (
	"context"
	"io"
	"net/url"
	"sort"
	"strconv"
	"sync"

	"github.com/nxnminieye/nexa/cli/protocol"
	generationapi "github.com/nxnminieye/nexa/generation/api"
)

// Header is one canonical logical wire header shared by requests and responses.
// Request builders and response constructors enforce their respective wire constraints.
type Header struct {
	Name  string
	Value string
}

// WireRequest is an immutable logical request ready for a Transport.
type WireRequest struct {
	method  generationapi.HTTPMethod
	target  url.URL
	headers []Header
	body    []byte
}

func (r WireRequest) Method() generationapi.HTTPMethod { return r.method }

// URL returns an independent copy of the request URL.
func (r WireRequest) URL() *url.URL {
	target := r.target
	return &target
}

// Headers returns an independent copy of the logical headers.
func (r WireRequest) Headers() []Header { return append([]Header(nil), r.headers...) }

// Body returns an independent copy of the canonical body, or nil when absent.
func (r WireRequest) Body() []byte { return append([]byte(nil), r.body...) }

// WireResponse is an immutable logical response whose body has shared close-once ownership.
type WireResponse struct {
	statusCode int
	headers    []Header
	state      *responseBodyState
}

type responseBodyState struct {
	body io.ReadCloser
	once sync.Once
}

// NewWireResponse validates and takes ownership of one logical response body.
func NewWireResponse(statusCode int, headers []Header, body io.ReadCloser) (WireResponse, error) {
	if statusCode < 200 || statusCode > 599 {
		return WireResponse{}, newWireResponseError("response_status_invalid", "/statusCode", 0)
	}
	for index, header := range headers {
		if !validResponseHeaderName(header.Name) {
			return WireResponse{}, newWireResponseError("response_header_name_invalid", responseHeaderPointer(index, "name"), statusCode)
		}
		if !validResponseHeaderValue(header.Value) {
			return WireResponse{}, newWireResponseError("response_header_value_invalid", responseHeaderPointer(index, "value"), statusCode)
		}
	}
	if nilLike(body) {
		return WireResponse{}, newWireResponseError("response_body_required", "/body", statusCode)
	}
	frozenHeaders := append([]Header(nil), headers...)
	sort.SliceStable(frozenHeaders, func(left, right int) bool {
		return frozenHeaders[left].Name < frozenHeaders[right].Name ||
			frozenHeaders[left].Name == frozenHeaders[right].Name && frozenHeaders[left].Value < frozenHeaders[right].Value
	})
	return WireResponse{
		statusCode: statusCode,
		headers:    frozenHeaders,
		state:      &responseBodyState{body: body},
	}, nil
}

// StatusCode returns the observed HTTP status, or zero for a zero response.
func (r WireResponse) StatusCode() int {
	if r.state == nil {
		return 0
	}
	return r.statusCode
}

// Headers returns an independent copy, or nil for a zero response.
func (r WireResponse) Headers() []Header {
	if r.state == nil {
		return nil
	}
	return append([]Header(nil), r.headers...)
}

func (r WireResponse) body() io.ReadCloser {
	if r.state == nil {
		return nil
	}
	return r.state.body
}

func (r WireResponse) close() {
	if r.state == nil {
		return
	}
	r.state.once.Do(func() { closeRecoverSafe(r.state.body) })
}

func (r WireResponse) valid() bool {
	return r.state != nil && r.statusCode >= 200 && r.statusCode <= 599 && !nilLike(r.state.body)
}

func closeRecoverSafe(closer io.Closer) {
	if nilLike(closer) {
		return
	}
	defer func() { _ = recover() }()
	_ = closer.Close()
}

func newWireResponseError(reason, pointer string, statusCode int) *Error {
	return newSDKError(
		codeTransportError,
		sdkErrorDomain,
		protocol.CategoryExternal,
		"transport response is invalid",
		ErrorDetails{reason: reason, pointer: pointer, httpStatus: statusCode},
	)
}

func responseHeaderPointer(index int, field string) string {
	return "/headers/" + strconv.Itoa(index) + "/" + field
}

func validResponseHeaderName(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' ||
			character == '!' || character == '#' || character == '$' || character == '%' || character == '&' ||
			character == '\'' || character == '*' || character == '+' || character == '-' || character == '.' ||
			character == '^' || character == '_' || character == '`' || character == '|' || character == '~' {
			continue
		}
		return false
	}
	return true
}

func validResponseHeaderValue(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '\t' || character >= 0x20 && character <= 0x7e {
			continue
		}
		return false
	}
	return true
}

// Transport executes one logical request.
type Transport interface {
	RoundTrip(context.Context, WireRequest) (WireResponse, error)
}

type TransportFunc func(context.Context, WireRequest) (WireResponse, error)

func (fn TransportFunc) RoundTrip(ctx context.Context, request WireRequest) (WireResponse, error) {
	return fn(ctx, request)
}

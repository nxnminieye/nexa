package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sort"
)

type httpTransport struct {
	roundTripper http.RoundTripper
}

// NewHTTPTransport adapts exactly one http.RoundTripper attempt to Transport.
func NewHTTPTransport(roundTripper http.RoundTripper) (Transport, error) {
	if nilLike(roundTripper) {
		return nil, newConstructorError(
			codeClientInvalid,
			"HTTP transport configuration is invalid",
			"round_tripper_required",
			"/roundTripper",
		)
	}
	return httpTransport{roundTripper: roundTripper}, nil
}

func (t httpTransport) RoundTrip(ctx context.Context, wire WireRequest) (response WireResponse, err error) {
	request, err := newHTTPRequest(ctx, wire)
	if err != nil {
		return WireResponse{}, newTransportFailure("")
	}
	httpResponse, roundTripErr, panicked := invokeHTTPRoundTrip(t.roundTripper, request)
	if panicked || roundTripErr != nil || httpResponse == nil {
		if httpResponse != nil {
			closeRecoverSafe(httpResponse.Body)
		}
		return WireResponse{}, newTransportFailure("")
	}
	headers := expandHTTPResponseHeaders(httpResponse.Header)
	response, err = NewWireResponse(httpResponse.StatusCode, headers, httpResponse.Body)
	if err != nil {
		closeRecoverSafe(httpResponse.Body)
		return WireResponse{}, err
	}
	return response, nil
}

func newHTTPRequest(ctx context.Context, wire WireRequest) (request *http.Request, err error) {
	defer func() {
		if recover() != nil {
			request, err = nil, io.ErrUnexpectedEOF
		}
	}()
	var body io.Reader
	if encoded := wire.Body(); encoded != nil {
		body = bytes.NewReader(encoded)
	}
	request, err = http.NewRequestWithContext(ctx, string(wire.Method()), wire.URL().String(), body)
	if err != nil {
		return nil, err
	}
	for _, header := range wire.Headers() {
		request.Header.Add(header.Name, header.Value)
	}
	return request, nil
}

func invokeHTTPRoundTrip(roundTripper http.RoundTripper, request *http.Request) (response *http.Response, err error, panicked bool) {
	defer func() {
		if recover() != nil {
			response, err, panicked = nil, nil, true
		}
	}()
	response, err = roundTripper.RoundTrip(request)
	return response, err, false
}

func expandHTTPResponseHeaders(headers http.Header) []Header {
	result := make([]Header, 0)
	for name, values := range headers {
		lowerName := asciiLower(name)
		for _, value := range values {
			result = append(result, Header{Name: lowerName, Value: value})
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		return result[left].Name < result[right].Name ||
			result[left].Name == result[right].Name && result[left].Value < result[right].Value
	})
	return result
}

func asciiLower(value string) string {
	result := []byte(value)
	for index, character := range result {
		if character >= 'A' && character <= 'Z' {
			result[index] = character + ('a' - 'A')
		}
	}
	return string(result)
}

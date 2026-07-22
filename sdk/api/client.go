package api

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"strings"

	"github.com/nxnminieye/nexa/cli/protocol"
	generationapi "github.com/nxnminieye/nexa/generation/api"
)

type Options struct {
	Manifest           generationapi.Manifest
	Endpoint           *url.URL
	Transport          Transport
	CredentialProvider CredentialProvider
	MaxResponseBytes   int64
}

// Client freezes one validated Manifest projection and its runtime dependencies.
type Client struct {
	model              *runtimeModel
	endpoint           url.URL
	normalizedPrefix   string
	transport          Transport
	credentialProvider CredentialProvider
	maxResponseBytes   int64
}

type requestBuildOptions struct {
	credentialProvider CredentialProvider
}

// CallOption is one immutable, closed per-call override.
type CallOption interface {
	apply(*callOptions) error
}

type callOptionKind uint8

const (
	callOptionCredentialProvider callOptionKind = iota + 1
	callOptionMaxResponseBytes
)

type callOption struct {
	kind               callOptionKind
	credentialProvider CredentialProvider
	maxResponseBytes   int64
}

type callOptions struct {
	credentialProvider CredentialProvider
	maxResponseBytes   int64
	providerSet        bool
	maxResponseSet     bool
}

type callOptionFailure struct{ reason string }

func (e callOptionFailure) Error() string { return e.reason }

// WithCredentialProvider replaces the client provider for one call.
func WithCredentialProvider(provider CredentialProvider) CallOption {
	return callOption{kind: callOptionCredentialProvider, credentialProvider: provider}
}

// WithMaxResponseBytes replaces the client response bound for one call.
func WithMaxResponseBytes(maxBytes int64) CallOption {
	return callOption{kind: callOptionMaxResponseBytes, maxResponseBytes: maxBytes}
}

func (o callOption) apply(options *callOptions) error {
	switch o.kind {
	case callOptionCredentialProvider:
		if options.providerSet {
			return callOptionFailure{reason: "call_option_duplicate"}
		}
		options.providerSet = true
		options.credentialProvider = o.credentialProvider
		if nilLike(options.credentialProvider) {
			options.credentialProvider = nil
		}
		return nil
	case callOptionMaxResponseBytes:
		if options.maxResponseSet {
			return callOptionFailure{reason: "call_option_duplicate"}
		}
		limits := RuntimeLimits()
		if o.maxResponseBytes < limits.ResponseBytesMin || o.maxResponseBytes > limits.ResponseBytesMax {
			return callOptionFailure{reason: "max_response_bytes_invalid"}
		}
		options.maxResponseSet = true
		options.maxResponseBytes = o.maxResponseBytes
		return nil
	default:
		return callOptionFailure{reason: "call_option_invalid"}
	}
}

func New(options Options) (*Client, error) {
	contract, err := BuildRuntimeContract(options.Manifest)
	if err != nil {
		return nil, err
	}
	return newClientFromRuntimeModel(
		contract.model,
		options.Endpoint,
		options.Transport,
		options.CredentialProvider,
		options.MaxResponseBytes,
	)
}

func newClientFromRuntimeModel(model *runtimeModel, endpointInput *url.URL, transport Transport, providerInput CredentialProvider, maxResponseBytes int64) (*Client, error) {
	endpoint, prefix, reason := normalizeEndpoint(endpointInput)
	if reason != "" {
		return nil, newConstructorError(codeClientInvalid, "API client configuration is invalid", reason, "/endpoint")
	}
	if nilLike(transport) {
		return nil, newConstructorError(codeClientInvalid, "API client configuration is invalid", "transport_required", "/transport")
	}
	limits := RuntimeLimits()
	if maxResponseBytes < limits.ResponseBytesMin || maxResponseBytes > limits.ResponseBytesMax {
		return nil, newConstructorError(codeClientInvalid, "API client configuration is invalid", "max_response_bytes_invalid", "/maxResponseBytes")
	}
	provider := providerInput
	if nilLike(provider) {
		provider = nil
	}
	return &Client{
		model:              model,
		endpoint:           endpoint,
		normalizedPrefix:   prefix,
		transport:          transport,
		credentialProvider: provider,
		maxResponseBytes:   maxResponseBytes,
	}, nil
}

// Call builds one request, executes Transport once and projects one bounded response.
func (c *Client) Call(ctx context.Context, apiOperationID string, request Request, options ...CallOption) (Result, error) {
	if c == nil {
		return Result{}, newCallConfigurationError("client_nil", "")
	}
	if ctx == nil {
		return Result{}, newCallConfigurationError("context_required", "/context")
	}
	effective := callOptions{
		credentialProvider: c.credentialProvider,
		maxResponseBytes:   c.maxResponseBytes,
	}
	for index, option := range options {
		pointer := fmt.Sprintf("/options/%d", index)
		if nilLike(option) {
			return Result{}, newCallConfigurationError("call_option_nil", pointer)
		}
		internalOption, closed := option.(callOption)
		if !closed {
			return Result{}, newCallConfigurationError("call_option_invalid", pointer)
		}
		if err := internalOption.apply(&effective); err != nil {
			reason := "call_option_invalid"
			if failure, ok := err.(callOptionFailure); ok {
				reason = failure.reason
			}
			return Result{}, newCallConfigurationError(reason, pointer)
		}
	}

	operation, exists := c.model.operation(apiOperationID)
	if !exists {
		return Result{}, newOperationLookupError()
	}
	wireRequest, err := c.buildWireRequest(ctx, apiOperationID, request, requestBuildOptions{credentialProvider: effective.credentialProvider})
	if err != nil {
		return Result{}, err
	}

	response, transportErr, panicked := invokeTransport(ctx, c.transport, wireRequest)
	if canceled := contextFailure(ctx, operation.id); canceled != nil {
		transferred := response.state != nil
		canceled.details.httpStatus = response.StatusCode()
		if transferred {
			response.close()
		}
		return Result{}, canceled
	}
	transferred := response.state != nil
	if transportErr != nil || panicked {
		if transferred {
			response.close()
		}
		return Result{}, projectTransportFailure(transportErr, operation.id)
	}
	if !response.valid() {
		if transferred {
			response.close()
		}
		return Result{}, newInvalidTransportResponse(operation.id)
	}
	defer response.close()
	return c.projectResponse(ctx, operation, response, effective.maxResponseBytes)
}

func invokeTransport(ctx context.Context, transport Transport, request WireRequest) (response WireResponse, err error, panicked bool) {
	defer func() {
		if recover() != nil {
			response = WireResponse{}
			err = nil
			panicked = true
		}
	}()
	response, err = transport.RoundTrip(ctx, request)
	return response, err, false
}

func newCallConfigurationError(reason, pointer string) *Error {
	return newSDKError(
		codeClientInvalid,
		sdkErrorDomain,
		protocol.CategoryInput,
		"API call configuration is invalid",
		ErrorDetails{reason: reason, pointer: pointer},
	)
}

func newInvalidTransportResponse(apiOperationID string) *Error {
	err := newSDKError(
		codeTransportError,
		sdkErrorDomain,
		protocol.CategoryExternal,
		"transport response is invalid",
		ErrorDetails{reason: "response_invalid"},
	)
	err.apiOperationID = apiOperationID
	return err
}

func projectTransportFailure(input error, apiOperationID string) *Error {
	apiError, trusted := input.(*Error)
	if trusted && apiError != nil && apiError.code == codeTransportError && apiError.domain == sdkErrorDomain {
		reason := apiError.details.reason
		message := "transport response is invalid"
		pointer := ""
		statusCode := 0
		switch reason {
		case "round_trip_failed":
			message = "API transport failed"
		case "response_status_invalid":
			pointer = "/statusCode"
		case "response_header_name_invalid", "response_header_value_invalid":
			pointer = apiError.details.pointer
			statusCode = apiError.details.httpStatus
		case "response_body_required":
			pointer = "/body"
			statusCode = apiError.details.httpStatus
		default:
			return newTransportFailure(apiOperationID)
		}
		result := newSDKError(
			codeTransportError,
			sdkErrorDomain,
			protocol.CategoryExternal,
			message,
			ErrorDetails{reason: reason, pointer: pointer, httpStatus: statusCode},
		)
		result.apiOperationID = apiOperationID
		return result
	}
	return newTransportFailure(apiOperationID)
}

func newTransportFailure(apiOperationID string) *Error {
	err := newSDKError(
		codeTransportError,
		sdkErrorDomain,
		protocol.CategoryExternal,
		"API transport failed",
		ErrorDetails{reason: "round_trip_failed"},
	)
	err.apiOperationID = apiOperationID
	return err
}

func contextFailureWithStatus(ctx context.Context, apiOperationID string, statusCode int) *Error {
	err := contextFailure(ctx, apiOperationID)
	if err != nil {
		err.details.httpStatus = statusCode
	}
	return err
}

func newConstructorError(code, message, reason, pointer string) *Error {
	return newSDKError(
		code,
		sdkErrorDomain,
		protocol.CategoryInput,
		message,
		ErrorDetails{reason: reason, pointer: pointer},
	)
}

func normalizeEndpoint(input *url.URL) (url.URL, string, string) {
	if input == nil {
		return url.URL{}, "", "endpoint_required"
	}
	if input.Scheme != "http" && input.Scheme != "https" || input.Host == "" || input.Opaque != "" || input.User != nil ||
		input.RawQuery != "" || input.ForceQuery || input.Fragment != "" || input.RawFragment != "" || input.RawPath != "" {
		return url.URL{}, "", "endpoint_invalid"
	}
	prefix := input.Path
	if prefix == "/" {
		prefix = ""
	} else if !validEndpointPrefix(prefix) {
		return url.URL{}, "", "endpoint_invalid"
	}
	endpoint := *input
	endpoint.Path = ""
	endpoint.RawPath = ""
	return endpoint, prefix, ""
}

func validEndpointPrefix(value string) bool {
	if value == "" {
		return true
	}
	if !strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for index := 0; index < len(segment); index++ {
			if !unreservedByte(segment[index]) {
				return false
			}
		}
	}
	return true
}

func nilLike(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

package api

import (
	"context"
	"io"
	"mime"
	"strings"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/cli/protocol"
	generationapi "github.com/nxnminieye/nexa/generation/api"
)

func (c *Client) projectResponse(ctx context.Context, operation runtimeOperation, response WireResponse, maxBytes int64) (Result, error) {
	statusCode := response.StatusCode()
	if statusCode >= 200 && statusCode <= 299 && operation.response.body == generationapi.ResponseBodyNone {
		return Result{
			apiOperationID: operation.id,
			httpStatus:     statusCode,
			responseBody:   generationapi.ResponseBodyNone,
		}, nil
	}
	if err := validateResponseContentType(response.Headers(), operation.id, statusCode); err != nil {
		return Result{}, err
	}
	body, err := readBoundedResponse(ctx, response.body(), maxBytes, operation.id, statusCode)
	if err != nil {
		return Result{}, err
	}
	if statusCode >= 200 && statusCode <= 299 {
		canonical, err := c.parseSuccessResponse(operation, body, statusCode)
		if err != nil {
			return Result{}, err
		}
		return Result{
			apiOperationID: operation.id,
			httpStatus:     statusCode,
			responseBody:   generationapi.ResponseBodyJSON,
			json:           append([]byte(nil), canonical...),
			hasJSON:        true,
		}, nil
	}
	return Result{}, c.projectRemoteResponse(operation, statusCode, body)
}

func validateResponseContentType(headers []Header, apiOperationID string, statusCode int) *Error {
	values := make([]string, 0, 1)
	for _, header := range headers {
		if header.Name == "content-type" {
			values = append(values, header.Value)
		}
	}
	if len(values) == 0 {
		return newResponseProtocolError("response_content_type_missing", "/headers/content-type", apiOperationID, statusCode)
	}
	if len(values) != 1 {
		return newResponseProtocolError("response_content_type_duplicate", "/headers/content-type", apiOperationID, statusCode)
	}
	mediaType, parameters, err := mime.ParseMediaType(values[0])
	if err != nil {
		return newResponseProtocolError("response_content_type_malformed", "/headers/content-type", apiOperationID, statusCode)
	}
	parameterNames, duplicateParameter := mediaParameterNames(values[0])
	if duplicateParameter {
		return newResponseProtocolError("response_content_type_malformed", "/headers/content-type", apiOperationID, statusCode)
	}
	if !isJSONMediaType(mediaType) {
		return newResponseProtocolError("response_content_type_unsupported", "/headers/content-type", apiOperationID, statusCode)
	}
	if len(parameters) == 0 && len(parameterNames) == 0 {
		return nil
	}
	if len(parameters) != 1 || len(parameterNames) != 1 || !strings.EqualFold(parameterNames[0], "charset") {
		return newResponseProtocolError("response_content_type_parameter_invalid", "/headers/content-type", apiOperationID, statusCode)
	}
	for name, value := range parameters {
		if !strings.EqualFold(name, "charset") || !strings.EqualFold(value, "utf-8") {
			return newResponseProtocolError("response_content_type_parameter_invalid", "/headers/content-type", apiOperationID, statusCode)
		}
	}
	return nil
}

func mediaParameterNames(value string) ([]string, bool) {
	separator := strings.IndexByte(value, ';')
	if separator < 0 {
		return nil, false
	}
	seen := make(map[string]struct{})
	names := make([]string, 0, 1)
	segmentStart := separator + 1
	quoted := false
	escaped := false
	for index := segmentStart; index <= len(value); index++ {
		if index < len(value) {
			character := value[index]
			if escaped {
				escaped = false
				continue
			}
			if quoted && character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				quoted = !quoted
				continue
			}
			if character != ';' || quoted {
				continue
			}
		}
		segment := strings.TrimSpace(value[segmentStart:index])
		name, _, _ := strings.Cut(segment, "=")
		name = strings.ToLower(strings.TrimSpace(name))
		if _, duplicate := seen[name]; duplicate {
			return nil, true
		}
		seen[name] = struct{}{}
		names = append(names, name)
		segmentStart = index + 1
	}
	return names, false
}

func isJSONMediaType(value string) bool {
	if strings.EqualFold(value, "application/json") {
		return true
	}
	if len(value) <= len("application/+json") || !strings.EqualFold(value[:len("application/")], "application/") || !strings.EqualFold(value[len(value)-len("+json"):], "+json") {
		return false
	}
	subtype := value[len("application/") : len(value)-len("+json")]
	return validHTTPToken(subtype)
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func readBoundedResponse(ctx context.Context, reader io.Reader, maxBytes int64, apiOperationID string, statusCode int) ([]byte, error) {
	if canceled := contextFailureWithStatus(ctx, apiOperationID, statusCode); canceled != nil {
		return nil, canceled
	}
	result := make([]byte, 0, minResponseBuffer(maxBytes))
	readLimit := maxBytes + 1
	scratch := make([]byte, minResponseBuffer(readLimit))
	noProgress := 0
	for {
		remaining := readLimit - int64(len(result))
		bufferSize := int64(len(scratch))
		if remaining < bufferSize {
			bufferSize = remaining
		}
		if bufferSize < 1 {
			bufferSize = 1
		}
		buffer := scratch[:int(bufferSize)]
		n, readErr, panicked := invokeResponseRead(reader, buffer)
		if canceled := contextFailureWithStatus(ctx, apiOperationID, statusCode); canceled != nil {
			return nil, canceled
		}
		if panicked || n < 0 || n > len(buffer) {
			return nil, newResponseProtocolError("response_body_read_failed", "/body", apiOperationID, statusCode)
		}
		if n > 0 {
			result = append(result, buffer[:n]...)
			noProgress = 0
		} else {
			noProgress++
		}
		if readErr != nil && readErr != io.EOF {
			return nil, newResponseProtocolError("response_body_read_failed", "/body", apiOperationID, statusCode)
		}
		if int64(len(result)) > maxBytes {
			return nil, newResponseProtocolError("response_body_too_large", "/body", apiOperationID, statusCode)
		}
		if readErr == io.EOF {
			break
		}
		if noProgress >= 100 {
			return nil, newResponseProtocolError("response_body_read_failed", "/body", apiOperationID, statusCode)
		}
	}
	if len(result) == 0 {
		return nil, newResponseProtocolError("response_body_empty", "/body", apiOperationID, statusCode)
	}
	return result, nil
}

func minResponseBuffer(maxBytes int64) int {
	if maxBytes < 32<<10 {
		return int(maxBytes)
	}
	return 32 << 10
}

func invokeResponseRead(reader io.Reader, target []byte) (n int, err error, panicked bool) {
	defer func() {
		if recover() != nil {
			n, err, panicked = 0, nil, true
		}
	}()
	n, err = reader.Read(target)
	return n, err, false
}

func (c *Client) parseSuccessResponse(operation runtimeOperation, body []byte, statusCode int) ([]byte, error) {
	limits := RuntimeLimits()
	parser := requestParser{
		data:      body,
		maxDepth:  limits.JSONDepth,
		maxNodes:  limits.JSONNodes,
		semantics: limits.JSONSemantics(),
		allowNull: true,
		newError: func(reason, _ string) *Error {
			return newResponseProtocolError(successParserReason(reason), "/body", operation.id, statusCode)
		},
	}
	value, err := parser.parseValue("", parser.semantics.RootDepth())
	if err != nil {
		return nil, err
	}
	parser.skipWhitespace()
	if parser.offset != len(body) {
		return nil, newResponseProtocolError("response_trailing_input", "/body", operation.id, statusCode)
	}
	schema, exists := c.model.schema(operation.response.schema)
	if !exists {
		return nil, newResponseProtocolError("response_schema_invalid", "/body", operation.id, statusCode)
	}
	normalized, validateErr := validateRequestValue(c.model, schema, value, "", operation.id)
	if validateErr != nil {
		return nil, newResponseProtocolError("response_schema_invalid", "/body", operation.id, statusCode)
	}
	canonical, canonicalErr := jcs.Transform(normalized.appendJSON(nil))
	if canonicalErr != nil {
		return nil, newResponseProtocolError("response_document_invalid", "/body", operation.id, statusCode)
	}
	return canonical, nil
}

func successParserReason(reason string) string {
	switch reason {
	case "duplicate_key":
		return "response_duplicate_key"
	case "depth_limit_exceeded":
		return "response_depth_limit_exceeded"
	case "node_limit_exceeded":
		return "response_node_limit_exceeded"
	default:
		return "response_document_invalid"
	}
}

func (c *Client) projectRemoteResponse(operation runtimeOperation, statusCode int, body []byte) error {
	remote, err := ParseRemoteError(body)
	if err != nil {
		return projectRemoteParserFailure(err, operation.id, statusCode)
	}
	codes := operation.errorProjections[remote.Domain()]
	if target, exists := codes[remote.Code()]; exists {
		if target.httpStatus != statusCode {
			result := newSDKError(
				codeRemoteProtocolError,
				sdkErrorDomain,
				protocol.CategoryExternal,
				"remote error status does not match API manifest",
				remoteProjectionDetails("response_status_mismatch", "/statusCode", statusCode, remote),
			)
			applyRemoteContext(result, operation.id, remote)
			return result
		}
		result := newSDKError(
			target.code,
			target.domain,
			protocol.CategoryExternal,
			remote.Message(),
			remoteProjectionDetails("", "", statusCode, remote),
		)
		applyRemoteContext(result, operation.id, remote)
		return result
	}
	result := newSDKError(
		codeRemoteErrorUnmapped,
		sdkErrorDomain,
		protocol.CategoryExternal,
		"remote API error is not mapped",
		remoteProjectionDetails("remote_error_unmapped", "", statusCode, remote),
	)
	applyRemoteContext(result, operation.id, remote)
	return result
}

func projectRemoteParserFailure(input error, apiOperationID string, statusCode int) *Error {
	apiError, trusted := input.(*Error)
	if trusted && apiError != nil && apiError.code == codeRemoteProtocolError && apiError.domain == sdkErrorDomain {
		result := newSDKError(
			apiError.code,
			apiError.domain,
			apiError.category,
			apiError.message,
			ErrorDetails{reason: apiError.details.reason, pointer: apiError.details.pointer, httpStatus: statusCode},
		)
		result.apiOperationID = apiOperationID
		return result
	}
	return newResponseProtocolError("response_document_invalid", "/body", apiOperationID, statusCode)
}

func remoteProjectionDetails(reason, pointer string, statusCode int, remote RemoteError) ErrorDetails {
	return ErrorDetails{
		reason:       reason,
		pointer:      pointer,
		httpStatus:   statusCode,
		remoteDomain: remote.Domain(),
		remoteCode:   remote.Code(),
	}
}

func applyRemoteContext(err *Error, apiOperationID string, remote RemoteError) {
	err.apiOperationID = apiOperationID
	err.requestID = remote.RequestID()
	err.traceID = remote.TraceID()
}

func newResponseProtocolError(reason, pointer, apiOperationID string, statusCode int) *Error {
	err := newSDKError(
		codeRemoteProtocolError,
		sdkErrorDomain,
		protocol.CategoryExternal,
		"API response is invalid",
		ErrorDetails{reason: reason, pointer: pointer, httpStatus: statusCode},
	)
	err.apiOperationID = apiOperationID
	return err
}

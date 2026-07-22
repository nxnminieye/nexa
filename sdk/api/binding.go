package api

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/nxnminieye/nexa/cli/protocol"
	generationapi "github.com/nxnminieye/nexa/generation/api"
)

type encodedQueryPair struct {
	name  string
	value string
}

func (c *Client) buildWireRequest(
	ctx context.Context,
	apiOperationID string,
	request Request,
	options requestBuildOptions,
) (WireRequest, error) {
	operation, exists := c.model.operation(apiOperationID)
	if !exists {
		return WireRequest{}, newOperationLookupError()
	}
	fields, err := validateSelectedRequest(c.model, operation, request)
	if err != nil {
		return WireRequest{}, err
	}

	pathValues := make(map[string]string)
	query := make([]encodedQueryPair, 0)
	headers := make([]Header, 0)
	bodyMembers := make(requestObject, 0)
	hasBodyBinding := false
	for _, field := range sortedRuntimeBindingNames(operation.request.bindings) {
		binding := operation.request.bindings[field]
		if binding.location == generationapi.RequestBindingBody {
			hasBodyBinding = true
		}
		value, present := fields[field]
		if !present {
			continue
		}
		if binding.location == generationapi.RequestBindingBody {
			bodyMembers = append(bodyMembers, requestMember{name: binding.name, value: value})
			continue
		}
		lexical, scalar := scalarLexical(value)
		if !scalar {
			return WireRequest{}, newRequestBuildError("value_invalid", "/"+escapeJSONPointer(field), operation.id)
		}
		switch binding.location {
		case generationapi.RequestBindingPath:
			if lexical == "." || lexical == ".." {
				return WireRequest{}, newRequestBuildError("value_invalid", "/"+escapeJSONPointer(field), operation.id)
			}
			pathValues[field] = lexical
		case generationapi.RequestBindingQuery:
			query = append(query, encodedQueryPair{name: encodeRFC3986(binding.name), value: encodeRFC3986(lexical)})
		case generationapi.RequestBindingHeader:
			if !validLogicalHeaderValue(lexical) {
				return WireRequest{}, newRequestBuildError("value_invalid", "/"+escapeJSONPointer(field), operation.id)
			}
			headers = append(headers, Header{Name: binding.name, Value: lexical})
		}
	}

	encodedPath := c.normalizedPrefix + encodeRuntimePath(operation.pathSegments, pathValues)
	var body []byte
	if hasBodyBinding {
		sort.Slice(bodyMembers, func(i, j int) bool { return lessUTF16(bodyMembers[i].name, bodyMembers[j].name) })
		body = bodyMembers.appendJSON(nil)
		headers = append(headers, Header{Name: generationapi.RequestContentTypeHeader, Value: generationapi.RequestJSONMediaType})
	}

	if canceled := contextFailure(ctx, operation.id); canceled != nil {
		return WireRequest{}, canceled
	}
	provider := options.credentialProvider
	if nilLike(provider) {
		provider = nil
	}
	credentials, providerErr, providerPanicked := invokeCredentialProvider(ctx, provider, operation.id)
	if canceled := contextFailure(ctx, operation.id); canceled != nil {
		return WireRequest{}, canceled
	}
	if providerErr != nil || providerPanicked {
		return WireRequest{}, newCredentialProviderError(operation.id)
	}
	var cookies []string
	if err := bindCredential(operation, credentials, &query, &headers, &cookies); err != nil {
		return WireRequest{}, err
	}
	if len(cookies) != 0 {
		headers = append(headers, Header{Name: "cookie", Value: strings.Join(cookies, "; ")})
	}

	sort.SliceStable(query, func(i, j int) bool {
		return query[i].name < query[j].name || query[i].name == query[j].name && query[i].value < query[j].value
	})
	rawQueryParts := make([]string, len(query))
	for index, pair := range query {
		rawQueryParts[index] = pair.name + "=" + pair.value
	}
	sort.SliceStable(headers, func(i, j int) bool {
		return headers[i].Name < headers[j].Name || headers[i].Name == headers[j].Name && headers[i].Value < headers[j].Value
	})

	target := c.endpoint
	decodedPath, _ := url.PathUnescape(encodedPath)
	target.Path = decodedPath
	target.RawPath = encodedPath
	target.RawQuery = strings.Join(rawQueryParts, "&")
	return WireRequest{
		method:  operation.method,
		target:  target,
		headers: append([]Header(nil), headers...),
		body:    append([]byte(nil), body...),
	}, nil
}

func encodeRuntimePath(segments []runtimePathSegment, values map[string]string) string {
	var result strings.Builder
	for _, segment := range segments {
		if segment.field != "" {
			result.WriteString(encodeRFC3986(values[segment.field]))
		} else {
			result.WriteString(segment.literal)
		}
	}
	return result.String()
}

func invokeCredentialProvider(
	ctx context.Context,
	provider CredentialProvider,
	apiOperationID string,
) (values []CredentialValue, err error, panicked bool) {
	if provider == nil {
		return nil, nil, false
	}
	defer func() {
		if recover() != nil {
			values = nil
			err = nil
			panicked = true
		}
	}()
	values, err = provider.Credentials(ctx, apiOperationID)
	values = append([]CredentialValue(nil), values...)
	return values, err, false
}

func bindCredential(
	operation runtimeOperation,
	values []CredentialValue,
	query *[]encodedQueryPair,
	headers *[]Header,
	cookies *[]string,
) error {
	auth := operation.auth
	countValid := false
	switch auth.mode {
	case generationapi.AuthNone:
		countValid = len(values) == 0
	case generationapi.AuthOptional:
		countValid = len(values) <= 1
	case generationapi.AuthRequired:
		countValid = len(values) == 1
	}
	if !countValid {
		return newRequestBuildError("credential_count_invalid", "/credentials", operation.id)
	}
	if len(values) == 0 {
		return nil
	}

	supplied := values[0]
	selected, found := auth.credentials[supplied.ID]
	if !found {
		return newRequestBuildError("credential_id_unknown", "/credentials", operation.id)
	}
	if supplied.Value == "" {
		return newRequestBuildError("credential_value_empty", "/credentials", operation.id)
	}

	switch selected.typeID {
	case generationapi.CredentialBearer:
		composed := "Bearer " + supplied.Value
		if !validLogicalHeaderValue(supplied.Value) || !validLogicalHeaderValue(composed) {
			return newRequestBuildError("credential_value_invalid", "/credentials", operation.id)
		}
		*headers = append(*headers, Header{Name: selected.name, Value: composed})
	case generationapi.CredentialAPIKey:
		switch selected.location {
		case generationapi.CredentialLocationHeader:
			if !validLogicalHeaderValue(supplied.Value) {
				return newRequestBuildError("credential_value_invalid", "/credentials", operation.id)
			}
			*headers = append(*headers, Header{Name: selected.name, Value: supplied.Value})
		case generationapi.CredentialLocationQuery:
			if !utf8.ValidString(supplied.Value) {
				return newRequestBuildError("credential_value_invalid", "/credentials", operation.id)
			}
			*query = append(*query, encodedQueryPair{name: encodeRFC3986(selected.name), value: encodeRFC3986(supplied.Value)})
		case generationapi.CredentialLocationCookie:
			if !validCookieValue(supplied.Value) {
				return newRequestBuildError("credential_value_invalid", "/credentials", operation.id)
			}
			*cookies = append(*cookies, selected.name+"="+supplied.Value)
		}
	case generationapi.CredentialSessionCookie:
		if !validCookieValue(supplied.Value) {
			return newRequestBuildError("credential_value_invalid", "/credentials", operation.id)
		}
		*cookies = append(*cookies, selected.name+"="+supplied.Value)
	}
	return nil
}

func newCredentialProviderError(apiOperationID string) *Error {
	err := newSDKError(
		codeCredentialProviderError,
		sdkErrorDomain,
		protocol.CategoryExternal,
		credentialProviderFailureMessage,
		ErrorDetails{reason: credentialProviderFailureReason},
	)
	err.apiOperationID = apiOperationID
	return err
}

func encodeRFC3986(value string) string {
	const hex = "0123456789ABCDEF"
	var result strings.Builder
	result.Grow(len(value))
	for index := 0; index < len(value); index++ {
		character := value[index]
		if unreservedByte(character) {
			result.WriteByte(character)
			continue
		}
		result.WriteByte('%')
		result.WriteByte(hex[character>>4])
		result.WriteByte(hex[character&0x0f])
	}
	return result.String()
}

func unreservedByte(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9' || character == '-' || character == '.' || character == '_' || character == '~'
}

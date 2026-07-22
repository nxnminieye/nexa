package api

import (
	_ "embed"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/cli/protocol"
)

const (
	remoteErrorAPIVersion = "nexa.dev/remote-error/v1"

	// RemoteErrorLimitsAPIVersion identifies the typed limits projection.
	RemoteErrorLimitsAPIVersion = "nexa.dev/remote-error-limits/v1"
)

var remoteErrorIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

//go:embed remote-error-v1.schema.json
var embeddedRemoteErrorSchema []byte

// RemoteErrorSpec defines one immutable remote error document.
type RemoteErrorSpec struct {
	Domain      string
	Code        string
	Message     string
	RequestID   string
	TraceID     string
	DetailsJSON []byte
}

// RemoteErrorLimitSet is the typed semantic limit contract for remote errors.
// Values returned by RemoteErrorLimits are independent copies.
type RemoteErrorLimitSet struct {
	APIVersion         string `json:"apiVersion"`
	IDBytes            int    `json:"idBytes"`
	MessageBytes       int    `json:"messageBytes"`
	DetailsBytes       int    `json:"detailsBytes"`
	DetailsDepth       int    `json:"detailsDepth"`
	DetailsMemberTotal int    `json:"detailsMemberTotal"`
}

// RemoteErrorLimits returns the immutable semantic limits consumed by runtimes.
func RemoteErrorLimits() RemoteErrorLimitSet {
	return RemoteErrorLimitSet{
		APIVersion:         RemoteErrorLimitsAPIVersion,
		IDBytes:            256,
		MessageBytes:       1024,
		DetailsBytes:       32 << 10,
		DetailsDepth:       16,
		DetailsMemberTotal: 256,
	}
}

// RemoteError is a validated remote error document.
type RemoteError struct {
	domain    string
	code      string
	message   string
	requestID string
	traceID   string
	details   []byte
}

// NewRemoteError validates and freezes a remote error document.
func NewRemoteError(spec RemoteErrorSpec) (RemoteError, error) {
	limits := RemoteErrorLimits()
	if !validRemoteErrorID(spec.Domain) {
		return RemoteError{}, newRemoteProtocolError("domain_invalid", "/domain")
	}
	if !validRemoteErrorID(spec.Code) {
		return RemoteError{}, newRemoteProtocolError("code_invalid", "/code")
	}
	if !validRemoteText(spec.Message, limits.MessageBytes, true) {
		return RemoteError{}, newRemoteProtocolError("message_invalid", "/message")
	}
	if !validOptionalRemoteID(spec.RequestID) {
		return RemoteError{}, newRemoteProtocolError("request_id_invalid", "/requestId")
	}
	if !validOptionalRemoteID(spec.TraceID) {
		return RemoteError{}, newRemoteProtocolError("trace_id_invalid", "/traceId")
	}

	var details []byte
	if spec.DetailsJSON != nil {
		if len(spec.DetailsJSON) > limits.DetailsBytes {
			return RemoteError{}, newRemoteProtocolError("details_size_limit_exceeded", "/details")
		}
		value, err := parseRemoteDetailsJSON(spec.DetailsJSON)
		if err != nil {
			return RemoteError{}, err
		}
		if _, ok := value.(requestObject); !ok {
			return RemoteError{}, newRemoteProtocolError("details_invalid", "/details")
		}
		raw := value.appendJSON(nil)
		canonical, err := jcs.Transform(raw)
		if err != nil {
			return RemoteError{}, newRemoteProtocolError("details_invalid", "/details")
		}
		if len(canonical) > limits.DetailsBytes {
			return RemoteError{}, newRemoteProtocolError("details_size_limit_exceeded", "/details")
		}
		depth, members := remoteDetailStats(value, 1)
		if depth > limits.DetailsDepth {
			return RemoteError{}, newRemoteProtocolError("details_depth_limit_exceeded", "/details")
		}
		if members > limits.DetailsMemberTotal {
			return RemoteError{}, newRemoteProtocolError("details_member_limit_exceeded", "/details")
		}
		details = append([]byte(nil), canonical...)
	}
	return RemoteError{
		domain:    spec.Domain,
		code:      spec.Code,
		message:   spec.Message,
		requestID: spec.RequestID,
		traceID:   spec.TraceID,
		details:   details,
	}, nil
}

// ParseRemoteError parses an exact nexa.dev/remote-error/v1 document.
// It enforces shared depth and node safety; standalone callers bound raw bytes
// according to their transport policy before calling it.
func ParseRemoteError(data []byte) (RemoteError, error) {
	value, err := parseRemoteDocumentJSON(data)
	if err != nil {
		return RemoteError{}, err
	}
	document, ok := value.(requestObject)
	if !ok {
		return RemoteError{}, newRemoteProtocolError("root_object_required", "")
	}
	allowed := map[string]struct{}{
		"apiVersion": {}, "domain": {}, "code": {}, "message": {},
		"requestId": {}, "traceId": {}, "details": {},
	}
	for _, member := range document {
		if _, ok := allowed[member.name]; !ok {
			return RemoteError{}, newRemoteProtocolError("document_unknown_field", "")
		}
	}
	version, ok := remoteStringField(document, "apiVersion")
	if !ok {
		return RemoteError{}, newRemoteProtocolError("document_invalid", "/apiVersion")
	}
	if version != remoteErrorAPIVersion {
		return RemoteError{}, newRemoteProtocolError("version_unsupported", "/apiVersion")
	}
	domain, ok := remoteStringField(document, "domain")
	if !ok {
		return RemoteError{}, newRemoteProtocolError("document_invalid", "/domain")
	}
	code, ok := remoteStringField(document, "code")
	if !ok {
		return RemoteError{}, newRemoteProtocolError("document_invalid", "/code")
	}
	message, ok := remoteStringField(document, "message")
	if !ok {
		return RemoteError{}, newRemoteProtocolError("document_invalid", "/message")
	}
	requestID, requestIDPresent, ok := remoteOptionalStringField(document, "requestId")
	if !ok || (requestIDPresent && requestID == "") {
		return RemoteError{}, newRemoteProtocolError("request_id_invalid", "/requestId")
	}
	traceID, traceIDPresent, ok := remoteOptionalStringField(document, "traceId")
	if !ok || (traceIDPresent && traceID == "") {
		return RemoteError{}, newRemoteProtocolError("trace_id_invalid", "/traceId")
	}
	detailsMember, detailsPresent := remoteMember(document, "details")
	if detailsPresent {
		if _, ok := detailsMember.value.(requestObject); !ok {
			return RemoteError{}, newRemoteProtocolError("details_invalid", "/details")
		}
	}
	spec := RemoteErrorSpec{
		Domain: domain, Code: code, Message: message,
		RequestID: requestID, TraceID: traceID,
	}
	if detailsPresent {
		spec.DetailsJSON = append([]byte(nil), data[detailsMember.start:detailsMember.end]...)
	}
	return NewRemoteError(spec)
}

// RemoteErrorSchema returns an independent copy of the structural wire schema.
func RemoteErrorSchema() []byte { return append([]byte(nil), embeddedRemoteErrorSchema...) }

// CanonicalJSON returns RFC 8785 canonical JSON for the remote error.
func (e RemoteError) CanonicalJSON() ([]byte, error) {
	limits := RemoteErrorLimits()
	if !validRemoteErrorID(e.domain) {
		return nil, newRemoteProtocolError("domain_invalid", "/domain")
	}
	if !validRemoteErrorID(e.code) {
		return nil, newRemoteProtocolError("code_invalid", "/code")
	}
	if !validRemoteText(e.message, limits.MessageBytes, true) {
		return nil, newRemoteProtocolError("message_invalid", "/message")
	}
	if !validOptionalRemoteID(e.requestID) || !validOptionalRemoteID(e.traceID) {
		return nil, newRemoteProtocolError("document_invalid", "")
	}
	document := requestObject{
		{name: "apiVersion", value: requestString(remoteErrorAPIVersion)},
		{name: "domain", value: requestString(e.domain)},
		{name: "code", value: requestString(e.code)},
		{name: "message", value: requestString(e.message)},
	}
	if e.requestID != "" {
		document = append(document, requestMember{name: "requestId", value: requestString(e.requestID)})
	}
	if e.traceID != "" {
		document = append(document, requestMember{name: "traceId", value: requestString(e.traceID)})
	}
	if e.details != nil {
		details, err := parseRemoteDetailsJSON(e.details)
		if err != nil {
			return nil, err
		}
		document = append(document, requestMember{name: "details", value: details})
	}
	return jcs.Transform(document.appendJSON(nil))
}

func (e RemoteError) Domain() string    { return e.domain }
func (e RemoteError) Code() string      { return e.code }
func (e RemoteError) Message() string   { return e.message }
func (e RemoteError) RequestID() string { return e.requestID }
func (e RemoteError) TraceID() string   { return e.traceID }

func (e RemoteError) DetailsJSON() ([]byte, bool) {
	if e.details == nil {
		return nil, false
	}
	return append([]byte(nil), e.details...), true
}

func parseRemoteDocumentJSON(data []byte) (requestValue, error) {
	return parseRemoteJSON(data, newRemoteDocumentProtocolError)
}

func parseRemoteDetailsJSON(data []byte) (requestValue, error) {
	return parseRemoteJSON(data, newRemoteDetailsProtocolError)
}

func parseRemoteJSON(data []byte, newError func(reason, pointer string) *Error) (requestValue, error) {
	limits := RuntimeLimits()
	parser := requestParser{
		data:      data,
		maxDepth:  limits.JSONDepth,
		maxNodes:  limits.JSONNodes,
		semantics: limits.JSONSemantics(),
		allowNull: true,
		newError:  newError,
	}
	value, err := parser.parseValue("", parser.semantics.RootDepth())
	if err != nil {
		return nil, err
	}
	parser.skipWhitespace()
	if parser.offset != len(data) {
		return nil, newError("trailing_input", "")
	}
	return value, nil
}

func newRemoteDocumentProtocolError(reason, pointer string) *Error {
	return newRemoteProtocolError(reason, safeRemoteDocumentPointer(pointer))
}

func newRemoteDetailsProtocolError(reason, _ string) *Error {
	return newRemoteProtocolError(reason, "/details")
}

func safeRemoteDocumentPointer(pointer string) string {
	for _, root := range []string{"/apiVersion", "/domain", "/code", "/message", "/requestId", "/traceId", "/details"} {
		if pointer == root || strings.HasPrefix(pointer, root+"/") {
			return root
		}
	}
	return ""
}

func validRemoteErrorID(value string) bool {
	return len(value) <= RemoteErrorLimits().IDBytes && remoteErrorIDPattern.MatchString(value)
}

func validOptionalRemoteID(value string) bool {
	return value == "" || validRemoteText(value, RemoteErrorLimits().IDBytes, false)
}

func validRemoteText(value string, maxBytes int, allowEmpty bool) bool {
	if (!allowEmpty && value == "") || len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func remoteField(document requestObject, name string) (requestValue, bool) {
	member, ok := remoteMember(document, name)
	if !ok {
		return nil, false
	}
	return member.value, true
}

func remoteMember(document requestObject, name string) (requestMember, bool) {
	for _, member := range document {
		if member.name == name {
			return member, true
		}
	}
	return requestMember{}, false
}

func remoteStringField(document requestObject, name string) (string, bool) {
	value, ok := remoteField(document, name)
	if !ok {
		return "", false
	}
	result, ok := value.(requestString)
	return string(result), ok
}

func remoteOptionalStringField(document requestObject, name string) (string, bool, bool) {
	value, present := remoteField(document, name)
	if !present {
		return "", false, true
	}
	result, ok := value.(requestString)
	return string(result), true, ok
}

func remoteDetailStats(value requestValue, depth int) (int, int) {
	maximumDepth := depth
	members := 0
	switch typed := value.(type) {
	case requestObject:
		members += len(typed)
		for _, member := range typed {
			childDepth, childMembers := remoteDetailStats(member.value, nestedContainerDepth(member.value, depth))
			if childDepth > maximumDepth {
				maximumDepth = childDepth
			}
			members += childMembers
		}
	case requestArray:
		for _, child := range typed {
			childDepth, childMembers := remoteDetailStats(child, nestedContainerDepth(child, depth))
			if childDepth > maximumDepth {
				maximumDepth = childDepth
			}
			members += childMembers
		}
	}
	return maximumDepth, members
}

func nestedContainerDepth(value requestValue, parentDepth int) int {
	switch value.(type) {
	case requestObject, requestArray:
		return parentDepth + 1
	default:
		return parentDepth
	}
}

func newRemoteProtocolError(reason, pointer string) *Error {
	return newSDKError(
		codeRemoteProtocolError,
		sdkErrorDomain,
		protocol.CategoryExternal,
		"remote error response is invalid",
		ErrorDetails{reason: reason, pointer: pointer},
	)
}

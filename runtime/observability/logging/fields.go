package logging

import (
	"log/slog"
	"unicode"
	"unicode/utf8"
)

const (
	FieldRequestID = "request.id"
	FieldTraceID   = "trace.id"
	FieldSpanID    = "span.id"
	FieldTenantID  = "tenant.id"
	FieldMemberID  = "member.id"
	FieldSessionID = "session.id"
)

// ContextFieldsSpec supplies optional correlation and consumer identity fields.
type ContextFieldsSpec struct {
	RequestID string
	TraceID   string
	SpanID    string
	TenantID  string
	MemberID  string
	SessionID string
}

// ContextFields is an immutable ordered set of validated slog attributes.
type ContextFields struct {
	attrs []slog.Attr
}

// NewContextFields validates and builds optional context attributes.
func NewContextFields(spec ContextFieldsSpec) (ContextFields, error) {
	fields := []struct {
		key     string
		value   string
		pointer string
		valid   func(string) bool
	}{
		{key: FieldRequestID, value: spec.RequestID, pointer: "/requestId", valid: validIdentifier},
		{key: FieldTraceID, value: spec.TraceID, pointer: "/traceId", valid: func(value string) bool { return validHexID(value, 32) }},
		{key: FieldSpanID, value: spec.SpanID, pointer: "/spanId", valid: func(value string) bool { return validHexID(value, 16) }},
		{key: FieldTenantID, value: spec.TenantID, pointer: "/tenantId", valid: validIdentifier},
		{key: FieldMemberID, value: spec.MemberID, pointer: "/memberId", valid: validIdentifier},
		{key: FieldSessionID, value: spec.SessionID, pointer: "/sessionId", valid: validIdentifier},
	}

	attrs := make([]slog.Attr, 0, len(fields))
	for _, field := range fields {
		if field.value == "" {
			continue
		}
		if !field.valid(field.value) {
			return ContextFields{}, invalid("field_invalid", field.pointer)
		}
		attrs = append(attrs, slog.String(field.key, field.value))
	}
	return ContextFields{attrs: attrs}, nil
}

// Attrs returns a defensive copy in stable field order.
func (f ContextFields) Attrs() []slog.Attr {
	return append([]slog.Attr(nil), f.attrs...)
}

func validIdentifier(value string) bool {
	if len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}

func validHexID(value string, size int) bool {
	if len(value) != size {
		return false
	}
	nonZero := false
	for index := 0; index < len(value); index++ {
		current := value[index]
		if !((current >= '0' && current <= '9') || (current >= 'a' && current <= 'f')) {
			return false
		}
		nonZero = nonZero || current != '0'
	}
	return nonZero
}

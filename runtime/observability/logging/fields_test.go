package logging

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestContextFieldsStableKeysOrderAndAbsentOmission(t *testing.T) {
	fields, err := NewContextFields(ContextFieldsSpec{
		RequestID: "request-1",
		TraceID:   "0123456789abcdef0123456789abcdef",
		SpanID:    "0123456789abcdef",
		TenantID:  "tenant-1",
		MemberID:  "member-1",
		SessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("NewContextFields() error = %v", err)
	}

	want := []slog.Attr{
		slog.String(FieldRequestID, "request-1"),
		slog.String(FieldTraceID, "0123456789abcdef0123456789abcdef"),
		slog.String(FieldSpanID, "0123456789abcdef"),
		slog.String(FieldTenantID, "tenant-1"),
		slog.String(FieldMemberID, "member-1"),
		slog.String(FieldSessionID, "session-1"),
	}
	assertAttrsEqual(t, fields.Attrs(), want)

	absent, err := NewContextFields(ContextFieldsSpec{
		TraceID:  "0123456789abcdef0123456789abcdef",
		MemberID: "member-2",
	})
	if err != nil {
		t.Fatalf("NewContextFields() error = %v", err)
	}
	assertAttrsEqual(t, absent.Attrs(), []slog.Attr{
		slog.String(FieldTraceID, "0123456789abcdef0123456789abcdef"),
		slog.String(FieldMemberID, "member-2"),
	})
}

func TestContextFieldsValidateIdentifiersByBytesAndUnicode(t *testing.T) {
	valid, err := NewContextFields(ContextFieldsSpec{RequestID: strings.Repeat("a", 256)})
	if err != nil || len(valid.Attrs()) != 1 {
		t.Fatalf("256-byte identifier = (%v, %v), want one attr and nil error", valid.Attrs(), err)
	}

	tests := []struct {
		name    string
		spec    ContextFieldsSpec
		pointer string
	}{
		{name: "over byte limit", spec: ContextFieldsSpec{RequestID: strings.Repeat("a", 257)}, pointer: "/requestId"},
		{name: "multi-byte over limit", spec: ContextFieldsSpec{TenantID: strings.Repeat("界", 86)}, pointer: "/tenantId"},
		{name: "invalid utf8", spec: ContextFieldsSpec{SessionID: string([]byte{0xff})}, pointer: "/sessionId"},
		{name: "control rune", spec: ContextFieldsSpec{TenantID: "tenant\n1"}, pointer: "/tenantId"},
		{name: "leading unicode whitespace", spec: ContextFieldsSpec{MemberID: "\u2003member"}, pointer: "/memberId"},
		{name: "trailing unicode whitespace", spec: ContextFieldsSpec{SessionID: "session\u00a0"}, pointer: "/sessionId"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewContextFields(test.spec)
			requireLoggingError(t, err, "field_invalid", test.pointer)
		})
	}
}

func TestContextFieldsValidateTraceAndSpanGrammar(t *testing.T) {
	tests := []struct {
		name    string
		spec    ContextFieldsSpec
		pointer string
	}{
		{name: "trace length", spec: ContextFieldsSpec{TraceID: strings.Repeat("a", 31)}, pointer: "/traceId"},
		{name: "trace uppercase", spec: ContextFieldsSpec{TraceID: "0123456789abcdef0123456789abcdeF"}, pointer: "/traceId"},
		{name: "trace non-hex", spec: ContextFieldsSpec{TraceID: "g123456789abcdef0123456789abcdef"}, pointer: "/traceId"},
		{name: "trace zero", spec: ContextFieldsSpec{TraceID: strings.Repeat("0", 32)}, pointer: "/traceId"},
		{name: "span length", spec: ContextFieldsSpec{SpanID: strings.Repeat("a", 15)}, pointer: "/spanId"},
		{name: "span uppercase", spec: ContextFieldsSpec{SpanID: "0123456789abcdeF"}, pointer: "/spanId"},
		{name: "span non-hex", spec: ContextFieldsSpec{SpanID: "g123456789abcdef"}, pointer: "/spanId"},
		{name: "span zero", spec: ContextFieldsSpec{SpanID: strings.Repeat("0", 16)}, pointer: "/spanId"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewContextFields(test.spec)
			requireLoggingError(t, err, "field_invalid", test.pointer)
		})
	}
}

func TestContextFieldsAttrsAreDefensiveAndConsumerLocal(t *testing.T) {
	first, err := NewContextFields(ContextFieldsSpec{TenantID: "tenant-a", MemberID: "member-a"})
	if err != nil {
		t.Fatalf("NewContextFields(first) error = %v", err)
	}
	second, err := NewContextFields(ContextFieldsSpec{TenantID: "tenant-b", MemberID: "member-b"})
	if err != nil {
		t.Fatalf("NewContextFields(second) error = %v", err)
	}

	attrs := first.Attrs()
	attrs[0] = slog.String("changed", "secret")
	attrs = append(attrs, slog.String("extra", "value"))
	assertAttrsEqual(t, first.Attrs(), []slog.Attr{
		slog.String(FieldTenantID, "tenant-a"),
		slog.String(FieldMemberID, "member-a"),
	})
	assertAttrsEqual(t, second.Attrs(), []slog.Attr{
		slog.String(FieldTenantID, "tenant-b"),
		slog.String(FieldMemberID, "member-b"),
	})
}

func requireLoggingError(t *testing.T, err error, reason, pointer string) *Error {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil")
	}
	var loggingError *Error
	if !errors.As(err, &loggingError) || loggingError == nil {
		t.Fatalf("error = %T %v, want *Error", err, err)
	}
	if loggingError.Code() != "logging_invalid" || loggingError.Reason() != reason || loggingError.Pointer() != pointer {
		t.Fatalf("error tuple = (%q, %q, %q)", loggingError.Code(), loggingError.Reason(), loggingError.Pointer())
	}
	if loggingError.Error() != "logging invalid" {
		t.Fatalf("Error() = %q, want safe diagnostic", loggingError.Error())
	}
	return loggingError
}

func assertAttrsEqual(t *testing.T, got, want []slog.Attr) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(attrs) = %d, want %d: %#v", len(got), len(want), got)
	}
	for index := range want {
		if !got[index].Equal(want[index]) {
			t.Fatalf("attrs[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}

package provenance

import (
	"fmt"
	"strings"
	"testing"
	"unicode"
)

func TestDomainSourceZeroAndExactRoundTrip(t *testing.T) {
	var zero DomainSource
	if got := zero.String(); got != "" {
		t.Fatalf("zero DomainSource.String() = %q, want empty", got)
	}
	if _, err := ParseDomainSource(zero.String()); err == nil {
		t.Fatal("zero DomainSource crossed a typed consumer boundary")
	}

	values := []string{
		"nexa.dev/ent-schema-meta/v1",
		"nexa.dev/ent-field-meta/v1",
		"nexa.dev/ent-crud/v1",
		"backend/core/rpc/ent/schema",
		"services/caf\u00e9/schema?literal=%2F",
		"metadata/\u200d/schema",
		strings.Repeat("a", 253) + "\u00e9",
		strings.Repeat("a", 254) + "\u00e9",
	}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			parsed, err := ParseDomainSource(value)
			if err != nil {
				t.Fatalf("ParseDomainSource(%q) error = %v", value, err)
			}
			if got := parsed.String(); got != value {
				t.Fatalf("round trip = %q, want byte-exact %q", got, value)
			}
		})
	}
	if got := len([]byte(values[len(values)-2])); got != 255 {
		t.Fatalf("255-byte fixture length = %d", got)
	}
	if got := len([]byte(values[len(values)-1])); got != MaxDomainSourceBytes {
		t.Fatalf("256-byte fixture length = %d", got)
	}
	if value := strings.Repeat("a", 255) + "\u00e9"; len([]byte(value)) != 257 {
		t.Fatalf("257-byte fixture length = %d", len([]byte(value)))
	} else if _, err := ParseDomainSource(value); err == nil {
		t.Fatal("257-byte DomainSource was accepted")
	}
}

func TestDomainSourceRejectsCcButAcceptsOtherUnicodeCategories(t *testing.T) {
	for _, table := range unicode.Cc.R16 {
		for character := table.Lo; character <= table.Hi; character += table.Stride {
			value := "schema/" + string(rune(character)) + "/value"
			t.Run(fmt.Sprintf("U+%04X", character), func(t *testing.T) {
				assertDomainSourceFailure(t, value, domainSourceControl)
			})
		}
	}
	for _, table := range unicode.Cc.R32 {
		for character := table.Lo; character <= table.Hi; character += table.Stride {
			value := "schema/" + string(rune(character)) + "/value"
			t.Run(fmt.Sprintf("U+%04X", character), func(t *testing.T) {
				assertDomainSourceFailure(t, value, domainSourceControl)
			})
		}
	}
	for _, value := range []string{
		"schema/\u200d/value", // Cf
		"schema/\u2028/value", // Zl
		"schema/\U0001f680/value",
	} {
		t.Run(value, func(t *testing.T) {
			parsed, err := ParseDomainSource(value)
			if err != nil || parsed.String() != value {
				t.Fatalf("non-Cc value rejected: %q, %v", parsed.String(), err)
			}
		})
	}
}

func TestDomainSourceFailurePrecedenceIsTotal(t *testing.T) {
	invalidUTF8 := string([]byte{0xff}) + strings.Repeat("a", MaxDomainSourceBytes+1)
	tests := []struct {
		name  string
		value string
		want  domainSourceFailure
	}{
		{name: "empty", value: "", want: domainSourceEmpty},
		{name: "invalid UTF-8 before byte limit", value: invalidUTF8, want: domainSourceInvalidUTF8},
		{name: "byte limit before NFC", value: strings.Repeat("a", MaxDomainSourceBytes) + "e\u0301", want: domainSourceTooLong},
		{name: "NFC before control", value: "e\u0301/\x00", want: domainSourceNonNFC},
		{name: "control before absolute", value: "\x00/absolute", want: domainSourceControl},
		{name: "absolute before volume", value: "/C:/outside", want: domainSourceAbsolute},
		{name: "volume before backslash", value: `C:\outside`, want: domainSourceVolume},
		{name: "backslash before segments", value: `backend\schema/../value`, want: domainSourceBackslash},
		{name: "first empty segment before later dot", value: "backend//./value", want: domainSourceEmptySegment},
		{name: "first dot segment before later empty", value: "backend/./value/", want: domainSourceDotSegment},
		{name: "first parent segment before trailing empty", value: "backend/../value/", want: domainSourceParentSegment},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertDomainSourceFailure(t, test.value, test.want)
		})
	}
}

func TestDomainSourceDoesNotRewriteInput(t *testing.T) {
	for _, value := range []string{
		" backend/schema ",
		"backend/%2F/schema",
		"backend/a:b/schema",
	} {
		parsed, err := ParseDomainSource(value)
		if err != nil {
			t.Fatalf("ParseDomainSource(%q) error = %v", value, err)
		}
		if parsed.String() != value {
			t.Fatalf("ParseDomainSource(%q) rewrote to %q", value, parsed.String())
		}
	}
}

func assertDomainSourceFailure(t *testing.T, value string, want domainSourceFailure) {
	t.Helper()
	parsed, err := ParseDomainSource(value)
	if parsed.String() != "" {
		t.Fatalf("failure returned DomainSource %q", parsed.String())
	}
	typed, ok := err.(*domainSourceError)
	if !ok {
		t.Fatalf("error type = %T, want private domainSourceError", err)
	}
	if typed.failure != want {
		t.Fatalf("failure = %v, want %v", typed.failure, want)
	}
}

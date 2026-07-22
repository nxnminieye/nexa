package protocol_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
)

func TestFailureEnvelopeRoundTrip(t *testing.T) {
	envelope := protocol.Failure("op_test", protocol.NewError(
		"fact_source_missing", "facts", protocol.CategoryInput,
		"services catalog is required", "provide the catalog path",
	))
	var output bytes.Buffer
	if err := protocol.Encode(&output, envelope, true); err != nil {
		t.Fatal(err)
	}
	var decoded protocol.Envelope
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.APIVersion != protocol.EnvelopeVersion || decoded.OK || decoded.Error == nil {
		t.Fatalf("unexpected envelope: %#v", decoded)
	}
}

func TestSuccessEnvelopePreservesCallerValues(t *testing.T) {
	result := map[string]any{"source": "caller"}
	envelope := protocol.Success("", result)

	if envelope.APIVersion != protocol.EnvelopeVersion || !envelope.OK {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
	if envelope.OperationID != "" {
		t.Fatalf("operation id = %q, want caller-provided empty value", envelope.OperationID)
	}
	if envelope.Result == nil || envelope.Error != nil {
		t.Fatalf("unexpected envelope payload: %#v", envelope)
	}
}

func TestEncodeCompactWritesSingleLine(t *testing.T) {
	var output bytes.Buffer
	if err := protocol.Encode(&output, protocol.Success("op_test", "ok"), true); err != nil {
		t.Fatal(err)
	}

	encoded := output.String()
	if !strings.HasSuffix(encoded, "\n") || strings.Count(encoded, "\n") != 1 {
		t.Fatalf("compact encoding must be one line plus one newline: %q", encoded)
	}
}

func TestEncodeIndentedWritesIndentedJSON(t *testing.T) {
	var output bytes.Buffer
	if err := protocol.Encode(&output, protocol.Success("op_test", "ok"), false); err != nil {
		t.Fatal(err)
	}

	encoded := output.String()
	if !strings.Contains(encoded, "\n  \"apiVersion\"") || !strings.HasSuffix(encoded, "\n") {
		t.Fatalf("expected indented JSON with trailing newline: %q", encoded)
	}
}

func TestFailureEnvelopeRetryableBytes(t *testing.T) {
	retryableError, err := protocol.NewErrorWithOptions(
		"service_unavailable", "runtime", protocol.CategoryUnavailable,
		"service is unavailable", "retry later", protocol.ErrorOptions{Retryable: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	nonRetryableError := protocol.NewError(
		"fact_source_missing", "facts", protocol.CategoryInput,
		"services catalog is required", "provide the catalog path",
	)

	tests := []struct {
		name     string
		compact  bool
		err      error
		expected string
	}{
		{
			name: "compact false", compact: true, err: nonRetryableError,
			expected: `{"apiVersion":"nexa.dev/cli-envelope/v1","ok":false,"operationId":"op_test","error":{"code":"fact_source_missing","domain":"facts","category":"input","message":"services catalog is required","recommendedAction":"provide the catalog path","retryable":false}}` + "\n",
		},
		{
			name: "compact true", compact: true, err: retryableError,
			expected: `{"apiVersion":"nexa.dev/cli-envelope/v1","ok":false,"operationId":"op_test","error":{"code":"service_unavailable","domain":"runtime","category":"unavailable","message":"service is unavailable","recommendedAction":"retry later","retryable":true}}` + "\n",
		},
		{
			name: "indented false", compact: false, err: nonRetryableError,
			expected: "{\n" +
				"  \"apiVersion\": \"nexa.dev/cli-envelope/v1\",\n" +
				"  \"ok\": false,\n" +
				"  \"operationId\": \"op_test\",\n" +
				"  \"error\": {\n" +
				"    \"code\": \"fact_source_missing\",\n" +
				"    \"domain\": \"facts\",\n" +
				"    \"category\": \"input\",\n" +
				"    \"message\": \"services catalog is required\",\n" +
				"    \"recommendedAction\": \"provide the catalog path\",\n" +
				"    \"retryable\": false\n" +
				"  }\n" +
				"}\n",
		},
		{
			name: "indented true", compact: false, err: retryableError,
			expected: "{\n" +
				"  \"apiVersion\": \"nexa.dev/cli-envelope/v1\",\n" +
				"  \"ok\": false,\n" +
				"  \"operationId\": \"op_test\",\n" +
				"  \"error\": {\n" +
				"    \"code\": \"service_unavailable\",\n" +
				"    \"domain\": \"runtime\",\n" +
				"    \"category\": \"unavailable\",\n" +
				"    \"message\": \"service is unavailable\",\n" +
				"    \"recommendedAction\": \"retry later\",\n" +
				"    \"retryable\": true\n" +
				"  }\n" +
				"}\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := protocol.Encode(&output, protocol.Failure("op_test", test.err), test.compact); err != nil {
				t.Fatal(err)
			}
			if got := output.String(); got != test.expected {
				t.Fatalf("encoded envelope:\n%s\nwant:\n%s", got, test.expected)
			}
		})
	}
}

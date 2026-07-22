package protocol_test

import (
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
)

func TestOperationIDGeneratorFunc(t *testing.T) {
	generator := protocol.OperationIDGeneratorFunc(func() (string, error) {
		return "op_injected", nil
	})

	var contract protocol.OperationIDGenerator = generator
	got, err := contract.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	if got != "op_injected" {
		t.Fatalf("operation id = %q, want %q", got, "op_injected")
	}
}

func TestOperationIDsAreUniqueAndWellFormed(t *testing.T) {
	generator := protocol.RandomOperationIDGenerator{}
	seen := make(map[string]struct{}, 256)

	for range 256 {
		operationID, err := generator.NewOperationID()
		if err != nil {
			t.Fatal(err)
		}
		if !protocol.IsValidOperationID(operationID) {
			t.Fatalf("operation id %q is not valid", operationID)
		}
		if _, duplicate := seen[operationID]; duplicate {
			t.Fatalf("duplicate operation id: %q", operationID)
		}
		seen[operationID] = struct{}{}
	}
}

func TestIsValidOperationID(t *testing.T) {
	tests := []struct {
		name        string
		operationID string
		want        bool
	}{
		{name: "generated shape", operationID: "op_0123456789abcdef0123456789abcdef", want: true},
		{name: "sentinel", operationID: protocol.SentinelOperationID, want: true},
		{name: "empty", operationID: "", want: false},
		{name: "short", operationID: "op_0123456789abcdef", want: false},
		{name: "long", operationID: "op_0123456789abcdef0123456789abcdef0", want: false},
		{name: "wrong prefix", operationID: "id_0123456789abcdef0123456789abcdef", want: false},
		{name: "uppercase hex", operationID: "op_0123456789ABCDEF0123456789ABCDEF", want: false},
		{name: "non hex", operationID: "op_0123456789abcdef0123456789abcdeg", want: false},
		{name: "leading whitespace", operationID: " op_0123456789abcdef0123456789abcdef", want: false},
		{name: "trailing newline", operationID: "op_0123456789abcdef0123456789abcdef\n", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := protocol.IsValidOperationID(tt.operationID); got != tt.want {
				t.Fatalf("IsValidOperationID(%q) = %v, want %v", tt.operationID, got, tt.want)
			}
		})
	}
}

func TestSentinelOperationIDIsStableAndValid(t *testing.T) {
	if protocol.SentinelOperationID != "op_00000000000000000000000000000000" {
		t.Fatalf("sentinel operation id = %q", protocol.SentinelOperationID)
	}
	if !protocol.IsValidOperationID(protocol.SentinelOperationID) {
		t.Fatalf("sentinel operation id is invalid: %q", protocol.SentinelOperationID)
	}
}

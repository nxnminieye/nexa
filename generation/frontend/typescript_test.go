package frontend_test

import (
	"math"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/frontend"
)

func TestTypeScriptScalarContract(t *testing.T) {
	for _, name := range []string{"int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float", "float32", "float64", "number"} {
		contract, ok := frontend.TypeScriptScalar(name)
		if !ok || !strings.Contains(contract.TypeScript, `__nexaScalar: "`+name+`"`) {
			t.Fatalf("%s mapping=%#v ok=%v", name, contract, ok)
		}
	}
	for name, want := range map[string]string{"string": "string", "bool": "boolean"} {
		contract, ok := frontend.TypeScriptScalar(name)
		if !ok || contract.TypeScript != want {
			t.Fatalf("%s mapping=%#v ok=%v", name, contract, ok)
		}
	}
	if _, ok := frontend.TypeScriptScalar("interface{}"); ok {
		t.Fatal("open scalar accepted")
	}
}

func TestValidateTypeScriptNumber(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  float64
		reason string
	}{
		{"float64", math.Inf(1), "typescript_number_nonfinite"},
		{"int64", 9007199254740992, "typescript_integer_unsafe"},
		{"uint64", -1, "typescript_number_out_of_range"},
		{"int8", 128, "typescript_number_out_of_range"},
		{"uint8", 256, "typescript_number_out_of_range"},
		{"string", 1, "typescript_scalar_unsupported"},
	} {
		err := frontend.ValidateTypeScriptNumber(tc.name, tc.value)
		assertReason(t, err, tc.reason)
	}
	for _, tc := range []struct {
		name  string
		value float64
	}{{"int64", 9007199254740991}, {"uint32", 4294967295}, {"float", 1.25}} {
		if err := frontend.ValidateTypeScriptNumber(tc.name, tc.value); err != nil {
			t.Fatalf("%s %v: %v", tc.name, tc.value, err)
		}
	}
}

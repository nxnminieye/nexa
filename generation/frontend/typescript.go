package frontend

import "math"

// TypeScriptScalarContract is the closed v1 scalar-to-TypeScript mapping.
type TypeScriptScalarContract struct {
	Name       string
	TypeScript string
	Integer    bool
	Unsigned   bool
	Minimum    *float64
	Maximum    *float64
}

var typeScriptScalarContracts = map[string]TypeScriptScalarContract{
	"string":  {Name: "string", TypeScript: "string"},
	"bool":    {Name: "bool", TypeScript: "boolean"},
	"int":     numberContract("int", true, false, nil, nil),
	"int8":    numberContract("int8", true, false, number(-128), number(127)),
	"int16":   numberContract("int16", true, false, number(-32768), number(32767)),
	"int32":   numberContract("int32", true, false, number(-2147483648), number(2147483647)),
	"int64":   numberContract("int64", true, false, nil, nil),
	"uint":    numberContract("uint", true, true, number(0), nil),
	"uint8":   numberContract("uint8", true, true, number(0), number(255)),
	"uint16":  numberContract("uint16", true, true, number(0), number(65535)),
	"uint32":  numberContract("uint32", true, true, number(0), number(4294967295)),
	"uint64":  numberContract("uint64", true, true, number(0), nil),
	"float":   numberContract("float", false, false, nil, nil),
	"float32": numberContract("float32", false, false, nil, nil),
	"float64": numberContract("float64", false, false, nil, nil),
	"number":  numberContract("number", false, false, nil, nil),
}

func number(value float64) *float64 { return &value }

func numberContract(name string, integer, unsigned bool, minimum, maximum *float64) TypeScriptScalarContract {
	return TypeScriptScalarContract{Name: name, TypeScript: "number & { readonly __nexaScalar: \"" + name + "\" }", Integer: integer, Unsigned: unsigned, Minimum: minimum, Maximum: maximum}
}

// TypeScriptScalar returns the exact, non-collapsing v1 mapping for a scalar.
func TypeScriptScalar(name string) (TypeScriptScalarContract, bool) {
	contract, ok := typeScriptScalarContracts[name]
	if contract.Minimum != nil {
		minimum := *contract.Minimum
		contract.Minimum = &minimum
	}
	if contract.Maximum != nil {
		maximum := *contract.Maximum
		contract.Maximum = &maximum
	}
	return contract, ok
}

// ValidateTypeScriptNumber enforces the runtime JSON-number contract before a
// numeric DTO value is admitted to generated TypeScript code.
func ValidateTypeScriptNumber(name string, value float64) error {
	contract, ok := typeScriptScalarContracts[name]
	if !ok || contract.TypeScript == "string" || contract.TypeScript == "boolean" {
		return buildError("typescript_scalar_unsupported", "/value", "scalar has no numeric TypeScript mapping")
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return buildError("typescript_number_nonfinite", "/value", "numeric DTO values must be finite")
	}
	if contract.Integer && (math.Trunc(value) != value || math.Abs(value) > 9007199254740991) {
		return buildError("typescript_integer_unsafe", "/value", "integer DTO values must be safe JavaScript integers")
	}
	if contract.Minimum != nil && value < *contract.Minimum || contract.Maximum != nil && value > *contract.Maximum {
		return buildError("typescript_number_out_of_range", "/value", "numeric DTO value is outside its exact scalar range")
	}
	return nil
}

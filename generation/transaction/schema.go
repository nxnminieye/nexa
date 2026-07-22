package transaction

import _ "embed"

//go:embed generation-plan-v2.schema.json
var planSchema []byte

//go:embed generation-result-v1.schema.json
var resultSchema []byte

func PlanSchema() []byte   { return append([]byte(nil), planSchema...) }
func ResultSchema() []byte { return append([]byte(nil), resultSchema...) }

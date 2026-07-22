package api

import _ "embed"

const RuntimeContractLimitsAPIVersion = "nexa.dev/runtime-contract-limits/v1"

// RuntimeContractLimitSet is the typed public resource boundary for contracts.
type RuntimeContractLimitSet struct {
	APIVersion string `json:"apiVersion"`
	RawBytes   int    `json:"rawBytes"`
	JSONDepth  int    `json:"jsonDepth"`
	JSONNodes  int    `json:"jsonNodes"`
}

//go:embed runtime-contract-limits-v1.schema.json
var embeddedRuntimeContractLimitsSchema []byte

//go:embed runtime-limits-v1.schema.json
var embeddedRuntimeLimitsSchema []byte

//go:embed remote-error-limits-v1.schema.json
var embeddedRemoteErrorLimitsSchema []byte

func RuntimeContractLimits() RuntimeContractLimitSet {
	return RuntimeContractLimitSet{
		APIVersion: RuntimeContractLimitsAPIVersion,
		RawBytes:   16 << 20,
		JSONDepth:  16,
		JSONNodes:  262_144,
	}
}

func RuntimeContractLimitsSchema() []byte {
	return append([]byte(nil), embeddedRuntimeContractLimitsSchema...)
}

func RuntimeLimitsSchema() []byte {
	return append([]byte(nil), embeddedRuntimeLimitsSchema...)
}

func RemoteErrorLimitsSchema() []byte {
	return append([]byte(nil), embeddedRemoteErrorLimitsSchema...)
}

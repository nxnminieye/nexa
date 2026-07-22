// Package apigo plans verified API Go artifacts through a consumer-pinned helper.
package apigo

import (
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	generatedOwner   = "nexa.dev/generator/api-go/v1"
	manualOwner      = "nexa.dev/generator/api-go-manual/v1"
	generatorID      = "api-go"
	generatorVersion = "v1.0.0"

	resultAPIVersion = "nexa.dev/api-go-result/v1"
	resultKind       = "APIGoResult"
	roleGenerated    = "generated"
	roleManual       = "manual"
)

type Options struct {
	CoreServiceID  string
	RepositoryRoot string
	StagingRoot    string
	Emit           func(string, []byte) error
	Tool           toolchain.Tool
	Runner         toolchain.Runner
	Environment    []toolchain.EnvVar
	Sources        []provenance.Source
}

type resultDocument struct {
	APIVersion    string           `json:"apiVersion"`
	Kind          string           `json:"kind"`
	CoreServiceID string           `json:"coreServiceId"`
	InputDigest   string           `json:"inputDigest"`
	GoTestPassed  bool             `json:"goTestPassed"`
	Artifacts     []resultArtifact `json:"artifacts"`
}

type resultArtifact struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Role   string `json:"role"`
	Digest string `json:"digest"`
}

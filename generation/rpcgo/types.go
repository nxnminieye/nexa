// Package rpcgo plans verified RPC Go artifacts through a consumer-pinned tool.
package rpcgo

import (
	"github.com/nxnminieye/nexa/generation/toolchain"
)

const generatedOwner = "nexa.dev/generator/rpc-go/v1"
const manualOwner = "nexa.dev/generator/rpc-go-manual/v1"
const generatorID = "rpc-go"
const generatorVersion = "v1.0.0"

type Options struct {
	ServiceID      string
	RepositoryRoot string
	StagingRoot    string
	Emit           func(string, []byte) error
	Tool           toolchain.Tool
	Runner         toolchain.Runner
	Environment    []toolchain.EnvVar
}

type resultDocument struct {
	APIVersion   string           `json:"apiVersion"`
	Kind         string           `json:"kind"`
	ServiceID    string           `json:"serviceId"`
	InputDigest  string           `json:"inputDigest"`
	GoTestPassed bool             `json:"goTestPassed"`
	Artifacts    []resultArtifact `json:"artifacts"`
}

type resultArtifact struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Role   string `json:"role"`
	Digest string `json:"digest"`
}

const resultAPIVersion = "nexa.dev/rpc-go-result/v1"
const resultKind = "RPCGoResult"
const roleGenerated = "generated"
const roleManual = "manual"

package api

import (
	"github.com/nxnminieye/nexa/cli/protocol"
	generationapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

const RuntimeContractAPIVersion = "nexa.dev/runtime-contract/v1"

const runtimeContractDigestPlaceholder = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

const (
	codeRuntimeContractInvalid         = "runtime_contract_invalid"
	codeRuntimeContractUnrepresentable = "runtime_contract_unrepresentable"
)

// RuntimeContract is one immutable runtime-only projection of an API Manifest.
type RuntimeContract struct {
	model *runtimeModel
}

// RuntimeContractTrace identifies the Manifest projection used to build a contract.
type RuntimeContractTrace struct {
	apiManifestVersion         string
	apiManifestCanonicalDigest provenance.Digest
	sourceDigest               provenance.Digest
}

func (c RuntimeContract) Trace() RuntimeContractTrace {
	if c.model == nil {
		return RuntimeContractTrace{}
	}
	manifestDigest, _ := provenance.ParseDigest(c.model.trace.APIManifestCanonicalDigest)
	sourceDigest, _ := provenance.ParseDigest(c.model.trace.SourceDigest)
	return RuntimeContractTrace{
		apiManifestVersion:         c.model.trace.APIManifestVersion,
		apiManifestCanonicalDigest: manifestDigest,
		sourceDigest:               sourceDigest,
	}
}

func (t RuntimeContractTrace) APIManifestVersion() string { return t.apiManifestVersion }
func (t RuntimeContractTrace) APIManifestCanonicalDigest() provenance.Digest {
	return t.apiManifestCanonicalDigest
}
func (t RuntimeContractTrace) SourceDigest() provenance.Digest { return t.sourceDigest }

// BuildRuntimeContract compiles typed Manifest accessors into the runtime DAG.
func BuildRuntimeContract(manifest generationapi.Manifest) (RuntimeContract, error) {
	if manifest.APIVersion() == "" {
		return RuntimeContract{}, newConstructorError(
			codeAPIManifestRequired,
			"API manifest is required",
			"manifest_required",
			"/manifest",
		)
	}
	model, err := buildRuntimeModel(manifest, runtimeContractTraceDocument{
		APIManifestVersion:         manifest.APIVersion(),
		APIManifestCanonicalDigest: runtimeContractDigestPlaceholder,
		SourceDigest:               manifest.SourceDigest().String(),
	})
	if err != nil {
		return RuntimeContract{}, err
	}
	if issue := validateRuntimeModel(model); issue != nil {
		return RuntimeContract{}, newRuntimeContractInvalid(issue.reason, issue.pointer)
	}
	limits := RuntimeContractLimits()
	stats := measureRuntimeContract(model)
	if stats.nodes > limits.JSONNodes {
		return RuntimeContract{}, newRuntimeContractUnrepresentable("runtime_contract_node_limit_exceeded")
	}
	if stats.rawBytes > limits.RawBytes {
		return RuntimeContract{}, newRuntimeContractUnrepresentable("runtime_contract_raw_limit_exceeded")
	}
	manifestJSON, err := manifest.CanonicalJSON()
	if err != nil {
		return RuntimeContract{}, newRuntimeContractInvalid("runtime_contract_schema_invalid", "/runtimeContract")
	}
	model.trace.APIManifestCanonicalDigest = provenance.SHA256(manifestJSON).String()
	return RuntimeContract{model: model}, nil
}

// CanonicalJSON returns independent RFC 8785 bytes for the runtime contract.
func (c RuntimeContract) CanonicalJSON() ([]byte, error) {
	if c.model == nil {
		return nil, newRuntimeContractInvalid("runtime_contract_schema_invalid", "/runtimeContract")
	}
	if issue := validateRuntimeModel(c.model); issue != nil {
		return nil, newRuntimeContractInvalid(issue.reason, issue.pointer)
	}
	return c.canonicalJSONUnchecked()
}

func (c RuntimeContract) canonicalJSONUnchecked() ([]byte, error) {
	return encodeRuntimeContract(c.model)
}

type runtimeContractTraceDocument struct {
	APIManifestVersion         string `json:"apiManifestVersion"`
	APIManifestCanonicalDigest string `json:"apiManifestCanonicalDigest"`
	SourceDigest               string `json:"sourceDigest"`
}

type runtimeSchemaDocument struct {
	Kind   generationapi.SchemaKind         `json:"kind"`
	Items  *int                             `json:"items,omitempty"`
	Fields *map[string]runtimeFieldDocument `json:"fields,omitempty"`
}

type runtimeFieldDocument struct {
	Required bool `json:"required"`
	Schema   int  `json:"schema"`
}

func runtimeSchemaToDocument(schema runtimeSchema) runtimeSchemaDocument {
	document := runtimeSchemaDocument{Kind: schema.kind}
	switch schema.kind {
	case generationapi.SchemaArray:
		items := schema.items
		document.Items = &items
	case generationapi.SchemaObject:
		fields := make(map[string]runtimeFieldDocument, len(schema.fields))
		for name, field := range schema.fields {
			fields[name] = runtimeFieldDocument{Required: field.required, Schema: field.schema}
		}
		document.Fields = &fields
	}
	return document
}

func newRuntimeContractInvalid(reason, pointer string) *Error {
	return newSDKError(
		codeRuntimeContractInvalid,
		sdkErrorDomain,
		protocol.CategoryInput,
		"runtime contract is invalid",
		ErrorDetails{reason: reason, pointer: pointer},
	)
}

func newRuntimeContractUnrepresentable(reason string) *Error {
	return newSDKError(
		codeRuntimeContractUnrepresentable,
		sdkErrorDomain,
		protocol.CategoryInput,
		"runtime contract exceeds runtime SDK capability",
		ErrorDetails{reason: reason, pointer: "/manifest"},
	)
}

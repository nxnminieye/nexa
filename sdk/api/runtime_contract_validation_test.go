package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	generationapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	runtimeContractOversizedIndex         = "9223372036854776000"
	runtimeContractOversizedNegativeIndex = "-9223372036854776000"
)

func TestRuntimeContractNativeRelationMatrix(t *testing.T) {
	simple := mustRuntimeContractJSON(t, runtimeContractManifest(t))
	relation := mustRuntimeContractJSON(t, runtimeContractRelationManifest(t))
	if _, err := ParseRuntimeContract(relation); err != nil {
		t.Fatalf("owner-built relation contract does not parse: %v", err)
	}

	tests := []struct {
		name    string
		base    []byte
		mutate  func(map[string]any)
		reason  string
		pointer string
	}{
		{
			name: "array schema edge self", base: simple,
			mutate: func(root map[string]any) {
				schemas := runtimeContractSchemas(root)
				schemas[2] = map[string]any{"kind": "array", "items": json.Number("2")}
			},
			reason: "runtime_schema_index_invalid", pointer: "/schemas/2/items",
		},
		{
			name: "object schema edge forward", base: simple,
			mutate: func(root map[string]any) {
				field := runtimeContractSchemas(root)[1].(map[string]any)["fields"].(map[string]any)["id"].(map[string]any)
				field["schema"] = json.Number("2")
			},
			reason: "runtime_schema_index_invalid", pointer: "/schemas/1/fields/id/schema",
		},
		{
			name: "object schema edge negative", base: simple,
			mutate: func(root map[string]any) {
				field := runtimeContractSchemas(root)[1].(map[string]any)["fields"].(map[string]any)["id"].(map[string]any)
				field["schema"] = json.Number("-1")
			},
			reason: "runtime_schema_index_invalid", pointer: "/schemas/1/fields/id/schema",
		},
		{
			name: "array schema edge out of range", base: simple,
			mutate: func(root map[string]any) {
				schemas := runtimeContractSchemas(root)
				schemas[2] = map[string]any{"kind": "array", "items": json.Number("99")}
			},
			reason: "runtime_schema_index_invalid", pointer: "/schemas/2/items",
		},
		{
			name: "array schema edge oversized integer", base: simple,
			mutate: func(root map[string]any) {
				schemas := runtimeContractSchemas(root)
				schemas[2] = map[string]any{"kind": "array", "items": json.Number(runtimeContractOversizedIndex)}
			},
			reason: "runtime_schema_index_invalid", pointer: "/schemas/2/items",
		},
		{
			name: "array schema edge oversized negative integer", base: simple,
			mutate: func(root map[string]any) {
				schemas := runtimeContractSchemas(root)
				schemas[2] = map[string]any{"kind": "array", "items": json.Number(runtimeContractOversizedNegativeIndex)}
			},
			reason: "runtime_schema_index_invalid", pointer: "/schemas/2/items",
		},
		{
			name: "object schema edge oversized integer", base: simple,
			mutate: func(root map[string]any) {
				field := runtimeContractSchemas(root)[1].(map[string]any)["fields"].(map[string]any)["id"].(map[string]any)
				field["schema"] = json.Number(runtimeContractOversizedIndex)
			},
			reason: "runtime_schema_index_invalid", pointer: "/schemas/1/fields/id/schema",
		},
		{
			name: "schema semantic duplicate", base: simple,
			mutate: func(root map[string]any) {
				root["schemas"] = append(runtimeContractSchemas(root), map[string]any{"kind": "integer"})
			},
			reason: "runtime_schema_duplicate", pointer: "/schemas/4",
		},
		{
			name: "request root out of range", base: simple,
			mutate: func(root map[string]any) {
				runtimeContractOperation(root, "sample.get")["request"].(map[string]any)["schema"] = json.Number("99")
			},
			reason: "runtime_operation_schema_index_invalid", pointer: "/operations/sample.get/request/schema",
		},
		{
			name: "response root out of range", base: simple,
			mutate: func(root map[string]any) {
				runtimeContractOperation(root, "sample.get")["response"].(map[string]any)["schema"] = json.Number("99")
			},
			reason: "runtime_operation_schema_index_invalid", pointer: "/operations/sample.get/response/schema",
		},
		{
			name: "request root oversized integer", base: simple,
			mutate: func(root map[string]any) {
				runtimeContractOperation(root, "sample.get")["request"].(map[string]any)["schema"] = json.Number(runtimeContractOversizedIndex)
			},
			reason: "runtime_operation_schema_index_invalid", pointer: "/operations/sample.get/request/schema",
		},
		{
			name: "response root oversized integer", base: simple,
			mutate: func(root map[string]any) {
				runtimeContractOperation(root, "sample.get")["response"].(map[string]any)["schema"] = json.Number(runtimeContractOversizedIndex)
			},
			reason: "runtime_operation_schema_index_invalid", pointer: "/operations/sample.get/response/schema",
		},
		{
			name: "request root scalar", base: simple,
			mutate: func(root map[string]any) {
				runtimeContractOperation(root, "sample.get")["request"].(map[string]any)["schema"] = json.Number("0")
			},
			reason: "runtime_request_schema_kind_invalid", pointer: "/operations/sample.get/request/schema",
		},
		{
			name: "binding unresolved", base: simple,
			mutate: func(root map[string]any) {
				bindings := runtimeContractOperation(root, "sample.get")["request"].(map[string]any)["bindings"].(map[string]any)
				bindings["other"] = map[string]any{"in": "query", "name": "other"}
			},
			reason: "runtime_binding_field_unresolved", pointer: "/operations/sample.get/request/bindings/other",
		},
		{
			name: "binding missing", base: simple,
			mutate: func(root map[string]any) {
				bindings := runtimeContractOperation(root, "sample.get")["request"].(map[string]any)["bindings"].(map[string]any)
				delete(bindings, "id")
			},
			reason: "runtime_binding_field_missing", pointer: "/operations/sample.get/request/bindings",
		},
		{
			name: "non body binding is not scalar", base: relation,
			mutate: func(root map[string]any) {
				binding := runtimeContractBinding(root, "sample.call", "payload")
				binding["in"], binding["name"] = "query", "payload"
			},
			reason: "runtime_binding_schema_kind_invalid", pointer: "/operations/sample.call/request/bindings/payload",
		},
		{
			name: "path field optional", base: simple,
			mutate: func(root map[string]any) {
				field := runtimeContractSchemas(root)[1].(map[string]any)["fields"].(map[string]any)["id"].(map[string]any)
				field["required"] = false
			},
			reason: "runtime_path_field_optional", pointer: "/operations/sample.get/request/bindings/id",
		},
		{
			name: "path binding set mismatch", base: simple,
			mutate: func(root map[string]any) {
				runtimeContractOperation(root, "sample.get")["pathSegments"] = []any{map[string]any{"literal": "/items"}}
			},
			reason: "runtime_path_binding_mismatch", pointer: "/operations/sample.get/pathSegments",
		},
		{
			name: "path binding name differs", base: simple,
			mutate: func(root map[string]any) { runtimeContractBinding(root, "sample.get", "id")["name"] = "other" },
			reason: "runtime_path_binding_name_invalid", pointer: "/operations/sample.get/request/bindings/id/name",
		},
		{
			name: "path shape invalid", base: simple,
			mutate: func(root map[string]any) {
				runtimeContractOperation(root, "sample.get")["pathSegments"] = []any{
					map[string]any{"literal": "/items//"}, map[string]any{"field": "id"},
				}
			},
			reason: "runtime_path_invalid", pointer: "/operations/sample.get/pathSegments",
		},
		{
			name: "binding wire target duplicate", base: relation,
			mutate: func(root map[string]any) {
				binding := runtimeContractBinding(root, "sample.call", "b")
				binding["in"], binding["name"] = "query", "qa"
			},
			reason: "runtime_binding_wire_target_duplicate", pointer: "/operations/sample.call/request/bindings/b/name",
		},
		{
			name: "content type binding reserved", base: relation,
			mutate: func(root map[string]any) { runtimeContractBinding(root, "sample.call", "b")["name"] = "content-type" },
			reason: "runtime_header_name_reserved", pointer: "/operations/sample.call/request/bindings/b/name",
		},
		{
			name: "auth cardinality", base: simple,
			mutate: func(root map[string]any) {
				runtimeContractOperation(root, "sample.get")["auth"].(map[string]any)["mode"] = "none"
			},
			reason: "runtime_credential_combination_invalid", pointer: "/operations/sample.get/auth/credentials",
		},
		{
			name: "bearer location", base: simple,
			mutate: func(root map[string]any) {
				credential := runtimeContractCredential(root, "sample.get", "access")
				credential["in"] = "query"
			},
			reason: "runtime_credential_combination_invalid", pointer: "/operations/sample.get/auth/credentials/access/in",
		},
		{
			name: "bearer name", base: simple,
			mutate: func(root map[string]any) { runtimeContractCredential(root, "sample.get", "access")["name"] = "x-token" },
			reason: "runtime_credential_combination_invalid", pointer: "/operations/sample.get/auth/credentials/access/name",
		},
		{
			name: "credential wire duplicate", base: relation,
			mutate: func(root map[string]any) {
				credential := runtimeContractCredential(root, "sample.call", "key2")
				credential["in"], credential["name"] = "header", "x-key1"
			},
			reason: "runtime_credential_wire_target_duplicate", pointer: "/operations/sample.call/auth/credentials/key2/name",
		},
		{
			name: "cookie credentials share direct target", base: relation,
			mutate: func(root map[string]any) {
				first := runtimeContractCredential(root, "sample.call", "key1")
				first["in"], first["name"] = "cookie", "session"
				second := runtimeContractCredential(root, "sample.call", "key2")
				second["in"], second["name"] = "cookie", "session"
			},
			reason: "runtime_credential_wire_target_duplicate", pointer: "/operations/sample.call/auth/credentials/key2/name",
		},
		{
			name: "cookie credential conflicts with ordinary cookie header binding", base: relation,
			mutate: func(root map[string]any) {
				binding := runtimeContractBinding(root, "sample.call", "b")
				binding["in"], binding["name"] = "header", "cookie"
				credential := runtimeContractCredential(root, "sample.call", "key1")
				credential["in"], credential["name"] = "cookie", "session"
			},
			reason: "runtime_credential_binding_conflict", pointer: "/operations/sample.call/auth/credentials/key1/name",
		},
		{
			name: "credential binding conflict", base: relation,
			mutate: func(root map[string]any) { runtimeContractCredential(root, "sample.call", "key1")["name"] = "x-b" },
			reason: "runtime_credential_binding_conflict", pointer: "/operations/sample.call/auth/credentials/key1/name",
		},
		{
			name: "permission without auth", base: simple,
			mutate: func(root map[string]any) {
				auth := runtimeContractOperation(root, "sample.get")["auth"].(map[string]any)
				auth["mode"], auth["credentials"] = "none", map[string]any{}
			},
			reason: "runtime_permission_auth_conflict", pointer: "/operations/sample.get/permission",
		},
		{
			name: "capability version", base: simple,
			mutate: func(root map[string]any) {
				runtimeContractOperation(root, "sample.get")["capability"].(map[string]any)["apiVersion"] = "sample/api/v0"
			},
			reason: "runtime_capability_version_invalid", pointer: "/operations/sample.get/capability/apiVersion",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := mutateRuntimeContract(t, test.base, test.mutate)
			requireRuntimeContractError(t, mustRuntimeContractParseError(candidate), codeRuntimeContractInvalid,
				"runtime contract is invalid", test.reason, test.pointer)
		})
	}
}

func TestRuntimeContractNativeFirstErrorPrecedence(t *testing.T) {
	simple := mustRuntimeContractJSON(t, runtimeContractManifest(t))
	relation := mustRuntimeContractJSON(t, runtimeContractRelationManifest(t))
	tests := []struct {
		name    string
		base    []byte
		mutate  func(map[string]any)
		reason  string
		pointer string
	}{
		{
			name: "schema edge before duplicate", base: simple,
			mutate: func(root map[string]any) {
				schemas := runtimeContractSchemas(root)
				schemas[3] = map[string]any{"fields": map[string]any{"name": map[string]any{"required": true, "schema": json.Number("3")}}, "kind": "object"}
				root["schemas"] = append(schemas, map[string]any{"kind": "integer"})
			},
			reason: "runtime_schema_index_invalid", pointer: "/schemas/3/fields/name/schema",
		},
		{
			name: "oversized schema edge before oversized operation root", base: simple,
			mutate: func(root map[string]any) {
				schemas := runtimeContractSchemas(root)
				schemas[2] = map[string]any{"kind": "array", "items": json.Number(runtimeContractOversizedIndex)}
				runtimeContractOperation(root, "sample.get")["request"].(map[string]any)["schema"] = json.Number(runtimeContractOversizedIndex)
			},
			reason: "runtime_schema_index_invalid", pointer: "/schemas/2/items",
		},
		{
			name: "request root before bindings", base: simple,
			mutate: func(root map[string]any) {
				operation := runtimeContractOperation(root, "sample.get")
				operation["request"].(map[string]any)["schema"] = json.Number("99")
				operation["request"].(map[string]any)["bindings"].(map[string]any)["other"] = map[string]any{"in": "query", "name": "other"}
			},
			reason: "runtime_operation_schema_index_invalid", pointer: "/operations/sample.get/request/schema",
		},
		{
			name: "operations use lexical ID order", base: simple,
			mutate: func(root map[string]any) {
				operations := root["operations"].(map[string]any)
				var clone map[string]any
				encoded, _ := json.Marshal(operations["sample.get"])
				_ = json.Unmarshal(encoded, &clone)
				clone["request"].(map[string]any)["schema"] = float64(99)
				operations["a.call"] = clone
				operations["sample.get"].(map[string]any)["response"].(map[string]any)["schema"] = json.Number("99")
			},
			reason: "runtime_operation_schema_index_invalid", pointer: "/operations/a.call/request/schema",
		},
		{
			name: "unresolved before missing coverage", base: simple,
			mutate: func(root map[string]any) {
				bindings := runtimeContractOperation(root, "sample.get")["request"].(map[string]any)["bindings"].(map[string]any)
				delete(bindings, "id")
				bindings["other"] = map[string]any{"in": "query", "name": "other"}
			},
			reason: "runtime_binding_field_unresolved", pointer: "/operations/sample.get/request/bindings/other",
		},
		{
			name: "binding scalar before wire duplicate", base: relation,
			mutate: func(root map[string]any) {
				payload := runtimeContractBinding(root, "sample.call", "payload")
				payload["in"], payload["name"] = "query", "qa"
			},
			reason: "runtime_binding_schema_kind_invalid", pointer: "/operations/sample.call/request/bindings/payload",
		},
		{
			name: "auth cardinality before credential details", base: simple,
			mutate: func(root map[string]any) {
				auth := runtimeContractOperation(root, "sample.get")["auth"].(map[string]any)
				auth["mode"] = "none"
				runtimeContractCredential(root, "sample.get", "access")["in"] = "query"
			},
			reason: "runtime_credential_combination_invalid", pointer: "/operations/sample.get/auth/credentials",
		},
		{
			name: "credential combination before wire duplicate", base: relation,
			mutate: func(root map[string]any) {
				first := runtimeContractCredential(root, "sample.call", "key1")
				first["type"], first["in"], first["name"] = "bearer", "query", "key1"
				second := runtimeContractCredential(root, "sample.call", "key2")
				second["name"] = "key1"
			},
			reason: "runtime_credential_combination_invalid", pointer: "/operations/sample.call/auth/credentials/key1/in",
		},
		{
			name: "permission before capability", base: simple,
			mutate: func(root map[string]any) {
				operation := runtimeContractOperation(root, "sample.get")
				operation["auth"] = map[string]any{"mode": "none", "credentials": map[string]any{}}
				operation["capability"].(map[string]any)["apiVersion"] = "sample/api/v0"
			},
			reason: "runtime_permission_auth_conflict", pointer: "/operations/sample.get/permission",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := mutateRuntimeContract(t, test.base, test.mutate)
			requireRuntimeContractError(t, mustRuntimeContractParseError(candidate), codeRuntimeContractInvalid,
				"runtime contract is invalid", test.reason, test.pointer)
		})
	}
}

func TestRuntimeContractStrictResourceBoundaries(t *testing.T) {
	limits := RuntimeContractLimits()

	exactDepth := nestedRuntimeContractJSON(limits.JSONDepth)
	requireRuntimeContractError(t, mustRuntimeContractParseError(exactDepth), codeRuntimeContractInvalid,
		"runtime contract is invalid", "runtime_contract_schema_invalid", "/runtimeContract")
	overDepth := nestedRuntimeContractJSON(limits.JSONDepth + 1)
	requireRuntimeContractError(t, mustRuntimeContractParseError(overDepth), codeRuntimeContractInvalid,
		"runtime contract is invalid", "depth_limit_exceeded", "/runtimeContract")

	exactNodes := flatRuntimeContractJSON(limits.JSONNodes - 1)
	requireRuntimeContractError(t, mustRuntimeContractParseError(exactNodes), codeRuntimeContractInvalid,
		"runtime contract is invalid", "runtime_contract_schema_invalid", "/runtimeContract")
	overNodes := flatRuntimeContractJSON(limits.JSONNodes)
	requireRuntimeContractError(t, mustRuntimeContractParseError(overNodes), codeRuntimeContractInvalid,
		"runtime contract is invalid", "node_limit_exceeded", "/runtimeContract")

	exactRaw := bytes.Repeat([]byte{' '}, limits.RawBytes)
	requireRuntimeContractError(t, mustRuntimeContractParseError(exactRaw), codeRuntimeContractInvalid,
		"runtime contract is invalid", "invalid_json", "/runtimeContract")
	overRaw := append(exactRaw, ' ')
	requireRuntimeContractError(t, mustRuntimeContractParseError(overRaw), codeRuntimeContractInvalid,
		"runtime contract is invalid", "size_limit_exceeded", "/runtimeContract")
}

func TestRuntimeContractStrictAndStructuralClassification(t *testing.T) {
	valid := mustRuntimeContractJSON(t, runtimeContractManifest(t))
	tests := []struct {
		name   string
		data   func() []byte
		reason string
	}{
		{name: "invalid json", data: func() []byte { return []byte("{") }, reason: "invalid_json"},
		{name: "trailing value", data: func() []byte { return append(append([]byte(nil), valid...), []byte("{}")...) }, reason: "trailing_input"},
		{name: "unknown nested member", data: func() []byte {
			return mutateRuntimeContract(t, valid, func(root map[string]any) { runtimeContractOperation(root, "sample.get")["unknown"] = true })
		}, reason: "runtime_contract_schema_invalid"},
		{name: "missing member", data: func() []byte {
			return mutateRuntimeContract(t, valid, func(root map[string]any) { delete(root, "trace") })
		}, reason: "runtime_contract_schema_invalid"},
		{name: "null member", data: func() []byte {
			return mutateRuntimeContract(t, valid, func(root map[string]any) { root["trace"] = nil })
		}, reason: "runtime_contract_schema_invalid"},
		{name: "invalid enum", data: func() []byte {
			return mutateRuntimeContract(t, valid, func(root map[string]any) { runtimeContractOperation(root, "sample.get")["method"] = "TRACE" })
		}, reason: "runtime_contract_schema_invalid"},
		{name: "non integer index", data: func() []byte {
			return mutateRuntimeContract(t, valid, func(root map[string]any) {
				runtimeContractOperation(root, "sample.get")["request"].(map[string]any)["schema"] = json.Number("1.5")
			})
		}, reason: "runtime_contract_schema_invalid"},
		{name: "oversized error projection status", data: func() []byte {
			return mutateRuntimeContract(t, valid, func(root map[string]any) {
				projections := runtimeContractOperation(root, "sample.get")["errorProjections"].(map[string]any)
				projections["sample"].(map[string]any)["not_found"].(map[string]any)["httpStatus"] = json.Number(runtimeContractOversizedIndex)
			})
		}, reason: "runtime_contract_schema_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireRuntimeContractError(t, mustRuntimeContractParseError(test.data()), codeRuntimeContractInvalid,
				"runtime contract is invalid", test.reason, "/runtimeContract")
		})
	}
}

func TestRuntimeContractAllowsDuplicateProjectionTargets(t *testing.T) {
	valid := mustRuntimeContractJSON(t, runtimeContractManifest(t))
	candidate := mutateRuntimeContract(t, valid, func(root map[string]any) {
		projections := runtimeContractOperation(root, "sample.get")["errorProjections"].(map[string]any)
		projections["other"] = map[string]any{
			"missing": map[string]any{"domain": "nexa.sample", "code": "not_found", "httpStatus": json.Number("404")},
		}
	})
	if _, err := ParseRuntimeContract(candidate); err != nil {
		t.Fatalf("duplicate target tuple rejected: %v", err)
	}
}

func TestRuntimeContractCapabilityIDGrammarMatchesManifestOwner(t *testing.T) {
	valid := mustRuntimeContractJSON(t, runtimeContractManifest(t))
	if _, err := ParseRuntimeContract(valid); err != nil {
		t.Fatalf("owner-valid namespace/name capability rejected: %v", err)
	}

	tests := []struct {
		name       string
		id         string
		apiVersion string
	}{
		{name: "missing slash", id: "sample.api", apiVersion: "sample.api/v1"},
		{name: "extra slash", id: "sample/api/extra", apiVersion: "sample/api/extra/v1"},
		{name: "empty namespace", id: "/api", apiVersion: "/api/v1"},
		{name: "empty name", id: "sample/", apiVersion: "sample//v1"},
		{name: "invalid namespace segment", id: "sample_/api", apiVersion: "sample_/api/v1"},
		{name: "invalid name segment", id: "sample/api_name", apiVersion: "sample/api_name/v1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := mutateRuntimeContract(t, valid, func(root map[string]any) {
				capability := runtimeContractOperation(root, "sample.get")["capability"].(map[string]any)
				capability["id"] = test.id
				capability["apiVersion"] = test.apiVersion
			})
			requireRuntimeContractError(t, mustRuntimeContractParseError(candidate), codeRuntimeContractInvalid,
				"runtime contract is invalid", "runtime_contract_schema_invalid", "/runtimeContract")
		})
	}
}

func TestRuntimeContractBuildRepresentabilityBoundaries(t *testing.T) {
	limits := RuntimeContractLimits()

	nodeBoundary := runtimeContractNodeBoundaryManifest(t, false, false)
	if _, err := BuildRuntimeContract(nodeBoundary); err != nil {
		t.Fatalf("node boundary rejected: %v", err)
	}
	nodeOver := runtimeContractNodeBoundaryManifest(t, true, false)
	requireRuntimeContractError(t, runtimeContractBuildError(nodeOver), codeRuntimeContractUnrepresentable,
		"runtime contract exceeds runtime SDK capability", "runtime_contract_node_limit_exceeded", "/manifest")

	base := runtimeContractRawBoundaryManifest(t, 1)
	baseContract, err := BuildRuntimeContract(base)
	if err != nil {
		t.Fatal(err)
	}
	baseJSON, err := baseContract.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	exactPathBytes := 1 + limits.RawBytes - len(baseJSON)
	rawBoundary := runtimeContractRawBoundaryManifest(t, exactPathBytes)
	rawContract, err := BuildRuntimeContract(rawBoundary)
	if err != nil {
		t.Fatalf("raw boundary rejected: %v", err)
	}
	rawJSON, err := rawContract.CanonicalJSON()
	if err != nil || len(rawJSON) != limits.RawBytes {
		t.Fatalf("raw boundary bytes = %d, want %d, err=%v", len(rawJSON), limits.RawBytes, err)
	}
	if _, err := ParseRuntimeContract(rawJSON); err != nil {
		t.Fatalf("raw boundary contract did not parse: %v", err)
	}
	rawOver := runtimeContractRawBoundaryManifest(t, exactPathBytes+1)
	requireRuntimeContractError(t, runtimeContractBuildError(rawOver), codeRuntimeContractUnrepresentable,
		"runtime contract exceeds runtime SDK capability", "runtime_contract_raw_limit_exceeded", "/manifest")

	bothOver := runtimeContractNodeBoundaryManifest(t, true, true)
	requireRuntimeContractError(t, runtimeContractBuildError(bothOver), codeRuntimeContractUnrepresentable,
		"runtime contract exceeds runtime SDK capability", "runtime_contract_node_limit_exceeded", "/manifest")

	_, err = New(Options{Manifest: nodeOver})
	requireRuntimeContractError(t, err, codeRuntimeContractUnrepresentable,
		"runtime contract exceeds runtime SDK capability", "runtime_contract_node_limit_exceeded", "/manifest")
}

func runtimeContractRelationManifest(t *testing.T) generationapi.Manifest {
	t.Helper()
	ref := runtimeContractRef(t, "repo:sdk/api/testdata/runtime.api#Relations")
	fields := []generationapi.FieldSpec{
		{Name: "a", SchemaRef: "scalar.string", Required: true, Provenance: generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}}},
		{Name: "b", SchemaRef: "scalar.string", Required: true, Provenance: generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}}},
		{Name: "payload", SchemaRef: "sample.payload", Required: true, Provenance: generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}}},
	}
	manifest, err := generationapi.NewManifest(generationapi.ManifestSpec{
		Sources: []provenance.Source{{Ref: ref, Digest: provenance.SHA256([]byte("relations"))}},
		Schemas: []generationapi.SchemaSpec{
			{ID: "scalar.string", Kind: generationapi.SchemaString},
			{ID: "sample.payload", Kind: generationapi.SchemaObject, Provenance: runtimeContractProvenance(ref), Fields: []generationapi.FieldSpec{}},
			{ID: "sample.request", Kind: generationapi.SchemaObject, Provenance: runtimeContractProvenance(ref), Fields: fields},
		},
		Operations: []generationapi.OperationSpec{{
			ID: "sample.call", Method: generationapi.MethodPOST, Path: "/items",
			Provenance:       generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}},
			RequestSchemaRef: "sample.request", ResponseBody: generationapi.ResponseBodyNone,
			RequestBindings: []generationapi.RequestBindingSpec{
				{Field: "a", Location: generationapi.RequestBindingQuery, Name: "qa"},
				{Field: "b", Location: generationapi.RequestBindingHeader, Name: "x-b"},
				{Field: "payload", Location: generationapi.RequestBindingBody, Name: "payload"},
			},
			Auth: generationapi.AuthSpec{Mode: generationapi.AuthRequired, Credentials: []generationapi.CredentialSpec{
				{ID: "key1", Type: generationapi.CredentialAPIKey, Location: generationapi.CredentialLocationHeader, Name: "x-key1"},
				{ID: "key2", Type: generationapi.CredentialAPIKey, Location: generationapi.CredentialLocationQuery, Name: "key2"},
			}},
			Permission: "sample.read", Capability: &generationapi.CapabilitySpec{ID: "sample/api", APIVersion: "sample/api/v1"},
			ErrorProjections: []generationapi.ErrorProjectionSpec{},
		}},
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	return manifest
}

func runtimeContractNodeBoundaryManifest(t *testing.T, oneOver, rawOver bool) generationapi.Manifest {
	t.Helper()
	const operationCount = 17_475
	ref := runtimeContractRef(t, "repo:sdk/api/testdata/runtime.api#NodeBoundary")
	operations := make([]generationapi.OperationSpec, operationCount)
	for index := range operations {
		operations[index] = generationapi.OperationSpec{
			ID: fmt.Sprintf("sample.call.%05d", index), Method: generationapi.MethodGET,
			Path:             fmt.Sprintf("/sample/%d", index),
			Provenance:       generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}},
			RequestSchemaRef: "sample.request", ResponseBody: generationapi.ResponseBodyNone,
			RequestBindings: []generationapi.RequestBindingSpec{}, Auth: generationapi.AuthSpec{Mode: generationapi.AuthNone},
			ErrorProjections: []generationapi.ErrorProjectionSpec{},
		}
	}
	operations[0].ErrorProjections = []generationapi.ErrorProjectionSpec{{
		Match:   generationapi.ErrorMatchSpec{Domain: "sample", Code: "missing"},
		Project: generationapi.ErrorTargetSpec{Domain: "sample", Code: "missing", HTTPStatus: 404},
	}}
	if oneOver {
		operations[0].Auth = generationapi.AuthSpec{Mode: generationapi.AuthRequired, Credentials: []generationapi.CredentialSpec{{
			ID: "key", Type: generationapi.CredentialAPIKey, Location: generationapi.CredentialLocationHeader, Name: "x-key",
		}}}
	} else {
		operations[0].Capability = &generationapi.CapabilitySpec{ID: "sample/api", APIVersion: "sample/api/v1"}
	}
	if rawOver {
		operations[0].Path = "/" + strings.Repeat("a", RuntimeContractLimits().RawBytes)
	}
	manifest, err := generationapi.NewManifest(generationapi.ManifestSpec{
		Sources:    []provenance.Source{{Ref: ref, Digest: provenance.SHA256([]byte("node boundary"))}},
		Schemas:    []generationapi.SchemaSpec{{ID: "sample.request", Kind: generationapi.SchemaObject, Provenance: runtimeContractProvenance(ref), Fields: []generationapi.FieldSpec{}}},
		Operations: operations,
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	return manifest
}

func runtimeContractRawBoundaryManifest(t *testing.T, pathBytes int) generationapi.Manifest {
	t.Helper()
	if pathBytes < 1 {
		t.Fatalf("path bytes = %d", pathBytes)
	}
	ref := runtimeContractRef(t, "repo:sdk/api/testdata/runtime.api#RawBoundary")
	manifest, err := generationapi.NewManifest(generationapi.ManifestSpec{
		Sources: []provenance.Source{{Ref: ref, Digest: provenance.SHA256([]byte("raw boundary"))}},
		Schemas: []generationapi.SchemaSpec{{ID: "sample.request", Kind: generationapi.SchemaObject, Provenance: runtimeContractProvenance(ref), Fields: []generationapi.FieldSpec{}}},
		Operations: []generationapi.OperationSpec{{
			ID: "sample.call", Method: generationapi.MethodGET, Path: "/" + strings.Repeat("a", pathBytes-1),
			Provenance:       generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}},
			RequestSchemaRef: "sample.request", ResponseBody: generationapi.ResponseBodyNone,
			RequestBindings: []generationapi.RequestBindingSpec{}, Auth: generationapi.AuthSpec{Mode: generationapi.AuthNone},
			ErrorProjections: []generationapi.ErrorProjectionSpec{},
		}},
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	return manifest
}

func runtimeContractBuildError(manifest generationapi.Manifest) error {
	_, err := BuildRuntimeContract(manifest)
	return err
}

func mustRuntimeContractJSON(t *testing.T, manifest generationapi.Manifest) []byte {
	t.Helper()
	contract, err := BuildRuntimeContract(manifest)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := contract.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func runtimeContractSchemas(root map[string]any) []any {
	return root["schemas"].([]any)
}

func runtimeContractOperation(root map[string]any, id string) map[string]any {
	return root["operations"].(map[string]any)[id].(map[string]any)
}

func runtimeContractBinding(root map[string]any, operationID, field string) map[string]any {
	request := runtimeContractOperation(root, operationID)["request"].(map[string]any)
	return request["bindings"].(map[string]any)[field].(map[string]any)
}

func runtimeContractCredential(root map[string]any, operationID, id string) map[string]any {
	auth := runtimeContractOperation(root, operationID)["auth"].(map[string]any)
	return auth["credentials"].(map[string]any)[id].(map[string]any)
}

func nestedRuntimeContractJSON(depth int) []byte {
	return []byte(strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth))
}

func flatRuntimeContractJSON(items int) []byte {
	if items == 0 {
		return []byte("[]")
	}
	return []byte("[" + strings.Repeat("0,", items-1) + "0]")
}

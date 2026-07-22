package api

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"

	generationapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	runtimeContractBuildAllocationLimit = 256 << 20
	runtimeContractParseAllocationLimit = 384 << 20
)

func TestRuntimeContractAllocationBudgets(t *testing.T) {
	limits := RuntimeContractLimits()
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
	exactManifest := runtimeContractRawBoundaryManifest(t, exactPathBytes)
	exactContract, err := BuildRuntimeContract(exactManifest)
	if err != nil {
		t.Fatalf("prepare exact contract: %v", err)
	}
	exactJSON, err := exactContract.CanonicalJSON()
	if err != nil || len(exactJSON) != limits.RawBytes {
		t.Fatalf("prepare exact JSON: bytes=%d err=%v", len(exactJSON), err)
	}
	farOverManifest := runtimeContractRawBoundaryManifest(t, 32<<20)

	t.Run("exact 16 MiB Build", func(t *testing.T) {
		var built RuntimeContract
		var buildErr error
		delta := runtimeContractTotalAlloc(func() {
			built, buildErr = BuildRuntimeContract(exactManifest)
		})
		runtime.KeepAlive(built)
		if buildErr != nil {
			t.Fatalf("BuildRuntimeContract() error = %v", buildErr)
		}
		t.Logf("TotalAlloc delta = %d bytes", delta)
		if delta > runtimeContractBuildAllocationLimit {
			t.Fatalf("Build TotalAlloc delta = %d, want <= %d", delta, runtimeContractBuildAllocationLimit)
		}
	})

	t.Run("32 MiB far-over Build", func(t *testing.T) {
		var buildErr error
		delta := runtimeContractTotalAlloc(func() {
			_, buildErr = BuildRuntimeContract(farOverManifest)
		})
		requireRuntimeContractError(t, buildErr, codeRuntimeContractUnrepresentable,
			"runtime contract exceeds runtime SDK capability", "runtime_contract_raw_limit_exceeded", "/manifest")
		t.Logf("TotalAlloc delta = %d bytes", delta)
		if delta > runtimeContractBuildAllocationLimit {
			t.Fatalf("far-over Build TotalAlloc delta = %d, want <= %d", delta, runtimeContractBuildAllocationLimit)
		}
	})

	t.Run("exact 16 MiB Parse", func(t *testing.T) {
		var parsed RuntimeContract
		var parseErr error
		delta := runtimeContractTotalAlloc(func() {
			parsed, parseErr = ParseRuntimeContract(exactJSON)
		})
		runtime.KeepAlive(parsed)
		if parseErr != nil {
			t.Fatalf("ParseRuntimeContract() error = %v", parseErr)
		}
		t.Logf("TotalAlloc delta = %d bytes", delta)
		if delta > runtimeContractParseAllocationLimit {
			t.Fatalf("Parse TotalAlloc delta = %d, want <= %d", delta, runtimeContractParseAllocationLimit)
		}
	})
}

func TestRuntimeContractDistributedLongKeyAllocationBudgets(t *testing.T) {
	const (
		operationCount   = 32
		operationIDBytes = 256 << 10
	)
	manifest := runtimeContractDistributedLongKeyManifest(t, operationCount, operationIDBytes)

	t.Run("Build", func(t *testing.T) {
		var contract RuntimeContract
		var buildErr error
		delta := runtimeContractTotalAlloc(func() {
			contract, buildErr = BuildRuntimeContract(manifest)
		})
		runtime.KeepAlive(contract)
		if buildErr != nil {
			t.Fatalf("BuildRuntimeContract() error = %v", buildErr)
		}
		encoded, err := contract.CanonicalJSON()
		if err != nil {
			t.Fatalf("CanonicalJSON() error = %v", err)
		}
		runtimeContractRequireDistributedLongKeySize(t, encoded, operationCount*operationIDBytes)
		t.Logf("distributed long-key Build: raw=%d TotalAlloc delta=%d bytes", len(encoded), delta)
		if delta > runtimeContractBuildAllocationLimit {
			t.Fatalf("distributed long-key Build TotalAlloc delta = %d, want <= %d", delta, runtimeContractBuildAllocationLimit)
		}
	})

	t.Run("Parse", func(t *testing.T) {
		contract, err := BuildRuntimeContract(manifest)
		if err != nil {
			t.Fatalf("prepare contract: %v", err)
		}
		encoded, err := contract.CanonicalJSON()
		if err != nil {
			t.Fatalf("prepare canonical JSON: %v", err)
		}
		runtimeContractRequireDistributedLongKeySize(t, encoded, operationCount*operationIDBytes)

		var parsed RuntimeContract
		var parseErr error
		delta := runtimeContractTotalAlloc(func() {
			parsed, parseErr = ParseRuntimeContract(encoded)
		})
		runtime.KeepAlive(parsed)
		if parseErr != nil {
			t.Fatalf("ParseRuntimeContract() error = %v", parseErr)
		}
		roundTrip, err := parsed.CanonicalJSON()
		if err != nil {
			t.Fatalf("parsed CanonicalJSON() error = %v", err)
		}
		if !bytes.Equal(roundTrip, encoded) {
			t.Fatal("parsed distributed long-key contract changed canonical bytes")
		}
		t.Logf("distributed long-key Parse: raw=%d TotalAlloc delta=%d bytes", len(encoded), delta)
		if delta > runtimeContractParseAllocationLimit {
			t.Fatalf("distributed long-key Parse TotalAlloc delta = %d, want <= %d", delta, runtimeContractParseAllocationLimit)
		}
	})
}

func TestRuntimeContractDistributedLongKeysRejectWithinBuildBudget(t *testing.T) {
	const (
		operationCount   = 68
		operationIDBytes = 256 << 10
	)
	manifest := runtimeContractDistributedLongKeyManifest(t, operationCount, operationIDBytes)

	var buildErr error
	delta := runtimeContractTotalAlloc(func() {
		_, buildErr = BuildRuntimeContract(manifest)
	})
	requireRuntimeContractError(t, buildErr, codeRuntimeContractUnrepresentable,
		"runtime contract exceeds runtime SDK capability", "runtime_contract_raw_limit_exceeded", "/manifest")
	t.Logf("distributed over-limit long-key Build: projected key bytes=%d TotalAlloc delta=%d bytes",
		operationCount*operationIDBytes, delta)
	if delta > runtimeContractBuildAllocationLimit {
		t.Fatalf("distributed over-limit long-key Build TotalAlloc delta = %d, want <= %d", delta, runtimeContractBuildAllocationLimit)
	}
}

func runtimeContractDistributedLongKeyManifest(t *testing.T, operationCount, operationIDBytes int) generationapi.Manifest {
	t.Helper()
	ref := runtimeContractRef(t, "repo:sdk/api/testdata/runtime.api#DistributedLongKeys")
	owner := generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{ref}}
	operations := make([]generationapi.OperationSpec, operationCount)
	for index := range operations {
		suffix := fmt.Sprintf("%015d", index)
		operationID := "a" + strings.Repeat("x", operationIDBytes-len(suffix)-1) + suffix
		operations[index] = generationapi.OperationSpec{
			ID: operationID, Method: generationapi.MethodGET, Path: fmt.Sprintf("/distributed/%d", index),
			Provenance: owner, RequestSchemaRef: "sample.request", ResponseBody: generationapi.ResponseBodyNone,
			RequestBindings: []generationapi.RequestBindingSpec{}, Auth: generationapi.AuthSpec{Mode: generationapi.AuthNone},
			ErrorProjections: []generationapi.ErrorProjectionSpec{},
		}
	}
	manifest, err := generationapi.NewManifest(generationapi.ManifestSpec{
		Sources: []provenance.Source{{Ref: ref, Digest: provenance.SHA256([]byte("distributed long keys"))}},
		Schemas: []generationapi.SchemaSpec{{
			ID: "sample.request", Kind: generationapi.SchemaObject, Provenance: runtimeContractProvenance(ref),
			Fields: []generationapi.FieldSpec{},
		}},
		Operations: operations,
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	return manifest
}

func runtimeContractRequireDistributedLongKeySize(t *testing.T, encoded []byte, projectedKeyBytes int) {
	t.Helper()
	if len(encoded) < projectedKeyBytes || len(encoded) > projectedKeyBytes+(1<<20) {
		t.Fatalf("distributed long-key canonical bytes = %d, want about %d", len(encoded), projectedKeyBytes)
	}
	if len(encoded) > RuntimeContractLimits().RawBytes {
		t.Fatalf("distributed long-key canonical bytes = %d, exceeds raw limit", len(encoded))
	}
}

func runtimeContractTotalAlloc(operation func()) uint64 {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	operation()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

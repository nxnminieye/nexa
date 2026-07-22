package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/service"
	"github.com/nxnminieye/nexa/provenance"
)

func TestManifestCanonicalRoundTripAndContractDigest(t *testing.T) {
	sources := []provenance.Source{
		testSource(t, "rpc/sample.proto", "service:sample.v1.Sample", "rpc"),
		testSource(t, "api/core.api", "route:GET /samples/{id}", "api"),
	}
	digest := contractDigest(t, sources)
	manifest, err := service.New(service.Spec{
		ServiceID: "sample", ServiceKind: "rpc", ModulePath: "example.com/consumer/backend/sample",
		ContractSources: sources, ContractDigest: digest,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	canonical, err := service.CanonicalJSON(manifest)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	parsed, err := service.Parse("generated/service-manifest.json", canonical)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	roundTrip, err := service.CanonicalJSON(parsed)
	if err != nil || !bytes.Equal(roundTrip, canonical) {
		t.Fatalf("round trip = %s, %v", roundTrip, err)
	}

	changed := append([]provenance.Source(nil), sources...)
	changed[0].Digest = provenance.SHA256([]byte("changed"))
	if contractDigest(t, changed) == digest {
		t.Fatal("contract source digest change did not change ContractDigest")
	}
	_, err = service.New(service.Spec{
		ServiceID: "sample", ServiceKind: "rpc", ModulePath: "example.com/consumer/backend/sample",
		ContractSources: changed, ContractDigest: digest,
	})
	assertServiceError(t, err, "contract_digest_mismatch", "/contractDigest")
}

func TestManifestComputesItsOwnClosureDigestFromCompiledProtocolSources(t *testing.T) {
	proto, err := protocol.Compile(context.Background(), protocol.CompileOptions{
		ServiceID: "sample", EntryFiles: []string{"rpc/sample.proto"},
		Resolver: resolverFunc(func(context.Context, string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(`syntax = "proto3";
package sample.v1;
message Request { string id = 1; }
message Response { string name = 1; }
service Sample { rpc Get(Request) returns (Response); }
`)), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := service.ComputeContractDigest(proto.Sources())
	if err != nil {
		t.Fatal(err)
	}
	if digest == proto.SourceDigest() {
		t.Fatal("service and ProtocolIR source-set identities unexpectedly share one digest")
	}
	if _, err := service.New(service.Spec{
		ServiceID: "sample", ServiceKind: "rpc", ModulePath: "example.com/consumer/backend/sample",
		ContractSources: proto.Sources(), ContractDigest: digest,
	}); err != nil {
		t.Fatalf("New(rpc) error = %v", err)
	}
}

func TestParseRejectsNonCanonicalAndRuntimeFacts(t *testing.T) {
	source := testSource(t, "rpc/sample.proto", "service:sample.v1.Sample", "rpc")
	manifest, err := service.New(service.Spec{
		ServiceID: "sample", ServiceKind: "rpc", ModulePath: "example.com/consumer/backend/sample",
		ContractSources: []provenance.Source{source}, ContractDigest: contractDigest(t, []provenance.Source{source}),
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := service.CanonicalJSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	pretty := append([]byte(" \n"), canonical...)
	assertServiceError(t, parseError(pretty), "document_noncanonical", "")

	for _, field := range []string{"endpoint", "port", "health", "env", "credential", "deployment", "quality", "gitHost"} {
		data := append([]byte(nil), canonical[:len(canonical)-2]...)
		data = append(data, []byte(`,"`+field+`":"forbidden"}`+"\n")...)
		assertServiceError(t, parseError(data), "document_unknown_field", "/"+field)
	}
}

func TestSchemaIsDetachedAndDescribesClosedContract(t *testing.T) {
	first := service.Schema()
	if len(first) == 0 {
		t.Fatal("Schema() returned no bytes")
	}
	first[0] ^= 0xff
	if bytes.Equal(first, service.Schema()) {
		t.Fatal("Schema() returned aliased bytes")
	}
}

func parseError(data []byte) error {
	_, err := service.Parse("generated/service-manifest.json", data)
	return err
}

func assertServiceError(t *testing.T, err error, reason, pointer string) {
	t.Helper()
	var typed *service.Error
	if !errors.As(err, &typed) || typed.Reason() != reason || typed.Pointer() != pointer {
		t.Fatalf("error = %T %v, want reason=%q pointer=%q", err, err, reason, pointer)
	}
}

func testSource(t *testing.T, path, fragment, content string) provenance.Source {
	t.Helper()
	ref, err := provenance.RepositoryRef(path, fragment)
	if err != nil {
		t.Fatal(err)
	}
	return provenance.Source{Ref: ref, Digest: provenance.SHA256([]byte(content))}
}

type resolverFunc func(context.Context, string) (io.ReadCloser, error)

func (f resolverFunc) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	return f(ctx, path)
}

func contractDigest(t *testing.T, sources []provenance.Source) provenance.Digest {
	t.Helper()
	type wireSource struct {
		Ref    string `json:"ref"`
		Digest string `json:"digest"`
	}
	values := make([]wireSource, len(sources))
	for i, source := range sources {
		values[i] = wireSource{Ref: source.Ref.String(), Digest: source.Digest.String()}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Ref < values[j].Ref })
	data, err := json.Marshal(struct {
		APIVersion string       `json:"apiVersion"`
		Sources    []wireSource `json:"sources"`
	}{APIVersion: "nexa.dev/service-contract-source-set/v1", Sources: values})
	if err != nil {
		t.Fatal(err)
	}
	return provenance.SHA256(data)
}

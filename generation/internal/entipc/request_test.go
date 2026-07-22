package entipc

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/provenance"
)

func TestRequestCanonicalRoundTripAndDefensiveSpecs(t *testing.T) {
	request := newTestRequest(t)
	canonical, err := CanonicalRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical) == 0 || canonical[len(canonical)-1] == '\n' {
		t.Fatalf("canonical request has invalid framing: %q", canonical)
	}
	parsed, err := ParseRequest(testDomainSource(t, "quality/ent-request.json"), canonical)
	if err != nil {
		t.Fatalf("ParseRequest() error = %#v", err)
	}
	again, err := CanonicalRequest(parsed)
	if err != nil || !bytes.Equal(again, canonical) {
		t.Fatalf("round trip = %s, %v", again, err)
	}

	entitySpec, err := parsed.EntitySpec()
	if err != nil {
		t.Fatal(err)
	}
	entitySpec.BuildTags[0] = "mutated"
	entityAgain, err := parsed.EntitySpec()
	if err != nil {
		t.Fatal(err)
	}
	if got := entityAgain.BuildTags; len(got) != 2 || got[0] != "integration" || got[1] != "linux" {
		t.Fatalf("defensive EntitySpec build tags = %#v", got)
	}
	buildSpec, err := parsed.BuildSpec()
	if err != nil {
		t.Fatal(err)
	}
	if buildSpec.ServiceID != "accounts" || buildSpec.ProtoPackage != "acme.accounts.v1" || buildSpec.GoPackage != "example.com/acme/gen/accountsv1" {
		t.Fatalf("BuildSpec = %#v", buildSpec)
	}
	if buildSpec.ProtoArtifactPath != "api/accounts.crud.generated.proto" || buildSpec.LockPath != "api/accounts.crud-protocol.lock.json" {
		t.Fatalf("BuildSpec paths = %q, %q", buildSpec.ProtoArtifactPath, buildSpec.LockPath)
	}
	if !buildSpec.MultiTenant.Enabled {
		t.Fatalf("BuildSpec lost multi-tenant config: %#v", buildSpec.MultiTenant)
	}
}

func TestRequestRejectsWirePollutionAndDigestMismatch(t *testing.T) {
	canonical, err := CanonicalRequest(newTestRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(canonical, &object); err != nil {
		t.Fatal(err)
	}
	mutate := func(t *testing.T, change func(map[string]any)) []byte {
		t.Helper()
		copyObject := map[string]any{}
		for key, value := range object {
			copyObject[key] = value
		}
		change(copyObject)
		return mustCanonicalJSON(t, copyObject)
	}
	tests := []struct {
		name string
		data []byte
	}{
		{name: "unknown", data: mutate(t, func(value map[string]any) { value["capability"] = "forbidden" })},
		{name: "null", data: mutate(t, func(value map[string]any) { value["publishedArtifact"] = nil })},
		{name: "digest", data: mutate(t, func(value map[string]any) { value["serviceId"] = "other" })},
		{name: "duplicate", data: []byte(`{"apiVersion":"nexa.dev/ent-graph-request/v1","apiVersion":"nexa.dev/ent-graph-request/v1"}`)},
		{name: "trailing", data: append(append([]byte(nil), canonical...), []byte(`{}`)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseRequest(testDomainSource(t, "quality/ent-request.json"), test.data); err == nil {
				t.Fatal("ParseRequest accepted invalid request")
			}
		})
	}
}

func TestRequestDigestChangesWithSemanticInputsButNotRepositoryRoot(t *testing.T) {
	base := testRequestSpec(t)
	first := requestDigest(t, base)
	base.RepositoryRoot = "/private/other-checkout"
	if got := requestDigest(t, base); got != first {
		t.Fatalf("repository root changed semantic digest: %s != %s", got, first)
	}
	base = testRequestSpec(t)
	base.MultiTenant.Enabled = false
	if got := requestDigest(t, base); got == first {
		t.Fatal("multi-tenant config did not change semantic digest")
	}
	base = testRequestSpec(t)
	base.BuildTags = append(base.BuildTags, "race")
	if got := requestDigest(t, base); got == first {
		t.Fatal("build tags did not change semantic digest")
	}
	base = testRequestSpec(t)
	base.ModuleSources[0].Digest = provenance.SHA256([]byte("changed module source"))
	if got := requestDigest(t, base); got == first {
		t.Fatal("module sources did not change semantic digest")
	}
}

func newTestRequest(t *testing.T) Request {
	t.Helper()
	request, err := NewRequest(testRequestSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func testRequestSpec(t *testing.T) RequestSpec {
	t.Helper()
	return RequestSpec{
		RepositoryRoot:    "/private/repository",
		SchemaDir:         testDomainSource(t, "fixtures/generation/ent-consumer/schema"),
		BuildTags:         []string{"linux", "integration"},
		ModuleGraphDigest: provenance.SHA256([]byte("module graph")),
		BuildInputDigest:  provenance.SHA256([]byte("build input")),
		ModuleSources:     []provenance.Source{{Ref: testSourceRef(t, "go.mod"), Digest: provenance.SHA256([]byte("go.mod"))}},
		ServiceID:         "accounts",
		MultiTenant:       MultiTenantConfig{Enabled: true},
		ProtoPackage:      "acme.accounts.v1",
		GoPackage:         "example.com/acme/gen/accountsv1",
		ProtoDestination:  ProtoDestination{EntryPath: "api/accounts.proto", ArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json"},
		Tool:              ToolIdentity{ID: "go", Version: "go1.25", ExecutableVersion: "go version go1.25.0 darwin/arm64"},
	}
}

func testSourceRef(t *testing.T, path string) provenance.SourceRef {
	t.Helper()
	value, err := provenance.RepositoryRef(path, "")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func requestDigest(t *testing.T, spec RequestSpec) string {
	t.Helper()
	request, err := NewRequest(spec)
	if err != nil {
		t.Fatal(err)
	}
	return request.RequestDigest().String()
}

func testDomainSource(t *testing.T, value string) provenance.DomainSource {
	t.Helper()
	result, err := provenance.ParseDomainSource(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustCanonicalJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBusinessContractExternalConsumer(t *testing.T) {
	temporary := t.TempDir()
	moduleRoot := filepath.Join(temporary, "consumer")
	if err := os.MkdirAll(moduleRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	module := "module example.com/nexa-business-contract-consumer\n\ngo 1.25.0\n\nrequire github.com/nxnminieye/nexa v0.0.0\n\n" +
		"replace github.com/nxnminieye/nexa => " + repositoryRoot(t) + "\n"
	writeConsumerFile(t, filepath.Join(moduleRoot, "go.mod"), module)
	writeConsumerFile(t, filepath.Join(moduleRoot, "contracts_test.go"), businessContractConsumerSource)

	environment := prepareHermeticExternalModule(t, temporary, moduleRoot)
	t.Cleanup(func() {
		command := exec.Command("go", "clean", "-modcache")
		command.Env = environment
		if combined, err := command.CombinedOutput(); err != nil {
			t.Errorf("clean external module cache: %v\n%s", err, combined)
		}
	})
	runBusinessContractGo(t, moduleRoot, environment, "test readonly external module", "test", "-mod=readonly", "./...")
}

func runBusinessContractGo(t *testing.T, moduleRoot string, environment []string, stage string, arguments ...string) {
	t.Helper()
	command := exec.Command("go", arguments...)
	command.Dir = moduleRoot
	command.Env = environment
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", stage, err, combined)
	}
}

const businessContractConsumerSource = `package consumer_test

import (
	"bytes"
	"encoding/json"
	"runtime/debug"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/project/servicecatalog"
	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/runtime/buildinfo"
)

func TestBusinessContracts(t *testing.T) {
	digest := provenance.SHA256([]byte("sample-source"))
	parsedDigest, err := provenance.ParseDigest(digest.String())
	if err != nil || parsedDigest != digest { t.Fatalf("digest round-trip: %v", err) }
	wholeRef, err := provenance.RepositoryRef("contracts/sample.api", "")
	if err != nil { t.Fatal(err) }
	fragmentRef, err := provenance.RepositoryRef("contracts/sample.api", "GetSample")
	if err != nil { t.Fatal(err) }
	fieldRef, err := provenance.RepositoryRef("contracts/sample.api", "GetSample.id")
	if err != nil { t.Fatal(err) }
	originRef, err := provenance.RepositoryRef("contracts/sample-relation.yaml", "sample-id")
	if err != nil { t.Fatal(err) }
	for _, ref := range []provenance.SourceRef{wholeRef, fragmentRef, fieldRef, originRef} {
		parsed, parseErr := provenance.ParseSourceRef(ref.String())
		if parseErr != nil || parsed != ref { t.Fatalf("source ref round-trip: %v", parseErr) }
	}

	if empty := servicecatalog.Empty(); empty.Len() != 0 || len(empty.Services()) != 0 { t.Fatalf("empty catalog = %#v", empty) }
	catalogJSON := []byte("{\"apiVersion\":\"nexa.dev/service-catalog/v1\",\"kind\":\"ServiceCatalog\",\"services\":[{\"id\":\"foundation\",\"root\":\"backend/foundation\",\"dependsOn\":[],\"capabilityBindings\":[{\"id\":\"nexa.dev/generation-api-proxy\",\"apiVersion\":\"nexa.dev/generation-api-proxy/v1\"}]}]}")
	catalog, err := servicecatalog.Parse("services.json", catalogJSON)
	if err != nil { t.Fatal(err) }
	service, ok := catalog.Lookup("foundation")
	order := catalog.DependencyOrder()
	serviceSource := service.Source()
	resolvedServiceSource, sourceExists := catalog.Source(serviceSource.Ref)
	bindings := service.CapabilityBindings()
	if !ok || service.Root() != "backend/foundation" || len(service.DependsOn()) != 0 || len(bindings) != 1 || len(order) != 1 || order[0].ID() != "foundation" || len(catalog.Sources()) != 2 || !sourceExists || resolvedServiceSource != serviceSource { t.Fatalf("catalog projection is invalid") }
	serviceEnvelope, err := json.Marshal(map[string]any{"apiVersion":servicecatalog.ServiceNodeAPIVersion, "id":service.ID(), "root":service.Root(), "dependsOn":[]string{}})
	if err != nil { t.Fatal(err) }
	serviceCanonical, err := jcs.Transform(serviceEnvelope)
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(serviceCanonical, service.CanonicalSourceJSON()) || provenance.SHA256(serviceCanonical) != serviceSource.Digest { t.Fatalf("service source is not independently reproducible") }
	binding := bindings[0]
	bindingEnvelope, err := json.Marshal(map[string]any{"apiVersion":servicecatalog.CapabilityBindingNodeAPIVersion, "serviceId":service.ID(), "id":binding.ID(), "capabilityApiVersion":binding.APIVersion()})
	if err != nil { t.Fatal(err) }
	bindingCanonical, err := jcs.Transform(bindingEnvelope)
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(bindingCanonical, binding.CanonicalSourceJSON()) || provenance.SHA256(bindingCanonical) != binding.Source().Digest { t.Fatalf("binding source is not independently reproducible") }

	source := provenance.Source{Ref: wholeRef, Digest: digest}
	artifactManifest, err := artifact.NewManifest(artifact.ManifestSpec{
		Generator: artifact.GeneratorSpec{ID: "sample-generator", Version: "v0.1.0"},
		Sources: []provenance.Source{source},
		Artifacts: []artifact.ArtifactSpec{{ID: "sample-artifact", Path: "generated/sample.json", Owner: "sample-generator", Digest: provenance.SHA256([]byte("sample-artifact")), Sources: []provenance.SourceRef{wholeRef}, StalePolicy: artifact.StaleRetain}},
	})
	if err != nil { t.Fatal(err) }
	artifactJSON, err := artifactManifest.CanonicalJSON()
	if err != nil { t.Fatal(err) }
	parsedArtifact, err := artifact.Parse("artifact-manifest.json", artifactJSON)
	if err != nil { t.Fatal(err) }
	parsedSources := parsedArtifact.Sources()
	artifacts := parsedArtifact.Artifacts()
	if parsedArtifact.APIVersion() != artifact.APIVersion || parsedArtifact.Generator().ID() != "sample-generator" || parsedArtifact.Generator().Version() != "v0.1.0" || parsedArtifact.InputDigest() != artifactManifest.InputDigest() || len(parsedSources) != 1 || parsedSources[0].Ref != wholeRef || parsedSources[0].Digest != digest || len(artifacts) != 1 { t.Fatalf("artifact manifest projection is invalid") }
	artifactSources := artifacts[0].Sources()
	if artifacts[0].ID() != "sample-artifact" || artifacts[0].Path() != "generated/sample.json" || artifacts[0].Owner() != "sample-generator" || artifacts[0].Digest() != provenance.SHA256([]byte("sample-artifact")) || len(artifactSources) != 1 || artifactSources[0] != wholeRef || artifacts[0].StalePolicy() != artifact.StaleRetain { t.Fatalf("artifact projection is invalid") }

	identity, err := buildinfo.NewIdentity("foundation", "rpc", "sample.Sample")
	if err != nil { t.Fatal(err) }
	info, err := buildinfo.Resolve(identity, buildinfo.ReaderFunc(func() (*debug.BuildInfo, bool) { return nil, false }))
	if err != nil { t.Fatal(err) }
	buildJSON, err := info.CanonicalJSON()
	if err != nil { t.Fatal(err) }
	wantBuildJSON := "{\"apiVersion\":\"nexa.dev/build-info/v1\",\"kind\":\"BuildInfo\",\"service\":\"foundation\",\"serviceKind\":\"rpc\",\"contractVersion\":\"sample.Sample\",\"available\":false,\"commit\":\"unknown\",\"dirty\":true,\"vcsTime\":\"\",\"goVersion\":\"\",\"modulePath\":\"\",\"moduleVersion\":\"\"}\n"
	if info.Available() || info.Commit() != "unknown" || !info.Dirty() || string(buildJSON) != wantBuildJSON { t.Fatalf("fallback build info is invalid: %s", buildJSON) }

	getters := []struct{name string; get func() []byte}{
		{name: "service catalog", get: servicecatalog.Schema},
		{name: "artifact manifest", get: artifact.Schema},
		{name: "build info", get: buildinfo.Schema},
	}
	for _, getter := range getters {
		t.Run(getter.name, func(t *testing.T) {
			first := getter.get()
			if !json.Valid(first) { t.Fatal("schema is not JSON") }
			var document any
			if err := json.Unmarshal(first, &document); err != nil { t.Fatal(err) }
			original := append([]byte(nil), first...)
			first[0] ^= 0xff
			if !bytes.Equal(original, getter.get()) { t.Fatal("schema accessor does not return a defensive copy") }
		})
	}
}
`

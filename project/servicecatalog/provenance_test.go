package servicecatalog_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/project/servicecatalog"
	"github.com/nxnminieye/nexa/provenance"
)

func TestParseSourceIdentityPrecedesPayloadInspection(t *testing.T) {
	tests := []string{"", ".", "/services.yaml", "backend/../services.yaml", "backend\\services.yaml", "backend//services.yaml", "backend/services.yaml\x00"}
	payloads := [][]byte{nil, []byte(validCatalogYAML)}
	for _, source := range tests {
		for _, payload := range payloads {
			_, err := servicecatalog.Parse(source, payload)
			catalogErr := requireCatalogError(t, err, "service_catalog_invalid", "source_identity_invalid")
			if catalogErr.Source() != "" || catalogErr.Pointer() != "" || catalogErr.Line() != 0 || catalogErr.Column() != 0 {
				t.Fatalf("Parse(%q) location = (%q,%q,%d,%d), want empty zero location", source, catalogErr.Source(), catalogErr.Pointer(), catalogErr.Line(), catalogErr.Column())
			}
		}
	}
}

func TestCatalogProjectsCanonicalNodeSources(t *testing.T) {
	catalog, err := servicecatalog.Parse("project/services.yaml", []byte(validCatalogYAML))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	foundation, ok := catalog.Lookup("foundation")
	if !ok {
		t.Fatal("Lookup(foundation) failed")
	}
	sample, ok := catalog.Lookup("sample")
	if !ok {
		t.Fatal("Lookup(sample) failed")
	}
	binding := sample.CapabilityBindings()[0]

	expectedFoundation := catalogNodeSource(t, "project/services.yaml", "service:foundation", `{"apiVersion":"nexa.dev/service-node/v1","dependsOn":[],"id":"foundation","root":"backend/foundation"}`)
	expectedSample := catalogNodeSource(t, "project/services.yaml", "service:sample", `{"apiVersion":"nexa.dev/service-node/v1","dependsOn":["foundation"],"id":"sample","root":"backend/sample"}`)
	expectedBinding := catalogNodeSource(t, "project/services.yaml", "service:sample/binding:example.com/cross-source-relation@example.com/cross-source-relation/v1", `{"apiVersion":"nexa.dev/capability-binding-node/v1","capabilityApiVersion":"example.com/cross-source-relation/v1","id":"example.com/cross-source-relation","serviceId":"sample"}`)

	if foundation.Source() != expectedFoundation || sample.Source() != expectedSample || binding.Source() != expectedBinding {
		t.Fatalf("node sources = %#v %#v %#v", foundation.Source(), sample.Source(), binding.Source())
	}
	wantSources := []provenance.Source{expectedFoundation, expectedSample, expectedBinding}
	sortSources(wantSources)
	if got := catalog.Sources(); !reflect.DeepEqual(got, wantSources) {
		t.Fatalf("Sources() = %#v, want %#v", got, wantSources)
	}
	for _, want := range wantSources {
		got, exists := catalog.Source(want.Ref)
		if !exists || got != want {
			t.Fatalf("Source(%s) = %#v, %v", want.Ref.String(), got, exists)
		}
	}
	missing, err := provenance.RepositoryRef("project/services.yaml", "service:missing")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := catalog.Source(missing); exists {
		t.Fatal("Source(missing) unexpectedly succeeded")
	}

	mutated := catalog.Sources()
	mutated[0] = provenance.Source{}
	if got := catalog.Sources(); !reflect.DeepEqual(got, wantSources) {
		t.Fatalf("mutating Sources() changed catalog: %#v", got)
	}
}

func TestCatalogNodeDigestsIgnoreSyntaxAndOrder(t *testing.T) {
	yamlCatalog, err := servicecatalog.Parse("services.yaml", []byte("# catalog\n"+validCatalogYAML+"# end\n"))
	if err != nil {
		t.Fatal(err)
	}
	jsonDocument := []byte(`{"services":[{"capabilityBindings":[{"apiVersion":"example.com/cross-source-relation/v1","id":"example.com/cross-source-relation"}],"dependsOn":["foundation"],"root":"backend/sample","id":"sample"},{"root":"backend/foundation","id":"foundation","capabilityBindings":[]}],"kind":"ServiceCatalog","apiVersion":"nexa.dev/service-catalog/v1"}`)
	jsonCatalog, err := servicecatalog.Parse("services.json", jsonDocument)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"foundation", "sample"} {
		left, _ := yamlCatalog.Lookup(id)
		right, _ := jsonCatalog.Lookup(id)
		if left.Source().Digest != right.Source().Digest {
			t.Fatalf("service %s digest changed across syntax/order", id)
		}
	}
	left, _ := yamlCatalog.Lookup("sample")
	right, _ := jsonCatalog.Lookup("sample")
	if left.CapabilityBindings()[0].Source().Digest != right.CapabilityBindings()[0].Source().Digest {
		t.Fatal("binding digest changed across syntax/order")
	}
}

func TestCatalogNodeDigestsTrackOnlyOwnedFacts(t *testing.T) {
	base, err := servicecatalog.Parse("services.yaml", []byte(validCatalogYAML))
	if err != nil {
		t.Fatal(err)
	}
	rootChanged := strings.Replace(validCatalogYAML, "root: backend/sample", "root: services/sample", 1)
	rootCatalog, err := servicecatalog.Parse("services.yaml", []byte(rootChanged))
	if err != nil {
		t.Fatal(err)
	}
	versionChanged := strings.Replace(validCatalogYAML, "cross-source-relation/v1", "cross-source-relation/v2", 1)
	versionCatalog, err := servicecatalog.Parse("services.yaml", []byte(versionChanged))
	if err != nil {
		t.Fatal(err)
	}

	baseSample, _ := base.Lookup("sample")
	rootSample, _ := rootCatalog.Lookup("sample")
	versionSample, _ := versionCatalog.Lookup("sample")
	if baseSample.Source().Digest == rootSample.Source().Digest {
		t.Fatal("service root change did not change service node digest")
	}
	if baseSample.CapabilityBindings()[0].Source().Digest != rootSample.CapabilityBindings()[0].Source().Digest {
		t.Fatal("service root change changed binding node digest")
	}
	if baseSample.Source().Digest != versionSample.Source().Digest {
		t.Fatal("binding version change changed service node digest")
	}
	if baseSample.CapabilityBindings()[0].Source().Digest == versionSample.CapabilityBindings()[0].Source().Digest {
		t.Fatal("binding version change did not change binding node digest")
	}
}

func TestCatalogCanonicalSourceJSONUsesVersionedJCS(t *testing.T) {
	document := []byte(`{"apiVersion":"nexa.dev/service-catalog/v1","kind":"ServiceCatalog","services":[{"id":"foundation","root":"backend/foundation","capabilityBindings":[]},{"id":"zeta","root":"backend/zeta","capabilityBindings":[]},{"id":"sample","root":"backend/sample<>&\u2028node","dependsOn":["zeta","foundation"],"capabilityBindings":[{"id":"nexa.dev/generation-api-proxy","apiVersion":"nexa.dev/generation-api-proxy/v1"}]}]}`)
	catalog, err := servicecatalog.Parse("project/services.json", document)
	if err != nil {
		t.Fatal(err)
	}
	sample, ok := catalog.Lookup("sample")
	if !ok {
		t.Fatal("Lookup(sample) failed")
	}
	wantService := []byte("{\"apiVersion\":\"nexa.dev/service-node/v1\",\"dependsOn\":[\"foundation\",\"zeta\"],\"id\":\"sample\",\"root\":\"backend/sample<>&" + string(rune(0x2028)) + "node\"}")
	if got := sample.CanonicalSourceJSON(); !reflect.DeepEqual(got, wantService) {
		t.Fatalf("service canonical bytes = %q, want %q", got, wantService)
	}
	if sample.Source().Digest != provenance.SHA256(wantService) {
		t.Fatalf("service digest = %s, independently recomputed %s", sample.Source().Digest.String(), provenance.SHA256(wantService).String())
	}

	binding := sample.CapabilityBindings()[0]
	wantBinding := []byte(`{"apiVersion":"nexa.dev/capability-binding-node/v1","capabilityApiVersion":"nexa.dev/generation-api-proxy/v1","id":"nexa.dev/generation-api-proxy","serviceId":"sample"}`)
	if got := binding.CanonicalSourceJSON(); !reflect.DeepEqual(got, wantBinding) {
		t.Fatalf("binding canonical bytes = %q, want %q", got, wantBinding)
	}
	if binding.Source().Digest != provenance.SHA256(wantBinding) {
		t.Fatalf("binding digest = %s, independently recomputed %s", binding.Source().Digest.String(), provenance.SHA256(wantBinding).String())
	}

	serviceBytes := sample.CanonicalSourceJSON()
	serviceBytes[0] = '['
	if got := sample.CanonicalSourceJSON(); !reflect.DeepEqual(got, wantService) {
		t.Fatal("mutating service canonical bytes changed later result")
	}
	bindingBytes := binding.CanonicalSourceJSON()
	bindingBytes[0] = '['
	if got := binding.CanonicalSourceJSON(); !reflect.DeepEqual(got, wantBinding) {
		t.Fatal("mutating binding canonical bytes changed later result")
	}
}

func TestCatalogRejectsMismatchedCapabilityVersionPrefix(t *testing.T) {
	payload := []byte(catalogYAML(`  - id: foundation
    root: backend/foundation
    capabilityBindings:
      - id: nexa.dev/generation-api-proxy
        apiVersion: nexa.dev/other/v1
`))
	_, err := servicecatalog.Parse("services.yaml", payload)
	catalogErr := requireCatalogError(t, err, "service_binding_version_invalid", "")
	if catalogErr.Pointer() != "/services/0/capabilityBindings/0/apiVersion" {
		t.Fatalf("Pointer() = %q", catalogErr.Pointer())
	}
}

func catalogNodeSource(t *testing.T, path, fragment, canonical string) provenance.Source {
	t.Helper()
	ref, err := provenance.RepositoryRef(path, fragment)
	if err != nil {
		t.Fatal(err)
	}
	return provenance.Source{Ref: ref, Digest: provenance.SHA256([]byte(canonical))}
}

func sortSources(sources []provenance.Source) {
	for i := 0; i < len(sources); i++ {
		for j := i + 1; j < len(sources); j++ {
			if sources[j].Ref.String() < sources[i].Ref.String() {
				sources[i], sources[j] = sources[j], sources[i]
			}
		}
	}
}

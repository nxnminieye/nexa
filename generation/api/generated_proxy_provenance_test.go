package api_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	api "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/project/servicecatalog"
	"github.com/nxnminieye/nexa/provenance"
	"gopkg.in/yaml.v3"
)

type protoNodeFixture struct {
	fragment      string
	canonicalJSON string
}

func (node protoNodeFixture) source(t *testing.T) provenance.Source {
	t.Helper()
	ref, err := provenance.RepositoryRef("backend/sample/rpc/desc/sample.proto", node.fragment)
	if err != nil {
		t.Fatal(err)
	}
	return provenance.Source{Ref: ref, Digest: provenance.SHA256([]byte(node.canonicalJSON))}
}

func TestGeneratedProxyDerivedProvenanceMatrixRoundTrip(t *testing.T) {
	fixture := generatedProxyFixture(t, `{"apiVersion":"nexa.dev/proto-field-node/v1","message":"GetSampleRequest","field":"id","number":1}`)
	manifest, err := api.NewManifest(fixture.spec)
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	assertGeneratedProxyProjection(t, manifest, fixture)

	canonical, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	parsedJSON, err := api.Parse("generated-proxy.json", canonical)
	if err != nil {
		t.Fatal(err)
	}
	assertGeneratedProxyProjection(t, parsedJSON, fixture)
	var document map[string]any
	if err := json.Unmarshal(canonical, &document); err != nil {
		t.Fatal(err)
	}
	yamlDocument, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	parsedYAML, err := api.Parse("generated-proxy.yaml", yamlDocument)
	if err != nil {
		t.Fatal(err)
	}
	assertGeneratedProxyProjection(t, parsedYAML, fixture)

	changed := generatedProxyFixture(t, `{"apiVersion":"nexa.dev/proto-field-node/v1","message":"GetSampleRequest","field":"id","number":2}`)
	changedManifest, err := api.NewManifest(changed.spec)
	if err != nil {
		t.Fatal(err)
	}
	if changedManifest.SourceDigest() == manifest.SourceDigest() {
		t.Fatal("request field owner-byte change did not change manifest source digest")
	}
	for _, source := range fixture.sources {
		got, exists := changedManifest.Source(source.Ref)
		if !exists {
			t.Fatalf("changed manifest lost source %s", source.Ref.String())
		}
		if source.Ref == fixture.requestField.Ref {
			if got.Digest == source.Digest {
				t.Fatal("request field digest did not change")
			}
			continue
		}
		if got.Digest != source.Digest {
			t.Fatalf("unrelated source %s digest changed", source.Ref.String())
		}
	}
}

type generatedProxyContractFixture struct {
	spec                                        api.ManifestSpec
	sources                                     []provenance.Source
	method, requestMessage, responseMessage     provenance.Source
	requestField, responseField, catalogBinding provenance.Source
}

func generatedProxyFixture(t *testing.T, requestFieldCanonical string) generatedProxyContractFixture {
	t.Helper()
	method := protoNodeFixture{fragment: "method:Sample.GetSample", canonicalJSON: `{"apiVersion":"nexa.dev/proto-method-node/v2","service":"Sample","method":"GetSample","http":{"method":"GET","path":"/samples/{id}"}}`}.source(t)
	requestMessage := protoNodeFixture{fragment: "message:GetSampleRequest", canonicalJSON: `{"apiVersion":"nexa.dev/proto-message-node/v1","message":"GetSampleRequest"}`}.source(t)
	responseMessage := protoNodeFixture{fragment: "message:GetSampleResponse", canonicalJSON: `{"apiVersion":"nexa.dev/proto-message-node/v1","message":"GetSampleResponse"}`}.source(t)
	requestField := protoNodeFixture{fragment: "field:GetSampleRequest.id", canonicalJSON: requestFieldCanonical}.source(t)
	responseField := protoNodeFixture{fragment: "field:GetSampleResponse.displayName", canonicalJSON: `{"apiVersion":"nexa.dev/proto-field-node/v1","message":"GetSampleResponse","field":"displayName","number":1}`}.source(t)
	catalog, err := servicecatalog.Parse("project/services.yaml", []byte(`apiVersion: nexa.dev/service-catalog/v1
kind: ServiceCatalog
services:
  - id: sample
    root: backend/sample
    capabilityBindings:
      - id: nexa.dev/generation-api-proxy
        apiVersion: nexa.dev/generation-api-proxy/v1
`))
	if err != nil {
		t.Fatal(err)
	}
	service, ok := catalog.Lookup("sample")
	if !ok {
		t.Fatal("Lookup(sample) failed")
	}
	catalogBinding := service.CapabilityBindings()[0].Source()
	sources := []provenance.Source{method, requestMessage, responseMessage, requestField, responseField, catalogBinding}
	derived := func(items ...provenance.Source) api.NodeProvenanceSpec {
		refs := make([]provenance.SourceRef, len(items))
		for index, item := range items {
			refs[index] = item.Ref
		}
		return api.NodeProvenanceSpec{Kind: api.NodeDerived, Refs: refs}
	}
	return generatedProxyContractFixture{
		sources: sources, method: method, requestMessage: requestMessage, responseMessage: responseMessage,
		requestField: requestField, responseField: responseField, catalogBinding: catalogBinding,
		spec: api.ManifestSpec{
			Sources: sources,
			Schemas: []api.SchemaSpec{
				{ID: "sample.get.request", Kind: api.SchemaObject, Provenance: provenancePointer(derived(method, requestMessage, catalogBinding)), Fields: []api.FieldSpec{{Name: "id", SchemaRef: "scalar.string", Required: true, Provenance: derived(method, requestField, catalogBinding)}}},
				{ID: "sample.get.response", Kind: api.SchemaObject, Provenance: provenancePointer(derived(method, responseMessage, catalogBinding)), Fields: []api.FieldSpec{{Name: "displayName", SchemaRef: "scalar.string", Required: true, Provenance: derived(method, responseField, catalogBinding)}}},
				{ID: "scalar.string", Kind: api.SchemaString},
			},
			Operations: []api.OperationSpec{{
				ID: "sample.get", Method: api.MethodGET, Path: "/samples/{id}", Provenance: derived(method, catalogBinding),
				RequestSchemaRef: "sample.get.request", ResponseBody: api.ResponseBodyJSON, ResponseSchemaRef: "sample.get.response",
				RequestBindings: []api.RequestBindingSpec{{Field: "id", Location: api.RequestBindingPath, Name: "id"}},
				Auth:            api.AuthSpec{Mode: api.AuthNone}, Capability: &api.CapabilitySpec{ID: "nexa.dev/generation-api-proxy", APIVersion: "nexa.dev/generation-api-proxy/v1"},
			}},
		},
	}
}

func provenancePointer(value api.NodeProvenanceSpec) *api.NodeProvenanceSpec { return &value }

func assertGeneratedProxyProjection(t *testing.T, manifest api.Manifest, fixture generatedProxyContractFixture) {
	t.Helper()
	wantSources := append([]provenance.Source(nil), fixture.sources...)
	sort.Slice(wantSources, func(left, right int) bool { return wantSources[left].Ref.String() < wantSources[right].Ref.String() })
	if got := manifest.Sources(); !reflect.DeepEqual(got, wantSources) {
		t.Fatalf("Sources() = %#v, want %#v", got, wantSources)
	}
	for _, source := range wantSources {
		if got, exists := manifest.Source(source.Ref); !exists || got != source {
			t.Fatalf("Source(%s) = %#v, %v", source.Ref.String(), got, exists)
		}
	}
	for _, forbidden := range []struct{ path, fragment string }{
		{path: "generated/sample.api", fragment: "route:GetSample"},
		{path: "generated/composition-ir.json", fragment: "operation:sample.get"},
		{path: "generated/api-manifest.json", fragment: "operation:sample.get"},
		{path: "generated/sample_proxy.go", fragment: "operation:sample.get"},
	} {
		ref, err := provenance.RepositoryRef(forbidden.path, forbidden.fragment)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := manifest.Source(ref); exists {
			t.Fatalf("generated proxy projection contains synthetic source %s", ref.String())
		}
	}
	operation, ok := manifest.Operation("sample.get")
	if !ok {
		t.Fatal("Operation(sample.get) missing")
	}
	assertDerivedRefs(t, operation.Provenance(), fixture.method.Ref, fixture.catalogBinding.Ref)
	request, _ := manifest.Schema("sample.get.request")
	requestProvenance, _ := request.Provenance()
	assertDerivedRefs(t, requestProvenance, fixture.method.Ref, fixture.requestMessage.Ref, fixture.catalogBinding.Ref)
	requestField, _ := request.Field("id")
	assertDerivedRefs(t, requestField.Provenance(), fixture.method.Ref, fixture.requestField.Ref, fixture.catalogBinding.Ref)
	response, _ := manifest.Schema("sample.get.response")
	responseProvenance, _ := response.Provenance()
	assertDerivedRefs(t, responseProvenance, fixture.method.Ref, fixture.responseMessage.Ref, fixture.catalogBinding.Ref)
	responseField, _ := response.Field("displayName")
	assertDerivedRefs(t, responseField.Provenance(), fixture.method.Ref, fixture.responseField.Ref, fixture.catalogBinding.Ref)
}

func assertDerivedRefs(t *testing.T, value api.NodeProvenance, refs ...provenance.SourceRef) {
	t.Helper()
	want := append([]provenance.SourceRef(nil), refs...)
	sort.Slice(want, func(left, right int) bool { return want[left].String() < want[right].String() })
	if value.Kind() != api.NodeDerived || !reflect.DeepEqual(value.Refs(), want) {
		t.Fatalf("derived provenance = (%q,%v), want (%q,%v)", value.Kind(), value.Refs(), api.NodeDerived, want)
	}
}

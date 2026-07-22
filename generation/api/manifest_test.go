package api_test

import (
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

func TestManifestPublicContractRoundTrip(t *testing.T) {
	operationRef := mustRef(t, "repo:backend/sample/api/desc/base.api#GetSample")
	requestRef := mustRef(t, "repo:backend/sample/api/desc/base.api#GetSampleRequest")
	requestIDRef := mustRef(t, "repo:backend/sample/api/desc/base.api#GetSampleRequest.id")
	responseRef := mustRef(t, "repo:backend/sample/api/desc/base.api#GetSampleResponse")
	displayNameRef := mustRef(t, "repo:backend/sample/api/desc/base.api#GetSampleResponse.displayName")
	sources := nodeSources(operationRef, requestRef, requestIDRef, responseRef, displayNameRef)

	manifest, err := api.NewManifest(api.ManifestSpec{
		Sources: sources,
		Schemas: []api.SchemaSpec{
			{ID: "sample.get.request", Kind: api.SchemaObject, Provenance: canonicalNode(requestRef), Fields: []api.FieldSpec{{Name: "id", SchemaRef: "scalar.string", Required: true, Provenance: *canonicalNode(requestIDRef)}}},
			{ID: "sample.get.response", Kind: api.SchemaObject, Provenance: canonicalNode(responseRef), Fields: []api.FieldSpec{{Name: "displayName", SchemaRef: "scalar.string", Required: true, Provenance: *canonicalNode(displayNameRef)}}},
			{ID: "scalar.string", Kind: api.SchemaString},
		},
		Operations: []api.OperationSpec{{
			ID: "sample.get", Method: api.MethodGET, Path: "/samples/{id}", Provenance: *canonicalNode(operationRef),
			RequestSchemaRef: "sample.get.request", ResponseBody: api.ResponseBodyJSON, ResponseSchemaRef: "sample.get.response",
			RequestBindings: []api.RequestBindingSpec{{Field: "id", Location: api.RequestBindingPath, Name: "id"}},
			Auth: api.AuthSpec{Mode: api.AuthRequired, Credentials: []api.CredentialSpec{{
				ID: "primary", Type: api.CredentialBearer, Location: api.CredentialLocationHeader, Name: "Authorization",
			}}},
			Permission: "sample.read",
			Capability: &api.CapabilitySpec{ID: "nexa.dev/sample-api", APIVersion: "nexa.dev/sample-api/v1"},
			ErrorProjections: []api.ErrorProjectionSpec{{
				Match:   api.ErrorMatchSpec{Domain: "sample", Code: "not_found"},
				Project: api.ErrorTargetSpec{Domain: "api", Code: "sample_not_found", HTTPStatus: 404},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	assertManifestContract(t, manifest, sources)

	encoded, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	parsed, err := api.Parse("api-manifest.json", encoded)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	assertManifestContract(t, parsed, sources)
}

func assertManifestContract(t *testing.T, manifest api.Manifest, sources []provenance.Source) {
	t.Helper()
	if manifest.APIVersion() != api.APIVersion {
		t.Fatalf("APIVersion() = %q", manifest.APIVersion())
	}
	wantDigest, err := api.ComputeSourceDigest(sources)
	if err != nil || manifest.SourceDigest() != wantDigest {
		t.Fatalf("SourceDigest() = %q, want %q (%v)", manifest.SourceDigest().String(), wantDigest.String(), err)
	}
	if !reflect.DeepEqual(manifest.Sources(), sortedNodeSources(sources)) {
		t.Fatalf("Sources() = %#v", manifest.Sources())
	}
	operation, ok := manifest.Operation("sample.get")
	if !ok {
		t.Fatal("Operation(sample.get) missing")
	}
	byRoute, ok := manifest.OperationByRoute(api.MethodGET, "/samples/{id}")
	if !ok || byRoute.ID() != operation.ID() {
		t.Fatalf("OperationByRoute() = %#v, %v", byRoute, ok)
	}
	if operation.Method() != api.MethodGET || operation.Path() != "/samples/{id}" || operation.Provenance().Refs()[0].String() != "repo:backend/sample/api/desc/base.api#GetSample" || operation.RequestSchemaRef() != "sample.get.request" || operation.ResponseBody() != api.ResponseBodyJSON || operation.ResponseSchemaRef() != "sample.get.response" || operation.Permission() != "sample.read" {
		t.Fatalf("operation = %#v", operation)
	}
	bindings := operation.RequestBindings()
	if len(bindings) != 1 || bindings[0].Field() != "id" || bindings[0].Location() != api.RequestBindingPath || bindings[0].Name() != "id" {
		t.Fatalf("RequestBindings() = %#v", bindings)
	}
	auth := operation.Auth()
	credentials := auth.Credentials()
	if auth.Mode() != api.AuthRequired || len(credentials) != 1 || credentials[0].ID() != "primary" || credentials[0].Type() != api.CredentialBearer || credentials[0].Location() != api.CredentialLocationHeader || credentials[0].Name() != "authorization" {
		t.Fatalf("Auth() = %#v", auth)
	}
	capability, ok := operation.Capability()
	if !ok || capability.ID() != "nexa.dev/sample-api" || capability.APIVersion() != "nexa.dev/sample-api/v1" {
		t.Fatalf("Capability() = %#v, %v", capability, ok)
	}
	projections := operation.ErrorProjections()
	if len(projections) != 1 || projections[0].Match().Domain() != "sample" || projections[0].Match().Code() != "not_found" || projections[0].Project().Domain() != "api" || projections[0].Project().Code() != "sample_not_found" || projections[0].Project().HTTPStatus() != 404 {
		t.Fatalf("ErrorProjections() = %#v", projections)
	}
	request, ok := manifest.Schema("sample.get.request")
	if !ok || request.Kind() != api.SchemaObject || request.ItemSchemaRef() != "" {
		t.Fatalf("request schema = %#v, %v", request, ok)
	}
	if node, exists := request.Provenance(); !exists || node.Refs()[0].String() != "repo:backend/sample/api/desc/base.api#GetSampleRequest" {
		t.Fatalf("request Provenance() = %v, %v", node, exists)
	}
	id, ok := request.Field("id")
	if !ok || id.Name() != "id" || id.SchemaRef() != "scalar.string" || !id.Required() {
		t.Fatalf("request field = %#v, %v", id, ok)
	}
	if id.Provenance().Refs()[0].String() != "repo:backend/sample/api/desc/base.api#GetSampleRequest.id" {
		t.Fatalf("request field provenance = %#v", id.Provenance())
	}
	response, ok := manifest.Schema("sample.get.response")
	if !ok || len(response.Fields()) != 1 {
		t.Fatalf("response schema = %#v, %v", response, ok)
	}
	field := response.Fields()[0]
	if ref := field.Provenance().Refs()[0]; ref.String() != "repo:backend/sample/api/desc/base.api#GetSampleResponse.displayName" {
		t.Fatalf("displayName provenance ref = %v", ref)
	}
	if len(manifest.Operations()) != 1 || len(manifest.Schemas()) != 3 {
		t.Fatalf("operations=%d schemas=%d", len(manifest.Operations()), len(manifest.Schemas()))
	}
}

func canonicalNode(ref provenance.SourceRef) *api.NodeProvenanceSpec {
	return &api.NodeProvenanceSpec{Kind: api.NodeCanonical, Refs: []provenance.SourceRef{ref}}
}

func nodeSources(refs ...provenance.SourceRef) []provenance.Source {
	result := make([]provenance.Source, len(refs))
	for index, ref := range refs {
		result[index] = provenance.Source{Ref: ref, Digest: provenance.SHA256([]byte(ref.String()))}
	}
	return result
}

func sortedNodeSources(input []provenance.Source) []provenance.Source {
	result := append([]provenance.Source(nil), input...)
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j].Ref.String() < result[i].Ref.String() {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

func mustRef(t *testing.T, value string) provenance.SourceRef {
	t.Helper()
	ref, err := provenance.ParseSourceRef(value)
	if err != nil {
		t.Fatalf("ParseSourceRef(%q): %v", value, err)
	}
	return ref
}

package api_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

func TestSourceDigestGoldenAndOrder(t *testing.T) {
	source := provenance.Source{Ref: mustRef(t, "repo:backend/sample/api/desc/base.api"), Digest: provenance.SHA256([]byte("source"))}
	digest, err := api.ComputeSourceDigest([]provenance.Source{source})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := digest.String(), "sha256:7d6a885b171fff223892eeeddbcbe1d60ca4bfd330e941aeab8adb0aaa040e65"; got != want {
		t.Fatalf("digest = %q, want %q", got, want)
	}
	empty, err := api.ComputeSourceDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := empty.String(), "sha256:b69c2b986427008bd25b57cfb4f7d4a0e13dd42d63278a8f709593aac5b32633"; got != want {
		t.Fatalf("empty digest = %q, want %q", got, want)
	}
	second := provenance.Source{Ref: mustRef(t, "repo:backend/sample/api/desc/extra.api"), Digest: provenance.SHA256([]byte("extra"))}
	left, err := api.ComputeSourceDigest([]provenance.Source{source, second})
	if err != nil {
		t.Fatal(err)
	}
	right, err := api.ComputeSourceDigest([]provenance.Source{second, source})
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("reordered source digest differs: %s != %s", left.String(), right.String())
	}
}

func TestSourceDigestRejectsInvalidAndDuplicateSources(t *testing.T) {
	valid := provenance.Source{Ref: mustRef(t, "repo:desc/core.api"), Digest: provenance.SHA256([]byte("source"))}
	tests := []struct {
		name, reason, pointer string
		sources               []provenance.Source
	}{
		{name: "zero ref", reason: "source_ref_invalid", pointer: "/sources/0/ref", sources: []provenance.Source{{Digest: valid.Digest}}},
		{name: "zero digest", reason: "source_digest_invalid", pointer: "/sources/0/digest", sources: []provenance.Source{{Ref: valid.Ref}}},
		{name: "duplicate ref", reason: "source_duplicate", pointer: "/sources/1/ref", sources: []provenance.Source{valid, valid}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := api.ComputeSourceDigest(test.sources)
			manifestError := requireAPIError(t, err, test.reason)
			if manifestError.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", manifestError.Pointer(), test.pointer)
			}
		})
	}
}

func TestSourceCoverageRequiresExactNodeSources(t *testing.T) {
	whole := provenance.Source{Ref: mustRef(t, "repo:desc/core.api"), Digest: provenance.SHA256([]byte("source"))}
	requestRef := mustRef(t, "repo:desc/core.api#Request")
	operationRef := mustRef(t, "repo:desc/core.api#Get")
	_, err := api.NewManifest(api.ManifestSpec{
		Sources:    wholeSources(whole),
		Schemas:    []api.SchemaSpec{{ID: "request", Kind: api.SchemaObject, Provenance: canonicalNode(requestRef), Fields: []api.FieldSpec{}}, {ID: "scalar.string", Kind: api.SchemaString}},
		Operations: []api.OperationSpec{{ID: "get", Method: api.MethodGET, Path: "/", Provenance: *canonicalNode(operationRef), RequestSchemaRef: "request", ResponseBody: api.ResponseBodyNone, RequestBindings: []api.RequestBindingSpec{}, Auth: api.AuthSpec{Mode: api.AuthNone, Credentials: []api.CredentialSpec{}}, ErrorProjections: []api.ErrorProjectionSpec{}}},
	})
	manifestError := requireAPIError(t, err, "node_provenance_ref_unresolved")
	if manifestError.Pointer() != "/operations/0/provenance/refs/0" {
		t.Fatalf("Pointer() = %q", manifestError.Pointer())
	}

	uncovered := mustRef(t, "repo:desc/other.api#Request")
	_, err = api.NewManifest(api.ManifestSpec{
		Sources: []provenance.Source{{Ref: mustRef(t, "repo:desc/other.api#Other"), Digest: provenance.SHA256([]byte("node"))}},
		Schemas: []api.SchemaSpec{{ID: "request", Kind: api.SchemaObject, Provenance: canonicalNode(uncovered), Fields: []api.FieldSpec{}}},
	})
	manifestError = requireAPIError(t, err, "node_provenance_ref_unresolved")
	if manifestError.Pointer() != "/schemas/0/provenance/refs/0" {
		t.Fatalf("Pointer() = %q", manifestError.Pointer())
	}
}

func TestCanonicalEmptyObjectListsAndZeroManifest(t *testing.T) {
	requestRef := mustRef(t, "repo:desc/core.api#Request")
	manifest, err := api.NewManifest(api.ManifestSpec{
		Sources: nodeSources(requestRef),
		Schemas: []api.SchemaSpec{{ID: "request", Kind: api.SchemaObject, Provenance: canonicalNode(requestRef), Fields: []api.FieldSpec{}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"fields":[]`)) {
		t.Fatalf("canonical object omitted empty fields: %s", encoded)
	}
	empty, err := api.NewManifest(api.ManifestSpec{})
	if err != nil {
		t.Fatal(err)
	}
	emptyJSON, err := empty.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"apiVersion":"nexa.dev/api-manifest/v1","kind":"APIManifest","sourceDigest":"sha256:b69c2b986427008bd25b57cfb4f7d4a0e13dd42d63278a8f709593aac5b32633","sources":[],"schemas":[],"operations":[]}` + "\n"
	if string(emptyJSON) != want {
		t.Fatalf("empty canonical = %s, want %s", emptyJSON, want)
	}
	if _, err := (api.Manifest{}).CanonicalJSON(); err == nil {
		t.Fatal("zero Manifest serialized")
	}
}

func TestManifestValuesAreImmutable(t *testing.T) {
	requestRef := mustRef(t, "repo:desc/core.api#Request")
	fieldRef := mustRef(t, "repo:desc/core.api#Request.id")
	operationRef := mustRef(t, "repo:desc/core.api#Get")
	sourcesInput := nodeSources(requestRef, fieldRef, operationRef)
	wantSources := sortedNodeSources(sourcesInput)
	fields := []api.FieldSpec{{Name: "id", SchemaRef: "scalar.string", Required: true, Provenance: *canonicalNode(fieldRef)}}
	bindings := []api.RequestBindingSpec{{Field: "id", Location: api.RequestBindingPath, Name: "id"}}
	credentials := []api.CredentialSpec{{ID: "primary", Type: api.CredentialBearer, Location: api.CredentialLocationHeader, Name: "Authorization"}}
	projections := []api.ErrorProjectionSpec{{Match: api.ErrorMatchSpec{Domain: "sample", Code: "not_found"}, Project: api.ErrorTargetSpec{Domain: "api", Code: "not_found", HTTPStatus: 404}}}
	spec := api.ManifestSpec{
		Sources:    sourcesInput,
		Schemas:    []api.SchemaSpec{{ID: "request", Kind: api.SchemaObject, Provenance: canonicalNode(requestRef), Fields: fields}, {ID: "scalar.string", Kind: api.SchemaString}},
		Operations: []api.OperationSpec{{ID: "get", Method: api.MethodGET, Path: "/{id}", Provenance: *canonicalNode(operationRef), RequestSchemaRef: "request", ResponseBody: api.ResponseBodyNone, RequestBindings: bindings, Auth: api.AuthSpec{Mode: api.AuthRequired, Credentials: credentials}, ErrorProjections: projections}},
	}
	manifest, err := api.NewManifest(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Sources[0] = provenance.Source{}
	fields[0] = api.FieldSpec{}
	bindings[0] = api.RequestBindingSpec{}
	credentials[0] = api.CredentialSpec{}
	projections[0] = api.ErrorProjectionSpec{}

	sources := manifest.Sources()
	sources[0] = provenance.Source{}
	schemas := manifest.Schemas()
	schemas[0] = api.Schema{}
	operations := manifest.Operations()
	operations[0] = api.Operation{}
	operation, _ := manifest.Operation("get")
	returnedBindings := operation.RequestBindings()
	returnedBindings[0] = api.RequestBinding{}
	returnedCredentials := operation.Auth().Credentials()
	returnedCredentials[0] = api.Credential{}
	returnedErrors := operation.ErrorProjections()
	returnedErrors[0] = api.ErrorProjection{}
	request, _ := manifest.Schema("request")
	returnedFields := request.Fields()
	returnedFields[0] = api.Field{}

	operation, ok := manifest.Operation("get")
	if !ok || operation.RequestBindings()[0].Name() != "id" || operation.Auth().Credentials()[0].Name() != "authorization" || operation.ErrorProjections()[0].Match().Code() != "not_found" {
		t.Fatalf("operation mutated: %#v", operation)
	}
	request, ok = manifest.Schema("request")
	if !ok || request.Fields()[0].Name() != "id" {
		t.Fatalf("schema mutated: %#v", request)
	}
	if !reflect.DeepEqual(manifest.Sources(), wantSources) {
		t.Fatalf("sources mutated: %#v", manifest.Sources())
	}
}

func wholeSources(source provenance.Source) []provenance.Source {
	return []provenance.Source{source}
}

func requireAPIError(t *testing.T, err error, reason string) *api.Error {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil")
	}
	var manifestError *api.Error
	if !errors.As(err, &manifestError) {
		t.Fatalf("error type = %T, want *api.Error", err)
	}
	if manifestError.Code() != "api_manifest_invalid" || manifestError.Reason() != reason {
		t.Fatalf("error = code %q reason %q, want api_manifest_invalid/%s", manifestError.Code(), manifestError.Reason(), reason)
	}
	return manifestError
}

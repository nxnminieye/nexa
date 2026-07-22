package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/provenance"
	goctlparser "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/parser"
)

func TestGeneratedDocumentMergeRenderManifestAndSnapshot(t *testing.T) {
	method := source(t, "rpc/sample.proto", "method:sample.v1.Sample.Get", "method")
	message := source(t, "rpc/sample.proto", "message:sample.v1.GetRequest", "message")
	derived := generatedProvenance(t, method, message)
	spec := httpapi.GeneratedDocumentSpec{
		Types: []httpapi.GeneratedTypeSpec{
			{Name: "ProxyRequest", Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: derived, Fields: []httpapi.GeneratedFieldSpec{{Path: []string{"ID"}, Required: true, ValueType: httpapi.ValueTypeSpec{Kind: httpapi.ValueScalar, Name: "string"}, Binding: &httpapi.BindingSpec{Location: api.RequestBindingPath, Name: "id"}, Provenance: derived}}},
			{Name: "ProxyResponse", Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: derived, Fields: []httpapi.GeneratedFieldSpec{
				{Path: []string{"Name"}, Required: true, ValueType: httpapi.ValueTypeSpec{Kind: httpapi.ValueScalar, Name: "string"}, Provenance: derived},
				{Path: []string{"Tags"}, Required: true, ValueType: httpapi.ValueTypeSpec{Kind: httpapi.ValueArray, Element: &httpapi.ValueTypeSpec{Kind: httpapi.ValueScalar, Name: "string"}}, Provenance: derived},
			}},
		},
		Operations: []httpapi.GeneratedOperationSpec{{
			ID: "sample.get", Method: api.MethodGET, Path: "/proxy/{id}", RequestType: "ProxyRequest", ResponseBody: api.ResponseBodyJSON, ResponseType: "ProxyResponse",
			Auth: httpapi.AuthSpec{Mode: api.AuthRequired, Credentials: []httpapi.CredentialSpec{
				{ID: "primary", Type: api.CredentialBearer, Location: api.CredentialLocationHeader, Name: "Authorization"},
				{ID: "service-key", Type: api.CredentialAPIKey, Location: api.CredentialLocationQuery, Name: "api_key"},
			}}, Permission: "sample.read", Capability: &httpapi.CapabilitySpec{ID: "nexa.dev/sample-api", APIVersion: "nexa.dev/sample-api/v1"},
			ErrorProjections: []api.ErrorProjectionSpec{
				{Match: api.ErrorMatchSpec{Domain: "sample", Code: "validation"}, Project: api.ErrorTargetSpec{Domain: "api", Code: "validation_failed", HTTPStatus: 400}},
				{Match: api.ErrorMatchSpec{Domain: "sample", Code: "not_found"}, Project: api.ErrorTargetSpec{Domain: "api", Code: "sample_not_found", HTTPStatus: 404}},
			}, Provenance: derived,
		}},
	}
	document, err := httpapi.NewGeneratedDocument(spec)
	if err != nil {
		t.Fatalf("NewGeneratedDocument() error = %v", err)
	}
	spec.Types[0].Fields[0].Path[0] = "Mutated"
	spec.Operations[0].Path = "/mutated"
	typeValue, _ := document.Type("ProxyRequest")
	field, _ := typeValue.Field("ID")
	if typeValue.Provenance().Kind() != httpapi.NodeFactGenerated || field.Path()[0] != "ID" {
		t.Fatalf("generated document aliases input: %#v %#v", typeValue, field)
	}
	operationValue, _ := document.Operation("sample.get")
	credentials := operationValue.Auth().Credentials()
	errors := operationValue.ErrorProjections()
	if len(credentials) != 2 || credentials[0].ID() != "primary" || credentials[1].ID() != "service-key" || len(errors) != 2 || errors[0].Match.Code != "not_found" || errors[1].Match.Code != "validation" {
		t.Fatalf("generated metadata is not canonical: credentials=%#v errors=%#v", credentials, errors)
	}
	if _, ok := typeValue.Provenance().NativeSource(); ok {
		t.Fatal("generated provenance exposed NativeSource")
	}
	if _, ok := typeValue.Provenance().CanonicalSourceJSON(); ok {
		t.Fatal("generated provenance exposed canonical API bytes")
	}

	rendered, err := httpapi.RenderGenerated(document)
	if err != nil {
		t.Fatalf("RenderGenerated() error = %v", err)
	}
	parsed, err := goctlparser.Parse("/nonexistent/generated.api", rendered)
	if err != nil || parsed.Validate() != nil {
		t.Fatalf("rendered formal API rejected: %v", err)
	}
	if err := httpapi.VerifyRenderedGenerated("generated.api", rendered, document); err != nil {
		t.Fatalf("VerifyRenderedGenerated() error = %v", err)
	}
	changed := append([]byte(nil), rendered...)
	changed = bytes.Replace(changed, []byte("/proxy/:id"), []byte("/other/:id"), 1)
	if err := httpapi.VerifyRenderedGenerated("generated.api", changed, document); err == nil {
		t.Fatal("VerifyRenderedGenerated() accepted semantic drift")
	}

	manifestSpec, err := httpapi.ManifestSpec(document)
	if err != nil {
		t.Fatalf("ManifestSpec() error = %v", err)
	}
	manifest, err := api.NewManifest(manifestSpec)
	if err != nil {
		t.Fatalf("api.NewManifest() error = %v", err)
	}
	operation, ok := manifest.Operation("sample.get")
	if !ok || operation.Provenance().Kind() != api.NodeDerived || len(operation.Provenance().Refs()) != 2 {
		t.Fatalf("manifest operation = %#v, %v", operation, ok)
	}
	manifestCredentials := operation.Auth().Credentials()
	manifestErrors := operation.ErrorProjections()
	if len(manifestCredentials) != 2 || manifestCredentials[0].ID() != "primary" || manifestCredentials[1].ID() != "service-key" || len(manifestErrors) != 2 || manifestErrors[0].Match().Code() != "not_found" || manifestErrors[1].Match().Code() != "validation" {
		t.Fatalf("ManifestSpec lost generated auth/error metadata: auth=%#v errors=%#v", manifestCredentials, manifestErrors)
	}

	repository := t.TempDir()
	writeAPI(t, repository, "native.api", fmt.Sprintf(`syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-api/v1")
type HealthRequest { Probe string %cform:"probe"%c }
type HealthResponse { OK bool }
@server (nexaOperationId: "health.get" nexaAuthMode: "none")
service core-api { @handler health get /health (HealthRequest) returns (HealthResponse) }`, '`', '`'))
	native, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: repository, EntryFile: "native.api"})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := httpapi.Merge(native, document)
	if err != nil || len(merged.Types()) != 4 || len(merged.Operations()) != 2 {
		t.Fatalf("Merge() = %#v, %v", merged, err)
	}

	canonical, err := httpapi.CanonicalJSON(merged)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	snapshotSource, _ := provenance.ParseDomainSource("generated/http-api-ir.json")
	snapshot, err := httpapi.ParseSnapshot(snapshotSource, canonical)
	if err != nil {
		t.Fatalf("ParseSnapshot() error = %v", err)
	}
	readback, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(readback, canonical) {
		t.Fatalf("snapshot readback differs: %v", err)
	}
	readback[0] ^= 0xff
	again, _ := snapshot.CanonicalJSON()
	if again[0] == readback[0] {
		t.Fatal("Snapshot.CanonicalJSON() returned aliased bytes")
	}
	var tampered map[string]any
	if err := json.Unmarshal(canonical, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered["operations"].([]any)[0].(map[string]any)["path"] = "/tampered"
	ordinary, _ := json.Marshal(tampered)
	tamperedCanonical, _ := jcs.Transform(ordinary)
	if _, err := httpapi.ParseSnapshot(snapshotSource, tamperedCanonical); err == nil {
		t.Fatal("ParseSnapshot() accepted canonical semantic tampering")
	}
	if err := json.Unmarshal(canonical, &tampered); err != nil {
		t.Fatal(err)
	}
	for _, value := range tampered["operations"].([]any) {
		operation := value.(map[string]any)
		credentials := operation["auth"].(map[string]any)["credentials"].([]any)
		if len(credentials) == 2 {
			credentials[0], credentials[1] = credentials[1], credentials[0]
		}
	}
	ordinary, _ = json.Marshal(tampered)
	tamperedCanonical, _ = jcs.Transform(ordinary)
	if _, err := httpapi.ParseSnapshot(snapshotSource, tamperedCanonical); err == nil {
		t.Fatal("ParseSnapshot() accepted noncanonical credential order")
	}
	if err := json.Unmarshal(canonical, &tampered); err != nil {
		t.Fatal(err)
	}
	for _, value := range tampered["operations"].([]any) {
		projections := value.(map[string]any)["errorProjections"].([]any)
		if len(projections) == 2 {
			projections[0], projections[1] = projections[1], projections[0]
		}
	}
	ordinary, _ = json.Marshal(tampered)
	tamperedCanonical, _ = jcs.Transform(ordinary)
	if _, err := httpapi.ParseSnapshot(snapshotSource, tamperedCanonical); err == nil {
		t.Fatal("ParseSnapshot() accepted noncanonical error projection order")
	}
	var schema map[string]any
	if err := json.Unmarshal(httpapi.Schema(), &schema); err != nil || schema["$id"] == nil {
		t.Fatalf("Schema() = %#v, %v", schema, err)
	}
	if _, err := httpapi.ParseSnapshot(snapshotSource, append(bytes.TrimSuffix(canonical, []byte{'\n'}), []byte(`,"unknown":true}`)...)); err == nil {
		t.Fatal("ParseSnapshot() accepted noncanonical/unknown data")
	}
}

func TestManifestSpecRejectsMapWithoutChangingWireShape(t *testing.T) {
	a := source(t, "rpc/sample.proto", "message:A", "a")
	b := source(t, "project/services.yaml", "service:sample.capability:nexa.dev/sample", "b")
	derived := generatedProvenance(t, a, b)
	document, err := httpapi.NewGeneratedDocument(httpapi.GeneratedDocumentSpec{Types: []httpapi.GeneratedTypeSpec{{
		Name: "MapResponse", Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: derived,
		Fields: []httpapi.GeneratedFieldSpec{{Path: []string{"Attributes"}, Required: true, ValueType: httpapi.ValueTypeSpec{Kind: httpapi.ValueMap, Key: &httpapi.ValueTypeSpec{Kind: httpapi.ValueScalar, Name: "string"}, Value: &httpapi.ValueTypeSpec{Kind: httpapi.ValueScalar, Name: "string"}}, Provenance: derived}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := httpapi.ManifestSpec(document); err == nil {
		t.Fatal("ManifestSpec() silently changed map object semantics")
	} else if typed, ok := err.(*httpapi.Error); !ok || typed.Reason() != "map_schema_unrepresentable" {
		t.Fatalf("ManifestSpec() error = %T %v", err, err)
	}
}

func TestGeneratedProvenanceAndSourceConflictGates(t *testing.T) {
	a := source(t, "rpc/sample.proto", "message:A", "a")
	b := source(t, "project/services.yaml", "service:sample.capability:nexa.dev/sample", "b")
	for name, values := range map[string][]provenance.Source{"empty": nil, "one": {a}, "duplicate": {a, a}} {
		t.Run(name, func(t *testing.T) {
			if _, err := httpapi.NewGeneratedProvenance(values); err == nil {
				t.Fatal("NewGeneratedProvenance() succeeded")
			}
		})
	}
	valid := generatedProvenance(t, a, b)
	mutatedSources := valid.Sources()
	mutatedSources[0] = provenance.Source{}
	if reflect.DeepEqual(valid.Sources(), mutatedSources) {
		t.Fatal("Sources() returned aliased slice")
	}

	conflicting := provenance.Source{Ref: a.Ref, Digest: provenance.SHA256([]byte("different"))}
	other := source(t, "rpc/sample.proto", "message:B", "other")
	conflictingProvenance := generatedProvenance(t, conflicting, other)
	_, err := httpapi.NewGeneratedDocument(httpapi.GeneratedDocumentSpec{Types: []httpapi.GeneratedTypeSpec{
		{Name: "A", Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: valid},
		{Name: "B", Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: conflictingProvenance},
	}})
	if err == nil {
		t.Fatal("NewGeneratedDocument() accepted one SourceRef with conflicting digests")
	}
}

func generatedProvenance(t *testing.T, sources ...provenance.Source) httpapi.NodeProvenance {
	t.Helper()
	value, err := httpapi.NewGeneratedProvenance(sources)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func source(t *testing.T, path, fragment, canonical string) provenance.Source {
	t.Helper()
	return provenance.Source{Ref: mustRef(t, path, fragment), Digest: provenance.SHA256([]byte(canonical))}
}

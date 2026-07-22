package httpapi_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/provenance"
)

type recordingResolver struct {
	want provenance.Source
	seen []provenance.Source
}

func (r *recordingResolver) Resolve(_ context.Context, ref provenance.SourceRef, digest provenance.Digest) error {
	source := provenance.Source{Ref: ref, Digest: digest}
	r.seen = append(r.seen, source)
	if source != r.want {
		return os.ErrInvalid
	}
	return nil
}

func TestLoadProjectsFormalAPIIntoNativeOwnerNodes(t *testing.T) {
	repository := t.TempDir()
	writeAPI(t, repository, "desc/types.api", fmt.Sprintf(`syntax = "v1"

type GetSampleResponse {
  Name string %cjson:"name"%c
}
`, '`', '`'))
	originRef := mustRef(t, "rpc/sample.proto", "message:sample.v1.Sample.field:name")
	originDigest := provenance.SHA256([]byte("rpc-name"))
	writeAPI(t, repository, "desc/core.api", fmt.Sprintf(`syntax = "v1"

info (
  nexaContractVersion: "nexa.dev/http-api/v1"
)

import "types.api"

type GetSampleRequest {
  ID string %cpath:"id"%c
  Query *string %cform:"q,optional"%c
  Trace string %cheader:"X-Trace"%c
  Payload {
    Name string %cjson:"name" nexaOriginRef:"%s" nexaOriginDigest:"%s"%c
  } %cjson:"payload"%c
}

@server (
  nexaOperationId: "sample.get"
  nexaAuthMode: "required"
  nexaCredentialId: "primary"
  nexaCredentialType: "bearer"
  nexaCredentialLocation: "header"
  nexaCredentialName: "Authorization"
  nexaPermission: "sample.read"
  nexaCapabilityId: "nexa.dev/sample-api"
  nexaCapabilityVersion: "nexa.dev/sample-api/v1"
)
service core-api {
  @handler getSample
  get /samples/:id (GetSampleRequest) returns (GetSampleResponse)
}
`, '`', '`', '`', '`', '`', '`', '`', originRef.String(), originDigest.String(), '`', '`', '`'))
	resolver := &recordingResolver{want: provenance.Source{Ref: originRef, Digest: originDigest}}

	document, err := httpapi.Load(context.Background(), httpapi.LoadOptions{
		RepositoryRoot: repository,
		EntryFile:      "desc/core.api",
		SourceResolver: resolver,
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := document.APIVersion(), httpapi.APIVersion; got != want {
		t.Fatalf("APIVersion() = %q, want %q", got, want)
	}
	if got := len(document.Types()); got != 2 {
		t.Fatalf("Types() length = %d, want 2", got)
	}
	response, ok := document.Type("GetSampleResponse")
	if !ok || response.Shape().Kind() != httpapi.ValueObject {
		t.Fatalf("imported response type = %#v, %v", response, ok)
	}
	assertNativeOwner(t, response.Provenance(), "desc/types.api", "type:GetSampleResponse")

	request, ok := document.Type("GetSampleRequest")
	if !ok {
		t.Fatal("GetSampleRequest not found")
	}
	assertNativeOwner(t, request.Provenance(), "desc/core.api", "type:GetSampleRequest")
	fields := request.Fields()
	if got := len(fields); got != 5 {
		t.Fatalf("request Fields() length = %d, want 5", got)
	}
	query, ok := request.Field("Query")
	if !ok || query.Required() || query.ValueType().Kind() != httpapi.ValueOptional {
		t.Fatalf("Query = %#v, %v", query, ok)
	}
	trace, _ := request.Field("Trace")
	binding, ok := trace.Binding()
	if !ok || binding.Location() != api.RequestBindingHeader || binding.Name() != "x-trace" {
		t.Fatalf("Trace binding = %#v, %v", binding, ok)
	}
	nested, ok := request.Field("Payload.Name")
	if !ok || nested.ValueType().Kind() != httpapi.ValueScalar {
		t.Fatalf("nested field = %#v, %v", nested, ok)
	}
	origin, ok := nested.Origin()
	if !ok || origin != resolver.want || len(resolver.seen) != 1 {
		t.Fatalf("origin = %#v, %v; resolver calls = %#v", origin, ok, resolver.seen)
	}
	assertNativeOwner(t, nested.Provenance(), "desc/core.api", "field:GetSampleRequest.Payload.Name")

	operation, ok := document.Operation("sample.get")
	if !ok {
		t.Fatal("sample.get operation not found")
	}
	if operation.Method() != api.MethodGET || operation.Path() != "/samples/{id}" || operation.RequestType() != "GetSampleRequest" || operation.ResponseType() != "GetSampleResponse" {
		t.Fatalf("operation projection = %#v", operation)
	}
	if operation.ResponseBody() != api.ResponseBodyJSON || operation.Permission() != "sample.read" {
		t.Fatalf("operation response/permission = %q, %q", operation.ResponseBody(), operation.Permission())
	}
	auth := operation.Auth()
	credentials := auth.Credentials()
	if auth.Mode() != api.AuthRequired || len(credentials) != 1 || credentials[0].Name() != "authorization" {
		t.Fatalf("operation auth = %#v", auth)
	}
	capability, ok := operation.Capability()
	if !ok || capability.ID() != "nexa.dev/sample-api" || capability.APIVersion() != "nexa.dev/sample-api/v1" {
		t.Fatalf("operation capability = %#v, %v", capability, ok)
	}
	assertNativeOwner(t, operation.Provenance(), "desc/core.api", "route:GET /samples/{id}")
}

func TestLoadRejectsInvalidFormalAndTypedMetadata(t *testing.T) {
	tests := map[string]struct {
		source, reason string
	}{
		"parser error": {source: `syntax = "v1"
type Broken {`, reason: "parser_error"},
		"missing contract version": {source: `syntax = "v1"
type Request { ID string }
service api { @handler x get /x (Request) }`, reason: "contract_version_missing"},
		"wrong contract version": {source: `syntax = "v1"
info (nexaContractVersion: "wrong")
type Request { ID string }
service api { @handler x get /x (Request) }`, reason: "contract_version_unsupported"},
		"unknown nexa server key": {source: `syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-api/v1")
type Request { ID string }
@server (nexaOperationId: "x.get" nexaAuthMode: "none" nexaUnknown: "x")
service api { @handler x get /x (Request) }`, reason: "server_metadata_unknown"},
		"half credential": {source: `syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-api/v1")
type Request { ID string }
@server (nexaOperationId: "x.get" nexaAuthMode: "required" nexaCredentialId: "primary")
service api { @handler x get /x (Request) }`, reason: "server_metadata_invalid"},
		"half origin": {source: fmt.Sprintf(`syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-api/v1")
type Request { ID string %cjson:"id" nexaOriginRef:"repo:x.api#field%%3AX.id"%c }
@server (nexaOperationId: "x.get" nexaAuthMode: "none")
service api { @handler x get /x (Request) }`, '`', '`'), reason: "field_tags_invalid"},
		"multiple bindings": {source: fmt.Sprintf(`syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-api/v1")
type Request { ID string %cpath:"id" json:"id"%c }
@server (nexaOperationId: "x.get" nexaAuthMode: "none")
service api { @handler x get /x/:id (Request) }`, '`', '`'), reason: "field_tags_invalid"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			repository := t.TempDir()
			writeAPI(t, repository, "core.api", test.source)
			_, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: repository, EntryFile: "core.api"})
			if err == nil {
				t.Fatal("Load() succeeded")
			}
			var typed *httpapi.Error
			if !errors.As(err, &typed) || typed.Reason() != test.reason {
				t.Fatalf("Load() error = %T %v, want reason %q", err, err, test.reason)
			}
		})
	}
}

func assertNativeOwner(t *testing.T, owner httpapi.NodeProvenance, path, fragment string) {
	t.Helper()
	if owner.Kind() != httpapi.NodeFactNative {
		t.Fatalf("Kind() = %q, want native", owner.Kind())
	}
	source, ok := owner.NativeSource()
	if !ok {
		t.Fatal("NativeSource() not available")
	}
	wantRef := mustRef(t, path, fragment)
	canonical, ok := owner.CanonicalSourceJSON()
	if !ok || len(canonical) == 0 || canonical[len(canonical)-1] == '\n' {
		t.Fatalf("CanonicalSourceJSON() = %q, %v", canonical, ok)
	}
	if source.Ref != wantRef || source.Digest != provenance.SHA256(canonical) {
		t.Fatalf("native source = %#v, want ref %q and canonical digest", source, wantRef.String())
	}
	canonical[0] ^= 0xff
	again, _ := owner.CanonicalSourceJSON()
	if again[0] == canonical[0] {
		t.Fatal("CanonicalSourceJSON() returned aliased bytes")
	}
}

func writeAPI(t *testing.T, root, name, source string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustRef(t *testing.T, path, fragment string) provenance.SourceRef {
	t.Helper()
	ref, err := provenance.RepositoryRef(path, fragment)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

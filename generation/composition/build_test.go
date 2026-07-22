package composition_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/project/servicecatalog"
	"github.com/nxnminieye/nexa/provenance"
)

func TestBuildProjectsSelectedUnaryProxyIntoGeneratedAPI(t *testing.T) {
	catalog := parseCatalog(t, true)
	proto := compileProtocol(t, validProtocolSource(false))
	native := loadNative(t, "health.get", "/health")

	document, err := composition.Build(catalog, []protocol.Document{proto}, native, composition.BuildOptions{
		CoreServiceID: "core", ConsumerModulePath: "example.com/consumer",
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	generated, err := composition.GeneratedAPI(document)
	if err != nil {
		t.Fatalf("GeneratedAPI() error = %v", err)
	}
	operation, ok := generated.Operation("account.get")
	if !ok || operation.Method() != api.MethodGET || operation.Path() != "/accounts/{id}" || operation.Permission() != "account.read" {
		t.Fatalf("generated operation = %#v, %v", operation, ok)
	}
	capability, ok := operation.Capability()
	if !ok || capability.ID() != composition.CapabilityID || capability.APIVersion() != composition.CapabilityVersion {
		t.Fatalf("generated capability = %#v, %v", capability, ok)
	}
	request, ok := generated.Type(operation.RequestType())
	if !ok || len(request.Fields()) != 1 {
		t.Fatalf("generated request = %#v, %v", request, ok)
	}
	response, ok := generated.Type(operation.ResponseType())
	if !ok || len(response.Fields()) != 1 {
		t.Fatalf("generated response = %#v, %v", response, ok)
	}
	service, _ := catalog.Lookup("account")
	bindingSource := service.CapabilityBindings()[0].Source()
	method, _ := proto.Method("account.v1.AccountService.Get")
	requestMessage, _ := proto.Message("account.v1.GetAccountRequest")
	idField := requestMessage.Fields()[0]
	assertSources(t, operation.Provenance().Sources(), method.Source(), bindingSource)
	assertSources(t, request.Provenance().Sources(), method.Source(), requestMessage.Source(), bindingSource)
	assertSources(t, request.Fields()[0].Provenance().Sources(), method.Source(), idField.Source(), bindingSource)

	merged, err := httpapi.Merge(native, generated)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	manifestSpec, err := httpapi.ManifestSpec(merged)
	if err != nil {
		t.Fatalf("ManifestSpec() error = %v", err)
	}
	manifest, err := api.NewManifest(manifestSpec)
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	projected, ok := manifest.Operation("account.get")
	if !ok || projected.Provenance().Kind() != api.NodeDerived || len(projected.Provenance().Refs()) != 2 {
		t.Fatalf("manifest operation = %#v, %v", projected, ok)
	}
}

func TestCompositionConsumesMethodContextOnlyWhenHTTPProxyExists(t *testing.T) {
	source := strings.Replace(validProtocolSource(false), "service AccountService {", `service InternalService {
  rpc Internal(GetAccountRequest) returns (GetAccountResponse) {
    option (nexa.protocol.v1.rpc_context) = {
      context_fields: { source: TENANT_ID rpc_field: "tenant_id" }
    };
  }
}
service AccountService {`, 1)
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, source)}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	generated, err := composition.GeneratedAPI(document)
	if err != nil || len(generated.Operations()) != 1 {
		t.Fatalf("GeneratedAPI() operations = %#v, %v", generated.Operations(), err)
	}
}

func TestBuildWithoutExactCapabilityBindingProducesNoProxy(t *testing.T) {
	proto := compileProtocol(t, validProtocolSource(false))
	native := loadNative(t, "health.get", "/health")
	for name, catalog := range map[string]servicecatalog.Catalog{
		"empty":  servicecatalog.Empty(),
		"absent": parseCatalog(t, false),
	} {
		t.Run(name, func(t *testing.T) {
			document, err := composition.Build(catalog, []protocol.Document{proto}, native, composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			generated, err := composition.GeneratedAPI(document)
			if err != nil || len(generated.Operations()) != 0 || len(generated.Types()) != 0 {
				t.Fatalf("GeneratedAPI() = %#v, %v", generated, err)
			}
		})
	}
}

func TestBuildRejectsNativeProxyCollisionAndMapProjection(t *testing.T) {
	catalog := parseCatalog(t, true)
	for name, native := range map[string]httpapi.Document{
		"operation": loadNative(t, "account.get", "/native"),
		"route":     loadNative(t, "native.get", "/accounts/{id}"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := composition.Build(catalog, []protocol.Document{compileProtocol(t, validProtocolSource(false))}, native, composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
			assertCompositionError(t, err, "native_operation_collision")
		})
	}
	_, err := composition.Build(catalog, []protocol.Document{compileProtocol(t, validProtocolSource(true))}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	assertCompositionError(t, err, "map_mapping_unrepresentable")
}

func TestBuildRejectsGeneratedDocumentAsNativeAPI(t *testing.T) {
	catalog := parseCatalog(t, true)
	proto := compileProtocol(t, validProtocolSource(false))
	service, _ := catalog.Lookup("account")
	method, _ := proto.Method("account.v1.AccountService.Get")
	owner, err := httpapi.NewGeneratedProvenance([]provenance.Source{method.Source(), service.CapabilityBindings()[0].Source()})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := httpapi.NewGeneratedDocument(httpapi.GeneratedDocumentSpec{Types: []httpapi.GeneratedTypeSpec{{Name: "GeneratedOnly", Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: owner}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = composition.Build(catalog, []protocol.Document{proto}, generated, composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	assertCompositionError(t, err, "native_api_invalid")
}

func TestBuildRejectsMissingCoreAndManyToOneBindings(t *testing.T) {
	native := loadNative(t, "health.get", "/health")
	missingCore, err := servicecatalog.Parse("project/services.yaml", []byte(fmt.Sprintf("apiVersion: nexa.dev/service-catalog/v1\nkind: ServiceCatalog\nservices:\n  - id: account\n    root: backend/account\n    capabilityBindings:\n      - id: %s\n        apiVersion: %s\n", composition.CapabilityID, composition.CapabilityVersion)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = composition.Build(missingCore, []protocol.Document{compileProtocol(t, validProtocolSource(false))}, native, composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	assertCompositionError(t, err, "core_service_missing")

	manyToOne := strings.Replace(validProtocolSource(false), `string id = 1; int64 tenant_id = 2; string request_id = 3; string trace_id = 4;`, `int64 id = 1; string request_id = 3; string trace_id = 4;`, -1)
	manyToOne = strings.Replace(manyToOne, `context_fields: { source: TENANT_ID rpc_field: "tenant_id" }`, `context_fields: { source: TENANT_ID rpc_field: "id" }`, -1)
	_, err = composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, manyToOne)}, native, composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	assertCompositionError(t, err, "many_to_one_mapping")
}

func TestBuildRejectsErrorProjectionWithoutRequestAndTraceContextBindings(t *testing.T) {
	for name, marker := range map[string]string{
		"request-id": `      context_fields: { source: REQUEST_ID rpc_field: "request_id" }
`,
		"trace-id": `      context_fields: { source: TRACE_ID rpc_field: "trace_id" }
`,
	} {
		t.Run(name, func(t *testing.T) {
			source := strings.Replace(validProtocolSource(false), marker, "", 1)
			_, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, source)}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
			assertCompositionError(t, err, "error_context_binding_missing")
		})
	}
}

func TestBuildResolvesTypedFieldOwnerAndProjectsClosedNumericScalars(t *testing.T) {
	source := strings.ReplaceAll(validProtocolSource(false), "account.v1", "account.id.v1")
	source = strings.Replace(source, "string name = 1;", "fixed32 name = 1;", -1)
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, source)}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	generated, err := composition.GeneratedAPI(document)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := httpapi.ManifestSpec(generated)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := api.NewManifest(spec)
	if err != nil {
		t.Fatal(err)
	}
	var fieldRef string
	for _, schema := range manifest.Schemas() {
		if field, ok := schema.Field("Name"); ok {
			fieldRef = field.SchemaRef()
		}
	}
	fieldSchema, ok := manifest.Schema(fieldRef)
	if !ok || fieldSchema.Kind() != api.SchemaInteger {
		t.Fatalf("fixed32 schema = %#v, %v", fieldSchema, ok)
	}
}

func TestBuildAllowsDistinctPathsThroughOneMessageType(t *testing.T) {
	source := `syntax = "proto3";
package account.v1;
import "nexa/protocol/v1/options.proto";
message Address { string city = 1; }
message GetAccountRequest { Address home = 1; Address work = 2; }
message GetAccountResponse { string name = 1; }
service AccountService {
  rpc Get(GetAccountRequest) returns (GetAccountResponse) {
    option (nexa.protocol.v1.http_proxy) = {
      operation_id: "account.get" method: GET path: "/addresses"
      auth: { mode: REQUIRED credentials: { id: "primary" type: BEARER location: HEADER name: "Authorization" } }
      permission: "account.read"
      request_fields: { http_field: "homeCity" rpc_field: "home.city" }
      request_fields: { http_field: "workCity" rpc_field: "work.city" }
      response_fields: { rpc_field: "name" http_field: "name" }
    };
  }
}`
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, source)}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	generated, err := composition.GeneratedAPI(document)
	if err != nil {
		t.Fatal(err)
	}
	operation, _ := generated.Operation("account.get")
	request, _ := generated.Type(operation.RequestType())
	if len(request.Fields()) != 2 {
		t.Fatalf("request fields = %#v", request.Fields())
	}
}

func TestBuildRejectsRenderedIdentifierCollision(t *testing.T) {
	source := `syntax = "proto3";
package account.v1;
import "nexa/protocol/v1/options.proto";
message Address { string city = 1; }
message GetAccountRequest { Address home = 1; string home_city = 2; }
message GetAccountResponse { string name = 1; }
service AccountService {
  rpc Get(GetAccountRequest) returns (GetAccountResponse) {
    option (nexa.protocol.v1.http_proxy) = {
      operation_id: "account.get" method: GET path: "/accounts"
      auth: { mode: REQUIRED credentials: { id: "primary" type: BEARER location: HEADER name: "Authorization" } }
      permission: "account.read"
      request_fields: { http_field: "homeCity" rpc_field: "home.city" }
      request_fields: { http_field: "home_city" rpc_field: "home_city" }
      response_fields: { rpc_field: "name" http_field: "name" }
    };
  }
}`
	_, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, source)}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	assertCompositionError(t, err, "generated_identifier_collision")
}

func parseCatalog(t *testing.T, selected bool) servicecatalog.Catalog {
	t.Helper()
	binding := " []"
	if selected {
		binding = fmt.Sprintf("\n      - id: %s\n        apiVersion: %s", composition.CapabilityID, composition.CapabilityVersion)
	}
	source := fmt.Sprintf("apiVersion: nexa.dev/service-catalog/v1\nkind: ServiceCatalog\nservices:\n  - id: core\n    root: backend/core\n    capabilityBindings: []\n  - id: account\n    root: backend/account\n    capabilityBindings:%s\n", binding)
	catalog, err := servicecatalog.Parse("project/services.yaml", []byte(source))
	if err != nil {
		t.Fatalf("Parse catalog: %v\n%s", err, source)
	}
	return catalog
}

func compileProtocol(t *testing.T, source string) protocol.Document {
	t.Helper()
	resolver := protocolResolver(func(_ context.Context, path string) (io.ReadCloser, error) {
		if path != "account/v1/account.proto" {
			return nil, os.ErrNotExist
		}
		return io.NopCloser(strings.NewReader(source)), nil
	})
	document, err := protocol.Compile(context.Background(), protocol.CompileOptions{ServiceID: "account", EntryFiles: []string{"account/v1/account.proto"}, Resolver: resolver})
	if err != nil {
		t.Fatalf("Compile protocol: %v", err)
	}
	return document
}

func validProtocolSource(withMap bool) string {
	responseField := "string name = 1;"
	responseBinding := `response_fields: { rpc_field: "name" http_field: "name" }`
	if withMap {
		responseField = "map<string, string> labels = 1;"
		responseBinding = ""
	}
	return fmt.Sprintf(`syntax = "proto3";
package account.v1;
import "nexa/protocol/v1/options.proto";
message GetAccountRequest { string id = 1; int64 tenant_id = 2; string request_id = 3; string trace_id = 4; }
message GetAccountResponse { %s }
service AccountService {
  rpc Get(GetAccountRequest) returns (GetAccountResponse) {
    option (nexa.protocol.v1.rpc_context) = {
      context_fields: { source: TENANT_ID rpc_field: "tenant_id" }
      context_fields: { source: REQUEST_ID rpc_field: "request_id" }
      context_fields: { source: TRACE_ID rpc_field: "trace_id" }
    };
    option (nexa.protocol.v1.http_proxy) = {
      operation_id: "account.get" method: GET path: "/accounts/{id}"
      auth: { mode: REQUIRED credentials: { id: "primary" type: BEARER location: HEADER name: "Authorization" } }
      permission: "account.read"
      request_fields: { http_field: "id" rpc_field: "id" }
      %s
      errors: { match: { domain: "account" code: "not_found" } project: { domain: "api" code: "account_not_found" http_status: 404 } }
    };
  }
}`, responseField, responseBinding)
}

func loadNative(t *testing.T, operationID, route string) httpapi.Document {
	t.Helper()
	root := t.TempDir()
	path := strings.ReplaceAll(route, "{id}", ":id")
	tag := "form:\"id,optional\""
	if strings.Contains(route, "{id}") {
		tag = "path:\"id\""
	}
	source := fmt.Sprintf("syntax = \"v1\"\ninfo (nexaContractVersion: \"nexa.dev/http-api/v1\")\ntype NativeRequest { ID string `%s` }\ntype NativeResponse { OK bool }\n@server (nexaOperationId: \"%s\" nexaAuthMode: \"none\")\nservice core-api { @handler native get %s (NativeRequest) returns (NativeResponse) }", tag, operationID, path)
	if err := os.WriteFile(filepath.Join(root, "core.api"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "core.api"})
	if err != nil {
		t.Fatalf("Load native: %v\n%s", err, source)
	}
	return document
}

func assertSources(t *testing.T, got []provenance.Source, want ...provenance.Source) {
	t.Helper()
	sort.Slice(got, func(i, j int) bool { return got[i].Ref.String() < got[j].Ref.String() })
	sort.Slice(want, func(i, j int) bool { return want[i].Ref.String() < want[j].Ref.String() })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %#v, want %#v", got, want)
	}
}

func assertCompositionError(t *testing.T, err error, reason string) {
	t.Helper()
	var typed *composition.Error
	if !errors.As(err, &typed) || typed.Reason() != reason {
		t.Fatalf("error = %T %v, want reason %q", err, err, reason)
	}
}

type protocolResolver func(context.Context, string) (io.ReadCloser, error)

func (f protocolResolver) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	return f(ctx, path)
}

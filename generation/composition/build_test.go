package composition_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/project/servicecatalog"
)

func TestBuildDerivesCanonicalAPIWithoutFieldMappings(t *testing.T) {
	document, err := composition.Build(testCatalog(t), []protocol.Document{testProtocol(t, canonicalProtocolSource())}, testNative(t), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := composition.GeneratedAPI(document)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := httpapi.RenderGenerated(generated)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := httpapi.ProjectionForRenderedGenerated(generated, "account.generated.api", rendered, document.FactGraph())
	if err != nil {
		t.Fatal(err)
	}
	renderRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(renderRoot, "account.generated.api"), rendered, 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: renderRoot, EntryFile: "account.generated.api", SourceProjection: &projection})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Operation("account.account.v1.accountService.get"); !ok {
		t.Fatal("generated API lost the complete projected RPC identity")
	}
	operation, ok := generated.Operation("account.account.v1.accountService.get")
	if !ok || operation.Method() != api.MethodGET || operation.Path() != "/accounts/{id}" || operation.Auth().Mode() != api.AuthRequired {
		t.Fatalf("operation = %#v, %v", operation, ok)
	}
	request, ok := generated.Type(operation.RequestType())
	if !ok || len(request.Fields()) != 5 {
		t.Fatalf("request fields = %#v, %v", request.Fields(), ok)
	}
	gotFields := map[string]bool{}
	for _, field := range request.Fields() {
		gotFields[field.Path()[0]] = true
	}
	for _, want := range []string{"id", "tenantId", "subjectId", "requestId", "traceId"} {
		if !gotFields[want] {
			t.Fatalf("request fields %#v do not contain %q", gotFields, want)
		}
	}
	response, ok := generated.Type(operation.ResponseType())
	if !ok || len(response.Fields()) != 3 {
		t.Fatalf("response = %#v, %v", response, ok)
	}
	if response.Fields()[2].ValueType().Name() != "uint64" {
		t.Fatalf("uint64 identity lost: %#v", response.Fields()[2].ValueType())
	}
}

func TestBuildDoesNotRejectMapBeforeFrontendClosure(t *testing.T) {
	mapSource := strings.Replace(canonicalProtocolSource(), "string display_name = 2;", "map<string,string> labels = 2;", 1)
	document, err := composition.Build(testCatalog(t), []protocol.Document{testProtocol(t, mapSource)}, testNative(t), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatalf("map rejected before frontend closure: %v", err)
	}
	generated, err := composition.GeneratedAPI(document)
	if err != nil {
		t.Fatalf("map rejected by canonical HTTP projection: %v", err)
	}
	if _, err := composition.FrontendClosure(generated); err == nil {
		t.Fatal("frontend closure accepted a reachable map")
	}
	if _, err := composition.FrontendClosure(testNative(t)); err != nil {
		t.Fatalf("unreferenced map blocked frontend closure: %v", err)
	}
}

func TestFrontendClosureKeepsRecursiveNamedDTO(t *testing.T) {
	root := t.TempDir()
	source := `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-convention/v1")
type Request {}
type RouteItem {
  Name string
  Children []RouteItem
}
type Response { Routes []RouteItem }
service core {
  // @nexa auth: "required"
  // @nexa permission: "nexa.menu.read"
  @handler GetAllMenus
  get /menu/all (Request) returns (Response)
}
`
	if err := os.WriteFile(filepath.Join(root, "core.api"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "core.api"})
	if err != nil {
		t.Fatal(err)
	}
	closure, err := composition.FrontendClosure(document)
	if err != nil {
		t.Fatal(err)
	}
	route, ok := closure.Type("RouteItem")
	if !ok {
		t.Fatal("RouteItem missing from frontend closure")
	}
	children, ok := route.Field("children")
	if !ok {
		t.Fatal("RouteItem.Children missing from frontend closure")
	}
	array, ok := children.ValueType().Element()
	if !ok || array.Kind() != api.ValueRef || array.Name() != "RouteItem" {
		t.Fatalf("RouteItem.Children = %#v, %v", array, ok)
	}
}

func TestBuildKeepsCompleteRPCIdentityForSameMethodNames(t *testing.T) {
	source := `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "proto3";
package account.v1;
message ListRequest { uint32 limit = 1; uint32 offset = 2; }
message ListResponse { repeated string items = 1; uint32 total = 2; }
service AccountService {
  // @nexa auth: "none"
  // @nexa http.method: "GET"
  // @nexa http.path: "/accounts"
  rpc List(ListRequest) returns (ListResponse);
}
service RoleService {
  // @nexa auth: "none"
  // @nexa http.method: "GET"
  // @nexa http.path: "/roles"
  rpc List(ListRequest) returns (ListResponse);
}
`
	document, err := composition.Build(testCatalog(t), []protocol.Document{testProtocol(t, source)}, testNative(t), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := composition.GeneratedAPI(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, operationID := range []string{"account.account.v1.accountService.list", "account.account.v1.roleService.list"} {
		if _, ok := generated.Operation(operationID); !ok {
			t.Fatalf("operation %q missing", operationID)
		}
	}
}

func canonicalProtocolSource() string {
	return `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "proto3";
package account.v1;
message GetAccountRequest { string id = 1; int64 tenant_id = 2; string subject_id = 3; string request_id = 4; string trace_id = 5; }
message GetAccountResponse { string id = 1; string display_name = 2; uint64 version = 3; }
service AccountService {
  // @nexa auth: "required"
  // @nexa permission: "account.read"
  // @nexa http.method: "GET"
  // @nexa http.path: "/accounts/{id}"
  rpc Get(GetAccountRequest) returns (GetAccountResponse);
}
`
}

func testCatalog(t *testing.T) servicecatalog.Catalog {
	t.Helper()
	source := fmt.Sprintf("apiVersion: nexa.dev/service-catalog/v1\nkind: ServiceCatalog\nservices:\n  - id: core\n    root: backend/core\n    capabilityBindings: []\n  - id: account\n    root: backend/account\n    capabilityBindings:\n      - id: %s\n        apiVersion: %s\n", composition.CapabilityID, composition.CapabilityVersion)
	result, err := servicecatalog.Parse("project/services.yaml", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func testProtocol(t *testing.T, source string) protocol.Document {
	t.Helper()
	result, err := protocol.Compile(context.Background(), protocol.CompileOptions{ServiceID: "account", EntryFiles: []string{"account/v1/account.proto"}, Resolver: protocolResolver(func(_ context.Context, path string) (io.ReadCloser, error) {
		if path != "account/v1/account.proto" {
			return nil, os.ErrNotExist
		}
		return io.NopCloser(strings.NewReader(source)), nil
	})})
	if err != nil {
		t.Fatalf("Compile: %v\n%s", err, source)
	}
	return result
}
func testNative(t *testing.T) httpapi.Document {
	t.Helper()
	root := t.TempDir()
	source := `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-convention/v1")
type HealthRequest {}
type HealthResponse { Ready bool }
type UnusedMetadata { Labels map[string]string }
service core {
  // @nexa auth: "none"
  @handler Health
  get /health (HealthRequest) returns (HealthResponse)
}
`
	if err := os.WriteFile(filepath.Join(root, "core.api"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "core.api"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type protocolResolver func(context.Context, string) (io.ReadCloser, error)

func (f protocolResolver) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	return f(ctx, path)
}

package httpapi_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/httpconvention"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
)

func TestLoadBuildsTypedFactsForNativeAPITypeAndFields(t *testing.T) {
	root := t.TempDir()
	writeHTTPAPI(t, root, `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-convention/v1")
// @nexa crud.operations: ["list", "get", "create", "update", "delete"]
// @nexa scope: "global"
type Item {
  // @nexa label.zh-CN: "名称"
  // @nexa crud.read: "include"
  // @nexa crud.mutation: "create-update"
  Name string
}
type Request {}
type Response { Item Item }
service sample {
  // @nexa auth: "none"
  @handler Get
  get /items (Request) returns (Response)
}
`)
	document, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "sample.api"})
	if err != nil {
		t.Fatal(err)
	}
	facts := document.FactGraph()
	for id, want := range map[sourcecomment.FactID]string{
		{SemanticID: "Item", Key: "scope"}:            "global",
		{SemanticID: "Item.name", Key: "label.zh-CN"}: "名称",
		{SemanticID: "Item.name", Key: "crud.read"}:   "include",
	} {
		fact, ok := facts.Fact(id)
		if !ok {
			t.Fatalf("fact %s missing", id.String())
		}
		value, ok := fact.Value().String()
		if !ok || value != want {
			t.Fatalf("fact %s = %q, %v", id.String(), value, ok)
		}
		if fact.FirstSource().Stage() != sourcecomment.StageAPI {
			t.Fatalf("fact %s source = %s", id.String(), fact.FirstSource().String())
		}
	}
	crud, ok, err := facts.CRUD("Item")
	operations := crud.Operations()
	if err != nil || !ok || len(operations) != 5 || operations[4] != sourcecomment.CRUDDelete {
		t.Fatalf("CRUD = %#v, %v, %v", crud, ok, err)
	}
	item, ok := document.Type("Item")
	if !ok || item.SemanticID() != "Item" {
		t.Fatalf("item semantic ID = %q, %v", item.SemanticID(), ok)
	}
	name, ok := item.Field("name")
	if !ok || name.SemanticID() != "Item.name" {
		t.Fatalf("field semantic ID = %q, %v", name.SemanticID(), ok)
	}
	if location, declared := name.Transport(); declared || location != httpconvention.Location("") {
		t.Fatalf("field transport = %q, %v", location, declared)
	}
}

func TestLoadBindsRouteFactsAcrossDocDirective(t *testing.T) {
	root := t.TempDir()
	writeHTTPAPI(t, root, `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-convention/v1")
type Request {}
type Response { Value string }
service sample {
  // @nexa label.en-US: "Read sample"
  @doc "Read sample"
  // @nexa auth: "none"
  @handler Get
  get /samples (Request) returns (Response)
}
`)
	document, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "sample.api"})
	if err != nil {
		t.Fatal(err)
	}
	operation, ok := document.Operation("get")
	if !ok {
		t.Fatal("route operation missing")
	}
	fact, ok := document.FactGraph().Fact(sourcecomment.FactID{SemanticID: operation.ID(), Key: "label.en-US"})
	if !ok {
		t.Fatal("route label fact missing")
	}
	if value, ok := fact.Value().String(); !ok || value != "Read sample" {
		t.Fatalf("route label = %q, %v", value, ok)
	}
}

func TestLoadAcceptsCanonicalAuthoringWithoutTransportMetadata(t *testing.T) {
	root := t.TempDir()
	source := `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-convention/v1")
type ListRequest {
  Limit int64
  Offset int64
}
type Item {
  ID string
  DisplayName string
}
type ListResponse {
  Items []Item
  Total int32
}
service sample {
  // @nexa auth: "required"
  // @nexa permission: "sample.read"
  @handler List
  get /samples (ListRequest) returns (ListResponse)
}
`
	writeHTTPAPI(t, root, source)
	document, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "sample.api"})
	if err != nil {
		t.Fatal(err)
	}
	if err := httpapi.ValidateConvention(document); err != nil {
		t.Fatal(err)
	}
	operation, ok := document.Operation("list")
	if !ok || operation.Method() != api.MethodGET || operation.Auth().Mode() != api.AuthRequired || operation.ResponseType() != "ListResponse" {
		t.Fatalf("operation = %#v, %v", operation, ok)
	}
	item, ok := document.Type("Item")
	if !ok || len(item.Fields()) != 2 {
		t.Fatalf("item = %#v, %v", item, ok)
	}
}

func TestLoadAcceptsPDCLTransportTagsAndRejectsAliases(t *testing.T) {
	for name, mutation := range map[string]string{
		"accepted transport tag":   "type Request { ID string `path:\"id\"` }\nservice sample {\n// @nexa auth: \"required\"\n// @nexa permission: \"sample.read\"\n@handler Get\nget /samples/:id (Request) returns (Request)\n}",
		"accepted lower snake tag": "type Request { TenantID string `form:\"tenant_id\"` }\nservice sample {\n// @nexa auth: \"required\"\n// @nexa permission: \"sample.read\"\n@handler List\nget /samples (Request) returns (Request)\n}",
		"transport alias":          "type Request { ID string `json:\"alias\"` }",
		"legacy Nexa metadata":     "type Request { ID string }\n@server (nexaOperationId: \"sample.get\" nexaAuthMode: \"required\")\nservice sample { @handler Get get /samples/:id (Request) returns (Request) }",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeHTTPAPI(t, root, "// @nexa $contract: \"nexa.dev/source-comment/v1\"\nsyntax = \"v1\"\ninfo (nexaContractVersion: \"nexa.dev/http-convention/v1\")\n"+mutation)
			document, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "sample.api"})
			if name == "accepted transport tag" || name == "accepted lower snake tag" {
				if err != nil || httpapi.ValidateConvention(document) != nil {
					t.Fatalf("PDCL transport tag rejected: %v", err)
				}
				if name == "accepted lower snake tag" {
					request, ok := document.Type("Request")
					if !ok || len(request.Fields()) != 1 || request.Fields()[0].Path()[0] != "tenant_id" {
						t.Fatalf("lower_snake identity = %#v, %v", request, ok)
					}
					if location, declared := request.Fields()[0].Transport(); !declared || location != httpconvention.LocationQuery {
						t.Fatalf("lower_snake transport = %q, %v", location, declared)
					}
				}
				return
			}
			if err == nil {
				t.Fatal("non-convention authoring accepted")
			}
		})
	}
}

func TestLoadAcceptsPatchInConventionGate(t *testing.T) {
	root := t.TempDir()
	writeHTTPAPI(t, root, `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-convention/v1")
type Request {
  ID string
  Name string
}
type Response {
  ID string
  Name string
}
service sample {
  // @nexa auth: "required"
  // @nexa permission: "sample.write"
  @handler Patch
  patch /samples/:id (Request) returns (Response)
}
`)
	document, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "sample.api"})
	if err != nil {
		t.Fatal(err)
	}
	if err := httpapi.ValidateConvention(document); err != nil {
		t.Fatalf("PATCH convention error = %v", err)
	}
}

func TestLoadAcceptsRecursiveNamedDTO(t *testing.T) {
	root := t.TempDir()
	writeHTTPAPI(t, root, `// @nexa $contract: "nexa.dev/source-comment/v1"
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
`)
	document, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "sample.api"})
	if err != nil {
		t.Fatal(err)
	}
	if err := httpapi.ValidateConvention(document); err != nil {
		t.Fatal(err)
	}
	route, ok := document.Type("RouteItem")
	if !ok {
		t.Fatal("RouteItem missing")
	}
	children, ok := route.Field("children")
	if !ok {
		t.Fatal("RouteItem.Children missing")
	}
	element, ok := children.ValueType().Element()
	if !ok || element.Kind() != httpapi.ValueRef || element.Name() != "RouteItem" {
		t.Fatalf("RouteItem.Children = %#v, %v", element, ok)
	}
}

func TestLoadDerivesGoZeroGroupHandlerIdentityAndRejectsCanonicalCollision(t *testing.T) {
	root := t.TempDir()
	writeHTTPAPI(t, root, `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-convention/v1")
type Request {}
type Response { OK bool }
@server (group: asset)
service api {
  // @nexa auth: "none"
  @handler List
  get /assets (Request) returns (Response)
}
@server (group: role)
service api {
  // @nexa auth: "none"
  @handler List
  get /roles (Request) returns (Response)
}
`)
	document, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "sample.api"})
	if err != nil {
		t.Fatal(err)
	}
	for _, operationID := range []string{"asset.list", "role.list"} {
		if _, ok := document.Operation(operationID); !ok {
			t.Fatalf("operation %q missing", operationID)
		}
	}

	writeHTTPAPI(t, root, `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-convention/v1")
type Request {}
type Response { OK bool }
@server (group: foo_bar)
service api {
  // @nexa auth: "none"
  @handler List
  get /one (Request) returns (Response)
}
@server (group: fooBar)
service api {
  // @nexa auth: "none"
  @handler List
  get /two (Request) returns (Response)
}
`)
	if _, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "sample.api"}); err == nil {
		t.Fatal("canonical group/handler collision accepted")
	}
}

func writeHTTPAPI(t *testing.T, root, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "sample.api"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

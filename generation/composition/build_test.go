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
	responseBinding, ok := response.Fields()[0].Binding()
	if !ok || responseBinding.Location() != api.RequestBindingBody || responseBinding.Name() != "name" {
		t.Fatalf("generated response binding = %#v, %v", responseBinding, ok)
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
	requestBindings := projected.RequestBindings()
	if len(requestBindings) != 1 || requestBindings[0].Field() != "Id" || requestBindings[0].Location() != api.RequestBindingPath || requestBindings[0].Name() != "id" {
		t.Fatalf("manifest request bindings = %#v", requestBindings)
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

func TestBuildProjectsAcyclicNamedObjectAndCollectionClosure(t *testing.T) {
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, objectProtocolSource())}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	generated, err := composition.GeneratedAPI(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AccountAccountV1Member", "AccountAccountV1Settings"} {
		if _, ok := generated.Type(name); !ok {
			t.Fatalf("projected type %q missing", name)
		}
	}
	counts := map[string]int{}
	for _, value := range generated.Types() {
		counts[value.Name()]++
	}
	if counts["AccountAccountV1Member"] != 1 || counts["AccountAccountV1Settings"] != 1 {
		t.Fatalf("projected identity counts = %#v", counts)
	}
	operation, ok := generated.Operation("account.replace")
	if !ok {
		t.Fatal("operation missing")
	}
	request, _ := generated.Type(operation.RequestType())
	roles, ok := request.Field("RoleCodes")
	if !ok || !roles.Required() || roles.ValueType().Kind() != httpapi.ValueArray {
		t.Fatalf("required roleCodes = %#v, %v", roles, ok)
	}
	settings, ok := request.Field("Settings")
	if !ok || !settings.Required() || settings.ValueType().Kind() != httpapi.ValueRef || settings.ValueType().Name() != "AccountAccountV1Settings" {
		t.Fatalf("required settings = %#v, %v", settings, ok)
	}
	response, _ := generated.Type(operation.ResponseType())
	items, ok := response.Field("Items")
	itemType, itemOK := items.ValueType().Element()
	if !ok || !items.Required() || items.ValueType().Kind() != httpapi.ValueArray || !itemOK || itemType.Kind() != httpapi.ValueRef || itemType.Name() != "AccountAccountV1Member" {
		t.Fatalf("items = %#v, %v", items, ok)
	}
	member, _ := generated.Type("AccountAccountV1Member")
	roleCodes, ok := member.Field("RoleCodes")
	roleCodeType, roleCodeOK := roleCodes.ValueType().Element()
	if !ok || roleCodes.ValueType().Kind() != httpapi.ValueArray || !roleCodeOK || roleCodeType.Name() != "string" {
		t.Fatalf("member roleCodes = %#v, %v", roleCodes, ok)
	}
	memberSettings, ok := member.Field("Settings")
	settingsType, settingsOK := memberSettings.ValueType().Element()
	if !ok || memberSettings.ValueType().Kind() != httpapi.ValueOptional || !settingsOK || settingsType.Kind() != httpapi.ValueRef || settingsType.Name() != "AccountAccountV1Settings" {
		t.Fatalf("member settings = %#v, %v", memberSettings, ok)
	}
	for _, tc := range []struct {
		field, wire string
	}{{"Id", "id"}, {"RoleCodes", "roleCodes"}, {"Settings", "settings"}} {
		field, ok := member.Field(tc.field)
		binding, bound := field.Binding()
		if !ok || !bound || binding.Location() != api.RequestBindingBody || binding.Name() != tc.wire {
			t.Fatalf("projected field %s binding = %#v, field=%v bound=%v", tc.field, binding, ok, bound)
		}
	}
}

func TestGeneratedAPIKeepsExactResponseWireNames(t *testing.T) {
	source := strings.Replace(validProtocolSource(false), "message GetAccountResponse { string name = 1; }", "message GetAccountResponse { string id = 1; string api_version = 2; }", 1)
	source = strings.Replace(source, `response_fields: { rpc_field: "name" http_field: "name" }`, `response_fields: { rpc_field: "id" http_field: "id" }
      response_fields: { rpc_field: "api_version" http_field: "apiVersion" }`, 1)
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, source)}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := composition.GeneratedAPI(document)
	if err != nil {
		t.Fatal(err)
	}
	operation, _ := generated.Operation("account.get")
	response, _ := generated.Type(operation.ResponseType())
	for _, tc := range []struct {
		path, wire string
	}{{"Id", "id"}, {"ApiVersion", "apiVersion"}} {
		field, ok := response.Field(tc.path)
		binding, bound := field.Binding()
		if !ok || !bound || binding.Location() != api.RequestBindingBody || binding.Name() != tc.wire {
			t.Fatalf("response field %s binding = %#v, field=%v bound=%v", tc.path, binding, ok, bound)
		}
	}
}

func TestBuildRejectsRecursiveProjectedMessageGraph(t *testing.T) {
	source := strings.Replace(objectProtocolSource(), "message Settings { string locale = 1; }", "message Settings { string locale = 1; Settings child = 2; }", 1)
	_, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, source)}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	assertCompositionError(t, err, "message_graph_recursive")
}

func TestBuildRejectsProjectedTypeCollisionWithOperationType(t *testing.T) {
	source := strings.ReplaceAll(objectProtocolSource(), "Member", "MemberRequest")
	source = strings.Replace(source, `operation_id: "account.replace"`, `operation_id: "account.account-v1.member"`, 1)
	_, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, source)}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	assertCompositionError(t, err, "native_type_collision")
}

func TestBuildRejectsAncestorDescendantProjectedNameCollision(t *testing.T) {
	source := `syntax = "proto3";
package account.v1;
import "nexa/protocol/v1/options.proto";
message AB { string value = 1; }
message A_B { AB child = 1; }
message ReplaceRequest { A_B item = 1; }
message ReplaceResponse { string result = 1; }
service AccountService {
  rpc Replace(ReplaceRequest) returns (ReplaceResponse) {
    option (nexa.protocol.v1.http_proxy) = {
      operation_id: "account.replace" method: POST path: "/accounts/replace"
      auth: { mode: NONE }
      request_fields: { http_field: "item" rpc_field: "item" }
      response_fields: { rpc_field: "result" http_field: "result" }
    };
  }
}`
	_, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, source)}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	assertCompositionError(t, err, "projected_type_collision")
}

func TestBuildRejectsProjectedTypeCollisionWithNativeType(t *testing.T) {
	root := t.TempDir()
	nativeSource := `syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-api/v1")
type AccountAccountV1Settings { Locale string }
type HealthRequest {}
type HealthResponse { OK bool }
@server (nexaOperationId: "health.get" nexaAuthMode: "none")
service core-api { @handler health get /health (HealthRequest) returns (HealthResponse) }`
	if err := os.WriteFile(filepath.Join(root, "core.api"), []byte(nativeSource), 0o600); err != nil {
		t.Fatal(err)
	}
	native, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "core.api"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, objectProtocolSource())}, native, composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	assertCompositionError(t, err, "projected_type_collision")
}

func TestBuildIsolatesSameProtoIdentityAcrossServices(t *testing.T) {
	source := func(serviceName, operationID, route string) string {
		return fmt.Sprintf(`syntax = "proto3";
package shared.v1;
import "nexa/protocol/v1/options.proto";
message Payload { repeated string values = 1; }
message ReplaceRequest { Payload payload = 1; }
message ReplaceResponse { Payload payload = 1; }
service %s {
  rpc Replace(ReplaceRequest) returns (ReplaceResponse) {
    option (nexa.protocol.v1.http_proxy) = {
      operation_id: %q method: POST path: %q
      auth: { mode: NONE }
      request_fields: { http_field: "payload" rpc_field: "payload" }
      response_fields: { rpc_field: "payload" http_field: "payload" }
    };
  }
}`, serviceName, operationID, route)
	}
	account := compileProtocolForService(t, "account", source("AccountService", "account.replace", "/accounts/replace"))
	billing := compileProtocolForService(t, "billing", source("BillingService", "billing.replace", "/billing/replace"))
	document, err := composition.Build(objectCatalog(t), []protocol.Document{account, billing}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := composition.GeneratedAPI(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AccountSharedV1Payload", "BillingSharedV1Payload"} {
		if _, ok := generated.Type(name); !ok {
			t.Fatalf("isolated projected type %q missing", name)
		}
	}
}

func objectProtocolSource() string {
	return `syntax = "proto3";
package account.v1;
import "nexa/protocol/v1/options.proto";
message Settings { string locale = 1; }
message Member { string id = 1; repeated string role_codes = 2; Settings settings = 3; }
message ReplaceRequest { repeated string role_codes = 1; Settings settings = 2; repeated Member items = 3; }
message ReplaceResponse { int64 total = 1; repeated Member items = 2; }
service AccountService {
  rpc Replace(ReplaceRequest) returns (ReplaceResponse) {
    option (nexa.protocol.v1.http_proxy) = {
      operation_id: "account.replace" method: POST path: "/accounts/replace"
      auth: { mode: REQUIRED credentials: { id: "primary" type: BEARER location: HEADER name: "Authorization" } }
      permission: "account.replace"
      request_fields: { http_field: "roleCodes" rpc_field: "role_codes" }
      request_fields: { http_field: "settings" rpc_field: "settings" }
      request_fields: { http_field: "items" rpc_field: "items" }
      response_fields: { rpc_field: "total" http_field: "total" }
      response_fields: { rpc_field: "items" http_field: "items" }
    };
  }
}`
}

func TestBuildAcceptsStringTenant(t *testing.T) {
	stringTenant := strings.Replace(validProtocolSource(false), "int64 tenant_id = 2;", "string tenant_id = 2;", 1)
	if _, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, stringTenant)}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"}); err != nil {
		t.Fatalf("Build() string tenant error = %v", err)
	}
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

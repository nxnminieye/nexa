package core_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"entgo.io/ent"

	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	core "github.com/nxnminieye/nexa/plugins/service/core"
	coreschema "github.com/nxnminieye/nexa/plugins/service/core/_bundle/backend/core/ent/schema"
	"github.com/nxnminieye/nexa/project/servicecatalog"
	"github.com/nxnminieye/nexa/sourceplugin"
)

func TestProfileClosuresAreIndependent(t *testing.T) {
	provider, err := core.New()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		profile      string
		wantProfiles []string
		present      []string
		absent       []string
	}{
		{profile: "backend", wantProfiles: []string{"backend"}, present: []string{"backend/core/coreapp/health.go", "backend/core/desc/core.proto"}, absent: []string{"backend/core/coreapp/oidc_adapter.go", "frontend/frontend/core/pages/accounts.page.json"}},
		{profile: "identity-oidc", wantProfiles: []string{"backend", "identity-oidc"}, present: []string{"backend/core/coreapp/health.go", "backend/core/coreapp/oidc_adapter.go"}, absent: []string{"frontend/frontend/core/pages/accounts.page.json"}},
	}
	for _, test := range tests {
		t.Run(test.profile, func(t *testing.T) {
			closure, err := provider.Manifest().ResolveProfile(test.profile)
			if err != nil {
				t.Fatal(err)
			}
			if !sameStrings(closure.ProfileIDs(), test.wantProfiles) {
				t.Fatalf("profile IDs = %#v", closure.ProfileIDs())
			}
			paths := closurePaths(closure)
			for _, path := range test.present {
				if !paths[path] {
					t.Fatalf("%s missing", path)
				}
			}
			for _, path := range test.absent {
				if paths[path] {
					t.Fatalf("%s unexpectedly present", path)
				}
			}
		})
	}
	for _, removed := range []string{"frontend", "full"} {
		if _, ok := provider.Manifest().LookupProfile(removed); ok {
			t.Fatalf("consumer-owned %q profile remains public", removed)
		}
	}
}

func TestProfileBackendNativeFactsLoadSemantically(t *testing.T) {
	models := []interface{ Fields() []ent.Field }{coreschema.Tenant{}, coreschema.IdentityAccount{}, coreschema.TenantMember{}, coreschema.Role{}, coreschema.TenantMemberRoleGrant{}, coreschema.Permission{}, coreschema.AuthSession{}}
	for _, model := range models {
		for _, value := range model.Fields() {
			descriptor := value.Descriptor()
			if descriptor.Err != nil {
				t.Fatalf("%T.%s: %v", model, descriptor.Name, descriptor.Err)
			}
		}
	}
	rolePermissions := coreschema.Role{}.Edges()[2].Descriptor()
	if rolePermissions.StorageKey == nil || rolePermissions.StorageKey.Table != "role_permissions" {
		t.Fatalf("legacy permission relation storage = %#v", rolePermissions.StorageKey)
	}
	physicalEdges := []struct {
		name, field string
		edge        ent.Edge
	}{
		{"tenant", "tenant_id", coreschema.TenantMember{}.Edges()[0]},
		{"identity_account", "identity_account_id", coreschema.TenantMember{}.Edges()[1]},
		{"tenant", "tenant_id", coreschema.Role{}.Edges()[0]},
		{"tenant", "tenant_id", coreschema.TenantMemberRoleGrant{}.Edges()[0]},
		{"member", "tenant_member_id", coreschema.TenantMemberRoleGrant{}.Edges()[1]},
		{"role", "role_id", coreschema.TenantMemberRoleGrant{}.Edges()[2]},
		{"tenant", "tenant_id", coreschema.AuthSession{}.Edges()[0]},
		{"identity_account", "identity_account_id", coreschema.AuthSession{}.Edges()[1]},
	}
	for _, expected := range physicalEdges {
		descriptor := expected.edge.Descriptor()
		if descriptor.Name != expected.name || descriptor.Field != expected.field || !descriptor.Unique || !descriptor.Required {
			t.Fatalf("physical edge = %#v, want name=%s field=%s unique required", descriptor, expected.name, expected.field)
		}
	}
	accessHashUnique := false
	for _, value := range (coreschema.AuthSession{}).Indexes() {
		descriptor := value.Descriptor()
		if descriptor.Unique && sameStrings(descriptor.Fields, []string{"access_token_hash"}) {
			accessHashUnique = true
		}
	}
	if !accessHashUnique {
		t.Fatal("AuthSession access_token_hash unique index missing")
	}

	provider, err := core.New()
	if err != nil {
		t.Fatal(err)
	}
	resolver := &treeResolver{tree: provider.Tree()}
	proto, err := protocol.Compile(context.Background(), protocol.CompileOptions{
		ServiceID: "core", EntryFiles: []string{"backend/core/desc/core.proto"}, Resolver: resolver,
	})
	if err != nil {
		if typed, ok := err.(*protocol.Error); ok {
			t.Fatalf("compile Proto: reason=%s pointer=%s: %v", typed.Reason(), typed.Pointer(), err)
		}
		t.Fatalf("compile Proto: %v", err)
	}
	repository := t.TempDir()
	apiFile, _ := provider.Tree().Lookup("backend/core/desc/core.api")
	path := filepath.Join(repository, "desc", "core.api")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, apiFile.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	api, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: repository, EntryFile: "desc/core.api"})
	if err != nil {
		t.Fatalf("load API: %v", err)
	}
	if _, ok := api.Operation("listProviders"); !ok {
		t.Fatal("provider discovery operation missing")
	}
	service, ok := proto.Service("core.v1.CoreService")
	if !ok {
		t.Fatal("CoreService missing")
	}
	if len(service.Methods()) != 35 {
		t.Fatalf("CoreService methods = %d, want 35", len(service.Methods()))
	}
	exported := map[string]map[string]string{
		"core.core.v1.coreService.health":                   {"http.method": "GET", "http.path": "/health", "auth": "none"},
		"core.core.v1.coreService.register":                 {"http.method": "POST", "http.path": "/auth/register", "auth": "none"},
		"core.core.v1.coreService.login":                    {"http.method": "POST", "http.path": "/auth/login", "auth": "none"},
		"core.core.v1.coreService.refresh":                  {"http.method": "POST", "http.path": "/auth/refresh", "auth": "none"},
		"core.core.v1.coreService.checkPermission":          {"http.method": "GET", "http.path": "/auth/permissions/{permission}", "auth": "required", "permission": "core.authorization.check"},
		"core.core.v1.coreService.listIdentityAccounts":     {"http.method": "GET", "http.path": "/identity-accounts", "auth": "required", "permission": "nexa.identity.account.read"},
		"core.core.v1.coreService.getIdentityAccount":       {"http.method": "GET", "http.path": "/identity-accounts/{accountId}", "auth": "required", "permission": "nexa.identity.account.read"},
		"core.core.v1.coreService.updateAccountStatus":      {"http.method": "PUT", "http.path": "/identity-accounts/{accountId}/status", "auth": "required", "permission": "nexa.identity.account.status.update"},
		"core.core.v1.coreService.resetAccountPassword":     {"http.method": "POST", "http.path": "/identity-accounts/{accountId}/password/reset", "auth": "required", "permission": "nexa.identity.account.password.reset"},
		"core.core.v1.coreService.listTenantMembers":        {"http.method": "GET", "http.path": "/users", "auth": "required", "permission": "nexa.user.read"},
		"core.core.v1.coreService.getTenantMember":          {"http.method": "GET", "http.path": "/users/{memberId}", "auth": "required", "permission": "nexa.user.read"},
		"core.core.v1.coreService.updateTenantMemberStatus": {"http.method": "PUT", "http.path": "/users/{memberId}/status", "auth": "required", "permission": "nexa.user.status.update"},
		"core.core.v1.coreService.replaceTenantMemberRoles": {"http.method": "PUT", "http.path": "/users/{memberId}/roles", "auth": "required", "permission": "nexa.user.roles.update"},
		"core.core.v1.coreService.provisionTenant":          {"http.method": "POST", "http.path": "/tenants", "auth": "required", "permission": "nexa.tenant.create"},
		"core.core.v1.coreService.listTenants":              {"http.method": "GET", "http.path": "/tenants", "auth": "required", "permission": "nexa.tenant.read"},
		"core.core.v1.coreService.getTenant":                {"http.method": "GET", "http.path": "/tenants/{tenantId}", "auth": "required", "permission": "nexa.tenant.read"},
		"core.core.v1.coreService.updateTenant":             {"http.method": "PUT", "http.path": "/tenants/{tenantId}", "auth": "required", "permission": "nexa.tenant.update"},
		"core.core.v1.coreService.updateTenantStatus":       {"http.method": "PUT", "http.path": "/tenants/{tenantId}/status", "auth": "required", "permission": "nexa.tenant.update"},
		"core.core.v1.coreService.listRoles":                {"http.method": "GET", "http.path": "/roles", "auth": "required", "permission": "nexa.auth.roles.read"},
		"core.core.v1.coreService.getRole":                  {"http.method": "GET", "http.path": "/roles/{roleId}", "auth": "required", "permission": "nexa.auth.roles.read"},
		"core.core.v1.coreService.createRole":               {"http.method": "POST", "http.path": "/roles", "auth": "required", "permission": "nexa.auth.roles.create"},
		"core.core.v1.coreService.updateRole":               {"http.method": "PUT", "http.path": "/roles/{roleId}", "auth": "required", "permission": "nexa.auth.roles.update"},
		"core.core.v1.coreService.updateRoleStatus":         {"http.method": "PUT", "http.path": "/roles/{roleId}/status", "auth": "required", "permission": "nexa.auth.roles.update"},
		"core.core.v1.coreService.replaceRolePermissions":   {"http.method": "PUT", "http.path": "/roles/{roleId}/permissions", "auth": "required", "permission": "nexa.auth.role_permissions.bind"},
		"core.core.v1.coreService.replaceRoleMenus":         {"http.method": "PUT", "http.path": "/roles/{roleId}/menus", "auth": "required", "permission": "nexa.auth.roles.update"},
		"core.core.v1.coreService.listMenus":                {"http.method": "GET", "http.path": "/menus", "auth": "required", "permission": "nexa.menu.read"},
		"core.core.v1.coreService.getMenu":                  {"http.method": "GET", "http.path": "/menus/{code}", "auth": "required", "permission": "nexa.menu.read"},
		"core.core.v1.coreService.listPermissions":          {"http.method": "GET", "http.path": "/permissions", "auth": "required", "permission": "nexa.auth.permissions.read"},
		"core.core.v1.coreService.getPermission":            {"http.method": "GET", "http.path": "/permissions/{code}", "auth": "required", "permission": "nexa.auth.permissions.read"},
	}
	graph := proto.FactGraph()
	for methodName, contract := range exported {
		for key, want := range contract {
			fact, exists := graph.Fact(sourcecomment.FactID{SemanticID: methodName, Key: key})
			got, stringValue := fact.Value().String()
			if !exists || !stringValue || got != want {
				t.Fatalf("Core fact %s:%s = %q, %v; want %q", methodName, key, got, exists, want)
			}
		}
	}
	for _, methodName := range []string{
		"core.v1.CoreService.CurrentSession", "core.v1.CoreService.Revoke", "core.v1.CoreService.Logout",
		"core.v1.CoreService.GetAccessCodes", "core.v1.CoreService.GetUserInfo", "core.v1.CoreService.GetAllMenus",
	} {
		if _, ok := proto.Method(methodName); !ok {
			t.Fatalf("RPC method %q missing", methodName)
		}
		operationID, err := sourcecomment.CanonicalRPCOperationID(proto.ServiceID(), methodName)
		if err != nil {
			t.Fatal(err)
		}
		if _, selected := graph.Fact(sourcecomment.FactID{SemanticID: operationID, Key: "http.method"}); selected {
			t.Fatalf("removed HTTP contract %q remains exported", methodName)
		}
	}
	catalogSource := fmt.Sprintf(`apiVersion: nexa.dev/service-catalog/v1
kind: ServiceCatalog
services:
  - id: core
    root: backend/core
    capabilityBindings:
      - id: %s
        apiVersion: %s
`, composition.CapabilityID, composition.CapabilityVersion)
	catalog, err := servicecatalog.Parse("project/services.yaml", []byte(catalogSource))
	if err != nil {
		t.Fatal(err)
	}
	composed, err := composition.Build(catalog, []protocol.Document{proto}, api, composition.BuildOptions{
		CoreServiceID: "core", ConsumerModulePath: "example.com/consumer",
	})
	if err != nil {
		t.Fatalf("compose native and generated Core API: %v", err)
	}
	generated, err := composition.GeneratedAPI(composed)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := httpapi.Merge(api, generated)
	if err != nil || len(merged.Operations()) != 34 {
		t.Fatalf("merged Core operations = %d, %v", len(merged.Operations()), err)
	}
	login, ok := generated.Operation("core.core.v1.coreService.login")
	if !ok {
		t.Fatal("canonical Core login operation missing")
	}
	assertCoreFields(t, generated, login.RequestType(), []string{"password", "username"})
	assertCoreFields(t, generated, login.ResponseType(), []string{"accessToken", "memberId", "refreshToken", "tenantId"})
	refresh, ok := generated.Operation("core.core.v1.coreService.refresh")
	if !ok {
		t.Fatal("canonical Core refresh operation missing")
	}
	assertCoreFields(t, generated, refresh.RequestType(), []string{"refreshToken"})
	assertCoreFields(t, generated, refresh.ResponseType(), []string{"accessToken", "memberId", "refreshToken", "tenantId"})
	menuType, ok := api.Type("RouteItem")
	if !ok {
		t.Fatal("native RouteItem type missing")
	}
	children, ok := menuType.Field("children")
	if !ok {
		t.Fatal("recursive menu children missing")
	}
	childArray, ok := children.ValueType().Element()
	if !ok || childArray.Name() != menuType.Name() {
		t.Fatalf("recursive children = %#v, %v; want ref %q", childArray, ok, menuType.Name())
	}
}

func assertCoreFields(t *testing.T, document httpapi.Document, typeName string, want []string) {
	t.Helper()
	value, ok := document.Type(typeName)
	if !ok {
		t.Fatalf("type %q missing", typeName)
	}
	got := make([]string, 0, len(value.Fields()))
	for _, field := range value.Fields() {
		got = append(got, field.Path()[0])
	}
	if !sameStrings(got, want) {
		t.Fatalf("type %q fields = %#v, want %#v", typeName, got, want)
	}
}

func mergedCoreAPI(t *testing.T, provider sourceplugin.Provider) httpapi.Document {
	t.Helper()
	proto, err := protocol.Compile(context.Background(), protocol.CompileOptions{
		ServiceID: "core", EntryFiles: []string{"backend/core/desc/core.proto"}, Resolver: &treeResolver{tree: provider.Tree()},
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := t.TempDir()
	apiFile, _ := provider.Tree().Lookup("backend/core/desc/core.api")
	path := filepath.Join(repository, "desc", "core.api")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, apiFile.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	native, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: repository, EntryFile: "desc/core.api"})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := servicecatalog.Parse("project/services.yaml", []byte(fmt.Sprintf(`apiVersion: nexa.dev/service-catalog/v1
kind: ServiceCatalog
services:
  - id: core
    root: backend/core
    capabilityBindings:
      - id: %s
        apiVersion: %s
`, composition.CapabilityID, composition.CapabilityVersion)))
	if err != nil {
		t.Fatal(err)
	}
	composed, err := composition.Build(catalog, []protocol.Document{proto}, native, composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := composition.GeneratedAPI(composed)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := httpapi.Merge(native, generated)
	if err != nil {
		t.Fatal(err)
	}
	return merged
}

func closurePaths(closure sourceplugin.ProfileClosure) map[string]bool {
	result := make(map[string]bool)
	for _, file := range closure.Files() {
		result[file.Path()] = true
	}
	return result
}

func profilePaths(closure sourceplugin.ProfileClosure) []string {
	result := make([]string, 0, len(closure.Files()))
	for _, file := range closure.Files() {
		result = append(result, file.Path())
	}
	return result
}

type treeResolver struct{ tree sourceplugin.Tree }

func (r *treeResolver) Open(_ context.Context, path string) (io.ReadCloser, error) {
	file, ok := r.tree.Lookup(path)
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(file.Bytes())), nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"entgo.io/ent"
	entschema "entgo.io/ent/schema"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/nexaent"
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
		{profile: "backend", wantProfiles: []string{"backend"}, present: []string{"backend/core/coreapp/health.go", "backend/core/desc/core.proto"}, absent: []string{"backend/core/coreapp/oidc_adapter.go", "frontend/frontend/core/pages/identity-accounts.page.json"}},
		{profile: "identity-oidc", wantProfiles: []string{"backend", "identity-oidc"}, present: []string{"backend/core/coreapp/health.go", "backend/core/coreapp/oidc_adapter.go"}, absent: []string{"frontend/frontend/core/pages/identity-accounts.page.json"}},
		{profile: "frontend", wantProfiles: []string{"frontend"}, present: []string{"frontend/frontend/core/pages/identity-accounts.page.json"}, absent: []string{"backend/core/coreapp/health.go", "backend/core/coreapp/oidc_adapter.go"}},
		{profile: "full", wantProfiles: []string{"backend", "frontend", "full"}, present: []string{"backend/core/coreapp/health.go", "frontend/frontend/core/pages/identity-accounts.page.json"}, absent: []string{"backend/core/coreapp/oidc_adapter.go"}},
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
}

func TestProfileBackendFactsLoadSemantically(t *testing.T) {
	models := []interface {
		Annotations() []entschema.Annotation
		Fields() []ent.Field
	}{coreschema.Tenant{}, coreschema.IdentityAccount{}, coreschema.TenantMember{}, coreschema.Role{}, coreschema.Permission{}, coreschema.AuthSession{}}
	for _, model := range models {
		assertTypedAnnotation(t, model.Annotations(), nexaent.SchemaAnnotationName)
		for _, value := range model.Fields() {
			descriptor := value.Descriptor()
			if descriptor.Err != nil {
				t.Fatalf("%T.%s: %v", model, descriptor.Name, descriptor.Err)
			}
			assertTypedAnnotation(t, descriptor.Annotations, nexaent.FieldAnnotationName)
		}
	}
	memberRoles := coreschema.TenantMember{}.Edges()[2].Descriptor()
	rolePermissions := coreschema.Role{}.Edges()[2].Descriptor()
	if memberRoles.StorageKey == nil || memberRoles.StorageKey.Table != "tenant_member_roles" ||
		rolePermissions.StorageKey == nil || rolePermissions.StorageKey.Table != "role_permissions" {
		t.Fatalf("RBAC relation storage = %#v, %#v", memberRoles.StorageKey, rolePermissions.StorageKey)
	}
	physicalEdges := []struct {
		name, field string
		edge        ent.Edge
	}{
		{"tenant", "tenant_id", coreschema.TenantMember{}.Edges()[0]},
		{"identity_account", "identity_account_id", coreschema.TenantMember{}.Edges()[1]},
		{"tenant", "tenant_id", coreschema.Role{}.Edges()[0]},
		{"tenant", "tenant_id", coreschema.AuthSession{}.Edges()[0]},
		{"identity_account", "identity_account_id", coreschema.AuthSession{}.Edges()[1]},
	}
	for _, expected := range physicalEdges {
		descriptor := expected.edge.Descriptor()
		if descriptor.Name != expected.name || descriptor.Field != expected.field || !descriptor.Unique || !descriptor.Required {
			t.Fatalf("physical edge = %#v, want name=%s field=%s unique required", descriptor, expected.name, expected.field)
		}
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
	if _, ok := proto.Method("core.v1.CoreService.Login"); !ok {
		t.Fatal("Login method missing")
	}
	if _, ok := proto.Method("core.v1.CoreService.Register"); !ok {
		t.Fatal("Register method missing")
	}
	if _, ok := proto.Method("core.v1.CoreService.Revoke"); !ok {
		t.Fatal("Revoke method missing")
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
	if _, ok := api.Operation("core.auth.providers"); !ok {
		t.Fatal("provider discovery operation missing")
	}
	nativeIDs := make(map[string]struct{})
	nativeRoutes := make(map[string]struct{})
	for _, operation := range api.Operations() {
		nativeIDs[operation.ID()] = struct{}{}
		nativeRoutes[string(operation.Method())+"\x00"+operation.Path()] = struct{}{}
	}
	service, ok := proto.Service("core.v1.CoreService")
	if !ok {
		t.Fatal("CoreService missing")
	}
	for _, method := range service.Methods() {
		proxy, selected := method.HTTPProxy()
		if !selected {
			continue
		}
		if _, duplicate := nativeIDs[proxy.OperationID()]; duplicate {
			t.Fatalf("native and generated operation %q collide", proxy.OperationID())
		}
		if _, duplicate := nativeRoutes[string(proxy.Method())+"\x00"+proxy.Path()]; duplicate {
			t.Fatalf("native and generated route %s %s collide", proxy.Method(), proxy.Path())
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
	if err != nil || len(merged.Operations()) != 7 {
		t.Fatalf("merged Core operations = %d, %v", len(merged.Operations()), err)
	}
}

func TestProfileFrontendFactsAreStructuredAndStandalone(t *testing.T) {
	provider, err := core.New()
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Manifest().ResolveProfile("frontend")
	if err != nil {
		t.Fatal(err)
	}
	frontend, ok := provider.Manifest().LookupProfile("frontend")
	if !ok || len(frontend.RequiredProfileIDs()) != 0 {
		t.Fatalf("frontend dependencies = %#v", frontend.RequiredProfileIDs())
	}

	schemaFile, _ := provider.Tree().Lookup("frontend/frontend/core/object-schema/identity-account.schema.json")
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaFile.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "https://nexa.dev/frontend/core/identity-account.schema.json"
	if err := compiler.AddResource(schemaURL, document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(map[string]any{"username": "alice", "displayName": "Alice", "status": "enabled"}); err != nil {
		t.Fatal(err)
	}

	zh := locale(t, provider.Tree(), "frontend/frontend/core/locales/zh-CN.json")
	en := locale(t, provider.Tree(), "frontend/frontend/core/locales/en-US.json")
	pageFile, _ := provider.Tree().Lookup("frontend/frontend/core/pages/identity-accounts.page.json")
	var page struct {
		TitleKey     string `json:"titleKey"`
		ObjectSchema string `json:"objectSchema"`
	}
	if err := json.Unmarshal(pageFile.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if zh[page.TitleKey] == "" || en[page.TitleKey] == "" || page.ObjectSchema != "../object-schema/identity-account.schema.json" {
		t.Fatalf("page references are unresolved: %#v", page)
	}
}

func closurePaths(closure sourceplugin.ProfileClosure) map[string]bool {
	result := make(map[string]bool)
	for _, file := range closure.Files() {
		result[file.Path()] = true
	}
	return result
}

func assertTypedAnnotation(t *testing.T, values []entschema.Annotation, name string) {
	t.Helper()
	for _, value := range values {
		annotation, ok := value.(nexaent.Annotation)
		if !ok || annotation.Name() != name {
			continue
		}
		if _, err := annotation.CanonicalJSON(); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("annotation %s missing", name)
}

type treeResolver struct{ tree sourceplugin.Tree }

func (r *treeResolver) Open(_ context.Context, path string) (io.ReadCloser, error) {
	if path == "nexa/protocol/v1/options.proto" {
		return io.NopCloser(bytes.NewReader(protocol.OptionsProto())), nil
	}
	file, ok := r.tree.Lookup(path)
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(file.Bytes())), nil
}

func locale(t *testing.T, tree sourceplugin.Tree, path string) map[string]string {
	t.Helper()
	file, ok := tree.Lookup(path)
	if !ok {
		t.Fatalf("%s missing", path)
	}
	result := make(map[string]string)
	if err := json.Unmarshal(file.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
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

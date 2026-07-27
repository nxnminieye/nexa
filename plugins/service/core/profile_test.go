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

	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/frontend"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/nexaent"
	core "github.com/nxnminieye/nexa/plugins/service/core"
	coreschema "github.com/nxnminieye/nexa/plugins/service/core/_bundle/backend/core/ent/schema"
	"github.com/nxnminieye/nexa/project/servicecatalog"
	"github.com/nxnminieye/nexa/provenance"
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
		{profile: "frontend", wantProfiles: []string{"frontend"}, present: []string{"frontend/frontend/core/pages/accounts.page.json"}, absent: []string{"backend/core/coreapp/health.go", "backend/core/coreapp/oidc_adapter.go"}},
		{profile: "full", wantProfiles: []string{"backend", "frontend", "full"}, present: []string{"backend/core/coreapp/health.go", "frontend/frontend/core/pages/accounts.page.json"}, absent: []string{"backend/core/coreapp/oidc_adapter.go"}},
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
	}{coreschema.Tenant{}, coreschema.IdentityAccount{}, coreschema.TenantMember{}, coreschema.Role{}, coreschema.TenantMemberRoleGrant{}, coreschema.Permission{}, coreschema.AuthSession{}}
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
	if err != nil || len(merged.Operations()) != 31 {
		t.Fatalf("merged Core operations = %d, %v", len(merged.Operations()), err)
	}
}

func TestProfileFrontendFactsAreStructuredAndStandalone(t *testing.T) {
	provider, err := core.New()
	if err != nil {
		t.Fatal(err)
	}
	closure, err := provider.Manifest().ResolveProfile("frontend")
	if err != nil {
		t.Fatal(err)
	}
	frontendProfile, ok := provider.Manifest().LookupProfile("frontend")
	if !ok || len(frontendProfile.RequiredProfileIDs()) != 0 {
		t.Fatalf("frontend dependencies = %#v", frontendProfile.RequiredProfileIDs())
	}

	wantPaths := []string{
		"frontend/frontend/core/locales/en-US.json",
		"frontend/frontend/core/locales/zh-CN.json",
		"frontend/frontend/core/pages/accounts.page.json",
		"frontend/frontend/core/pages/members.page.json",
		"frontend/frontend/core/pages/menus.page.json",
		"frontend/frontend/core/pages/permissions.page.json",
		"frontend/frontend/core/pages/roles.page.json",
		"frontend/frontend/core/pages/tenants.page.json",
	}
	if got := profilePaths(closure); !sameStrings(got, wantPaths) {
		t.Fatalf("frontend files = %#v", got)
	}

	var specs []frontend.PageSpec
	var locales []frontend.Locale
	for _, path := range wantPaths {
		file, ok := provider.Tree().Lookup(path)
		if !ok {
			t.Fatalf("%s missing", path)
		}
		switch filepath.Base(filepath.Dir(path)) {
		case "pages":
			spec, err := frontend.ParsePageSpec(path, file.Bytes())
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			specs = append(specs, spec)
		case "locales":
			locale, err := frontend.ParseLocale(path, file.Bytes())
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			locales = append(locales, locale)
		default:
			t.Fatalf("legacy frontend asset remains: %s", path)
		}
	}
	merged := mergedCoreAPI(t, provider)
	operation, ok := merged.Operation("core.iam.account.list")
	if !ok {
		t.Fatal("core.iam.account.list missing")
	}
	response, ok := merged.Type(operation.ResponseType())
	if !ok {
		t.Fatalf("response type %q missing", operation.ResponseType())
	}
	items, ok := response.Field("Items")
	if !ok {
		t.Fatal("Items field missing")
	}
	element, ok := items.ValueType().Element()
	if !ok {
		t.Fatalf("Items value type = %s", items.ValueType().Kind())
	}
	item, ok := merged.Type(element.Name())
	if !ok {
		t.Fatalf("Items ref type %q missing", element.Name())
	}
	accountID, ok := item.Field("AccountId")
	if !ok {
		var paths [][]string
		for _, field := range item.Fields() {
			paths = append(paths, field.Path())
		}
		t.Fatalf("AccountId field missing from %#v", paths)
	}
	if !items.Required() || items.ValueType().Kind() != httpapi.ValueArray || element.Kind() != httpapi.ValueRef || !accountID.Required() || accountID.ValueType().Kind() != httpapi.ValueScalar || accountID.ValueType().Name() != "string" {
		t.Fatalf("account list projection is not a required array/ref row key")
	}
	document, err := frontend.Build(merged, specs, locales...)
	if err != nil {
		t.Fatalf("build frontend IR: %v", err)
	}
	canonical, err := frontend.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Pages []struct {
			Operations []json.RawMessage `json:"operations"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(canonical, &wire); err != nil {
		t.Fatal(err)
	}
	operationCount := 0
	for _, page := range wire.Pages {
		operationCount += len(page.Operations)
	}
	if document.PageCount() != 6 || operationCount != 22 {
		t.Fatalf("frontend IR = pages:%d operations:%d", document.PageCount(), operationCount)
	}
	request, err := frontend.CanonicalRenderRequest(frontend.RenderRequest{
		FrontendIR: document, RepositoryRoot: "/workspace/example", GeneratedScope: "frontend/generated",
		ExtensionScopes: []string{"frontend/extensions"}, FrontendSourceLockDigest: provenance.SHA256([]byte("core-frontend-lock")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := frontend.ValidateRendererInput(request); err != nil {
		t.Fatalf("validate renderer input: %v", err)
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

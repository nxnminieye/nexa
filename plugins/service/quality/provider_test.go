package quality

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/frontend"
	"github.com/nxnminieye/nexa/generation/httpapi"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func TestProviderPublishesFrozenProfiles(t *testing.T) {
	provider, err := New()
	if err != nil {
		var projected *sourceplugin.Error
		if errors.As(err, &projected) {
			t.Fatalf("provider: code=%s reason=%s pointer=%s", projected.Code(), projected.Reason(), projected.Pointer())
		}
		t.Fatal(err)
	}
	identity := provider.Manifest().Identity()
	if identity.ProviderID() != "quality-runtime" || identity.Version() != "v0.2.0" {
		t.Fatalf("identity = %s@%s", identity.ProviderID(), identity.Version())
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	if ref.ProviderID() != identity.ProviderID() || ref.ModulePath() != identity.ModulePath() || ref.PackagePath() != identity.PackagePath() || ref.Version() != "v0.2.0" {
		t.Fatalf("provider release ref = %s@%s", ref.ProviderID(), ref.Version())
	}
	snapshot, err := sourceplugin.SnapshotProvider(provider)
	if err != nil || snapshot.Manifest().Digest() != provider.Manifest().Digest() || snapshot.Tree().Digest() != provider.Tree().Digest() {
		t.Fatalf("provider snapshot = %#v, %v", snapshot, err)
	}

	backend, err := provider.Manifest().ResolveProfile("backend")
	if err != nil || !reflect.DeepEqual(backend.ProfileIDs(), []string{"backend"}) {
		t.Fatalf("backend closure = %#v, %v", backend.ProfileIDs(), err)
	}
	frontend, err := provider.Manifest().ResolveProfile("frontend")
	if err != nil || !reflect.DeepEqual(frontend.ProfileIDs(), []string{"frontend"}) {
		t.Fatalf("frontend closure = %#v, %v", frontend.ProfileIDs(), err)
	}
	full, err := provider.Manifest().ResolveProfile("full")
	if err != nil || !reflect.DeepEqual(full.ProfileIDs(), []string{"backend", "frontend", "full"}) {
		t.Fatalf("full closure = %#v, %v", full.ProfileIDs(), err)
	}

	first := provider.Tree().Files()
	first[0] = first[len(first)-1]
	if reflect.DeepEqual(first, provider.Tree().Files()) {
		t.Fatal("provider tree aliases caller mutation")
	}
}

func TestProviderManualContractsAreReadOnly(t *testing.T) {
	provider, err := New()
	if err != nil {
		t.Fatal(err)
	}
	root := materializeProfile(t, provider, "backend")

	apiDocument, err := httpapi.Load(context.Background(), httpapi.LoadOptions{
		RepositoryRoot: root,
		EntryFile:      "backend/quality/desc/quality.api",
	})
	if err != nil {
		t.Fatal(err)
	}
	operations := apiDocument.Operations()
	if len(operations) != 2 {
		t.Fatalf("API operations = %d", len(operations))
	}
	for _, operation := range operations {
		if operation.Method() != api.MethodGET {
			t.Fatalf("operation %q method = %s", operation.ID(), operation.Method())
		}
	}

	protocolDocument, err := genprotocol.Compile(context.Background(), genprotocol.CompileOptions{
		ServiceID:  "quality",
		EntryFiles: []string{"backend/quality/desc/quality.proto"},
		Resolver:   directoryResolver{root: root},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, ok := protocolDocument.Service("quality.v1.QualityService")
	if !ok || len(service.Methods()) != 2 {
		t.Fatalf("QualityService methods = %#v", service.Methods())
	}
}

func TestFrontendProfileIsIndependentStructuredReadOnlySource(t *testing.T) {
	provider, err := New()
	if err != nil {
		t.Fatal(err)
	}
	closure, err := provider.Manifest().ResolveProfile("frontend")
	if err != nil {
		t.Fatal(err)
	}
	if len(closure.BundleRequirements()) != 0 || len(closure.Validations()) != 0 {
		t.Fatalf("frontend dependencies = bundles:%d validations:%d", len(closure.BundleRequirements()), len(closure.Validations()))
	}

	wantPaths := []string{
		"frontend/frontend/quality/locales/en-US.json",
		"frontend/frontend/quality/locales/zh-CN.json",
		"frontend/frontend/quality/pages/snapshot.page.json",
	}
	var specs []frontend.PageSpec
	var locales []frontend.Locale
	var gotPaths []string
	for _, file := range closure.Files() {
		gotPaths = append(gotPaths, file.Path())
		entry, ok := provider.Tree().Lookup(file.Path())
		if !ok {
			t.Fatalf("tree file %q missing", file.Path())
		}
		switch filepath.Base(filepath.Dir(file.Path())) {
		case "pages":
			spec, err := frontend.ParsePageSpec(file.Path(), entry.Bytes())
			if err != nil {
				t.Fatalf("parse %s: %v", file.Path(), err)
			}
			specs = append(specs, spec)
		case "locales":
			locale, err := frontend.ParseLocale(file.Path(), entry.Bytes())
			if err != nil {
				t.Fatalf("parse %s: %v", file.Path(), err)
			}
			locales = append(locales, locale)
		default:
			t.Fatalf("legacy frontend asset remains: %s", file.Path())
		}
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("frontend files = %#v", gotPaths)
	}

	root := materializeProfile(t, provider, "backend")
	apiDocument, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "backend/quality/desc/quality.api"})
	if err != nil {
		t.Fatal(err)
	}
	document, err := frontend.Build(apiDocument, specs, locales...)
	if err != nil {
		t.Fatalf("build frontend IR: %v", err)
	}
	canonical, err := frontend.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	type binding struct {
		Direction string   `json:"direction"`
		Path      []string `json:"path"`
	}
	type field struct {
		ID       string    `json:"id"`
		Bindings []binding `json:"bindings"`
		Columns  []struct {
			ID   string   `json:"id"`
			Path []string `json:"path"`
		} `json:"columns"`
	}
	var wire struct {
		Pages []struct {
			Operations []json.RawMessage `json:"operations"`
			Fields     []field           `json:"fields"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(canonical, &wire); err != nil {
		t.Fatal(err)
	}
	if document.PageCount() != 1 || len(wire.Pages) != 1 || len(wire.Pages[0].Operations) != 1 {
		t.Fatalf("frontend IR = pages:%d operations:%d", document.PageCount(), len(wire.Pages[0].Operations))
	}
	wantFields := map[string]string{
		"api-version": "APIVersion", "kind": "Kind", "source-profile": "SourceProfile",
		"read-model-scope": "ReadModelScope", "revision": "Revision", "requirements": "Requirements",
	}
	for _, candidate := range wire.Pages[0].Fields {
		wantPath, ok := wantFields[candidate.ID]
		if !ok || len(candidate.Bindings) != 1 || candidate.Bindings[0].Direction != "response" || !reflect.DeepEqual(candidate.Bindings[0].Path, []string{wantPath}) {
			t.Fatalf("quality field = %#v", candidate)
		}
		if candidate.ID == "requirements" {
			wantColumns := []struct {
				id, path string
			}{
				{"requirement", "Requirement"}, {"title", "Title"}, {"status", "Status"}, {"test-refs", "TestRefs"},
				{"evidence-refs", "EvidenceRefs"}, {"freeze-refs", "FreezeRefs"}, {"freeze-status", "FreezeStatus"}, {"gap-codes", "GapCodes"},
			}
			if len(candidate.Columns) != len(wantColumns) {
				t.Fatalf("requirements columns = %d", len(candidate.Columns))
			}
			for index, column := range candidate.Columns {
				if column.ID != wantColumns[index].id || !reflect.DeepEqual(column.Path, []string{wantColumns[index].path}) {
					t.Fatalf("requirements column %d = %#v", index, column)
				}
			}
		}
		delete(wantFields, candidate.ID)
	}
	if len(wantFields) != 0 {
		t.Fatalf("missing quality fields = %#v", wantFields)
	}
	request, err := frontend.CanonicalRenderRequest(frontend.RenderRequest{
		FrontendIR: document, RepositoryRoot: "/workspace/example", GeneratedScope: "frontend/generated",
		ExtensionScopes: []string{"frontend/extensions"}, FrontendSourceLockDigest: provenance.SHA256([]byte("quality-frontend-lock")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := frontend.ValidateRendererInput(request); err != nil {
		t.Fatalf("validate renderer input: %v", err)
	}
}

func materializeProfile(t *testing.T, provider sourceplugin.Provider, profile string) string {
	t.Helper()
	closure, err := provider.Manifest().ResolveProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, file := range closure.Files() {
		entry, ok := provider.Tree().Lookup(file.Path())
		if !ok {
			t.Fatalf("tree file %q missing", file.Path())
		}
		name := filepath.Join(root, filepath.FromSlash(file.Path()))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, entry.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

type directoryResolver struct{ root string }

func (r directoryResolver) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.Open(filepath.Join(r.root, filepath.FromSlash(path)))
}

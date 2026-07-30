package quality

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
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
	if identity.ProviderID() != "quality-runtime" || identity.Version() != "v0.3.0-alpha.2" {
		t.Fatalf("identity = %s@%s", identity.ProviderID(), identity.Version())
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	if ref.ProviderID() != identity.ProviderID() || ref.ModulePath() != identity.ModulePath() || ref.PackagePath() != identity.PackagePath() || ref.Version() != "v0.3.0-alpha.2" {
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
	for _, removed := range []string{"frontend", "full"} {
		if _, ok := provider.Manifest().LookupProfile(removed); ok {
			t.Fatalf("consumer-owned %q profile remains public", removed)
		}
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

package integration_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/nxnminieye/nexa/generation/composition"
	generationhttpapi "github.com/nxnminieye/nexa/generation/httpapi"
	generationprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/project/servicecatalog"
	goctlparser "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/parser"
)

func TestSourceBundleCoreCatalogSelectsCoreAndAccountProxyOperations(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core", "services.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := servicecatalog.Parse("services.yaml", content)
	if err != nil {
		t.Fatal(err)
	}
	for _, serviceID := range []string{"core", "account"} {
		service, ok := catalog.Lookup(serviceID)
		if !ok {
			t.Fatalf("service %q missing", serviceID)
		}
		bindings := service.CapabilityBindings()
		if len(bindings) != 1 || bindings[0].ID() != composition.CapabilityID || bindings[0].APIVersion() != composition.CapabilityVersion {
			t.Fatalf("%s capability bindings = %#v", serviceID, bindings)
		}
	}
}

func TestSourceBundleCoreCompositionRendersTypedArtifacts(t *testing.T) {
	temporary := canonicalIntegrationDirectory(t, t.TempDir())
	if err := os.CopyFS(temporary, os.DirFS(filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core"))); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(temporary, os.DirFS(filepath.Join(repositoryRoot(t), "plugins", "service", "core", "_bundle"))); err != nil {
		t.Fatal(err)
	}
	repository, err := os.OpenRoot(temporary)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	catalog, err := servicecatalog.Load(repository, "services.yaml")
	if err != nil {
		t.Fatal(err)
	}
	protocols := []generationprotocol.Document{compileSourceBundleProtocol(t, temporary, "core"), compileSourceBundleProtocol(t, temporary, "account")}
	native, err := generationhttpapi.Load(context.Background(), generationhttpapi.LoadOptions{RepositoryRoot: temporary, EntryFile: "backend/core/api/desc/core.api"})
	if err != nil {
		t.Fatal(err)
	}
	document, err := composition.Build(catalog, protocols, native, composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/nexa-generation-consumer"})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := composition.Render(document, composition.RenderOptions{CoreRoot: "backend/core/api"})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) == 0 {
		t.Fatal("composition rendered no artifacts")
	}
	var apiPaths []string
	var coreProxy bool
	for _, artifact := range artifacts {
		if artifact.ID == "api.core" && artifact.Path == "backend/core/api/desc/generated/core.proxy.generated.api" {
			coreProxy = true
		}
		if artifact.Path == "backend/core/api/desc/generated/core.generated.api" {
			t.Fatal("Core proxy fragment collides with the API aggregate path")
		}
		if filepath.Ext(artifact.Path) != ".api" {
			continue
		}
		apiPaths = append(apiPaths, artifact.Path)
		parsed, parseErr := goctlparser.Parse(artifact.Path, artifact.Content)
		if parseErr != nil || parsed.Validate() != nil {
			t.Fatalf("rendered API %s is invalid: %v", artifact.Path, parseErr)
		}
	}
	if !coreProxy || len(apiPaths) == 0 {
		t.Fatalf("rendered API artifacts = %#v", apiPaths)
	}
	sort.Strings(apiPaths)
	generated, err := composition.GeneratedAPI(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generationhttpapi.Merge(native, generated); err != nil {
		t.Fatal(err)
	}
}

func TestSourceBundleCoreDetachedMigrationFactsValidateStructurally(t *testing.T) {
	temporary := canonicalIntegrationDirectory(t, t.TempDir())
	bundle := filepath.Join(repositoryRoot(t), "plugins", "service", "core", "_bundle")
	if err := os.CopyFS(temporary, os.DirFS(bundle)); err != nil {
		t.Fatal(err)
	}
	validateDetachedMigrationFacts(t, temporary)
}

type sourceBundleProtocolResolver struct{ root string }

func (resolver sourceBundleProtocolResolver) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name = filepath.Join(resolver.root, filepath.FromSlash(name))
	return os.Open(name)
}

func compileSourceBundleProtocol(t *testing.T, root, service string) generationprotocol.Document {
	t.Helper()
	entry := "backend/core/rpc/desc/core.proto"
	if service != "core" {
		entry = "backend/" + service + "/desc/" + service + ".proto"
	}
	resolver := sourceBundleProtocolResolver{root: root}
	document, err := generationprotocol.Compile(context.Background(), generationprotocol.CompileOptions{ServiceID: service, EntryFiles: []string{entry}, Resolver: resolver})
	if err != nil {
		t.Fatalf("compile %s ProtocolIR fixture: %v", service, err)
	}
	return document
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func makeTreeWritableOnCleanup(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			mode := os.FileMode(0o600)
			if entry.IsDir() {
				mode = 0o700
			}
			_ = os.Chmod(path, mode)
			return nil
		})
	})
}

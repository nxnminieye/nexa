package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	modmodule "golang.org/x/mod/module"
)

func TestEntHelperModuleProjectionExternalConsumer(t *testing.T) {
	base := canonicalIntegrationDirectory(t, t.TempDir())
	repository := filepath.Join(base, "repository")
	consumer := filepath.Join(repository, "consumer")
	framework := filepath.Join(repository, "framework")
	staging := filepath.Join(base, "staging")
	scratch := filepath.Join(base, "scratch")
	for _, path := range []string{filepath.Join(consumer, "ent", "schema"), staging, scratch} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	frameworkVersion := "v0.8.0"
	if err := materializeHermeticModuleDirectory(repositoryRoot(t), framework, modmodule.Version{Path: nexaModulePath, Version: frameworkVersion}); err != nil {
		t.Fatal(err)
	}
	moduleFile := "module example.com/nexa-projection-consumer\n\ngo 1.25.0\n\nrequire (\n\tgithub.com/nxnminieye/nexa " + frameworkVersion + "\n\tgolang.org/x/mod v0.31.0\n)\n\nreplace github.com/nxnminieye/nexa " + frameworkVersion + " => " + filepath.ToSlash(framework) + "\n"
	writeConsumerFile(t, filepath.Join(consumer, "go.mod"), moduleFile)
	writeConsumerFile(t, filepath.Join(consumer, "main.go"), externalProjectionConsumerSource)

	command := exec.Command("go", "run", "-mod=mod", ".", repository, staging, scratch)
	command.Dir = consumer
	command.Env = overriddenEnvironment(
		os.Environ(),
		"GOWORK=off",
		"GOENV=off",
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOMODCACHE="+rootModuleCache(t),
		"GOCACHE="+filepath.Join(base, "go-cache"),
		"HOME="+filepath.Join(base, "home"),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("external projection consumer: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "projection-ok" {
		t.Fatalf("external projection output = %q", got)
	}
}

func canonicalIntegrationDirectory(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

const externalProjectionConsumerSource = `package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/modfile"
)

func main() {
	if len(os.Args) != 4 { panic("arguments") }
	repository, staging, scratchParent := os.Args[1], os.Args[2], os.Args[3]
	identity, err := toolchain.CurrentFrameworkModuleIdentity()
	if err != nil { panic(err) }
	module, err := identity.Module()
	if err != nil || module.Path != "github.com/nxnminieye/nexa" || module.Version != "v0.8.0" { panic("module identity") }
	kind, err := identity.ReplacementKind()
	if err != nil || kind != "local" { panic("replacement identity") }
	schema, err := provenance.ParseDomainSource("consumer/ent/schema")
	if err != nil { panic(err) }
	location, err := toolchain.LocateModule(toolchain.ModuleLocateSpec{RepositoryRoot: repository, SchemaDir: schema})
	if err != nil { panic(err) }
	helper := []byte("package main\nfunc main() {}\n")
	projected, err := toolchain.ProjectScratchModule(toolchain.ScratchModuleSpec{
		RepositoryRoot: repository, StagingRoot: staging, ScratchParent: scratchParent,
		Location: location, BuildTags: []string{"integration"}, Framework: identity,
		Helper: toolchain.HelperSource{Path: "cmd/enthelper/main.go", Bytes: helper, Digest: provenance.SHA256(helper)},
	})
	if err != nil { panic(err) }
	root, err := projected.Root()
	if err != nil { panic(err) }
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil { panic(err) }
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil || parsed.Module == nil || parsed.Module.Mod.Path != "github.com/nxnminieye/nexa/generation/enthelperexec" { panic("scratch module") }
	if err := projected.Cleanup(); err != nil { panic(err) }
	if _, err := os.Stat(root); !os.IsNotExist(err) { panic("scratch cleanup") }
	fmt.Println("projection-ok")
}
`

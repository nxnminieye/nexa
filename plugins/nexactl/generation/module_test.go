package generation

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/provenance"
)

func TestNestedModuleFactsRebaseArtifactsAndAttributeModuleSource(t *testing.T) {
	repository := t.TempDir()
	for name, content := range map[string]string{
		"backend/go.mod":             "module example.com/consumer/backend\n\ngo 1.25.0\n",
		"backend/core/desc/core.api": "syntax = \"v1\"\n",
	} {
		filename := filepath.Join(repository, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	repository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	module, repositoryCoreRoot, moduleCoreRoot, err := loadAPIModuleFacts(repository, "backend/core/desc/core.api")
	if err != nil {
		t.Fatalf("module facts: %#v", err)
	}
	owner, err := provenance.RepositoryRef("backend/account/desc/account.proto", "method:account.get")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := rebaseRenderedArtifacts([]composition.RenderedArtifact{{
		ID: "logic.account.get", Path: "core/internal/logic/rpcproxy/account-get.generated.go", Owner: "nexa.dev/generator/composition/v1",
		Content: []byte("package rpcproxy\nimport _ \"example.com/consumer/backend/core/internal/serviceclients/account\"\n"),
		Sources: []provenance.SourceRef{owner},
	}}, module, repositoryCoreRoot)
	if err != nil {
		t.Fatal(err)
	}
	if module.modulePath != "example.com/consumer/backend" || module.repositoryRoot != "backend" || moduleCoreRoot != "core" {
		t.Fatalf("module facts = %#v, module core root = %q", module, moduleCoreRoot)
	}
	if len(rendered) != 1 || rendered[0].Path != "backend/core/internal/logic/rpcproxy/account-get.generated.go" ||
		!reflect.DeepEqual(rendered[0].Sources, []provenance.SourceRef{owner, module.source.Ref}) {
		t.Fatalf("rendered = %#v", rendered)
	}
}

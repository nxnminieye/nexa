package apigo_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/generation/apigo"
	"github.com/nxnminieye/nexa/generation/directwrite"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
)

const directAdapterModuleGraph = `{"apiVersion":"nexa.dev/ent-helper-module-graph/v1","consumerModule":{"path":"example.com/consumer","version":"v0.0.0"},"goVersion":"1.25","helperDigest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","kind":"EntHelperModuleGraph","moduleSources":[],"modules":[],"toolModule":{"path":"github.com/nxnminieye/nexa","version":"v0.1.0"}}`

func TestWriteDirectAPIKeepsHostOnlyEvidenceAfterReplaceTreeDeletion(t *testing.T) {
	document, rendered, sources := compositionFixture(t)
	source, _ := provenance.ParseDomainSource("quality/module-graph.json")
	graph, err := toolchain.ParseModuleGraphSnapshot(source, []byte(directAdapterModuleGraph))
	if err != nil {
		t.Fatal(err)
	}
	host := []directwrite.OutputScope{
		{Path: "backend/core/desc/generated", Mode: directwrite.OutputModeReplaceTree},
		{Path: "backend/core/generated", Mode: directwrite.OutputModeFileSet},
		{Path: "backend/core/internal/logic/rpcproxy", Mode: directwrite.OutputModeFileSet},
		{Path: "backend/core/internal/rpcproxy/generated", Mode: directwrite.OutputModeFileSet},
		{Path: "backend/core/internal/serviceclients", Mode: directwrite.OutputModeFileSet},
	}
	toolScopes := []directwrite.OutputScope{{Path: "backend/core/internal/handler/generated", Mode: directwrite.OutputModeReplaceTree}}
	command := append(append([]directwrite.OutputScope(nil), host...), toolScopes...)
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	runner := toolchain.DirectRunnerFunc(func(_ context.Context, call toolchain.DirectRequest) (toolchain.Result, error) {
		return toolchain.Result{ToolID: "api", Version: "v2", ExecutableVersion: "api-v2", Stdout: []byte("{}")}, nil
	})
	_, err = apigo.WriteDirect(context.Background(), apigo.DirectSpec{CoreServiceID: "core", RepositoryRoot: repository, ModuleGraph: graph, HTTPAPIIR: document, Rendered: rendered, CommandScopes: command, ToolScopes: toolScopes}, apigo.DirectOptions{Tool: toolchain.Tool{ID: "api", Version: "v2", Probe: toolchain.ExecutableProbe{ExpectedVersion: "api-v2"}}, Runner: runner, Sources: sources})
	var typed *apigo.Error
	if !errors.As(err, &typed) || typed.ChangeEvidence() != directwrite.ChangeEvidenceHostOnly || len(typed.Report().CompletedWrites) == 0 {
		t.Fatalf("replace-tree failure evidence = %#v", err)
	}
}

func TestWriteDirectAPIRejectsToolMutationOfHostScopeManualFile(t *testing.T) {
	document, rendered, sources := compositionFixture(t)
	source, _ := provenance.ParseDomainSource("quality/module-graph.json")
	graph, err := toolchain.ParseModuleGraphSnapshot(source, []byte(directAdapterModuleGraph))
	if err != nil {
		t.Fatal(err)
	}
	host := []directwrite.OutputScope{
		{Path: "backend/core/desc/generated", Mode: directwrite.OutputModeReplaceTree},
		{Path: "backend/core/generated", Mode: directwrite.OutputModeFileSet},
		{Path: "backend/core/internal/logic/rpcproxy", Mode: directwrite.OutputModeFileSet},
		{Path: "backend/core/internal/rpcproxy/generated", Mode: directwrite.OutputModeFileSet},
		{Path: "backend/core/internal/serviceclients", Mode: directwrite.OutputModeFileSet},
	}
	toolScopes := []directwrite.OutputScope{{Path: "backend/core/internal/handler/generated", Mode: directwrite.OutputModeReplaceTree}}
	command := append(append([]directwrite.OutputScope(nil), host...), toolScopes...)
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	manual := filepath.Join(repository, filepath.FromSlash("backend/core/generated/manual.yaml"))
	if err := os.MkdirAll(filepath.Dir(manual), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manual, []byte("manual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := toolchain.DirectRunnerFunc(func(_ context.Context, call toolchain.DirectRequest) (toolchain.Result, error) {
		if err := os.WriteFile(manual, []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return toolchain.Result{ToolID: "api", Version: "v2", ExecutableVersion: "api-v2", Stdout: canonicalAdapterAPIResult(t, provenance.SHA256(call.Stdin), toolScopes)}, nil
	})
	_, err = apigo.WriteDirect(context.Background(), apigo.DirectSpec{CoreServiceID: "core", RepositoryRoot: repository, ModuleGraph: graph, HTTPAPIIR: document, Rendered: rendered, CommandScopes: command, ToolScopes: toolScopes}, apigo.DirectOptions{Tool: toolchain.Tool{ID: "api", Version: "v2", Probe: toolchain.ExecutableProbe{ExpectedVersion: "api-v2"}}, Runner: runner, Sources: sources})
	var typed *apigo.Error
	if !errors.As(err, &typed) || typed.Reason() != "artifact_invalid" {
		t.Fatalf("host-scope manual mutation = %#v", err)
	}
}

func canonicalAdapterAPIResult(t *testing.T, digest provenance.Digest, scopes []directwrite.OutputScope) []byte {
	t.Helper()
	encoded, err := apigo.CanonicalAPIGoResult(apigo.APIGoResult{APIVersion: apigo.APIGoResultAPIVersion, Kind: apigo.APIGoResultKind, Status: apigo.APIGoResultGenerated, CoreServiceID: "core", InputDigest: digest, OutputScopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

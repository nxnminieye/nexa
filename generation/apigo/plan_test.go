package apigo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	generationapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/apigo"
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/project/servicecatalog"
	"github.com/nxnminieye/nexa/provenance"
	sdkapi "github.com/nxnminieye/nexa/sdk/api"
)

func TestPlanRunsPinnedHelperAndReturnsVerifiedExecutableArtifacts(t *testing.T) {
	document, rendered, sources := compositionFixture(t)
	wantInput, err := httpapi.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fixtureRunner{t: t, coreServiceID: "core", rendered: rendered}
	environment := []toolchain.EnvVar{{Name: "PATH", Value: "/consumer/bin"}}
	artifacts, err := apigo.Plan(context.Background(), document, rendered, optionsFor(runner, environment, sources))
	if err != nil {
		var typed *apigo.Error
		if errors.As(err, &typed) {
			t.Fatalf("Plan() error = %v (%s/%s/%s)", err, typed.Code(), typed.Stage(), typed.Reason())
		}
		t.Fatalf("Plan() error = %T %v", err, err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d", len(runner.calls))
	}
	request := runner.calls[0]
	if !reflect.DeepEqual(request.Args, []string{"generate", "--core-service", "core"}) {
		t.Fatalf("argv = %#v", request.Args)
	}
	if overlap, overlapErr := testDirectoriesOverlap(request.RepositoryRoot, request.StagingRoot); overlapErr != nil || overlap || request.WorkDir != request.StagingRoot || request.Scratch != nil || !reflect.DeepEqual(request.Environment, environment) {
		t.Fatalf("staging request = %#v", request)
	}
	if !runner.rootsCanonical {
		t.Fatal("runner received noncanonical roots")
	}
	environment[0] = toolchain.EnvVar{}
	if request.Environment[0].Name != "PATH" || request.Environment[0].Value != "/consumer/bin" {
		t.Fatalf("request environment aliases Options: %#v", request.Environment)
	}
	if !bytes.Equal(request.Stdin, wantInput) || len(request.Stdin) > toolchain.MaxStdinBytes {
		t.Fatalf("stdin = %s", request.Stdin)
	}
	if _, err := os.Stat(request.StagingRoot); err != nil {
		t.Fatalf("caller staging removed after Plan: %v", err)
	}
	if _, err := os.Stat(request.RepositoryRoot); err != nil {
		t.Fatalf("caller repository removed after Plan: %v", err)
	}

	byPath := make(map[string]transaction.ArtifactInput, len(artifacts))
	for _, value := range artifacts {
		byPath[value.Path] = value
		content := artifactContent(t, request.StagingRoot, value)
		if len(content) == 0 || value.Digest != provenance.SHA256(content) || len(value.Sources) == 0 {
			t.Fatalf("artifact is incomplete: %#v", value)
		}
	}
	for _, expected := range rendered {
		got, ok := byPath[expected.Path]
		if !ok || got.ID != expected.ID || got.Owner != expected.Owner || !bytes.Equal(artifactContent(t, request.StagingRoot, got), expected.Content) || !reflect.DeepEqual(got.Sources, expected.Sources) || got.CreateManual {
			t.Fatalf("composition artifact %q = %#v", expected.Path, got)
		}
	}
	for _, path := range []string{
		"backend/core/desc/generated/core.generated.api",
		"backend/core/generated/api-manifest.json",
		"backend/core/generated/runtime-contract.json",
		"backend/core/internal/handler/accountgethandler.go",
		"backend/core/internal/logic/account/getlogic.go",
		"backend/core/internal/types/types.go",
	} {
		if _, ok := byPath[path]; !ok {
			t.Fatalf("missing artifact %q", path)
		}
	}
	manual := byPath["backend/core/internal/logic/account/getlogic.go"]
	if !manual.CreateManual || manual.Owner != "nexa.dev/generator/api-go-manual/v1" || manual.StalePolicy != artifact.StaleRetain || manual.Probe != nil {
		t.Fatalf("manual artifact = %#v", manual)
	}
	generated := byPath["backend/core/internal/handler/accountgethandler.go"]
	if generated.CreateManual || generated.Owner != "nexa.dev/generator/api-go/v1" || generated.StalePolicy != artifact.StaleDeleteIfUnmodified || generated.Probe == nil {
		t.Fatalf("generated artifact = %#v", generated)
	}
	manifestArtifact := byPath["backend/core/generated/api-manifest.json"]
	manifestContent := artifactContent(t, request.StagingRoot, manifestArtifact)
	manifest, err := generationapi.Parse(manifestArtifact.Path, manifestContent)
	if err != nil {
		t.Fatalf("Parse API Manifest: %v", err)
	}
	runtimeArtifact := byPath["backend/core/generated/runtime-contract.json"]
	runtimeContent := artifactContent(t, request.StagingRoot, runtimeArtifact)
	runtimeContract, err := sdkapi.ParseRuntimeContract(runtimeContent)
	if err != nil {
		t.Fatalf("Parse RuntimeContract: %v", err)
	}
	trace := runtimeContract.Trace()
	if trace.APIManifestVersion() != generationapi.APIVersion || trace.APIManifestCanonicalDigest() != provenance.SHA256(manifestContent) || trace.SourceDigest() != manifest.SourceDigest() {
		t.Fatalf("runtime trace does not identify the same API Manifest: %#v", trace)
	}
	inputDigest, err := artifact.ComputeInputDigest(artifact.GeneratorSpec{ID: "api-go", Version: "v1.0.0"}, sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range artifacts {
		if value.CreateManual {
			continue
		}
		expected := transaction.Ownership{GeneratorID: "api-go", ArtifactID: value.ID, InputDigest: inputDigest}
		content := artifactContent(t, request.StagingRoot, value)
		known, err := value.Probe.Inspect(value.Path, content, expected)
		if err != nil || !known {
			t.Fatalf("ownership probe rejected %q: %v, %v", value.ID, known, err)
		}
		expected.GeneratorID = "other-generator"
		if adopted, err := value.Probe.Inspect(value.Path, content, expected); err != nil || adopted {
			t.Fatalf("ownership probe adopted foreign generator for %q: %v, %v", value.ID, adopted, err)
		}
		expected.GeneratorID, expected.InputDigest = "api-go", provenance.SHA256([]byte("different input"))
		if adopted, err := value.Probe.Inspect(value.Path, content, expected); err != nil || adopted {
			t.Fatalf("ownership probe adopted foreign input for %q: %v, %v", value.ID, adopted, err)
		}
		expected.InputDigest = inputDigest
		changed := append([]byte(nil), content...)
		changed[0] ^= 0xff
		if adopted, err := value.Probe.Inspect(value.Path, changed, expected); err != nil || adopted {
			t.Fatalf("ownership probe adopted changed content for %q: %v, %v", value.ID, adopted, err)
		}
	}

	second, err := apigo.Plan(context.Background(), document, rendered, optionsFor(&fixtureRunner{t: t, coreServiceID: "core", rendered: rendered}, []toolchain.EnvVar{{Name: "PATH", Value: "/consumer/bin"}}, sources))
	if err != nil || !equalArtifacts(artifacts, second) {
		t.Fatalf("second Plan() = %#v, %v", second, err)
	}
	artifacts[0].Digest = provenance.Digest{}
	artifacts[0].Sources[0] = provenance.SourceRef{}
	third, err := apigo.Plan(context.Background(), document, rendered, optionsFor(&fixtureRunner{t: t, coreServiceID: "core", rendered: rendered}, []toolchain.EnvVar{{Name: "PATH", Value: "/consumer/bin"}}, sources))
	if err != nil || !equalArtifacts(second, third) {
		t.Fatalf("Plan() aliases output: %v", err)
	}
}

func TestPlanCanonicalizesRepositoryAndStagingRootsBeforeRunner(t *testing.T) {
	document, rendered, sources := compositionFixture(t)
	runner := &fixtureRunner{t: t, coreServiceID: "core", rendered: rendered}
	options := optionsFor(runner, nil, sources)
	canonicalRepository, err := filepath.EvalSymlinks(options.RepositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	canonicalStaging, err := filepath.EvalSymlinks(options.StagingRoot)
	if err != nil {
		t.Fatal(err)
	}
	aliases := t.TempDir()
	repositoryAlias := filepath.Join(aliases, "repository-alias")
	if err := os.Symlink(options.RepositoryRoot, repositoryAlias); err != nil {
		t.Fatal(err)
	}
	stagingAlias := filepath.Join(aliases, "staging-alias")
	if err := os.Symlink(options.StagingRoot, stagingAlias); err != nil {
		t.Fatal(err)
	}
	options.RepositoryRoot = repositoryAlias
	options.StagingRoot = stagingAlias
	if _, err := apigo.Plan(context.Background(), document, rendered, options); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 || runner.calls[0].RepositoryRoot != canonicalRepository || runner.calls[0].StagingRoot != canonicalStaging || runner.calls[0].WorkDir != canonicalStaging {
		t.Fatalf("runner roots = %#v, want repository=%q staging/workdir=%q", runner.calls, canonicalRepository, canonicalStaging)
	}
}

func TestPlanRejectsMissingRepositoryBeforeRunner(t *testing.T) {
	document, rendered, sources := compositionFixture(t)
	runner := &fixtureRunner{t: t, coreServiceID: "core", rendered: rendered}
	options := optionsFor(runner, nil, sources)
	options.RepositoryRoot = filepath.Join(t.TempDir(), "missing")
	_, err := apigo.Plan(context.Background(), document, rendered, options)
	var typed *apigo.Error
	if !errors.As(err, &typed) || typed.Stage() != "input" || typed.Reason() != "request_invalid" || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Plan() error = %#v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner called with missing repository: %#v", runner.calls)
	}
}

func TestPlanRejectsInvalidStagingBeforeRunner(t *testing.T) {
	document, rendered, sources := compositionFixture(t)
	for _, test := range []struct {
		name    string
		prepare func(*testing.T) string
		cause   error
	}{
		{name: "missing", prepare: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing") }, cause: os.ErrNotExist},
		{name: "not directory", prepare: func(t *testing.T) string {
			name := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(name, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return name
		}, cause: os.ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &fixtureRunner{t: t, coreServiceID: "core", rendered: rendered}
			options := optionsFor(runner, nil, sources)
			options.StagingRoot = test.prepare(t)
			_, err := apigo.Plan(context.Background(), document, rendered, options)
			var typed *apigo.Error
			if !errors.As(err, &typed) || typed.Stage() != "input" || typed.Reason() != "request_invalid" || !errors.Is(err, test.cause) {
				t.Fatalf("Plan() error = %#v", err)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner called with invalid staging: %#v", runner.calls)
			}
		})
	}
}

func TestPlanRejectsOverlappingRepositoryAndStagingBeforeWriting(t *testing.T) {
	document, rendered, sources := compositionFixture(t)
	for _, test := range []struct {
		name    string
		prepare func(*testing.T) (string, string, string)
	}{
		{name: "equal", prepare: func(t *testing.T) (string, string, string) {
			root := t.TempDir()
			return root, root, root
		}},
		{name: "repository contains staging", prepare: func(t *testing.T) (string, string, string) {
			repository := t.TempDir()
			staging := filepath.Join(repository, "tool-staging")
			if err := os.Mkdir(staging, 0o700); err != nil {
				t.Fatal(err)
			}
			return repository, staging, repository
		}},
		{name: "staging contains repository", prepare: func(t *testing.T) (string, string, string) {
			staging := t.TempDir()
			repository := filepath.Join(staging, "repository")
			if err := os.Mkdir(repository, 0o700); err != nil {
				t.Fatal(err)
			}
			return repository, staging, staging
		}},
		{name: "symlink overlap", prepare: func(t *testing.T) (string, string, string) {
			repository := t.TempDir()
			staging := filepath.Join(repository, "tool-staging")
			if err := os.Mkdir(staging, 0o700); err != nil {
				t.Fatal(err)
			}
			aliases := t.TempDir()
			repositoryAlias := filepath.Join(aliases, "repository")
			stagingAlias := filepath.Join(aliases, "staging")
			if err := os.Symlink(repository, repositoryAlias); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(staging, stagingAlias); err != nil {
				t.Fatal(err)
			}
			return repositoryAlias, stagingAlias, repository
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &fixtureRunner{t: t, coreServiceID: "core", rendered: rendered}
			options := optionsFor(runner, nil, sources)
			repository, staging, observedRoot := test.prepare(t)
			seedOverlapRoots(t, repository, staging)
			before := snapshotTree(t, observedRoot)
			options.RepositoryRoot = repository
			options.StagingRoot = staging

			_, err := apigo.Plan(context.Background(), document, rendered, options)
			var typed *apigo.Error
			if !errors.As(err, &typed) || typed.Stage() != "input" || typed.Reason() != "request_invalid" || !errors.Is(err, os.ErrInvalid) {
				t.Fatalf("Plan() error = %#v", err)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner called with overlapping roots: %#v", runner.calls)
			}
			if after := snapshotTree(t, observedRoot); !reflect.DeepEqual(after, before) {
				t.Fatalf("overlap rejection changed repository or staging: before=%#v after=%#v", before, after)
			}
		})
	}
}

func TestPlanEmitsOnlyVerifiedArtifactsFromNonOverlappingToolStaging(t *testing.T) {
	document, rendered, sources := compositionFixture(t)
	runner := &fixtureRunner{t: t, coreServiceID: "core", rendered: rendered}
	options := optionsFor(runner, nil, sources)
	options.StagingRoot = t.TempDir()
	if err := os.MkdirAll(filepath.Join(options.StagingRoot, ".nexa-env", "gopath"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.StagingRoot, ".nexa-env", "gopath", "scratch.go"), []byte("package scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	emitted := map[string][]byte{}
	options.Emit = func(name string, content []byte) error {
		emitted[name] = append([]byte(nil), content...)
		return nil
	}
	artifacts, err := apigo.Plan(context.Background(), document, rendered, options)
	if err != nil {
		t.Fatal(err)
	}
	if overlap, err := testDirectoriesOverlap(options.RepositoryRoot, options.StagingRoot); err != nil || overlap {
		t.Fatalf("tool staging overlaps repository: repo=%q staging=%q err=%v", options.RepositoryRoot, options.StagingRoot, err)
	}
	if len(emitted) != len(artifacts) {
		t.Fatalf("emitted candidates = %d, artifacts = %d", len(emitted), len(artifacts))
	}
	if _, ok := emitted["go.mod"]; ok {
		t.Fatal("tool workspace go.mod leaked into publish candidates")
	}
	for name := range emitted {
		if strings.HasPrefix(name, ".nexa-env/") {
			t.Fatalf("tool environment scratch leaked into publish candidates: %q", name)
		}
	}
	for _, value := range artifacts {
		if provenance.SHA256(emitted[value.Path]) != value.Digest {
			t.Fatalf("candidate %q does not match verified digest", value.Path)
		}
	}
}

func TestInvalidAPIOutputClosesTransactionCandidateWithoutEmit(t *testing.T) {
	document, rendered, sources := compositionFixture(t)
	repository := t.TempDir()
	runner := &fixtureRunner{t: t, coreServiceID: "core", rendered: rendered, invalidGo: true}
	options := optionsFor(runner, nil, sources)
	options.RepositoryRoot = repository
	options.StagingRoot = t.TempDir()
	emitCount := 0
	_, err := transaction.Build(context.Background(), repository, func(_ string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		options.Emit = func(name string, content []byte) error {
			emitCount++
			return emit(name, content)
		}
		_, planErr := apigo.Plan(context.Background(), document, rendered, options)
		return transaction.PlanRequest{}, planErr
	})
	if err == nil || emitCount != 0 {
		t.Fatalf("invalid output error=%v emitCount=%d", err, emitCount)
	}
	matches, globErr := filepath.Glob(filepath.Join(repository, ".nexa-generation-staging-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("failed Build left candidate staging = %v, %v", matches, globErr)
	}
}

func TestStaleOwnershipProbesBindPreviousGeneratedArtifacts(t *testing.T) {
	ref, err := provenance.RepositoryRef("backend/core/desc/core.api", "")
	if err != nil {
		t.Fatal(err)
	}
	sources := []provenance.Source{{Ref: ref, Digest: provenance.SHA256([]byte("core contract"))}}
	generatedAPI := []byte("syntax = \"v1\"\nservice generated { }\n")
	preparedGo := []byte("package accountclient\n\ntype Client struct{}\n")
	previous, err := artifact.NewManifest(artifact.ManifestSpec{
		Generator: artifact.GeneratorSpec{ID: "api-go", Version: "v1.0.0"},
		Sources:   sources,
		Artifacts: []artifact.ArtifactSpec{
			{ID: "api.account", Path: "backend/core/desc/generated/account.generated.api", Owner: "api-go", Digest: provenance.SHA256(generatedAPI), Sources: []provenance.SourceRef{ref}, StalePolicy: artifact.StaleDeleteIfUnmodified},
			{ID: "client.account", Path: "backend/core/internal/serviceclients/account/client.generated.go", Owner: "api-go", Digest: provenance.SHA256(preparedGo), Sources: []provenance.SourceRef{ref}, StalePolicy: artifact.StaleDeleteIfUnmodified},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	probes := apigo.StaleOwnershipProbes(previous)
	if len(probes) != 2 {
		t.Fatalf("stale probes = %d", len(probes))
	}
	for id, fixture := range map[string]struct {
		path    string
		content []byte
	}{
		"api.account":    {path: "backend/core/desc/generated/account.generated.api", content: generatedAPI},
		"client.account": {path: "backend/core/internal/serviceclients/account/client.generated.go", content: preparedGo},
	} {
		expected := transaction.Ownership{GeneratorID: "api-go", ArtifactID: id, InputDigest: previous.InputDigest()}
		if !anyAPIProbeOwns(t, probes, fixture.path, fixture.content, expected) {
			t.Fatalf("previous artifact %q is not owned", id)
		}
		if anyAPIProbeOwns(t, probes, fixture.path, append(append([]byte(nil), fixture.content...), '\n'), expected) {
			t.Fatalf("changed previous artifact %q was adopted", id)
		}
	}
}

func anyAPIProbeOwns(t *testing.T, probes []transaction.OwnershipProbe, name string, content []byte, expected transaction.Ownership) bool {
	t.Helper()
	for _, probe := range probes {
		owned, err := probe.Inspect(name, content, expected)
		if err != nil {
			t.Fatal(err)
		}
		if owned {
			return true
		}
	}
	return false
}

func TestPlanAcceptsNativeOnlyAPIWithNoGeneratedProxyFragment(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "core.api")
	source := "syntax = \"v1\"\ninfo (nexaContractVersion: \"nexa.dev/http-api/v1\")\ntype HealthRequest {}\ntype HealthResponse { OK bool }\n@server (nexaOperationId: \"health.get\" nexaAuthMode: \"none\")\nservice core-api { @handler health get /health (HealthRequest) returns (HealthResponse) }"
	if err := os.WriteFile(entry, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "core.api"})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fixtureRunner{t: t, coreServiceID: "core", skipBehaviorTest: true}
	artifacts, err := apigo.Plan(context.Background(), document, nil, optionsFor(runner, []toolchain.EnvVar{{Name: "PATH", Value: "/consumer/bin"}}, document.Sources()))
	if err != nil {
		var typed *apigo.Error
		if errors.As(err, &typed) {
			t.Fatalf("Plan() error = %v (%s/%s/%s)", err, typed.Code(), typed.Stage(), typed.Reason())
		}
		t.Fatal(err)
	}
	found := false
	for _, value := range artifacts {
		found = found || value.ID == "api.aggregate.core"
	}
	if !found {
		t.Fatal("Plan() omitted the explicit empty generated API aggregate")
	}
}

func TestPlanRejectsHelperAndReadbackFailuresBeforePublishingArtifacts(t *testing.T) {
	document, rendered, sources := compositionFixture(t)
	tests := []struct {
		name, reason string
		prepare      func(*fixtureRunner, *context.Context)
	}{
		{name: "version mismatch", reason: "tool_result_invalid", prepare: func(r *fixtureRunner, _ *context.Context) { r.executableVersion = "goctl 0.0.0" }},
		{name: "runner failure", reason: "tool_failed", prepare: func(r *fixtureRunner, _ *context.Context) { r.failure = errors.New("private helper output") }},
		{name: "stdout limit", reason: "result_output_limit", prepare: func(r *fixtureRunner, _ *context.Context) { r.oversized = true }},
		{name: "noncanonical result", reason: "result_invalid", prepare: func(r *fixtureRunner, _ *context.Context) { r.noncanonical = true }},
		{name: "artifact digest", reason: "artifact_invalid", prepare: func(r *fixtureRunner, _ *context.Context) { r.digestMismatch = true }},
		{name: "input digest", reason: "result_invalid", prepare: func(r *fixtureRunner, _ *context.Context) { r.inputDigestMismatch = true }},
		{name: "nonzero result", reason: "tool_failed", prepare: func(r *fixtureRunner, _ *context.Context) { r.exitCode = 23 }},
		{name: "invalid Go", reason: "artifact_invalid", prepare: func(r *fixtureRunner, _ *context.Context) { r.invalidGo = true }},
		{name: "canceled", reason: "operation_canceled", prepare: func(_ *fixtureRunner, selected *context.Context) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			*selected = ctx
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fixtureRunner{t: t, coreServiceID: "core", rendered: rendered}
			ctx := context.Context(context.Background())
			test.prepare(runner, &ctx)
			artifacts, err := apigo.Plan(ctx, document, rendered, optionsFor(runner, []toolchain.EnvVar{{Name: "PATH", Value: "/consumer/bin"}}, sources))
			var typed *apigo.Error
			if !errors.As(err, &typed) || typed.Reason() != test.reason || len(artifacts) != 0 {
				t.Fatalf("Plan() = %#v, %#v; want %q", artifacts, err, test.reason)
			}
			if test.name == "nonzero result" && typed.ExitCode() != 23 {
				t.Fatalf("exit code = %d", typed.ExitCode())
			}
			if bytes.Contains([]byte(err.Error()), []byte("private")) {
				t.Fatalf("error leaked helper details: %v", err)
			}
			if len(runner.calls) != 0 {
				if _, statErr := os.Stat(runner.calls[0].StagingRoot); statErr != nil {
					t.Fatalf("failed Plan removed caller staging: %v", statErr)
				}
			}
		})
	}
}

func TestPlanRejectsGeneratedAndManualArtifactsOutsideCoreOwnerRoot(t *testing.T) {
	document, rendered, sources := compositionFixture(t)
	for _, role := range []string{"generated", "manual"} {
		t.Run(role, func(t *testing.T) {
			runner := &fixtureRunner{t: t, coreServiceID: "core", rendered: rendered, outsideCoreRole: role}
			artifacts, err := apigo.Plan(context.Background(), document, rendered, optionsFor(runner, []toolchain.EnvVar{{Name: "PATH", Value: "/consumer/bin"}}, sources))
			var typed *apigo.Error
			if !errors.As(err, &typed) || typed.Stage() != "verify" || typed.Reason() != "artifact_invalid" || len(artifacts) != 0 || len(runner.calls) != 1 {
				t.Fatalf("Plan() = %#v, %#v; calls=%d", artifacts, err, len(runner.calls))
			}
		})
	}
}

func TestPlanBuildsManifestAndRuntimeBeforeStagingOrHelper(t *testing.T) {
	runner := &fixtureRunner{t: t, coreServiceID: "core"}
	artifacts, err := apigo.Plan(context.Background(), httpapi.Document{}, nil, optionsFor(runner, []toolchain.EnvVar{{Name: "PATH", Value: "/consumer/bin"}}, nil))
	if len(artifacts) != 0 || len(runner.calls) != 0 {
		t.Fatalf("Plan() = %#v; calls=%d", artifacts, len(runner.calls))
	}
	var typed *apigo.Error
	if !errors.As(err, &typed) || typed.Stage() != "manifest" || typed.Reason() != "document_invalid" {
		t.Fatalf("error = %#v", err)
	}
}

func TestPlanRequiresExactGenerationSourceClosureBeforeHelper(t *testing.T) {
	document, rendered, sources := compositionFixture(t)
	extraRef, err := provenance.RepositoryRef("contracts/unused.api", "type:Unused")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		sources []provenance.Source
	}{
		{name: "missing", sources: append([]provenance.Source(nil), sources[:len(sources)-1]...)},
		{name: "extra", sources: append(append([]provenance.Source(nil), sources...), provenance.Source{Ref: extraRef, Digest: provenance.SHA256([]byte("unused"))})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fixtureRunner{t: t, coreServiceID: "core", rendered: rendered}
			artifacts, err := apigo.Plan(context.Background(), document, rendered, optionsFor(runner, []toolchain.EnvVar{{Name: "PATH", Value: "/consumer/bin"}}, test.sources))
			var typed *apigo.Error
			if !errors.As(err, &typed) || typed.Stage() != "input" || typed.Reason() != "source_closure_invalid" || len(artifacts) != 0 || len(runner.calls) != 0 {
				t.Fatalf("Plan() = %#v, %#v; calls=%d", artifacts, err, len(runner.calls))
			}
		})
	}
}

func TestPlanReturnsRuntimeUnrepresentableBeforeHelperOrArtifacts(t *testing.T) {
	base := rawBoundaryHTTPDocument(t, 1)
	baseSpec, err := httpapi.ManifestSpec(base)
	if err != nil {
		t.Fatal(err)
	}
	baseManifest, err := generationapi.NewManifest(baseSpec)
	if err != nil {
		t.Fatal(err)
	}
	baseContract, err := sdkapi.BuildRuntimeContract(baseManifest)
	if err != nil {
		t.Fatal(err)
	}
	baseJSON, err := baseContract.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	exactPathBytes := 1 + sdkapi.RuntimeContractLimits().RawBytes - len(baseJSON)
	over := rawBoundaryHTTPDocument(t, exactPathBytes+1)
	runner := &fixtureRunner{t: t, coreServiceID: "core"}
	artifacts, err := apigo.Plan(context.Background(), over, nil, optionsFor(runner, []toolchain.EnvVar{{Name: "PATH", Value: "/consumer/bin"}}, over.Sources()))
	var runtimeError *sdkapi.Error
	var adapterError *apigo.Error
	if !errors.As(err, &runtimeError) || runtimeError.Code() != "runtime_contract_unrepresentable" || errors.As(err, &adapterError) || len(artifacts) != 0 || len(runner.calls) != 0 {
		t.Fatalf("Plan() = %#v, %T %#v; calls=%d", artifacts, err, err, len(runner.calls))
	}
}

type fixtureRunner struct {
	t                   *testing.T
	coreServiceID       string
	rendered            []composition.RenderedArtifact
	calls               []toolchain.Request
	rootsCanonical      bool
	executableVersion   string
	failure             error
	oversized           bool
	invalidGo           bool
	noncanonical        bool
	digestMismatch      bool
	inputDigestMismatch bool
	exitCode            int
	outsideCoreRole     string
	skipBehaviorTest    bool
}

func (r *fixtureRunner) Run(ctx context.Context, request toolchain.Request) (toolchain.Result, error) {
	r.t.Helper()
	copyRequest := request
	copyRequest.Args = append([]string(nil), request.Args...)
	copyRequest.Stdin = append([]byte(nil), request.Stdin...)
	r.calls = append(r.calls, copyRequest)
	r.rootsCanonical = true
	for _, root := range []string{request.RepositoryRoot, request.StagingRoot} {
		if canonical, err := filepath.EvalSymlinks(root); err != nil || canonical != root {
			r.rootsCanonical = false
		}
	}
	if err := ctx.Err(); err != nil {
		return toolchain.Result{}, err
	}
	if r.failure != nil {
		return toolchain.Result{}, r.failure
	}
	version := request.Tool.Probe.ExpectedVersion
	if r.executableVersion != "" {
		version = r.executableVersion
	}
	result := toolchain.Result{ToolID: request.Tool.ID, Version: request.Tool.Version, ExecutableVersion: version, ExitCode: r.exitCode}
	if r.oversized {
		result.Stdout = bytes.Repeat([]byte{'x'}, toolchain.MaxStdoutBytes+1)
		return result, nil
	}

	generated := []byte("// Code generated by fixture. DO NOT EDIT.\npackage handler\nfunc AccountGet() {}\n")
	if r.invalidGo {
		generated = []byte("package handler\nfunc (")
	}
	files := []fixtureArtifact{
		{ID: "api-handler-account-get", Path: "backend/core/internal/handler/accountgethandler.go", Role: "generated", Content: generated},
		{ID: "api-logic-account-get", Path: "backend/core/internal/logic/account/getlogic.go", Role: "manual", Content: []byte("package account\nfunc Handle() {}\n")},
		{ID: "api-types", Path: "backend/core/internal/types/types.go", Role: "generated", Content: []byte("// Code generated by fixture. DO NOT EDIT.\npackage types\ntype AccountGetRequest struct { ID string }\n")},
	}
	if r.outsideCoreRole != "" {
		content := []byte("package foreign\nfunc Handle() {}\n")
		if r.outsideCoreRole == "generated" {
			content = []byte("// Code generated by fixture. DO NOT EDIT.\npackage foreign\nfunc Handle() {}\n")
		}
		files = append(files, fixtureArtifact{ID: "foreign-output", Path: "backend/foreign/internal/output.go", Role: r.outsideCoreRole, Content: content})
	}
	for _, value := range r.rendered {
		files = append(files, fixtureArtifact{ID: value.ID, Path: value.Path, Role: "generated", Content: value.Content})
	}
	aggregatePath := "backend/" + r.coreServiceID + "/desc/generated/" + r.coreServiceID + ".generated.api"
	aggregate, err := os.ReadFile(filepath.Join(request.StagingRoot, filepath.FromSlash(aggregatePath)))
	if err == nil {
		files = append(files, fixtureArtifact{ID: "api.aggregate." + r.coreServiceID, Path: aggregatePath, Role: "generated", Content: aggregate})
	} else if !os.IsNotExist(err) {
		r.t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file.Path, ".api") || findRendered(r.rendered, file.Path) {
			continue
		}
		name := filepath.Join(request.StagingRoot, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(name, file.Content, 0o600); err != nil {
			r.t.Fatal(err)
		}
	}
	if !r.invalidGo && !r.skipBehaviorTest {
		r.runStagedBehaviorTest(request.StagingRoot)
	}
	inputDigest := provenance.SHA256(request.Stdin)
	if r.inputDigestMismatch {
		inputDigest = provenance.SHA256([]byte("different input"))
	}
	result.Stdout = fixtureResult(r.t, r.coreServiceID, inputDigest, files)
	if r.noncanonical {
		result.Stdout = append(result.Stdout, '\n')
	}
	if r.digestMismatch {
		name := filepath.Join(request.StagingRoot, filepath.FromSlash(files[0].Path))
		if err := os.WriteFile(name, append(files[0].Content, []byte("changed")...), 0o600); err != nil {
			r.t.Fatal(err)
		}
	}
	return result, nil
}

func findRendered(values []composition.RenderedArtifact, artifactPath string) bool {
	for _, value := range values {
		if value.Path == artifactPath {
			return true
		}
	}
	return false
}

func (r *fixtureRunner) runStagedBehaviorTest(staging string) {
	r.t.Helper()
	repository, err := filepath.Abs("../..")
	if err != nil {
		r.t.Fatal(err)
	}
	module := "module example.com/consumer\n\ngo 1.25.0\n\nrequire github.com/nxnminieye/nexa v0.0.0\nreplace github.com/nxnminieye/nexa => " + filepath.ToSlash(repository) + "\n"
	if err := os.WriteFile(filepath.Join(staging, "go.mod"), []byte(module), 0o600); err != nil {
		r.t.Fatal(err)
	}
	name := filepath.Join(staging, "backend/core/internal/serviceclients/account/apigo_helper_test.go")
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		r.t.Fatal(err)
	}
	testSource := []byte(`package accountclient

import (
  "bytes"
  "net/http/httptest"
  "testing"
  sdkapi "github.com/nxnminieye/nexa/sdk/api"
)

func TestHelperVerifiesRemoteErrorProducer(t *testing.T) {
  mapped, err := ProjectAccountGetError(RPCError{Domain: "account", Code: "not_found", Message: "private"}, RequestContext{RequestID: "request-1", TraceID: "trace-1"})
  if err != nil { t.Fatal(err) }
  recorder := httptest.NewRecorder()
  if err := mapped.WriteHTTP(recorder); err != nil { t.Fatal(err) }
  remote, err := sdkapi.ParseRemoteError(recorder.Body.Bytes())
  if err != nil || recorder.Code != 404 || recorder.Header().Get("Content-Type") != "application/json" || remote.Code() != "account_not_found" { t.Fatalf("mapped = %#v %v", remote, err) }
  independent, _ := sdkapi.NewRemoteError(sdkapi.RemoteErrorSpec{Domain: "api", Code: "account_not_found", Message: "request failed", RequestID: "request-1", TraceID: "trace-1"})
  canonical, _ := independent.CanonicalJSON()
  if !bytes.Equal(recorder.Body.Bytes(), canonical) { t.Fatalf("body = %s, want %s", recorder.Body.Bytes(), canonical) }

  unmapped, err := ProjectAccountGetError(RPCError{Domain: "private", Code: "boom", Message: "private"}, RequestContext{RequestID: "request-2", TraceID: "trace-2"})
  if err != nil { t.Fatal(err) }
  hidden, err := sdkapi.ParseRemoteError(unmapped.Body)
  if err != nil || hidden.Domain() != "internal" || hidden.Code() != "internal" || hidden.Message() != "internal error" { t.Fatalf("unmapped = %#v %v", hidden, err) }

  internal, err := ProjectAccountGetFailure(errors.New("private internal cause"), RequestContext{RequestID: "request-3", TraceID: "trace-3"})
  if err != nil { t.Fatal(err) }
  safe, err := sdkapi.ParseRemoteError(internal.Body)
  if err != nil || safe.Domain() != "internal" || safe.Code() != "internal" || safe.Message() != "internal error" { t.Fatalf("internal = %#v %v", safe, err) }
}
`)
	testSource = bytes.Replace(testSource, []byte("\"testing\""), []byte("\"testing\"\n  \"errors\""), 1)
	if err := os.WriteFile(name, testSource, 0o600); err != nil {
		r.t.Fatal(err)
	}
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = staging
	command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOENV=off", "GOPROXY=off", "GOSUMDB=off")
	output, testErr := command.CombinedOutput()
	removeErr := os.Remove(name)
	if testErr != nil || removeErr != nil {
		r.t.Fatalf("staged go test: %v; remove=%v\n%s", testErr, removeErr, output)
	}
}

type fixtureArtifact struct {
	ID, Path, Role string
	Content        []byte
}

func fixtureResult(t *testing.T, coreServiceID string, inputDigest provenance.Digest, files []fixtureArtifact) []byte {
	t.Helper()
	type artifactWire struct {
		ID     string `json:"id"`
		Path   string `json:"path"`
		Role   string `json:"role"`
		Digest string `json:"digest"`
	}
	type resultWire struct {
		APIVersion    string         `json:"apiVersion"`
		Kind          string         `json:"kind"`
		CoreServiceID string         `json:"coreServiceId"`
		InputDigest   string         `json:"inputDigest"`
		GoTestPassed  bool           `json:"goTestPassed"`
		Artifacts     []artifactWire `json:"artifacts"`
	}
	artifacts := make([]artifactWire, len(files))
	for index, file := range files {
		artifacts[index] = artifactWire{ID: file.ID, Path: file.Path, Role: file.Role, Digest: provenance.SHA256(file.Content).String()}
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].ID < artifacts[j].ID })
	encoded, err := json.Marshal(resultWire{APIVersion: "nexa.dev/api-go-result/v1", Kind: "APIGoResult", CoreServiceID: coreServiceID, InputDigest: inputDigest.String(), GoTestPassed: true, Artifacts: artifacts})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func pinnedTool() toolchain.Tool {
	return toolchain.Tool{
		ID: "consumer.api-go", Version: "v1.0.0", Executable: "/consumer/bin/api-helper",
		Environment: []toolchain.EnvironmentRule{{Name: "PATH", Source: toolchain.EnvironmentHost}},
		Probe:       toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: "goctl 1.9.2"},
	}
}

func optionsFor(runner toolchain.Runner, environment []toolchain.EnvVar, sources []provenance.Source) apigo.Options {
	fixture := runner.(*fixtureRunner)
	repository := fixture.t.TempDir()
	staging := fixture.t.TempDir()
	emit := func(name string, content []byte) error {
		full := filepath.Join(staging, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return err
		}
		return os.WriteFile(full, content, 0o644)
	}
	return apigo.Options{CoreServiceID: "core", RepositoryRoot: repository, StagingRoot: staging, Emit: emit, Tool: pinnedTool(), Runner: runner, Environment: environment, Sources: sources}
}

func testDirectoriesOverlap(left, right string) (bool, error) {
	for _, pair := range [][2]string{{left, right}, {right, left}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err != nil {
			return false, err
		}
		if relative == "." || filepath.IsLocal(relative) {
			return true, nil
		}
	}
	return false, nil
}

func seedOverlapRoots(t *testing.T, repository, staging string) {
	t.Helper()
	for name, root := range map[string]string{"repository-sentinel": repository, "staging-sentinel": staging} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func snapshotTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := make(map[string][]byte)
	if err := filepath.WalkDir(canonical, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(canonical, name)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			snapshot[relative] = nil
			return nil
		}
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		snapshot[relative] = content
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func artifactContent(t *testing.T, staging string, input transaction.ArtifactInput) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(staging, filepath.FromSlash(input.Path)))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func equalArtifacts(left, right []transaction.ArtifactInput) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || left[index].Path != right[index].Path || left[index].Owner != right[index].Owner || left[index].Digest != right[index].Digest || !reflect.DeepEqual(left[index].Sources, right[index].Sources) || left[index].StalePolicy != right[index].StalePolicy || left[index].CreateManual != right[index].CreateManual || (left[index].Probe == nil) != (right[index].Probe == nil) {
			return false
		}
	}
	return true
}

func compositionFixture(t *testing.T) (httpapi.Document, []composition.RenderedArtifact, []provenance.Source) {
	t.Helper()
	catalogSource := fmt.Sprintf("apiVersion: nexa.dev/service-catalog/v1\nkind: ServiceCatalog\nservices:\n  - id: core\n    root: backend/core\n    capabilityBindings: []\n  - id: account\n    root: backend/account\n    capabilityBindings:\n      - id: %s\n        apiVersion: %s\n", composition.CapabilityID, composition.CapabilityVersion)
	catalog, err := servicecatalog.Parse("project/services.yaml", []byte(catalogSource))
	if err != nil {
		t.Fatal(err)
	}
	protocolSource := `syntax = "proto3";
package account.v1;
import "nexa/protocol/v1/options.proto";
message GetAccountRequest { string id = 1; int64 tenant_id = 2; string request_id = 3; string trace_id = 4; }
message GetAccountResponse { string name = 1; }
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
      response_fields: { rpc_field: "name" http_field: "name" }
      errors: { match: { domain: "account" code: "not_found" } project: { domain: "api" code: "account_not_found" http_status: 404 } }
    };
  }
}`
	resolver := protocolResolver(func(_ context.Context, path string) (io.ReadCloser, error) {
		if path != "account/v1/account.proto" {
			return nil, os.ErrNotExist
		}
		return io.NopCloser(strings.NewReader(protocolSource)), nil
	})
	proto, err := protocol.Compile(context.Background(), protocol.CompileOptions{ServiceID: "account", EntryFiles: []string{"account/v1/account.proto"}, Resolver: resolver})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	nativeSource := "syntax = \"v1\"\ninfo (nexaContractVersion: \"nexa.dev/http-api/v1\")\ntype HealthRequest {}\ntype HealthResponse { OK bool }\n@server (nexaOperationId: \"health.get\" nexaAuthMode: \"none\")\nservice core-api { @handler health get /health (HealthRequest) returns (HealthResponse) }"
	if err := os.WriteFile(filepath.Join(root, "core.api"), []byte(nativeSource), 0o600); err != nil {
		t.Fatal(err)
	}
	native, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "core.api"})
	if err != nil {
		t.Fatal(err)
	}
	compositionDocument, err := composition.Build(catalog, []protocol.Document{proto}, native, composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := composition.GeneratedAPI(compositionDocument)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := httpapi.Merge(native, generated)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := composition.Render(compositionDocument, composition.RenderOptions{CoreRoot: "backend/core"})
	if err != nil {
		t.Fatal(err)
	}
	byRef := make(map[string]provenance.Source)
	for _, source := range merged.Sources() {
		byRef[source.Ref.String()] = source
	}
	for _, value := range rendered {
		for _, ref := range value.Sources {
			if _, exists := byRef[ref.String()]; exists {
				continue
			}
			if source, ok := proto.Source(ref); ok {
				byRef[ref.String()] = source
				continue
			}
			if source, ok := catalog.Source(ref); ok {
				byRef[ref.String()] = source
				continue
			}
			t.Fatalf("rendered source %q has no owner", ref.String())
		}
	}
	sources := make([]provenance.Source, 0, len(byRef))
	for _, source := range byRef {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Ref.String() < sources[j].Ref.String() })
	return merged, rendered, sources
}

func rawBoundaryHTTPDocument(t *testing.T, pathBytes int) httpapi.Document {
	t.Helper()
	if pathBytes < 1 {
		t.Fatalf("path bytes = %d", pathBytes)
	}
	typeRef, err := provenance.RepositoryRef("contracts/runtime-boundary.api", "type:BoundaryRequest")
	if err != nil {
		t.Fatal(err)
	}
	operationRef, err := provenance.RepositoryRef("contracts/runtime-boundary.api", "operation:boundary.call")
	if err != nil {
		t.Fatal(err)
	}
	sources := []provenance.Source{
		{Ref: typeRef, Digest: provenance.SHA256([]byte("boundary request"))},
		{Ref: operationRef, Digest: provenance.SHA256([]byte("boundary operation"))},
	}
	owner, err := httpapi.NewGeneratedProvenance(sources)
	if err != nil {
		t.Fatal(err)
	}
	document, err := httpapi.NewGeneratedDocument(httpapi.GeneratedDocumentSpec{
		Types: []httpapi.GeneratedTypeSpec{{Name: "BoundaryRequest", Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Fields: []httpapi.GeneratedFieldSpec{}, Provenance: owner}},
		Operations: []httpapi.GeneratedOperationSpec{{
			ID: "boundary.call", Method: generationapi.MethodGET, Path: "/" + strings.Repeat("a", pathBytes-1),
			RequestType: "BoundaryRequest", ResponseBody: generationapi.ResponseBodyNone,
			Auth: httpapi.AuthSpec{Mode: generationapi.AuthNone}, ErrorProjections: []generationapi.ErrorProjectionSpec{}, Provenance: owner,
		}},
	})
	if err != nil {
		t.Fatalf("NewGeneratedDocument(%d): %v", pathBytes, err)
	}
	return document
}

type protocolResolver func(context.Context, string) (io.ReadCloser, error)

func (f protocolResolver) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	return f(ctx, path)
}

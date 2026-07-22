package rpcgo_test

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
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/rpcgo"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
)

func TestPlanRunsPinnedGeneratorAndVerifierInIsolatedStaging(t *testing.T) {
	document := compileProtocol(t)
	wantInput, err := protocol.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fixtureRunner{t: t, serviceID: "account"}
	environment := []toolchain.EnvVar{{Name: "HOME", Value: "/consumer/home"}}
	artifacts, err := rpcgo.Plan(context.Background(), document, rpcOptions(t, runner, environment))
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d", len(runner.calls))
	}
	generate := runner.calls[0]
	if !reflect.DeepEqual(generate.Args, []string{"generate", "--service", "account"}) {
		t.Fatalf("argv = %#v", generate.Args)
	}
	if overlap, overlapErr := testDirectoriesOverlap(generate.RepositoryRoot, generate.StagingRoot); overlapErr != nil || overlap || generate.WorkDir != generate.StagingRoot || generate.Scratch != nil {
		t.Fatalf("staging request = %#v", generate)
	}
	if !runner.rootsCanonical {
		t.Fatal("runner received noncanonical roots")
	}
	if !bytes.Equal(generate.Stdin, wantInput) {
		t.Fatalf("stdin = %s", generate.Stdin)
	}
	if !reflect.DeepEqual(generate.Environment, environment) || environment[0].Value != "/consumer/home" {
		t.Fatalf("environment = %#v; caller = %#v", generate.Environment, environment)
	}
	if _, err := os.Stat(generate.StagingRoot); err != nil {
		t.Fatalf("caller staging removed after Plan: %v", err)
	}
	if _, err := os.Stat(generate.RepositoryRoot); err != nil {
		t.Fatalf("caller repository removed after Plan: %v", err)
	}
	if got := artifactSummary(artifacts); !reflect.DeepEqual(got, []string{
		"account-logic|internal/logic/get.go|nexa.dev/generator/rpc-go-manual/v1|retain",
		"account-proto|generated/account.proto|nexa.dev/generator/rpc-go/v1|delete-if-unmodified",
		"account-rpc|internal/pb/account.pb.go|nexa.dev/generator/rpc-go/v1|delete-if-unmodified",
	}) {
		t.Fatalf("artifacts = %#v", got)
	}
	wantRefs := sourceRefs(document.Sources())
	inputDigest, err := artifact.ComputeInputDigest(artifact.GeneratorSpec{ID: "rpc-go", Version: "v1.0.0"}, document.Sources())
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range artifacts {
		content, readErr := os.ReadFile(filepath.Join(generate.StagingRoot, filepath.FromSlash(value.Path)))
		if readErr != nil || len(content) == 0 || value.Digest != provenance.SHA256(content) || !reflect.DeepEqual(value.Sources, wantRefs) {
			t.Fatalf("artifact = %#v", value)
		}
		if value.Owner == "nexa.dev/generator/rpc-go/v1" {
			if value.Probe == nil || value.CreateManual {
				t.Fatalf("generated artifact %q has no ownership probe", value.ID)
			}
		} else if value.Probe != nil || !value.CreateManual {
			t.Fatalf("manual artifact %q lacks create-once classification", value.ID)
		}
		if value.Probe != nil {
			expected := transaction.Ownership{GeneratorID: "rpc-go", ArtifactID: value.ID, InputDigest: inputDigest}
			owned, inspectErr := value.Probe.Inspect(value.Path, content, expected)
			if inspectErr != nil || !owned {
				t.Fatalf("ownership probe = %v, %v", owned, inspectErr)
			}
			expected.GeneratorID = "other-generator"
			if owned, _ := value.Probe.Inspect(value.Path, content, expected); owned {
				t.Fatal("ownership probe adopted another generator")
			}
			expected.GeneratorID, expected.InputDigest = "rpc-go", provenance.SHA256([]byte("different input"))
			if owned, _ := value.Probe.Inspect(value.Path, content, expected); owned {
				t.Fatal("ownership probe ignored input digest")
			}
			if filepath.Ext(value.Path) == ".proto" {
				expected.InputDigest = inputDigest
				imported := []byte("syntax = \"proto3\"; package account.v1; import \"other.proto\"; message Generated { Other value = 1; }\n")
				if owned, inspectErr := value.Probe.Inspect(value.Path, imported, expected); inspectErr != nil || owned {
					t.Fatalf("changed Proto ownership = %v, %v", owned, inspectErr)
				}
			}
		}
	}

	secondRunner := &fixtureRunner{t: t, serviceID: "account"}
	second, err := rpcgo.Plan(context.Background(), document, rpcOptions(t, secondRunner, nil))
	if err != nil || !reflect.DeepEqual(artifactSummary(second), artifactSummary(artifacts)) {
		t.Fatalf("second Plan = %#v, %v", second, err)
	}
	for index := range artifacts {
		firstContent, firstErr := os.ReadFile(filepath.Join(generate.StagingRoot, filepath.FromSlash(artifacts[index].Path)))
		secondContent, secondErr := os.ReadFile(filepath.Join(secondRunner.calls[0].StagingRoot, filepath.FromSlash(second[index].Path)))
		if firstErr != nil || secondErr != nil || !bytes.Equal(firstContent, secondContent) {
			t.Fatal("Plan output depends on staging path")
		}
	}
}

func TestPlanCanonicalizesRepositoryAndStagingRootsBeforeRunner(t *testing.T) {
	document := compileProtocol(t)
	runner := &fixtureRunner{t: t, serviceID: "account"}
	options := rpcOptions(t, runner, nil)
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
	if _, err := rpcgo.Plan(context.Background(), document, options); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 || runner.calls[0].RepositoryRoot != canonicalRepository || runner.calls[0].StagingRoot != canonicalStaging || runner.calls[0].WorkDir != canonicalStaging {
		t.Fatalf("runner roots = %#v, want repository=%q staging/workdir=%q", runner.calls, canonicalRepository, canonicalStaging)
	}
}

func TestPlanRejectsMissingRepositoryBeforeRunner(t *testing.T) {
	document := compileProtocol(t)
	runner := &fixtureRunner{t: t, serviceID: "account"}
	options := rpcOptions(t, runner, nil)
	options.RepositoryRoot = filepath.Join(t.TempDir(), "missing")
	_, err := rpcgo.Plan(context.Background(), document, options)
	var typed *rpcgo.Error
	if !errors.As(err, &typed) || typed.Stage() != "input" || typed.Reason() != "request_invalid" || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Plan() error = %#v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner called with missing repository: %#v", runner.calls)
	}
}

func TestPlanRejectsInvalidStagingBeforeRunner(t *testing.T) {
	document := compileProtocol(t)
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
			runner := &fixtureRunner{t: t, serviceID: "account"}
			options := rpcOptions(t, runner, nil)
			options.StagingRoot = test.prepare(t)
			_, err := rpcgo.Plan(context.Background(), document, options)
			var typed *rpcgo.Error
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
	document := compileProtocol(t)
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
			runner := &fixtureRunner{t: t, serviceID: "account"}
			options := rpcOptions(t, runner, nil)
			repository, staging, observedRoot := test.prepare(t)
			seedOverlapRoots(t, repository, staging)
			before := snapshotTree(t, observedRoot)
			options.RepositoryRoot = repository
			options.StagingRoot = staging

			_, err := rpcgo.Plan(context.Background(), document, options)
			var typed *rpcgo.Error
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
	document := compileProtocol(t)
	runner := &fixtureRunner{t: t, serviceID: "account"}
	options := rpcOptions(t, runner, nil)
	options.StagingRoot = t.TempDir()
	if err := os.MkdirAll(filepath.Join(options.StagingRoot, ".nexa-env", "gocache"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.StagingRoot, ".nexa-env", "gocache", "scratch.go"), []byte("package scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	emitted := map[string][]byte{}
	options.Emit = func(name string, content []byte) error {
		emitted[name] = append([]byte(nil), content...)
		return nil
	}
	artifacts, err := rpcgo.Plan(context.Background(), document, options)
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

func TestInvalidRPCOutputClosesTransactionCandidateWithoutEmit(t *testing.T) {
	document := compileProtocol(t)
	repository := t.TempDir()
	runner := &fixtureRunner{t: t, serviceID: "account", invalidGo: true}
	options := rpcOptions(t, runner, nil)
	options.RepositoryRoot = repository
	options.StagingRoot = t.TempDir()
	emitCount := 0
	_, err := transaction.Build(context.Background(), repository, func(_ string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		options.Emit = func(name string, content []byte) error {
			emitCount++
			return emit(name, content)
		}
		_, planErr := rpcgo.Plan(context.Background(), document, options)
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
	ref, err := provenance.RepositoryRef("account.proto", "")
	if err != nil {
		t.Fatal(err)
	}
	sources := []provenance.Source{{Ref: ref, Digest: provenance.SHA256([]byte("account contract"))}}
	generatedGo := []byte("// Code generated by fixture. DO NOT EDIT.\npackage accountpb\ntype Account struct{}\n")
	generatedProto := []byte("syntax = \"proto3\"; package account.v1; message Account {}\n")
	previous, err := artifact.NewManifest(artifact.ManifestSpec{
		Generator: artifact.GeneratorSpec{ID: "rpc-go", Version: "v1.0.0"},
		Sources:   sources,
		Artifacts: []artifact.ArtifactSpec{
			{ID: "account-proto", Path: "generated/account.proto", Owner: "rpc-go", Digest: provenance.SHA256(generatedProto), Sources: []provenance.SourceRef{ref}, StalePolicy: artifact.StaleDeleteIfUnmodified},
			{ID: "account-rpc", Path: "internal/pb/account.pb.go", Owner: "rpc-go", Digest: provenance.SHA256(generatedGo), Sources: []provenance.SourceRef{ref}, StalePolicy: artifact.StaleDeleteIfUnmodified},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	probes := rpcgo.StaleOwnershipProbes(previous)
	if len(probes) != 2 {
		t.Fatalf("stale probes = %d", len(probes))
	}
	for id, fixture := range map[string]struct {
		path    string
		content []byte
	}{
		"account-proto": {path: "generated/account.proto", content: generatedProto},
		"account-rpc":   {path: "internal/pb/account.pb.go", content: generatedGo},
	} {
		expected := transaction.Ownership{GeneratorID: "rpc-go", ArtifactID: id, InputDigest: previous.InputDigest()}
		if !anyProbeOwns(t, probes, fixture.path, fixture.content, expected) {
			t.Fatalf("previous artifact %q is not owned", id)
		}
		if anyProbeOwns(t, probes, fixture.path, append(append([]byte(nil), fixture.content...), '\n'), expected) {
			t.Fatalf("changed previous artifact %q was adopted", id)
		}
	}
}

func anyProbeOwns(t *testing.T, probes []transaction.OwnershipProbe, name string, content []byte, expected transaction.Ownership) bool {
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

func TestPlanRejectsToolFailuresBeforePublishingArtifacts(t *testing.T) {
	document := compileProtocol(t)
	tests := []struct {
		name, reason string
		wantExit     int
		prepare      func(*fixtureRunner, *context.Context)
	}{
		{name: "version mismatch", reason: "tool_result_invalid", prepare: func(r *fixtureRunner, _ *context.Context) { r.executableVersion = "goctl 0.0.0" }},
		{name: "runner failure", reason: "tool_failed", prepare: func(r *fixtureRunner, _ *context.Context) { r.failure = errors.New("private tool failure") }},
		{name: "stdout limit", reason: "result_output_limit", prepare: func(r *fixtureRunner, _ *context.Context) { r.oversized = true }},
		{name: "noncanonical result", reason: "result_invalid", prepare: func(r *fixtureRunner, _ *context.Context) { r.noncanonical = true }},
		{name: "artifact digest", reason: "artifact_invalid", prepare: func(r *fixtureRunner, _ *context.Context) { r.digestMismatch = true }},
		{name: "environment scratch artifact", reason: "artifact_invalid", prepare: func(r *fixtureRunner, _ *context.Context) { r.environmentArtifact = true }},
		{name: "input digest", reason: "result_invalid", prepare: func(r *fixtureRunner, _ *context.Context) { r.inputDigestMismatch = true }},
		{name: "nonzero result", reason: "tool_failed", wantExit: 23, prepare: func(r *fixtureRunner, _ *context.Context) { r.exitCode = 23 }},
		{name: "canceled", reason: "operation_canceled", prepare: func(_ *fixtureRunner, selected *context.Context) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			*selected = ctx
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fixtureRunner{t: t, serviceID: "account"}
			ctx := context.Context(context.Background())
			test.prepare(runner, &ctx)
			artifacts, err := rpcgo.Plan(ctx, document, rpcOptions(t, runner, nil))
			var typed *rpcgo.Error
			if !errors.As(err, &typed) || typed.Reason() != test.reason || typed.ExitCode() != test.wantExit || len(artifacts) != 0 {
				t.Fatalf("Plan() = %#v, %#v; want %q", artifacts, err, test.reason)
			}
			if bytes.Contains([]byte(err.Error()), []byte("private")) {
				t.Fatalf("error leaked tool details: %v", err)
			}
			if len(runner.calls) != 0 {
				if _, statErr := os.Stat(runner.calls[0].StagingRoot); statErr != nil {
					t.Fatalf("failed Plan removed caller staging: %v", statErr)
				}
			}
		})
	}
}

func TestPlanRejectsUnparseableGeneratedGoBeforeVerification(t *testing.T) {
	runner := &fixtureRunner{t: t, serviceID: "account", invalidGo: true}
	artifacts, err := rpcgo.Plan(context.Background(), compileProtocol(t), rpcOptions(t, runner, nil))
	var typed *rpcgo.Error
	if !errors.As(err, &typed) || typed.Reason() != "artifact_invalid" || len(artifacts) != 0 || len(runner.calls) != 1 {
		t.Fatalf("Plan() = %#v, %#v; calls=%d", artifacts, err, len(runner.calls))
	}
}

type fixtureRunner struct {
	t                   *testing.T
	serviceID           string
	calls               []toolchain.Request
	executableVersion   string
	failure             error
	oversized           bool
	invalidGo           bool
	environmentArtifact bool
	noncanonical        bool
	digestMismatch      bool
	inputDigestMismatch bool
	exitCode            int
	rootsCanonical      bool
}

func (r *fixtureRunner) Run(ctx context.Context, request toolchain.Request) (toolchain.Result, error) {
	r.t.Helper()
	copyRequest := request
	copyRequest.Args = append([]string(nil), request.Args...)
	copyRequest.Stdin = append([]byte(nil), request.Stdin...)
	copyRequest.Environment = append([]toolchain.EnvVar(nil), request.Environment...)
	r.calls = append(r.calls, copyRequest)
	r.rootsCanonical = true
	for _, root := range []string{request.RepositoryRoot, request.StagingRoot} {
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil || canonical != root {
			r.rootsCanonical = false
		}
	}
	if len(request.Environment) != 0 {
		request.Environment[0].Value = "runner-mutated"
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
	if r.exitCode != 0 {
		return result, nil
	}
	if r.oversized {
		result.Stdout = bytes.Repeat([]byte{'x'}, toolchain.MaxStdoutBytes+1)
		return result, nil
	}
	generatedGo := []byte("// Code generated by fixture. DO NOT EDIT.\npackage accountpb\ntype Request struct{}\n")
	if r.invalidGo {
		generatedGo = []byte("package accountpb\ntype")
	}
	files := []fixtureArtifact{
		{ID: "account-logic", Path: "internal/logic/get.go", Role: "manual", Content: []byte("package logic\nimport accountpb \"example.invalid/nexa/rpc/account/internal/pb\"\nfunc Handle() accountpb.Request { return accountpb.Request{} }\n")},
		{ID: "account-proto", Path: "generated/account.proto", Role: "generated", Content: []byte("syntax = \"proto3\"; package account.v1; message Generated {}\n")},
		{ID: "account-rpc", Path: "internal/pb/account.pb.go", Role: "generated", Content: generatedGo},
	}
	if r.environmentArtifact {
		files = append(files, fixtureArtifact{ID: "environment-scratch", Path: ".nexa-env/gocache/scratch.go", Role: "generated", Content: generatedGo})
	}
	for _, file := range files {
		path := filepath.Join(request.StagingRoot, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(path, file.Content, 0o600); err != nil {
			r.t.Fatal(err)
		}
	}
	if !r.invalidGo {
		command := exec.Command("go", "test", "./...")
		command.Dir = request.StagingRoot
		command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOENV=off", "GOPROXY=off", "GOSUMDB=off")
		if output, err := command.CombinedOutput(); err != nil {
			return toolchain.Result{}, fmt.Errorf("staged go test: %w: %s", err, output)
		}
	}
	inputDigest := provenance.SHA256(request.Stdin)
	if r.inputDigestMismatch {
		inputDigest = provenance.SHA256([]byte("different input"))
	}
	result.Stdout = fixtureResult(r.t, r.serviceID, inputDigest, files)
	if r.noncanonical {
		result.Stdout = append(result.Stdout, '\n')
	}
	if r.digestMismatch {
		path := filepath.Join(request.StagingRoot, filepath.FromSlash(files[0].Path))
		if err := os.WriteFile(path, append(files[0].Content, []byte("changed")...), 0o600); err != nil {
			r.t.Fatal(err)
		}
	}
	return result, nil
}

type fixtureArtifact struct {
	ID, Path, Role string
	Content        []byte
}

func fixtureResult(t *testing.T, serviceID string, inputDigest provenance.Digest, files []fixtureArtifact) []byte {
	t.Helper()
	type artifactWire struct {
		ID     string `json:"id"`
		Path   string `json:"path"`
		Role   string `json:"role"`
		Digest string `json:"digest"`
	}
	type resultWire struct {
		APIVersion   string         `json:"apiVersion"`
		Kind         string         `json:"kind"`
		ServiceID    string         `json:"serviceId"`
		InputDigest  string         `json:"inputDigest"`
		GoTestPassed bool           `json:"goTestPassed"`
		Artifacts    []artifactWire `json:"artifacts"`
	}
	artifacts := make([]artifactWire, len(files))
	for index, file := range files {
		artifacts[index] = artifactWire{ID: file.ID, Path: file.Path, Role: file.Role, Digest: provenance.SHA256(file.Content).String()}
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].ID < artifacts[j].ID })
	encoded, err := json.Marshal(resultWire{APIVersion: "nexa.dev/rpc-go-result/v1", Kind: "RPCGoResult", ServiceID: serviceID, InputDigest: inputDigest.String(), GoTestPassed: true, Artifacts: artifacts})
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
	return toolchain.Tool{ID: "consumer.rpc-go", Version: "v1.0.0", Executable: "/consumer/bin/rpc-helper", Environment: []toolchain.EnvironmentRule{{Name: "HOME", Source: toolchain.EnvironmentHost}}, Probe: toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: "goctl 1.9.2"}}
}

func rpcOptions(t *testing.T, runner toolchain.Runner, environment []toolchain.EnvVar) rpcgo.Options {
	t.Helper()
	repository := t.TempDir()
	staging := t.TempDir()
	return rpcgo.Options{
		ServiceID: "account", RepositoryRoot: repository, StagingRoot: staging,
		Emit: func(name string, content []byte) error {
			full := filepath.Join(staging, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
				return err
			}
			return os.WriteFile(full, content, 0o644)
		},
		Tool: pinnedTool(), Runner: runner, Environment: environment,
	}
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

func compileProtocol(t *testing.T) protocol.Document {
	t.Helper()
	source := `syntax = "proto3";
package account.v1;
message GetRequest { string id = 1; }
message GetResponse { string name = 1; }
service AccountService { rpc Get(GetRequest) returns (GetResponse); }`
	resolver := resolverFunc(func(_ context.Context, path string) (io.ReadCloser, error) {
		if path != "account/v1/account.proto" {
			return nil, os.ErrNotExist
		}
		return io.NopCloser(bytes.NewBufferString(source)), nil
	})
	document, err := protocol.Compile(context.Background(), protocol.CompileOptions{ServiceID: "account", EntryFiles: []string{"account/v1/account.proto"}, Resolver: resolver})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

type resolverFunc func(context.Context, string) (io.ReadCloser, error)

func (f resolverFunc) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	return f(ctx, path)
}

func artifactSummary(values []transaction.ArtifactInput) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ID + "|" + value.Path + "|" + value.Owner + "|" + string(value.StalePolicy)
	}
	return result
}

func sourceRefs(values []provenance.Source) []provenance.SourceRef {
	result := make([]provenance.SourceRef, len(values))
	for index, value := range values {
		result[index] = value.Ref
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}

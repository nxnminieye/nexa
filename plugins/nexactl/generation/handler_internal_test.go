package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/provenance"
)

func TestInvocationRunnerScopesScratchEnvironmentToDelegatedStaging(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(base, "repository")
	staging := filepath.Join(base, "staging")
	outside := filepath.Join(base, "handler-environment")
	for _, directory := range []string{repository, staging, outside} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	next := &internalRecordingRunner{}
	request := toolchain.Request{
		RepositoryRoot: repository,
		StagingRoot:    staging,
		WorkDir:        staging,
		Tool: toolchain.Tool{
			ID: "helper", Version: "v1", Executable: "/usr/bin/true",
			Environment: []toolchain.EnvironmentRule{
				{Name: "PATH", Source: toolchain.EnvironmentHost},
				{Name: "HOME", Source: toolchain.EnvironmentScratch},
				{Name: "TMPDIR", Source: toolchain.EnvironmentScratch},
				{Name: "GOENV", Source: toolchain.EnvironmentFixed, FixedValue: "off"},
			},
			Probe: toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: "helper v1"},
		},
		Environment: []toolchain.EnvVar{
			{Name: "PATH", Value: "/usr/bin"},
			{Name: "HOME", Value: filepath.Join(outside, "home")},
			{Name: "TMPDIR", Value: filepath.Join(outside, "tmpdir")},
			{Name: "GOENV", Value: "off"},
		},
	}
	if _, err := (invocationRunner{next: next}).Run(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(next.requests) != 1 {
		t.Fatalf("delegated requests = %d", len(next.requests))
	}
	values := map[string]string{}
	for _, value := range next.requests[0].Environment {
		values[value.Name] = value.Value
	}
	if values["PATH"] != "/usr/bin" || values["GOENV"] != "off" {
		t.Fatalf("non-scratch environment changed: %#v", values)
	}
	for _, name := range []string{"HOME", "TMPDIR"} {
		value := values[name]
		relative, err := filepath.Rel(staging, value)
		if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
			t.Fatalf("%s = %q, want child of %q", name, value, staging)
		}
		if info, err := os.Stat(value); err != nil || !info.IsDir() {
			t.Fatalf("%s scratch directory = %q: %v", name, value, err)
		}
	}
}

func TestDisabledMultiTenantRejectsTenantMixin(t *testing.T) {
	err := validateCRUDMultiTenant(ServiceProject{}, true)
	projected := protocol.Project(err)
	var details errorDetails
	if decodeErr := decodeCanonicalDetails(projected.Details, &details); decodeErr != nil || projected.Code != "fact_source_invalid" || details.Reason != "multi_tenant_disabled" || details.Pointer != "/project/services/multiTenant/enabled" {
		t.Fatalf("error = %#v details = %#v decode = %v", projected, details, decodeErr)
	}
}

func TestProjectBuildErrorPreservesGenerationErrorsAndProjectsUnknownOwners(t *testing.T) {
	t.Run("generation error", func(t *testing.T) {
		generationErr := inputError("fact_source_invalid", "provider", "multi_tenant_disabled", "/project/services/multiTenant/enabled", "")
		cleanupErr := errors.New("remove /private/tmp/nexa-staging-secret: permission denied")
		joined := errors.Join(generationErr, cleanupErr)
		result := projectBuildError(joined)
		if result.Error() != generationErr.Error() || strings.Contains(result.Error(), "/private/tmp/nexa-staging-secret") {
			t.Fatalf("public error = %q, want stable generation message %q", result.Error(), generationErr.Error())
		}
		if !errors.Is(result, joined) || !errors.Is(result, generationErr) || !errors.Is(result, cleanupErr) {
			t.Fatalf("error chain does not preserve joined causes: %v", result)
		}
		projected := protocol.Project(result)
		var details errorDetails
		if decodeErr := decodeCanonicalDetails(projected.Details, &details); decodeErr != nil || projected.Code != "fact_source_invalid" || projected.Domain != errorDomain || projected.Category != protocol.CategoryInput || details.Reason != "multi_tenant_disabled" || details.Pointer != "/project/services/multiTenant/enabled" {
			t.Fatalf("error = %#v details = %#v decode = %v", projected, details, decodeErr)
		}
	})

	t.Run("unknown owner", func(t *testing.T) {
		projected := protocol.Project(projectBuildError(errors.New("unknown owner error")))
		var details errorDetails
		if decodeErr := decodeCanonicalDetails(projected.Details, &details); decodeErr != nil || projected.Code != "internal_error" || projected.Domain != errorDomain || projected.Category != protocol.CategoryInternal || details.Reason != "owner_error_unrecognized" {
			t.Fatalf("error = %#v details = %#v decode = %v", projected, details, decodeErr)
		}
	})
}

type internalRecordingRunner struct {
	requests []toolchain.Request
}

func (runner *internalRecordingRunner) Run(_ context.Context, request toolchain.Request) (toolchain.Result, error) {
	runner.requests = append(runner.requests, request)
	return toolchain.Result{ToolID: "helper", Version: "v1", ExecutableVersion: "helper v1"}, nil
}

func TestLoadProtoPackageFactsUsesFormalDescriptor(t *testing.T) {
	repository := t.TempDir()
	source := []byte(`syntax = "proto3";
package acme.account.v1;
option go_package =
  "example.com/acme/account/v1;accountv1";
message Account {}
`)
	if err := os.WriteFile(filepath.Join(repository, "account.proto"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(repository)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	facts, err := loadProtoPackageFacts(root, "account.proto")
	if err != nil {
		t.Fatalf("load facts: %#v", protocol.Project(err))
	}
	if facts.protoPackage != "acme.account.v1" || facts.goPackage != "example.com/acme/account/v1;accountv1" {
		t.Fatalf("facts = %#v", facts)
	}
}

func TestLoadProtoPackageFactsRejectsInvalidOrIncompleteEntry(t *testing.T) {
	tests := []struct {
		name, source, reason string
	}{
		{name: "syntax", source: `syntax = "proto3"; package`, reason: "proto_entry_invalid"},
		{name: "package", source: `syntax = "proto3"; option go_package = "example.com/acme/account/v1;accountv1";`, reason: "proto_package_missing"},
		{name: "go package", source: `syntax = "proto3"; package acme.account.v1;`, reason: "proto_go_package_missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := t.TempDir()
			if err := os.WriteFile(filepath.Join(repository, "account.proto"), []byte(test.source), 0o600); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(repository)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			_, err = loadProtoPackageFacts(root, "account.proto")
			projected := protocol.Project(err)
			var details errorDetails
			if decodeErr := decodeCanonicalDetails(projected.Details, &details); decodeErr != nil || details.Reason != test.reason {
				t.Fatalf("error = %#v, details = %#v, decode = %v", projected, details, decodeErr)
			}
		})
	}
}

func TestOfficialEntGenerateArgsUseConsumerModuleAndScratchModfile(t *testing.T) {
	base := t.TempDir()
	moduleRoot := filepath.Join(base, "consumer")
	scratchRoot := filepath.Join(base, "scratch")
	schemaRoot := filepath.Join(moduleRoot, "ent", "schema")
	for _, path := range []string{moduleRoot, scratchRoot, schemaRoot} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	args, err := officialEntGenerateArgs(moduleRoot, scratchRoot, schemaRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-C", moduleRoot,
		"run", "-modfile=" + filepath.Join(scratchRoot, "go.mod"), "-mod=mod",
		"entgo.io/ent/cmd/ent", "generate", "./ent/schema",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestBuildCanonicalizesRelativeRepositoryRootBeforeProviderResolution(t *testing.T) {
	repository := t.TempDir()
	relative, err := filepath.Rel(mustWorkingDirectory(t), repository)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	var resolvedRoot string
	provider := callbackProvider{
		descriptor: ProviderDescriptor{ID: "consumer", Version: "v1.0.0"},
		resolve: func(_ context.Context, root string) (Project, error) {
			resolvedRoot = root
			return Project{}, nil
		},
	}
	runner, err := newCommandRunner(Options{Providers: []ProjectProvider{provider}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runner.build(context.Background(), plugin.Invocation{Flags: map[string]any{
		"repo-root": relative,
		"provider":  "consumer",
		"service":   "accounts",
	}})
	projected := protocol.Project(err)
	if projected.Code != "fact_source_missing" || resolvedRoot != canonical {
		t.Fatalf("root=%q want=%q error=%#v", resolvedRoot, canonical, projected)
	}
}

func TestFinalizeCRUDCommandBuildClosesPlanOnPostBuildFailure(t *testing.T) {
	repository := t.TempDir()
	unrelated := filepath.Join(repository, "unrelated")
	invocationRoot := filepath.Join(t.TempDir(), "invocation")
	for _, directory := range []string{unrelated, invocationRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := transaction.Build(context.Background(), repository, func(string, func(string, []byte) error) (transaction.PlanRequest, error) {
		return transaction.PlanRequest{
			Generator:    artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"},
			ManifestPath: ".nexa/generation/crud-proto.accounts.manifest.json",
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(repository, ".nexa-generation-staging-*"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("staging before finalization = %v, %v", matches, err)
	}

	_, err = finalizeCRUDCommandBuild(repository, plan, crudproto.EntGraphPlan{}, true)
	if err == nil {
		t.Fatal("invalid post-build lock state accepted")
	}
	if err := os.RemoveAll(invocationRoot); err != nil {
		t.Fatal(err)
	}
	matches, globErr := filepath.Glob(filepath.Join(repository, ".nexa-generation-staging-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("post-build failure left plan staging = %v, %v", matches, globErr)
	}
	if _, statErr := os.Stat(invocationRoot); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("post-build failure left invocation scratch: %v", statErr)
	}
	if info, statErr := os.Stat(unrelated); statErr != nil || !info.IsDir() {
		t.Fatalf("post-build failure removed unrelated directory: %v", statErr)
	}
}

func TestFinalizeCRUDCommandBuildSafelyProjectsPlanCloseFailure(t *testing.T) {
	repository := t.TempDir()
	var staging string
	plan, err := transaction.Build(context.Background(), repository, func(root string, _ func(string, []byte) error) (transaction.PlanRequest, error) {
		staging = root
		return transaction.PlanRequest{
			Generator:    artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"},
			ManifestPath: ".nexa/generation/crud-proto.accounts.manifest.json",
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(staging, 0o500); err != nil {
		t.Fatal(err)
	}
	_, err = finalizeCRUDCommandBuild(repository, plan, crudproto.EntGraphPlan{}, true)
	if err == nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("finalize error = %#v", err)
	}
	if strings.Contains(err.Error(), staging) {
		t.Fatalf("safe error rendered staging path: %q", err)
	}
	projected := protocol.Project(err)
	encoded, marshalErr := json.Marshal(projected)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if bytes.Contains(encoded, []byte(staging)) {
		t.Fatalf("protocol projection leaked staging path: %s", encoded)
	}
	if chmodErr := os.Chmod(staging, 0o700); chmodErr != nil && !errors.Is(chmodErr, os.ErrNotExist) {
		t.Fatal(chmodErr)
	}
	if removeErr := os.RemoveAll(staging); removeErr != nil {
		t.Fatal(removeErr)
	}
}

func TestProjectOwnerCancellationUsesUniformProtocol(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		reason string
	}{
		{name: "context canceled", err: context.Canceled, reason: "context_canceled"},
		{name: "context deadline", err: context.DeadlineExceeded, reason: "context_deadline_exceeded"},
		{name: "tool canceled", err: closedOwnerError{code: "tool_canceled", stage: "wait", reason: "context_canceled", pointer: "/context"}, reason: "context_canceled"},
		{name: "tool deadline", err: closedOwnerError{code: "tool_deadline_exceeded", stage: "wait", reason: "context_deadline_exceeded", pointer: "/context"}, reason: "context_deadline_exceeded"},
		{name: "transaction canceled", err: canceledTransactionError(t), reason: "cancelled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := projectOwnerError(test.err)
			if !errors.Is(err, test.err) {
				t.Fatalf("projected error did not preserve cause: %#v", err)
			}
			projected := protocol.Project(err)
			if projected.Code != "operation_canceled" || projected.Domain != "nexactl.generation" || projected.Category != protocol.CategoryCanceled || protocol.ExitStatus(err) != 130 {
				t.Fatalf("error = %#v exit=%d", projected, protocol.ExitStatus(err))
			}
			var details errorDetails
			if decodeErr := decodeCanonicalDetails(projected.Details, &details); decodeErr != nil || details.Reason != test.reason {
				t.Fatalf("details=%#v decode=%v", details, decodeErr)
			}
		})
	}
}

func TestProjectOwnerCancellationClassificationPrefersTypedOwner(t *testing.T) {
	wrapped := fmt.Errorf("outer: %w", context.Canceled)
	projected := projectOwnerError(wrapped)
	if !errors.Is(projected, context.Canceled) || protocol.Project(projected).Category != protocol.CategoryCanceled {
		t.Fatalf("wrapped raw cancellation = %#v", protocol.Project(projected))
	}

	typed := typedNestedCancellationError{cause: context.Canceled}
	projected = projectOwnerError(typed)
	if !errors.Is(projected, context.Canceled) {
		t.Fatalf("typed owner lost nested cause: %#v", projected)
	}
	if got := protocol.Project(projected); got.Category == protocol.CategoryCanceled || got.Code != "internal_error" {
		t.Fatalf("typed external owner was hijacked by nested cancellation: %#v", got)
	}
}

type typedNestedCancellationError struct{ cause error }

func (e typedNestedCancellationError) Error() string   { return "typed external failure" }
func (e typedNestedCancellationError) Unwrap() error   { return e.cause }
func (e typedNestedCancellationError) Code() string    { return "external_failed" }
func (e typedNestedCancellationError) Stage() string   { return "external" }
func (e typedNestedCancellationError) Reason() string  { return "tool_failed" }
func (e typedNestedCancellationError) Pointer() string { return "/external" }

func TestReadOptionalRegularRejectsDeterministicPathReplacement(t *testing.T) {
	for _, replaceAt := range []string{"open", "after-open"} {
		t.Run(replaceAt, func(t *testing.T) {
			rootPath := t.TempDir()
			if err := os.WriteFile(filepath.Join(rootPath, "control.json"), []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(rootPath, "replacement.json"), []byte("replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(rootPath)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			reader := &replacingRoot{root: root, rootPath: rootPath, replaceAt: replaceAt}
			data, exists, err := readOptionalRegularFrom(reader, "control.json")
			if len(data) != 0 || exists || err == nil {
				t.Fatalf("data=%q exists=%v error=%v", data, exists, err)
			}
			projected := protocol.Project(err)
			var details errorDetails
			if decodeErr := decodeCanonicalDetails(projected.Details, &details); projected.Code != "fact_source_invalid" || decodeErr != nil || details.Reason != "control_source_changed" {
				t.Fatalf("error=%#v details=%#v decode=%v", projected, details, decodeErr)
			}
		})
	}
}

func TestProjectTransactionRequestWiresStaleOwnershipProbesOnce(t *testing.T) {
	probe := closedOwnershipProbe{}
	projection := &fakeTransactionProjection{
		sources: []provenance.Source{{Ref: mustGenerationRef(t, "schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}},
		probes:  []transaction.OwnershipProbe{probe},
	}
	request, err := projectTransactionRequest(projection, nil, ".nexa/generation/crud-proto.accounts.manifest.json", func(string, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if projection.inputCalls != 1 || projection.probeCalls != 1 || projection.sourceCalls != 1 {
		t.Fatalf("projection calls = inputs:%d probes:%d sources:%d", projection.inputCalls, projection.probeCalls, projection.sourceCalls)
	}
	if len(request.Expected) != 0 || len(request.ControlSources) != 0 || len(request.StaleOwnershipProbes) != 1 || request.StaleOwnershipProbes[0] == nil {
		t.Fatalf("request = %#v", request)
	}
}

type callbackProvider struct {
	descriptor ProviderDescriptor
	resolve    func(context.Context, string) (Project, error)
}

func (p callbackProvider) Descriptor() ProviderDescriptor { return p.descriptor }
func (p callbackProvider) Resolve(ctx context.Context, root string) (Project, error) {
	return p.resolve(ctx, root)
}

type closedOwnerError struct {
	code, stage, reason, pointer, source string
}

func (e closedOwnerError) Error() string   { return "closed owner error" }
func (e closedOwnerError) Code() string    { return e.code }
func (e closedOwnerError) Stage() string   { return e.stage }
func (e closedOwnerError) Reason() string  { return e.reason }
func (e closedOwnerError) Pointer() string { return e.pointer }
func (e closedOwnerError) Source() string  { return e.source }

type replacingRoot struct {
	root      *os.Root
	rootPath  string
	replaceAt string
	lstats    int
}

type closedOwnershipProbe struct{}

func (closedOwnershipProbe) Inspect(string, []byte, transaction.Ownership) (bool, error) {
	return true, nil
}

type fakeTransactionProjection struct {
	inputCalls, probeCalls, sourceCalls int
	sources                             []provenance.Source
	probes                              []transaction.OwnershipProbe
}

func (p *fakeTransactionProjection) TransactionInputs(func(string, []byte) error) ([]transaction.ArtifactInput, []transaction.ControlSourceMutation, error) {
	p.inputCalls++
	return nil, nil, nil
}

func (p *fakeTransactionProjection) StaleOwnershipProbes() ([]transaction.OwnershipProbe, error) {
	p.probeCalls++
	return append([]transaction.OwnershipProbe(nil), p.probes...), nil
}

func (p *fakeTransactionProjection) Sources() ([]provenance.Source, error) {
	p.sourceCalls++
	return append([]provenance.Source(nil), p.sources...), nil
}

func (r *replacingRoot) Lstat(name string) (os.FileInfo, error) {
	r.lstats++
	if r.replaceAt == "after-open" && r.lstats == 2 {
		if err := r.replace(name); err != nil {
			return nil, err
		}
	}
	return r.root.Lstat(name)
}

func (r *replacingRoot) Open(name string) (*os.File, error) {
	if r.replaceAt == "open" {
		if err := r.replace(name); err != nil {
			return nil, err
		}
	}
	return r.root.Open(name)
}

func (r *replacingRoot) replace(name string) error {
	return os.Rename(filepath.Join(r.rootPath, "replacement.json"), filepath.Join(r.rootPath, name))
}

func mustWorkingDirectory(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func decodeCanonicalDetails(raw []byte, target any) error {
	return json.Unmarshal(raw, target)
}

func canceledTransactionError(t *testing.T) error {
	t.Helper()
	repositoryPath := t.TempDir()
	repository, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	plan, err := transaction.Build(context.Background(), repositoryPath, func(string, func(string, []byte) error) (transaction.PlanRequest, error) {
		return transaction.PlanRequest{
			Generator:    artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"},
			ManifestPath: ".nexa/generation/crud-proto.accounts.manifest.json",
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = transaction.Write(ctx, plan, repositoryPath, transaction.WriteOptions{PlanDigest: plan.PlanDigest()})
	return err
}

func mustGenerationRef(t *testing.T, path, fragment string) provenance.SourceRef {
	t.Helper()
	ref, err := provenance.RepositoryRef(path, fragment)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

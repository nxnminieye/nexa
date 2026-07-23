package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
			{Name: "HOME", Value: filepath.Join(outside, "rpc-1-home")},
			{Name: "TMPDIR", Value: filepath.Join(outside, "rpc-2-tmpdir")},
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
	for name, basename := range map[string]string{"HOME": "rpc-1-home", "TMPDIR": "rpc-2-tmpdir"} {
		value := values[name]
		relative, err := filepath.Rel(staging, value)
		if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
			t.Fatalf("%s = %q, want child of %q", name, value, staging)
		}
		if info, err := os.Stat(value); err != nil || !info.IsDir() {
			t.Fatalf("%s scratch directory = %q: %v", name, value, err)
		}
		if filepath.Base(value) != basename {
			t.Fatalf("%s scratch basename = %q, want %q", name, filepath.Base(value), basename)
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
		{name: "typed nested cancellation", err: typedNestedCancellationError{cause: context.Canceled}, reason: "context_canceled"},
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
	if got := protocol.Project(projected); got.Category != protocol.CategoryCanceled || got.Code != "operation_canceled" || protocol.ExitStatus(projected) != 130 {
		t.Fatalf("typed nested cancellation was not preserved: %#v", got)
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

func TestCRUDPlanCombinesProtoLockHelperAndLogic(t *testing.T) {
	protoSource := provenance.Source{Ref: mustGenerationRef(t, "backend/account/ent/schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	validationSource := provenance.Source{Ref: mustGenerationRef(t, "backend/account/internal/logic", "crudlogic-validation"), Digest: provenance.SHA256([]byte("validation"))}
	proto := &fakeUnifiedProtoProjection{
		inputs:   []transaction.ArtifactInput{{ID: "crud-proto.account", Path: "backend/account/rpc/account.crud.generated.proto", Owner: "nexa.dev/generator/crud-proto/v1", Digest: provenance.SHA256([]byte("proto")), Sources: []provenance.SourceRef{protoSource.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified}},
		controls: []transaction.ControlSourceMutation{mustCompatibilityMutation(t)},
		probes:   []transaction.OwnershipProbe{closedOwnershipProbe{}},
		sources:  []provenance.Source{protoSource},
	}
	logic := &fakeUnifiedLogicProjection{
		inputs: []transaction.ArtifactInput{
			{ID: "crud-logic.account.tenant-helper", Path: "backend/account/internal/logic/crudtenant/tenant.generated.go", Owner: "nexa.dev/generator/crud-logic/v1", Digest: provenance.SHA256([]byte("helper")), Sources: []provenance.SourceRef{protoSource.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified},
			{ID: "crud-logic.account.createaccount", Path: "backend/account/internal/logic/createaccountlogic.go", Owner: "nexa.dev/generator/crud-logic/v1", Digest: provenance.SHA256([]byte("logic")), Sources: []provenance.SourceRef{protoSource.Ref}, StalePolicy: artifact.StaleRetain, CreateManual: true},
		},
		probes: []transaction.OwnershipProbe{closedOwnershipProbe{}},
	}
	request, err := projectUnifiedTransactionRequest(proto, logic, validationSource, nil, ".nexa/generation/crud-proto.account.manifest.json", func(string, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if request.Generator.ID != "crud-proto" || request.ManifestPath != ".nexa/generation/crud-proto.account.manifest.json" || len(request.Expected) != 3 || len(request.ControlSources) != 1 || len(request.StaleOwnershipProbes) != 2 {
		t.Fatalf("request = %#v", request)
	}
	if len(request.Sources) != 2 || request.Sources[1] != validationSource {
		t.Fatalf("sources = %#v", request.Sources)
	}
}

func TestEnabledTenantWithoutCRUDPlansOnlyManagedHelper(t *testing.T) {
	source := provenance.Source{Ref: mustGenerationRef(t, "backend/account/ent/schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	validation := provenance.Source{Ref: mustGenerationRef(t, "backend/account/internal/logic", "crudlogic-validation"), Digest: provenance.SHA256([]byte("validation"))}
	request, err := projectUnifiedTransactionRequest(
		&fakeUnifiedProtoProjection{sources: []provenance.Source{source}},
		&fakeUnifiedLogicProjection{inputs: []transaction.ArtifactInput{{ID: "crud-logic.account.tenant-helper", Path: "backend/account/internal/logic/crudtenant/tenant.generated.go", Owner: "nexa.dev/generator/crud-logic/v1", Digest: provenance.SHA256([]byte("helper")), Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified}}, probes: []transaction.OwnershipProbe{closedOwnershipProbe{}}},
		validation, nil, ".nexa/generation/crud-proto.account.manifest.json", func(string, []byte) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Expected) != 1 || request.Expected[0].CreateManual || request.Expected[0].OverwriteManual || len(request.ControlSources) != 0 {
		t.Fatalf("request = %#v", request)
	}
}

func TestUnifiedCRUDKeepsExistingProtoArtifactAndManifestIdentity(t *testing.T) {
	source := provenance.Source{Ref: mustGenerationRef(t, "backend/account/ent/schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	proto := transaction.ArtifactInput{ID: "crud-proto.account", Path: "backend/account/rpc/account.crud.generated.proto", Owner: "nexa.dev/generator/crud-proto/v1", Digest: provenance.SHA256([]byte("proto")), Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified}
	request, err := projectUnifiedTransactionRequest(&fakeUnifiedProtoProjection{inputs: []transaction.ArtifactInput{proto}, sources: []provenance.Source{source}}, &fakeUnifiedLogicProjection{}, provenance.Source{Ref: mustGenerationRef(t, "backend/account/internal/logic", "crudlogic-validation"), Digest: provenance.SHA256([]byte("validation"))}, nil, ".nexa/generation/crud-proto.account.manifest.json", func(string, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if request.Generator.ID != "crud-proto" || request.Generator.Version != capabilityVersion || request.ManifestPath != ".nexa/generation/crud-proto.account.manifest.json" || len(request.Expected) != 1 || request.Expected[0].ID != proto.ID || request.Expected[0].Owner != proto.Owner {
		t.Fatalf("request = %#v", request)
	}
}

func TestCRUDWriteRejectsOverwriteFlagMismatch(t *testing.T) {
	repository := t.TempDir()
	target := "backend/account/internal/logic/createaccountlogic.go"
	writeGenerationTestFile(t, repository, target, []byte("package logic\n\nfunc customized() {}\n"))

	defaultPlan := buildUnifiedCRUDTestPlan(t, repository, target, false)
	defer defaultPlan.Close()
	overwritePlan := buildUnifiedCRUDTestPlan(t, repository, target, true)
	defer overwritePlan.Close()
	if defaultPlan.PlanDigest() == overwritePlan.PlanDigest() {
		t.Fatal("overwrite mode did not change the unified CRUD plan digest")
	}
	_, err := transaction.Write(context.Background(), overwritePlan, repository, transaction.WriteOptions{PlanDigest: defaultPlan.PlanDigest()})
	var typed *transaction.Error
	if !errors.As(err, &typed) || typed.Reason() != "plan_digest_mismatch" {
		t.Fatalf("Write() error = %#v", err)
	}
}

func TestToolEnvironmentForStagingUsesOnlySelectedToolRules(t *testing.T) {
	staging := t.TempDir()
	runner := &commandRunner{hostEnvironment: []toolchain.EnvVar{{Name: "PATH", Value: "/hermetic/bin"}, {Name: "ENT_ONLY", Value: "ent"}}}
	rpcTool := toolchain.Tool{Environment: []toolchain.EnvironmentRule{
		{Name: "PATH", Source: toolchain.EnvironmentHost},
		{Name: "RPC_CACHE", Source: toolchain.EnvironmentScratch},
		{Name: "RPC_MODE", Source: toolchain.EnvironmentFixed, FixedValue: "fixture"},
	}}
	got, err := runner.toolEnvironmentForStaging(rpcTool, staging, "rpc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != (toolchain.EnvVar{Name: "PATH", Value: "/hermetic/bin"}) || got[2] != (toolchain.EnvVar{Name: "RPC_MODE", Value: "fixture"}) {
		t.Fatalf("RPC environment = %#v", got)
	}
	if strings.Contains(fmt.Sprint(got), "ENT_ONLY") {
		t.Fatalf("Ent-only environment leaked into RPC: %#v", got)
	}
	relative, err := filepath.Rel(staging, got[1].Value)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
		t.Fatalf("RPC scratch = %q, staging = %q", got[1].Value, staging)
	}
	if _, err := os.Lstat(got[1].Value); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("declared RPC scratch was materialized: %v", err)
	}
}

func TestLogicImportsPackageUsesExactSafeGoImports(t *testing.T) {
	repository := t.TempDir()
	logicRoot := "backend/account/internal/logic"
	root := filepath.Join(repository, filepath.FromSlash(logicRoot))
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("unrelated.go", "package logic\nimport _ \"example.com/service/internal/pbextra\"\n")
	if depends, sources, err := logicImportsPackage(repository, logicRoot, "example.com/service/internal/pb"); err != nil || depends || len(sources) != 1 {
		t.Fatalf("unrelated import = %v, %v", depends, err)
	}
	write("crud.go", "package logic\nimport pb \"example.com/service/internal/pb\"\nvar _ = pb.Value{}\n")
	if depends, sources, err := logicImportsPackage(repository, logicRoot, "example.com/service/internal/pb"); err != nil || !depends || len(sources) != 2 {
		t.Fatalf("exact import = %v, %v", depends, err)
	}
	if err := os.Remove(filepath.Join(root, "crud.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "unrelated.go"), filepath.Join(root, "unsafe.go")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := logicImportsPackage(repository, logicRoot, "example.com/service/internal/pb"); err == nil {
		t.Fatal("unsafe logic symlink was accepted")
	}
}

func TestLogicImportsPackageRejectsTraversalAndSymlinkedParents(t *testing.T) {
	repository := t.TempDir()
	outside := t.TempDir()
	for _, logicRoot := range []string{"../outside", outside} {
		if _, _, err := logicImportsPackage(repository, logicRoot, "example.com/service/internal/pb"); err == nil {
			t.Fatalf("unsafe LogicRoot %q was accepted", logicRoot)
		}
	}
	realRoot := filepath.Join(repository, "real-logic")
	if err := os.MkdirAll(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(repository, "backend", "account")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRoot, filepath.Join(parent, "internal")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := logicImportsPackage(repository, "backend/account/internal", "example.com/service/internal/pb"); err == nil {
		t.Fatal("symlinked LogicRoot parent was accepted")
	}
}

func TestLogicImportsPackageRejectsFileReplacement(t *testing.T) {
	for _, replaceAt := range []string{"open", "after-open"} {
		for _, replacement := range []string{"regular", "symlink"} {
			t.Run(replaceAt+"/"+replacement, func(t *testing.T) {
				rootPath := t.TempDir()
				if err := os.WriteFile(filepath.Join(rootPath, "manual.go"), []byte("package logic\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if replacement == "regular" {
					if err := os.WriteFile(filepath.Join(rootPath, "replacement.json"), []byte("package logic\n// replacement\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(filepath.Join(rootPath, "replacement-target.go"), []byte("package logic\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink("replacement-target.go", filepath.Join(rootPath, "replacement.json")); err != nil {
						t.Fatal(err)
					}
				}
				root, err := os.OpenRoot(rootPath)
				if err != nil {
					t.Fatal(err)
				}
				defer root.Close()
				reader := &replacingLogicScanRoot{replacingRoot: replacingRoot{root: root, rootPath: rootPath, replaceAt: replaceAt}}
				if _, _, err := logicImportsPackageFrom(reader, "backend/account/internal/logic", "example.com/service/internal/pb"); err == nil {
					t.Fatal("replaced Logic file was accepted")
				}
			})
		}
	}
}

func TestCRUDRemovalSourcesDetectChangeAfterPlan(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
	}{
		{name: "digest change", content: "package logic\n// changed after plan\n"},
		{name: "protected import added", content: "package logic\nimport _ \"example.com/service/internal/pb\"\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := t.TempDir()
			logicRoot := "backend/account/internal/logic"
			root := filepath.Join(repository, filepath.FromSlash(logicRoot))
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "manual.go")
			if err := os.WriteFile(target, []byte("package logic\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			initial, err := validateCRUDRemoval(repository, logicRoot, "example.com/service/internal/pb;pb")
			if err != nil || len(initial) != 1 {
				t.Fatalf("initial removal sources = %#v, %v", initial, err)
			}
			plan, err := transaction.Build(context.Background(), repository, func(string, func(string, []byte) error) (transaction.PlanRequest, error) {
				return transaction.PlanRequest{
					Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"}, Sources: initial,
					ManifestPath: ".nexa/generation/crud-proto.account.manifest.json",
					RevalidateSources: func(context.Context) ([]provenance.Source, error) {
						return validateCRUDRemoval(repository, logicRoot, "example.com/service/internal/pb;pb")
					},
				}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer plan.Close()
			if err := os.WriteFile(target, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := transaction.Write(context.Background(), plan, repository, transaction.WriteOptions{PlanDigest: plan.PlanDigest()}); err == nil {
				t.Fatal("Logic source change after plan was accepted")
			}
		})
	}
}

func TestCRUDDefaultSkipsExistingLogic(t *testing.T) {
	repository := t.TempDir()
	target := "backend/account/internal/logic/createaccountlogic.go"
	original := []byte("package logic\n\nfunc customized() {}\n")
	writeGenerationTestFile(t, repository, target, original)

	plan := buildUnifiedCRUDTestPlan(t, repository, target, false)
	defer plan.Close()
	if len(plan.Changes()) != 0 {
		t.Fatalf("default unified CRUD changes = %#v", plan.Changes())
	}
	got, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(target)))
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("existing logic = %q, %v", got, err)
	}
}

func TestCRUDOverwriteCannotExpandAcceptedWriteSet(t *testing.T) {
	repository := t.TempDir()
	target := "backend/account/internal/logic/createaccountlogic.go"
	unrelated := "backend/account/internal/logic/customlogic.go"
	writeGenerationTestFile(t, repository, target, []byte("package logic\n\nfunc customized() {}\n"))
	writeGenerationTestFile(t, repository, unrelated, []byte("package logic\n\nfunc unrelated() {}\n"))

	plan := buildUnifiedCRUDTestPlan(t, repository, target, true)
	defer plan.Close()
	changes := plan.Changes()
	if len(changes) != 1 || changes[0].Path() != target {
		t.Fatalf("overwrite changes = %#v", changes)
	}
	before, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(unrelated)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Write(context.Background(), plan, repository, transaction.WriteOptions{PlanDigest: plan.PlanDigest()}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(unrelated)))
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("unaccepted path changed = %q, %v", after, err)
	}
}

func TestCRUDValidationMapsEntCRUDToolToGoTool(t *testing.T) {
	entCRUDTool := toolchain.Tool{ID: "go", Version: "go1.25.0", Executable: "/usr/local/bin/go", Probe: toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: "go version go1.25.0 darwin/arm64"}}
	rpcGoTool := toolchain.Tool{ID: "rpc-go", Version: "v1.0.0", Executable: "/usr/local/bin/goctl", Probe: toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: "goctl version 1.9.2"}}
	rpcEnvironment := []toolchain.EnvVar{{Name: "RPC_MODE", Value: "fixture"}}
	goEnvironment := []toolchain.EnvVar{{Name: "GOWORK", Value: "off"}}
	runner := &internalRecordingRunner{}
	input := crudLogicValidationInput("/repository", "/staging", ServiceProject{EntCRUDTool: entCRUDTool, RPCGoTool: rpcGoTool}, runner, rpcEnvironment, goEnvironment)
	if !reflect.DeepEqual(input.GoTool, entCRUDTool) || !reflect.DeepEqual(input.RPCGoTool, rpcGoTool) || input.Runner != runner || input.RepositoryRoot != "/repository" || input.StagingRoot != "/staging" || !reflect.DeepEqual(input.RPCEnvironment, rpcEnvironment) || !reflect.DeepEqual(input.GoEnvironment, goEnvironment) {
		t.Fatalf("validation input = %#v", input)
	}
	rpcEnvironment[0].Value = "changed"
	goEnvironment[0].Value = "changed"
	if input.RPCEnvironment[0].Value != "fixture" || input.GoEnvironment[0].Value != "off" {
		t.Fatal("validation input retained caller-owned environment")
	}
}

func buildUnifiedCRUDTestPlan(t *testing.T, repository, target string, overwrite bool) transaction.Plan {
	t.Helper()
	schemaPath := "backend/account/ent/schema/account.go"
	validationPath := "backend/account/internal/logic/.crudlogic-validation"
	writeGenerationTestFile(t, repository, schemaPath, []byte("schema"))
	writeGenerationTestFile(t, repository, validationPath, []byte("validation"))
	source := provenance.Source{Ref: mustGenerationRef(t, schemaPath, "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	content := []byte("package logic\n\nfunc generated() {}\n")
	input := transaction.ArtifactInput{
		ID: "crud-logic.account.createaccount", Path: target, Owner: "nexa.dev/generator/crud-logic/v1",
		Digest: provenance.SHA256(content), Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleRetain,
		CreateManual: !overwrite, OverwriteManual: overwrite,
	}
	validation := provenance.Source{Ref: mustGenerationRef(t, validationPath, "crudlogic-validation"), Digest: provenance.SHA256([]byte("validation"))}
	plan, err := transaction.Build(context.Background(), repository, func(_ string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		request, projectErr := projectUnifiedTransactionRequest(
			&fakeUnifiedProtoProjection{sources: []provenance.Source{source}},
			&fakeUnifiedLogicProjection{inputs: []transaction.ArtifactInput{input}, contents: map[string][]byte{target: content}},
			validation, nil, ".nexa/generation/crud-proto.account.manifest.json", emit,
		)
		if projectErr != nil {
			return transaction.PlanRequest{}, projectErr
		}
		request.RevalidateSources = func(context.Context) ([]provenance.Source, error) {
			currentSchema, schemaErr := os.ReadFile(filepath.Join(repository, filepath.FromSlash(schemaPath)))
			if schemaErr != nil {
				return nil, schemaErr
			}
			currentValidation, validationErr := os.ReadFile(filepath.Join(repository, filepath.FromSlash(validationPath)))
			if validationErr != nil {
				return nil, validationErr
			}
			return []provenance.Source{
				{Ref: source.Ref, Digest: provenance.SHA256(currentSchema)},
				{Ref: validation.Ref, Digest: provenance.SHA256(currentValidation)},
			}, nil
		}
		return request, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func writeGenerationTestFile(t *testing.T, repository, name string, content []byte) {
	t.Helper()
	target := filepath.Join(repository, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

type fakeUnifiedProtoProjection struct {
	inputs   []transaction.ArtifactInput
	controls []transaction.ControlSourceMutation
	probes   []transaction.OwnershipProbe
	sources  []provenance.Source
}

func (p *fakeUnifiedProtoProjection) TransactionInputs(func(string, []byte) error) ([]transaction.ArtifactInput, []transaction.ControlSourceMutation, error) {
	return append([]transaction.ArtifactInput(nil), p.inputs...), append([]transaction.ControlSourceMutation(nil), p.controls...), nil
}
func (p *fakeUnifiedProtoProjection) StaleOwnershipProbes() ([]transaction.OwnershipProbe, error) {
	return append([]transaction.OwnershipProbe(nil), p.probes...), nil
}
func (p *fakeUnifiedProtoProjection) Sources() ([]provenance.Source, error) {
	return append([]provenance.Source(nil), p.sources...), nil
}

type fakeUnifiedLogicProjection struct {
	inputs   []transaction.ArtifactInput
	probes   []transaction.OwnershipProbe
	contents map[string][]byte
	sources  []provenance.Source
}

func (p *fakeUnifiedLogicProjection) TransactionInputs(emit func(string, []byte) error) ([]transaction.ArtifactInput, error) {
	for name, content := range p.contents {
		if err := emit(name, content); err != nil {
			return nil, err
		}
	}
	return append([]transaction.ArtifactInput(nil), p.inputs...), nil
}
func (p *fakeUnifiedLogicProjection) StaleOwnershipProbes() ([]transaction.OwnershipProbe, error) {
	return append([]transaction.OwnershipProbe(nil), p.probes...), nil
}
func (p *fakeUnifiedLogicProjection) Sources() ([]provenance.Source, error) {
	return append([]provenance.Source(nil), p.sources...), nil
}

func mustCompatibilityMutation(t *testing.T) transaction.ControlSourceMutation {
	t.Helper()
	mutation, err := transaction.NewCompatibilityLockMutation(transaction.CompatibilityLockMutationSpec{
		ID: "crud-proto.account.lock", Path: "backend/account/rpc/account.crud-protocol.lock.json", Owner: "nexa.dev/generator/crud-proto/v1",
		After: []byte("lock"), AfterDigest: provenance.SHA256([]byte("lock")), Sources: []provenance.SourceRef{mustGenerationRef(t, "backend/account/ent/schema/account.go", "schema:Account")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return mutation
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

type replacingLogicScanRoot struct{ replacingRoot }

func (r *replacingLogicScanRoot) FS() fs.FS { return r.root.FS() }

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

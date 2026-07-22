package source_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	sourceadapter "github.com/nxnminieye/nexa/plugins/nexactl/source"
	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/engine"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func TestSourceAdapterHasOneCapabilityAndSevenCommandsForZeroOneOrManyProviders(t *testing.T) {
	first, _ := sourceTestProvider(t, "sample.alpha", "v0.1.0", "alpha\n")
	second, _ := sourceTestProvider(t, "sample.beta", "v0.1.0", "beta\n")
	for _, test := range []struct {
		name      string
		providers []sourceplugin.Provider
	}{{"zero", nil}, {"one", []sourceplugin.Provider{first}}, {"many", []sourceplugin.Provider{second, first}}} {
		t.Run(test.name, func(t *testing.T) {
			candidate, err := sourceadapter.New(sourceOptions(), test.providers...)
			if err != nil {
				t.Fatal(err)
			}
			spec := candidate.Spec()
			if spec.Descriptor.ID != "source" || spec.Descriptor.Version != "v0.1.0" || spec.Descriptor.ContractVersion == "" {
				t.Fatalf("descriptor = %#v", spec.Descriptor)
			}
			if len(spec.Descriptor.Provides) != 1 || spec.Descriptor.Provides[0].ID != sourceadapter.CapabilityID || spec.Descriptor.Provides[0].Version != sourceadapter.CapabilityVersion {
				t.Fatalf("capability = %#v", spec.Descriptor.Provides)
			}
			wantPaths := [][]string{{"source", "plan"}, {"source", "materialize"}, {"source", "status"}, {"source", "diff"}, {"source", "upgrade"}, {"source", "detach"}, {"source", "check"}}
			if len(spec.Commands) != len(wantPaths) {
				t.Fatalf("commands = %#v", spec.Commands)
			}
			for index, command := range spec.Commands {
				if !reflect.DeepEqual(command.Path, wantPaths[index]) || len(command.InputSchema) == 0 || len(command.OutputSchema) == 0 {
					t.Fatalf("command %d = %#v", index, command)
				}
			}
		})
	}
}

func TestSourceAdapterRejectsDuplicateExactProvider(t *testing.T) {
	provider, _ := sourceTestProvider(t, "sample", "v0.1.0", "value\n")
	if _, err := sourceadapter.New(sourceOptions(), provider, provider); err == nil {
		t.Fatal("duplicate exact provider was accepted")
	}
}

func TestSourceAdapterInspectionPublishesCanonicalExactReleases(t *testing.T) {
	alpha, alphaRef := sourceTestProvider(t, "sample.alpha", "v0.1.0", "alpha\n")
	beta, betaRef := sourceTestProvider(t, "sample.beta", "v0.2.0", "beta\n")
	for _, test := range []struct {
		name      string
		providers []sourceplugin.Provider
		want      []release.Ref
	}{
		{name: "zero", providers: nil, want: []release.Ref{}},
		{name: "canonical", providers: []sourceplugin.Provider{beta, alpha}, want: []release.Ref{alphaRef, betaRef}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate, err := sourceadapter.New(sourceOptions(), test.providers...)
			if err != nil {
				t.Fatal(err)
			}
			var releases []struct {
				ProviderID     string   `json:"providerId"`
				ModulePath     string   `json:"modulePath"`
				PackagePath    string   `json:"packagePath"`
				Version        string   `json:"version"`
				ManifestDigest string   `json:"manifestDigest"`
				TreeDigest     string   `json:"treeDigest"`
				Profiles       []string `json:"profiles"`
			}
			for _, command := range candidate.Spec().Commands {
				if !reflect.DeepEqual(command.Path, []string{"source", "plan"}) {
					continue
				}
				var schema struct {
					Releases []struct {
						ProviderID     string   `json:"providerId"`
						ModulePath     string   `json:"modulePath"`
						PackagePath    string   `json:"packagePath"`
						Version        string   `json:"version"`
						ManifestDigest string   `json:"manifestDigest"`
						TreeDigest     string   `json:"treeDigest"`
						Profiles       []string `json:"profiles"`
					} `json:"x-nexa-source-releases"`
				}
				if err := json.Unmarshal(command.InputSchema, &schema); err != nil {
					t.Fatal(err)
				}
				releases = schema.Releases
			}
			if releases == nil || len(releases) != len(test.want) {
				t.Fatalf("releases = %#v", releases)
			}
			for index, want := range test.want {
				got := releases[index]
				if got.ProviderID != want.ProviderID() || got.ModulePath != want.ModulePath() || got.PackagePath != want.PackagePath() ||
					got.Version != want.Version() || got.ManifestDigest != want.ManifestDigest().String() || got.TreeDigest != want.TreeDigest().String() ||
					!reflect.DeepEqual(got.Profiles, []string{"default"}) {
					t.Fatalf("release[%d] = %#v", index, got)
				}
			}
		})
	}
}

func TestUnlinkedHostAndProviderRemainInert(t *testing.T) {
	_, _ = sourceTestProvider(t, "sample", "v0.1.0", "value\n")
	composed, err := host.New(host.Options{Version: "v0.0.0-test"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(composed.Inspect())
	if err != nil {
		t.Fatal(err)
	}
	var inspection struct {
		Plugins      []any `json:"plugins"`
		Capabilities []any `json:"capabilities"`
		Commands     []struct {
			Path []string `json:"path"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(encoded, &inspection); err != nil {
		t.Fatal(err)
	}
	if len(inspection.Plugins) != 0 || len(inspection.Capabilities) != 0 {
		t.Fatalf("unlinked inspection=%s", encoded)
	}
	for _, command := range inspection.Commands {
		if len(command.Path) > 0 && command.Path[0] == "source" {
			t.Fatalf("provider registered source command: %v", command.Path)
		}
	}
}

func TestZeroProviderExactPlanReturnsUnavailable(t *testing.T) {
	candidate, err := sourceadapter.New(sourceOptions())
	if err != nil {
		t.Fatal(err)
	}
	digest := provenance.SHA256([]byte("missing")).String()
	result := executeSourceHost(t, context.Background(), candidate, []string{"source", "plan", "--repo-root", t.TempDir(), "--provider", "missing", "--version", "v0.1.0", "--profile", "default", "--target", "services/sample", "--manifest-digest", digest, "--tree-digest", digest, "--json"})
	assertSourceExecution(t, result, 6, false, protocol.CategoryUnavailable)
}

func TestSourceAdapterDeterministicHostMatrix(t *testing.T) {
	provider, ref := sourceTestProvider(t, "sample", "v0.1.0", "source\n")
	pluginWithProvider, err := sourceadapter.New(sourceOptions(), provider)
	if err != nil {
		t.Fatal(err)
	}
	selection := func(repository string, selected release.Ref) []string {
		return []string{
			"--repo-root", repository, "--provider", selected.ProviderID(), "--version", selected.Version(),
			"--profile", "default", "--target", "services/sample", "--manifest-digest", selected.ManifestDigest().String(),
			"--tree-digest", selected.TreeDigest().String(), "--json",
		}
	}
	managed := func(repository string) []string {
		return []string{"--repo-root", repository, "--provider", "sample", "--target", "services/sample", "--json"}
	}

	t.Run("zero provider unavailable", func(t *testing.T) {
		candidate, newErr := sourceadapter.New(sourceOptions())
		if newErr != nil {
			t.Fatal(newErr)
		}
		digest := provenance.SHA256([]byte("absent")).String()
		result := executeSourceHost(t, context.Background(), candidate, []string{
			"source", "plan", "--repo-root", t.TempDir(), "--provider", "absent", "--version", "v0.1.0",
			"--profile", "default", "--target", "services/sample", "--manifest-digest", digest, "--tree-digest", digest, "--json",
		})
		assertSourceExecution(t, result, 6, false, protocol.CategoryUnavailable)
	})

	t.Run("malformed input", func(t *testing.T) {
		repository := t.TempDir()
		args := selection(repository, ref)
		args[13] = "not-a-digest"
		result := executeSourceHost(t, context.Background(), pluginWithProvider, append([]string{"source", "plan"}, args...))
		assertSourceExecution(t, result, 3, false, protocol.CategoryInput, repository)
	})

	t.Run("not managed states", func(t *testing.T) {
		repository := t.TempDir()
		result := executeSourceHost(t, context.Background(), pluginWithProvider, append([]string{"source", "status"}, managed(repository)...))
		assertSourceExecution(t, result, 0, true, "", repository)
		var status struct {
			State engine.ManagedState `json:"state"`
		}
		decodeResult(t, result.envelope, &status)
		if status.State != engine.ManagedStateNotManaged {
			t.Fatalf("status state = %q", status.State)
		}

		for _, command := range []struct {
			action string
			flags  []string
		}{{"diff", managed(repository)}, {"detach", managed(repository)}, {"upgrade", selection(repository, ref)}} {
			t.Run(command.action, func(t *testing.T) {
				result := executeSourceHost(t, context.Background(), pluginWithProvider, append([]string{"source", command.action}, command.flags...))
				assertSourceExecution(t, result, 3, false, protocol.CategoryInput, repository)
			})
		}
	})

	t.Run("plan and write conflict", func(t *testing.T) {
		repository := t.TempDir()
		target := filepath.Join(repository, "services/sample")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "value.txt"), []byte("private-local-value\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		args := selection(repository, ref)
		plan := executeSourceHost(t, context.Background(), pluginWithProvider, append([]string{"source", "plan"}, args...))
		assertSourceExecution(t, plan, 0, true, "", repository, "private-local-value")
		var projected struct {
			CanApply  bool  `json:"canApply"`
			Conflicts []any `json:"conflicts"`
		}
		decodeResult(t, plan.envelope, &projected)
		if projected.CanApply || len(projected.Conflicts) == 0 {
			t.Fatalf("plan result = %#v", projected)
		}

		assertRepositoryUnchanged(t, repository, func() {
			write := executeSourceHost(t, context.Background(), pluginWithProvider, append([]string{"source", "materialize"}, args...))
			assertSourceExecution(t, write, 13, false, protocol.CategoryConflict, repository, "private-local-value")
		})
	})

	t.Run("validation outcomes", func(t *testing.T) {
		validationProvider, validationRef := sourceValidationProvider(t, "validation.sample", "v0.1.0")
		repository := t.TempDir()
		missingOptions := sourceOptions()
		missingOptions.Executor = sourceExecutorFunc(func(context.Context, engine.Execution) (engine.ExecutionResult, error) {
			return engine.ExecutionResult{}, nil
		})
		missingPlugin, newErr := sourceadapter.New(missingOptions, validationProvider)
		if newErr != nil {
			t.Fatal(newErr)
		}
		missing := executeSourceHost(t, context.Background(), missingPlugin, append([]string{"source", "materialize"}, selection(repository, validationRef)...))
		assertSourceExecution(t, missing, 6, false, protocol.CategoryUnavailable, repository)

		externalOptions := sourceOptions()
		externalOptions.Executor = sourceExecutorFunc(func(context.Context, engine.Execution) (engine.ExecutionResult, error) {
			return engine.ExecutionResult{ExitCode: 9}, nil
		})
		externalOptions.GoToolchain = sourceGoToolchain(t)
		externalPlugin, newErr := sourceadapter.New(externalOptions, validationProvider)
		if newErr != nil {
			t.Fatal(newErr)
		}
		external := executeSourceHost(t, context.Background(), externalPlugin, append([]string{"source", "materialize"}, selection(repository, validationRef)...))
		assertSourceExecution(t, external, 7, false, protocol.CategoryExternal, repository)
	})

	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		repository := t.TempDir()
		result := executeSourceHost(t, ctx, pluginWithProvider, append([]string{"source", "plan"}, selection(repository, ref)...))
		assertSourceExecution(t, result, 130, false, protocol.CategoryCanceled, repository)
	})
}

type sourceMergeDriver struct{}

func (sourceMergeDriver) Merge(_ context.Context, input engine.TextMergeInput) (engine.TextMergeResult, error) {
	return engine.NewTextMergeResult(input.New, true), nil
}

func sourceOptions() sourceadapter.Options {
	return sourceadapter.Options{Version: "v0.1.0", TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(), MergeDriver: sourceMergeDriver{}}
}

func sourceTestProvider(t *testing.T, providerID, version, content string) (sourceplugin.Provider, release.Ref) {
	return sourceProvider(t, providerID, version, "value.txt", content, nil)
}

func sourceValidationProvider(t *testing.T, providerID, version string) (sourceplugin.Provider, release.Ref) {
	return sourceProvider(t, providerID, version, "go.mod", "module example.test/generated\n\ngo 1.25.0\n", []sourceplugin.ValidationRecipeSpec{{ID: "build", Kind: sourceplugin.ValidationGoBuild, WorkingDirectory: ".", Packages: []string{"."}}})
}

func sourceProvider(t *testing.T, providerID, version, path, content string, validations []sourceplugin.ValidationRecipeSpec) (sourceplugin.Provider, release.Ref) {
	t.Helper()
	bytes := []byte(content)
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: providerID, ModulePath: "example.test/" + providerID, PackagePath: "example.test/" + providerID + "/source", Version: version},
		Files:    []sourceplugin.FileSpec{{Path: path, Mode: sourceplugin.Mode0644, Size: int64(len(bytes)), Digest: provenance.SHA256(bytes)}},
		Profiles: []sourceplugin.ProfileSpec{{ID: "default", Files: []string{path}, Validations: validations}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sourceplugin.NewTree(manifest, []sourceplugin.TreeInput{{Path: path, Content: bytes}}, sourceplugin.DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	provider, err := sourceplugin.NewProvider(manifest, tree)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	return provider, ref
}

type sourceHostExecution struct {
	envelope protocol.Envelope
	exit     int
	stdout   string
	stderr   string
}

func executeSourceHost(t *testing.T, ctx context.Context, candidate interface{ Spec() plugin.Spec }, args []string) sourceHostExecution {
	t.Helper()
	composed, err := host.New(host.Options{
		Version: "v0.0.0-test",
		OperationIDs: protocol.OperationIDGeneratorFunc(func() (string, error) {
			return sourceOperationID, nil
		}),
	}, candidate)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit := composed.Execute(ctx, args, &stdout, &stderr)
	return sourceHostExecution{envelope: decodeSingleEnvelope(t, stdout.Bytes()), exit: exit, stdout: stdout.String(), stderr: stderr.String()}
}

func decodeSingleEnvelope(t *testing.T, payload []byte) protocol.Envelope {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(payload))
	var envelope protocol.Envelope
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("decode envelope %q: %v", payload, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("stdout contains more than one JSON value: %q", payload)
	}
	return envelope
}

func assertSourceExecution(t *testing.T, result sourceHostExecution, wantExit int, wantOK bool, wantCategory protocol.Category, secrets ...string) {
	t.Helper()
	if result.exit != wantExit || result.stderr != "" || result.envelope.OK != wantOK || result.envelope.OperationID != sourceOperationID {
		t.Fatalf("exit=%d stderr=%q envelope=%#v", result.exit, result.stderr, result.envelope)
	}
	if wantOK {
		if result.envelope.Error != nil {
			t.Fatalf("success error = %#v", result.envelope.Error)
		}
	} else if result.envelope.Error == nil || result.envelope.Error.Category != wantCategory || len(result.envelope.Error.Details) != 0 && !jsonObject(result.envelope.Error.Details) {
		t.Fatalf("failure envelope = %#v", result.envelope)
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(result.stdout, secret) {
			t.Fatalf("stdout exposed private value %q: %s", secret, result.stdout)
		}
	}
}

func decodeResult(t *testing.T, envelope protocol.Envelope, target any) {
	t.Helper()
	encoded, err := json.Marshal(envelope.Result)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		t.Fatal(err)
	}
}

func jsonObject(value json.RawMessage) bool {
	var object map[string]any
	return len(value) != 0 && json.Unmarshal(value, &object) == nil && object != nil
}

func assertRepositoryUnchanged(t *testing.T, root string, execute func()) {
	t.Helper()
	before := repositoryState(t, root)
	execute()
	after := repositoryState(t, root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("repository changed: before=%#v after=%#v", before, after)
	}
}

func repositoryState(t *testing.T, root string) map[string]string {
	t.Helper()
	state := make(map[string]string)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		switch {
		case info.Mode().IsRegular():
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			value += ":" + provenance.SHA256(content).String()
		case info.Mode()&os.ModeSymlink != 0:
			target, readErr := os.Readlink(path)
			if readErr != nil {
				return readErr
			}
			value += ":" + target
		}
		state[filepath.ToSlash(relative)] = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return state
}

type sourceExecutorFunc func(context.Context, engine.Execution) (engine.ExecutionResult, error)

func (execute sourceExecutorFunc) Execute(ctx context.Context, execution engine.Execution) (engine.ExecutionResult, error) {
	return execute(ctx, execution)
}

func sourceGoToolchain(t *testing.T) engine.GoToolchain {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return engine.GoToolchain{
		Executable: executable, Home: t.TempDir(), TempDir: t.TempDir(), GOPATH: t.TempDir(), ModuleCache: t.TempDir(), BuildCache: t.TempDir(),
	}
}

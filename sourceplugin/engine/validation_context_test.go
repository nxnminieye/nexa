package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

const (
	validationProviderVersion      = "v0.0.0-20260727202435-6db820cb3333"
	validationSourceReleaseVersion = "v0.2.0"
)

func TestMaterializeValidatesExampleShapedTargetInStagedConsumerModule(t *testing.T) {
	providerModule := createLocalValidationProviderModule(t)
	provider, ref := validationContextProvider(t, map[string]string{
		"backend/core/coreapp/generated.go": "package coreapp\n\nimport \"example.test/provider/helper\"\n\nvar Generated = helper.Value\n",
	}, true)
	resolver, err := release.NewExactResolver(nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{
		Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(),
		MergeDriver: publishMergeDriver{}, Executor: NewOSExecutor(), GoToolchain: osValidationToolchain(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	moduleBytes := []byte("module example.test/consumer\n\ngo 1.25.0\n\nrequire example.test/provider " + validationProviderVersion + " // indirect\n")
	workBytes := []byte("go 1.25.0\n\nuse (\n\t.\n\t./unrelated\n)\n\nreplace example.test/provider " + validationProviderVersion + " => " + filepath.ToSlash(providerModule) + "\nreplace example.test/unrelated => ./missing\n")
	sumBytes := []byte{}
	writePublishTestFile(t, filepath.Join(root, "go.mod"), moduleBytes, 0o644)
	writePublishTestFile(t, filepath.Join(root, "go.sum"), sumBytes, 0o644)
	writePublishTestFile(t, filepath.Join(root, "go.work"), workBytes, 0o644)
	writePublishTestFile(t, filepath.Join(root, "unrelated", "broken.go"), []byte("this is not Go\n"), 0o644)

	selection := lifecycleSelection(t, ref, "framework/core")
	result, err := engine.Materialize(context.Background(), MaterializeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: selection}})
	if err != nil || result.Operation() != PlanMaterialize {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	assertPublishedFile(t, filepath.Join(root, "framework/core/backend/core/coreapp/generated.go"), "package coreapp\n\nimport \"example.test/provider/helper\"\n\nvar Generated = helper.Value\n", 0o644)
	assertExactFileBytes(t, filepath.Join(root, "go.mod"), moduleBytes)
	assertExactFileBytes(t, filepath.Join(root, "go.sum"), sumBytes)
	assertExactFileBytes(t, filepath.Join(root, "go.work"), workBytes)
	for _, path := range []string{"framework/core/go.mod", "framework/core/go.sum"} {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("validation-only metadata was published at %s: %v", path, err)
		}
	}
	assertPublishStagingClean(t, root)

	repeat, err := engine.Materialize(context.Background(), MaterializeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: selection}})
	if err != nil || repeat.Operation() != PlanNoop || repeat.Status().State() != ManagedStateClean {
		t.Fatalf("repeat=%#v err=%v", repeat, err)
	}
}

func TestMaterializeValidatesRequirementOnlyProfile(t *testing.T) {
	tests := []struct {
		name       string
		module     string
		wantReason string
	}{
		{
			name:       "missing",
			module:     "module example.test/consumer\n\ngo 1.25.0\n",
			wantReason: "module_requirement_missing",
		},
		{
			name:       "mismatch",
			module:     "module example.test/consumer\n\ngo 1.25.0\n\nrequire example.test/provider v0.0.1\n",
			wantReason: "module_requirement_mismatch",
		},
		{
			name:   "success without executor",
			module: "module example.test/consumer\n\ngo 1.25.0\n\nrequire example.test/provider " + validationProviderVersion + "\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider, ref := validationRequirementOnlyProvider(t)
			resolver, err := release.NewExactResolver(nil, provider)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := New(Options{
				Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(),
				MergeDriver: publishMergeDriver{},
			})
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			writePublishTestFile(t, filepath.Join(root, "go.mod"), []byte(test.module), 0o644)
			result, materializeErr := candidate.Materialize(context.Background(), MaterializeRequest{PlanRequest: PlanRequest{
				RepositoryRoot: root,
				Selection:      lifecycleSelection(t, ref, "framework/core"),
			}})
			if test.wantReason != "" {
				assertLifecycleError(t, materializeErr, ErrInput, test.wantReason)
				assertNoValidationBusinessWrites(t, root)
				return
			}
			if materializeErr != nil || result.Operation() != PlanMaterialize || result.Status().State() != ManagedStateClean {
				t.Fatalf("result=%#v err=%v", result, materializeErr)
			}
			assertPublishedFile(t, filepath.Join(root, "framework/core/value.txt"), "requirement-only\n", 0o644)
		})
	}
}

func TestMaterializeValidationModuleContextFailsClosedWithoutBusinessWrites(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		prepare    func(*testing.T, string)
		wantReason string
	}{
		{
			name: "root module missing", files: validationContextSourceFiles(),
			prepare: func(t *testing.T, root string) {}, wantReason: "repository_module_missing",
		},
		{
			name: "root module malformed", files: validationContextSourceFiles(),
			prepare: func(t *testing.T, root string) {
				writePublishTestFile(t, filepath.Join(root, "go.mod"), []byte("not a module\n"), 0o644)
			},
			wantReason: "repository_module_malformed",
		},
		{
			name: "root module nonregular", files: validationContextSourceFiles(),
			prepare: func(t *testing.T, root string) {
				if err := os.Mkdir(filepath.Join(root, "go.mod"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "repository_module_invalid",
		},
		{
			name: "root sum symlink", files: validationContextSourceFiles(),
			prepare: func(t *testing.T, root string) {
				writeValidationConsumerModule(t, root)
				if err := os.Symlink("go.mod", filepath.Join(root, "go.sum")); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "repository_sum_invalid",
		},
		{
			name: "root workspace nonregular", files: validationContextSourceFiles(),
			prepare: func(t *testing.T, root string) {
				writeValidationConsumerModule(t, root)
				if err := os.Mkdir(filepath.Join(root, "go.work"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "repository_work_invalid",
		},
		{
			name: "required module missing", files: validationContextSourceFiles(),
			prepare: func(t *testing.T, root string) {
				writePublishTestFile(t, filepath.Join(root, "go.mod"), []byte("module example.test/consumer\n\ngo 1.25.0\n"), 0o644)
			},
			wantReason: "module_requirement_missing",
		},
		{
			name: "required module mismatch", files: validationContextSourceFiles(),
			prepare: func(t *testing.T, root string) {
				writePublishTestFile(t, filepath.Join(root, "go.mod"), []byte("module example.test/consumer\n\ngo 1.25.0\n\nrequire example.test/provider v0.0.1\n"), 0o644)
			},
			wantReason: "module_requirement_mismatch",
		},
		{
			name: "selected replace in root module", files: validationContextSourceFiles(),
			prepare: func(t *testing.T, root string) {
				writePublishTestFile(t, filepath.Join(root, "go.mod"), []byte("module example.test/consumer\n\ngo 1.25.0\n\nrequire example.test/provider "+validationProviderVersion+"\nreplace example.test/provider "+validationProviderVersion+" => ../provider\n"), 0o644)
			},
			wantReason: "provider_module_replace_conflict",
		},
		{
			name: "workspace wildcard provider replace", files: validationContextSourceFiles(),
			prepare: func(t *testing.T, root string) {
				writeValidationConsumerModule(t, root)
				writePublishTestFile(t, filepath.Join(root, "go.work"), []byte("go 1.25.0\nreplace example.test/provider => ../provider\n"), 0o644)
			},
			wantReason: "workspace_provider_replace_mismatch",
		},
		{
			name: "workspace malformed", files: validationContextSourceFiles(),
			prepare: func(t *testing.T, root string) {
				writeValidationConsumerModule(t, root)
				writePublishTestFile(t, filepath.Join(root, "go.work"), []byte("not a workspace\n"), 0o644)
			},
			wantReason: "repository_workspace_malformed",
		},
		{
			name: "workspace competing provider replaces", files: validationContextSourceFiles(),
			prepare: func(t *testing.T, root string) {
				writeValidationConsumerModule(t, root)
				work := "go 1.25.0\nreplace (\nexample.test/provider " + validationProviderVersion + " => ../provider-one\nexample.test/provider v0.0.1 => ../provider-two\n)\n"
				writePublishTestFile(t, filepath.Join(root, "go.work"), []byte(work), 0o644)
			},
			wantReason: "workspace_provider_replace_conflict",
		},
		{
			name: "workspace local module path mismatch", files: validationContextSourceFiles(),
			prepare: func(t *testing.T, root string) {
				writeValidationConsumerModule(t, root)
				local, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				writePublishTestFile(t, filepath.Join(local, "go.mod"), []byte("module example.test/not-provider\n\ngo 1.25.0\n"), 0o644)
				work := "go 1.25.0\nreplace example.test/provider " + validationProviderVersion + " => " + filepath.ToSlash(local) + "\n"
				writePublishTestFile(t, filepath.Join(root, "go.work"), []byte(work), 0o644)
			},
			wantReason: "workspace_provider_local_mismatch",
		},
		{
			name: "workspace local path contains symlink", files: validationContextSourceFiles(),
			prepare: func(t *testing.T, root string) {
				writeValidationConsumerModule(t, root)
				local := createLocalValidationProviderModule(t)
				link := filepath.Join(t.TempDir(), "provider-link")
				if err := os.Symlink(local, link); err != nil {
					t.Fatal(err)
				}
				work := "go 1.25.0\nreplace example.test/provider " + validationProviderVersion + " => " + filepath.ToSlash(link) + "\n"
				writePublishTestFile(t, filepath.Join(root, "go.work"), []byte(work), 0o644)
			},
			wantReason: "workspace_provider_local_invalid",
		},
		{
			name: "target root module nonregular", files: validationContextSourceFiles(),
			prepare: func(t *testing.T, root string) {
				if err := os.MkdirAll(filepath.Join(root, "framework/core/go.mod"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "target_module_invalid",
		},
		{
			name: "nested target module", files: map[string]string{
				"backend/core/coreapp/generated.go": "package coreapp\n",
				"backend/core/nested/go.mod":        "module example.test/nested\n\ngo 1.25.0\n",
			},
			prepare:    func(t *testing.T, root string) { writeValidationConsumerModule(t, root) },
			wantReason: "nested_module_unsupported",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider, ref := validationContextProvider(t, test.files, true)
			resolver, _ := release.NewExactResolver(nil, provider)
			recorder := &recordingValidationExecutor{}
			engine, err := New(Options{
				Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(),
				MergeDriver: publishMergeDriver{}, Executor: recorder, GoToolchain: validationToolchain(t),
			})
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			test.prepare(t, root)
			beforeModule := testPathState(t, filepath.Join(root, "go.mod"))
			beforeSum := testPathState(t, filepath.Join(root, "go.sum"))
			beforeWork := testPathState(t, filepath.Join(root, "go.work"))
			key, _ := lock.NewKey("validation-context", "framework/core")
			beforeBusiness := publishBusinessState(t, root, key)
			_, err = engine.Materialize(context.Background(), MaterializeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, ref, "framework/core")}})
			assertLifecycleError(t, err, ErrInput, test.wantReason)
			if len(recorder.calls) != 0 {
				t.Fatalf("validation executor ran after context rejection: %#v", recorder.calls)
			}
			if got := testPathState(t, filepath.Join(root, "go.mod")); got != beforeModule {
				t.Fatalf("go.mod changed: got=%q want=%q", got, beforeModule)
			}
			if got := testPathState(t, filepath.Join(root, "go.sum")); got != beforeSum {
				t.Fatalf("go.sum changed: got=%q want=%q", got, beforeSum)
			}
			if got := testPathState(t, filepath.Join(root, "go.work")); got != beforeWork {
				t.Fatalf("go.work changed: got=%q want=%q", got, beforeWork)
			}
			if got := publishBusinessState(t, root, key); got != beforeBusiness {
				t.Fatalf("target or lock changed: got=%q want=%q", got, beforeBusiness)
			}
			assertPublishStagingClean(t, root)
		})
	}
}

func TestMaterializeSelfContainedTargetIgnoresConsumerModule(t *testing.T) {
	t.Run("self contained", func(t *testing.T) {
		provider, ref := validationContextProvider(t, map[string]string{
			"go.mod":  "module example.test/materialized\n\ngo 1.25.0\n",
			"main.go": "package materialized\n\nconst Value = true\n",
		}, false)
		resolver, _ := release.NewExactResolver(nil, provider)
		engine, err := New(Options{Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(), MergeDriver: publishMergeDriver{}, Executor: NewOSExecutor(), GoToolchain: osValidationToolchain(t)})
		if err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "go.mod"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("missing-work", filepath.Join(root, "go.work")); err != nil {
			t.Fatal(err)
		}
		_, err = engine.Materialize(context.Background(), MaterializeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, ref, "services/self")}})
		if err != nil {
			t.Fatal(err)
		}
		assertPublishedFile(t, filepath.Join(root, "services/self/main.go"), "package materialized\n\nconst Value = true\n", 0o644)
	})
}

func TestMaterializeModuleSnapshotDriftPreventsTargetAndLockWrites(t *testing.T) {
	provider, ref := validationContextProvider(t, validationContextSourceFiles(), true)
	resolver, _ := release.NewExactResolver(nil, provider)
	root := t.TempDir()
	writeValidationConsumerModule(t, root)
	executor := lifecycleExecutorFunc(func(Execution) (ExecutionResult, error) {
		writePublishTestFile(t, filepath.Join(root, "go.sum"), []byte("concurrent change\n"), 0o644)
		return ExecutionResult{}, nil
	})
	engine, err := New(Options{Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(), MergeDriver: publishMergeDriver{}, Executor: executor, GoToolchain: validationToolchain(t)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Materialize(context.Background(), MaterializeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, ref, "framework/core")}})
	assertLifecycleError(t, err, ErrConflict, "source_snapshot_changed")
	assertNoValidationBusinessWrites(t, root)
}

func TestMaterializeOfflineModuleCacheMissDoesNotModifyConsumerMetadata(t *testing.T) {
	provider, ref := validationContextProvider(t, validationContextSourceFiles(), true)
	resolver, _ := release.NewExactResolver(nil, provider)
	engine, err := New(Options{Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(), MergeDriver: publishMergeDriver{}, Executor: NewOSExecutor(), GoToolchain: osValidationToolchain(t)})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeValidationConsumerModule(t, root)
	before := optionalFileBytes(t, filepath.Join(root, "go.mod"))
	_, err = engine.Materialize(context.Background(), MaterializeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, ref, "framework/core")}})
	assertLifecycleError(t, err, ErrExternal, "validation_failed")
	assertOptionalFileBytes(t, filepath.Join(root, "go.mod"), before)
	assertNoValidationBusinessWrites(t, root)
}

func validationContextProvider(t *testing.T, files map[string]string, requireProvider bool) (sourceplugin.Provider, release.Ref) {
	return validationContextProviderWithRecipes(t, files, requireProvider, true)
}

func validationRequirementOnlyProvider(t *testing.T) (sourceplugin.Provider, release.Ref) {
	return validationContextProviderWithRecipes(t, map[string]string{"value.txt": "requirement-only\n"}, true, false)
}

func validationContextProviderWithRecipes(t *testing.T, files map[string]string, requireProvider, withRecipes bool) (sourceplugin.Provider, release.Ref) {
	t.Helper()
	paths := make([]string, 0, len(files))
	specs := make([]sourceplugin.FileSpec, 0, len(files))
	inputs := make([]sourceplugin.TreeInput, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		value := files[path]
		content := []byte(value)
		specs = append(specs, sourceplugin.FileSpec{Path: path, Mode: sourceplugin.Mode0644, Size: int64(len(content)), Digest: provenance.SHA256(content)})
		inputs = append(inputs, sourceplugin.TreeInput{Path: path, Content: content})
	}
	profile := sourceplugin.ProfileSpec{ID: "default", Files: paths}
	if withRecipes {
		profile.Validations = []sourceplugin.ValidationRecipeSpec{{ID: "core-test", Kind: sourceplugin.ValidationGoTest, WorkingDirectory: "backend/core", Packages: []string{"./coreapp"}}}
		if _, selfContained := files["go.mod"]; selfContained {
			profile.Validations = []sourceplugin.ValidationRecipeSpec{{ID: "root-test", Kind: sourceplugin.ValidationGoTest, WorkingDirectory: ".", Packages: []string{"."}}}
		}
	}
	if requireProvider {
		profile.RequiresGoModules = []sourceplugin.GoModuleRequirementSpec{{ModulePath: "example.test/provider", Version: validationProviderVersion}}
	}
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: "validation-context", ModulePath: "example.test/provider", PackagePath: "example.test/provider/source", Version: validationSourceReleaseVersion},
		Files:    specs, Profiles: []sourceplugin.ProfileSpec{profile},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sourceplugin.NewTree(manifest, inputs, sourceplugin.DefaultTreeLimits())
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

func validationContextSourceFiles() map[string]string {
	return map[string]string{"backend/core/coreapp/generated.go": "package coreapp\n\nimport \"example.test/provider/helper\"\n\nvar Generated = helper.Value\n"}
}

func createLocalValidationProviderModule(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writePublishTestFile(t, filepath.Join(root, "go.mod"), []byte("module example.test/provider\n\ngo 1.25.0\n"), 0o644)
	writePublishTestFile(t, filepath.Join(root, "helper", "helper.go"), []byte("package helper\n\nconst Value = true\n"), 0o644)
	return root
}

func writeValidationConsumerModule(t *testing.T, root string) {
	t.Helper()
	writePublishTestFile(t, filepath.Join(root, "go.mod"), []byte("module example.test/consumer\n\ngo 1.25.0\n\nrequire example.test/provider "+validationProviderVersion+"\n"), 0o644)
}

func osValidationToolchain(t *testing.T) GoToolchain {
	t.Helper()
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Skip("Go toolchain unavailable")
	}
	return GoToolchain{Executable: goExecutable, Home: t.TempDir(), TempDir: t.TempDir(), GOPATH: t.TempDir(), ModuleCache: t.TempDir(), BuildCache: t.TempDir()}
}

func assertNoValidationBusinessWrites(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, "framework/core")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target was written: %v", err)
	}
	key, _ := lock.NewKey("validation-context", "framework/core")
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(key.RepositoryPath()))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock was written: %v", err)
	}
	assertPublishStagingClean(t, root)
}

func assertExactFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(want) {
		t.Fatalf("%s bytes changed: %q, %v", path, got, err)
	}
}

func optionalFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func assertOptionalFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got := optionalFileBytes(t, path)
	if string(got) != string(want) {
		t.Fatalf("%s bytes changed: got=%q want=%q", path, got, want)
	}
}

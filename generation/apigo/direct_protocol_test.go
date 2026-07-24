package apigo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/apigo"
	"github.com/nxnminieye/nexa/generation/directwrite"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
)

func TestAPIGoV2ProtocolAndDirectRunner(t *testing.T) {
	snapshot := apiSnapshot(t)
	scopes := []directwrite.OutputScope{{Path: "backend/core/internal/handler", Mode: directwrite.OutputModeReplaceTree}}
	static := []apigo.StaticInput{{ID: "api-entry", Path: "backend/core/api/generated.api", Digest: provenance.SHA256([]byte("api"))}}
	request := apigo.APIGoRequest{APIVersion: apigo.APIGoRequestAPIVersion, Kind: apigo.APIGoRequestKind, CoreServiceID: "core", ModulePath: "example.com/consumer", HTTPAPIIR: snapshot, APIEntry: static[0].Path, StaticInputs: static, OutputScopes: scopes}
	stdin, err := apigo.CanonicalAPIGoRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := apigo.ParseAPIGoRequest(stdin)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := apigo.CanonicalAPIGoRequest(parsed)
	if !bytes.Equal(stdin, again) || bytes.Contains(stdin, []byte(`"artifacts"`)) || bytes.Contains(stdin, []byte(`"manual"`)) {
		t.Fatalf("API v2 request = %s", stdin)
	}

	repository, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(filepath.Join(repository, filepath.FromSlash(static[0].Path))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, filepath.FromSlash(static[0].Path)), []byte("api"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := toolchain.DirectRunnerFunc(func(_ context.Context, call toolchain.DirectRequest) (toolchain.Result, error) {
		if call.RepositoryRoot != repository || !reflect.DeepEqual(call.Args, []string{"generate", "--core-service", "core"}) || !bytes.Equal(call.Stdin, stdin) {
			t.Fatalf("direct call = %#v", call)
		}
		resultBytes, resultErr := apigo.CanonicalAPIGoResult(apigo.APIGoResult{APIVersion: apigo.APIGoResultAPIVersion, Kind: apigo.APIGoResultKind, Status: apigo.APIGoResultGenerated, CoreServiceID: "core", InputDigest: provenance.SHA256(call.Stdin), OutputScopes: scopes})
		return toolchain.Result{ToolID: "api", Version: "v2", ExecutableVersion: "api-v2", Stdout: resultBytes}, resultErr
	})
	result, err := apigo.RunDirectAPIGo(context.Background(), request, apigo.DirectOptions{RepositoryRoot: repository, Tool: toolchain.Tool{ID: "api", Version: "v2", InputScopes: []string{static[0].Path}, WriteScopes: []string{scopes[0].Path}, Probe: toolchain.ExecutableProbe{ExpectedVersion: "api-v2"}}, Runner: runner, OutputScopes: scopes})
	if err != nil || result.InputDigest != provenance.SHA256(stdin) {
		t.Fatalf("RunDirectAPIGo = %#v, %v", result, err)
	}
	invalidRunner := toolchain.DirectRunnerFunc(func(_ context.Context, call toolchain.DirectRequest) (toolchain.Result, error) {
		encoded, resultErr := apigo.CanonicalAPIGoResult(apigo.APIGoResult{APIVersion: apigo.APIGoResultAPIVersion, Kind: apigo.APIGoResultKind, Status: apigo.APIGoResultGenerated, CoreServiceID: "core", InputDigest: provenance.SHA256([]byte("different stdin")), OutputScopes: scopes})
		return toolchain.Result{ToolID: "api", Version: "v2", ExecutableVersion: "api-v2", Stdout: encoded}, resultErr
	})
	if _, err := apigo.RunDirectAPIGo(context.Background(), request, apigo.DirectOptions{RepositoryRoot: repository, Tool: toolchain.Tool{ID: "api", Version: "v2", InputScopes: []string{static[0].Path}, WriteScopes: []string{scopes[0].Path}, Probe: toolchain.ExecutableProbe{ExpectedVersion: "api-v2"}}, Runner: invalidRunner, OutputScopes: scopes}); err == nil {
		t.Fatal("accepted result digest that does not match exact stdin")
	}
}

func TestRunDirectAPIGoRejectsPostToolStaticInputMutation(t *testing.T) {
	request := validDirectAPIRequest(t)
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	entry := filepath.Join(repository, filepath.FromSlash(request.APIEntry))
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	runner := toolchain.DirectRunnerFunc(func(_ context.Context, call toolchain.DirectRequest) (toolchain.Result, error) {
		if err := os.WriteFile(entry, []byte("mutated"), 0o644); err != nil {
			t.Fatal(err)
		}
		return toolchain.Result{ToolID: "api", Version: "v2", ExecutableVersion: "api-v2", Stdout: canonicalAPIDirectResult(t, provenance.SHA256(call.Stdin), request.OutputScopes)}, nil
	})
	tool := toolchain.Tool{ID: "api", Version: "v2", InputScopes: apiTestInputPaths(request.StaticInputs), WriteScopes: apiTestScopePaths(request.OutputScopes), Probe: toolchain.ExecutableProbe{ExpectedVersion: "api-v2"}}
	_, err := apigo.RunDirectAPIGo(context.Background(), request, apigo.DirectOptions{RepositoryRoot: repository, Tool: tool, Runner: runner, OutputScopes: request.OutputScopes})
	assertAPIPostInvocationEvidence(t, err, "result_acknowledgement_invalid", "/result")
}

func TestRunDirectAPIGoRejectsPostToolStaticInputIdentityReplacement(t *testing.T) {
	request := validDirectAPIRequest(t)
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	entry := filepath.Join(repository, filepath.FromSlash(request.APIEntry))
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	runner := toolchain.DirectRunnerFunc(func(_ context.Context, call toolchain.DirectRequest) (toolchain.Result, error) {
		replacement := entry + ".replacement"
		if err := os.WriteFile(replacement, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, entry); err != nil {
			t.Fatal(err)
		}
		return toolchain.Result{ToolID: "api", Version: "v2", ExecutableVersion: "api-v2", Stdout: canonicalAPIDirectResult(t, provenance.SHA256(call.Stdin), request.OutputScopes)}, nil
	})
	tool := toolchain.Tool{ID: "api", Version: "v2", InputScopes: apiTestInputPaths(request.StaticInputs), WriteScopes: apiTestScopePaths(request.OutputScopes), Probe: toolchain.ExecutableProbe{ExpectedVersion: "api-v2"}}
	_, err := apigo.RunDirectAPIGo(context.Background(), request, apigo.DirectOptions{RepositoryRoot: repository, Tool: tool, Runner: runner, OutputScopes: request.OutputScopes})
	assertAPIPostInvocationEvidence(t, err, "result_acknowledgement_invalid", "/result")
}

func TestRunDirectAPIGoPreservesEveryNonGoManualFile(t *testing.T) {
	for _, extension := range []string{".proto", ".api", ".json", ".yaml"} {
		for _, action := range []string{"mutate", "delete", "create"} {
			t.Run(extension+"/"+action, func(t *testing.T) {
				request := validDirectAPIRequest(t)
				repository, _ := filepath.EvalSymlinks(t.TempDir())
				entry := filepath.Join(repository, filepath.FromSlash(request.APIEntry))
				manual := filepath.Join(repository, filepath.FromSlash(request.OutputScopes[0].Path), "manual"+extension)
				if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(manual), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(entry, nil, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(manual, []byte("manual"), 0o644); err != nil {
					t.Fatal(err)
				}
				runner := toolchain.DirectRunnerFunc(func(_ context.Context, call toolchain.DirectRequest) (toolchain.Result, error) {
					if action == "create" {
						_ = os.WriteFile(filepath.Join(repository, filepath.FromSlash(request.OutputScopes[0].Path), "new"+extension), nil, 0o644)
					} else if action == "delete" {
						_ = os.Remove(manual)
					} else {
						_ = os.WriteFile(manual, []byte("changed"), 0o644)
					}
					return toolchain.Result{ToolID: "api", Version: "v2", ExecutableVersion: "api-v2", Stdout: canonicalAPIDirectResult(t, provenance.SHA256(call.Stdin), request.OutputScopes)}, nil
				})
				tool := toolchain.Tool{ID: "api", Version: "v2", InputScopes: apiTestInputPaths(request.StaticInputs), WriteScopes: apiTestScopePaths(request.OutputScopes), Probe: toolchain.ExecutableProbe{ExpectedVersion: "api-v2"}}
				if _, err := apigo.RunDirectAPIGo(context.Background(), request, apigo.DirectOptions{RepositoryRoot: repository, Tool: tool, Runner: runner, OutputScopes: request.OutputScopes}); err == nil {
					t.Fatal("accepted changed manual file")
				}
			})
		}
	}
}

func TestAPIGoV2SchemasRejectEveryRepositoryPathEscapeAndOpenNestedIR(t *testing.T) {
	request := validDirectAPIRequest(t)
	canonical, err := apigo.CanonicalAPIGoRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"/absolute", `backend\\escape`, ".", "..", "backend/../escape", "backend/.git/output"} {
		t.Run(invalid, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(canonical, &document); err != nil {
				t.Fatal(err)
			}
			document["apiEntry"] = invalid
			document["staticInputs"].([]any)[0].(map[string]any)["path"] = invalid
			encoded, _ := json.Marshal(document)
			if _, err := apigo.ParseAPIGoRequest(encoded); err == nil {
				t.Fatal("accepted invalid repository path")
			}
		})
	}
	var open map[string]any
	_ = json.Unmarshal(canonical, &open)
	open["httpAPIIR"].(map[string]any)["unknown"] = true
	encoded, _ := json.Marshal(open)
	if _, err := apigo.ParseAPIGoRequest(encoded); err == nil {
		t.Fatal("accepted open nested HTTPAPIIR")
	}
}

func TestAPIGoV2SchemasReuseDG0RepositoryPath(t *testing.T) {
	want := schemaDefinition(t, directwrite.GenerationResultSchema(), "repositoryPath")
	for _, schema := range [][]byte{apigo.APIGoRequestSchema(), apigo.APIGoResultSchema()} {
		if got := schemaDefinition(t, schema, "repositoryPath"); !reflect.DeepEqual(got, want) {
			t.Fatalf("repositoryPath drift: got %#v want %#v", got, want)
		}
		var document map[string]any
		if err := json.Unmarshal(schema, &document); err != nil {
			t.Fatal(err)
		}
		for _, annotation := range []string{"x-nexa-unicode-casefold-uniqueness", "x-nexa-path-topology", "x-nexa-canonical-order"} {
			if _, ok := document[annotation]; !ok {
				t.Fatalf("schema missing %s", annotation)
			}
		}
	}
	result, err := apigo.CanonicalAPIGoResult(apigo.APIGoResult{APIVersion: apigo.APIGoResultAPIVersion, Kind: apigo.APIGoResultKind, Status: apigo.APIGoResultGenerated, CoreServiceID: "core", InputDigest: provenance.SHA256(nil), OutputScopes: []directwrite.OutputScope{{Path: "backend/core/generated", Mode: directwrite.OutputModeReplaceTree}}})
	if err != nil {
		t.Fatal(err)
	}
	var resultDocument map[string]any
	_ = json.Unmarshal(result, &resultDocument)
	resultDocument["outputScopes"].([]any)[0].(map[string]any)["path"] = "../escape"
	result, _ = json.Marshal(resultDocument)
	if _, err := apigo.ParseAPIGoResult(result); err == nil {
		t.Fatal("accepted invalid result repository path")
	}
}

func schemaDefinition(t *testing.T, schema []byte, name string) any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(schema, &document); err != nil {
		t.Fatal(err)
	}
	return document["$defs"].(map[string]any)[name]
}

func TestRunDirectAPIGoProjectsEveryPostInvocationFailure(t *testing.T) {
	tests := []struct {
		name        string
		wantReason  string
		wantPointer string
		mutate      func(*testing.T, toolchain.DirectRequest, *toolchain.Result)
	}{
		{name: "invalid identity", wantReason: "process_identity_invalid", wantPointer: "/process", mutate: func(_ *testing.T, _ toolchain.DirectRequest, result *toolchain.Result) { result.ToolID = "other" }},
		{name: "invalid result JSON", wantReason: "result_invalid", wantPointer: "/stdout", mutate: func(_ *testing.T, _ toolchain.DirectRequest, result *toolchain.Result) { result.Stdout = []byte("{") }},
		{name: "noncanonical result", wantReason: "result_invalid", wantPointer: "/stdout", mutate: func(_ *testing.T, _ toolchain.DirectRequest, result *toolchain.Result) {
			result.Stdout = append(result.Stdout, '\n')
		}},
		{name: "wrong digest acknowledgement", wantReason: "result_acknowledgement_invalid", wantPointer: "/result", mutate: func(t *testing.T, _ toolchain.DirectRequest, result *toolchain.Result) {
			result.Stdout = canonicalAPIDirectResult(t, provenance.SHA256([]byte("wrong")), validDirectAPIRequest(t).OutputScopes)
		}},
		{name: "wrong scope acknowledgement", wantReason: "result_acknowledgement_invalid", wantPointer: "/result", mutate: func(t *testing.T, call toolchain.DirectRequest, result *toolchain.Result) {
			wrong := []directwrite.OutputScope{{Path: "backend/core/manual", Mode: directwrite.OutputModeFileSet}}
			result.Stdout = canonicalAPIDirectResult(t, provenance.SHA256(call.Stdin), wrong)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validDirectAPIRequest(t)
			repository, _ := filepath.EvalSymlinks(t.TempDir())
			entry := filepath.Join(repository, filepath.FromSlash(request.APIEntry))
			if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(entry, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			calls := 0
			runner := toolchain.DirectRunnerFunc(func(_ context.Context, call toolchain.DirectRequest) (toolchain.Result, error) {
				calls++
				result := toolchain.Result{ToolID: "api", Version: "v2", ExecutableVersion: "api-v2", Stdout: canonicalAPIDirectResult(t, provenance.SHA256(call.Stdin), request.OutputScopes)}
				test.mutate(t, call, &result)
				return result, nil
			})
			tool := toolchain.Tool{ID: "api", Version: "v2", InputScopes: apiTestInputPaths(request.StaticInputs), WriteScopes: apiTestScopePaths(request.OutputScopes), Probe: toolchain.ExecutableProbe{ExpectedVersion: "api-v2"}}
			_, err := apigo.RunDirectAPIGo(context.Background(), request, apigo.DirectOptions{RepositoryRoot: repository, Tool: tool, Runner: runner, OutputScopes: request.OutputScopes})
			if calls != 1 {
				t.Fatalf("runner calls = %d", calls)
			}
			assertAPIPostInvocationEvidence(t, err, test.wantReason, test.wantPointer)
		})
	}
}

func TestRunDirectAPIGoPreservesRunnerNotStartedEvidence(t *testing.T) {
	request := validDirectAPIRequest(t)
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	entry := filepath.Join(repository, filepath.FromSlash(request.APIEntry))
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := toolchain.Tool{ID: "api", Version: "v2", Executable: filepath.Join(repository, "missing"), InputScopes: apiTestInputPaths(request.StaticInputs), WriteScopes: apiTestScopePaths(request.OutputScopes), Probe: toolchain.ExecutableProbe{Args: []string{"--version"}, ExpectedVersion: "api-v2"}}
	_, err := apigo.RunDirectAPIGo(context.Background(), request, apigo.DirectOptions{RepositoryRoot: repository, Tool: tool, Runner: toolchain.NewExecDirectRunner(), OutputScopes: request.OutputScopes})
	var typed *toolchain.Error
	if !errors.As(err, &typed) || typed.Stage() != "probe" || typed.Started() || typed.MayHaveWritten() {
		t.Fatalf("runner not-started error = %#v", err)
	}
}

func TestAPIGoV2RejectsUnknownFieldsUnsortedInputsAndScopeEscape(t *testing.T) {
	request := validDirectAPIRequest(t)
	canonical, _ := apigo.CanonicalAPIGoRequest(request)
	var document map[string]any
	_ = json.Unmarshal(canonical, &document)
	document["manual"] = true
	foreign, _ := json.Marshal(document)
	if _, err := apigo.ParseAPIGoRequest(foreign); err == nil {
		t.Fatal("accepted manual field")
	}
	request.OutputScopes[0].Path = "backend/.GiT/generated"
	if _, err := apigo.CanonicalAPIGoRequest(request); err == nil {
		t.Fatal("accepted denied scope")
	}
	request = validDirectAPIRequest(t)
	request.StaticInputs = []apigo.StaticInput{{ID: "z", Path: "backend/core/z.api", Digest: provenance.SHA256(nil)}, {ID: "a", Path: "backend/core/a.api", Digest: provenance.SHA256(nil)}}
	request.APIEntry = "backend/core/a.api"
	canonical, err := apigo.CanonicalAPIGoRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Index(canonical, []byte(`"id":"a"`)) > bytes.Index(canonical, []byte(`"id":"z"`)) {
		t.Fatal("static inputs are not canonical")
	}
	request = validDirectAPIRequest(t)
	request.StaticInputs = append(request.StaticInputs, apigo.StaticInput{ID: "folded", Path: "backend/core/API/generate\u0301d.api", Digest: provenance.SHA256(nil)})
	request.StaticInputs[0].Path = "backend/core/api/generat\u00e9d.api"
	request.APIEntry = request.StaticInputs[0].Path
	if _, err := apigo.CanonicalAPIGoRequest(request); err == nil {
		t.Fatal("accepted NFC/case-fold colliding static inputs")
	}
	request = validDirectAPIRequest(t)
	request.StaticInputs = append(request.StaticInputs, apigo.StaticInput{ID: "child", Path: request.StaticInputs[0].Path + "/child.api", Digest: provenance.SHA256(nil)})
	if _, err := apigo.CanonicalAPIGoRequest(request); err == nil {
		t.Fatal("accepted ancestor/descendant static input topology")
	}
}

func TestRunDirectAPIGoRejectsFixedArgsAndStaticSymlink(t *testing.T) {
	request := validDirectAPIRequest(t)
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	entry := filepath.Join(repository, filepath.FromSlash(request.APIEntry))
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repository, "target.api")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, entry); err != nil {
		t.Fatal(err)
	}
	called := false
	runner := toolchain.DirectRunnerFunc(func(context.Context, toolchain.DirectRequest) (toolchain.Result, error) {
		called = true
		return toolchain.Result{}, nil
	})
	options := apigo.DirectOptions{RepositoryRoot: repository, Tool: toolchain.Tool{ID: "api", Version: "v2", InputScopes: apiTestInputPaths(request.StaticInputs), WriteScopes: []string{"backend/core/generated"}}, Runner: runner, OutputScopes: request.OutputScopes}
	options.Tool.Args = []string{"wrapper"}
	if _, err := apigo.RunDirectAPIGo(context.Background(), request, options); err == nil || called {
		t.Fatalf("fixed args rejection = %v, called = %v", err, called)
	} else {
		assertAPIPreInvocationError(t, err)
	}
	options.Tool.Args = nil
	if _, err := apigo.RunDirectAPIGo(context.Background(), request, options); err == nil || called {
		t.Fatalf("symlink rejection = %v, called = %v", err, called)
	} else {
		assertAPIPreInvocationError(t, err)
	}
}

func TestAPIGoV2RejectsStaticInputOutputScopeTopology(t *testing.T) {
	tests := []struct {
		name       string
		staticPath string
		scopePath  string
	}{
		{name: "exact", staticPath: "backend/core/generated", scopePath: "backend/core/generated"},
		{name: "static ancestor", staticPath: "backend/core/generated", scopePath: "backend/core/generated/output"},
		{name: "scope ancestor", staticPath: "backend/core/generated/input.api", scopePath: "backend/core/generated"},
		{name: "casefold", staticPath: "backend/core/Generated/input.api", scopePath: "backend/core/generated"},
		{name: "NFC", staticPath: "backend/core/ge\u0301nerated/input.api", scopePath: "backend/core/g\u00e9nerated"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validDirectAPIRequest(t)
			request.StaticInputs = append(request.StaticInputs, apigo.StaticInput{ID: "collision", Path: test.staticPath, Digest: provenance.SHA256(nil)})
			request.OutputScopes[0].Path = test.scopePath
			if _, err := apigo.CanonicalAPIGoRequest(request); err == nil {
				t.Fatal("accepted static input/output scope topology overlap")
			}

			called := false
			runner := toolchain.DirectRunnerFunc(func(context.Context, toolchain.DirectRequest) (toolchain.Result, error) {
				called = true
				return toolchain.Result{}, nil
			})
			options := apigo.DirectOptions{
				RepositoryRoot: t.TempDir(),
				Tool:           toolchain.Tool{ID: "api", Version: "v2", WriteScopes: []string{test.scopePath}},
				Runner:         runner,
				OutputScopes:   request.OutputScopes,
			}
			if _, err := apigo.RunDirectAPIGo(context.Background(), request, options); err == nil || called {
				t.Fatalf("RunDirectAPIGo overlap rejection = %v, called = %v", err, called)
			}
		})
	}
}

func validDirectAPIRequest(t *testing.T) apigo.APIGoRequest {
	return apigo.APIGoRequest{APIVersion: apigo.APIGoRequestAPIVersion, Kind: apigo.APIGoRequestKind, CoreServiceID: "core", ModulePath: "example.com/consumer", HTTPAPIIR: apiSnapshot(t), APIEntry: "backend/core/api/generated.api", StaticInputs: []apigo.StaticInput{{ID: "entry", Path: "backend/core/api/generated.api", Digest: provenance.SHA256(nil)}}, OutputScopes: []directwrite.OutputScope{{Path: "backend/core/generated", Mode: directwrite.OutputModeReplaceTree}}}
}

func canonicalAPIDirectResult(t *testing.T, digest provenance.Digest, scopes []directwrite.OutputScope) []byte {
	t.Helper()
	encoded, err := apigo.CanonicalAPIGoResult(apigo.APIGoResult{APIVersion: apigo.APIGoResultAPIVersion, Kind: apigo.APIGoResultKind, Status: apigo.APIGoResultGenerated, CoreServiceID: "core", InputDigest: digest, OutputScopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func apiTestScopePaths(scopes []directwrite.OutputScope) []string {
	paths := make([]string, len(scopes))
	for index, scope := range scopes {
		paths[index] = scope.Path
	}
	return paths
}

func apiTestInputPaths(inputs []apigo.StaticInput) []string {
	paths := make([]string, len(inputs))
	for index, input := range inputs {
		paths[index] = input.Path
	}
	return paths
}

func assertAPIPostInvocationEvidence(t *testing.T, err error, reason, pointer string) {
	t.Helper()
	var typed *toolchain.Error
	if !errors.As(err, &typed) || typed.Code() != "tool_output_invalid" || typed.Stage() != "result" || typed.Reason() != reason || typed.Pointer() != pointer || typed.Source() != "" || typed.ToolID() != "api" || typed.ExitCode() != 0 || !typed.Started() || !typed.MayHaveWritten() {
		t.Fatalf("post-invocation error = %#v", err)
	}
}

func assertAPIPreInvocationError(t *testing.T, err error) {
	t.Helper()
	var typed *toolchain.Error
	if errors.As(err, &typed) && (typed.Started() || typed.MayHaveWritten()) {
		t.Fatalf("pre-invocation error has write evidence: %#v", err)
	}
}

func apiSnapshot(t *testing.T) httpapi.Snapshot {
	t.Helper()
	document, _, _ := compositionFixture(t)
	canonical, err := httpapi.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := provenance.ParseDomainSource("facts/core/http-api-ir.json")
	snapshot, err := httpapi.ParseSnapshot(source, canonical)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

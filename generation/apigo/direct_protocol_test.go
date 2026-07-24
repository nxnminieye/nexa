package apigo_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	result, err := apigo.RunDirectAPIGo(context.Background(), request, apigo.DirectOptions{RepositoryRoot: repository, Tool: toolchain.Tool{ID: "api", Version: "v2", WriteScopes: []string{scopes[0].Path}, Probe: toolchain.ExecutableProbe{ExpectedVersion: "api-v2"}}, Runner: runner, OutputScopes: scopes})
	if err != nil || result.InputDigest != provenance.SHA256(stdin) {
		t.Fatalf("RunDirectAPIGo = %#v, %v", result, err)
	}
	invalidRunner := toolchain.DirectRunnerFunc(func(_ context.Context, call toolchain.DirectRequest) (toolchain.Result, error) {
		encoded, resultErr := apigo.CanonicalAPIGoResult(apigo.APIGoResult{APIVersion: apigo.APIGoResultAPIVersion, Kind: apigo.APIGoResultKind, Status: apigo.APIGoResultGenerated, CoreServiceID: "core", InputDigest: provenance.SHA256([]byte("different stdin")), OutputScopes: scopes})
		return toolchain.Result{ToolID: "api", Version: "v2", ExecutableVersion: "api-v2", Stdout: encoded}, resultErr
	})
	if _, err := apigo.RunDirectAPIGo(context.Background(), request, apigo.DirectOptions{RepositoryRoot: repository, Tool: toolchain.Tool{ID: "api", Version: "v2", WriteScopes: []string{scopes[0].Path}, Probe: toolchain.ExecutableProbe{ExpectedVersion: "api-v2"}}, Runner: invalidRunner, OutputScopes: scopes}); err == nil {
		t.Fatal("accepted result digest that does not match exact stdin")
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
}

func validDirectAPIRequest(t *testing.T) apigo.APIGoRequest {
	return apigo.APIGoRequest{APIVersion: apigo.APIGoRequestAPIVersion, Kind: apigo.APIGoRequestKind, CoreServiceID: "core", ModulePath: "example.com/consumer", HTTPAPIIR: apiSnapshot(t), APIEntry: "backend/core/api/generated.api", StaticInputs: []apigo.StaticInput{{ID: "entry", Path: "backend/core/api/generated.api", Digest: provenance.SHA256(nil)}}, OutputScopes: []directwrite.OutputScope{{Path: "backend/core/generated", Mode: directwrite.OutputModeReplaceTree}}}
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

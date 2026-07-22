package composition_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/provenance"
	goctlparser "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/parser"
)

func TestRenderProducesParseableAndExecutableStaticSources(t *testing.T) {
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, validProtocolSource(false))}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := composition.Render(document, composition.RenderOptions{CoreRoot: "backend/core"})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	wantPaths := []string{
		"backend/core/desc/generated/account.generated.api",
		"backend/core/internal/logic/rpcproxy/account-get.generated.go",
		"backend/core/internal/rpcproxy/generated/register.generated.go",
		"backend/core/internal/serviceclients/account/client.generated.go",
		"backend/core/internal/serviceclients/account/errors.generated.go",
		"backend/core/internal/serviceclients/account/mapper.generated.go",
	}
	gotPaths := make([]string, len(artifacts))
	for index, artifact := range artifacts {
		gotPaths[index] = artifact.Path
		if len(artifact.Content) == 0 || len(artifact.Sources) == 0 || artifact.ID == "" || artifact.Owner == "" {
			t.Fatalf("artifact %d is incomplete: %#v", index, artifact)
		}
		if filepath.Ext(artifact.Path) == ".go" {
			if _, err := parser.ParseFile(token.NewFileSet(), artifact.Path, artifact.Content, parser.AllErrors); err != nil {
				t.Fatalf("ParseFile(%s): %v", artifact.Path, err)
			}
		} else {
			parsed, err := goctlparser.Parse("/virtual/generated.api", artifact.Content)
			if err != nil || parsed.Validate() != nil {
				t.Fatalf("parse generated API %s: %v", artifact.Path, err)
			}
		}
	}
	sort.Strings(gotPaths)
	sort.Strings(wantPaths)
	if !equalStrings(gotPaths, wantPaths) {
		t.Fatalf("artifact paths = %#v, want %#v", gotPaths, wantPaths)
	}

	first := append([]byte(nil), artifacts[0].Content...)
	firstSource := artifacts[0].Sources[0]
	artifacts[0].Content[0] ^= 0xff
	artifacts[0].Sources[0] = provenance.SourceRef{}
	again, err := composition.Render(document, composition.RenderOptions{CoreRoot: "backend/core"})
	if err != nil || !bytes.Equal(again[0].Content, first) || again[0].Sources[0] != firstSource {
		t.Fatalf("Render() aliases returned content: %v", err)
	}
	executeGeneratedModule(t, again)
}

func TestCompositionInjectsInt64TenantWithoutExposingHTTPField(t *testing.T) {
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, validProtocolSource(false))}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := composition.Render(document, composition.RenderOptions{CoreRoot: "backend/core"})
	if err != nil {
		t.Fatal(err)
	}
	var client, mapper []byte
	for _, artifact := range artifacts {
		switch artifact.Path {
		case "backend/core/internal/serviceclients/account/client.generated.go":
			client = artifact.Content
		case "backend/core/internal/serviceclients/account/mapper.generated.go":
			mapper = artifact.Content
		}
	}
	if !bytes.Contains(client, []byte("TenantID  int64")) || !bytes.Contains(client, []byte("TenantId  int64")) {
		t.Fatalf("generated client does not preserve int64 tenant: %s", client)
	}
	if bytes.Contains(mapper, []byte("type AccountGetHTTPRequest struct {\n\tTenant")) || !bytes.Contains(mapper, []byte("TenantId:  values.TenantID")) {
		t.Fatalf("generated mapper exposes or omits tenant context: %s", mapper)
	}
}

func TestRenderUsesOperationScopedRPCClientMethods(t *testing.T) {
	second := strings.Replace(validProtocolSource(false), "service AccountService {", `service LookupService {
  rpc Get(GetAccountRequest) returns (GetAccountResponse) {
    option (nexa.protocol.v1.rpc_context) = {
      context_fields: { source: TENANT_ID rpc_field: "tenant_id" }
      context_fields: { source: REQUEST_ID rpc_field: "request_id" }
      context_fields: { source: TRACE_ID rpc_field: "trace_id" }
    };
    option (nexa.protocol.v1.http_proxy) = {
      operation_id: "account.lookup" method: GET path: "/accounts/lookup/{id}"
      auth: { mode: REQUIRED credentials: { id: "primary" type: BEARER location: HEADER name: "Authorization" } }
      permission: "account.read"
      request_fields: { http_field: "id" rpc_field: "id" }
      response_fields: { rpc_field: "name" http_field: "name" }
      errors: { match: { domain: "account" code: "not_found" } project: { domain: "api" code: "account_not_found" http_status: 404 } }
    };
  }
}
service AccountService {`, 1)
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, second)}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := composition.Render(document, composition.RenderOptions{CoreRoot: "backend/core"})
	if err != nil {
		t.Fatal(err)
	}
	compileGeneratedModule(t, artifacts)
}

func executeGeneratedModule(t *testing.T, artifacts []composition.RenderedArtifact) {
	t.Helper()
	root := t.TempDir()
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	module := "module example.com/consumer\n\ngo 1.25.0\n\nrequire github.com/nxnminieye/nexa v0.0.0\nreplace github.com/nxnminieye/nexa => " + filepath.ToSlash(repository) + "\n"
	writeGeneratedFile(t, root, "go.mod", []byte(module))
	for _, artifact := range artifacts {
		if filepath.Ext(artifact.Path) == ".go" {
			writeGeneratedFile(t, root, artifact.Path, artifact.Content)
		}
	}
	testSource := []byte(`package accountclient

import (
  "bytes"
  "net/http/httptest"
  "testing"
  sdkapi "github.com/nxnminieye/nexa/sdk/api"
)

func TestGeneratedMapperAndErrorAdapter(t *testing.T) {
  request := MapAccountGetRequest(AccountGetHTTPRequest{Id: "acct-1"}, RequestContext{TenantID: 42})
  if request.Id != "acct-1" || request.TenantId != 42 { t.Fatalf("request = %#v", request) }
  response := MapAccountGetResponse(AccountGetRPCResponse{Name: "Ada"})
  if response.Name != "Ada" { t.Fatalf("response = %#v", response) }

  projected, err := ProjectAccountGetError(RPCError{Domain: "account", Code: "not_found", Message: "raw database cause", DetailsJSON: []byte(` + "`" + `{"secret":"database"}` + "`" + `)}, RequestContext{RequestID: "request-1", TraceID: "trace-1"})
  if err != nil || projected.Status != 404 || projected.ContentType != "application/json" { t.Fatalf("projected = %#v, %v", projected, err) }
  recorder := httptest.NewRecorder()
  if err := projected.WriteHTTP(recorder); err != nil { t.Fatal(err) }
  if recorder.Code != 404 || recorder.Header().Get("Content-Type") != "application/json" { t.Fatalf("written response = %d %#v", recorder.Code, recorder.Header()) }
  if bytes.Contains(recorder.Body.Bytes(), []byte("database")) || bytes.Contains(recorder.Body.Bytes(), []byte("secret")) { t.Fatalf("mapped projection leaked RPC error: %s", recorder.Body.Bytes()) }
  remote, err := sdkapi.ParseRemoteError(recorder.Body.Bytes())
  if err != nil || remote.Domain() != "api" || remote.Code() != "account_not_found" || remote.Message() != "request failed" || remote.RequestID() != "request-1" || remote.TraceID() != "trace-1" { t.Fatalf("remote = %#v, %v", remote, err) }
  independent, _ := sdkapi.NewRemoteError(sdkapi.RemoteErrorSpec{Domain: "api", Code: "account_not_found", Message: "request failed", RequestID: "request-1", TraceID: "trace-1"})
  canonical, _ := independent.CanonicalJSON()
  if !bytes.Equal(projected.Body, canonical) { t.Fatalf("body = %s, want %s", projected.Body, canonical) }

  hidden, err := ProjectAccountGetError(RPCError{Domain: "secret", Code: "boom", Message: "raw database cause"}, RequestContext{RequestID: "request-2", TraceID: "trace-2"})
  if err != nil || hidden.Status != 500 || bytes.Contains(hidden.Body, []byte("database")) { t.Fatalf("hidden = %#v, %v", hidden, err) }
  safe, err := sdkapi.ParseRemoteError(hidden.Body)
  if err != nil || safe.Domain() != "internal" || safe.Code() != "internal" || safe.Message() != "internal error" { t.Fatalf("safe = %#v, %v", safe, err) }
}
`)
	writeGeneratedFile(t, root, "backend/core/internal/serviceclients/account/generated_behavior_test.go", testSource)
	logicTestSource := []byte(`package rpcproxy

import (
  "bytes"
  "context"
  "net/http"
  "net/http/httptest"
  "testing"
  accountclient "example.com/consumer/backend/core/internal/serviceclients/account"
  sdkapi "github.com/nxnminieye/nexa/sdk/api"
)

type failingAccountClient struct{}
func (failingAccountClient) AccountGet(context.Context, accountclient.AccountGetRPCRequest) (accountclient.AccountGetRPCResponse, error) {
  return accountclient.AccountGetRPCResponse{}, accountclient.RPCError{Domain: "account", Code: "not_found", Message: "raw database cause", DetailsJSON: []byte(` + "`" + `{"secret":"database"}` + "`" + `)}
}
type fixedContextReader struct{}
func (fixedContextReader) Read(context.Context) (accountclient.RequestContext, error) {
  return accountclient.RequestContext{TenantID: 42, RequestID: "request-1", TraceID: "trace-1"}, nil
}

func TestGeneratedLogicReturnsWritableProjectedError(t *testing.T) {
  logic := NewAccountGetLogic(failingAccountClient{}, fixedContextReader{})
  _, err := logic.Execute(context.Background(), accountclient.AccountGetHTTPRequest{Id: "acct-1"})
  projected, ok := err.(interface{ WriteHTTP(http.ResponseWriter) error })
  if !ok { t.Fatalf("logic error = %T %v", err, err) }
  recorder := httptest.NewRecorder()
  if err := projected.WriteHTTP(recorder); err != nil { t.Fatal(err) }
  if recorder.Code != 404 || recorder.Header().Get("Content-Type") != "application/json" || bytes.Contains(recorder.Body.Bytes(), []byte("database")) { t.Fatalf("response = %d %#v %s", recorder.Code, recorder.Header(), recorder.Body.Bytes()) }
  remote, err := sdkapi.ParseRemoteError(recorder.Body.Bytes())
  if err != nil || remote.Code() != "account_not_found" || remote.RequestID() != "request-1" || remote.TraceID() != "trace-1" { t.Fatalf("remote = %#v, %v", remote, err) }
}
`)
	writeGeneratedFile(t, root, "backend/core/internal/logic/rpcproxy/generated_behavior_test.go", logicTestSource)
	runGeneratedModule(t, root)
}

func compileGeneratedModule(t *testing.T, artifacts []composition.RenderedArtifact) {
	t.Helper()
	root := t.TempDir()
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	module := "module example.com/consumer\n\ngo 1.25.0\n\nrequire github.com/nxnminieye/nexa v0.0.0\nreplace github.com/nxnminieye/nexa => " + filepath.ToSlash(repository) + "\n"
	writeGeneratedFile(t, root, "go.mod", []byte(module))
	for _, artifact := range artifacts {
		if filepath.Ext(artifact.Path) == ".go" {
			writeGeneratedFile(t, root, artifact.Path, artifact.Content)
		}
	}
	runGeneratedModule(t, root)
}

func runGeneratedModule(t *testing.T, root string) {
	t.Helper()
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOENV=off", "GOPROXY=off", "GOSUMDB=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated module go test: %v\n%s", err, output)
	}
}

func writeGeneratedFile(t *testing.T, root, name string, content []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

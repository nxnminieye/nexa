package composition_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	generationapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/project/servicecatalog"
	"github.com/nxnminieye/nexa/provenance"
	goctlparser "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/parser"
)

func TestRenderScalarArtifactCompatibilityOracle(t *testing.T) {
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, validProtocolSource(false))}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := composition.Render(document, composition.RenderOptions{CoreRoot: "backend/core"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"backend/core/desc/generated/account.generated.api":                "cb70adc0dd2d720ea56126a2928d1187cdd092a021faa3c2badaa3da9026b702",
		"backend/core/internal/logic/rpcproxy/account-get.generated.go":    "ee03b2d086728a2d9d5ee9f49bcb98384f3505956e297bb3d68f21ea90b4bc45",
		"backend/core/internal/rpcproxy/generated/register.generated.go":   "42d9d3bf2b07732b3d4fa2deb91e0290aeeece75ffe7eeaff48f9f54199fdfa9",
		"backend/core/internal/serviceclients/account/client.generated.go": "a79ce1e37c791b148768ddd77125d3b3dd9fceb24ddd6a76f6cf59ee9791c778",
		"backend/core/internal/serviceclients/account/errors.generated.go": "f9f172050448c78245f5b2e945d1b75fddec14ac0f0aff8036cc202f9f874835",
		"backend/core/internal/serviceclients/account/mapper.generated.go": "152d07dc2e341b5d81e0f897939c7dee07d00626ae548b1074916a64ecedf52b",
	}
	if len(artifacts) != len(want) {
		t.Fatalf("scalar artifact count = %d, want %d", len(artifacts), len(want))
	}
	seen := make(map[string]bool, len(artifacts))
	for _, artifact := range artifacts {
		expected, ok := want[artifact.Path]
		if !ok {
			t.Fatalf("unexpected scalar artifact %q", artifact.Path)
		}
		digest := sha256.Sum256(artifact.Content)
		if actual := fmt.Sprintf("%x", digest); actual != expected {
			t.Fatalf("scalar artifact %q SHA-256 = %s, want %s", artifact.Path, actual, expected)
		}
		seen[artifact.Path] = true
	}
	for artifactPath := range want {
		if !seen[artifactPath] {
			t.Fatalf("missing scalar artifact %q", artifactPath)
		}
	}
}

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

func TestCompositionRendersStringTenantRequestContext(t *testing.T) {
	source := strings.Replace(validProtocolSource(false), "int64 tenant_id = 2;", "string tenant_id = 2;", 1)
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, source)}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := composition.Render(document, composition.RenderOptions{CoreRoot: "backend/core"})
	if err != nil {
		t.Fatal(err)
	}
	client := renderedByID(t, artifacts, "client.account")
	mapper := renderedByID(t, artifacts, "mapper.account")
	if !bytes.Contains(client.Content, []byte("TenantID  string")) || !bytes.Contains(client.Content, []byte("TenantId  string")) {
		t.Fatalf("generated client does not use string tenant: %s", client.Content)
	}
	if !bytes.Contains(mapper.Content, []byte("TenantId:  values.TenantID")) {
		t.Fatalf("generated mapper omits string tenant context: %s", mapper.Content)
	}
	compileGeneratedModule(t, artifacts)
}

func TestCompositionPreservesIndependentTenantTypesAcrossServices(t *testing.T) {
	accountSource := strings.Replace(validProtocolSource(false), "int64 tenant_id = 2;", "string tenant_id = 2;", 1)
	billingSource := strings.ReplaceAll(validProtocolSource(false), "account.v1", "billing.v1")
	billingSource = strings.ReplaceAll(billingSource, "account.get", "billing.get")
	billingSource = strings.ReplaceAll(billingSource, "/accounts/{id}", "/billing/{id}")
	billingSource = strings.ReplaceAll(billingSource, "AccountService", "BillingService")
	account := compileProtocolForService(t, "account", accountSource)
	billing := compileProtocolForService(t, "billing", billingSource)

	for serviceID, document := range map[string]protocol.Document{"account": account, "billing": billing} {
		canonical, err := protocol.CanonicalJSON(document)
		if err != nil {
			t.Fatalf("CanonicalJSON(%s): %v", serviceID, err)
		}
		source, err := provenance.ParseDomainSource("generated/protocol-" + serviceID + ".json")
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := protocol.ParseSnapshot(source, canonical)
		if err != nil {
			t.Fatalf("ParseSnapshot(%s): %v", serviceID, err)
		}
		roundTrip, err := snapshot.CanonicalJSON()
		if err != nil || !bytes.Equal(roundTrip, canonical) {
			t.Fatalf("Protocol snapshot %s changed canonical bytes: %v", serviceID, err)
		}
	}

	document, err := composition.Build(objectCatalog(t), []protocol.Document{account, billing}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := composition.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	compositionSource, _ := provenance.ParseDomainSource("generated/composition-independent-tenants.json")
	snapshot, err := composition.ParseSnapshot(compositionSource, canonical)
	if err != nil {
		t.Fatalf("ParseSnapshot(composition): %v", err)
	}
	roundTrip, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(roundTrip, canonical) {
		t.Fatalf("Composition snapshot changed canonical bytes: %v", err)
	}

	artifacts, err := composition.Render(document, composition.RenderOptions{CoreRoot: "backend/core"})
	if err != nil {
		t.Fatal(err)
	}
	accountClient := renderedByID(t, artifacts, "client.account")
	billingClient := renderedByID(t, artifacts, "client.billing")
	if !bytes.Contains(accountClient.Content, []byte("TenantID  string")) || !bytes.Contains(billingClient.Content, []byte("TenantID  int64")) {
		t.Fatalf("generated RequestContext tenant types are wrong:\n%s\n%s", accountClient.Content, billingClient.Content)
	}
	compileGeneratedModule(t, artifacts)
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

func TestRenderProjectsObjectCollectionsAndServiceScopedSources(t *testing.T) {
	account := compileProtocolForService(t, "account", objectProtocolSource())
	billingSource := strings.ReplaceAll(objectProtocolSource(), "account.v1", "billing.v1")
	billingSource = strings.ReplaceAll(billingSource, "account.replace", "billing.replace")
	billingSource = strings.ReplaceAll(billingSource, "/accounts/replace", "/billing/replace")
	billingSource = strings.ReplaceAll(billingSource, "AccountService", "BillingService")
	billing := compileProtocolForService(t, "billing", billingSource)
	catalog := objectCatalog(t)
	document, err := composition.Build(catalog, []protocol.Document{account, billing}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := composition.Render(document, composition.RenderOptions{CoreRoot: "backend/core"})
	if err != nil {
		t.Fatal(err)
	}
	accountAPI := renderedByID(t, artifacts, "api.account")
	accountClient := renderedByID(t, artifacts, "client.account")
	accountMapper := renderedByID(t, artifacts, "mapper.account")
	accountLogic := renderedByID(t, artifacts, "logic.account.replace")
	accountErrors := renderedByID(t, artifacts, "errors.account")
	for _, artifact := range []composition.RenderedArtifact{accountAPI, accountClient, accountMapper, accountLogic} {
		assertSourceFragments(t, artifact.Sources,
			"field:account.v1.Member.id", "field:account.v1.Member.role_codes", "field:account.v1.Member.settings",
			"field:account.v1.ReplaceRequest.items", "field:account.v1.ReplaceRequest.role_codes", "field:account.v1.ReplaceRequest.settings",
			"field:account.v1.ReplaceResponse.items", "field:account.v1.ReplaceResponse.total", "field:account.v1.Settings.locale",
			"message:account.v1.Member", "message:account.v1.Settings", "method:account.v1.AccountService.Replace",
			"service:account/binding:"+composition.CapabilityID+"@"+composition.CapabilityVersion,
		)
	}
	assertSourceFragments(t, accountErrors.Sources,
		"field:account.v1.ReplaceRequest.items", "field:account.v1.ReplaceRequest.role_codes", "field:account.v1.ReplaceRequest.settings",
		"field:account.v1.ReplaceResponse.items", "field:account.v1.ReplaceResponse.total", "method:account.v1.AccountService.Replace",
		"service:account/binding:"+composition.CapabilityID+"@"+composition.CapabilityVersion,
	)
	for _, artifact := range artifacts {
		if strings.Contains(artifact.ID, "billing") {
			for _, ref := range artifact.Sources {
				if strings.Contains(ref.Fragment(), "account.v1") || strings.Contains(ref.Fragment(), "service:account/") {
					t.Fatalf("%s contains account source %s", artifact.ID, ref.String())
				}
			}
		}
	}
	if !bytes.Contains(accountAPI.Content, []byte("[]AccountAccountV1Member")) || !bytes.Contains(accountClient.Content, []byte("type AccountAccountV1Member struct")) {
		t.Fatalf("object projection missing from rendered sources:\n%s\n%s", accountAPI.Content, accountClient.Content)
	}
	executeObjectGeneratedModule(t, artifacts)

	changedAccountSource := strings.Replace(objectProtocolSource(), "message Settings { string locale = 1; }", "message Settings { string locale = 1; string timezone = 2; }", 1)
	changedAccount := compileProtocolForService(t, "account", changedAccountSource)
	changedDocument, err := composition.Build(catalog, []protocol.Document{changedAccount, billing}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	changedArtifacts, err := composition.Render(changedDocument, composition.RenderOptions{CoreRoot: "backend/core"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"api.billing", "client.billing", "mapper.billing", "logic.billing.replace", "errors.billing"} {
		assertArtifactStable(t, renderedByID(t, artifacts, id), renderedByID(t, changedArtifacts, id), catalog, []protocol.Document{account, billing}, []protocol.Document{changedAccount, billing})
	}
	assertArtifactStable(t, accountErrors, renderedByID(t, changedArtifacts, "errors.account"), catalog, []protocol.Document{account, billing}, []protocol.Document{changedAccount, billing})
	for _, id := range []string{"api.account", "client.account"} {
		before, after := renderedByID(t, artifacts, id), renderedByID(t, changedArtifacts, id)
		if bytes.Equal(before.Content, after.Content) || resolvedArtifactDigest(t, before, catalog, account, billing) == resolvedArtifactDigest(t, after, catalog, changedAccount, billing) {
			t.Fatalf("%s did not change for nested shape mutation", id)
		}
	}
	for _, id := range []string{"mapper.account", "logic.account.replace", "register"} {
		before, after := renderedByID(t, artifacts, id), renderedByID(t, changedArtifacts, id)
		if !bytes.Equal(before.Content, after.Content) || resolvedArtifactDigest(t, before, catalog, account, billing) == resolvedArtifactDigest(t, after, catalog, changedAccount, billing) {
			t.Fatalf("%s content/digest behavior is invalid", id)
		}
	}
}

func compileProtocolForService(t *testing.T, serviceID, source string) protocol.Document {
	t.Helper()
	entry := serviceID + "/v1/" + serviceID + ".proto"
	resolver := protocolResolver(func(_ context.Context, path string) (io.ReadCloser, error) {
		if path != entry {
			return nil, os.ErrNotExist
		}
		return io.NopCloser(strings.NewReader(source)), nil
	})
	document, err := protocol.Compile(context.Background(), protocol.CompileOptions{ServiceID: serviceID, EntryFiles: []string{entry}, Resolver: resolver})
	if err != nil {
		t.Fatalf("Compile %s protocol: %v", serviceID, err)
	}
	return document
}

func objectCatalog(t *testing.T) servicecatalog.Catalog {
	t.Helper()
	source := fmt.Sprintf(`apiVersion: nexa.dev/service-catalog/v1
kind: ServiceCatalog
services:
  - id: core
    root: backend/core
    capabilityBindings: []
  - id: account
    root: backend/account
    capabilityBindings:
      - id: %s
        apiVersion: %s
  - id: billing
    root: backend/billing
    capabilityBindings:
      - id: %s
        apiVersion: %s
`, composition.CapabilityID, composition.CapabilityVersion, composition.CapabilityID, composition.CapabilityVersion)
	catalog, err := servicecatalog.Parse("project/services.yaml", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func renderedByID(t *testing.T, artifacts []composition.RenderedArtifact, id string) composition.RenderedArtifact {
	t.Helper()
	for _, artifact := range artifacts {
		if artifact.ID == id {
			return artifact
		}
	}
	t.Fatalf("rendered artifact %q missing", id)
	return composition.RenderedArtifact{}
}

func assertSourceFragments(t *testing.T, refs []provenance.SourceRef, want ...string) {
	t.Helper()
	got := make([]string, len(refs))
	for index, ref := range refs {
		got[index] = ref.Fragment()
	}
	sort.Strings(got)
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Fatalf("source fragments = %#v, want %#v", got, want)
	}
}

func resolvedArtifactDigest(t *testing.T, artifact composition.RenderedArtifact, catalog servicecatalog.Catalog, protocols ...protocol.Document) provenance.Digest {
	t.Helper()
	index := map[string]provenance.Source{}
	for _, source := range catalog.Sources() {
		index[source.Ref.String()] = source
	}
	for _, document := range protocols {
		for _, source := range document.Sources() {
			index[source.Ref.String()] = source
		}
	}
	resolved := make([]provenance.Source, len(artifact.Sources))
	for itemIndex, ref := range artifact.Sources {
		source, ok := index[ref.String()]
		if !ok {
			t.Fatalf("source %s cannot be resolved", ref.String())
		}
		resolved[itemIndex] = source
	}
	digest, err := generationapi.ComputeSourceDigest(resolved)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func assertArtifactStable(t *testing.T, before, after composition.RenderedArtifact, catalog servicecatalog.Catalog, beforeProtocols, afterProtocols []protocol.Document) {
	t.Helper()
	if !bytes.Equal(before.Content, after.Content) || !equalSourceRefs(before.Sources, after.Sources) || resolvedArtifactDigest(t, before, catalog, beforeProtocols...) != resolvedArtifactDigest(t, after, catalog, afterProtocols...) {
		t.Fatalf("artifact %s changed across unrelated nested mutation", before.ID)
	}
}

func equalSourceRefs(left, right []provenance.SourceRef) bool {
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

func executeObjectGeneratedModule(t *testing.T, artifacts []composition.RenderedArtifact) {
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

import "testing"

func TestObjectCollectionMapper(t *testing.T) {
  empty := MapAccountReplaceRequest(AccountReplaceHTTPRequest{
    RoleCodes: []string{},
    Settings: AccountAccountV1Settings{Locale: "en"},
    Items: []AccountAccountV1Member{},
  }, RequestContext{})
  if empty.RoleCodes == nil || len(empty.RoleCodes) != 0 || empty.Items == nil || len(empty.Items) != 0 {
    t.Fatalf("empty request = %#v", empty)
  }
  items := []AccountAccountV1Member{{Id: "member-1", RoleCodes: []string{"admin", "reader"}, Settings: &AccountAccountV1Settings{Locale: "zh"}}}
  request := MapAccountReplaceRequest(AccountReplaceHTTPRequest{RoleCodes: []string{"owner", "auditor"}, Settings: AccountAccountV1Settings{Locale: "en"}, Items: items}, RequestContext{})
  if len(request.RoleCodes) != 2 || request.RoleCodes[0] != "owner" || request.RoleCodes[1] != "auditor" || len(request.Items) != 1 || request.Items[0].RoleCodes[1] != "reader" || request.Items[0].Settings.Locale != "zh" {
    t.Fatalf("request = %#v", request)
  }
  response := MapAccountReplaceResponse(AccountReplaceRPCResponse{Total: 1, Items: items})
  if response.Total != 1 || len(response.Items) != 1 || response.Items[0].Id != "member-1" || response.Items[0].RoleCodes[0] != "admin" {
    t.Fatalf("response = %#v", response)
  }
}
`)
	writeGeneratedFile(t, root, "backend/core/internal/serviceclients/account/object_behavior_test.go", testSource)
	runGeneratedModule(t, root)
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

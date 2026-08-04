package frontend_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/frontend"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/httpconvention"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

func TestParsePageSpecAcceptsOnlySourceCommentYAML(t *testing.T) {
	spec := mustSpec(t, pageSource(""))
	if spec.ID() != "accounts" || spec.SourceRef().String() == "" || spec.Digest().String() == "" {
		t.Fatalf("page source identity = %q %q %q", spec.ID(), spec.SourceRef().String(), spec.Digest().String())
	}
	if _, err := frontend.ParsePageSpec("frontend/accounts.page.json", []byte(`{"apiVersion":"nexa.dev/frontend-page-spec/v1"}`)); err == nil {
		t.Fatal("legacy JSON page spec accepted")
	}
	if _, err := frontend.ParsePageSpec("frontend/accounts.page.yaml", []byte("apiVersion: nexa.dev/frontend-source/v1\nkind: Page\nid: accounts\n")); err == nil {
		t.Fatal("page without ui.entity accepted")
	}
}

func TestBuildDerivesCRUDFieldsAndLocalesFromFacts(t *testing.T) {
	document, err := frontend.Build(withFacts(t, loadAPI(t, canonicalAPISource()), upstreamFacts(t)), []frontend.PageSpec{mustSpec(t, pageSource(""))})
	if err != nil {
		t.Fatal(err)
	}
	encoded := canonical(t, document)
	assertJCS(t, encoded)
	var wire struct {
		Operations []struct {
			ClientName string `json:"clientName"`
			ID         string `json:"id"`
		} `json:"operations"`
		Locales []struct {
			Locale   string            `json:"locale"`
			Messages map[string]string `json:"messages"`
		} `json:"locales"`
		Pages []struct {
			TitleKey           string                      `json:"titleKey"`
			Route              struct{ Path, Name string } `json:"route"`
			ExtensionComponent string                      `json:"extensionComponent"`
			Operations         map[string]string           `json:"operations"`
			Fields             []struct {
				Name, LabelKey, Control string
				Surfaces                []string
			} `json:"fields"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Pages) != 1 || wire.Pages[0].TitleKey != "account.label" || wire.Pages[0].Route.Path != "/system/accounts" || wire.Pages[0].Route.Name != "AccountsRoute" {
		t.Fatalf("derived page = %#v", wire.Pages)
	}
	operationIDs := make([]string, len(wire.Operations))
	clientNames := make([]string, len(wire.Operations))
	for index, operation := range wire.Operations {
		operationIDs[index] = operation.ID
		clientNames[index] = operation.ClientName
	}
	if got, want := strings.Join(operationIDs, ","), "accounts.createAccount,accounts.deleteAccount,accounts.getAccount,accounts.listAccounts,accounts.updateAccount"; got != want {
		t.Fatalf("operation closure = %s, want %s", got, want)
	}
	if got, want := strings.Join(clientNames, ","), "createAccount,deleteAccount,getAccount,listAccounts,updateAccount"; got != want {
		t.Fatalf("operation client names = %s, want %s", got, want)
	}
	fields := map[string]struct {
		control  string
		surfaces []string
	}{}
	for _, field := range wire.Pages[0].Fields {
		fields[field.Name] = struct {
			control  string
			surfaces []string
		}{field.Control, field.Surfaces}
	}
	if got := strings.Join(fields["name"].surfaces, ","); got != "list,create,edit" || fields["name"].control != "text" {
		t.Fatalf("name projection = %#v", fields["name"])
	}
	if got := strings.Join(fields["keyword"].surfaces, ","); got != "search" {
		t.Fatalf("keyword projection = %#v", fields["keyword"])
	}
	if len(wire.Locales) != 2 || wire.Locales[0].Messages["account.label"] != "Account" || wire.Locales[1].Messages["account.name.label"] != "名称" {
		t.Fatalf("derived locales = %#v", wire.Locales)
	}
	pages := document.Pages()
	if len(pages) != 1 || pages[0].Route != (frontend.Route{Name: "AccountsRoute", Path: "/system/accounts"}) || pages[0].Menu == nil || pages[0].Menu.TitleKey != "account.label" || pages[0].Operations.List != "accounts.listAccounts" {
		t.Fatalf("typed page readback = %#v", pages)
	}
	operations := document.Operations()
	if len(operations) != 5 || operations[0].ClientName != "createAccount" || operations[0].ID != "accounts.createAccount" || operations[0].Permission != "accounts.write" {
		t.Fatalf("typed operation readback = %#v", operations)
	}
	pageFact, ok := document.FactGraph().Fact(sourcecomment.FactID{SemanticID: "accounts", Key: "route.path"})
	if !ok || pageFact.FirstSource().Stage() != sourcecomment.StagePage {
		t.Fatalf("final source graph page fact = %#v, present=%v", pageFact, ok)
	}
	lock, err := document.FactGraph().ProjectionLock()
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.ValidateFactGraph(document.FactGraph()); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"listOperationId", "fields\":[]", "choices", "frontend-page-spec"} {
		if strings.Contains(string(encoded), removed) {
			t.Fatalf("removed authoring contract %q leaked into IR", removed)
		}
	}
}

func TestBuildPreservesTrustedExtensionFact(t *testing.T) {
	document, err := frontend.Build(withFacts(t, loadAPI(t, canonicalAPISource()), upstreamFacts(t)), []frontend.PageSpec{mustSpec(t, pageSource("# @nexa ui.extensionComponent: \"frontend/src/extensions/accounts/accounts-page.vue\"\n"))})
	if err != nil {
		t.Fatal(err)
	}
	if encoded := string(canonical(t, document)); !strings.Contains(encoded, `"extensionComponent":"frontend/src/extensions/accounts/accounts-page.vue"`) {
		t.Fatalf("extension component missing: %s", encoded)
	}
}

func TestBuildRejectsAmbiguousEntityList(t *testing.T) {
	source := canonicalAPISource() + `
type ListAccountCopiesRequest { Limit *int64 Offset *int64 }
type ListAccountCopiesResponse { Items []Account Total int32 }
@server (group: accountCopies)
service api {
  // @nexa auth: "required"
  // @nexa permission: "accounts.read"
  @handler ListAccountCopies
  get /account-copies (ListAccountCopiesRequest) returns (ListAccountCopiesResponse)
}
`
	closure, err := loadAPIResult(t, source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = frontend.Build(withFacts(t, closure, upstreamFacts(t)), []frontend.PageSpec{mustSpec(t, pageSource(""))})
	assertReason(t, err, "resource_operation_ambiguous")
}

func TestBuildNamesOperationsAndTypesBeforeSelectingFrontendClosure(t *testing.T) {
	source := canonicalAPISource() + `
type AssetListRequest { Limit *int64 Offset *int64 Keyword *string }
type Asset_Item { ID string }
type AssetListResponse { Items []Asset_Item Total int32 }
type RoleListRequest { Limit *int64 Offset *int64 Keyword *string }
type Role_Item { ID string }
type RoleListResponse { Items []Role_Item Total int32 }
@server (group: asset)
service api {
  // @nexa auth: "required"
  // @nexa permission: "assets.read"
  @handler list
  get /assets (AssetListRequest) returns (AssetListResponse)
}
@server (group: role)
service api {
  // @nexa auth: "required"
  // @nexa permission: "roles.read"
  @handler list
  get /roles (RoleListRequest) returns (RoleListResponse)
}
`
	closure := withFacts(t, loadAPI(t, source), upstreamFacts(t))
	document, err := frontend.BuildApplication(closure, nil, []string{"asset.list"})
	if err != nil {
		t.Fatal(err)
	}
	operations := document.Operations()
	if len(operations) != 1 || operations[0].ID != "asset.list" || operations[0].ClientName != "AssetList" {
		t.Fatalf("selected operation client identity = %#v", operations)
	}

	collisionSource := strings.ReplaceAll(source, "Role_Item", "AssetItem")
	collision := withFacts(t, loadAPI(t, collisionSource), upstreamFacts(t))
	_, err = frontend.BuildApplication(collision, nil, []string{"asset.list"})
	assertReason(t, err, "generated_type_name_collision")
}

func TestBuildAndRendererBoundaryHandleEmptyAndCanonicalInputs(t *testing.T) {
	emptyFacts, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	empty, err := frontend.Build(api.Closure{ConventionValue: httpconvention.APIVersion, FactGraphValue: emptyFacts}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if encoded := string(canonical(t, empty)); !strings.Contains(encoded, `"pages":[]`) || !strings.Contains(encoded, `"locales":[]`) {
		t.Fatalf("empty IR = %s", encoded)
	}
	document, err := frontend.Build(withFacts(t, loadAPI(t, canonicalAPISource()), upstreamFacts(t)), []frontend.PageSpec{mustSpec(t, pageSource(""))})
	if err != nil {
		t.Fatal(err)
	}
	request, err := frontend.CanonicalRenderRequest(frontend.RenderRequest{
		FrontendIR: document, RepositoryRoot: filepath.Clean(t.TempDir()), GeneratedScope: "frontend/src/generated",
		ExtensionScopes: []string{"frontend/src/extensions"}, FrontendSourceLockDigest: provenance.SHA256([]byte("frontend-source-lock")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := frontend.ValidateRendererInput(request); err != nil {
		t.Fatalf("canonical renderer request rejected: %v", err)
	}
	var decoded any
	for _, schema := range [][]byte{frontend.IRSchema(), frontend.RenderRequestSchema()} {
		if err := json.Unmarshal(schema, &decoded); err != nil {
			t.Fatalf("invalid embedded schema: %v", err)
		}
	}
}

func pageSource(extra string) []byte {
	return []byte(`# @nexa $contract: "nexa.dev/source-comment/v1"
apiVersion: nexa.dev/frontend-source/v1
kind: Page
# @nexa ui.entity: "Account"
# @nexa ui.pageSize: 25
# @nexa route.path: "/system/accounts"
# @nexa route.name: "AccountsRoute"
# @nexa route.icon: "lucide:users"
# @nexa menu.order: 10
` + extra + `id: accounts
`)
}

func upstreamFacts(t *testing.T) sourcecomment.FactGraph {
	t.Helper()
	type definition struct {
		id    string
		kind  sourcecomment.NodeKind
		stage sourcecomment.Stage
		facts []string
	}
	definitions := []definition{
		{"Account", sourcecomment.NodeSchema, sourcecomment.StageEnt, []string{`label.zh-CN: "账号"`, `label.en-US: "Account"`, `description.zh-CN: "账号"`, `description.en-US: "Account"`, `scope: "tenant"`, `crud.operations: ["list","get","create","update","delete"]`}},
		{"Account.id", sourcecomment.NodeField, sourcecomment.StageEnt, fieldDirectives("account.id", "readonly", "include", "none")},
		{"Account.name", sourcecomment.NodeField, sourcecomment.StageEnt, fieldDirectives("account.name", "text", "include", "create-update")},
		{"Account.status", sourcecomment.NodeField, sourcecomment.StageEnt, fieldDirectives("account.status", "select", "include", "create-update")},
		{"Account.version", sourcecomment.NodeField, sourcecomment.StageEnt, fieldDirectives("account.version", "number", "include", "none")},
	}
	inputs := make([]sourcecomment.NodeInput, 0, len(definitions))
	for index, definition := range definitions {
		ref, err := sourcecomment.ParseSourceRef(string(definition.stage) + "://facts/source.go#" + strings.ReplaceAll(definition.id, ".", "_"))
		if err != nil {
			t.Fatal(err)
		}
		target := sourcecomment.Target{SemanticID: definition.id, Kind: definition.kind, Stage: definition.stage, Source: ref}
		lines := []sourcecomment.Line{{Text: `// @nexa $contract: "nexa.dev/source-comment/v1"`, CommentPrefix: "//", Location: sourcecomment.Location{File: "facts/source.go", Line: 1}}}
		for line, fact := range definition.facts {
			lines = append(lines, sourcecomment.Line{Text: "// @nexa " + fact, CommentPrefix: "//", Location: sourcecomment.Location{File: "facts/source.go", Line: line + 2}, Target: &target})
		}
		parsed, diagnostics := sourcecomment.ParseFile(sourcecomment.StandardRegistry(), "facts/source.go", lines)
		if len(diagnostics) != 0 {
			t.Fatalf("parse facts %s: %#v", definition.id, diagnostics)
		}
		input := sourcecomment.NodeInput{SemanticID: definition.id, Kind: definition.kind, Stage: definition.stage, Source: ref, Location: sourcecomment.Location{File: "facts/source.go", Line: index + 1}, NativeCanonical: []byte(definition.id)}
		for _, fact := range parsed.Facts() {
			input.Facts = append(input.Facts, fact.Directive())
		}
		inputs = append(inputs, input)
	}
	graph, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: inputs})
	if len(diagnostics) != 0 {
		t.Fatalf("build facts: %#v", diagnostics)
	}
	return graph
}

func withFacts(t *testing.T, closure api.Closure, additional sourcecomment.FactGraph) api.Closure {
	t.Helper()
	merged, diagnostics := sourcecomment.MergeGraphs(sourcecomment.StandardRegistry(), closure.FactGraph(), additional)
	if len(diagnostics) != 0 {
		t.Fatalf("merge upstream facts: %#v", diagnostics)
	}
	closure.FactGraphValue = merged
	return closure
}

func fieldDirectives(prefix, control, read, mutation string) []string {
	return []string{`label.zh-CN: "` + map[string]string{"account.id": "编号", "account.name": "名称", "account.status": "状态", "account.version": "版本"}[prefix] + `"`, `label.en-US: "` + strings.Title(strings.TrimPrefix(prefix, "account.")) + `"`, `description.zh-CN: "字段"`, `description.en-US: "Field"`, `ui.control: "` + control + `"`, `visibility: "public"`, `crud.read: "` + read + `"`, `crud.mutation: "` + mutation + `"`}
}

func canonicalAPISource() string {
	return `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-convention/v1")
type ListAccountsRequest {
  Limit *int64
  Offset *int64
  // @nexa label.zh-CN: "关键词"
  // @nexa label.en-US: "Keyword"
  // @nexa description.zh-CN: "关键词"
  // @nexa description.en-US: "Keyword"
  // @nexa ui.control: "text"
  // @nexa visibility: "public"
  Keyword *string
}
type Account { ID string Name string Status string Version int64 }
type ListAccountsResponse { Items []Account Total int32 }
type CreateAccountRequest { Name string Status string }
type GetAccountRequest { ID string }
type UpdateAccountRequest { ID string Name string Status string }
type DeleteAccountRequest { ID string }
@server (group: accounts)
service api {
  // @nexa auth: "required"
  // @nexa permission: "accounts.read"
  @handler ListAccounts
  get /accounts (ListAccountsRequest) returns (ListAccountsResponse)
}
@server (group: accounts)
service api {
  // @nexa auth: "required"
  // @nexa permission: "accounts.write"
  @handler CreateAccount
  post /accounts (CreateAccountRequest) returns (Account)
}
@server (group: accounts)
service api {
  // @nexa auth: "required"
  // @nexa permission: "accounts.read"
  @handler GetAccount
  get /accounts/:id (GetAccountRequest) returns (Account)
}
@server (group: accounts)
service api {
  // @nexa auth: "required"
  // @nexa permission: "accounts.write"
  @handler UpdateAccount
  put /accounts/:id (UpdateAccountRequest) returns (Account)
}
@server (group: accounts)
service api {
  // @nexa auth: "required"
  // @nexa permission: "accounts.write"
  @handler DeleteAccount
  delete /accounts/:id (DeleteAccountRequest)
}
`
}

func loadAPI(t *testing.T, source string) api.Closure {
	t.Helper()
	value, err := loadAPIResult(t, source)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func loadAPIResult(t *testing.T, source string) (api.Closure, error) {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "api.api"), []byte(source), 0o600); err != nil {
		return api.Closure{}, err
	}
	document, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: directory, EntryFile: "api.api"})
	if err != nil {
		return api.Closure{}, err
	}
	return composition.FrontendClosure(document)
}
func mustSpec(t *testing.T, data []byte) frontend.PageSpec {
	t.Helper()
	value, err := frontend.ParsePageSpec("frontend/accounts.page.yaml", data)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func canonical(t *testing.T, document frontend.Document) []byte {
	t.Helper()
	value, err := frontend.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func assertJCS(t *testing.T, data []byte) {
	t.Helper()
	value, err := jcs.Transform(data)
	if err != nil || string(value) != string(data) {
		t.Fatalf("not canonical JSON: %v", err)
	}
}
func assertReason(t *testing.T, err error, reason string) {
	t.Helper()
	var typed *frontend.Error
	if !errors.As(err, &typed) || typed.Reason() != reason {
		t.Fatalf("error=%v; want reason=%s", err, reason)
	}
}

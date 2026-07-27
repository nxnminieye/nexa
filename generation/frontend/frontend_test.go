package frontend_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/frontend"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/provenance"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestPageSpecStrictAndModes(t *testing.T) {
	base := collectionSpec()
	if _, err := frontend.ParsePageSpec("frontend/accounts.json", base); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		mutate  func(map[string]any)
		pointer string
	}{
		{"unknown", func(v map[string]any) { v["unknown"] = true }, "/unknown"},
		{"mode", func(v map[string]any) { v["mode"] = "other" }, "/mode"},
		{"access", func(v map[string]any) { delete(v, "accessOperation") }, "/accessOperation"},
		{"custom", func(v map[string]any) { ops(v)[0]["role"] = "custom" }, "/operations/0/role"},
		{"dynamic-route", func(v map[string]any) { v["route"].(map[string]any)["path"] = "/accounts/:id" }, "/route/path"},
		{"context-required", func(v map[string]any) { delete(ops(v)[0], "contextBindings") }, "/operations/0/contextBindings"},
		{"action-effect", func(v map[string]any) { delete(actions(v)[0], "effect") }, "/actions/0/effect"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := object(base)
			tc.mutate(v)
			_, err := frontend.ParsePageSpec("frontend/case.json", marshal(v))
			assertError(t, err, "frontend_page_spec_invalid", tc.pointer)
		})
	}
	singleton := object(singletonSpec())
	singleton["actions"] = []any{map[string]any{"id": "bad", "labelKey": "action.bad", "operation": "get", "effect": "update", "fields": []any{}, "placement": "row"}}
	_, err := frontend.Build(loadAPI(t, apiSource()), []frontend.PageSpec{mustSpec(t, marshal(singleton))}, locale(t, "page.account", "field.name", "action.bad"))
	assertError(t, err, "frontend_ir_invalid", "/pages/0/operations")
}

func TestBuildCollectionProjectionAndActionClosure(t *testing.T) {
	doc, err := frontend.Build(loadAPI(t, apiSource()), []frontend.PageSpec{mustSpec(t, collectionSpec())}, collectionLocale(t))
	if err != nil {
		t.Fatal(err)
	}
	b := canonical(t, doc)
	assertJCS(t, b)
	var wire struct {
		Pages []struct {
			Mode, AccessOperation, AccessPermission string
			Operations                              []struct {
				ID, RequestType, ResponseType, Permission string
				ContextBindings                           []any
			}
			Fields []struct {
				ID       string
				Bindings []struct {
					Required  bool
					ValueType struct{ Kind, Name string }
				}
			}
		}
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	p := wire.Pages[0]
	if p.Mode != "collection" || p.AccessOperation != "list" || p.AccessPermission != "account.read" {
		t.Fatalf("page=%#v", p)
	}
	byOp := map[string]struct {
		req, res string
		contexts int
	}{}
	for _, op := range p.Operations {
		byOp[op.ID] = struct {
			req, res string
			contexts int
		}{op.RequestType, op.ResponseType, len(op.ContextBindings)}
	}
	if byOp["status"].req != "UpdateStatusRequest" || byOp["list"].res != "ListResponse" || byOp["list"].contexts != 1 {
		t.Fatalf("operations=%#v", byOp)
	}
	if !strings.Contains(string(b), `"valueType":{"kind":"scalar","name":"int64"}`) {
		t.Fatalf("exact type projection missing: %s", b)
	}
	if !strings.Contains(string(b), `"valueType":{"element":{"kind":"ref","name":"Quality"},"kind":"array"}`) {
		t.Fatalf("ref identity projection missing: %s", b)
	}
	for _, want := range []string{
		`"columns":[{"id":"code","labelKey":"column.code","path":["Code"],"required":true,"valueType":{"kind":"scalar","name":"string"}}`,
		`{"id":"score","labelKey":"column.score","path":["Score"],"required":false,"valueType":{"kind":"scalar","name":"int32"}}`,
		`{"id":"tags","labelKey":"column.tags","path":["Tags"],"required":true,"valueType":{"element":{"kind":"scalar","name":"string"},"kind":"array"}}]`,
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("column projection missing %s: %s", want, b)
		}
	}
}

func TestColumnsAndOptionsClosedSemantics(t *testing.T) {
	api := loadAPI(t, apiSource())
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		reason string
	}{
		{"options-orphan", func(v map[string]any) { actions(v)[1]["fields"] = []any{"status"} }, "options_action_required"},
		{"options-surface", func(v map[string]any) { fields(v)[4]["surfaces"] = []any{"list"} }, "options_surface_forbidden"},
		{"columns-path", func(v map[string]any) { fields(v)[5]["columns"].([]any)[0].(map[string]any)["path"] = []any{"Missing"} }, "column_path_invalid"},
		{"columns-control", func(v map[string]any) { fields(v)[5]["control"] = "text" }, "columns_control_forbidden"},
		{"columns-non-array", func(v map[string]any) {
			fields(v)[5]["bindings"].([]any)[0].(map[string]any)["path"] = []any{"Items", "Count"}
		}, "columns_type_invalid"},
		{"columns-required", func(v map[string]any) { delete(fields(v)[5], "columns") }, "columns_required"},
		{"column-cross-array", func(v map[string]any) {
			fields(v)[5]["columns"].([]any)[0].(map[string]any)["path"] = []any{"Tags", "Value"}
		}, "column_path_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := object(collectionSpec())
			tc.mutate(value)
			spec, err := frontend.ParsePageSpec("frontend/case.json", marshal(value))
			if err != nil {
				t.Fatal(err)
			}
			_, err = frontend.Build(api, []frontend.PageSpec{spec}, collectionLocale(t))
			assertReason(t, err, tc.reason)
		})
	}
}

func TestBuildRejectsClosedWorldMutations(t *testing.T) {
	api := loadAPI(t, apiSource())
	cases := []struct {
		name   string
		mutate func(map[string]any)
		reason string
	}{
		{"access-role", func(v map[string]any) { v["accessOperation"] = "create" }, "access_operation_role_invalid"},
		{"collection-get", func(v map[string]any) {
			ops(v)[0]["role"] = "get"
			delete(ops(v)[0], "result")
			delete(ops(v)[0], "pagination")
		}, "collection_operation_shape_invalid"},
		{"row-key-missing", func(v map[string]any) { delete(ops(v)[0]["result"].(map[string]any), "rowKeyPath") }, "row_key_required"},
		{"row-key-outside-items", func(v map[string]any) { ops(v)[0]["result"].(map[string]any)["rowKeyPath"] = []any{"Missing"} }, "row_key_type_invalid"},
		{"action-unreferenced", func(v map[string]any) { v["actions"] = actions(v)[:1] }, "action_operation_unreferenced"},
		{"action-placement", func(v map[string]any) { actions(v)[0]["placement"] = "row" }, "action_placement_invalid"},
		{"action-field-missing", func(v map[string]any) { actions(v)[0]["fields"] = []any{} }, "create_row_source_forbidden"},
		{"search-no-control", func(v map[string]any) { delete(fields(v)[0], "control") }, "search_surface_binding_invalid"},
		{"response-outside-items", func(v map[string]any) { fields(v)[1]["bindings"].([]any)[0].(map[string]any)["path"] = []any{"Total"} }, "response_binding_scope_invalid"},
		{"options-unreferenced", func(v map[string]any) { fields(v)[4]["surfaces"] = []any{"list"}; delete(fields(v)[4], "options") }, "list_surface_binding_invalid"},
		{"options-extra-required", func(v map[string]any) { ops(v)[3]["operationId"] = "account.options.extra" }, "required_request_binding_missing"},
		{"pagination-conflict", func(v map[string]any) {
			ops(v)[0]["contextBindings"] = append(ops(v)[0]["contextBindings"].([]any), map[string]any{"context": "page-limit", "path": []any{"Limit"}})
		}, "request_binding_conflict"},
		{"field-unused", func(v map[string]any) { fields(v)[1]["surfaces"] = []any{}; fields(v)[1]["bindings"] = []any{} }, "field_unused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := object(collectionSpec())
			tc.mutate(v)
			spec, err := frontend.ParsePageSpec("frontend/case.json", marshal(v))
			if err != nil {
				t.Fatal(err)
			}
			_, err = frontend.Build(api, []frontend.PageSpec{spec}, collectionLocale(t))
			assertReason(t, err, tc.reason)
		})
	}
}

func TestBindingExactTypesPresenceAndContext(t *testing.T) {
	api := loadAPI(t, apiSource())
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		reason string
	}{
		{"numeric-mismatch", func(v map[string]any) { fields(v)[1]["bindings"].([]any)[1].(map[string]any)["path"] = []any{"Count"} }, "binding_type_inconsistent"},
		{"optional-row-required", func(v map[string]any) {
			fields(v)[1]["bindings"].([]any)[0].(map[string]any)["path"] = []any{"Items", "OptionalID"}
		}, "optional_row_source_required_target"},
		{"duplicate-field-op-direction", func(v map[string]any) {
			f := fields(v)[0]
			f["bindings"] = append(f["bindings"].([]any), map[string]any{"operation": "list", "direction": "request", "path": []any{"Other"}})
		}, "field_binding_duplicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := object(collectionSpec())
			tc.mutate(v)
			spec, err := frontend.ParsePageSpec("frontend/case.json", marshal(v))
			if err != nil {
				assertReason(t, err, tc.reason)
				return
			}
			_, err = frontend.Build(api, []frontend.PageSpec{spec}, collectionLocale(t))
			assertReason(t, err, tc.reason)
		})
	}
	other := object(collectionSpec())
	for _, op := range ops(other) {
		for _, c := range op["contextBindings"].([]any) {
			c.(map[string]any)["context"] = "shared"
		}
	}
	second := object(singletonSpec())
	ops(second)[0]["operationId"] = "account.get.int-context"
	ops(second)[0]["contextBindings"].([]any)[0].(map[string]any)["context"] = "shared"
	_, err := frontend.Build(api, []frontend.PageSpec{mustSpec(t, marshal(other)), mustSpec(t, marshal(second))}, locale(t, append(collectionLocaleKeys(), "page.account")...))
	assertReason(t, err, "context_type_inconsistent")
}

func TestBindingPresenceIncludesOptionalParents(t *testing.T) {
	value := object(collectionSpec())
	value["fields"] = append(fields(value), map[string]any{
		"id": "profile-label", "labelKey": "field.profileLabel", "surfaces": []any{"list"},
		"bindings": []any{map[string]any{"operation": "list", "direction": "response", "path": []any{"Items", "Profile", "Label"}}},
	})
	doc, err := frontend.Build(loadAPI(t, apiSource()), []frontend.PageSpec{mustSpec(t, marshal(value))}, locale(t, append(collectionLocaleKeys(), "field.profileLabel")...))
	if err != nil {
		t.Fatal(err)
	}
	encoded := canonical(t, doc)
	if !strings.Contains(string(encoded), `"path":["Items","Profile","Label"],"required":false,"valueType":{"kind":"scalar","name":"string"}`) {
		t.Fatalf("optional parent presence was not projected: %s", encoded)
	}

	invalid := object(collectionSpec())
	ops(invalid)[0]["contextBindings"].([]any)[0].(map[string]any)["path"] = []any{"Query"}
	_, err = frontend.Build(loadAPI(t, apiSource()), []frontend.PageSpec{mustSpec(t, marshal(invalid))}, collectionLocale(t))
	assertReason(t, err, "context_binding_type_invalid")

	hidden := object(collectionSpec())
	nameBindings := fields(hidden)[2]["bindings"].([]any)
	nameBindings[0].(map[string]any)["path"] = []any{"Items", "Profile", "Label"}
	fields(hidden)[2]["bindings"] = append(nameBindings, map[string]any{"operation": "status", "direction": "request", "path": []any{"Status"}})
	fields(hidden)[3]["bindings"] = fields(hidden)[3]["bindings"].([]any)[:1]
	actions(hidden)[1]["fields"] = []any{"choice"}
	_, err = frontend.Build(loadAPI(t, apiSource()), []frontend.PageSpec{mustSpec(t, marshal(hidden))}, collectionLocale(t))
	assertReason(t, err, "optional_row_source_required_target")
}

func TestRowKeyUsesWholePathPresence(t *testing.T) {
	apiTemplate := `syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-api/v1")
type ListRequest { TenantID string ` + "`header:\"x-tenant\"`" + ` Limit int64 ` + "`form:\"limit\"`" + ` Offset int64 ` + "`form:\"offset\"`" + ` }
type Row { ID int64 ` + "`json:\"id\"`" + ` }
type Result { Items []Row ` + "`json:\"items\"`" + ` Total int64 ` + "`json:\"total\"`" + ` }
type ListResponse { Result %sResult ` + "`json:\"result%s\"`" + ` }
@server (nexaOperationId: "row.list" nexaAuthMode: "required" nexaCredentialId: "primary" nexaCredentialType: "bearer" nexaCredentialLocation: "header" nexaCredentialName: "Authorization" nexaPermission: "row.read")
service api { @handler list get /rows (ListRequest) returns (ListResponse) }
`
	spec := []byte(`{"apiVersion":"nexa.dev/frontend-page-spec/v1","kind":"FrontendPageSpec","id":"rows","titleKey":"page.rows","mode":"collection","accessOperation":"list","route":{"path":"/rows","name":"Rows"},"operations":[{"id":"list","role":"list","operationId":"row.list","contextBindings":[{"context":"tenant-id","path":["TenantID"]}],"result":{"itemsPath":["Result","Items"],"totalPath":["Result","Total"],"rowKeyPath":["ID"]},"pagination":{"mode":"offset","limitPath":["Limit"],"offsetPath":["Offset"],"totalPath":["Result","Total"],"pageSize":20}}],"fields":[{"id":"id","labelKey":"field.id","surfaces":["list"],"bindings":[{"operation":"list","direction":"response","path":["Result","Items","ID"]}]}],"actions":[],"extensionPoints":[]}`)
	_, err := frontend.Build(loadAPI(t, fmt.Sprintf(apiTemplate, "*", ",optional")), []frontend.PageSpec{mustSpec(t, spec)}, locale(t, "page.rows", "field.id"))
	assertReason(t, err, "row_key_type_invalid")
	doc, err := frontend.Build(loadAPI(t, fmt.Sprintf(apiTemplate, "", "")), []frontend.PageSpec{mustSpec(t, spec)}, locale(t, "page.rows", "field.id"))
	if err != nil {
		t.Fatal(err)
	}
	_ = canonical(t, doc)
	request, err := frontend.CanonicalRenderRequest(frontend.RenderRequest{FrontendIR: doc, RepositoryRoot: "/workspace/example", GeneratedScope: "frontend/generated", ExtensionScopes: []string{"frontend/extensions"}, FrontendSourceLockDigest: provenance.SHA256([]byte("lock"))})
	if err != nil {
		t.Fatal(err)
	}
	if err := frontend.ValidateRendererInput(request); err != nil {
		t.Fatalf("required row key roundtrip rejected: %v", err)
	}
}

func TestInlineObjectArrayColumnsRoundTrip(t *testing.T) {
	api := loadAPI(t, `syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-api/v1")
type ListRequest { TenantID string `+"`header:\"x-tenant\"`"+` Limit int64 `+"`form:\"limit\"`"+` Offset int64 `+"`form:\"offset\"`"+` }
type Row {
  ID int64 `+"`json:\"id\"`"+`
  Entries []Entry `+"`json:\"entries\"`"+`
}
type Entry {
  ID int64 `+"`json:\"id\"`"+`
  Meta { Label string `+"`json:\"label\"`"+` } `+"`json:\"meta\"`"+`
}
type ListResponse {
  Items []Row `+"`json:\"items\"`"+`
  Total int64 `+"`json:\"total\"`"+`
}
@server (nexaOperationId: "inline.list" nexaAuthMode: "required" nexaCredentialId: "primary" nexaCredentialType: "bearer" nexaCredentialLocation: "header" nexaCredentialName: "Authorization" nexaPermission: "inline.read")
service api { @handler list get /inline (ListRequest) returns (ListResponse) }
`)
	spec := mustSpec(t, []byte(`{"apiVersion":"nexa.dev/frontend-page-spec/v1","kind":"FrontendPageSpec","id":"inline","titleKey":"page.inline","mode":"collection","accessOperation":"list","route":{"path":"/inline","name":"Inline"},"operations":[{"id":"list","role":"list","operationId":"inline.list","contextBindings":[{"context":"tenant-id","path":["TenantID"]}],"result":{"itemsPath":["Items"],"totalPath":["Total"],"rowKeyPath":["ID"]},"pagination":{"mode":"offset","limitPath":["Limit"],"offsetPath":["Offset"],"totalPath":["Total"],"pageSize":20}}],"fields":[{"id":"items","labelKey":"field.items","surfaces":["list"],"bindings":[{"operation":"list","direction":"response","path":["Items","Entries"]}],"columns":[{"id":"id","labelKey":"column.id","path":["ID"]},{"id":"label","labelKey":"column.label","path":["Meta","Label"]}]}],"actions":[],"extensionPoints":[]}`))
	doc, err := frontend.Build(api, []frontend.PageSpec{spec}, locale(t, "page.inline", "field.items", "column.id", "column.label"))
	if err != nil {
		t.Fatal(err)
	}
	encoded := canonical(t, doc)
	if !strings.Contains(string(encoded), `"valueType":{"element":{"kind":"ref","name":"Entry"},"kind":"array"}`) {
		t.Fatalf("inline object identity missing: %s", encoded)
	}
	request, err := frontend.CanonicalRenderRequest(frontend.RenderRequest{FrontendIR: doc, RepositoryRoot: "/workspace/example", GeneratedScope: "frontend/generated", ExtensionScopes: []string{"frontend/extensions"}, FrontendSourceLockDigest: provenance.SHA256([]byte("lock"))})
	if err != nil {
		t.Fatal(err)
	}
	if err := frontend.ValidateRendererInput(request); err != nil {
		t.Fatalf("inline object columns roundtrip rejected: %v", err)
	}
}

func TestSingletonAndLocalePrefixCollision(t *testing.T) {
	doc, err := frontend.Build(loadAPI(t, apiSource()), []frontend.PageSpec{mustSpec(t, singletonSpec())}, locale(t, "page.account", "field.name"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageCount() != 1 {
		t.Fatal(doc.PageCount())
	}
	v := object(singletonSpec())
	ops(v)[0]["contextBindings"] = []any{}
	_, err = frontend.Build(loadAPI(t, apiSource()), []frontend.PageSpec{mustSpec(t, marshal(v))}, locale(t, "page.account", "field.name"))
	assertReason(t, err, "required_request_binding_missing")
	v = object(singletonSpec())
	v["titleKey"] = "field"
	_, err = frontend.Build(loadAPI(t, apiSource()), []frontend.PageSpec{mustSpec(t, marshal(v))}, locale(t, "field", "field.name"))
	assertReason(t, err, "locale_message_prefix_collision")
}

func TestSchemasAndRenderRequest(t *testing.T) {
	doc, err := frontend.Build(loadAPI(t, apiSource()), nil)
	if err != nil {
		t.Fatal(err)
	}
	req := frontend.RenderRequest{FrontendIR: doc, RepositoryRoot: filepath.Clean(t.TempDir()), GeneratedScope: "frontend/generated", ExtensionScopes: []string{"frontend/extensions"}, FrontendSourceLockDigest: provenance.SHA256([]byte("lock"))}
	b, err := frontend.CanonicalRenderRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	assertJCS(t, b)
	if err := frontend.ValidateRendererInput(b); err != nil {
		t.Fatalf("canonical render request rejected: %v", err)
	}
	compileSchemas(t)
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	v["unknown"] = true
	if validateSchema(t, frontend.RenderRequestSchema(), "https://nexa.dev/schemas/generation/frontend/frontend-render-request-v1.schema.json", v) == nil {
		t.Fatal("unknown render request field accepted")
	}
	for _, mutate := range []func(*frontend.RenderRequest){func(r *frontend.RenderRequest) { r.GeneratedScope = "../bad" }, func(r *frontend.RenderRequest) { r.ExtensionScopes = []string{"frontend/generated/nested"} }} {
		copy := req
		mutate(&copy)
		if _, err := frontend.CanonicalRenderRequest(copy); err == nil {
			t.Fatal("invalid scope accepted")
		}
	}
}

func TestRendererContractCorpus(t *testing.T) {
	root := "testdata/renderer-contract/v1"
	type entry struct {
		File      string `json:"file"`
		SHA256    string `json:"sha256"`
		Outcome   string `json:"outcome"`
		Code      string `json:"code,omitempty"`
		ErrorCode string `json:"errorCode,omitempty"`
		Pointer   string `json:"pointer,omitempty"`
	}
	var manifest struct {
		APIVersion string  `json:"apiVersion"`
		Entries    []entry `json:"entries"`
	}
	manifestBytes, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.APIVersion != "nexa.dev/frontend-renderer-corpus/v1" {
		t.Fatal(manifest.APIVersion)
	}
	if got := provenance.SHA256(manifestBytes).String(); got != "sha256:8ef98c5204033cda97328a57952455756b22d86d326d2517116a3856b4efe6d9" {
		t.Fatalf("manifest digest=%s", got)
	}
	for _, e := range manifest.Entries {
		data, err := os.ReadFile(filepath.Join(root, e.File))
		if err != nil {
			t.Fatal(err)
		}
		if provenance.SHA256(data).String() != e.SHA256 {
			t.Fatalf("%s digest mismatch", e.File)
		}
		if e.Outcome == "accept" || e.File != "noncanonical.json" {
			if c, x := jcs.Transform(data); x != nil || string(c) != string(data) {
				t.Fatalf("%s is not canonical", e.File)
			}
		}
		if strings.HasSuffix(e.File, "-ir.json") {
			requestFile := strings.TrimSuffix(e.File, "-ir.json") + "-render-request.json"
			requestBytes, readErr := os.ReadFile(filepath.Join(root, requestFile))
			if readErr != nil {
				t.Fatal(readErr)
			}
			var request struct {
				FrontendIR json.RawMessage `json:"frontendIR"`
			}
			if err := json.Unmarshal(requestBytes, &request); err != nil || string(request.FrontendIR) != string(data) {
				t.Fatalf("%s does not equal %s frontendIR", e.File, requestFile)
			}
			if err := frontend.ValidateRendererInput(requestBytes); err != nil {
				t.Fatalf("%s linked request rejected: %v", e.File, err)
			}
			continue
		}
		validationErr := frontend.ValidateRendererInput(data)
		if e.Outcome == "accept" {
			if validationErr != nil {
				t.Fatalf("%s rejected: %v", e.File, validationErr)
			}
			continue
		}
		var contractErr *frontend.Error
		if !errors.As(validationErr, &contractErr) || contractErr.Reason() != e.Code {
			t.Fatalf("%s reason=%v want=%s", e.File, validationErr, e.Code)
		}
		if e.ErrorCode != "" && contractErr.Code() != e.ErrorCode {
			t.Fatalf("%s code=%s want=%s", e.File, contractErr.Code(), e.ErrorCode)
		}
		if e.Pointer != "" && contractErr.Pointer() != e.Pointer {
			t.Fatalf("%s pointer=%s want=%s", e.File, contractErr.Pointer(), e.Pointer)
		}
	}
	for _, tc := range []struct {
		name  string
		specs []frontend.PageSpec
		keys  []string
	}{
		{"empty", nil, nil},
		{"nonempty", []frontend.PageSpec{mustSpec(t, collectionSpec())}, collectionLocaleKeys()},
	} {
		locales := []frontend.Locale(nil)
		if len(tc.keys) > 0 {
			locales = []frontend.Locale{locale(t, tc.keys...)}
		}
		doc, err := frontend.Build(loadAPI(t, apiSource()), tc.specs, locales...)
		if err != nil {
			t.Fatal(err)
		}
		ir := canonical(t, doc)
		req, err := frontend.CanonicalRenderRequest(frontend.RenderRequest{FrontendIR: doc, RepositoryRoot: "/workspace/example", GeneratedScope: "frontend/generated", ExtensionScopes: []string{"frontend/extensions"}, FrontendSourceLockDigest: provenance.SHA256([]byte("lock"))})
		if err != nil {
			t.Fatal(err)
		}
		wantIR, _ := os.ReadFile(filepath.Join(root, tc.name+"-ir.json"))
		wantReq, _ := os.ReadFile(filepath.Join(root, tc.name+"-render-request.json"))
		if string(ir) != string(wantIR) || string(req) != string(wantReq) {
			t.Fatalf("%s canonical golden drift", tc.name)
		}
	}
}

func TestRendererMissingMenuAncestorIsDeterministic(t *testing.T) {
	data, err := os.ReadFile("testdata/renderer-contract/v1/menu-parent-missing.json")
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 256; iteration++ {
		validationErr := frontend.ValidateRendererInput(data)
		var contractErr *frontend.Error
		if !errors.As(validationErr, &contractErr) || contractErr.Code() != "frontend_render_request_invalid" || contractErr.Reason() != "menu_parent_invalid" || contractErr.Pointer() != "/frontendIR/pages" {
			t.Fatalf("iteration %d: error=%v", iteration, validationErr)
		}
	}
}

func apiSource() string {
	return `syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-api/v1")
type ListRequest { TenantID string ` + "`header:\"x-tenant\"`" + ` Query *string ` + "`form:\"q,optional\"`" + ` Other *string ` + "`form:\"other,optional\"`" + ` Limit int64 ` + "`form:\"limit\"`" + ` Offset int64 ` + "`form:\"offset\"`" + ` }
type Quality { Code string ` + "`json:\"code\"`" + ` Score *int32 ` + "`json:\"score,optional\"`" + ` Tags []string ` + "`json:\"tags\"`" + ` }
type Profile { Label string ` + "`json:\"label\"`" + ` }
type Account { ID int64 ` + "`json:\"id\"`" + ` OptionalID *int64 ` + "`json:\"optionalId,optional\"`" + ` Name string ` + "`json:\"name\"`" + ` Status string ` + "`json:\"status\"`" + ` Count int64 ` + "`json:\"count\"`" + ` Profile *Profile ` + "`json:\"profile,optional\"`" + ` Qualities []Quality ` + "`json:\"qualities\"`" + ` }
type ListResponse { Items []Account ` + "`json:\"items\"`" + ` Total int64 ` + "`json:\"total\"`" + ` }
type CreateRequest { TenantID string ` + "`header:\"x-tenant\"`" + ` Name string ` + "`json:\"name\"`" + ` }
type UpdateStatusRequest { TenantID string ` + "`header:\"x-tenant\"`" + ` ID int64 ` + "`path:\"id\"`" + ` Status string ` + "`json:\"status\"`" + ` Choices []string ` + "`json:\"choices\"`" + ` Count *int32 ` + "`json:\"count,optional\"`" + ` }
type DeleteRequest { TenantID string ` + "`header:\"x-tenant\"`" + ` ID int64 ` + "`path:\"id\"`" + ` }
type OptionsRequest { TenantID string ` + "`header:\"x-tenant\"`" + ` Limit int64 ` + "`form:\"limit\"`" + ` Offset int64 ` + "`form:\"offset\"`" + ` }
type OptionsExtraRequest { TenantID string ` + "`header:\"x-tenant\"`" + ` Limit int64 ` + "`form:\"limit\"`" + ` Offset int64 ` + "`form:\"offset\"`" + ` Filter string ` + "`form:\"filter\"`" + ` }
type Option { Code string ` + "`json:\"code\"`" + ` Label string ` + "`json:\"label\"`" + ` }
type OptionsResponse { Items []Option ` + "`json:\"items\"`" + ` Total int64 ` + "`json:\"total\"`" + ` }
type GetRequest { TenantID string ` + "`header:\"x-tenant\"`" + ` }
type GetIntRequest { TenantID int64 ` + "`header:\"x-tenant\"`" + ` }
@server (nexaOperationId: "account.list" nexaAuthMode: "required" nexaCredentialId: "primary" nexaCredentialType: "bearer" nexaCredentialLocation: "header" nexaCredentialName: "Authorization" nexaPermission: "account.read")
service api { @handler list get /accounts (ListRequest) returns (ListResponse) }
@server (nexaOperationId: "account.create" nexaAuthMode: "required" nexaCredentialId: "primary" nexaCredentialType: "bearer" nexaCredentialLocation: "header" nexaCredentialName: "Authorization" nexaPermission: "account.write")
service api { @handler create post /accounts (CreateRequest) }
@server (nexaOperationId: "account.status" nexaAuthMode: "required" nexaCredentialId: "primary" nexaCredentialType: "bearer" nexaCredentialLocation: "header" nexaCredentialName: "Authorization" nexaPermission: "account.write")
service api { @handler status put /accounts/:id/status (UpdateStatusRequest) }
@server (nexaOperationId: "account.delete" nexaAuthMode: "required" nexaCredentialId: "primary" nexaCredentialType: "bearer" nexaCredentialLocation: "header" nexaCredentialName: "Authorization" nexaPermission: "account.write")
service api { @handler delete delete /accounts/:id (DeleteRequest) }
@server (nexaOperationId: "account.options" nexaAuthMode: "none" nexaPermission: "")
service api { @handler options get /account-options (OptionsRequest) returns (OptionsResponse) }
@server (nexaOperationId: "account.options.extra" nexaAuthMode: "none" nexaPermission: "")
service api { @handler optionsextra get /account-options-extra (OptionsExtraRequest) returns (OptionsResponse) }
@server (nexaOperationId: "account.get" nexaAuthMode: "required" nexaCredentialId: "primary" nexaCredentialType: "bearer" nexaCredentialLocation: "header" nexaCredentialName: "Authorization" nexaPermission: "account.read")
service api { @handler get get /account (GetRequest) returns (Account) }
@server (nexaOperationId: "account.get.int-context" nexaAuthMode: "required" nexaCredentialId: "primary" nexaCredentialType: "bearer" nexaCredentialLocation: "header" nexaCredentialName: "Authorization" nexaPermission: "account.read")
service api { @handler getint get /account-int (GetIntRequest) returns (Account) }
`
}

func collectionSpec() []byte {
	return []byte(`{"apiVersion":"nexa.dev/frontend-page-spec/v1","kind":"FrontendPageSpec","id":"accounts","titleKey":"page.accounts","mode":"collection","accessOperation":"list","route":{"path":"/accounts","name":"Accounts"},"operations":[{"id":"list","role":"list","operationId":"account.list","contextBindings":[{"context":"tenant-id","path":["TenantID"]}],"result":{"itemsPath":["Items"],"totalPath":["Total"],"rowKeyPath":["ID"]},"pagination":{"mode":"offset","limitPath":["Limit"],"offsetPath":["Offset"],"totalPath":["Total"],"pageSize":20}},{"id":"create","role":"action","operationId":"account.create","contextBindings":[{"context":"tenant-id","path":["TenantID"]}]},{"id":"status","role":"action","operationId":"account.status","contextBindings":[{"context":"tenant-id","path":["TenantID"]}]},{"id":"choices","role":"options","operationId":"account.options","contextBindings":[{"context":"tenant-id","path":["TenantID"]}],"result":{"itemsPath":["Items"],"totalPath":["Total"]},"pagination":{"mode":"offset","limitPath":["Limit"],"offsetPath":["Offset"],"totalPath":["Total"],"pageSize":100}},{"id":"delete","role":"action","operationId":"account.delete","contextBindings":[{"context":"tenant-id","path":["TenantID"]}]}],"fields":[{"id":"query","labelKey":"field.query","surfaces":["search"],"control":"text","bindings":[{"operation":"list","direction":"request","path":["Query"]}]},{"id":"id","labelKey":"field.id","surfaces":[],"bindings":[{"operation":"list","direction":"response","path":["Items","ID"]},{"operation":"status","direction":"request","path":["ID"]},{"operation":"delete","direction":"request","path":["ID"]}]},{"id":"name","labelKey":"field.name","surfaces":["list"],"control":"text","bindings":[{"operation":"list","direction":"response","path":["Items","Name"]},{"operation":"create","direction":"request","path":["Name"]}]},{"id":"status","labelKey":"field.status","surfaces":["list"],"control":"select","choices":[{"value":"active","labelKey":"field.status"}],"bindings":[{"operation":"list","direction":"response","path":["Items","Status"]},{"operation":"status","direction":"request","path":["Status"]}]},{"id":"choice","labelKey":"field.choice","surfaces":[],"control":"multi-select","options":{"operation":"choices","valuePath":["Code"],"labelPath":["Label"]},"bindings":[{"operation":"status","direction":"request","path":["Choices"]}]},{"id":"qualities","labelKey":"field.qualities","surfaces":["list"],"bindings":[{"operation":"list","direction":"response","path":["Items","Qualities"]}],"columns":[{"id":"code","labelKey":"column.code","path":["Code"]},{"id":"score","labelKey":"column.score","path":["Score"]},{"id":"tags","labelKey":"column.tags","path":["Tags"]}]}],"actions":[{"id":"create","labelKey":"action.create","operation":"create","effect":"create","fields":["name"],"placement":"toolbar"},{"id":"status","labelKey":"action.status","operation":"status","effect":"update","fields":["status","choice"],"placement":"row"},{"id":"delete","labelKey":"action.delete","operation":"delete","effect":"delete","fields":[],"placement":"row","confirmKey":"action.deleteConfirm"}],"extensionPoints":[]}`)
}
func singletonSpec() []byte {
	return []byte(`{"apiVersion":"nexa.dev/frontend-page-spec/v1","kind":"FrontendPageSpec","id":"account","titleKey":"page.account","mode":"singleton","accessOperation":"get","route":{"path":"/account","name":"Account"},"operations":[{"id":"get","role":"get","operationId":"account.get","contextBindings":[{"context":"tenant-id","path":["TenantID"]}]}],"fields":[{"id":"name","labelKey":"field.name","surfaces":["detail"],"bindings":[{"operation":"get","direction":"response","path":["Name"]}]}],"actions":[],"extensionPoints":[]}`)
}

func loadAPI(t *testing.T, source string) httpapi.Document {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "api.api"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	d, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: dir, EntryFile: "api.api"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func mustSpec(t *testing.T, b []byte) frontend.PageSpec {
	t.Helper()
	s, err := frontend.ParsePageSpec("frontend/page.json", b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func locale(t *testing.T, keys ...string) frontend.Locale {
	t.Helper()
	m := map[string]string{}
	for _, k := range keys {
		m[k] = k
	}
	b, _ := json.Marshal(map[string]any{"apiVersion": frontend.LocaleAPIVersion, "kind": "FrontendLocale", "locale": "en-US", "messages": m})
	l, err := frontend.ParseLocale("frontend/en-US.json", b)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func collectionLocale(t *testing.T) frontend.Locale { return locale(t, collectionLocaleKeys()...) }

func collectionLocaleKeys() []string {
	return []string{"page.accounts", "field.query", "field.id", "field.name", "field.status", "field.choice", "field.qualities", "column.code", "column.score", "column.tags", "action.create", "action.status", "action.delete", "action.deleteConfirm"}
}
func object(b []byte) map[string]any {
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		panic(err)
	}
	return v
}
func marshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
func ops(v map[string]any) []map[string]any     { return maps(v["operations"].([]any)) }
func fields(v map[string]any) []map[string]any  { return maps(v["fields"].([]any)) }
func actions(v map[string]any) []map[string]any { return maps(v["actions"].([]any)) }
func maps(v []any) []map[string]any {
	r := make([]map[string]any, len(v))
	for i := range v {
		r[i] = v[i].(map[string]any)
	}
	return r
}
func canonical(t *testing.T, d frontend.Document) []byte {
	t.Helper()
	b, err := frontend.CanonicalJSON(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func assertJCS(t *testing.T, b []byte) {
	t.Helper()
	c, err := jcs.Transform(b)
	if err != nil || string(c) != string(b) {
		t.Fatalf("not JCS: %v", err)
	}
}
func assertError(t *testing.T, err error, code, pointer string) {
	t.Helper()
	var e *frontend.Error
	if !errors.As(err, &e) || e.Code() != code || e.Pointer() != pointer {
		t.Fatalf("error=%v code/pointer want %s %s", err, code, pointer)
	}
}
func assertReason(t *testing.T, err error, reason string) {
	t.Helper()
	var e *frontend.Error
	if !errors.As(err, &e) || e.Reason() != reason {
		t.Fatalf("error=%v reason want %s", err, reason)
	}
}
func compileSchemas(t *testing.T) {
	t.Helper()
	for _, v := range [][]byte{frontend.IRSchema(), frontend.RenderRequestSchema()} {
		var x any
		if err := json.Unmarshal(v, &x); err != nil {
			t.Fatal(err)
		}
	}
}
func validateSchema(t *testing.T, schema []byte, url string, v any) error {
	t.Helper()
	c := jsonschema.NewCompiler()
	resources := map[string][]byte{url: schema, "https://nexa.dev/schemas/generation/frontend/frontend-ir-v1.schema.json": frontend.IRSchema(), "https://nexa.dev/schemas/generation/httpapi/api-ir-v1.schema.json": httpapi.Schema()}
	for u, b := range resources {
		var x any
		if err := json.Unmarshal(b, &x); err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource(u, x); err != nil {
			t.Fatal(err)
		}
	}
	s, err := c.Compile(url)
	if err != nil {
		t.Fatal(err)
	}
	return s.Validate(v)
}

package httpconvention_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/httpconvention"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestPDCLConvention(t *testing.T) {
	for source, want := range map[string]string{
		"ID": "id", "TenantID": "tenantId", "created_at": "createdAt",
	} {
		got, err := httpconvention.CanonicalName(source)
		if err != nil || got != want {
			t.Fatalf("CanonicalName(%q) = %q, %v; want %q", source, got, err, want)
		}
	}
	for _, name := range []string{"tenantId", "source_key"} {
		if err := httpconvention.ValidateFieldName(name); err != nil {
			t.Fatalf("field name %q: %v", name, err)
		}
	}
	if err := httpconvention.ValidateFieldName("TenantID"); err == nil {
		t.Fatal("non-PDCL field spelling accepted")
	}

	fields, err := httpconvention.ClassifyRequest("PATCH", "/records/{id}", []string{"id", "expectedVersion", "name"})
	if err != nil || !reflect.DeepEqual(fields, []httpconvention.RequestField{
		{Name: "id", Location: httpconvention.LocationPath},
		{Name: "expectedVersion", Location: httpconvention.LocationBody},
		{Name: "name", Location: httpconvention.LocationBody},
	}) {
		t.Fatalf("ClassifyRequest() = %#v, %v", fields, err)
	}
	snakePath, err := httpconvention.ClassifyRequest("POST", "/kafka/acls/{acl_binding_id}/retry", []string{"tenantId", "aclBindingId", "operatorMemberId"})
	if err != nil || snakePath[1].Location != httpconvention.LocationPath {
		t.Fatalf("snake path classification = %#v, %v", snakePath, err)
	}
	queries, err := httpconvention.ClassifyRequest("GET", "/records", []string{"limit", "offset"})
	if err != nil || queries[0].Location != httpconvention.LocationQuery || queries[1].Location != httpconvention.LocationQuery {
		t.Fatalf("GET classification = %#v, %v", queries, err)
	}
	businessFields, err := httpconvention.ClassifyRequest("GET", "/records", []string{"tenantId", "subjectId", "requestId", "traceId"})
	if err != nil || len(businessFields) != 4 {
		t.Fatalf("business identity fields = %#v, %v", businessFields, err)
	}

	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		status, err := httpconvention.SuccessStatus(method, "/records", true)
		if err != nil || status != 200 {
			t.Fatalf("SuccessStatus(%s) = %d, %v", method, status, err)
		}
	}
	if err := httpconvention.ValidatePagination(20, 0); err != nil {
		t.Fatal(err)
	}
	if err := httpconvention.ValidatePagination(0, -1); err == nil {
		t.Fatal("invalid pagination accepted")
	}
	if err := httpconvention.ValidateListResponse(map[string]any{"items": []any{}, "total": int64(0)}); err != nil {
		t.Fatal(err)
	}
	if err := httpconvention.ValidateSuccessEnvelope(map[string]any{"code": 0, "msg": "ok", "data": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if err := httpconvention.ValidateErrorEnvelope(map[string]any{"code": 409, "msg": "conflict", "message": "conflict"}, 409); err != nil {
		t.Fatal(err)
	}
}

func TestConformanceFixtureMatchesSchemaAndRules(t *testing.T) {
	fixture, err := os.ReadFile("testdata/conformance-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var schemaDocument, fixtureDocument map[string]any
	if err := json.Unmarshal(httpconvention.ConformanceSchema(), &schemaDocument); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixture, &fixtureDocument); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("https://nexa.dev/schemas/generation/httpconvention/conformance-v1.schema.json", schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("https://nexa.dev/schemas/generation/httpconvention/conformance-v1.schema.json")
	if err != nil || compiled.Validate(fixtureDocument) != nil {
		t.Fatalf("fixture schema validation failed: %v", err)
	}
	for _, raw := range fixtureDocument["requestCases"].([]any) {
		item := raw.(map[string]any)
		fields := stringsOf(item["fields"].([]any))
		classified, err := httpconvention.ClassifyRequest(item["method"].(string), item["path"].(string), fields)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(classified))
		for index := range classified {
			got[index] = string(classified[index].Location)
		}
		if want := stringsOf(item["locations"].([]any)); !reflect.DeepEqual(got, want) {
			t.Fatalf("case %s locations = %v, want %v", item["name"], got, want)
		}
	}
	for _, raw := range fixtureDocument["successCases"].([]any) {
		item := raw.(map[string]any)
		status, err := httpconvention.SuccessStatus(item["method"].(string), item["path"].(string), true)
		if err != nil || status != int(item["status"].(float64)) {
			t.Fatalf("success case %#v = %d, %v", item, status, err)
		}
	}
	for _, raw := range fixtureDocument["paginationCases"].([]any) {
		item := raw.(map[string]any)
		err := httpconvention.ValidatePagination(int(item["limit"].(float64)), int(item["offset"].(float64)))
		if item["accepted"].(bool) != (err == nil) {
			t.Fatalf("pagination case %#v = %v", item, err)
		}
	}
}

func stringsOf(values []any) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.(string)
	}
	return result
}

package httpconvention_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/httpconvention"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestCanonicalName(t *testing.T) {
	for source, want := range map[string]string{
		"ID": "id", "TenantID": "tenantId", "HTTPResponseID": "httpResponseId",
		"created_at": "createdAt", "pageSize": "pageSize", "URL2Value": "url2Value",
	} {
		got, err := httpconvention.CanonicalName(source)
		if err != nil || got != want {
			t.Fatalf("CanonicalName(%q) = %q, %v; want %q", source, got, err, want)
		}
	}
	for _, invalid := range []string{"", "_id", "tenant__id", "tenant-id", "租户"} {
		if _, err := httpconvention.CanonicalName(invalid); err == nil {
			t.Fatalf("CanonicalName(%q) succeeded", invalid)
		}
	}
	if err := httpconvention.ValidateCanonicalName("TenantID"); err == nil {
		t.Fatal("non-canonical authored name accepted")
	}
}

func TestRequestClassification(t *testing.T) {
	fields, err := httpconvention.ClassifyRequest("PATCH", "/api/records/{id}", []string{"id", "expectedVersion", "name"})
	if err != nil || !reflect.DeepEqual(fields, []httpconvention.RequestField{
		{Name: "id", Location: httpconvention.LocationPath},
		{Name: "expectedVersion", Location: httpconvention.LocationBody},
		{Name: "name", Location: httpconvention.LocationBody},
	}) {
		t.Fatalf("ClassifyRequest() = %#v, %v", fields, err)
	}
	queries, err := httpconvention.ClassifyRequest("GET", "/api/records", []string{"page", "pageSize"})
	if err != nil || queries[0].Location != httpconvention.LocationQuery || queries[1].Location != httpconvention.LocationQuery {
		t.Fatalf("GET classification = %#v, %v", queries, err)
	}
	for _, test := range []struct {
		method, path string
		fields       []string
	}{
		{"GET", "/records", []string{"page"}},
		{"GET", "/api/Records", []string{}},
		{"POST", "/api/records/{id}", []string{"otherId"}},
		{"OPTIONS", "/api/records", []string{}},
		{"GET", "/api/records", []string{"authorization"}},
	} {
		if _, err := httpconvention.ClassifyRequest(test.method, test.path, test.fields); err == nil {
			t.Fatalf("invalid request accepted: %#v", test)
		}
	}
}

func TestSuccessPaginationAndProblem(t *testing.T) {
	for _, test := range []struct {
		method, route string
		body          bool
		status        int
	}{
		{"GET", "/api/records", true, 200}, {"POST", "/api/records", true, 201},
		{"POST", "/api/records/{id}/actions/archive", true, 200},
		{"PATCH", "/api/records/{id}", true, 200}, {"DELETE", "/api/records/{id}", false, 204},
	} {
		status, err := httpconvention.SuccessStatus(test.method, test.route, test.body)
		if err != nil || status != test.status {
			t.Fatalf("SuccessStatus(%#v) = %d, %v", test, status, err)
		}
	}
	if err := httpconvention.ValidatePage(1, 100); err != nil {
		t.Fatal(err)
	}
	if err := httpconvention.ValidatePage(0, 101); err == nil {
		t.Fatal("invalid pagination accepted")
	}
	if err := httpconvention.ValidateListResponse(map[string]any{"items": []any{}, "total": 0}); err != nil {
		t.Fatal(err)
	}
	if err := httpconvention.ValidateListResponse(map[string]any{"items": []any(nil), "total": 0}); err == nil {
		t.Fatal("null list items accepted")
	}
	problemType, err := httpconvention.ProblemType("failed_precondition")
	if err != nil {
		t.Fatal(err)
	}
	problem := map[string]any{"type": problemType, "title": "Precondition failed", "status": 422, "code": "record_version_mismatch"}
	if err := httpconvention.ValidateProblem(problem, 422); err != nil {
		t.Fatal(err)
	}
	problem["message"] = "legacy"
	if err := httpconvention.ValidateProblem(problem, 422); err == nil {
		t.Fatal("legacy problem alias accepted")
	}
}

func TestScalarAndNullRules(t *testing.T) {
	for _, value := range []string{"0", "1", "-1", "18446744073709551615"} {
		if err := httpconvention.ValidateDecimalString(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"-0", "+1", "01", "1.0"} {
		if err := httpconvention.ValidateDecimalString(value); err == nil {
			t.Fatalf("invalid decimal %q accepted", value)
		}
	}
	if err := httpconvention.ValidateTimestamp("2026-07-28T03:04:05.123Z"); err != nil {
		t.Fatal(err)
	}
	if err := httpconvention.ValidateTimestamp("2026-07-28T11:04:05+08:00"); err == nil {
		t.Fatal("non-Z timestamp accepted")
	}
	if err := httpconvention.ValidateDate("2026-07-28"); err != nil {
		t.Fatal(err)
	}
	if err := httpconvention.ValidateDate("2026-02-30"); err == nil {
		t.Fatal("invalid date accepted")
	}
	if err := httpconvention.ValidateNoNull(map[string]any{"items": []any{}, "name": "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := httpconvention.ValidateNoNull(map[string]any{"value": nil}); err == nil {
		t.Fatal("null accepted")
	}
}

func TestConformanceFixtureMatchesSchemaAndRules(t *testing.T) {
	fixture, err := os.ReadFile("testdata/conformance-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var schemaDocument, fixtureDocument any
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
	root := fixtureDocument.(map[string]any)
	for _, raw := range root["namingCases"].([]any) {
		item := raw.(map[string]any)
		got, err := httpconvention.CanonicalName(item["source"].(string))
		accepted := item["accepted"].(bool)
		if accepted != (err == nil) || accepted && got != item["canonical"] {
			t.Fatalf("naming case %#v = %q, %v", item, got, err)
		}
	}
	for _, raw := range root["requestCases"].([]any) {
		item := raw.(map[string]any)
		fields := stringsOf(item["fields"].([]any))
		want := stringsOf(item["locations"].([]any))
		classified, err := httpconvention.ClassifyRequest(item["method"].(string), item["path"].(string), fields)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(classified))
		for index := range classified {
			got[index] = string(classified[index].Location)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %s locations = %v, want %v", item["name"], got, want)
		}
	}
	for _, raw := range root["successCases"].([]any) {
		item := raw.(map[string]any)
		got, err := httpconvention.SuccessStatus(item["method"].(string), item["path"].(string), item["hasRepresentation"].(bool))
		if err != nil || got != int(item["status"].(float64)) {
			t.Fatalf("success case %#v = %d, %v", item, got, err)
		}
	}
	for _, raw := range root["paginationCases"].([]any) {
		item := raw.(map[string]any)
		err := httpconvention.ValidatePage(int(item["page"].(float64)), int(item["pageSize"].(float64)))
		if item["accepted"].(bool) != (err == nil) {
			t.Fatalf("pagination case %#v = %v", item, err)
		}
	}
	for _, raw := range root["problemCases"].([]any) {
		item := raw.(map[string]any)
		category := item["category"].(string)
		code := item["code"].(string)
		status := int(item["status"].(float64))
		problemType, err := httpconvention.ProblemType(category)
		problem := map[string]any{"type": problemType, "title": "Fixture", "status": status, "code": code}
		if err != nil || problemType != item["type"] || !mustProblemStatus(category, status) || httpconvention.ValidateProblem(problem, status) != nil {
			t.Fatalf("problem case %#v = %q, %v", item, problemType, err)
		}
	}
	for _, raw := range root["scalarCases"].([]any) {
		item := raw.(map[string]any)
		value := item["value"].(string)
		var err error
		switch item["kind"] {
		case "decimal":
			err = httpconvention.ValidateDecimalString(value)
		case "id":
			err = httpconvention.ValidateOpaqueID(value)
		case "timestamp":
			err = httpconvention.ValidateTimestamp(value)
		case "date":
			err = httpconvention.ValidateDate(value)
		}
		if item["accepted"].(bool) != (err == nil) {
			t.Fatalf("scalar case %#v = %v", item, err)
		}
	}
}

func mustProblemStatus(category string, want int) bool {
	got, ok := httpconvention.ProblemStatus(category)
	return ok && got == want
}

func stringsOf(values []any) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.(string)
	}
	return result
}

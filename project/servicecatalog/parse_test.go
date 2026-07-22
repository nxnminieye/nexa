package servicecatalog_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/project/servicecatalog"
)

func TestParseValidCatalog(t *testing.T) {
	catalog, err := servicecatalog.Parse("services.yaml", []byte(validCatalogYAML))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if catalog.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", catalog.Len())
	}
	assertServiceIDs(t, catalog.Services(), []string{"foundation", "sample"})
	assertServiceIDs(t, catalog.DependencyOrder(), []string{"foundation", "sample"})

	sample, ok := catalog.Lookup("sample")
	if !ok {
		t.Fatal("Lookup(sample) did not find service")
	}
	if sample.Root() != "backend/sample" {
		t.Fatalf("Root() = %q, want backend/sample", sample.Root())
	}
	if got := sample.DependsOn(); !reflect.DeepEqual(got, []string{"foundation"}) {
		t.Fatalf("DependsOn() = %v, want [foundation]", got)
	}
	bindings := sample.CapabilityBindings()
	if len(bindings) != 1 {
		t.Fatalf("CapabilityBindings() len = %d, want 1", len(bindings))
	}
	if bindings[0].ID() != "example.com/cross-source-relation" {
		t.Fatalf("binding ID() = %q", bindings[0].ID())
	}
	if bindings[0].APIVersion() != "example.com/cross-source-relation/v1" {
		t.Fatalf("binding APIVersion() = %q", bindings[0].APIVersion())
	}
}

func TestParseExplicitEmptyCatalog(t *testing.T) {
	data := []byte("apiVersion: nexa.dev/service-catalog/v1\nkind: ServiceCatalog\nservices: []\n")
	catalog, err := servicecatalog.Parse("services.yaml", data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if catalog.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", catalog.Len())
	}
}

func TestParseWhitespaceFails(t *testing.T) {
	_, err := servicecatalog.Parse("services.yaml", []byte(" \n\t"))
	catalogError := requireCatalogError(t, err, "service_catalog_empty", "")
	if catalogError.Source() != "services.yaml" {
		t.Fatalf("Source() = %q, want services.yaml", catalogError.Source())
	}
	if catalogError.Error() != "services.yaml: service catalog is empty" {
		t.Fatalf("Error() = %q", catalogError.Error())
	}
}

func TestInvalidCatalog(t *testing.T) {
	validService := `  - id: foundation
    root: backend/foundation
    capabilityBindings: []
`
	tests := []struct {
		name    string
		data    string
		code    string
		reason  string
		pointer string
		cycle   []string
	}{
		{
			name: "unknown field",
			data: "apiVersion: nexa.dev/service-catalog/v1\nkind: ServiceCatalog\nservices: []\nextra: true\n",
			code: "service_catalog_invalid", reason: "document_unknown_field", pointer: "/extra",
		},
		{
			name: "duplicate field",
			data: "apiVersion: nexa.dev/service-catalog/v1\nkind: ServiceCatalog\nkind: ServiceCatalog\nservices: []\n",
			code: "service_catalog_invalid", reason: "document_duplicate_key", pointer: "/kind",
		},
		{
			name: "alias",
			data: "apiVersion: &version nexa.dev/service-catalog/v1\nkind: *version\nservices: []\n",
			code: "service_catalog_invalid", reason: "document_alias_forbidden", pointer: "/kind",
		},
		{
			name: "merge key",
			data: `apiVersion: nexa.dev/service-catalog/v1
kind: ServiceCatalog
services:
  - &base
    id: foundation
    root: backend/foundation
    capabilityBindings: []
  - <<: *base
    id: sample
    root: backend/sample
    capabilityBindings: []
`,
			code: "service_catalog_invalid", reason: "document_merge_key_forbidden", pointer: "/services/1/<<",
		},
		{
			name: "custom tag",
			data: "apiVersion: nexa.dev/service-catalog/v1\nkind: !catalog ServiceCatalog\nservices: []\n",
			code: "service_catalog_invalid", reason: "document_tag_forbidden", pointer: "/kind",
		},
		{
			name: "trailing document",
			data: "apiVersion: nexa.dev/service-catalog/v1\nkind: ServiceCatalog\nservices: []\n---\nservices: []\n",
			code: "service_catalog_invalid", reason: "document_trailing_input",
		},
		{
			name: "schema required field",
			data: "apiVersion: nexa.dev/service-catalog/v1\nkind: ServiceCatalog\n",
			code: "service_catalog_invalid", reason: "document_invalid", pointer: "/services",
		},
		{
			name: "schema required service id",
			data: catalogYAML(`  - root: backend/foundation
    capabilityBindings: []
`),
			code: "service_catalog_invalid", reason: "document_invalid", pointer: "/services/0/id",
		},
		{
			name: "schema required binding id",
			data: catalogYAML(`  - id: foundation
    root: backend/foundation
    capabilityBindings:
      - apiVersion: example.com/relation/v1
`),
			code: "service_catalog_invalid", reason: "document_invalid", pointer: "/services/0/capabilityBindings/0/id",
		},
		{
			name: "unsupported version",
			data: "apiVersion: nexa.dev/service-catalog/v2\nkind: ServiceCatalog\nservices: []\n",
			code: "service_catalog_invalid", reason: "version_unsupported", pointer: "/apiVersion",
		},
		{
			name: "invalid kind",
			data: "apiVersion: nexa.dev/service-catalog/v1\nkind: Catalog\nservices: []\n",
			code: "service_catalog_invalid", reason: "kind_invalid", pointer: "/kind",
		},
		{
			name: "invalid service id",
			data: catalogYAML(`  - id: Foundation
    root: backend/foundation
    capabilityBindings: []
`),
			code: "service_id_invalid", pointer: "/services/0/id",
		},
		{
			name: "duplicate service id",
			data: catalogYAML(validService + `  - id: foundation
    root: backend/other
    capabilityBindings: []
`),
			code: "service_id_duplicate", pointer: "/services/1/id",
		},
		{
			name: "dot service root",
			data: catalogYAML(`  - id: foundation
    root: .
    capabilityBindings: []
`),
			code: "service_root_invalid", pointer: "/services/0/root",
		},
		{
			name: "absolute service root",
			data: catalogYAML(`  - id: foundation
    root: /backend/foundation
    capabilityBindings: []
`),
			code: "service_root_invalid", pointer: "/services/0/root",
		},
		{
			name: "backslash service root",
			data: catalogYAML("  - id: foundation\n    root: 'backend\\foundation'\n    capabilityBindings: []\n"),
			code: "service_root_invalid", pointer: "/services/0/root",
		},
		{
			name: "non-clean service root",
			data: catalogYAML(`  - id: foundation
    root: backend/../foundation
    capabilityBindings: []
`),
			code: "service_root_invalid", pointer: "/services/0/root",
		},
		{
			name: "portable volume service root",
			data: catalogYAML(`  - id: foundation
    root: C:/backend/foundation
    capabilityBindings: []
`),
			code: "service_root_invalid", pointer: "/services/0/root",
		},
		{
			name: "duplicate service root",
			data: catalogYAML(validService + `  - id: sample
    root: backend/foundation
    capabilityBindings: []
`),
			code: "service_root_duplicate", pointer: "/services/1/root",
		},
		{
			name: "unknown dependency",
			data: catalogYAML(`  - id: foundation
    root: backend/foundation
    dependsOn: [missing]
    capabilityBindings: []
`),
			code: "service_dependency_unknown", pointer: "/services/0/dependsOn/0",
		},
		{
			name: "duplicate dependency",
			data: catalogYAML(validService + `  - id: sample
    root: backend/sample
    dependsOn: [foundation, foundation]
    capabilityBindings: []
`),
			code: "service_dependency_duplicate", pointer: "/services/1/dependsOn/1",
		},
		{
			name: "self dependency",
			data: catalogYAML(`  - id: foundation
    root: backend/foundation
    dependsOn: [foundation]
    capabilityBindings: []
`),
			code: "service_dependency_self", pointer: "/services/0/dependsOn/0",
		},
		{
			name: "dependency cycle",
			data: catalogYAML(`  - id: sample
    root: backend/sample
    dependsOn: [foundation]
    capabilityBindings: []
  - id: foundation
    root: backend/foundation
    dependsOn: [sample]
    capabilityBindings: []
`),
			code: "service_dependency_cycle", pointer: "/services/1/dependsOn/0",
			cycle: []string{"foundation", "sample", "foundation"},
		},
		{
			name: "invalid binding id",
			data: catalogYAML(`  - id: foundation
    root: backend/foundation
    capabilityBindings:
      - id: Example.com/relation
        apiVersion: example.com/relation/v1
`),
			code: "service_binding_id_invalid", pointer: "/services/0/capabilityBindings/0/id",
		},
		{
			name: "invalid binding version",
			data: catalogYAML(`  - id: foundation
    root: backend/foundation
    capabilityBindings:
      - id: example.com/relation
        apiVersion: example.com/relation/v0
`),
			code: "service_binding_version_invalid", pointer: "/services/0/capabilityBindings/0/apiVersion",
		},
		{
			name: "duplicate binding",
			data: catalogYAML(`  - id: foundation
    root: backend/foundation
    capabilityBindings:
      - id: example.com/relation
        apiVersion: example.com/relation/v1
      - id: example.com/relation
        apiVersion: example.com/relation/v2
`),
			code: "service_binding_duplicate", pointer: "/services/0/capabilityBindings/1/id",
		},
		{
			name: "config ref escape hatch",
			data: catalogYAML(`  - id: foundation
    root: backend/foundation
    configRef: config/service.yaml
    capabilityBindings: []
`),
			code: "service_catalog_invalid", reason: "document_unknown_field", pointer: "/services/0/configRef",
		},
		{
			name: "extensions escape hatch",
			data: "apiVersion: nexa.dev/service-catalog/v1\nkind: ServiceCatalog\nextensions: {}\nservices: []\n",
			code: "service_catalog_invalid", reason: "document_unknown_field", pointer: "/extensions",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := servicecatalog.Parse("services.yaml", []byte(test.data))
			catalogError := requireCatalogError(t, err, test.code, test.reason)
			if catalogError.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", catalogError.Pointer(), test.pointer)
			}
			if got := catalogError.Cycle(); !reflect.DeepEqual(got, test.cycle) {
				t.Fatalf("Cycle() = %v, want %v", got, test.cycle)
			}
			if len(test.cycle) > 0 {
				cycle := catalogError.Cycle()
				cycle[0] = "mutated"
				if reflect.DeepEqual(catalogError.Cycle(), cycle) {
					t.Fatal("mutating Cycle() result changed later result")
				}
				if strings.Contains(catalogError.Error(), "foundation") || strings.Contains(catalogError.Error(), "sample") {
					t.Fatalf("Error() leaks cycle identifiers: %q", catalogError.Error())
				}
			}
		})
	}
}

func TestInvalidCatalogReportsFirstDocumentPointer(t *testing.T) {
	data := catalogYAML(`  - id: Foundation
    root: .
    capabilityBindings: []
  - id: AlsoBad
    root: /absolute
    capabilityBindings: []
`)
	_, err := servicecatalog.Parse("services.yaml", []byte(data))
	catalogError := requireCatalogError(t, err, "service_id_invalid", "")
	if catalogError.Pointer() != "/services/0/id" {
		t.Fatalf("Pointer() = %q, want /services/0/id", catalogError.Pointer())
	}
}

func TestInvalidCatalogPreservesNormalizedNulls(t *testing.T) {
	tests := []struct {
		name      string
		jsonValue string
		yamlValue string
		pointer   string
	}{
		{
			name: "services null", jsonValue: `null`, yamlValue: " null\n",
			pointer: "/services",
		},
		{
			name: "service null", jsonValue: `[null]`, yamlValue: "\n  - null\n",
			pointer: "/services/0",
		},
		{
			name:      "service id null",
			jsonValue: `[{"id":null,"root":"backend/foundation","capabilityBindings":[]}]`,
			yamlValue: "\n  - id: null\n    root: backend/foundation\n    capabilityBindings: []\n",
			pointer:   "/services/0/id",
		},
		{
			name:      "service root null",
			jsonValue: `[{"id":"foundation","root":null,"capabilityBindings":[]}]`,
			yamlValue: "\n  - id: foundation\n    root: null\n    capabilityBindings: []\n",
			pointer:   "/services/0/root",
		},
		{
			name:      "dependsOn null",
			jsonValue: `[{"id":"foundation","root":"backend/foundation","dependsOn":null,"capabilityBindings":[]}]`,
			yamlValue: "\n  - id: foundation\n    root: backend/foundation\n    dependsOn: null\n    capabilityBindings: []\n",
			pointer:   "/services/0/dependsOn",
		},
		{
			name:      "dependency null",
			jsonValue: `[{"id":"foundation","root":"backend/foundation","dependsOn":[null],"capabilityBindings":[]}]`,
			yamlValue: "\n  - id: foundation\n    root: backend/foundation\n    dependsOn: [null]\n    capabilityBindings: []\n",
			pointer:   "/services/0/dependsOn/0",
		},
		{
			name:      "bindings null",
			jsonValue: `[{"id":"foundation","root":"backend/foundation","capabilityBindings":null}]`,
			yamlValue: "\n  - id: foundation\n    root: backend/foundation\n    capabilityBindings: null\n",
			pointer:   "/services/0/capabilityBindings",
		},
		{
			name:      "binding null",
			jsonValue: `[{"id":"foundation","root":"backend/foundation","capabilityBindings":[null]}]`,
			yamlValue: "\n  - id: foundation\n    root: backend/foundation\n    capabilityBindings: [null]\n",
			pointer:   "/services/0/capabilityBindings/0",
		},
		{
			name:      "binding id null",
			jsonValue: `[{"id":"foundation","root":"backend/foundation","capabilityBindings":[{"id":null,"apiVersion":"example.com/relation/v1"}]}]`,
			yamlValue: "\n  - id: foundation\n    root: backend/foundation\n    capabilityBindings:\n      - id: null\n        apiVersion: example.com/relation/v1\n",
			pointer:   "/services/0/capabilityBindings/0/id",
		},
		{
			name:      "binding version null",
			jsonValue: `[{"id":"foundation","root":"backend/foundation","capabilityBindings":[{"id":"example.com/relation","apiVersion":null}]}]`,
			yamlValue: "\n  - id: foundation\n    root: backend/foundation\n    capabilityBindings:\n      - id: example.com/relation\n        apiVersion: null\n",
			pointer:   "/services/0/capabilityBindings/0/apiVersion",
		},
	}

	for _, test := range tests {
		formats := []struct {
			name   string
			source string
			data   string
		}{
			{
				name: "JSON", source: "services.json",
				data: `{"apiVersion":"nexa.dev/service-catalog/v1","kind":"ServiceCatalog","services":` + test.jsonValue + `}`,
			},
			{
				name: "YAML", source: "services.yaml",
				data: "apiVersion: nexa.dev/service-catalog/v1\nkind: ServiceCatalog\nservices:" + test.yamlValue,
			},
		}
		for _, format := range formats {
			t.Run(test.name+"/"+format.name, func(t *testing.T) {
				_, err := servicecatalog.Parse(format.source, []byte(format.data))
				catalogError := requireCatalogError(t, err, "service_catalog_invalid", "document_invalid")
				if catalogError.Pointer() != test.pointer {
					t.Fatalf("Pointer() = %q, want %q", catalogError.Pointer(), test.pointer)
				}
			})
		}
	}
}

func TestInvalidCatalogSelectsFirstCandidateAcrossPhases(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		code    string
		reason  string
		pointer string
	}{
		{
			name: "missing api version before invalid kind",
			data: "kind: Catalog\nservices: []\n",
			code: "service_catalog_invalid", reason: "document_invalid", pointer: "/apiVersion",
		},
		{
			name: "identity wins same-pointer schema const",
			data: "apiVersion: nexa.dev/service-catalog/v2\nkind: Catalog\nservices: []\n",
			code: "service_catalog_invalid", reason: "version_unsupported", pointer: "/apiVersion",
		},
		{
			name: "earlier semantic before later schema",
			data: catalogYAML(`  - id: Foundation
    root: backend/foundation
    capabilityBindings: []
  - id: sample
    capabilityBindings: []
`),
			code: "service_id_invalid", pointer: "/services/0/id",
		},
		{
			name: "object segments compare lexically",
			data: "aaa: true\nkind: ServiceCatalog\nservices: []\n",
			code: "service_catalog_invalid", reason: "document_unknown_field", pointer: "/aaa",
		},
		{
			name: "numeric object keys remain lexical",
			data: "apiVersion: nexa.dev/service-catalog/v1\nkind: ServiceCatalog\n'2': true\n'10': true\nservices: []\n",
			code: "service_catalog_invalid", reason: "document_unknown_field", pointer: "/10",
		},
		{
			name: "root before nested",
			data: "[]\n",
			code: "service_catalog_invalid", reason: "document_invalid", pointer: "",
		},
		{
			name: "array segments compare numerically",
			data: catalogWithMixedArrayFailures(),
			code: "service_id_invalid", pointer: "/services/2/id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := servicecatalog.Parse("services.yaml", []byte(test.data))
			catalogError := requireCatalogError(t, err, test.code, test.reason)
			if catalogError.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", catalogError.Pointer(), test.pointer)
			}
		})
	}
}

func TestCatalogErrorUnwrapUsesStableSentinels(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		data      string
		wantCause string
	}{
		{
			name: "empty", source: "services.yaml", data: " \n\t",
			wantCause: "service catalog empty",
		},
		{
			name: "strict parser", source: "services.json", data: `{`,
			wantCause: "service catalog invalid",
		},
		{
			name: "schema", source: "services.json",
			data:      `{"apiVersion":"nexa.dev/service-catalog/v1","kind":"ServiceCatalog","services":null}`,
			wantCause: "service catalog invalid",
		},
		{
			name: "semantic", source: "services.yaml",
			data: catalogYAML(`  - id: Invalid
    root: backend/invalid
    capabilityBindings: []
`),
			wantCause: "service catalog invalid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := servicecatalog.Parse(test.source, []byte(test.data))
			assertStableCause(t, err, test.wantCause)
		})
	}
}

func TestInvalidCatalogContinuesCandidateCollectionAfterDecodeFailure(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		code    string
		reason  string
		pointer string
	}{
		{
			name: "identity before later unknown field",
			data: "apiVersion: nexa.dev/service-catalog/v2\nkind: ServiceCatalog\nservices: []\nzzz: true\n",
			code: "service_catalog_invalid", reason: "version_unsupported", pointer: "/apiVersion",
		},
		{
			name: "semantic before later unknown field",
			data: catalogYAML(`  - id: Foundation
    root: backend/foundation
    capabilityBindings: []
  - id: sample
    root: backend/sample
    capabilityBindings: []
    zzz: true
`),
			code: "service_id_invalid", pointer: "/services/0/id",
		},
		{
			name: "semantic before later type failure",
			data: catalogYAML(`  - id: Foundation
    root: backend/foundation
    capabilityBindings: []
  - id: sample
    root: backend/sample
    dependsOn: [1]
    capabilityBindings: []
`),
			code: "service_id_invalid", pointer: "/services/0/id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := servicecatalog.Parse("services.yaml", []byte(test.data))
			catalogError := requireCatalogError(t, err, test.code, test.reason)
			if catalogError.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", catalogError.Pointer(), test.pointer)
			}
		})
	}
}

func assertStableCause(t *testing.T, err error, want string) {
	t.Helper()
	cause := errors.Unwrap(err)
	if cause == nil {
		t.Fatal("Unwrap() = nil")
	}
	if cause.Error() != want {
		t.Fatalf("Unwrap().Error() = %q, want %q", cause.Error(), want)
	}
	if next := errors.Unwrap(cause); next != nil {
		t.Fatalf("stable cause unwraps to %T: %v", next, next)
	}
}

func catalogWithMixedArrayFailures() string {
	services := make([]string, 11)
	for index := range services {
		id := fmt.Sprintf("service-%d", index)
		if index == 2 {
			id = "Service-2"
		}
		root := fmt.Sprintf("    root: backend/service-%d\n", index)
		if index == 10 {
			root = ""
		}
		services[index] = fmt.Sprintf(
			"  - id: %s\n%s    capabilityBindings: []\n",
			id, root,
		)
	}
	return catalogYAML(strings.Join(services, ""))
}

func catalogYAML(services string) string {
	return fmt.Sprintf("apiVersion: nexa.dev/service-catalog/v1\nkind: ServiceCatalog\nservices:\n%s", services)
}

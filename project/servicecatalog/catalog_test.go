package servicecatalog_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/nxnminieye/nexa/project/servicecatalog"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const validCatalogYAML = `apiVersion: nexa.dev/service-catalog/v1
kind: ServiceCatalog
services:
  - id: foundation
    root: backend/foundation
    capabilityBindings: []
  - id: sample
    root: backend/sample
    dependsOn: [foundation]
    capabilityBindings:
      - id: example.com/cross-source-relation
        apiVersion: example.com/cross-source-relation/v1
`

func TestEmptyCatalog(t *testing.T) {
	catalog := servicecatalog.Empty()
	if catalog.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", catalog.Len())
	}
	if catalog.APIVersion() != servicecatalog.APIVersion {
		t.Fatalf("APIVersion() = %q, want %q", catalog.APIVersion(), servicecatalog.APIVersion)
	}
	if services := catalog.Services(); len(services) != 0 {
		t.Fatalf("Services() = %v, want empty", services)
	}
	if order := catalog.DependencyOrder(); len(order) != 0 {
		t.Fatalf("DependencyOrder() = %v, want empty", order)
	}
	if sources := catalog.Sources(); len(sources) != 0 {
		t.Fatalf("Sources() = %v, want empty", sources)
	}
	if _, ok := catalog.Lookup("missing"); ok {
		t.Fatal("Lookup(missing) succeeded")
	}
}

func TestSchemaReturnsDefensiveCopy(t *testing.T) {
	first := servicecatalog.Schema()
	if len(first) == 0 {
		t.Fatal("Schema() returned empty content")
	}
	first[0] ^= 0xff
	second := servicecatalog.Schema()
	if first[0] == second[0] {
		t.Fatal("mutating Schema() result changed later result")
	}
}

func TestPublicSchemaValidatesCatalogDocuments(t *testing.T) {
	const documentJSON = `{"apiVersion":"nexa.dev/service-catalog/v1","kind":"ServiceCatalog","services":[{"id":"foundation","root":"backend/foundation","dependsOn":[],"capabilityBindings":[]}]}`
	schema := compiledCatalogPublicSchema(t)
	var document any
	if err := json.Unmarshal([]byte(documentJSON), &document); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatalf("public schema rejected valid catalog: %v", err)
	}

	base := document.(map[string]any)
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing", mutate: func(value map[string]any) { delete(value, "services") }},
		{name: "wrong type", mutate: func(value map[string]any) { value["services"] = "none" }},
		{name: "null", mutate: func(value map[string]any) { value["services"] = nil }},
		{name: "unknown field", mutate: func(value map[string]any) { value["profile"] = "sample" }},
		{name: "wrong apiVersion const", mutate: func(value map[string]any) { value["apiVersion"] = "nexa.dev/service-catalog/v2" }},
		{name: "wrong kind const", mutate: func(value map[string]any) { value["kind"] = "Catalog" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := make(map[string]any, len(base))
			for key, value := range base {
				candidate[key] = value
			}
			test.mutate(candidate)
			if err := schema.Validate(candidate); err == nil {
				t.Fatal("public schema accepted invalid catalog")
			}
		})
	}

	catalog, err := servicecatalog.Parse("services.json", []byte(documentJSON))
	if err != nil {
		t.Fatalf("Parse(valid) error = %v", err)
	}
	if got := catalogProjection(catalog); !reflect.DeepEqual(got, projectedCatalog{
		APIVersion: servicecatalog.APIVersion,
		Services: []projectedService{{
			ID: "foundation", Root: "backend/foundation",
		}},
		Order: []string{"foundation"},
	}) {
		t.Fatalf("catalog projection = %#v", got)
	}
	_, err = servicecatalog.Parse("services.json", []byte(`{"apiVersion":"nexa.dev/service-catalog/v1","kind":"ServiceCatalog","services":[{"id":"Foundation","root":"backend/foundation","capabilityBindings":[]}]}`))
	requireCatalogError(t, err, "service_id_invalid", "")
}

func compiledCatalogPublicSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	var schemaDocument any
	if err := json.Unmarshal(servicecatalog.Schema(), &schemaDocument); err != nil {
		t.Fatalf("Schema() is not JSON: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	const location = "https://nexa.dev/schemas/project/service-catalog-v1.schema.json"
	if err := compiler.AddResource(location, schemaDocument); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestCatalogAccessorsReturnDefensiveCopies(t *testing.T) {
	catalog, err := servicecatalog.Parse("services.yaml", []byte(validCatalogYAML))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	services := catalog.Services()
	services[0] = services[1]
	assertServiceIDs(t, catalog.Services(), []string{"foundation", "sample"})

	order := catalog.DependencyOrder()
	order[0] = order[1]
	assertServiceIDs(t, catalog.DependencyOrder(), []string{"foundation", "sample"})

	sample, ok := catalog.Lookup("sample")
	if !ok {
		t.Fatal("Lookup(sample) did not find service")
	}
	dependencies := sample.DependsOn()
	dependencies[0] = "mutated"
	if got := sample.DependsOn(); !reflect.DeepEqual(got, []string{"foundation"}) {
		t.Fatalf("DependsOn() after mutation = %v", got)
	}

	bindings := sample.CapabilityBindings()
	bindings[0] = servicecatalog.CapabilityBinding{}
	laterBindings := sample.CapabilityBindings()
	if len(laterBindings) != 1 || laterBindings[0].ID() != "example.com/cross-source-relation" {
		t.Fatalf("CapabilityBindings() after mutation = %v", laterBindings)
	}
}

func TestCatalogProjectionIsDeterministicAcrossInputOrder(t *testing.T) {
	first := `apiVersion: nexa.dev/service-catalog/v1
kind: ServiceCatalog
services:
  - id: sample
    root: backend/sample
    dependsOn: [zeta, foundation]
    capabilityBindings:
      - id: example.com/zeta
        apiVersion: example.com/zeta/v2
      - id: example.com/alpha
        apiVersion: example.com/alpha/v1
  - id: zeta
    root: backend/zeta
    capabilityBindings: []
  - id: foundation
    root: backend/foundation
    capabilityBindings: []
`
	second := `apiVersion: nexa.dev/service-catalog/v1
kind: ServiceCatalog
services:
  - id: foundation
    root: backend/foundation
    capabilityBindings: []
  - id: sample
    root: backend/sample
    dependsOn: [foundation, zeta]
    capabilityBindings:
      - id: example.com/alpha
        apiVersion: example.com/alpha/v1
      - id: example.com/zeta
        apiVersion: example.com/zeta/v2
  - id: zeta
    root: backend/zeta
    capabilityBindings: []
`
	firstCatalog, err := servicecatalog.Parse("first.yaml", []byte(first))
	if err != nil {
		t.Fatalf("Parse(first) error = %v", err)
	}
	secondCatalog, err := servicecatalog.Parse("second.yaml", []byte(second))
	if err != nil {
		t.Fatalf("Parse(second) error = %v", err)
	}
	if got, want := catalogProjection(firstCatalog), catalogProjection(secondCatalog); !reflect.DeepEqual(got, want) {
		t.Fatalf("projections differ:\nfirst  = %#v\nsecond = %#v", got, want)
	}
	assertServiceIDs(t, firstCatalog.DependencyOrder(), []string{"foundation", "zeta", "sample"})
}

func TestParseCatalogConcurrently(t *testing.T) {
	wantCatalog, err := servicecatalog.Parse("services.yaml", []byte(validCatalogYAML))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := catalogProjection(wantCatalog)

	const workers = 100
	errors := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			catalog, parseErr := servicecatalog.Parse("services.yaml", []byte(validCatalogYAML))
			if parseErr != nil {
				errors <- parseErr
				return
			}
			if got := catalogProjection(catalog); !reflect.DeepEqual(got, want) {
				errors <- fmt.Errorf("projection = %#v, want %#v", got, want)
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

type projectedCatalog struct {
	APIVersion string
	Services   []projectedService
	Order      []string
}

type projectedService struct {
	ID           string
	Root         string
	Dependencies []string
	Bindings     []projectedBinding
}

type projectedBinding struct {
	ID         string
	APIVersion string
}

func catalogProjection(catalog servicecatalog.Catalog) projectedCatalog {
	projection := projectedCatalog{APIVersion: catalog.APIVersion()}
	for _, service := range catalog.Services() {
		projected := projectedService{
			ID: service.ID(), Root: service.Root(), Dependencies: service.DependsOn(),
		}
		for _, binding := range service.CapabilityBindings() {
			projected.Bindings = append(projected.Bindings, projectedBinding{
				ID: binding.ID(), APIVersion: binding.APIVersion(),
			})
		}
		projection.Services = append(projection.Services, projected)
	}
	projection.Order = serviceIDs(catalog.DependencyOrder())
	return projection
}

func serviceIDs(services []servicecatalog.Service) []string {
	ids := make([]string, len(services))
	for index, service := range services {
		ids[index] = service.ID()
	}
	return ids
}

func assertServiceIDs(t *testing.T, got []servicecatalog.Service, want []string) {
	t.Helper()
	if gotIDs := serviceIDs(got); !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("service IDs = %v, want %v", gotIDs, want)
	}
}

func requireCatalogError(t *testing.T, err error, code, reason string) *servicecatalog.Error {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil")
	}
	var catalogError *servicecatalog.Error
	if !errors.As(err, &catalogError) {
		t.Fatalf("error type = %T, want *servicecatalog.Error", err)
	}
	if catalogError.Code() != code {
		t.Fatalf("Code() = %q, want %q", catalogError.Code(), code)
	}
	if catalogError.Reason() != reason {
		t.Fatalf("Reason() = %q, want %q", catalogError.Reason(), reason)
	}
	return catalogError
}

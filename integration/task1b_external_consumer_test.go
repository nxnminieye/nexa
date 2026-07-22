package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTask1BExternalConsumerPublicRoundTripAndCompileIsolation(t *testing.T) {
	temporary := t.TempDir()
	moduleRoot := filepath.Join(temporary, "consumer")
	if err := os.MkdirAll(filepath.Join(moduleRoot, "good"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(moduleRoot, "unforgeable"), 0o755); err != nil {
		t.Fatal(err)
	}
	module := "module example.com/task1b-consumer\n\ngo 1.25.0\n\nrequire github.com/nxnminieye/nexa v0.0.0\n\n" +
		"replace github.com/nxnminieye/nexa => " + repositoryRoot(t) + "\n"
	writeConsumerFile(t, filepath.Join(moduleRoot, "go.mod"), module)
	writeConsumerFile(t, filepath.Join(moduleRoot, "good", "contract_test.go"), task1BExternalConsumerSource)
	writeConsumerFile(t, filepath.Join(moduleRoot, "unforgeable", "main.go"), task1BUnforgeableSource)

	environment := prepareHermeticExternalModule(t, temporary, moduleRoot)
	t.Cleanup(func() {
		command := exec.Command("go", "clean", "-modcache")
		command.Env = environment
		if combined, err := command.CombinedOutput(); err != nil {
			t.Errorf("clean Task1B external module cache: %v\n%s", err, combined)
		}
	})
	runBusinessContractGo(t, moduleRoot, environment, "test Task1B public round trip", "test", "-mod=readonly", "./good")

	command := exec.Command("go", "test", "-mod=readonly", "./unforgeable")
	command.Dir = moduleRoot
	command.Env = environment
	if err := command.Run(); err == nil {
		t.Fatal("external consumer forged an EntHelperErrorProjection into *nexaent.Error")
	}
}

const task1BExternalConsumerSource = `package good_test

import (
	"encoding/json"
	"testing"

	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
)

func TestPublicContracts(t *testing.T) {
	if acceptsDomainSource(provenance.DomainSource{}) {
		t.Fatal("zero DomainSource crossed the consumer boundary")
	}
	for _, value := range []string{
		"backend/core/rpc/ent/schema",
		nexaent.SchemaAnnotationName,
		nexaent.FieldAnnotationName,
		nexaent.CRUDAnnotationName,
	} {
		parsed, err := provenance.ParseDomainSource(value)
		if err != nil || parsed.String() != value || !acceptsDomainSource(parsed) {
			t.Fatalf("DomainSource round trip: %q, %v", parsed.String(), err)
		}
	}

	transport, err := json.Marshal(nexaent.CRUD())
	if err != nil { t.Fatal(err) }
	_, ownerErr := nexaent.DecodeCRUD(transport)
	projection, ok := nexaent.ProjectEntHelperError(ownerErr)
	if !ok { t.Fatal("real CRUD error was not projected") }
	parsed, validationErr := nexaent.ParseEntHelperErrorProjection(projection.Code(), projection.Reason(), projection.Pointer(), projection.Source())
	if validationErr != nil { t.Fatalf("public tuple rejected: %q", validationErr.Field()) }
	if parsed.Code() != projection.Code() || parsed.Reason() != projection.Reason() || parsed.Pointer() != projection.Pointer() || parsed.Source() != projection.Source() {
		t.Fatal("public tuple round trip changed values")
	}

	fields := []nexaent.EntHelperErrorField{nexaent.EntHelperErrorFieldNone}
	invalid := [][4]string{
		{"bad", "bad", "bad", "/bad"},
		{"annotation_invalid", "bad", "bad", "/bad"},
		{"annotation_invalid", "document_invalid", "bad", "/bad"},
		{"annotation_invalid", "document_invalid", "", "/bad"},
	}
	for _, tuple := range invalid {
		_, invalidErr := nexaent.ParseEntHelperErrorProjection(tuple[0], tuple[1], tuple[2], tuple[3])
		if invalidErr == nil { t.Fatal("invalid tuple was accepted") }
		fields = append(fields, invalidErr.Field())
	}
	want := []nexaent.EntHelperErrorField{
		nexaent.EntHelperErrorFieldNone,
		nexaent.EntHelperErrorFieldCode,
		nexaent.EntHelperErrorFieldReason,
		nexaent.EntHelperErrorFieldPointer,
		nexaent.EntHelperErrorFieldSource,
	}
	for index, field := range fields {
		if classify(field) != classify(want[index]) { t.Fatalf("field %q was not mapped", field) }
	}
}

func acceptsDomainSource(source provenance.DomainSource) bool { return source.String() != "" }

func classify(field nexaent.EntHelperErrorField) string {
	switch field {
	case nexaent.EntHelperErrorFieldNone:
		return "none"
	case nexaent.EntHelperErrorFieldCode:
		return "code"
	case nexaent.EntHelperErrorFieldReason:
		return "reason"
	case nexaent.EntHelperErrorFieldPointer:
		return "pointer"
	case nexaent.EntHelperErrorFieldSource:
		return "source"
	default:
		return "unknown"
	}
}
`

const task1BUnforgeableSource = `package unforgeable

import "github.com/nxnminieye/nexa/nexaent"

func Forge() *nexaent.Error {
	projection, _ := nexaent.ParseEntHelperErrorProjection("annotation_invalid", "document_invalid", "", nexaent.SchemaAnnotationName)
	return projection
}
`

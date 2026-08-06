package entproto

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/protocol"
)

func TestGenerateProjectsEntCRUDFixture(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "fixtures", "generation", "ent-consumer"))
	generated, err := Generate(context.Background(), Options{
		RepositoryRoot: repositoryRoot,
		SchemaDir:      "schema",
		ServiceID:      "account",
		ProtoPackage:   "account.v1",
		GoPackage:      "example.com/acme/account/v1;accountv1",
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	text := string(generated)
	for _, expected := range []string{
		`// @nexa $contract: "nexa.dev/source-comment/v1"`,
		`// @nexa $source: "ent://schema/account.go#Account"`,
		"ACCOUNT_STATE_ACTIVE = 1;",
		"message Account {",
		"message ListAccountRequest {",
		"service AccountCRUDService {",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("generated Proto is missing %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "message AuditEntry {") {
		t.Fatal("schema without crud.operations was generated")
	}
	if strings.Contains(text, "ACCOUNT_STATE_STATE_") {
		t.Fatal("compiled Ent enum prefix was duplicated")
	}

	baseline := stripSourceDirectives(text)
	if _, err := protocol.Compile(context.Background(), protocol.CompileOptions{
		ServiceID:  "account",
		EntryFiles: []string{"account.generated.proto"},
		Resolver:   protoMapResolver{"account.generated.proto": baseline},
	}); err != nil {
		t.Fatalf("generated Proto syntax is invalid: %v\n%s", err, baseline)
	}
}

func stripSourceDirectives(value string) string {
	lines := strings.Split(value, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(line, " @nexa $source: ") {
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

type protoMapResolver map[string]string

func (r protoMapResolver) Open(_ context.Context, path string) (io.ReadCloser, error) {
	source, ok := r[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(strings.NewReader(source)), nil
}

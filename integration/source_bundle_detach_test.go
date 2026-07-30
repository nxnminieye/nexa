package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"ariga.io/atlas/sql/migrate"
)

func assertDetachedSourceState(t *testing.T, repository string) {
	t.Helper()
	locks, err := filepath.Glob(filepath.Join(repository, ".nexa", "source", "locks", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 0 {
		t.Fatalf("detach retained source ownership snapshots: %#v", locks)
	}
}

func validateDetachedMigrationFacts(t *testing.T, repository string) {
	t.Helper()
	path := filepath.Join(repository, "backend", "core", "migrations", "001_core.sql")
	content := mustReadFile(t, path)
	statements, err := migrate.Stmts(string(content))
	if err != nil {
		t.Fatalf("scan detached Core migration with Atlas: %v", err)
	}
	if len(statements) == 0 {
		t.Fatal("detached Core migration contains no SQL statements")
	}
	previous := -1
	for _, statement := range statements {
		if statement == nil || strings.TrimSpace(statement.Text) == "" || statement.Pos <= previous {
			t.Fatalf("detached Core migration statement is invalid: %#v", statement)
		}
		previous = statement.Pos
	}
}

func decodeStrictJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(mustReadFile(t, path)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		t.Fatalf("decode structured JSON %s: %v", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("structured JSON %s has trailing data: %v", path, err)
	}
}

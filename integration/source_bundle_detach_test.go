package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"ariga.io/atlas/sql/migrate"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
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

func validateDetachedFrontendFacts(t *testing.T, repository string) {
	t.Helper()
	root := filepath.Join(repository, "frontend", "frontend", "core")
	schemaPath := filepath.Join(root, "object-schema", "identity-account.schema.json")
	schemaContent := mustReadFile(t, schemaPath)
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaContent))
	if err != nil {
		t.Fatalf("parse detached object schema: %v", err)
	}
	var schemaView struct {
		ID         string `json:"$id"`
		Properties map[string]struct {
			Title string `json:"title"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schemaContent, &schemaView); err != nil || schemaView.ID == "" || len(schemaView.Properties) == 0 {
		t.Fatalf("decode detached object schema metadata: %v %#v", err, schemaView)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaView.ID, document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(schemaView.ID)
	if err != nil {
		t.Fatalf("compile detached object schema: %v", err)
	}
	if err := compiled.Validate(map[string]any{"username": "alice", "displayName": "Alice", "status": "enabled"}); err != nil {
		t.Fatalf("validate detached object schema instance: %v", err)
	}

	locales := make(map[string]map[string]string, 2)
	for _, locale := range []string{"en-US", "zh-CN"} {
		path := filepath.Join(root, "locales", locale+".json")
		var messages map[string]string
		decodeStrictJSONFile(t, path, &messages)
		if len(messages) == 0 {
			t.Fatalf("detached locale %s is empty", locale)
		}
		for key, value := range messages {
			if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
				t.Fatalf("detached locale %s contains an empty entry", locale)
			}
		}
		locales[locale] = messages
	}
	pagePath := filepath.Join(root, "pages", "identity-accounts.page.json")
	var page struct {
		APIVersion   string   `json:"apiVersion"`
		Kind         string   `json:"kind"`
		ID           string   `json:"id"`
		TitleKey     string   `json:"titleKey"`
		ObjectSchema string   `json:"objectSchema"`
		Views        []string `json:"views"`
	}
	decodeStrictJSONFile(t, pagePath, &page)
	if page.APIVersion != "nexa.dev/frontend-page/v1" || page.Kind != "ObjectPage" || page.ID == "" || len(page.Views) == 0 {
		t.Fatalf("detached page contract = %#v", page)
	}
	boundSchema := filepath.Clean(filepath.Join(filepath.Dir(pagePath), filepath.FromSlash(page.ObjectSchema)))
	if boundSchema != schemaPath {
		t.Fatalf("detached page objectSchema resolves to %q, want %q", boundSchema, schemaPath)
	}
	for _, messages := range locales {
		if messages[page.TitleKey] == "" {
			t.Fatalf("detached page title key %q is unresolved", page.TitleKey)
		}
		for name, property := range schemaView.Properties {
			if property.Title == "" || messages[property.Title] == "" {
				t.Fatalf("detached schema property %s title %q is unresolved", name, property.Title)
			}
		}
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

package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"ariga.io/atlas/sql/migrate"
	"github.com/nxnminieye/nexa/generation/frontend"
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
	locales := make(map[string]map[string]string, 2)
	for _, localeID := range []string{"en-US", "zh-CN"} {
		path := filepath.Join(root, "locales", localeID+".json")
		content := mustReadFile(t, path)
		parsed, err := frontend.ParseLocale(detachedSourcePath(t, repository, path), content)
		if err != nil {
			t.Fatalf("parse detached locale %s: %v", localeID, err)
		}
		if parsed.Locale() != localeID {
			t.Fatalf("detached locale identity = %q, want %q", parsed.Locale(), localeID)
		}
		var envelope struct {
			Messages map[string]string `json:"messages"`
		}
		if err := json.Unmarshal(content, &envelope); err != nil || len(envelope.Messages) == 0 {
			t.Fatalf("decode detached locale %s: %v", localeID, err)
		}
		for key, value := range envelope.Messages {
			if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
				t.Fatalf("detached locale %s contains an empty entry", localeID)
			}
		}
		locales[localeID] = envelope.Messages
	}

	wantPages := map[string]string{
		"accounts.page.json": "core-accounts", "members.page.json": "core-members", "menus.page.json": "core-menus",
		"permissions.page.json": "core-permissions", "roles.page.json": "core-roles", "tenants.page.json": "core-tenants",
	}
	pagePaths, err := filepath.Glob(filepath.Join(root, "pages", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pagePaths) != len(wantPages) {
		t.Fatalf("detached Core pages = %#v", pagePaths)
	}
	for _, pagePath := range pagePaths {
		name := filepath.Base(pagePath)
		wantID, ok := wantPages[name]
		if !ok {
			t.Fatalf("unexpected detached Core page %q", name)
		}
		content := mustReadFile(t, pagePath)
		parsed, err := frontend.ParsePageSpec(detachedSourcePath(t, repository, pagePath), content)
		if err != nil {
			t.Fatalf("parse detached page %s: %v", name, err)
		}
		if parsed.ID() != wantID {
			t.Fatalf("detached page %s ID = %q, want %q", name, parsed.ID(), wantID)
		}
		var page struct {
			TitleKey string `json:"titleKey"`
			Menu     *struct {
				TitleKey string `json:"titleKey"`
			} `json:"menu"`
			Fields []struct {
				LabelKey string `json:"labelKey"`
				Choices  []struct {
					LabelKey string `json:"labelKey"`
				} `json:"choices"`
				Columns []struct {
					LabelKey string `json:"labelKey"`
				} `json:"columns"`
			} `json:"fields"`
			Actions []struct {
				LabelKey   string `json:"labelKey"`
				ConfirmKey string `json:"confirmKey"`
			} `json:"actions"`
		}
		if err := json.Unmarshal(content, &page); err != nil {
			t.Fatalf("decode detached page %s locale keys: %v", name, err)
		}
		keys := []string{page.TitleKey}
		if page.Menu != nil {
			keys = append(keys, page.Menu.TitleKey)
		}
		for _, field := range page.Fields {
			keys = append(keys, field.LabelKey)
			for _, choice := range field.Choices {
				keys = append(keys, choice.LabelKey)
			}
			for _, column := range field.Columns {
				keys = append(keys, column.LabelKey)
			}
		}
		for _, action := range page.Actions {
			keys = append(keys, action.LabelKey)
			if action.ConfirmKey != "" {
				keys = append(keys, action.ConfirmKey)
			}
		}
		for _, key := range keys {
			for localeID, messages := range locales {
				if key == "" || messages[key] == "" {
					t.Fatalf("detached page %s locale %s key %q is unresolved", name, localeID, key)
				}
			}
		}
		delete(wantPages, name)
	}
	if len(wantPages) != 0 {
		t.Fatalf("missing detached Core pages = %#v", wantPages)
	}
}

func detachedSourcePath(t *testing.T, repository, path string) string {
	t.Helper()
	relative, err := filepath.Rel(repository, path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(relative)
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

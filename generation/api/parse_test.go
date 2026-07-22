package api_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/nxnminieye/nexa/generation/api"
)

func TestDocumentSchemaDefensiveCopyAndStructuralValidation(t *testing.T) {
	first := api.DocumentSchema()
	if len(first) == 0 || !json.Valid(first) {
		t.Fatalf("DocumentSchema() is not JSON: %q", first)
	}
	first[0] ^= 0xff
	if bytes.Equal(first, api.DocumentSchema()) {
		t.Fatal("mutating DocumentSchema() changed later result")
	}

	valid := canonicalWireJSON(t)
	if _, err := api.Parse("manifest.json", valid); err != nil {
		t.Fatalf("valid canonical document rejected: %v", err)
	}
	empty, err := api.NewManifest(api.ManifestSpec{})
	if err != nil {
		t.Fatal(err)
	}
	emptyJSON, err := empty.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var structural map[string]any
	if err := json.Unmarshal(emptyJSON, &structural); err != nil {
		t.Fatal(err)
	}
	structural["sources"] = nil
	nullSources, _ := json.Marshal(structural)
	_, err = api.Parse("manifest.json", nullSources)
	manifestError := requireAPIError(t, err, "document_invalid")
	if manifestError.Pointer() != "/sources" {
		t.Fatalf("Pointer() = %q", manifestError.Pointer())
	}
}

func TestParseStrictJSONAndYAMLErrors(t *testing.T) {
	valid := string(canonicalWireJSON(t))
	tests := []struct {
		name, source, data, reason, pointer string
	}{
		{name: "unknown field", source: "manifest.json", data: strings.TrimSuffix(valid, "}\n") + `,"extra":true}` + "\n", reason: "document_unknown_field", pointer: "/extra"},
		{name: "duplicate key", source: "manifest.json", data: strings.Replace(valid, `"kind":"APIManifest"`, `"kind":"APIManifest","kind":"APIManifest"`, 1), reason: "document_duplicate_key", pointer: "/kind"},
		{name: "trailing", source: "manifest.json", data: valid + `{}`, reason: "document_trailing_input", pointer: ""},
		{name: "missing kind", source: "manifest.json", data: strings.Replace(valid, `"kind":"APIManifest",`, "", 1), reason: "document_invalid", pointer: "/kind"},
		{name: "null operation list", source: "manifest.json", data: nullMember(t, valid, "operations"), reason: "document_invalid", pointer: "/operations"},
		{name: "yaml alias", source: "manifest.yaml", data: "apiVersion: nexa.dev/api-manifest/v1\nkind: APIManifest\nsourceDigest: &d sha256:b69c2b986427008bd25b57cfb4f7d4a0e13dd42d63278a8f709593aac5b32633\nsources: []\nschemas: []\noperations: *d\n", reason: "document_alias_forbidden", pointer: "/operations"},
		{name: "yaml merge", source: "manifest.yaml", data: "apiVersion: nexa.dev/api-manifest/v1\nkind: APIManifest\nsourceDigest: sha256:b69c2b986427008bd25b57cfb4f7d4a0e13dd42d63278a8f709593aac5b32633\nsources: []\nschemas: []\noperations:\n  - <<: {}\n", reason: "document_merge_key_forbidden", pointer: "/operations/0/<<"},
		{name: "yaml custom tag", source: "manifest.yaml", data: "apiVersion: nexa.dev/api-manifest/v1\nkind: APIManifest\nsourceDigest: sha256:b69c2b986427008bd25b57cfb4f7d4a0e13dd42d63278a8f709593aac5b32633\nsources: !custom []\nschemas: []\noperations: []\n", reason: "document_tag_forbidden", pointer: "/sources"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := api.Parse(test.source, []byte(test.data))
			manifestError := requireAPIError(t, err, test.reason)
			if manifestError.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", manifestError.Pointer(), test.pointer)
			}
		})
	}
}

func TestParseEmptyYAMLMatchesCanonicalJSON(t *testing.T) {
	data := []byte("apiVersion: nexa.dev/api-manifest/v1\nkind: APIManifest\nsourceDigest: sha256:b69c2b986427008bd25b57cfb4f7d4a0e13dd42d63278a8f709593aac5b32633\nsources: []\nschemas: []\noperations: []\n")
	manifest, err := api.Parse("manifest.yaml", data)
	if err != nil {
		t.Fatalf("Parse(YAML) error = %v", err)
	}
	encoded, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"apiVersion":"nexa.dev/api-manifest/v1","kind":"APIManifest","sourceDigest":"sha256:b69c2b986427008bd25b57cfb4f7d4a0e13dd42d63278a8f709593aac5b32633","sources":[],"schemas":[],"operations":[]}` + "\n"
	if string(encoded) != want {
		t.Fatalf("canonical YAML = %s", encoded)
	}
}

func TestParseRejectsStoredSourceDigestMismatch(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(canonicalWireJSON(t), &document); err != nil {
		t.Fatal(err)
	}
	document["sourceDigest"] = "sha256:" + strings.Repeat("a", 64)
	data, _ := json.Marshal(document)
	_, err := api.Parse("manifest.json", data)
	manifestError := requireAPIError(t, err, "source_digest_mismatch")
	if manifestError.Pointer() != "/sourceDigest" {
		t.Fatalf("Pointer() = %q", manifestError.Pointer())
	}
}

func TestParseSelectsFirstCandidateAcrossPhases(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(canonicalWireJSON(t), &document); err != nil {
		t.Fatal(err)
	}
	document["apiVersion"] = "nexa.dev/api-manifest/v2"
	document["zzz"] = true
	data, _ := json.Marshal(document)
	_, err := api.Parse("manifest.json", data)
	manifestError := requireAPIError(t, err, "version_unsupported")
	if manifestError.Pointer() != "/apiVersion" {
		t.Fatalf("Pointer() = %q", manifestError.Pointer())
	}

	delete(document, "zzz")
	document["apiVersion"] = api.APIVersion
	operations := document["operations"].([]any)
	operations[0].(map[string]any)["id"] = "Invalid"
	document["zzz"] = true
	data, _ = json.Marshal(document)
	_, err = api.Parse("manifest.json", data)
	manifestError = requireAPIError(t, err, "operation_id_invalid")
	if manifestError.Pointer() != "/operations/0/id" {
		t.Fatalf("Pointer() = %q", manifestError.Pointer())
	}
}

func TestParseUsesNumericArrayPointerOrder(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(canonicalWireJSON(t), &document); err != nil {
		t.Fatal(err)
	}
	template := document["operations"].([]any)[0].(map[string]any)
	operations := make([]any, 11)
	for index := range operations {
		encoded, _ := json.Marshal(template)
		var operation map[string]any
		_ = json.Unmarshal(encoded, &operation)
		operation["id"] = fmt.Sprintf("sample.operation-%d", index)
		operation["path"] = fmt.Sprintf("/samples/%d/{id}", index)
		operations[index] = operation
	}
	operations[2].(map[string]any)["id"] = "InvalidTwo"
	operations[10].(map[string]any)["id"] = "InvalidTen"
	document["operations"] = operations
	data, _ := json.Marshal(document)
	_, err := api.Parse("manifest.json", data)
	manifestError := requireAPIError(t, err, "operation_id_invalid")
	if manifestError.Pointer() != "/operations/2/id" {
		t.Fatalf("Pointer() = %q", manifestError.Pointer())
	}
}

func TestParseCookieCredentialConflictUsesCredentialLocation(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(canonicalWireJSON(t), &document); err != nil {
		t.Fatal(err)
	}
	operation := document["operations"].([]any)[0].(map[string]any)
	auth := operation["auth"].(map[string]any)
	auth["credentials"] = []any{map[string]any{"id": "session", "type": "session-cookie", "in": "cookie", "name": "SessionID"}}
	bindings := operation["requestBindings"].([]any)
	bindings[3].(map[string]any)["name"] = "Cookie"
	data, _ := json.MarshalIndent(document, "", "  ")
	for _, source := range []string{"manifest.json", "manifest.yaml"} {
		t.Run(source, func(t *testing.T) {
			_, err := api.Parse(source, data)
			manifestError := requireAPIError(t, err, "credential_binding_conflict")
			if manifestError.Pointer() != "/operations/0/auth/credentials/0/name" {
				t.Fatalf("Pointer() = %q", manifestError.Pointer())
			}
			if manifestError.Line() == 0 || manifestError.Column() == 0 {
				t.Fatalf("location = %d:%d", manifestError.Line(), manifestError.Column())
			}
		})
	}
}

func TestAPIErrorDoesNotExposeRawCause(t *testing.T) {
	_, first := api.Parse("manifest.json", []byte(`{"apiVersion":`))
	_, second := api.Parse("other.json", []byte(`{"apiVersion":`))
	firstError := requireAPIError(t, first, "document_invalid")
	secondError := requireAPIError(t, second, "document_invalid")
	if firstError.Unwrap() == nil || !errors.Is(firstError, secondError.Unwrap()) {
		t.Fatal("errors do not unwrap to a stable sentinel")
	}
	if errors.Unwrap(firstError.Unwrap()) != nil {
		t.Fatal("stable sentinel unwrap exposes another cause")
	}
	if strings.Contains(firstError.Error(), "unexpected") || strings.Contains(firstError.Error(), "EOF") {
		t.Fatalf("Error() leaks parser details: %q", firstError.Error())
	}
}

func TestConcurrentParseAndCanonicalDeterministic(t *testing.T) {
	data := canonicalWireJSON(t)
	wantManifest, err := api.Parse("manifest.json", data)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := wantManifest.CanonicalJSON()
	const workers = 100
	errorsChannel := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			manifest, parseErr := api.Parse("manifest.json", data)
			if parseErr != nil {
				errorsChannel <- parseErr
				return
			}
			encoded, encodeErr := manifest.CanonicalJSON()
			if encodeErr != nil {
				errorsChannel <- encodeErr
				return
			}
			if !bytes.Equal(encoded, want) {
				errorsChannel <- fmt.Errorf("canonical mismatch")
			}
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Fatal(err)
	}
}

func canonicalWireJSON(t *testing.T) []byte {
	t.Helper()
	manifest, err := api.NewManifest(validWireSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func nullMember(t *testing.T, data, member string) string {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(data), &document); err != nil {
		t.Fatal(err)
	}
	document[member] = nil
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

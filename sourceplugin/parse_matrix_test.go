package sourceplugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/internal/strictdoc"
	"gopkg.in/yaml.v3"
)

type documentPath []any

func TestParseRequiredAbsentNullAndListEntryParity(t *testing.T) {
	required := []struct {
		name    string
		path    documentPath
		pointer string
	}{
		{name: "api version", path: documentPath{"apiVersion"}, pointer: "/apiVersion"},
		{name: "kind", path: documentPath{"kind"}, pointer: "/kind"},
		{name: "identity", path: documentPath{"identity"}, pointer: "/identity"},
		{name: "files", path: documentPath{"files"}, pointer: "/files"},
		{name: "profiles", path: documentPath{"profiles"}, pointer: "/profiles"},
		{name: "provider ID", path: documentPath{"identity", "providerId"}, pointer: "/identity/providerId"},
		{name: "module path", path: documentPath{"identity", "modulePath"}, pointer: "/identity/modulePath"},
		{name: "package path", path: documentPath{"identity", "packagePath"}, pointer: "/identity/packagePath"},
		{name: "version", path: documentPath{"identity", "version"}, pointer: "/identity/version"},
		{name: "file path", path: documentPath{"files", 0, "path"}, pointer: "/files/0/path"},
		{name: "file size", path: documentPath{"files", 0, "size"}, pointer: "/files/0/size"},
		{name: "file digest", path: documentPath{"files", 0, "digest"}, pointer: "/files/0/digest"},
		{name: "file mode", path: documentPath{"files", 0, "mode"}, pointer: "/files/0/mode"},
		{name: "profile ID", path: documentPath{"profiles", 0, "id"}, pointer: "/profiles/0/id"},
		{name: "profile files", path: documentPath{"profiles", 0, "files"}, pointer: "/profiles/0/files"},
		{name: "requirement provider", path: documentPath{"profiles", 0, "requiresBundles", 0, "providerId"}, pointer: "/profiles/0/requiresBundles/0/providerId"},
		{name: "requirement module", path: documentPath{"profiles", 0, "requiresBundles", 0, "modulePath"}, pointer: "/profiles/0/requiresBundles/0/modulePath"},
		{name: "requirement package", path: documentPath{"profiles", 0, "requiresBundles", 0, "packagePath"}, pointer: "/profiles/0/requiresBundles/0/packagePath"},
		{name: "requirement version", path: documentPath{"profiles", 0, "requiresBundles", 0, "version"}, pointer: "/profiles/0/requiresBundles/0/version"},
		{name: "requirement profile", path: documentPath{"profiles", 0, "requiresBundles", 0, "profileId"}, pointer: "/profiles/0/requiresBundles/0/profileId"},
		{name: "requirement manifest digest", path: documentPath{"profiles", 0, "requiresBundles", 0, "manifestDigest"}, pointer: "/profiles/0/requiresBundles/0/manifestDigest"},
		{name: "requirement tree digest", path: documentPath{"profiles", 0, "requiresBundles", 0, "treeDigest"}, pointer: "/profiles/0/requiresBundles/0/treeDigest"},
		{name: "validation ID", path: documentPath{"profiles", 0, "validations", 0, "id"}, pointer: "/profiles/0/validations/0/id"},
		{name: "validation kind", path: documentPath{"profiles", 0, "validations", 0, "kind"}, pointer: "/profiles/0/validations/0/kind"},
		{name: "validation workdir", path: documentPath{"profiles", 0, "validations", 0, "workingDirectory"}, pointer: "/profiles/0/validations/0/workingDirectory"},
		{name: "validation packages", path: documentPath{"profiles", 0, "validations", 0, "packages"}, pointer: "/profiles/0/validations/0/packages"},
	}
	for _, field := range required {
		for _, mutation := range []string{"absent", "null"} {
			for _, format := range []string{"json", "yaml"} {
				t.Run(format+"/"+mutation+"/"+field.name, func(t *testing.T) {
					document := cloneTestDocument(completeTestDocument())
					if mutation == "absent" {
						deleteTestDocumentPath(document, field.path)
					} else {
						setTestDocumentPath(document, field.path, nil)
					}
					_, err := Parse("manifest."+format, encodeTestDocument(t, format, document))
					projected := assertSourceError(t, err, "source_manifest_invalid", "document_invalid", field.pointer)
					if projected.Source() != "manifest."+format || projected.Line() == 0 || projected.Column() == 0 {
						t.Fatalf("diagnostics = %q %d:%d", projected.Source(), projected.Line(), projected.Column())
					}
				})
			}
		}
	}

	entries := []struct {
		name    string
		path    documentPath
		pointer string
	}{
		{name: "file", path: documentPath{"files", 0}, pointer: "/files/0"},
		{name: "profile", path: documentPath{"profiles", 0}, pointer: "/profiles/0"},
		{name: "profile file", path: documentPath{"profiles", 0, "files", 0}, pointer: "/profiles/0/files/0"},
		{name: "required profile", path: documentPath{"profiles", 0, "requiresProfiles", 0}, pointer: "/profiles/0/requiresProfiles/0"},
		{name: "bundle requirement", path: documentPath{"profiles", 0, "requiresBundles", 0}, pointer: "/profiles/0/requiresBundles/0"},
		{name: "validation", path: documentPath{"profiles", 0, "validations", 0}, pointer: "/profiles/0/validations/0"},
		{name: "validation package", path: documentPath{"profiles", 0, "validations", 0, "packages", 0}, pointer: "/profiles/0/validations/0/packages/0"},
	}
	for _, entry := range entries {
		for _, format := range []string{"json", "yaml"} {
			t.Run(format+"/null-entry/"+entry.name, func(t *testing.T) {
				document := cloneTestDocument(completeTestDocument())
				setTestDocumentPath(document, entry.path, nil)
				_, err := Parse("manifest."+format, encodeTestDocument(t, format, document))
				assertSourceError(t, err, "source_manifest_invalid", "document_invalid", entry.pointer)
			})
		}
	}

	for _, format := range []string{"json", "yaml"} {
		t.Run(format+"/root-null", func(t *testing.T) {
			_, err := Parse("manifest."+format, encodeTestDocument(t, format, nil))
			assertSourceError(t, err, "source_manifest_invalid", "document_invalid", "")
		})
	}
}

func TestParseOptionalListAbsenceCanonicalizesLikeEmpty(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		explicit := completeTestDocument()
		profile := explicit["profiles"].([]any)[0].(map[string]any)
		profile["requiresProfiles"] = []any{}
		profile["requiresBundles"] = []any{}
		profile["validations"] = []any{}
		explicitManifest, err := Parse("explicit."+format, encodeTestDocument(t, format, explicit))
		if err != nil {
			t.Fatal(err)
		}
		explicitJSON, _ := explicitManifest.CanonicalJSON()
		for _, field := range []string{"requiresProfiles", "requiresBundles", "validations"} {
			t.Run(format+"/"+field, func(t *testing.T) {
				absent := cloneTestDocument(explicit)
				delete(absent["profiles"].([]any)[0].(map[string]any), field)
				manifest, err := Parse("absent."+format, encodeTestDocument(t, format, absent))
				if err != nil {
					t.Fatal(err)
				}
				canonical, _ := manifest.CanonicalJSON()
				if !bytes.Equal(canonical, explicitJSON) || manifest.Digest() != explicitManifest.Digest() {
					t.Fatalf("absence changed canonical value\nexplicit=%s\nabsent=%s", explicitJSON, canonical)
				}
			})
		}
	}
}

func TestManifestOwnerSurfaceRejectsForeignFacts(t *testing.T) {
	foreignFacts := map[string]any{
		"runtime":        map[string]any{"port": 8080},
		"deployment":     map[string]any{"replicas": 1},
		"entSchema":      map[string]any{"crud": true},
		"proto":          map[string]any{"service": "sample"},
		"api":            map[string]any{"route": "/sample"},
		"serviceCatalog": map[string]any{"service": "sample"},
		"configRef":      "foreign.yaml",
		"extensions":     map[string]any{"anything": true},
	}
	for field, value := range foreignFacts {
		for _, format := range []string{"json", "yaml"} {
			t.Run(format+"/"+field, func(t *testing.T) {
				document := stableBaseDocument()
				document[field] = value
				_, err := Parse("manifest."+format, encodeTestDocument(t, format, document))
				assertSourceError(t, err, "source_manifest_invalid", "document_unknown_field", "")
			})
		}
	}
}

func TestParseStableIDBoundariesAtEverySchemaOwner(t *testing.T) {
	owners := []struct {
		name            string
		build           func(string) map[string]any
		authoredPointer string
		publicPointer   string
		code            string
		reason          string
	}{
		{
			name: "profile declaration", build: stableProfileDeclarationDocument,
			authoredPointer: "/profiles/0/id", publicPointer: "/profiles/0/id",
			code: "source_profile_invalid", reason: "profile_id_invalid",
		},
		{
			name: "profile dependency", build: stableProfileDependencyDocument,
			authoredPointer: "/profiles/0/requiresProfiles/0", publicPointer: "/profiles/1/requiresProfiles/0",
			code: "source_profile_invalid", reason: "profile_dependency_invalid",
		},
		{
			name: "requirement profile", build: stableRequirementProfileDocument,
			authoredPointer: "/profiles/0/requiresBundles/0/profileId", publicPointer: "/profiles/0/requiresBundles/0/profileId",
			code: "source_bundle_requirement_invalid", reason: "requirement_profile_invalid",
		},
		{
			name: "validation ID", build: stableValidationIDDocument,
			authoredPointer: "/profiles/0/validations/0/id", publicPointer: "/profiles/0/validations/0/id",
			code: "source_validation_invalid", reason: "validation_id_invalid",
		},
	}
	for _, owner := range owners {
		for _, format := range []string{"json", "yaml"} {
			for _, length := range []int{1, MaxStableIDBytes} {
				t.Run(format+"/"+owner.name+"/valid/"+fmt.Sprint(length), func(t *testing.T) {
					value := "a" + strings.Repeat("b", length-1)
					if _, err := Parse("manifest."+format, encodeTestDocument(t, format, owner.build(value))); err != nil {
						t.Fatalf("%d-byte stable ID rejected: %v", length, err)
					}
				})
			}
			t.Run(format+"/"+owner.name+"/invalid/129", func(t *testing.T) {
				value := "a" + strings.Repeat("b", MaxStableIDBytes)
				data := encodeTestDocument(t, format, owner.build(value))
				document, err := parseStrictTestDocument(format, data)
				if err != nil {
					t.Fatal(err)
				}
				wantLine, wantColumn, ok := document.Location(owner.authoredPointer)
				if !ok {
					t.Fatalf("strict document has no location for %s", owner.authoredPointer)
				}
				_, err = Parse("manifest."+format, data)
				projected := assertSourceError(t, err, owner.code, owner.reason, owner.publicPointer)
				if projected.Line() != wantLine || projected.Column() != wantColumn {
					t.Fatalf("location = %d:%d, want authored %d:%d", projected.Line(), projected.Column(), wantLine, wantColumn)
				}
			})
		}
	}
}

func TestParseHostileNestedUnknownAndDuplicateDiagnosticsAreSafe(t *testing.T) {
	unknowns := []struct {
		name    string
		token   string
		pointer string
		mutate  func(map[string]any, string)
	}{
		{
			name: "absolute identity", token: "/private/var/token", pointer: "/identity",
			mutate: func(document map[string]any, token string) { document["identity"].(map[string]any)[token] = "secret" },
		},
		{
			name: "credential file", token: "credential-token-secret", pointer: "/files/0",
			mutate: func(document map[string]any, token string) {
				document["files"].([]any)[0].(map[string]any)[token] = "secret"
			},
		},
		{
			name: "control profile", token: "line\ncontrol\u200bkey", pointer: "/profiles/0",
			mutate: func(document map[string]any, token string) {
				document["profiles"].([]any)[0].(map[string]any)[token] = "secret"
			},
		},
		{
			name: "over limit validation", token: strings.Repeat("x", 512), pointer: "/profiles/0/validations/0",
			mutate: func(document map[string]any, token string) {
				profile := document["profiles"].([]any)[0].(map[string]any)
				profile["validations"] = completeTestDocument()["profiles"].([]any)[0].(map[string]any)["validations"]
				profile["validations"].([]any)[0].(map[string]any)[token] = "secret"
			},
		},
	}
	for _, tt := range unknowns {
		for _, format := range []string{"json", "yaml"} {
			t.Run(format+"/unknown/"+tt.name, func(t *testing.T) {
				document := stableBaseDocument()
				tt.mutate(document, tt.token)
				_, err := Parse("safe/manifest."+format, encodeTestDocument(t, format, document))
				pointer := tt.pointer
				if tt.name == "control profile" {
					pointer = ""
				}
				projected := assertSourceError(t, err, "source_manifest_invalid", "document_unknown_field", pointer)
				assertHostileDiagnosticNotExposed(t, projected, tt.token, "safe/manifest."+format)
			})
		}
	}

	duplicates := []struct {
		name     string
		token    string
		pointer  string
		jsonData func(string) string
		yamlData func(string) string
	}{
		{
			name: "identity absolute credential", token: "/absolute/credential-token", pointer: "/identity",
			jsonData: func(token string) string { return fmt.Sprintf(`{"identity":{%q:null,%q:null}}`, token, token) },
			yamlData: func(token string) string { return fmt.Sprintf("identity:\n  %q: null\n  %q: null\n", token, token) },
		},
		{
			name: "profile control overlimit", token: "secret\n" + strings.Repeat("z", 512), pointer: "/profiles/0",
			jsonData: func(token string) string { return fmt.Sprintf(`{"profiles":[{%q:null,%q:null}]}`, token, token) },
			yamlData: func(token string) string { return fmt.Sprintf("profiles:\n  - %q: null\n    %q: null\n", token, token) },
		},
	}
	for _, tt := range duplicates {
		for _, format := range []string{"json", "yaml"} {
			t.Run(format+"/duplicate/"+tt.name, func(t *testing.T) {
				data := tt.jsonData(tt.token)
				if format == "yaml" {
					data = tt.yamlData(tt.token)
				}
				_, err := Parse("safe/manifest."+format, []byte(data))
				projected := assertSourceError(t, err, "source_manifest_invalid", "document_duplicate_key", tt.pointer)
				assertHostileDiagnosticNotExposed(t, projected, tt.token, "safe/manifest."+format)
			})
		}
	}
}

func TestParsedCycleAndRequirementConflictDiagnosticsAcrossFormatsAndOrder(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		for _, reverse := range []bool{false, true} {
			t.Run(format+"/cycle/reverse="+fmt.Sprint(reverse), func(t *testing.T) {
				documentValue, authoredPointer := cycleTestDocument(reverse)
				data := encodeTestDocument(t, format, documentValue)
				document, err := parseStrictTestDocument(format, data)
				if err != nil {
					t.Fatal(err)
				}
				wantLine, wantColumn, ok := document.Location(authoredPointer)
				if !ok {
					t.Fatalf("missing authored edge %s", authoredPointer)
				}
				_, err = Parse("graph."+format, data)
				projected := assertSourceError(t, err, "source_profile_cycle", "profile_cycle", "/profiles/1/requiresProfiles/0")
				if projected.Source() != "graph."+format || projected.Line() != wantLine || projected.Column() != wantColumn ||
					!reflect.DeepEqual(projected.Cycle(), []string{"backend", "base", "backend"}) {
					t.Fatalf("cycle diagnostics = %v at %q %d:%d, want %d:%d", projected.Cycle(), projected.Source(), projected.Line(), projected.Column(), wantLine, wantColumn)
				}
			})

			t.Run(format+"/conflict/reverse="+fmt.Sprint(reverse), func(t *testing.T) {
				documentValue, authoredPointer := conflictTestDocument(reverse)
				data := encodeTestDocument(t, format, documentValue)
				document, err := parseStrictTestDocument(format, data)
				if err != nil {
					t.Fatal(err)
				}
				wantLine, wantColumn, ok := document.Location(authoredPointer)
				if !ok {
					t.Fatalf("missing authored requirement %s", authoredPointer)
				}
				manifest, err := Parse("graph."+format, data)
				if err != nil {
					t.Fatal(err)
				}
				_, err = manifest.ResolveProfile("backend")
				projected := assertSourceError(t, err, "source_bundle_requirement_invalid", "requirement_conflict", "/profiles/0/requiresBundles/0")
				if projected.Source() != "graph."+format || projected.Line() != wantLine || projected.Column() != wantColumn {
					t.Fatalf("conflict diagnostics = %q %d:%d, want %d:%d", projected.Source(), projected.Line(), projected.Column(), wantLine, wantColumn)
				}
			})
		}
	}
}

func cycleTestDocument(reverse bool) (map[string]any, string) {
	base := map[string]any{"id": "base", "files": []any{}, "requiresProfiles": []any{"backend"}, "requiresBundles": []any{}, "validations": []any{}}
	backend := map[string]any{"id": "backend", "files": []any{}, "requiresProfiles": []any{"base"}, "requiresBundles": []any{}, "validations": []any{}}
	profiles := []any{base, backend}
	baseAuthoredIndex := 0
	if reverse {
		profiles = []any{backend, base}
		baseAuthoredIndex = 1
	}
	return graphTestDocument(profiles), fmt.Sprintf("/profiles/%d/requiresProfiles/0", baseAuthoredIndex)
}

func conflictTestDocument(reverse bool) (map[string]any, string) {
	base := map[string]any{
		"id": "base", "files": []any{}, "requiresProfiles": []any{},
		"requiresBundles": []any{testBundleRequirement("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")},
		"validations":     []any{},
	}
	backend := map[string]any{
		"id": "backend", "files": []any{}, "requiresProfiles": []any{"base"},
		"requiresBundles": []any{testBundleRequirement("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")},
		"validations":     []any{},
	}
	profiles := []any{backend, base}
	backendAuthoredIndex := 0
	if reverse {
		profiles = []any{base, backend}
		backendAuthoredIndex = 1
	}
	return graphTestDocument(profiles), fmt.Sprintf("/profiles/%d/requiresBundles/0", backendAuthoredIndex)
}

func graphTestDocument(profiles []any) map[string]any {
	return map[string]any{
		"apiVersion": APIVersion, "kind": Kind,
		"identity": map[string]any{
			"providerId": "sample.foundation", "modulePath": "example.com/sample/foundation",
			"packagePath": "example.com/sample/foundation/source", "version": "v0.1.0",
		},
		"files": []any{}, "profiles": profiles,
	}
}

func testBundleRequirement(treeHex string) map[string]any {
	return map[string]any{
		"providerId": "sample.common", "modulePath": "example.com/sample/common",
		"packagePath": "example.com/sample/common/source", "version": "v0.2.0", "profileId": "base",
		"manifestDigest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"treeDigest":     "sha256:" + treeHex,
	}
}

func assertHostileDiagnosticNotExposed(t *testing.T, projected *Error, hostile, source string) {
	t.Helper()
	if projected.Source() != source || projected.Line() == 0 || projected.Column() == 0 || projected.Cycle() != nil {
		t.Fatalf("diagnostics = source %q at %d:%d cycle=%v", projected.Source(), projected.Line(), projected.Column(), projected.Cycle())
	}
	publicValues := []string{
		projected.Code(), projected.Reason(), projected.Source(), projected.Pointer(), projected.Error(), projected.Class().Error(),
	}
	for _, value := range publicValues {
		if strings.Contains(value, hostile) {
			t.Fatalf("hostile token exposed through public diagnostics")
		}
	}
}

func stableBaseDocument() map[string]any {
	document := completeTestDocument()
	profile := document["profiles"].([]any)[0].(map[string]any)
	profile["requiresProfiles"] = []any{}
	profile["requiresBundles"] = []any{}
	profile["validations"] = []any{}
	return document
}

func stableProfileDeclarationDocument(value string) map[string]any {
	document := stableBaseDocument()
	document["profiles"].([]any)[0].(map[string]any)["id"] = value
	return document
}

func stableProfileDependencyDocument(value string) map[string]any {
	document := stableBaseDocument()
	root := document["profiles"].([]any)[0].(map[string]any)
	root["requiresProfiles"] = []any{value}
	document["profiles"] = append(document["profiles"].([]any), map[string]any{
		"id": value, "files": []any{}, "requiresProfiles": []any{}, "requiresBundles": []any{}, "validations": []any{},
	})
	return document
}

func stableRequirementProfileDocument(value string) map[string]any {
	document := stableBaseDocument()
	requirement := completeTestDocument()["profiles"].([]any)[0].(map[string]any)["requiresBundles"].([]any)[0].(map[string]any)
	requirement["profileId"] = value
	document["profiles"].([]any)[0].(map[string]any)["requiresBundles"] = []any{requirement}
	return document
}

func stableValidationIDDocument(value string) map[string]any {
	document := stableBaseDocument()
	validation := completeTestDocument()["profiles"].([]any)[0].(map[string]any)["validations"].([]any)[0].(map[string]any)
	validation["id"] = value
	document["profiles"].([]any)[0].(map[string]any)["validations"] = []any{validation}
	return document
}

func parseStrictTestDocument(format string, data []byte) (strictdoc.Document, error) {
	if format == "json" {
		return strictdoc.ParseJSON("manifest.json", data)
	}
	return strictdoc.ParseYAML("manifest.yaml", data)
}

func completeTestDocument() map[string]any {
	return map[string]any{
		"apiVersion": APIVersion,
		"kind":       Kind,
		"identity": map[string]any{
			"providerId": "sample.foundation", "modulePath": "example.com/sample/foundation",
			"packagePath": "example.com/sample/foundation/source", "version": "v0.1.0",
		},
		"files": []any{map[string]any{
			"path": "main.go", "size": int64(4),
			"digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "mode": "0644",
		}},
		"profiles": []any{map[string]any{
			"id": "root", "files": []any{"main.go"}, "requiresProfiles": []any{"root"},
			"requiresBundles": []any{map[string]any{
				"providerId": "sample.common", "modulePath": "example.com/sample/common",
				"packagePath": "example.com/sample/common/source", "version": "v0.2.0", "profileId": "base",
				"manifestDigest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				"treeDigest":     "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			}},
			"validations": []any{map[string]any{
				"id": "test", "kind": "go-test", "workingDirectory": ".", "packages": []any{"."},
			}},
		}},
	}
}

func cloneTestDocument(document map[string]any) map[string]any {
	encoded, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		panic(err)
	}
	return clone
}

func encodeTestDocument(t *testing.T, format string, document any) []byte {
	t.Helper()
	var (
		encoded []byte
		err     error
	)
	if format == "json" {
		encoded, err = json.MarshalIndent(document, "", "  ")
	} else {
		encoded, err = yaml.Marshal(document)
	}
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func deleteTestDocumentPath(document map[string]any, path documentPath) {
	parent, key := testDocumentParent(document, path)
	if field, ok := key.(string); ok {
		delete(parent.(map[string]any), field)
		return
	}
	panic(fmt.Sprintf("cannot delete non-object path %v", path))
}

func setTestDocumentPath(document map[string]any, path documentPath, value any) {
	parent, key := testDocumentParent(document, path)
	switch key := key.(type) {
	case string:
		parent.(map[string]any)[key] = value
	case int:
		parent.([]any)[key] = value
	default:
		panic(fmt.Sprintf("unsupported path component %T", key))
	}
}

func testDocumentParent(document map[string]any, path documentPath) (any, any) {
	if len(path) == 0 {
		panic("empty document path")
	}
	var current any = document
	for _, component := range path[:len(path)-1] {
		switch component := component.(type) {
		case string:
			current = current.(map[string]any)[component]
		case int:
			current = current.([]any)[component]
		default:
			panic(fmt.Sprintf("unsupported path component %T", component))
		}
	}
	return current, path[len(path)-1]
}

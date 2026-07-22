package sourceplugin

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestParseJSONAndYAMLParity(t *testing.T) {
	manifest, err := NewManifest(validManifestSpec())
	if err != nil {
		t.Fatal(err)
	}
	jsonDocument, _ := manifest.CanonicalJSON()
	yamlDocument := []byte(`apiVersion: nexa.dev/source-bundle/v1
kind: SourceBundle
identity:
  providerId: sample.foundation
  modulePath: example.com/sample/foundation
  packagePath: example.com/sample/foundation/source
  version: v0.1.0
files:
  - path: go.mod
    size: 8
    digest: ` + validManifestSpec().Files[1].Digest.String() + `
    mode: "0644"
  - path: backend/main.go
    size: 12
    digest: ` + validManifestSpec().Files[0].Digest.String() + `
    mode: "0644"
profiles:
  - id: base
    files: [go.mod]
  - id: backend
    files: [backend/main.go]
    requiresProfiles: [base]
    requiresBundles:
      - providerId: sample.common
        modulePath: example.com/sample/common
        packagePath: example.com/sample/common/source
        version: v0.2.0
        profileId: base
        manifestDigest: ` + validRequirement().ManifestDigest.String() + `
        treeDigest: ` + validRequirement().TreeDigest.String() + `
    validations:
      - id: backend-test
        kind: go-test
        workingDirectory: .
        packages: [./backend/...]
`)
	fromJSON, err := Parse("provider/manifest.json", jsonDocument)
	if err != nil {
		t.Fatal(err)
	}
	fromYAML, err := Parse("provider/manifest.yaml", yamlDocument)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := fromJSON.CanonicalJSON()
	right, _ := fromYAML.CanonicalJSON()
	if !bytes.Equal(left, right) || fromJSON.Digest() != fromYAML.Digest() {
		t.Fatalf("JSON/YAML parity failed\nJSON: %s\nYAML: %s", left, right)
	}
	fromYML, err := Parse("provider/manifest.yml", yamlDocument)
	if err != nil {
		t.Fatal(err)
	}
	ymlJSON, _ := fromYML.CanonicalJSON()
	if !bytes.Equal(left, ymlJSON) || fromJSON.Digest() != fromYML.Digest() {
		t.Fatalf(".yml parity failed\nJSON: %s\nYML: %s", left, ymlJSON)
	}
}

func TestParseRejectsUnsafeSourceBeforeDocumentDecode(t *testing.T) {
	tests := []string{"/absolute.json", `C:/volume.json`, `bad\\path.json`, ".", ".git/manifest.json", ".GIT/manifest.json", ".nexa/source/manifest.json", ".NEXA/SOURCE/manifest.json", "bad\nname.json", "bad\x00name.json", "e\u0301.json", strings.Repeat("a", MaxSourceLabelBytes) + ".json"}
	for _, source := range tests {
		t.Run(source, func(t *testing.T) {
			_, err := Parse(source, []byte("not a document"))
			projected := assertSourceError(t, err, "source_manifest_invalid", "source_location_invalid", "")
			if projected.Source() != "" || projected.Line() != 0 || projected.Column() != 0 {
				t.Fatalf("unsafe source leaked diagnostics: source=%q at %d:%d", projected.Source(), projected.Line(), projected.Column())
			}
		})
	}
}

func TestParseRoutesOnlyExactLowercaseSuffix(t *testing.T) {
	for _, source := range []string{"manifest", "manifest.txt", "manifest.JSON", "manifest.Yaml"} {
		_, err := Parse(source, []byte(`{"apiVersion":"wrong"}`))
		projected := assertSourceError(t, err, "source_manifest_invalid", "source_format_unsupported", "")
		if projected.Source() != source || projected.Line() != 0 || projected.Column() != 0 {
			t.Fatalf("format error diagnostics = %q %d:%d", projected.Source(), projected.Line(), projected.Column())
		}
	}
}

func TestParseProjectsStrictDocumentErrorsWithoutHostileText(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		data    string
		reason  string
		pointer string
		hostile string
	}{
		{name: "JSON unknown root", source: "manifest.json", data: `{"apiVersion":"nexa.dev/source-bundle/v1","kind":"SourceBundle","identity":{"providerId":"sample.foundation","modulePath":"example.com/sample/foundation","packagePath":"example.com/sample/foundation/source","version":"v0.1.0"},"files":[],"profiles":[],"credential/token\nsecret":"x"}`, reason: "document_unknown_field", pointer: "", hostile: "credential"},
		{name: "JSON duplicate known", source: "manifest.json", data: `{"apiVersion":"a","apiVersion":"b"}`, reason: "document_duplicate_key", pointer: "/apiVersion"},
		{name: "JSON trailing", source: "manifest.json", data: `{ } { }`, reason: "document_trailing_input", pointer: ""},
		{name: "YAML alias", source: "manifest.yaml", data: "apiVersion: &v nexa.dev/source-bundle/v1\nkind: *v\n", reason: "document_alias_forbidden", pointer: "/kind"},
		{name: "YAML duplicate known", source: "manifest.yaml", data: "apiVersion: a\napiVersion: b\n", reason: "document_duplicate_key", pointer: "/apiVersion"},
		{name: "YAML merge", source: "manifest.yaml", data: "identity: &i {providerId: sample}\n<<: *i\n", reason: "document_merge_key_forbidden", pointer: ""},
		{name: "YAML tag", source: "manifest.yaml", data: "apiVersion: !secret x\n", reason: "document_tag_forbidden", pointer: "/apiVersion", hostile: "secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.source, []byte(tt.data))
			projected := assertSourceError(t, err, "source_manifest_invalid", tt.reason, tt.pointer)
			if projected.Source() != tt.source {
				t.Fatalf("source = %q, want %q", projected.Source(), tt.source)
			}
			if tt.hostile != "" && strings.Contains(projected.Error(), tt.hostile) {
				t.Fatalf("Error leaked hostile text: %q", projected.Error())
			}
		})
	}
}

func TestParseRejectsNullAbsentAndForbiddenFacts(t *testing.T) {
	base := `{"apiVersion":"nexa.dev/source-bundle/v1","kind":"SourceBundle","identity":{"providerId":"sample.foundation","modulePath":"example.com/sample/foundation","packagePath":"example.com/sample/foundation/source","version":"v0.1.0"},"files":[],"profiles":[]}`
	tests := []struct {
		name    string
		data    string
		pointer string
		reason  string
	}{
		{name: "root null", data: `null`, pointer: "", reason: "document_invalid"},
		{name: "required absent", data: `{}`, pointer: "/apiVersion", reason: "document_invalid"},
		{name: "object null", data: strings.Replace(base, `"identity":{"providerId":"sample.foundation","modulePath":"example.com/sample/foundation","packagePath":"example.com/sample/foundation/source","version":"v0.1.0"}`, `"identity":null`, 1), pointer: "/identity", reason: "document_invalid"},
		{name: "scalar null", data: strings.Replace(base, `"providerId":"sample.foundation"`, `"providerId":null`, 1), pointer: "/identity/providerId", reason: "document_invalid"},
		{name: "required null", data: strings.Replace(base, `"files":[]`, `"files":null`, 1), pointer: "/files", reason: "document_invalid"},
		{name: "nested required absent", data: strings.Replace(base, `"profiles":[]`, `"profiles":[{"id":"base"}]`, 1), pointer: "/profiles/0/files", reason: "document_invalid"},
		{name: "optional null", data: strings.Replace(base, `"profiles":[]`, `"profiles":[{"id":"base","files":[],"requiresProfiles":null}]`, 1), pointer: "/profiles/0/requiresProfiles", reason: "document_invalid"},
		{name: "runtime field", data: strings.Replace(base, `"profiles":[]`, `"profiles":[],"port":8080`, 1), pointer: "", reason: "document_unknown_field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("manifest.json", []byte(tt.data))
			assertSourceError(t, err, "source_manifest_invalid", tt.reason, tt.pointer)
		})
	}
}

func TestParseSemanticErrorsMatchTypedConstructor(t *testing.T) {
	base := `{"apiVersion":"nexa.dev/source-bundle/v1","kind":"SourceBundle","identity":{"providerId":"sample.foundation","modulePath":"example.com/sample/foundation","packagePath":"example.com/sample/foundation/source","version":"v0.1.0"},"files":[{"path":"main.go","size":4,"digest":"` + validManifestSpec().Files[0].Digest.String() + `","mode":"0644"}],"profiles":[{"id":"base","files":["main.go"]}]}`
	tests := []struct {
		name    string
		data    string
		code    string
		reason  string
		pointer string
	}{
		{name: "provider", data: strings.Replace(base, `sample.foundation`, `Bad`, 1), code: "source_manifest_invalid", reason: "provider_id_invalid", pointer: "/identity/providerId"},
		{name: "version", data: strings.Replace(base, `v0.1.0`, `latest`, 1), code: "source_manifest_invalid", reason: "version_invalid", pointer: "/identity/version"},
		{name: "path", data: strings.Replace(base, `main.go`, `.git/config`, 1), code: "source_path_invalid", reason: "path_reserved", pointer: "/files/0/path"},
		{name: "size", data: strings.Replace(base, `"size":4`, `"size":-1`, 1), code: "source_file_invalid", reason: "file_size_invalid", pointer: "/files/0/size"},
		{name: "digest", data: strings.Replace(base, validManifestSpec().Files[0].Digest.String(), `sha256:short`, 1), code: "source_file_invalid", reason: "file_digest_invalid", pointer: "/files/0/digest"},
		{name: "mode", data: strings.Replace(base, `"mode":"0644"`, `"mode":"0777"`, 1), code: "source_file_invalid", reason: "file_mode_invalid", pointer: "/files/0/mode"},
		{name: "profile id", data: strings.Replace(base, `"id":"base"`, `"id":"Bad"`, 1), code: "source_profile_invalid", reason: "profile_id_invalid", pointer: "/profiles/0/id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("manifest.json", []byte(tt.data))
			projected := assertSourceError(t, err, tt.code, tt.reason, tt.pointer)
			if projected.Source() != "manifest.json" || projected.Line() == 0 || projected.Column() == 0 {
				t.Fatalf("semantic diagnostics missing: %#v", projected)
			}
		})
	}
}

func TestParseRejectsUnsupportedVersionAndKindBeforeSemanticValidation(t *testing.T) {
	base := `{"apiVersion":"nexa.dev/source-bundle/v1","kind":"SourceBundle","identity":{"providerId":"sample.foundation","modulePath":"example.com/sample/foundation","packagePath":"example.com/sample/foundation/source","version":"v0.1.0"},"files":[],"profiles":[]}`
	tests := []struct {
		name    string
		data    string
		reason  string
		pointer string
	}{
		{name: "version", data: strings.Replace(base, APIVersion, "nexa.dev/source-bundle/v2", 1), reason: "version_unsupported", pointer: "/apiVersion"},
		{name: "kind", data: strings.Replace(base, Kind, "RuntimePlugin", 1), reason: "kind_invalid", pointer: "/kind"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("manifest.json", []byte(tt.data))
			assertSourceError(t, err, "source_manifest_invalid", tt.reason, tt.pointer)
		})
	}
}

func TestParseJSONDoesNotAcceptYAMLSyntax(t *testing.T) {
	for _, data := range []string{"apiVersion: nexa.dev/source-bundle/v1\n", "{\"apiVersion\": \"x\" # comment\n}"} {
		_, err := Parse("manifest.json", []byte(data))
		assertSourceError(t, err, "source_manifest_invalid", "document_invalid", "")
	}
}

func TestParseStructuralListEntryErrorsRemainDocumentErrors(t *testing.T) {
	base := `{"apiVersion":"nexa.dev/source-bundle/v1","kind":"SourceBundle","identity":{"providerId":"sample.foundation","modulePath":"example.com/sample/foundation","packagePath":"example.com/sample/foundation/source","version":"v0.1.0"},"files":[],"profiles":[]}`
	tests := []struct {
		data    string
		pointer string
	}{
		{data: strings.Replace(base, `"files":[]`, `"files":[null]`, 1), pointer: "/files/0"},
		{data: strings.Replace(base, `"profiles":[]`, `"profiles":[null]`, 1), pointer: "/profiles/0"},
		{data: strings.Replace(base, `"profiles":[]`, `"profiles":[{"id":"base","files":[null]}]`, 1), pointer: "/profiles/0/files/0"},
	}
	for _, tt := range tests {
		_, err := Parse("manifest.json", []byte(tt.data))
		assertSourceError(t, err, "source_manifest_invalid", "document_invalid", tt.pointer)
	}
}

func TestParseSemanticPointerIsCanonicalButLocationIsAuthored(t *testing.T) {
	document := []byte(`apiVersion: nexa.dev/source-bundle/v1
kind: SourceBundle
identity:
  providerId: sample.foundation
  modulePath: example.com/sample/foundation
  packagePath: example.com/sample/foundation/source
  version: v0.1.0
files:
  - path: z.go
    size: 1
    digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    mode: "0777"
  - path: a.go
    size: 1
    digest: sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    mode: "0644"
profiles:
  - id: all
    files: [z.go, a.go]
`)
	_, err := Parse("provider/manifest.yaml", document)
	projected := assertSourceError(t, err, "source_file_invalid", "file_mode_invalid", "/files/1/mode")
	if projected.Source() != "provider/manifest.yaml" || projected.Line() != 12 || projected.Column() != 11 {
		t.Fatalf("canonical pointer lost authored location: source=%q line=%d column=%d", projected.Source(), projected.Line(), projected.Column())
	}
}

func TestParseTruncatesOverBoundNumericPointerComponents(t *testing.T) {
	hostile := strings.Repeat("9", 4096)
	hostileYAML := strings.Repeat("9", 64)
	tests := []struct {
		name    string
		source  string
		data    string
		pointer string
		hostile string
	}{
		{name: "JSON root files", source: "manifest.json", data: fmt.Sprintf(`{"files":{%q:null,%q:null}}`, hostile, hostile), pointer: "/files", hostile: hostile},
		{name: "JSON nested profile files", source: "manifest.json", data: fmt.Sprintf(`{"profiles":[{"files":{%q:null,%q:null}}]}`, hostile, hostile), pointer: "/profiles/0/files", hostile: hostile},
		{name: "YAML root profiles", source: "manifest.yaml", data: fmt.Sprintf("profiles:\n  %q: null\n  %q: null\n", hostileYAML, hostileYAML), pointer: "/profiles", hostile: hostileYAML},
		{name: "YAML nested validations", source: "manifest.yaml", data: fmt.Sprintf("profiles:\n  - validations:\n      %q: null\n      %q: null\n", hostileYAML, hostileYAML), pointer: "/profiles/0/validations", hostile: hostileYAML},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.source, []byte(tt.data))
			projected := assertSourceError(t, err, "source_manifest_invalid", "document_duplicate_key", tt.pointer)
			if projected.Source() != tt.source || projected.Line() == 0 || projected.Column() == 0 {
				t.Fatalf("diagnostics = source %q at %d:%d", projected.Source(), projected.Line(), projected.Column())
			}
			for _, public := range []string{projected.Pointer(), projected.Error(), projected.Code(), projected.Reason(), projected.Source()} {
				if strings.Contains(public, tt.hostile) {
					t.Fatal("hostile numeric component escaped through typed diagnostics")
				}
			}
		})
	}
}

func TestParseNumericPointerUsesStable31BitProtocolBound(t *testing.T) {
	tests := []struct {
		index   string
		pointer string
	}{
		{index: "2147483647", pointer: "/files/2147483647"},
		{index: "2147483648", pointer: "/files"},
	}
	for _, tt := range tests {
		t.Run(tt.index, func(t *testing.T) {
			data := fmt.Sprintf(`{"files":{%q:null,%q:null}}`, tt.index, tt.index)
			_, err := Parse("manifest.json", []byte(data))
			assertSourceError(t, err, "source_manifest_invalid", "document_duplicate_key", tt.pointer)
		})
	}
}

func TestParseSelectsStableIDByAuthoredOrderAndProjectsCanonicalPointer(t *testing.T) {
	jsonPrefix := `{
  "apiVersion": "nexa.dev/source-bundle/v1",
  "kind": "SourceBundle",
  "identity": {
    "providerId": "sample.foundation",
    "modulePath": "example.com/sample/foundation",
    "packagePath": "example.com/sample/foundation/source",
    "version": "v0.1.0"
  },
  "files": [],
  "profiles": [
`
	yamlPrefix := `apiVersion: nexa.dev/source-bundle/v1
kind: SourceBundle
identity:
  providerId: sample.foundation
  modulePath: example.com/sample/foundation
  packagePath: example.com/sample/foundation/source
  version: v0.1.0
files: []
profiles:
`
	tests := []struct {
		name    string
		source  string
		data    string
		code    string
		reason  string
		pointer string
		line    int
		column  int
	}{
		{
			name: "JSON stable first", source: "manifest.json",
			data: jsonPrefix + "    {\"id\":\"Bad\",\"files\":[]},\n    {\"id\":\"later\",\"files\":null}\n  ]\n}",
			code: "source_profile_invalid", reason: "profile_id_invalid", pointer: "/profiles/0/id", line: 12, column: 11,
		},
		{
			name: "JSON structure first", source: "manifest.json",
			data: jsonPrefix + "    {\"id\":\"later\",\"files\":null},\n    {\"id\":\"Bad\",\"files\":[]}\n  ]\n}",
			code: "source_manifest_invalid", reason: "document_invalid", pointer: "/profiles/1/files", line: 12, column: 27,
		},
		{
			name: "YAML stable first", source: "manifest.yaml",
			data: yamlPrefix + "  - id: Bad\n    files: []\n  - id: later\n    files: null\n",
			code: "source_profile_invalid", reason: "profile_id_invalid", pointer: "/profiles/0/id", line: 10, column: 9,
		},
		{
			name: "YAML structure first", source: "manifest.yaml",
			data: yamlPrefix + "  - id: later\n    files: null\n  - id: Bad\n    files: []\n",
			code: "source_manifest_invalid", reason: "document_invalid", pointer: "/profiles/1/files", line: 11, column: 12,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.source, []byte(tt.data))
			projected := assertSourceError(t, err, tt.code, tt.reason, tt.pointer)
			if projected.Source() != tt.source || projected.Line() != tt.line || projected.Column() != tt.column {
				t.Fatalf("diagnostics = %q %d:%d, want %q %d:%d", projected.Source(), projected.Line(), projected.Column(), tt.source, tt.line, tt.column)
			}
		})
	}
}

func TestParseProjectsWireDecodeLocationWithoutChangingStrictdoc(t *testing.T) {
	tests := []struct {
		name   string
		source string
		data   string
		line   int
		column int
	}{
		{
			name: "JSON", source: "manifest.json", line: 10, column: 12,
			data: `{
  "apiVersion": "nexa.dev/source-bundle/v1",
  "kind": "SourceBundle",
  "identity": {
    "providerId": "sample.foundation",
    "modulePath": "example.com/sample/foundation",
    "packagePath": "example.com/sample/foundation/source",
    "version": "v0.1.0"
  },
  "files": [{
    "path": "main.go",
    "size": 9223372036854775808,
    "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "mode": "0644"
  }],
  "profiles": []
}`,
		},
		{
			name: "YAML", source: "manifest.yaml", line: 9, column: 3,
			data: `apiVersion: nexa.dev/source-bundle/v1
kind: SourceBundle
identity:
  providerId: sample.foundation
  modulePath: example.com/sample/foundation
  packagePath: example.com/sample/foundation/source
  version: v0.1.0
files:
  - path: main.go
    size: 9223372036854775808
    digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    mode: "0644"
profiles: []
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.source, []byte(tt.data))
			projected := assertSourceError(t, err, "source_manifest_invalid", "document_invalid", "/files")
			if projected.Source() != tt.source || projected.Line() != tt.line || projected.Column() != tt.column {
				t.Fatalf("wire diagnostics = %q %d:%d, want %q %d:%d", projected.Source(), projected.Line(), projected.Column(), tt.source, tt.line, tt.column)
			}
		})
	}
}

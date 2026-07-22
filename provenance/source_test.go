package provenance_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/provenance"
)

func TestSourceRepositoryRefRoundTrip(t *testing.T) {
	ref, err := provenance.RepositoryRef("backend/sample/api/desc/base.api", "GetSample")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ref.String(), "repo:backend/sample/api/desc/base.api#GetSample"; got != want {
		t.Fatalf("ref = %q, want %q", got, want)
	}
	parsed, err := provenance.ParseSourceRef(ref.String())
	if err != nil || parsed != ref {
		t.Fatalf("round trip = %v, %v", parsed, err)
	}
	if parsed.Path() != "backend/sample/api/desc/base.api" || parsed.Fragment() != "GetSample" {
		t.Fatalf("parts = %q, %q", parsed.Path(), parsed.Fragment())
	}
}

func TestSourceRepositoryRefWholeDocument(t *testing.T) {
	ref, err := provenance.RepositoryRef("backend/sample/api/desc/base.api", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ref.String(), "repo:backend/sample/api/desc/base.api"; got != want {
		t.Fatalf("ref = %q, want %q", got, want)
	}
	if got := ref.Path(); got != "backend/sample/api/desc/base.api" {
		t.Fatalf("path = %q", got)
	}
	if got := ref.Fragment(); got != "" {
		t.Fatalf("fragment = %q, want empty", got)
	}
}

func TestSourceRefParsesWholeDocument(t *testing.T) {
	parsed, err := provenance.ParseSourceRef("repo:docs/design%20notes/spec.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.String(); got != "repo:docs/design%20notes/spec.yaml" {
		t.Fatalf("ref = %q", got)
	}
	if got := parsed.Path(); got != "docs/design notes/spec.yaml" {
		t.Fatalf("path = %q", got)
	}
	if got := parsed.Fragment(); got != "" {
		t.Fatalf("fragment = %q, want empty", got)
	}
}

func TestSourceRepositoryRefCanonicalEscaping(t *testing.T) {
	ref, err := provenance.RepositoryRef("docs/design notes/spec.yaml", "section #1")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ref.String(), "repo:docs/design%20notes/spec.yaml#section%20%231"; got != want {
		t.Fatalf("ref = %q, want %q", got, want)
	}
	parsed, err := provenance.ParseSourceRef(ref.String())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path() != "docs/design notes/spec.yaml" || parsed.Fragment() != "section #1" {
		t.Fatalf("parts = %q, %q", parsed.Path(), parsed.Fragment())
	}
}

func TestSourceRepositoryRefRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		fragment string
	}{
		{name: "absolute path", path: "/backend/base.api", fragment: "Get"},
		{name: "dot path", path: ".", fragment: "Get"},
		{name: "dot component", path: "backend/./base.api", fragment: "Get"},
		{name: "parent component", path: "backend/../base.api", fragment: "Get"},
		{name: "empty component", path: "backend//base.api", fragment: "Get"},
		{name: "trailing slash", path: "backend/", fragment: "Get"},
		{name: "backslash", path: `backend\base.api`, fragment: "Get"},
		{name: "path NUL", path: "backend/\x00base.api", fragment: "Get"},
		{name: "query", path: "backend/base.api?raw=1", fragment: "Get"},
		{name: "fragment NUL", path: "backend/base.api", fragment: "Get\x00"},
		{name: "fragment query", path: "backend/base.api", fragment: "Get?raw=1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := provenance.RepositoryRef(tt.path, tt.fragment); err == nil {
				t.Fatalf("RepositoryRef(%q, %q) succeeded", tt.path, tt.fragment)
			}
		})
	}
}

func TestSourceRefRejectsInvalidAndNonCanonicalValues(t *testing.T) {
	validPath := "backend/sample.api"
	tests := map[string]string{
		"unknown scheme":           "https:" + validPath + "#Get",
		"missing scheme":           validPath + "#Get",
		"empty path":               "repo:#Get",
		"empty fragment":           "repo:" + validPath + "#",
		"query":                    "repo:" + validPath + "?raw=1#Get",
		"literal space":            "repo:docs/design notes/spec.yaml#Get",
		"lowercase percent hex":    "repo:docs/design%20notes/spec.yaml#Get%2fOne",
		"escaped unreserved":       "repo:backend/%73ample.api#Get",
		"escaped path separator":   "repo:backend%2Fsample.api#Get",
		"escaped dot component":    "repo:backend/%2E/sample.api#Get",
		"escaped parent component": "repo:backend/%2E%2E/sample.api#Get",
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := provenance.ParseSourceRef(value); err == nil {
				t.Fatalf("ParseSourceRef(%q) succeeded", value)
			}
		})
	}
}

func TestSourceWholeDocumentCrossesJSONBoundary(t *testing.T) {
	ref, err := provenance.RepositoryRef("backend/sample.api", "")
	if err != nil {
		t.Fatal(err)
	}
	digest := provenance.SHA256([]byte("stable"))

	encoded, err := json.Marshal(provenance.Source{Ref: ref, Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ref":"repo:backend/sample.api","digest":"` + digest.String() + `"}`
	if got := string(encoded); got != want {
		t.Fatalf("source JSON = %s, want %s", got, want)
	}
}

func TestSourceRepositoryRefRejectsPortableWindowsVolumePaths(t *testing.T) {
	for _, path := range []string{
		"C:/outside/file.api",
		"c:outside/file.api",
		"Z:/outside/file.api",
	} {
		t.Run(path, func(t *testing.T) {
			if _, err := provenance.RepositoryRef(path, "Get"); err == nil {
				t.Fatalf("RepositoryRef(%q, %q) succeeded", path, "Get")
			}
		})
	}
}

func TestSourceRefRejectsCanonicalWindowsVolumePath(t *testing.T) {
	if _, err := provenance.ParseSourceRef("repo:C%3A/outside/file.api#Get"); err == nil {
		t.Fatal("canonical Windows volume reference was accepted")
	}
}

func TestSourceZeroValuesCannotCrossJSONBoundary(t *testing.T) {
	validRef, err := provenance.RepositoryRef("backend/sample.api", "Get")
	if err != nil {
		t.Fatal(err)
	}
	validDigest := provenance.SHA256([]byte("stable"))

	for name, source := range map[string]provenance.Source{
		"zero source": {},
		"zero ref":    {Digest: validDigest},
		"zero digest": {Ref: validRef},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := json.Marshal(source); err == nil {
				t.Fatal("invalid source serialized successfully")
			}
		})
	}

	encoded, err := json.Marshal(provenance.Source{Ref: validRef, Digest: validDigest})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(encoded); !strings.Contains(got, validRef.String()) || !strings.Contains(got, validDigest.String()) {
		t.Fatalf("source JSON = %s", got)
	}
}

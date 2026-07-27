package sourceplugin

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
)

func TestGoModuleRequirementsCanonicalAndImmutable(t *testing.T) {
	without := validManifestSpec()
	empty := validManifestSpec()
	empty.Profiles[0].RequiresGoModules = []GoModuleRequirementSpec{}
	withoutManifest, err := NewManifest(without)
	if err != nil {
		t.Fatal(err)
	}
	emptyManifest, err := NewManifest(empty)
	if err != nil {
		t.Fatal(err)
	}
	withoutCanonical, _ := withoutManifest.CanonicalJSON()
	emptyCanonical, _ := emptyManifest.CanonicalJSON()
	if !bytes.Equal(withoutCanonical, emptyCanonical) || withoutManifest.Digest() != emptyManifest.Digest() || bytes.Contains(withoutCanonical, []byte("requiresGoModules")) {
		t.Fatalf("empty requirement changed v1 canonical manifest\nwithout: %s\nempty: %s", withoutCanonical, emptyCanonical)
	}

	spec := validManifestSpec()
	spec.Profiles[0].RequiresGoModules = []GoModuleRequirementSpec{
		{ModulePath: "golang.org/x/text", Version: "v0.34.0"},
		{ModulePath: "golang.org/x/crypto", Version: "v0.48.0"},
	}
	manifest, err := NewManifest(spec)
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := manifest.LookupProfile("backend")
	if !ok {
		t.Fatal("backend profile missing")
	}
	want := []GoModuleRequirementSpec{
		{ModulePath: "golang.org/x/crypto", Version: "v0.48.0"},
		{ModulePath: "golang.org/x/text", Version: "v0.34.0"},
	}
	if got := goModuleRequirementSpecs(profile.GoModuleRequirements()); !reflect.DeepEqual(got, want) {
		t.Fatalf("requirements = %#v, want %#v", got, want)
	}
	encoded, _ := manifest.CanonicalJSON()
	if !bytes.Contains(encoded, []byte(`"requiresGoModules":[{"modulePath":"golang.org/x/crypto","version":"v0.48.0"},{"modulePath":"golang.org/x/text","version":"v0.34.0"}]`)) {
		t.Fatalf("canonical requirements = %s", encoded)
	}

	spec.Profiles[0].RequiresGoModules[0].ModulePath = "mutated.example/module"
	returned := profile.GoModuleRequirements()
	returned[0] = GoModuleRequirement{}
	again, _ := manifest.LookupProfile("backend")
	if got := goModuleRequirementSpecs(again.GoModuleRequirements()); !reflect.DeepEqual(got, want) {
		t.Fatalf("requirements changed through caller mutation: %#v", got)
	}

	parsed, err := Parse("bundle.json", encoded)
	if err != nil {
		t.Fatal(err)
	}
	parsedCanonical, _ := parsed.CanonicalJSON()
	if !bytes.Equal(parsedCanonical, encoded) || parsed.Digest() != manifest.Digest() {
		t.Fatal("parse round trip changed Go module requirements")
	}
}

func TestGoModuleRequirementValidationAndClosure(t *testing.T) {
	tests := []struct {
		name         string
		requirements []GoModuleRequirementSpec
		reason       string
		pointer      string
	}{
		{name: "invalid module", requirements: []GoModuleRequirementSpec{{ModulePath: "bad path", Version: "v1.0.0"}}, reason: "requirement_invalid", pointer: "/profiles/0/requiresGoModules/0"},
		{name: "noncanonical version", requirements: []GoModuleRequirementSpec{{ModulePath: "example.com/dependency", Version: "v1.0"}}, reason: "requirement_invalid", pointer: "/profiles/0/requiresGoModules/0"},
		{name: "path major mismatch", requirements: []GoModuleRequirementSpec{{ModulePath: "example.com/dependency/v2", Version: "v1.0.0"}}, reason: "requirement_invalid", pointer: "/profiles/0/requiresGoModules/0"},
		{name: "duplicate", requirements: []GoModuleRequirementSpec{{ModulePath: "example.com/dependency", Version: "v1.0.0"}, {ModulePath: "example.com/dependency", Version: "v1.0.0"}}, reason: "requirement_duplicate", pointer: "/profiles/0/requiresGoModules/1"},
		{name: "direct conflict", requirements: []GoModuleRequirementSpec{{ModulePath: "example.com/dependency", Version: "v1.0.0"}, {ModulePath: "example.com/dependency", Version: "v1.1.0"}}, reason: "requirement_conflict", pointer: "/profiles/0/requiresGoModules/1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validManifestSpec()
			spec.Profiles[0].RequiresGoModules = test.requirements
			_, err := NewManifest(spec)
			assertSourceError(t, err, "source_go_module_requirement_invalid", test.reason, test.pointer)
		})
	}

	spec := validManifestSpec()
	spec.Profiles[0].RequiresGoModules = []GoModuleRequirementSpec{{ModulePath: "example.com/dependency", Version: "v1.0.0"}}
	spec.Profiles[1].RequiresGoModules = []GoModuleRequirementSpec{{ModulePath: "example.com/dependency", Version: "v1.0.0"}}
	manifest, err := NewManifest(spec)
	if err != nil {
		t.Fatal(err)
	}
	closure, err := manifest.ResolveProfile("backend")
	if err != nil {
		t.Fatal(err)
	}
	want := []GoModuleRequirementSpec{{ModulePath: "example.com/dependency", Version: "v1.0.0"}}
	if got := goModuleRequirementSpecs(closure.GoModuleRequirements()); !reflect.DeepEqual(got, want) {
		t.Fatalf("deduplicated closure = %#v, want %#v", got, want)
	}
	returned := closure.GoModuleRequirements()
	returned[0] = GoModuleRequirement{}
	if got := goModuleRequirementSpecs(closure.GoModuleRequirements()); !reflect.DeepEqual(got, want) {
		t.Fatalf("closure changed through caller mutation: %#v", got)
	}

	spec.Profiles[0].RequiresGoModules[0].Version = "v1.1.0"
	manifest, err = NewManifest(spec)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manifest.ResolveProfile("backend")
	assertSourceError(t, err, "source_go_module_requirement_invalid", "requirement_conflict", "/profiles/0/requiresGoModules/0")
}

func TestParsedGoModuleRequirementProjectsCanonicalPointerAndAuthoredLocation(t *testing.T) {
	document := []byte(`apiVersion: nexa.dev/source-bundle/v1
kind: SourceBundle
identity:
  providerId: sample.foundation
  modulePath: example.com/sample/foundation
  packagePath: example.com/sample/foundation/source
  version: v0.1.0
files: []
profiles:
  - id: root
    files: []
    requiresGoModules:
      - modulePath: example.com/z
        version: v1.0.0
      - modulePath: bad path
        version: v1.0.0
`)
	parsedDocument, err := parseStrictTestDocument("yaml", document)
	if err != nil {
		t.Fatal(err)
	}
	wantLine, wantColumn, ok := parsedDocument.Location("/profiles/0/requiresGoModules/1")
	if !ok {
		t.Fatal("authored requirement location missing")
	}
	_, err = Parse("provider/bundle.yaml", document)
	projected := assertSourceError(t, err, "source_go_module_requirement_invalid", "requirement_invalid", "/profiles/0/requiresGoModules/0")
	if projected.Source() != "provider/bundle.yaml" || projected.Line() != wantLine || projected.Column() != wantColumn {
		t.Fatalf("diagnostics = %s %d:%d, want %d:%d", projected.Source(), projected.Line(), projected.Column(), wantLine, wantColumn)
	}
}

func TestParsedGoModuleRequirementIsStrict(t *testing.T) {
	base := `{
  "apiVersion":"nexa.dev/source-bundle/v1",
  "kind":"SourceBundle",
  "identity":{"providerId":"sample.foundation","modulePath":"example.com/sample/foundation","packagePath":"example.com/sample/foundation/source","version":"v0.1.0"},
  "files":[],
  "profiles":[{"id":"root","files":[],"requiresGoModules":[%s]}]
}`
	tests := []struct {
		name, requirement, reason, pointer string
	}{
		{name: "missing module", requirement: `{"version":"v1.0.0"}`, reason: "document_invalid", pointer: "/profiles/0/requiresGoModules/0/modulePath"},
		{name: "missing version", requirement: `{"modulePath":"example.com/dependency"}`, reason: "document_invalid", pointer: "/profiles/0/requiresGoModules/0/version"},
		{name: "unknown field", requirement: `{"modulePath":"example.com/dependency","version":"v1.0.0","secret":"value"}`, reason: "document_unknown_field", pointer: "/profiles/0/requiresGoModules/0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse("provider/bundle.json", []byte(fmt.Sprintf(base, test.requirement)))
			assertSourceError(t, err, "source_manifest_invalid", test.reason, test.pointer)
		})
	}
}

func TestParsedGoModuleRequirementConflictUsesAuthoredLocation(t *testing.T) {
	document := []byte(`apiVersion: nexa.dev/source-bundle/v1
kind: SourceBundle
identity:
  providerId: sample.foundation
  modulePath: example.com/sample/foundation
  packagePath: example.com/sample/foundation/source
  version: v0.1.0
files: []
profiles:
  - id: root
    files: []
    requiresProfiles: [base]
    requiresGoModules:
      - modulePath: example.com/dependency
        version: v1.1.0
  - id: base
    files: []
    requiresGoModules:
      - modulePath: example.com/dependency
        version: v1.0.0
`)
	parsedDocument, err := parseStrictTestDocument("yaml", document)
	if err != nil {
		t.Fatal(err)
	}
	wantLine, wantColumn, ok := parsedDocument.Location("/profiles/0/requiresGoModules/0")
	if !ok {
		t.Fatal("authored conflict location missing")
	}
	manifest, err := Parse("provider/bundle.yaml", document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manifest.ResolveProfile("root")
	projected := assertSourceError(t, err, "source_go_module_requirement_invalid", "requirement_conflict", "/profiles/1/requiresGoModules/0")
	if projected.Source() != "provider/bundle.yaml" || projected.Line() != wantLine || projected.Column() != wantColumn {
		t.Fatalf("diagnostics = %s %d:%d, want %d:%d", projected.Source(), projected.Line(), projected.Column(), wantLine, wantColumn)
	}
}

func goModuleRequirementSpecs(requirements []GoModuleRequirement) []GoModuleRequirementSpec {
	result := make([]GoModuleRequirementSpec, len(requirements))
	for index, requirement := range requirements {
		result[index] = GoModuleRequirementSpec{ModulePath: requirement.ModulePath(), Version: requirement.Version()}
	}
	return result
}

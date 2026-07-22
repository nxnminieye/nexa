package sourceplugin

import (
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/provenance"
)

func TestProfileClosureIsDependencyFirstAndDeduplicated(t *testing.T) {
	spec := validManifestSpec()
	shared := validRequirement()
	spec.Profiles[1].RequiresBundles = []BundleRequirementSpec{shared}
	spec.Profiles[1].Validations = []ValidationRecipeSpec{{ID: "base-build", Kind: ValidationGoBuild, WorkingDirectory: ".", Packages: []string{"."}}}
	manifest, err := NewManifest(spec)
	if err != nil {
		t.Fatal(err)
	}
	closure, err := manifest.ResolveProfile("backend")
	if err != nil {
		t.Fatal(err)
	}
	if closure.RootProfileID() != "backend" || !reflect.DeepEqual(closure.ProfileIDs(), []string{"base", "backend"}) {
		t.Fatalf("profile order = %v", closure.ProfileIDs())
	}
	files := closure.Files()
	if len(files) != 2 || files[0].Path() != "backend/main.go" || files[1].Path() != "go.mod" {
		t.Fatalf("closure files = %#v", files)
	}
	if got := closure.BundleRequirements(); len(got) != 1 || got[0].ProviderID() != "sample.common" {
		t.Fatalf("requirements = %#v", got)
	}
	if got := closure.Validations(); len(got) != 2 || got[0].ID() != "base-build" || got[1].ID() != "backend-test" {
		t.Fatalf("validations = %#v", got)
	}
	ids := closure.ProfileIDs()
	ids[0] = "mutated"
	files[0] = File{}
	if closure.ProfileIDs()[0] != "base" || closure.Files()[0].Path() != "backend/main.go" {
		t.Fatal("closure accessors are not defensive")
	}
}

func TestProfileGraphAndClosureErrorsAreDeterministic(t *testing.T) {
	t.Run("unknown request", func(t *testing.T) {
		manifest, _ := NewManifest(validManifestSpec())
		_, err := manifest.ResolveProfile("missing")
		projected := assertSourceError(t, err, "source_profile_not_found", "profile_not_found", "/profile")
		if !errors.Is(projected, ErrProfileNotFound) || errors.Is(projected, ErrManifestInvalid) {
			t.Fatalf("unexpected errors.Is classes: %v", projected)
		}
	})

	t.Run("cycle", func(t *testing.T) {
		spec := validManifestSpec()
		spec.Profiles[1].RequiresProfiles = []string{"backend"}
		_, err := NewManifest(spec)
		projected := assertSourceError(t, err, "source_profile_cycle", "profile_cycle", "/profiles/1/requiresProfiles/0")
		if !errors.Is(projected, ErrProfileCycle) || !errors.Is(projected, ErrManifestInvalid) || !reflect.DeepEqual(projected.Cycle(), []string{"backend", "base", "backend"}) {
			t.Fatalf("cycle error = %#v cycle=%v", projected, projected.Cycle())
		}
		cycle := projected.Cycle()
		cycle[0] = "mutated"
		if projected.Cycle()[0] != "backend" {
			t.Fatal("cycle accessor is not defensive")
		}
		spec.Profiles[0], spec.Profiles[1] = spec.Profiles[1], spec.Profiles[0]
		_, err = NewManifest(spec)
		reversed := assertSourceError(t, err, "source_profile_cycle", "profile_cycle", "/profiles/1/requiresProfiles/0")
		if !reflect.DeepEqual(reversed.Cycle(), []string{"backend", "base", "backend"}) || reversed.Source() != "" || reversed.Line() != 0 || reversed.Column() != 0 {
			t.Fatalf("reversed constructor cycle = %#v at %q %d:%d", reversed.Cycle(), reversed.Source(), reversed.Line(), reversed.Column())
		}
	})

	t.Run("requirement conflict", func(t *testing.T) {
		spec := validManifestSpec()
		conflict := validRequirement()
		conflict.TreeDigest = provenance.SHA256([]byte("other-tree"))
		spec.Profiles[1].RequiresBundles = []BundleRequirementSpec{conflict}
		manifest, err := NewManifest(spec)
		if err != nil {
			t.Fatal(err)
		}
		_, err = manifest.ResolveProfile("backend")
		first := assertSourceError(t, err, "source_bundle_requirement_invalid", "requirement_conflict", "/profiles/0/requiresBundles/0")
		if first.Source() != "" || first.Line() != 0 || first.Column() != 0 {
			t.Fatalf("direct conflict unexpectedly has authored diagnostics: %#v", first)
		}
		spec.Profiles[0], spec.Profiles[1] = spec.Profiles[1], spec.Profiles[0]
		manifest, err = NewManifest(spec)
		if err != nil {
			t.Fatal(err)
		}
		_, err = manifest.ResolveProfile("backend")
		assertSourceError(t, err, "source_bundle_requirement_invalid", "requirement_conflict", "/profiles/0/requiresBundles/0")
	})
}

func TestStableIDAndValidationRecipeContracts(t *testing.T) {
	valid128 := "a" + strings.Repeat("b", MaxStableIDBytes-1)
	tests := []struct {
		name    string
		mutate  func(*ManifestSpec)
		reason  string
		pointer string
	}{
		{name: "profile empty", mutate: func(s *ManifestSpec) { s.Profiles[0].ID = "" }, reason: "profile_id_invalid", pointer: "/profiles/0/id"},
		{name: "profile long", mutate: func(s *ManifestSpec) { s.Profiles[0].ID = valid128 + "x" }, reason: "profile_id_invalid", pointer: "/profiles/0/id"},
		{name: "dependency uppercase", mutate: func(s *ManifestSpec) { s.Profiles[0].RequiresProfiles = []string{"Base"} }, reason: "profile_dependency_invalid", pointer: "/profiles/0/requiresProfiles/0"},
		{name: "requirement profile", mutate: func(s *ManifestSpec) { s.Profiles[0].RequiresBundles[0].ProfileID = "bad_" }, reason: "requirement_profile_invalid", pointer: "/profiles/0/requiresBundles/0/profileId"},
		{name: "validation id", mutate: func(s *ManifestSpec) { s.Profiles[0].Validations[0].ID = "Bad" }, reason: "validation_id_invalid", pointer: "/profiles/0/validations/0/id"},
		{name: "validation kind", mutate: func(s *ManifestSpec) { s.Profiles[0].Validations[0].Kind = "shell" }, reason: "validation_kind_invalid", pointer: "/profiles/0/validations/0/kind"},
		{name: "validation package flag", mutate: func(s *ManifestSpec) { s.Profiles[0].Validations[0].Packages = []string{"-run"} }, reason: "validation_package_invalid", pointer: "/profiles/0/validations/0/packages/0"},
		{name: "validation package version", mutate: func(s *ManifestSpec) { s.Profiles[0].Validations[0].Packages = []string{"./backend@latest"} }, reason: "validation_package_invalid", pointer: "/profiles/0/validations/0/packages/0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validManifestSpec()
			tt.mutate(&spec)
			_, err := NewManifest(spec)
			projected := assertSourceError(t, err, errorCodeForReason(tt.reason), tt.reason, tt.pointer)
			if !errors.Is(projected, ErrManifestInvalid) {
				t.Fatalf("%v does not match ErrManifestInvalid", projected)
			}
		})
	}
	manifest, err := NewManifest(func() ManifestSpec { spec := validManifestSpec(); spec.Profiles[0].ID = valid128; return spec }())
	if err != nil {
		t.Fatalf("128-byte stable ID rejected: %v", err)
	}
	if _, err := manifest.ResolveProfile(valid128); err != nil {
		t.Fatalf("128-byte requested ID rejected: %v", err)
	}
}

func TestStableIDContractAtEveryConsumer(t *testing.T) {
	identity := validManifestSpec().Identity
	minimal := func() ManifestSpec {
		return ManifestSpec{Identity: identity, Files: []FileSpec{}, Profiles: []ProfileSpec{{ID: "root", Files: []string{}}}}
	}
	for _, value := range []string{"a", "a" + strings.Repeat("b", MaxStableIDBytes-1)} {
		t.Run("valid/"+strconv.Itoa(len(value)), func(t *testing.T) {
			declaration := minimal()
			declaration.Profiles[0].ID = value
			manifest, err := NewManifest(declaration)
			if err != nil {
				t.Fatalf("declaration %q: %v", value, err)
			}
			if _, err := manifest.ResolveProfile(value); err != nil {
				t.Fatalf("request %q: %v", value, err)
			}

			dependency := minimal()
			dependency.Profiles = append(dependency.Profiles, ProfileSpec{ID: value, Files: []string{}})
			dependency.Profiles[0].RequiresProfiles = []string{value}
			if _, err := NewManifest(dependency); err != nil {
				t.Fatalf("dependency %q: %v", value, err)
			}

			requirement := minimal()
			exact := validRequirement()
			exact.ProfileID = value
			requirement.Profiles[0].RequiresBundles = []BundleRequirementSpec{exact}
			if _, err := NewManifest(requirement); err != nil {
				t.Fatalf("requirement %q: %v", value, err)
			}

			validation := minimal()
			validation.Profiles[0].Validations = []ValidationRecipeSpec{{ID: value, Kind: ValidationGoTest, WorkingDirectory: ".", Packages: []string{"."}}}
			if _, err := NewManifest(validation); err != nil {
				t.Fatalf("validation %q: %v", value, err)
			}
		})
	}

	invalidValues := []string{"", "a" + strings.Repeat("b", MaxStableIDBytes), "Bad", "é", "e\u0301", ".a", "a.", "a..b"}
	for index, value := range invalidValues {
		t.Run("invalid/"+string(rune('a'+index)), func(t *testing.T) {
			declaration := minimal()
			declaration.Profiles[0].ID = value
			_, err := NewManifest(declaration)
			assertSourceError(t, err, "source_profile_invalid", "profile_id_invalid", "/profiles/0/id")

			dependency := minimal()
			dependency.Profiles = append(dependency.Profiles, ProfileSpec{ID: "base", Files: []string{}})
			dependency.Profiles[0].RequiresProfiles = []string{value}
			_, err = NewManifest(dependency)
			assertSourceError(t, err, "source_profile_invalid", "profile_dependency_invalid", "/profiles/1/requiresProfiles/0")

			requirement := minimal()
			exact := validRequirement()
			exact.ProfileID = value
			requirement.Profiles[0].RequiresBundles = []BundleRequirementSpec{exact}
			_, err = NewManifest(requirement)
			assertSourceError(t, err, "source_bundle_requirement_invalid", "requirement_profile_invalid", "/profiles/0/requiresBundles/0/profileId")

			validation := minimal()
			validation.Profiles[0].Validations = []ValidationRecipeSpec{{ID: value, Kind: ValidationGoTest, WorkingDirectory: ".", Packages: []string{"."}}}
			_, err = NewManifest(validation)
			assertSourceError(t, err, "source_validation_invalid", "validation_id_invalid", "/profiles/0/validations/0/id")

			manifest, err := NewManifest(minimal())
			if err != nil {
				t.Fatal(err)
			}
			_, err = manifest.ResolveProfile(value)
			assertSourceError(t, err, "source_profile_invalid", "profile_id_invalid", "/profile")
		})
	}
}

func TestProfileNodeAndExactRequirementValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ManifestSpec)
		code    string
		reason  string
		pointer string
	}{
		{name: "duplicate profile", mutate: func(s *ManifestSpec) { s.Profiles[0].ID = "base"; s.Profiles[0].RequiresProfiles = nil }, code: "source_profile_invalid", reason: "profile_id_duplicate", pointer: "/profiles/1/id"},
		{name: "duplicate selected file", mutate: func(s *ManifestSpec) { s.Profiles[0].Files = []string{"backend/main.go", "backend/main.go"} }, code: "source_profile_invalid", reason: "profile_file_duplicate", pointer: "/profiles/0/files/1"},
		{name: "unknown selected file", mutate: func(s *ManifestSpec) { s.Profiles[0].Files = []string{"missing.go"} }, code: "source_profile_invalid", reason: "profile_file_unknown", pointer: "/profiles/0/files/0"},
		{name: "duplicate dependency", mutate: func(s *ManifestSpec) { s.Profiles[0].RequiresProfiles = []string{"base", "base"} }, code: "source_profile_invalid", reason: "profile_dependency_duplicate", pointer: "/profiles/0/requiresProfiles/1"},
		{name: "unknown dependency", mutate: func(s *ManifestSpec) { s.Profiles[0].RequiresProfiles = []string{"missing"} }, code: "source_profile_invalid", reason: "profile_dependency_unknown", pointer: "/profiles/0/requiresProfiles/0"},
		{name: "requirement identity", mutate: func(s *ManifestSpec) { s.Profiles[0].RequiresBundles[0].Version = "latest" }, code: "source_bundle_requirement_invalid", reason: "requirement_identity_invalid", pointer: "/profiles/0/requiresBundles/0"},
		{name: "requirement digest", mutate: func(s *ManifestSpec) { s.Profiles[0].RequiresBundles[0].TreeDigest = provenance.Digest{} }, code: "source_bundle_requirement_invalid", reason: "requirement_digest_invalid", pointer: "/profiles/0/requiresBundles/0"},
		{name: "duplicate requirement", mutate: func(s *ManifestSpec) {
			value := s.Profiles[0].RequiresBundles[0]
			s.Profiles[0].RequiresBundles = []BundleRequirementSpec{value, value}
		}, code: "source_bundle_requirement_invalid", reason: "requirement_duplicate", pointer: "/profiles/0/requiresBundles/1"},
		{name: "duplicate validation ID", mutate: func(s *ManifestSpec) {
			s.Profiles[1].Validations = []ValidationRecipeSpec{{ID: "backend-test", Kind: ValidationGoTest, WorkingDirectory: ".", Packages: []string{"."}}}
		}, code: "source_validation_invalid", reason: "validation_id_duplicate", pointer: "/profiles/1/validations/0/id"},
		{name: "validation workdir", mutate: func(s *ManifestSpec) { s.Profiles[0].Validations[0].WorkingDirectory = ".git" }, code: "source_validation_invalid", reason: "validation_workdir_invalid", pointer: "/profiles/0/validations/0/workingDirectory"},
		{name: "duplicate validation package", mutate: func(s *ManifestSpec) { s.Profiles[0].Validations[0].Packages = []string{".", "."} }, code: "source_validation_invalid", reason: "validation_package_duplicate", pointer: "/profiles/0/validations/0/packages/1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validManifestSpec()
			tt.mutate(&spec)
			_, err := NewManifest(spec)
			assertSourceError(t, err, tt.code, tt.reason, tt.pointer)
		})
	}

	manifest, _ := NewManifest(validManifestSpec())
	for _, invalid := range []string{"", "Bad", "bad_", "a..b", "é", "e\u0301", "a" + strings.Repeat("b", MaxStableIDBytes)} {
		_, err := manifest.ResolveProfile(invalid)
		assertSourceError(t, err, "source_profile_invalid", "profile_id_invalid", "/profile")
	}
}

func TestManifestDirectErrorSelectionIsCanonicalProfileByProfile(t *testing.T) {
	identity := validManifestSpec().Identity
	tests := []struct {
		name     string
		profiles []ProfileSpec
		code     string
		reason   string
		pointer  string
	}{
		{
			name: "earlier required files before later invalid ID",
			profiles: []ProfileSpec{
				{ID: "a", Files: nil},
				{ID: "z_", Files: []string{}},
			},
			code: "source_manifest_invalid", reason: "document_invalid", pointer: "/profiles/0/files",
		},
		{
			name: "earlier dependency before later duplicate ID",
			profiles: []ProfileSpec{
				{ID: "a", Files: []string{}, RequiresProfiles: []string{"Bad"}},
				{ID: "z", Files: []string{}},
				{ID: "z", Files: []string{}},
			},
			code: "source_profile_invalid", reason: "profile_dependency_invalid", pointer: "/profiles/0/requiresProfiles/0",
		},
	}
	for _, tt := range tests {
		for _, reverse := range []bool{false, true} {
			t.Run(tt.name+"/reverse="+strconv.FormatBool(reverse), func(t *testing.T) {
				profiles := append([]ProfileSpec(nil), tt.profiles...)
				if reverse {
					slices.Reverse(profiles)
				}
				_, err := NewManifest(ManifestSpec{Identity: identity, Files: []FileSpec{}, Profiles: profiles})
				projected := assertSourceError(t, err, tt.code, tt.reason, tt.pointer)
				if projected.Source() != "" || projected.Line() != 0 || projected.Column() != 0 {
					t.Fatalf("direct error has parsed diagnostics: %q %d:%d", projected.Source(), projected.Line(), projected.Column())
				}
			})
		}
	}
}

func TestValidationRecipeAcceptsOnlyClosedGoPackageForms(t *testing.T) {
	for _, packagePath := range []string{".", "./backend", "./backend/..."} {
		spec := validManifestSpec()
		spec.Profiles[0].Validations[0].Packages = []string{packagePath}
		if _, err := NewManifest(spec); err != nil {
			t.Fatalf("package %q rejected: %v", packagePath, err)
		}
	}
	for _, packagePath := range []string{"", "./...", "backend", "/backend", "-run", "./backend@latest", "./backend/../other"} {
		t.Run("reject/"+packagePath, func(t *testing.T) {
			spec := validManifestSpec()
			spec.Profiles[0].Validations[0].Packages = []string{packagePath}
			_, err := NewManifest(spec)
			assertSourceError(t, err, "source_validation_invalid", "validation_package_invalid", "/profiles/0/validations/0/packages/0")
		})
	}
}

func TestValidationWorkingDirectoryRejectsCaseFoldedControlRoots(t *testing.T) {
	for _, workingDirectory := range []string{".GIT", ".Git/hooks", ".NEXA/SOURCE", ".Nexa/Source/cache"} {
		t.Run(workingDirectory, func(t *testing.T) {
			spec := validManifestSpec()
			spec.Profiles[0].Validations[0].WorkingDirectory = workingDirectory
			_, err := NewManifest(spec)
			assertSourceError(t, err, "source_validation_invalid", "validation_workdir_invalid", "/profiles/0/validations/0/workingDirectory")
		})
	}
}

func errorCodeForReason(reason string) string {
	switch {
	case strings.HasPrefix(reason, "profile_"):
		return "source_profile_invalid"
	case strings.HasPrefix(reason, "requirement_"):
		return "source_bundle_requirement_invalid"
	default:
		return "source_validation_invalid"
	}
}

func TestParsedCycleUsesCanonicalPointerAndAuthoredLocation(t *testing.T) {
	document := []byte(`apiVersion: nexa.dev/source-bundle/v1
kind: SourceBundle
identity:
  providerId: sample.foundation
  modulePath: example.com/sample/foundation
  packagePath: example.com/sample/foundation/source
  version: v0.1.0
files: []
profiles:
  - id: base
    files: []
    requiresProfiles:
      - backend
  - id: backend
    files: []
    requiresProfiles:
      - base
`)
	_, err := Parse("provider/manifest.yaml", document)
	projected := assertSourceError(t, err, "source_profile_cycle", "profile_cycle", "/profiles/1/requiresProfiles/0")
	if projected.Source() != "provider/manifest.yaml" || projected.Line() != 13 || projected.Column() != 9 {
		t.Fatalf("parsed cycle location = %q %d:%d", projected.Source(), projected.Line(), projected.Column())
	}
	reversedDocument := []byte(`apiVersion: nexa.dev/source-bundle/v1
kind: SourceBundle
identity:
  providerId: sample.foundation
  modulePath: example.com/sample/foundation
  packagePath: example.com/sample/foundation/source
  version: v0.1.0
files: []
profiles:
  - id: backend
    files: []
    requiresProfiles:
      - base
  - id: base
    files: []
    requiresProfiles:
      - backend
`)
	_, err = Parse("provider/reversed.yml", reversedDocument)
	reversed := assertSourceError(t, err, "source_profile_cycle", "profile_cycle", "/profiles/1/requiresProfiles/0")
	if !reflect.DeepEqual(reversed.Cycle(), projected.Cycle()) || reversed.Source() != "provider/reversed.yml" || reversed.Line() != 17 || reversed.Column() != 9 {
		t.Fatalf("reversed parsed cycle = %v at %q %d:%d", reversed.Cycle(), reversed.Source(), reversed.Line(), reversed.Column())
	}
}

func TestParsedRequirementConflictUsesCanonicalPointerAndAuthoredObject(t *testing.T) {
	firstDigest := provenance.SHA256([]byte("first-tree")).String()
	secondDigest := provenance.SHA256([]byte("second-tree")).String()
	manifestDigest := provenance.SHA256([]byte("manifest")).String()
	document := []byte(`apiVersion: nexa.dev/source-bundle/v1
kind: SourceBundle
identity:
  providerId: sample.foundation
  modulePath: example.com/sample/foundation
  packagePath: example.com/sample/foundation/source
  version: v0.1.0
files: []
profiles:
  - id: backend
    files: []
    requiresProfiles: [base]
    requiresBundles:
      - providerId: sample.common
        modulePath: example.com/sample/common
        packagePath: example.com/sample/common/source
        version: v0.2.0
        profileId: base
        manifestDigest: ` + manifestDigest + `
        treeDigest: ` + secondDigest + `
  - id: base
    files: []
    requiresBundles:
      - providerId: sample.common
        modulePath: example.com/sample/common
        packagePath: example.com/sample/common/source
        version: v0.2.0
        profileId: base
        manifestDigest: ` + manifestDigest + `
        treeDigest: ` + firstDigest + `
`)
	manifest, err := Parse("provider/manifest.yaml", document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manifest.ResolveProfile("backend")
	projected := assertSourceError(t, err, "source_bundle_requirement_invalid", "requirement_conflict", "/profiles/0/requiresBundles/0")
	if projected.Source() != "provider/manifest.yaml" || projected.Line() != 14 || projected.Column() != 9 {
		t.Fatalf("conflict diagnostics = %q %d:%d", projected.Source(), projected.Line(), projected.Column())
	}
}

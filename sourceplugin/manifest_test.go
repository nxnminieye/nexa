package sourceplugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/nxnminieye/nexa/provenance"
)

func validManifestSpec() ManifestSpec {
	return ManifestSpec{
		Identity: IdentitySpec{
			ProviderID:  "sample.foundation",
			ModulePath:  "example.com/sample/foundation",
			PackagePath: "example.com/sample/foundation/source",
			Version:     "v0.1.0",
		},
		Files: []FileSpec{
			{Path: "backend/main.go", Size: 12, Digest: provenance.SHA256([]byte("backend")), Mode: Mode0644},
			{Path: "go.mod", Size: 8, Digest: provenance.SHA256([]byte("module")), Mode: Mode0644},
		},
		Profiles: []ProfileSpec{
			{
				ID:               "backend",
				Files:            []string{"backend/main.go"},
				RequiresProfiles: []string{"base"},
				RequiresBundles:  []BundleRequirementSpec{validRequirement()},
				Validations: []ValidationRecipeSpec{{
					ID: "backend-test", Kind: ValidationGoTest, WorkingDirectory: ".", Packages: []string{"./backend/..."},
				}},
			},
			{ID: "base", Files: []string{"go.mod"}},
		},
	}
}

func validRequirement() BundleRequirementSpec {
	return BundleRequirementSpec{
		ProviderID:     "sample.common",
		ModulePath:     "example.com/sample/common",
		PackagePath:    "example.com/sample/common/source",
		Version:        "v0.2.0",
		ProfileID:      "base",
		ManifestDigest: provenance.SHA256([]byte("manifest")),
		TreeDigest:     provenance.SHA256([]byte("tree")),
	}
}

func TestManifestCanonicalRoundTripAndImmutableAccessors(t *testing.T) {
	spec := validManifestSpec()
	manifest, err := NewManifest(spec)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) == 0 || encoded[len(encoded)-1] != '\n' || bytes.Count(encoded[len(encoded)-1:], []byte("\n")) != 1 {
		t.Fatalf("canonical JSON does not end with exactly one newline: %q", encoded)
	}
	parsed, err := Parse("provider/source-bundle.json", encoded)
	if err != nil {
		t.Fatal(err)
	}
	parsedJSON, err := parsed.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, parsedJSON) || parsed.Digest() != manifest.Digest() {
		t.Fatalf("round trip changed manifest\nfirst: %s\nsecond: %s", encoded, parsedJSON)
	}
	if want := provenance.SHA256(encoded[:len(encoded)-1]); manifest.Digest() != want {
		t.Fatalf("manifest digest = %s, want hash excluding newline %s", manifest.Digest().String(), want.String())
	}

	reversed := validManifestSpec()
	reversed.Files[0], reversed.Files[1] = reversed.Files[1], reversed.Files[0]
	reversed.Profiles[0], reversed.Profiles[1] = reversed.Profiles[1], reversed.Profiles[0]
	reverseManifest, err := NewManifest(reversed)
	if err != nil {
		t.Fatal(err)
	}
	reverseJSON, _ := reverseManifest.CanonicalJSON()
	if !bytes.Equal(encoded, reverseJSON) || reverseManifest.Digest() != manifest.Digest() {
		t.Fatalf("input order changed canonical manifest\nfirst: %s\nreverse: %s", encoded, reverseJSON)
	}

	spec.Files[0].Path = "mutated"
	spec.Profiles[0].Files[0] = "mutated"
	spec.Profiles[0].Validations[0].Packages[0] = "mutated"
	files := manifest.Files()
	profiles := manifest.Profiles()
	packages := profiles[0].Validations()[0].Packages()
	files[0] = File{}
	profiles[0] = Profile{}
	packages[0] = "mutated"
	if got, _ := manifest.LookupFile("backend/main.go"); got.Path() != "backend/main.go" {
		t.Fatalf("manifest changed through caller mutation: %q", got.Path())
	}
	backend, ok := manifest.LookupProfile("backend")
	if !ok || !reflect.DeepEqual(backend.FilePaths(), []string{"backend/main.go"}) || !reflect.DeepEqual(backend.Validations()[0].Packages(), []string{"./backend/..."}) {
		t.Fatalf("profile changed through caller mutation: %#v", backend)
	}
	copyBytes := Schema()
	copyBytes[0] = 'x'
	if Schema()[0] == 'x' {
		t.Fatal("Schema returned mutable shared bytes")
	}
}

func TestManifestCanonicalDocumentMatchesPublicSchema(t *testing.T) {
	manifest, err := NewManifest(validManifestSpec())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := manifest.CanonicalJSON()
	var schemaDocument, manifestDocument any
	if err := json.Unmarshal(Schema(), &schemaDocument); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &manifestDocument); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("https://nexa.dev/schemas/source-bundle-v1.schema.json", schemaDocument); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("https://nexa.dev/schemas/source-bundle-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(manifestDocument); err != nil {
		t.Fatal(err)
	}
}

func TestManifestRejectsInvalidIdentityFilesAndPaths(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ManifestSpec)
		code    string
		reason  string
		pointer string
	}{
		{name: "provider", mutate: func(s *ManifestSpec) { s.Identity.ProviderID = "Bad" }, code: "source_manifest_invalid", reason: "provider_id_invalid", pointer: "/identity/providerId"},
		{name: "module", mutate: func(s *ManifestSpec) { s.Identity.ModulePath = "invalid" }, code: "source_manifest_invalid", reason: "module_path_invalid", pointer: "/identity/modulePath"},
		{name: "package path", mutate: func(s *ManifestSpec) { s.Identity.PackagePath = "bad path" }, code: "source_manifest_invalid", reason: "package_path_invalid", pointer: "/identity/packagePath"},
		{name: "package", mutate: func(s *ManifestSpec) { s.Identity.PackagePath = "example.com/other/source" }, code: "source_manifest_invalid", reason: "package_module_mismatch", pointer: "/identity/packagePath"},
		{name: "version", mutate: func(s *ManifestSpec) { s.Identity.Version = "latest" }, code: "source_manifest_invalid", reason: "version_invalid", pointer: "/identity/version"},
		{name: "module major", mutate: func(s *ManifestSpec) {
			s.Identity.ModulePath = "example.com/sample/foundation/v2"
			s.Identity.PackagePath = "example.com/sample/foundation/v2/source"
		}, code: "source_manifest_invalid", reason: "module_version_mismatch", pointer: "/identity/version"},
		{name: "size", mutate: func(s *ManifestSpec) { s.Files[0].Size = -1 }, code: "source_file_invalid", reason: "file_size_invalid", pointer: "/files/0/size"},
		{name: "digest", mutate: func(s *ManifestSpec) { s.Files[0].Digest = provenance.Digest{} }, code: "source_file_invalid", reason: "file_digest_invalid", pointer: "/files/0/digest"},
		{name: "mode", mutate: func(s *ManifestSpec) { s.Files[0].Mode = "0777" }, code: "source_file_invalid", reason: "file_mode_invalid", pointer: "/files/0/mode"},
		{name: "reserved path", mutate: func(s *ManifestSpec) { s.Files[0].Path = ".git/config" }, code: "source_path_invalid", reason: "path_reserved", pointer: "/files/0/path"},
		{name: "invalid path", mutate: func(s *ManifestSpec) { s.Files[0].Path = "/absolute.go" }, code: "source_path_invalid", reason: "path_invalid", pointer: "/files/0/path"},
		{name: "non NFC path", mutate: func(s *ManifestSpec) { s.Files[0].Path = "backend/e\u0301.go" }, code: "source_path_invalid", reason: "path_not_nfc", pointer: "/files/0/path"},
		{name: "duplicate file", mutate: func(s *ManifestSpec) { s.Files[0] = s.Files[1] }, code: "source_file_invalid", reason: "file_duplicate", pointer: "/files/1/path"},
		{name: "case collision", mutate: func(s *ManifestSpec) { s.Files[0].Path = "GO.MOD" }, code: "source_path_invalid", reason: "path_collision", pointer: "/files/1/path"},
		{name: "prefix collision", mutate: func(s *ManifestSpec) { s.Files[0].Path = "go.mod/child" }, code: "source_path_invalid", reason: "path_prefix_collision", pointer: "/files/1/path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validManifestSpec()
			tt.mutate(&spec)
			_, err := NewManifest(spec)
			assertSourceError(t, err, tt.code, tt.reason, tt.pointer)
		})
	}
}

func TestManifestRequiredAndOptionalCollectionPresence(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ManifestSpec)
		pointer string
	}{
		{name: "manifest files", mutate: func(s *ManifestSpec) { s.Files = nil }, pointer: "/files"},
		{name: "manifest profiles", mutate: func(s *ManifestSpec) { s.Profiles = nil }, pointer: "/profiles"},
		{name: "profile files", mutate: func(s *ManifestSpec) { s.Profiles[0].Files = nil }, pointer: "/profiles/0/files"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validManifestSpec()
			tt.mutate(&spec)
			_, err := NewManifest(spec)
			assertSourceError(t, err, "source_manifest_invalid", "document_invalid", tt.pointer)
		})
	}

	spec := validManifestSpec()
	spec.Profiles[1].RequiresProfiles = nil
	spec.Profiles[1].RequiresBundles = nil
	spec.Profiles[1].Validations = nil
	manifest, err := NewManifest(spec)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := manifest.CanonicalJSON()
	var document struct {
		Profiles []struct {
			ID               string `json:"id"`
			RequiresProfiles []any  `json:"requiresProfiles"`
			RequiresBundles  []any  `json:"requiresBundles"`
			Validations      []any  `json:"validations"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	for _, profile := range document.Profiles {
		if profile.ID == "base" && (profile.RequiresProfiles == nil || profile.RequiresBundles == nil || profile.Validations == nil) {
			t.Fatalf("optional lists did not canonicalize to []: %s", encoded)
		}
	}
}

func TestManifestPortablePathsCaseFoldControlRootsAndPrefixes(t *testing.T) {
	for _, value := range []string{".GIT/config", ".Git/hooks/pre-commit", ".NEXA/SOURCE/state", ".Nexa/Source/cache"} {
		t.Run("reserved/"+value, func(t *testing.T) {
			spec := validManifestSpec()
			spec.Files[0].Path = value
			_, err := NewManifest(spec)
			assertSourceError(t, err, "source_path_invalid", "path_reserved", "/files/0/path")
		})
	}

	for _, reverse := range []bool{false, true} {
		t.Run("folded-prefix/reverse="+strconv.FormatBool(reverse), func(t *testing.T) {
			spec := validManifestSpec()
			spec.Files = []FileSpec{
				{Path: "Foo", Size: 1, Digest: provenance.SHA256([]byte("foo")), Mode: Mode0644},
				{Path: "foo/bar", Size: 1, Digest: provenance.SHA256([]byte("bar")), Mode: Mode0644},
			}
			if reverse {
				spec.Files[0], spec.Files[1] = spec.Files[1], spec.Files[0]
			}
			_, err := NewManifest(spec)
			assertSourceError(t, err, "source_path_invalid", "path_prefix_collision", "/files/1/path")
		})
	}
}

func TestManifestProfileAndClosureAllInputsAndOutputsAreImmutable(t *testing.T) {
	spec := validManifestSpec()
	manifest, err := NewManifest(spec)
	if err != nil {
		t.Fatal(err)
	}
	closure, err := manifest.ResolveProfile("backend")
	if err != nil {
		t.Fatal(err)
	}
	wantCanonical, _ := manifest.CanonicalJSON()
	wantDigest := manifest.Digest()

	spec.Identity.ProviderID = "mutated"
	spec.Files[0] = FileSpec{}
	spec.Profiles[0].ID = "mutated"
	spec.Profiles[0].Files[0] = "mutated"
	spec.Profiles[0].RequiresProfiles[0] = "mutated"
	spec.Profiles[0].RequiresBundles[0] = BundleRequirementSpec{}
	spec.Profiles[0].Validations[0].ID = "mutated"
	spec.Profiles[0].Validations[0].Packages[0] = "mutated"

	canonical := manifestCanonicalMust(t, manifest)
	canonical[0] ^= 0xff
	schema := Schema()
	schema[0] ^= 0xff
	files := manifest.Files()
	files[0] = File{}
	profiles := manifest.Profiles()
	profileFiles := profiles[0].FilePaths()
	profileFiles[0] = "mutated"
	profileDependencies := profiles[0].RequiredProfileIDs()
	profileDependencies[0] = "mutated"
	profileRequirements := profiles[0].BundleRequirements()
	profileRequirements[0] = BundleRequirement{}
	profileValidations := profiles[0].Validations()
	profilePackages := profileValidations[0].Packages()
	profilePackages[0] = "mutated"
	profileValidations[0] = ValidationRecipe{}
	profiles[0] = Profile{}
	lookup, ok := manifest.LookupProfile("backend")
	if !ok {
		t.Fatal("backend profile disappeared")
	}
	lookupFiles := lookup.FilePaths()
	lookupFiles[0] = "mutated"
	lookupValidations := lookup.Validations()
	lookupValidations[0] = ValidationRecipe{}

	closureIDs := closure.ProfileIDs()
	closureIDs[0] = "mutated"
	closureFiles := closure.Files()
	closureFiles[0] = File{}
	closureRequirements := closure.BundleRequirements()
	closureRequirements[0] = BundleRequirement{}
	closureValidations := closure.Validations()
	closurePackages := closureValidations[0].Packages()
	closurePackages[0] = "mutated"
	closureValidations[0] = ValidationRecipe{}

	if got := manifestCanonicalMust(t, manifest); !bytes.Equal(got, wantCanonical) || manifest.Digest() != wantDigest {
		t.Fatalf("manifest changed through caller mutation\nwant=%s\ngot=%s", wantCanonical, got)
	}
	identity := manifest.Identity()
	backend, ok := manifest.LookupProfile("backend")
	if identity.ProviderID() != "sample.foundation" || !ok || !reflect.DeepEqual(backend.FilePaths(), []string{"backend/main.go"}) ||
		!reflect.DeepEqual(backend.RequiredProfileIDs(), []string{"base"}) || backend.BundleRequirements()[0].ProviderID() != "sample.common" ||
		backend.Validations()[0].ID() != "backend-test" || !reflect.DeepEqual(backend.Validations()[0].Packages(), []string{"./backend/..."}) {
		t.Fatalf("typed manifest values changed: identity=%#v backend=%#v", identity, backend)
	}
	if !reflect.DeepEqual(closure.ProfileIDs(), []string{"base", "backend"}) || closure.Files()[0].Path() != "backend/main.go" ||
		closure.BundleRequirements()[0].ProviderID() != "sample.common" || closure.Validations()[0].ID() != "backend-test" ||
		!reflect.DeepEqual(closure.Validations()[0].Packages(), []string{"./backend/..."}) {
		t.Fatalf("closure changed through caller mutation: %#v", closure)
	}
	if !json.Valid(Schema()) || Schema()[0] == schema[0] {
		t.Fatal("schema accessor did not remain immutable")
	}
}

func TestManifestNestedInputOrderDoesNotAffectCanonicalOrClosureOrder(t *testing.T) {
	spec := validManifestSpec()
	spec.Files = append(spec.Files, FileSpec{Path: "backend/extra.go", Size: 5, Digest: provenance.SHA256([]byte("extra")), Mode: Mode0755})
	otherRequirement := validRequirement()
	otherRequirement.ProviderID = "sample.other"
	otherRequirement.ModulePath = "example.com/sample/other"
	otherRequirement.PackagePath = "example.com/sample/other/source"
	otherRequirement.Version = "v0.3.0"
	otherRequirement.ManifestDigest = provenance.SHA256([]byte("other-manifest"))
	otherRequirement.TreeDigest = provenance.SHA256([]byte("other-tree"))
	backend := &spec.Profiles[0]
	backend.Files = []string{"backend/main.go", "backend/extra.go"}
	backend.RequiresProfiles = []string{"mid", "base"}
	backend.RequiresBundles = []BundleRequirementSpec{otherRequirement, validRequirement()}
	backend.Validations = []ValidationRecipeSpec{
		{ID: "backend-test", Kind: ValidationGoTest, WorkingDirectory: ".", Packages: []string{"./backend/...", "./backend"}},
		{ID: "backend-build", Kind: ValidationGoBuild, WorkingDirectory: ".", Packages: []string{"."}},
	}
	spec.Profiles = append(spec.Profiles, ProfileSpec{ID: "mid", Files: []string{}})

	canonicalManifest, err := NewManifest(spec)
	if err != nil {
		t.Fatal(err)
	}
	reversedManifest, err := NewManifest(reverseManifestSpec(spec))
	if err != nil {
		t.Fatal(err)
	}
	canonicalJSON := manifestCanonicalMust(t, canonicalManifest)
	reversedJSON := manifestCanonicalMust(t, reversedManifest)
	if !bytes.Equal(canonicalJSON, reversedJSON) || canonicalManifest.Digest() != reversedManifest.Digest() {
		t.Fatalf("nested input order changed canonical manifest\ncanonical=%s\nreversed=%s", canonicalJSON, reversedJSON)
	}
	backendProfile, ok := reversedManifest.LookupProfile("backend")
	if !ok || !reflect.DeepEqual(backendProfile.FilePaths(), []string{"backend/extra.go", "backend/main.go"}) ||
		!reflect.DeepEqual(backendProfile.RequiredProfileIDs(), []string{"base", "mid"}) ||
		backendProfile.BundleRequirements()[0].ProviderID() != "sample.common" || backendProfile.BundleRequirements()[1].ProviderID() != "sample.other" ||
		backendProfile.Validations()[0].ID() != "backend-build" || backendProfile.Validations()[1].ID() != "backend-test" ||
		!reflect.DeepEqual(backendProfile.Validations()[1].Packages(), []string{"./backend", "./backend/..."}) {
		t.Fatalf("canonical profile accessors = %#v", backendProfile)
	}
	closure, err := reversedManifest.ResolveProfile("backend")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(closure.ProfileIDs(), []string{"base", "mid", "backend"}) || len(closure.Files()) != 3 ||
		closure.Files()[0].Path() != "backend/extra.go" || closure.Files()[1].Path() != "backend/main.go" || closure.Files()[2].Path() != "go.mod" ||
		closure.BundleRequirements()[0].ProviderID() != "sample.common" || closure.BundleRequirements()[1].ProviderID() != "sample.other" ||
		closure.Validations()[0].ID() != "backend-build" || closure.Validations()[1].ID() != "backend-test" {
		t.Fatalf("canonical closure = %#v", closure)
	}
}

func reverseManifestSpec(spec ManifestSpec) ManifestSpec {
	result := ManifestSpec{Identity: spec.Identity, Files: append([]FileSpec(nil), spec.Files...), Profiles: make([]ProfileSpec, len(spec.Profiles))}
	for index, profile := range spec.Profiles {
		result.Profiles[index] = ProfileSpec{
			ID: profile.ID, Files: append([]string{}, profile.Files...), RequiresProfiles: append([]string(nil), profile.RequiresProfiles...),
			RequiresBundles: append([]BundleRequirementSpec(nil), profile.RequiresBundles...), Validations: make([]ValidationRecipeSpec, len(profile.Validations)),
		}
		for validationIndex, validation := range profile.Validations {
			result.Profiles[index].Validations[validationIndex] = ValidationRecipeSpec{
				ID: validation.ID, Kind: validation.Kind, WorkingDirectory: validation.WorkingDirectory, Packages: append([]string(nil), validation.Packages...),
			}
			slices.Reverse(result.Profiles[index].Validations[validationIndex].Packages)
		}
		slices.Reverse(result.Profiles[index].Files)
		slices.Reverse(result.Profiles[index].RequiresProfiles)
		slices.Reverse(result.Profiles[index].RequiresBundles)
		slices.Reverse(result.Profiles[index].Validations)
	}
	slices.Reverse(result.Files)
	slices.Reverse(result.Profiles)
	return result
}

func manifestCanonicalMust(t *testing.T, manifest Manifest) []byte {
	t.Helper()
	canonical, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func assertSourceError(t *testing.T, err error, code, reason, pointer string) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("operation succeeded, want %s/%s", code, reason)
	}
	projected, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *sourceplugin.Error", err)
	}
	if projected.Code() != code || projected.Reason() != reason || projected.Pointer() != pointer {
		t.Fatalf("error = code=%q reason=%q pointer=%q, want %q/%q at %q", projected.Code(), projected.Reason(), projected.Pointer(), code, reason, pointer)
	}
	wantClass := ErrManifestInvalid
	if code == "source_profile_not_found" {
		wantClass = ErrProfileNotFound
	} else if code == "source_profile_cycle" {
		wantClass = ErrProfileCycle
	}
	if projected.Class() != wantClass || projected.Error() != wantClass.Error() || !errors.Is(projected, wantClass) {
		t.Fatalf("error class = %v message=%q, want %v/%q", projected.Class(), projected.Error(), wantClass, wantClass.Error())
	}
	if wantClass != ErrManifestInvalid && wantClass != ErrProfileCycle && errors.Is(projected, ErrManifestInvalid) {
		t.Fatalf("%v unexpectedly matches ErrManifestInvalid", projected)
	}
	if wantClass != ErrProfileCycle && errors.Is(projected, ErrProfileCycle) {
		t.Fatalf("%v unexpectedly matches ErrProfileCycle", projected)
	}
	return projected
}

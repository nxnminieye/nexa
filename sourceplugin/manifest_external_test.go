package sourceplugin_test

import (
	"testing"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
)

func TestExternalConsumerReadsCompleteTypedManifestContract(t *testing.T) {
	requirement := sourceplugin.BundleRequirementSpec{
		ProviderID: "sample.common", ModulePath: "example.com/sample/common", PackagePath: "example.com/sample/common/source", Version: "v0.2.0", ProfileID: "base",
		ManifestDigest: provenance.SHA256([]byte("manifest")), TreeDigest: provenance.SHA256([]byte("tree")),
	}
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: "sample.foundation", ModulePath: "example.com/sample/foundation", PackagePath: "example.com/sample/foundation/source", Version: "v0.1.0"},
		Files:    []sourceplugin.FileSpec{{Path: "main.go", Size: 4, Digest: provenance.SHA256([]byte("main")), Mode: sourceplugin.Mode0755}},
		Profiles: []sourceplugin.ProfileSpec{{
			ID: "base", Files: []string{"main.go"}, RequiresBundles: []sourceplugin.BundleRequirementSpec{requirement},
			Validations: []sourceplugin.ValidationRecipeSpec{{ID: "test", Kind: sourceplugin.ValidationGoTest, WorkingDirectory: ".", Packages: []string{"."}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := manifest.Identity()
	if manifest.APIVersion() != sourceplugin.APIVersion || manifest.Kind() != sourceplugin.Kind || identity.ProviderID() != "sample.foundation" || identity.ModulePath() == "" || identity.PackagePath() == "" || identity.Version() != "v0.1.0" {
		t.Fatalf("identity = %#v", identity)
	}
	file, ok := manifest.LookupFile("main.go")
	if !ok || file.Path() != "main.go" || file.Size() != 4 || file.Digest().String() == "" || file.Mode() != sourceplugin.Mode0755 {
		t.Fatalf("file = %#v", file)
	}
	profile, ok := manifest.LookupProfile("base")
	if !ok || profile.ID() != "base" || len(profile.FilePaths()) != 1 || len(profile.RequiredProfileIDs()) != 0 || len(profile.BundleRequirements()) != 1 || len(profile.Validations()) != 1 {
		t.Fatalf("profile = %#v", profile)
	}
	resolvedRequirement := profile.BundleRequirements()[0]
	if resolvedRequirement.ProviderID() != requirement.ProviderID || resolvedRequirement.ModulePath() != requirement.ModulePath || resolvedRequirement.PackagePath() != requirement.PackagePath || resolvedRequirement.Version() != requirement.Version || resolvedRequirement.ProfileID() != requirement.ProfileID || resolvedRequirement.ManifestDigest() != requirement.ManifestDigest || resolvedRequirement.TreeDigest() != requirement.TreeDigest {
		t.Fatalf("requirement = %#v", resolvedRequirement)
	}
	recipe := profile.Validations()[0]
	if recipe.ID() != "test" || recipe.Kind() != sourceplugin.ValidationGoTest || recipe.WorkingDirectory() != "." || len(recipe.Packages()) != 1 {
		t.Fatalf("recipe = %#v", recipe)
	}
	closure, err := manifest.ResolveProfile("base")
	if err != nil || closure.RootProfileID() != "base" || len(closure.ProfileIDs()) != 1 || len(closure.Files()) != 1 || len(closure.BundleRequirements()) != 1 || len(closure.Validations()) != 1 || manifest.Digest().String() == "" {
		t.Fatalf("closure = %#v, err=%v", closure, err)
	}
}

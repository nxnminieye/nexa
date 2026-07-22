package job_test

import (
	"testing"

	jobplugin "github.com/nxnminieye/nexa/plugins/service/job"
	"github.com/nxnminieye/nexa/sourceplugin"
)

func TestProviderPublishesOneImmutableBackendProfile(t *testing.T) {
	first, err := jobplugin.New()
	if err != nil {
		t.Fatal(err)
	}
	second, err := jobplugin.New()
	if err != nil {
		t.Fatal(err)
	}
	first, err = sourceplugin.SnapshotProvider(first)
	if err != nil {
		t.Fatal(err)
	}
	manifest := first.Manifest()
	identity := manifest.Identity()
	if identity.ProviderID() != "job-service" || identity.ModulePath() != "github.com/nxnminieye/nexa" || identity.PackagePath() != "github.com/nxnminieye/nexa/plugins/service/job" || identity.Version() != "v0.1.0" {
		t.Fatalf("identity = %#v", identity)
	}
	profiles := manifest.Profiles()
	if len(profiles) != 1 || profiles[0].ID() != "backend" {
		t.Fatalf("profiles = %#v", profiles)
	}
	closure, err := manifest.ResolveProfile("backend")
	if err != nil {
		t.Fatal(err)
	}
	if closure.RootProfileID() != "backend" || len(closure.ProfileIDs()) != 1 || len(closure.Files()) == 0 || len(closure.BundleRequirements()) != 0 {
		t.Fatalf("backend closure = %#v", closure)
	}
	validations := closure.Validations()
	if len(validations) != 1 || validations[0].Kind() != sourceplugin.ValidationGoTest || validations[0].WorkingDirectory() != "backend/job" {
		t.Fatalf("validations = %#v", validations)
	}
	if first.Manifest().Digest() != second.Manifest().Digest() || first.Tree().Digest() != second.Tree().Digest() {
		t.Fatal("New returned different provider snapshots")
	}
	files := first.Tree().Files()
	content := files[0].Bytes()
	content[0] ^= 0xff
	if first.Tree().Digest() != second.Tree().Digest() {
		t.Fatal("provider tree changed through returned bytes")
	}
}

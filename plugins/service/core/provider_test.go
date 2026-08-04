package core_test

import (
	"bytes"
	"reflect"
	"testing"

	core "github.com/nxnminieye/nexa/plugins/service/core"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func TestProviderPublishesImmutableCoreBundle(t *testing.T) {
	provider, err := core.New()
	if err != nil {
		t.Fatal(err)
	}
	identity := provider.Manifest().Identity()
	if identity.ProviderID() != "core-application" || identity.Version() != "v0.3.0-alpha.6" ||
		identity.ModulePath() != "github.com/nxnminieye/nexa" || identity.PackagePath() != "github.com/nxnminieye/nexa/plugins/service/core" {
		t.Fatalf("provider identity = %#v", identity)
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	if ref.ProviderID() != identity.ProviderID() || ref.ModulePath() != identity.ModulePath() || ref.PackagePath() != identity.PackagePath() || ref.Version() != "v0.3.0-alpha.6" {
		t.Fatalf("provider release ref = %s@%s", ref.ProviderID(), ref.Version())
	}
	profiles := provider.Manifest().Profiles()
	if got := profileIDs(profiles); !reflect.DeepEqual(got, []string{"backend"}) {
		t.Fatalf("profiles = %#v", got)
	}
	backend, err := provider.Manifest().ResolveProfile("backend")
	if err != nil {
		t.Fatal(err)
	}
	backendModules := backend.GoModuleRequirements()
	if len(backendModules) != 2 || backendModules[0].ModulePath() != "entgo.io/ent" || backendModules[0].Version() != "v0.14.5" || backendModules[1].ModulePath() != "golang.org/x/crypto" || backendModules[1].Version() != "v0.48.0" {
		t.Fatalf("backend Go module requirements = %#v", backendModules)
	}
	for _, removed := range []string{"frontend", "full"} {
		if _, ok := provider.Manifest().LookupProfile(removed); ok {
			t.Fatalf("consumer-owned %q profile remains public", removed)
		}
	}

	snapshot, err := sourceplugin.SnapshotProvider(provider)
	if err != nil || snapshot.Manifest().Digest() != provider.Manifest().Digest() || snapshot.Tree().Digest() != provider.Tree().Digest() {
		t.Fatalf("snapshot = %#v, %v", snapshot, err)
	}
	files := provider.Tree().Files()
	if len(files) == 0 {
		t.Fatal("provider tree is empty")
	}
	first := files[0]
	mutated := first.Bytes()
	mutated[0] ^= 0xff
	again, ok := provider.Tree().Lookup(first.Path())
	if !ok || bytes.Equal(mutated, again.Bytes()) || again.Digest() != first.Digest() || again.Mode() != first.Mode() {
		t.Fatal("provider tree bytes or file facts are mutable")
	}
}

func profileIDs(profiles []sourceplugin.Profile) []string {
	result := make([]string, len(profiles))
	for index, profile := range profiles {
		result[index] = profile.ID()
	}
	return result
}

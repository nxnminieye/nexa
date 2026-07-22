package release

import (
	"errors"
	"testing"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
)

func internalResolvedForCache(t *testing.T, content string) ResolvedRelease {
	return internalResolvedForCacheWithIdentity(t, "sample.internal", content)
}

func internalResolvedForCacheWithIdentity(t *testing.T, providerID, content string) ResolvedRelease {
	t.Helper()
	data := []byte(content)
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: providerID, ModulePath: "example.test/internal", PackagePath: "example.test/internal/source", Version: "v0.1.0"},
		Files:    []sourceplugin.FileSpec{{Path: "main.go", Size: int64(len(data)), Digest: provenance.SHA256(data), Mode: sourceplugin.Mode0644}},
		Profiles: []sourceplugin.ProfileSpec{{ID: "default", Files: []string{"main.go"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sourceplugin.NewTree(manifest, []sourceplugin.TreeInput{{Path: "main.go", Content: data}}, sourceplugin.DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	provider, err := sourceplugin.NewProvider(manifest, tree)
	if err != nil {
		t.Fatal(err)
	}
	resolved, projected := snapshotProvider(provider, "/provider")
	if projected != nil {
		t.Fatal(projected)
	}
	return resolved
}

func assertInternalReleaseError(t *testing.T, err error, class ErrorClass, code, reason, pointer string, stage Stage) *Error {
	t.Helper()
	var projected *Error
	if !errors.As(err, &projected) || projected.Class() != class || projected.Code() != code || projected.Reason() != reason || projected.Pointer() != pointer || projected.Stage() != stage || !errors.Is(projected, class) {
		t.Fatalf("error = %#v", err)
	}
	if errors.Unwrap(projected) != nil {
		t.Fatal("raw error escaped")
	}
	return projected
}

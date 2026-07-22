package release_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func TestRefAccessorsEqualityAndValidationOrder(t *testing.T) {
	provider, _ := releaseProvider(t, "sample", "a")
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	manifest, tree := provider.Manifest(), provider.Tree()
	identity := manifest.Identity()
	if ref.ProviderID() != identity.ProviderID() || ref.ModulePath() != identity.ModulePath() ||
		ref.PackagePath() != identity.PackagePath() || ref.Version() != identity.Version() ||
		ref.ManifestDigest() != manifest.Digest() || ref.TreeDigest() != tree.Digest() {
		t.Fatalf("ref = %#v", ref)
	}
	copyRef, err := release.NewRef(release.RefSpec{
		ProviderID: ref.ProviderID(), ModulePath: ref.ModulePath(), PackagePath: ref.PackagePath(), Version: ref.Version(),
		ManifestDigest: ref.ManifestDigest(), TreeDigest: ref.TreeDigest(),
	})
	if err != nil || !ref.Equal(copyRef) || !ref.SameCoordinates(copyRef) {
		t.Fatalf("copy ref = %#v, err=%v", copyRef, err)
	}
	if (release.Ref{}).Equal(release.Ref{}) || (release.Ref{}).SameCoordinates(copyRef) || copyRef.SameCoordinates(release.Ref{}) {
		t.Fatal("zero ref participated in equality")
	}

	valid := release.RefSpec{
		ProviderID: "sample", ModulePath: "example.test/sample", PackagePath: "example.test/sample/source", Version: "v0.1.0",
		ManifestDigest: provenance.SHA256([]byte("manifest")), TreeDigest: provenance.SHA256([]byte("tree")),
	}
	tests := []struct {
		name    string
		mutate  func(*release.RefSpec)
		reason  string
		pointer string
	}{
		{"provider", func(s *release.RefSpec) { s.ProviderID = "Bad"; s.ManifestDigest = provenance.Digest{} }, "provider_id_invalid", "/ref/providerId"},
		{"module", func(s *release.RefSpec) { s.ModulePath = "bad path"; s.ManifestDigest = provenance.Digest{} }, "module_path_invalid", "/ref/modulePath"},
		{"package", func(s *release.RefSpec) { s.PackagePath = "bad path"; s.ManifestDigest = provenance.Digest{} }, "package_path_invalid", "/ref/packagePath"},
		{"relation", func(s *release.RefSpec) { s.PackagePath = "example.test/other"; s.ManifestDigest = provenance.Digest{} }, "package_module_mismatch", "/ref/packagePath"},
		{"version", func(s *release.RefSpec) { s.Version = "latest"; s.ManifestDigest = provenance.Digest{} }, "version_invalid", "/ref/version"},
		{"manifest digest", func(s *release.RefSpec) { s.ManifestDigest = provenance.Digest{}; s.TreeDigest = provenance.Digest{} }, "manifest_digest_invalid", "/ref/manifestDigest"},
		{"tree digest", func(s *release.RefSpec) { s.TreeDigest = provenance.Digest{} }, "tree_digest_invalid", "/ref/treeDigest"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := valid
			test.mutate(&spec)
			_, err := release.NewRef(spec)
			assertReleaseError(t, err, release.ErrReleaseInput, "source_release_invalid", test.reason, test.pointer, release.StageRef)
		})
	}
}

func TestFromProviderSnapshotsOriginalExactlyOnce(t *testing.T) {
	first, _ := releaseProvider(t, "sample", "a")
	second, _ := releaseProvider(t, "sample", "b")
	source := &countingProvider{manifests: []sourceplugin.Manifest{first.Manifest(), second.Manifest()}, trees: []sourceplugin.Tree{first.Tree(), second.Tree()}}
	ref, err := release.FromProvider(source)
	if err != nil {
		t.Fatal(err)
	}
	if source.manifestCalls != 1 || source.treeCalls != 1 {
		t.Fatalf("provider calls = manifest:%d tree:%d", source.manifestCalls, source.treeCalls)
	}
	for range 3 {
		if ref.ManifestDigest() != first.Manifest().Digest() || ref.TreeDigest() != first.Tree().Digest() {
			t.Fatal("ref changed after capture")
		}
	}
	if source.manifestCalls != 1 || source.treeCalls != 1 {
		t.Fatal("ref delegated after capture")
	}
}

func TestReleaseZeroPublicBoundaryMatrix(t *testing.T) {
	_, ref := releaseProvider(t, "sample.boundary", "value")
	for _, test := range []struct {
		name string
		call func() error
	}{
		{"nil provider", func() error { _, err := release.FromProvider(nil); return err }},
		{"typed nil provider", func() error {
			var provider *countingProvider
			_, err := release.FromProvider(provider)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertReleaseError(t, test.call(), release.ErrReleaseInput, "source_release_invalid", "provider_nil", "/provider", release.StageProviderSnapshot)
		})
	}
	for _, test := range []struct {
		name     string
		resolver *release.ExactResolver
	}{
		{"nil resolver", nil},
		{"zero resolver", &release.ExactResolver{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.resolver.Resolve(context.Background(), ref)
			assertReleaseError(t, err, release.ErrReleaseInput, "source_release_invalid", "resolver_required", "/resolver", release.StageResolverStatic)
		})
	}
	_, err := release.NewExactResolver(&release.DirectoryCache{})
	assertReleaseError(t, err, release.ErrReleaseInput, "source_release_invalid", "cache_root_invalid", "/cache", release.StageResolverStatic)

	parent := filepath.Join(t.TempDir(), "cache")
	invalidRoots := []string{"", "relative/cache", parent + string(os.PathSeparator), parent + "/../cache", parent + "\x00", parent + "\n", string(os.PathSeparator)}
	for index, root := range invalidRoots {
		t.Run(fmt.Sprintf("invalid root %d", index), func(t *testing.T) {
			_, err := release.OpenDirectoryCache(root, release.DefaultCacheLimits())
			assertReleaseError(t, err, release.ErrReleaseInput, "source_release_invalid", "cache_root_invalid", "/cache/root", release.StageCacheOpen)
		})
	}
}

func TestExactResolverStaticMatrixAndDefensiveResolvedRelease(t *testing.T) {
	first, _ := releaseProvider(t, "sample.alpha", "alpha")
	second, _ := releaseProvider(t, "sample.beta", "beta")
	countedFirst := &countingProvider{manifests: []sourceplugin.Manifest{first.Manifest()}, trees: []sourceplugin.Tree{first.Tree()}}
	countedSecond := &countingProvider{manifests: []sourceplugin.Manifest{second.Manifest()}, trees: []sourceplugin.Tree{second.Tree()}}
	resolver, err := release.NewExactResolver(nil, countedSecond, countedFirst)
	if err != nil {
		t.Fatal(err)
	}
	firstRef, _ := release.FromProvider(first)
	resolved, err := resolver.Resolve(context.Background(), firstRef)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Ref().Equal(firstRef) || resolved.Manifest().Digest() != firstRef.ManifestDigest() || resolved.Tree().Digest() != firstRef.TreeDigest() {
		t.Fatalf("resolved = %#v", resolved)
	}
	if _, ok := resolver.Cache(); ok {
		t.Fatal("nil cache reported as enabled")
	}
	for range 8 {
		files := resolved.Tree().Files()
		files[0] = sourceplugin.TreeFile{}
		provider := resolved.Provider()
		if provider.Manifest().Digest() != firstRef.ManifestDigest() || provider.Tree().Digest() != firstRef.TreeDigest() {
			t.Fatal("resolved provider changed")
		}
	}
	if countedFirst.manifestCalls != 1 || countedFirst.treeCalls != 1 || countedSecond.manifestCalls != 1 || countedSecond.treeCalls != 1 {
		t.Fatalf("original provider calls changed: first=%d/%d second=%d/%d", countedFirst.manifestCalls, countedFirst.treeCalls, countedSecond.manifestCalls, countedSecond.treeCalls)
	}

	missing, _ := release.NewRef(release.RefSpec{
		ProviderID: "missing", ModulePath: "example.test/missing", PackagePath: "example.test/missing/source", Version: "v0.1.0",
		ManifestDigest: provenance.SHA256([]byte("missing-manifest")), TreeDigest: provenance.SHA256([]byte("missing-tree")),
	})
	_, err = resolver.Resolve(context.Background(), missing)
	assertReleaseError(t, err, release.ErrReleaseUnavailable, "source_release_unavailable", "release_not_found", "/release", release.StageResolverStatic)
}

func TestExactResolverConstructorIsCallerOrderIndependent(t *testing.T) {
	left, _ := releaseProvider(t, "sample", "left")
	right, _ := releaseProvider(t, "sample", "right")
	for _, providers := range [][]sourceplugin.Provider{{left, right}, {right, left}} {
		_, err := release.NewExactResolver(nil, providers...)
		assertReleaseError(t, err, release.ErrReleaseConflict, "source_release_conflict", "immutable_release_conflict", "/release", release.StageResolverStatic)
	}
	for _, providers := range [][]sourceplugin.Provider{{left, left}, {left, left}} {
		_, err := release.NewExactResolver(nil, providers...)
		assertReleaseError(t, err, release.ErrReleaseConflict, "source_release_conflict", "duplicate_release", "/release", release.StageResolverStatic)
	}

	var typedNil *countingProvider
	_, err := release.NewExactResolver(nil, left, typedNil)
	assertReleaseError(t, err, release.ErrReleaseInput, "source_release_invalid", "provider_nil", "/providers", release.StageProviderSnapshot)
	panicManifest := &countingProvider{panicManifest: true}
	_, err = release.NewExactResolver(nil, panicManifest)
	assertReleaseError(t, err, release.ErrReleaseInternal, "source_release_internal", "provider_manifest_panic", "/providers", release.StageProviderSnapshot)
	panicTree := &countingProvider{panicTree: true}
	_, err = release.NewExactResolver(nil, panicTree)
	assertReleaseError(t, err, release.ErrReleaseInternal, "source_release_internal", "provider_tree_panic", "/providers", release.StageProviderSnapshot)
	invalidRelation := &countingProvider{manifests: []sourceplugin.Manifest{left.Manifest()}, trees: []sourceplugin.Tree{right.Tree()}}
	_, err = release.NewExactResolver(nil, invalidRelation)
	assertReleaseError(t, err, release.ErrReleaseInput, "source_release_invalid", "provider_invalid", "/providers", release.StageProviderSnapshot)
}

func TestExactResolverConstructorResourceGrowthIsBounded(t *testing.T) {
	measure := func(count int) uint64 {
		t.Helper()
		providers := resolverScaleProviders(t, count)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		resolver, err := release.NewExactResolver(nil, providers...)
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatalf("NewExactResolver(%d): %v", count, err)
		}
		runtime.KeepAlive(resolver)
		return after.TotalAlloc - before.TotalAlloc
	}

	small := measure(512)
	large := measure(1024)
	const allocationSlack = 2 << 20
	if large > small*3+allocationSlack {
		t.Fatalf("2x providers allocated %d bytes after %d bytes; growth exceeds bounded constructor contract", large, small)
	}
}

func TestExactResolverContextOrderAndContainment(t *testing.T) {
	provider, _ := releaseProvider(t, "sample", "value")
	ref, _ := release.FromProvider(provider)
	resolver, err := release.NewExactResolver(nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	var typedNil *panicContext
	_, err = resolver.Resolve(typedNil, ref)
	assertReleaseError(t, err, release.ErrReleaseInput, "source_release_invalid", "context_required", "/context", release.StageResolverStatic)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = resolver.Resolve(canceled, ref)
	projected := assertReleaseError(t, err, release.ErrReleaseCanceled, "source_release_canceled", "context_canceled", "/context", release.StageResolverStatic)
	if !errors.Is(projected, context.Canceled) || errors.Is(projected, context.DeadlineExceeded) {
		t.Fatalf("canceled matching = %v", projected)
	}

	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	_, err = resolver.Resolve(deadline, ref)
	projected = assertReleaseError(t, err, release.ErrReleaseCanceled, "source_release_canceled", "deadline_exceeded", "/context", release.StageResolverStatic)
	if !errors.Is(projected, context.DeadlineExceeded) || errors.Is(projected, context.Canceled) {
		t.Fatalf("deadline matching = %v", projected)
	}

	_, err = resolver.Resolve(panicContext{}, ref)
	assertReleaseError(t, err, release.ErrReleaseInternal, "source_release_internal", "context_panic", "/context", release.StageResolverStatic)
}

func TestExactResolverSupportsConcurrentStaticReads(t *testing.T) {
	provider, _ := releaseProvider(t, "sample", "value")
	ref, _ := release.FromProvider(provider)
	resolver, err := release.NewExactResolver(nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 32)
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 100 {
				resolved, resolveErr := resolver.Resolve(context.Background(), ref)
				if resolveErr != nil || !resolved.Ref().Equal(ref) {
					errorsSeen <- resolveErr
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatalf("concurrent resolve: %v", err)
	}
}

func TestExternalConsumerResolvesBundleRequirementWithoutCatalog(t *testing.T) {
	dependency, dependencyRef := releaseProvider(t, "sample.dependency", "dependency")
	dependencyIdentity := dependency.Manifest().Identity()
	primaryData := []byte("primary")
	primaryManifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: "sample.primary", ModulePath: "example.test/primary", PackagePath: "example.test/primary/source", Version: "v0.1.0"},
		Files:    []sourceplugin.FileSpec{{Path: "main.go", Size: int64(len(primaryData)), Digest: provenance.SHA256(primaryData), Mode: sourceplugin.Mode0644}},
		Profiles: []sourceplugin.ProfileSpec{{
			ID: "default", Files: []string{"main.go"},
			RequiresBundles: []sourceplugin.BundleRequirementSpec{{
				ProviderID: dependencyIdentity.ProviderID(), ModulePath: dependencyIdentity.ModulePath(), PackagePath: dependencyIdentity.PackagePath(), Version: dependencyIdentity.Version(), ProfileID: "default",
				ManifestDigest: dependencyRef.ManifestDigest(), TreeDigest: dependencyRef.TreeDigest(),
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	primaryTree, err := sourceplugin.NewTree(primaryManifest, []sourceplugin.TreeInput{{Path: "main.go", Content: primaryData}}, sourceplugin.DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	primary, err := sourceplugin.NewProvider(primaryManifest, primaryTree)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := release.NewExactResolver(nil, primary, dependency)
	if err != nil {
		t.Fatal(err)
	}
	closure, err := primaryManifest.ResolveProfile("default")
	if err != nil || len(closure.BundleRequirements()) != 1 {
		t.Fatalf("closure = %#v, err=%v", closure, err)
	}
	requirement := closure.BundleRequirements()[0]
	requiredRef, err := release.NewRef(release.RefSpec{
		ProviderID: requirement.ProviderID(), ModulePath: requirement.ModulePath(), PackagePath: requirement.PackagePath(), Version: requirement.Version(),
		ManifestDigest: requirement.ManifestDigest(), TreeDigest: requirement.TreeDigest(),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), requiredRef)
	if err != nil || !resolved.Ref().Equal(dependencyRef) {
		t.Fatalf("required release = %#v, err=%v", resolved, err)
	}
	if _, err := resolved.Manifest().ResolveProfile(requirement.ProfileID()); err != nil {
		t.Fatal(err)
	}
	wrong, err := release.NewRef(release.RefSpec{
		ProviderID: requirement.ProviderID(), ModulePath: requirement.ModulePath(), PackagePath: requirement.PackagePath(), Version: requirement.Version(),
		ManifestDigest: requirement.ManifestDigest(), TreeDigest: provenance.SHA256([]byte("wrong")),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(context.Background(), wrong)
	assertReleaseError(t, err, release.ErrReleaseConflict, "source_release_conflict", "immutable_release_conflict", "/release", release.StageResolverStatic)
}

type countingProvider struct {
	manifests     []sourceplugin.Manifest
	trees         []sourceplugin.Tree
	manifestCalls int
	treeCalls     int
	panicManifest bool
	panicTree     bool
}

func (p *countingProvider) Manifest() sourceplugin.Manifest {
	p.manifestCalls++
	if p.panicManifest {
		panic("secret")
	}
	if len(p.manifests) == 0 {
		return sourceplugin.Manifest{}
	}
	return p.manifests[0]
}

func (p *countingProvider) Tree() sourceplugin.Tree {
	p.treeCalls++
	if p.panicTree {
		panic("secret")
	}
	if len(p.trees) == 0 {
		return sourceplugin.Tree{}
	}
	return p.trees[0]
}

type panicContext struct{}

func (panicContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (panicContext) Done() <-chan struct{}       { return nil }
func (panicContext) Err() error                  { panic("secret") }
func (panicContext) Value(any) any               { return nil }

func releaseProvider(t *testing.T, providerID, content string) (sourceplugin.Provider, release.Ref) {
	return releaseProviderAtPath(t, providerID, "main.go", content)
}

func releaseProviderAtPath(t *testing.T, providerID, filePath, content string) (sourceplugin.Provider, release.Ref) {
	t.Helper()
	data := []byte(content)
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{
			ProviderID: providerID, ModulePath: "example.test/sample", PackagePath: "example.test/sample/source", Version: "v0.1.0",
		},
		Files:    []sourceplugin.FileSpec{{Path: filePath, Size: int64(len(data)), Digest: provenance.SHA256(data), Mode: sourceplugin.Mode0644}},
		Profiles: []sourceplugin.ProfileSpec{{ID: "default", Files: []string{filePath}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sourceplugin.NewTree(manifest, []sourceplugin.TreeInput{{Path: filePath, Content: data}}, sourceplugin.DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	provider, err := sourceplugin.NewProvider(manifest, tree)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	return provider, ref
}

func resolverScaleProviders(t *testing.T, count int) []sourceplugin.Provider {
	t.Helper()
	providers := make([]sourceplugin.Provider, count)
	for index := range providers {
		providerID := fmt.Sprintf("p%08d", index)
		modulePath := "example.test/" + providerID
		manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
			Identity: sourceplugin.IdentitySpec{
				ProviderID: providerID, ModulePath: modulePath, PackagePath: modulePath + "/source", Version: "v0.1.0",
			},
			Files: []sourceplugin.FileSpec{}, Profiles: []sourceplugin.ProfileSpec{{ID: "default", Files: []string{}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		tree, err := sourceplugin.NewTree(manifest, []sourceplugin.TreeInput{}, sourceplugin.DefaultTreeLimits())
		if err != nil {
			t.Fatal(err)
		}
		providers[index], err = sourceplugin.NewProvider(manifest, tree)
		if err != nil {
			t.Fatal(err)
		}
	}
	return providers
}

func assertReleaseError(t *testing.T, err error, class release.ErrorClass, code, reason, pointer string, stage release.Stage) *release.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("operation succeeded, want %s", reason)
	}
	var projected *release.Error
	if !errors.As(err, &projected) {
		t.Fatalf("error type = %T", err)
	}
	if projected.Class() != class || projected.Code() != code || projected.Reason() != reason || projected.Pointer() != pointer || projected.Stage() != stage || !errors.Is(projected, class) {
		t.Fatalf("error = class=%v code=%q reason=%q pointer=%q stage=%q message=%q", projected.Class(), projected.Code(), projected.Reason(), projected.Pointer(), projected.Stage(), projected.Error())
	}
	if projected.Error() != class.Error() {
		t.Fatalf("error message = %q, want %q", projected.Error(), class.Error())
	}
	if errors.Unwrap(projected) != nil {
		t.Fatal("release error retained a raw cause")
	}
	return projected
}

package sourceplugin_test

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
)

func TestExternalProviderSnapshotCallsOnceAndStaysImmutable(t *testing.T) {
	manifest, tree := externalProviderValues(t, "a", "a")
	otherManifest, otherTree := externalProviderValues(t, "b", "b")
	source := &countingProvider{
		manifests: []sourceplugin.Manifest{manifest, otherManifest},
		trees:     []sourceplugin.Tree{tree, otherTree},
	}
	snapshot, err := sourceplugin.SnapshotProvider(source)
	if err != nil {
		t.Fatal(err)
	}
	if source.manifestCalls != 1 || source.treeCalls != 1 {
		t.Fatalf("source calls = manifest:%d tree:%d", source.manifestCalls, source.treeCalls)
	}
	source.manifests[0] = sourceplugin.Manifest{}
	source.trees[0] = sourceplugin.Tree{}
	for index := 0; index < 3; index++ {
		if snapshot.Manifest().Digest() != manifest.Digest() || snapshot.Tree().Digest() != tree.Digest() {
			t.Fatalf("snapshot changed at read %d", index)
		}
	}
	if source.manifestCalls != 1 || source.treeCalls != 1 {
		t.Fatalf("snapshot delegated after capture: manifest:%d tree:%d", source.manifestCalls, source.treeCalls)
	}
}

func TestExternalProviderSnapshotRejectsEveryNilLikeImplementationBeforeCalls(t *testing.T) {
	var pointer *countingProvider
	var mapValue nilMapProvider
	var sliceValue nilSliceProvider
	var funcValue nilFuncProvider
	var chanValue nilChanProvider
	providers := []sourceplugin.Provider{nil, pointer, mapValue, sliceValue, funcValue, chanValue}
	for index, provider := range providers {
		_, err := sourceplugin.SnapshotProvider(provider)
		assertExternalProviderError(t, err, "provider_nil", "/provider")
		if pointer != nil && (pointer.manifestCalls != 0 || pointer.treeCalls != 0) {
			t.Fatalf("case %d called typed nil provider", index)
		}
	}
}

func TestExternalProviderSnapshotContainsPanicsAndPreservesCallOrder(t *testing.T) {
	manifest, tree := externalProviderValues(t, "a", "a")
	t.Run("manifest panic stops tree", func(t *testing.T) {
		source := &countingProvider{panicManifest: true}
		_, err := sourceplugin.SnapshotProvider(source)
		projected := assertExternalProviderError(t, err, "provider_manifest_panic", "/provider/manifest")
		if source.manifestCalls != 1 || source.treeCalls != 0 {
			t.Fatalf("calls = manifest:%d tree:%d", source.manifestCalls, source.treeCalls)
		}
		if errors.Unwrap(projected) != nil {
			t.Fatal("provider panic was retained as a cause")
		}
	})

	t.Run("tree panic follows manifest", func(t *testing.T) {
		source := &countingProvider{manifests: []sourceplugin.Manifest{manifest}, panicTree: true}
		_, err := sourceplugin.SnapshotProvider(source)
		assertExternalProviderError(t, err, "provider_tree_panic", "/provider/tree")
		if source.manifestCalls != 1 || source.treeCalls != 1 {
			t.Fatalf("calls = manifest:%d tree:%d", source.manifestCalls, source.treeCalls)
		}
	})

	t.Run("zero manifest still captures tree once", func(t *testing.T) {
		source := &countingProvider{manifests: []sourceplugin.Manifest{{}}, trees: []sourceplugin.Tree{tree}}
		_, err := sourceplugin.SnapshotProvider(source)
		assertExternalProviderError(t, err, "provider_manifest_required", "/manifest")
		if source.manifestCalls != 1 || source.treeCalls != 1 {
			t.Fatalf("calls = manifest:%d tree:%d", source.manifestCalls, source.treeCalls)
		}
	})
}

func TestExternalProviderSupportsValidEmptySnapshot(t *testing.T) {
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: "sample.empty", ModulePath: "example.com/sample/empty", PackagePath: "example.com/sample/empty/source", Version: "v0.1.0"},
		Files:    []sourceplugin.FileSpec{}, Profiles: []sourceplugin.ProfileSpec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sourceplugin.NewTree(manifest, []sourceplugin.TreeInput{}, sourceplugin.DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := sourceplugin.SnapshotProvider(&countingProvider{manifests: []sourceplugin.Manifest{manifest}, trees: []sourceplugin.Tree{tree}})
	if err != nil || snapshot.Tree().Digest() != tree.Digest() {
		t.Fatalf("empty snapshot = %#v, err=%v", snapshot, err)
	}
}

func TestExternalProviderSnapshotsSupportConcurrentDefensiveReads(t *testing.T) {
	manifest, tree := externalProviderValues(t, "a", "a")
	source := &countingProvider{manifests: []sourceplugin.Manifest{manifest}, trees: []sourceplugin.Tree{tree}}
	snapshot, err := sourceplugin.SnapshotProvider(source)
	if err != nil {
		t.Fatal(err)
	}
	wantCanonical, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	wantManifestDigest, wantTreeDigest := manifest.Digest(), tree.Digest()

	const workers = 16
	const iterations = 100
	failures := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				files := tree.Files()
				if len(files) != 1 {
					failures <- fmt.Errorf("tree files length = %d", len(files))
					return
				}
				fileBytes := files[0].Bytes()
				if !bytes.Equal(fileBytes, []byte("a")) {
					failures <- fmt.Errorf("tree bytes = %q", fileBytes)
					return
				}
				fileBytes[0] = 'x'
				files[0] = sourceplugin.TreeFile{}

				lookup, ok := tree.Lookup("a")
				if !ok || !bytes.Equal(lookup.Bytes(), []byte("a")) {
					failures <- fmt.Errorf("lookup = %#v, ok=%v", lookup, ok)
					return
				}
				lookupBytes := lookup.Bytes()
				lookupBytes[0] = 'x'

				capturedManifest := snapshot.Manifest()
				canonical, canonicalErr := capturedManifest.CanonicalJSON()
				if canonicalErr != nil || !bytes.Equal(canonical, wantCanonical) {
					failures <- fmt.Errorf("canonical changed: err=%v value=%q", canonicalErr, canonical)
					return
				}
				canonical[0] = 'x'
				manifestFiles := capturedManifest.Files()
				manifestFiles[0] = sourceplugin.File{}

				capturedTree := snapshot.Tree()
				capturedFiles := capturedTree.Files()
				capturedBytes := capturedFiles[0].Bytes()
				capturedBytes[0] = 'x'
				capturedFiles[0] = sourceplugin.TreeFile{}
				if capturedManifest.Digest() != wantManifestDigest || capturedTree.Digest() != wantTreeDigest {
					failures <- fmt.Errorf("captured digest changed: manifest=%s tree=%s", capturedManifest.Digest().String(), capturedTree.Digest().String())
					return
				}
			}
		}()
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}

	finalCanonical, err := snapshot.Manifest().CanonicalJSON()
	finalFile, ok := snapshot.Tree().Lookup("a")
	if err != nil || !bytes.Equal(finalCanonical, wantCanonical) || !ok || !bytes.Equal(finalFile.Bytes(), []byte("a")) ||
		snapshot.Manifest().Digest() != wantManifestDigest || snapshot.Tree().Digest() != wantTreeDigest {
		t.Fatalf("snapshot changed after concurrent reads: canonical=%q file=%q ok=%v err=%v", finalCanonical, finalFile.Bytes(), ok, err)
	}
	if source.manifestCalls != 1 || source.treeCalls != 1 {
		t.Fatalf("snapshot delegated during concurrent reads: manifest=%d tree=%d", source.manifestCalls, source.treeCalls)
	}
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
		panic("credential-secret")
	}
	if len(p.manifests) == 0 {
		return sourceplugin.Manifest{}
	}
	return p.manifests[(p.manifestCalls-1)%len(p.manifests)]
}

func (p *countingProvider) Tree() sourceplugin.Tree {
	p.treeCalls++
	if p.panicTree {
		panic("credential-secret")
	}
	if len(p.trees) == 0 {
		return sourceplugin.Tree{}
	}
	return p.trees[(p.treeCalls-1)%len(p.trees)]
}

type nilMapProvider map[string]string

func (nilMapProvider) Manifest() sourceplugin.Manifest { return sourceplugin.Manifest{} }
func (nilMapProvider) Tree() sourceplugin.Tree         { return sourceplugin.Tree{} }

type nilSliceProvider []string

func (nilSliceProvider) Manifest() sourceplugin.Manifest { return sourceplugin.Manifest{} }
func (nilSliceProvider) Tree() sourceplugin.Tree         { return sourceplugin.Tree{} }

type nilFuncProvider func()

func (nilFuncProvider) Manifest() sourceplugin.Manifest { return sourceplugin.Manifest{} }
func (nilFuncProvider) Tree() sourceplugin.Tree         { return sourceplugin.Tree{} }

type nilChanProvider chan struct{}

func (nilChanProvider) Manifest() sourceplugin.Manifest { return sourceplugin.Manifest{} }
func (nilChanProvider) Tree() sourceplugin.Tree         { return sourceplugin.Tree{} }

func externalProviderValues(t *testing.T, path, content string) (sourceplugin.Manifest, sourceplugin.Tree) {
	t.Helper()
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: "sample.external", ModulePath: "example.com/sample/external", PackagePath: "example.com/sample/external/source", Version: "v0.1.0"},
		Files:    []sourceplugin.FileSpec{{Path: path, Size: int64(len(content)), Digest: provenance.SHA256([]byte(content)), Mode: sourceplugin.Mode0644}},
		Profiles: []sourceplugin.ProfileSpec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sourceplugin.NewTree(manifest, []sourceplugin.TreeInput{{Path: path, Content: []byte(content)}}, sourceplugin.DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	return manifest, tree
}

func assertExternalProviderError(t *testing.T, err error, reason, pointer string) *sourceplugin.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("operation succeeded, want %s", reason)
	}
	var projected *sourceplugin.Error
	if !errors.As(err, &projected) {
		t.Fatalf("error type = %T", err)
	}
	if projected.Class() != sourceplugin.ErrProviderInvalid || projected.Code() != "source_provider_invalid" || projected.Reason() != reason || projected.Pointer() != pointer || projected.Error() != "source provider is invalid" || !errors.Is(projected, sourceplugin.ErrProviderInvalid) {
		t.Fatalf("provider error = class=%v code=%q reason=%q pointer=%q message=%q", projected.Class(), projected.Code(), projected.Reason(), projected.Pointer(), projected.Error())
	}
	if projected.Source() != "" || projected.Line() != 0 || projected.Column() != 0 || projected.Cycle() != nil || errors.Unwrap(projected) != nil {
		t.Fatalf("provider error leaked diagnostics: %#v", projected)
	}
	return projected
}

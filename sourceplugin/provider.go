package sourceplugin

import (
	"reflect"
	"sort"
)

type Provider interface {
	Manifest() Manifest
	Tree() Tree
}

type providerSnapshot struct {
	manifest Manifest
	tree     Tree
}

func (p providerSnapshot) Manifest() Manifest { return p.manifest }
func (p providerSnapshot) Tree() Tree         { return p.tree }

func NewProvider(manifest Manifest, tree Tree) (Provider, error) {
	if manifest.APIVersion() != APIVersion || manifest.Kind() != Kind {
		return nil, newProviderError("provider_manifest_required", "/manifest")
	}
	if tree.Digest().String() == "" {
		return nil, newProviderError("provider_tree_required", "/tree")
	}
	manifestFiles := manifest.Files()
	treeFiles := tree.Files()
	sort.Slice(manifestFiles, func(i, j int) bool { return manifestFiles[i].Path() < manifestFiles[j].Path() })
	sort.Slice(treeFiles, func(i, j int) bool { return treeFiles[i].Path() < treeFiles[j].Path() })
	manifestPaths := filePaths(manifestFiles)
	treePaths := treeFilePaths(treeFiles)
	union := sortedPathUnion(manifestPaths, treePaths)
	unionIndex := pathIndices(union)
	manifestSet := pathSet(manifestPaths)
	treeSet := pathSet(treePaths)
	for _, filePath := range union {
		if _, expected := manifestSet[filePath]; expected {
			if _, present := treeSet[filePath]; !present {
				return nil, newProviderError("provider_file_missing", filePointer(unionIndex[filePath], "path"))
			}
		}
	}
	for _, filePath := range union {
		if _, present := treeSet[filePath]; present {
			if _, expected := manifestSet[filePath]; !expected {
				return nil, newProviderError("provider_file_extra", filePointer(unionIndex[filePath], "path"))
			}
		}
	}
	treeByPath := make(map[string]TreeFile, len(treeFiles))
	for _, file := range treeFiles {
		treeByPath[file.Path()] = file
	}
	for index, file := range manifestFiles {
		candidate := treeByPath[file.Path()]
		if candidate.Mode() != file.Mode() {
			return nil, newProviderError("provider_file_mode_mismatch", filePointer(index, "mode"))
		}
	}
	for index, file := range manifestFiles {
		candidate := treeByPath[file.Path()]
		if candidate.Size() != file.Size() {
			return nil, newProviderError("provider_file_size_mismatch", filePointer(index, "size"))
		}
	}
	for index, file := range manifestFiles {
		candidate := treeByPath[file.Path()]
		if candidate.Digest() != file.Digest() {
			return nil, newProviderError("provider_file_digest_mismatch", filePointer(index, "digest"))
		}
	}
	return providerSnapshot{manifest: manifest, tree: tree}, nil
}

func SnapshotProvider(provider Provider) (Provider, error) {
	if nilLikeProvider(provider) {
		return nil, newProviderError("provider_nil", "/provider")
	}
	manifest, panicked := captureProviderManifest(provider)
	if panicked {
		return nil, newProviderError("provider_manifest_panic", "/provider/manifest")
	}
	tree, panicked := captureProviderTree(provider)
	if panicked {
		return nil, newProviderError("provider_tree_panic", "/provider/tree")
	}
	return NewProvider(manifest, tree)
}

func nilLikeProvider(provider Provider) bool {
	if provider == nil {
		return true
	}
	value := reflect.ValueOf(provider)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func captureProviderManifest(provider Provider) (manifest Manifest, panicked bool) {
	defer func() {
		if recover() != nil {
			manifest = Manifest{}
			panicked = true
		}
	}()
	manifest = provider.Manifest()
	return manifest, false
}

func captureProviderTree(provider Provider) (tree Tree, panicked bool) {
	defer func() {
		if recover() != nil {
			tree = Tree{}
			panicked = true
		}
	}()
	tree = provider.Tree()
	return tree, false
}

func treeFilePaths(files []TreeFile) []string {
	paths := make([]string, len(files))
	for index, file := range files {
		paths[index] = file.Path()
	}
	return paths
}

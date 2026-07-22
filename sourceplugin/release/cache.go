package release

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
)

const (
	cacheEntryAPIVersion = "nexa.dev/source-cache-entry/v1"
	cacheEntryKind       = "SourceCacheEntry"
)

type CacheLimits struct {
	Tree             sourceplugin.TreeLimits
	MaxManifestBytes int64
	MaxMarkerBytes   int64
	MaxPathBytes     int
}

func DefaultCacheLimits() CacheLimits {
	return CacheLimits{Tree: sourceplugin.DefaultTreeLimits(), MaxManifestBytes: 64 << 20, MaxMarkerBytes: 4 << 10, MaxPathBytes: 1024}
}

func (l CacheLimits) Equal(other CacheLimits) bool { return l == other }

type DirectoryCache struct {
	root   string
	limits CacheLimits
	loader cacheLoadOverride
	valid  bool
}

type cacheLoadOverride func(context.Context, Ref) (ResolvedRelease, error)

func OpenDirectoryCache(root string, limits CacheLimits) (*DirectoryCache, error) {
	if !validCacheRoot(root) {
		return nil, releaseError(ErrReleaseInput, "source_release_invalid", "cache_root_invalid", "/cache/root", StageCacheOpen)
	}
	if pointer := validateCacheLimits(limits); pointer != "" {
		return nil, releaseError(ErrReleaseInput, "source_release_invalid", "cache_limit_invalid", pointer, StageCacheOpen)
	}
	if _, err := validateCacheRootComponents(root, StageCacheOpen); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(root); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, cacheConflictAt("cache_root_type_invalid", "/cache/root", StageCacheOpen)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, cacheInternal("cache_root_access_failed", "/cache/root", StageCacheOpen)
	}
	return &DirectoryCache{root: root, limits: limits, valid: true}, nil
}

func (c *DirectoryCache) Limits() CacheLimits {
	if c == nil || !c.valid {
		return CacheLimits{}
	}
	return c.limits
}

func (c *DirectoryCache) Load(ctx context.Context, ref Ref) (ResolvedRelease, error) {
	if c == nil || !c.valid {
		return ResolvedRelease{}, releaseError(ErrReleaseInput, "source_release_invalid", "cache_root_invalid", "/cache", StageCacheLoad)
	}
	if !ref.isValid() {
		return ResolvedRelease{}, releaseError(ErrReleaseInput, "source_release_invalid", "ref_required", "/ref", StageCacheLoad)
	}
	if err := validateCacheContext(ctx, StageCacheLoad); err != nil {
		return ResolvedRelease{}, err
	}
	if c.loader != nil {
		return loadFromOverride(c.loader, ctx, ref)
	}
	return c.loadEntry(ctx, filepath.Join(c.root, cacheEntryName(ref)), ref, StageCacheLoad)
}

func (c *DirectoryCache) Store(ctx context.Context, resolved ResolvedRelease) error {
	if c == nil || !c.valid {
		return releaseError(ErrReleaseInput, "source_release_invalid", "cache_root_invalid", "/cache", StageCacheStore)
	}
	if !resolved.isValid() {
		return releaseError(ErrReleaseInput, "source_release_invalid", "ref_required", "/release", StageCacheStore)
	}
	if err := validateResolvedRelease(resolved); err != nil {
		return err
	}
	if err := validateCacheContext(ctx, StageCacheStore); err != nil {
		return err
	}
	manifestBytes, err := resolved.manifest.CanonicalJSON()
	if err != nil {
		return cacheInternal("cache_write_failed", "/entry/manifest", StageCacheStore)
	}
	markerBytes, markerErr := canonicalCacheMarker(resolved.ref)
	if markerErr != nil {
		return markerErr
	}
	if pointer := storePolicyPointer(resolved, len(manifestBytes), len(markerBytes), c.limits); pointer != "" {
		return releaseError(ErrReleaseInput, "source_release_invalid", "cache_limit_exceeded", pointer, StageCacheStore)
	}
	if err := ensureCacheRoot(c.root); err != nil {
		return err
	}
	entry := filepath.Join(c.root, cacheEntryName(resolved.ref))
	if _, err := os.Lstat(entry); err == nil {
		_, loadErr := c.loadEntry(ctx, entry, resolved.ref, StageCacheStore)
		return loadErr
	} else if !errors.Is(err, os.ErrNotExist) {
		return cacheInternal("cache_read_failed", "/entry", StageCacheStore)
	}
	stage, err := os.MkdirTemp(c.root, ".stage-")
	if err != nil {
		return cacheInternal("cache_write_failed", "/entry", StageCacheStore)
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o700); err != nil {
		return cacheInternal("cache_write_failed", "/entry", StageCacheStore)
	}
	if err := writeCacheFile(filepath.Join(stage, "manifest.json"), manifestBytes, 0o600); err != nil {
		return cacheInternal("cache_write_failed", "/entry/manifest", StageCacheStore)
	}
	treeRoot := filepath.Join(stage, "tree")
	if err := os.Mkdir(treeRoot, 0o755); err != nil {
		return cacheInternal("cache_write_failed", "/entry/tree", StageCacheStore)
	}
	if err := os.Chmod(treeRoot, 0o755); err != nil {
		return cacheInternal("cache_write_failed", "/entry/tree", StageCacheStore)
	}
	for _, file := range resolved.tree.Files() {
		if err := validateCacheContext(ctx, StageCacheStore); err != nil {
			return err
		}
		destination := filepath.Join(treeRoot, filepath.FromSlash(file.Path()))
		if !withinCacheTree(treeRoot, destination) {
			return cacheInternal("cache_write_failed", "/entry/tree", StageCacheStore)
		}
		if err := ensureCacheTreeDirectory(treeRoot, filepath.Dir(destination)); err != nil {
			return cacheInternal("cache_write_failed", "/entry/tree", StageCacheStore)
		}
		mode := os.FileMode(0o644)
		if file.Mode() == sourceplugin.Mode0755 {
			mode = 0o755
		}
		if err := writeCacheFile(destination, file.Bytes(), mode); err != nil {
			return cacheInternal("cache_write_failed", "/entry/tree", StageCacheStore)
		}
		content, err := os.ReadFile(destination)
		if err != nil || provenance.SHA256(content) != file.Digest() {
			return cacheInternal("cache_write_failed", "/entry/tree", StageCacheStore)
		}
	}
	if err := writeCacheFile(filepath.Join(stage, "complete.json"), markerBytes, 0o600); err != nil {
		return cacheInternal("cache_write_failed", "/entry/complete", StageCacheStore)
	}
	if _, err := c.loadEntry(ctx, stage, resolved.ref, StageCacheStore); err != nil {
		return err
	}
	if err := os.Rename(stage, entry); err != nil {
		if _, statErr := os.Lstat(entry); statErr == nil {
			_, loadErr := c.loadEntry(ctx, entry, resolved.ref, StageCacheStore)
			if loadErr == nil {
				return nil
			}
			return cacheConflictAt("cache_publish_conflict", "/entry", StageCacheStore)
		}
		return cacheInternal("cache_publish_failed", "/entry", StageCacheStore)
	}
	_, err = c.loadEntry(ctx, entry, resolved.ref, StageCacheStore)
	return err
}

func (c *DirectoryCache) loadEntry(ctx context.Context, entry string, ref Ref, stage Stage) (ResolvedRelease, error) {
	if err := validateCacheContext(ctx, stage); err != nil {
		return ResolvedRelease{}, err
	}
	exists, rootErr := validateCacheRootComponents(c.root, stage)
	if rootErr != nil {
		return ResolvedRelease{}, rootErr
	}
	if !exists {
		return ResolvedRelease{}, cacheMiss()
	}
	rootInfo, err := os.Lstat(c.root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return ResolvedRelease{}, cacheConflictAt("cache_root_type_invalid", "/cache/root", stage)
	}
	entryInfo, err := os.Lstat(entry)
	if errors.Is(err, os.ErrNotExist) {
		return ResolvedRelease{}, cacheMiss()
	}
	if err != nil || !entryInfo.IsDir() || entryInfo.Mode()&os.ModeSymlink != 0 {
		return ResolvedRelease{}, cacheConflictAt("cache_entry_type_invalid", "/entry", stage)
	}
	if entryInfo.Mode().Perm() != 0o700 {
		return ResolvedRelease{}, cacheConflictAt("cache_entry_mode_mismatch", "/entry", stage)
	}
	entries, err := os.ReadDir(entry)
	if err != nil || !sameNames(entries, []string{"complete.json", "manifest.json", "tree"}) {
		return ResolvedRelease{}, cacheConflictAt("cache_entry_incomplete", "/entry", stage)
	}
	markerBytes, err := readCacheFile(filepath.Join(entry, "complete.json"), c.limits.MaxMarkerBytes, 0o600)
	if err != nil {
		return ResolvedRelease{}, projectCacheReadError(err, "cache_marker_invalid", "/entry/complete", stage)
	}
	markerRef, markerErr := parseCacheMarker(markerBytes)
	if markerErr != nil {
		markerErr.stage = stage
		return ResolvedRelease{}, markerErr
	}
	if !markerRef.Equal(ref) {
		return ResolvedRelease{}, cacheConflictAt("cache_marker_mismatch", firstRefMismatchPointer(ref, markerRef), stage)
	}
	manifestBytes, err := readCacheFile(filepath.Join(entry, "manifest.json"), c.limits.MaxManifestBytes, 0o600)
	if err != nil {
		return ResolvedRelease{}, projectCacheReadError(err, "cache_manifest_invalid", "/entry/manifest", stage)
	}
	manifest, parseErr := sourceplugin.Parse("cache/manifest.json", manifestBytes)
	if parseErr != nil {
		return ResolvedRelease{}, cacheConflictAt("cache_manifest_invalid", "/entry/manifest", stage)
	}
	canonical, canonicalErr := manifest.CanonicalJSON()
	if canonicalErr != nil || string(canonical) != string(manifestBytes) || manifest.Digest() != ref.ManifestDigest() {
		return ResolvedRelease{}, cacheConflictAt("cache_manifest_mismatch", "/entry/manifest", stage)
	}
	identity := manifest.Identity()
	if identity.ProviderID() != ref.ProviderID() || identity.ModulePath() != ref.ModulePath() || identity.PackagePath() != ref.PackagePath() || identity.Version() != ref.Version() {
		return ResolvedRelease{}, cacheConflictAt("cache_manifest_mismatch", "/entry/manifest", stage)
	}
	if pointer := loadManifestPolicyPointer(manifest, len(manifestBytes), len(markerBytes), c.limits); pointer != "" {
		return ResolvedRelease{}, cacheConflictAt("cache_entry_limit_exceeded", pointer, stage)
	}
	treeRoot := filepath.Join(entry, "tree")
	treeInfo, err := os.Lstat(treeRoot)
	if err != nil || !treeInfo.IsDir() || treeInfo.Mode()&os.ModeSymlink != 0 {
		return ResolvedRelease{}, cacheConflictAt("cache_entry_type_invalid", "/entry/tree", stage)
	}
	if treeInfo.Mode().Perm() != 0o755 {
		return ResolvedRelease{}, cacheConflictAt("cache_entry_mode_mismatch", "/entry/tree", stage)
	}
	expected := make(map[string]sourceplugin.File, len(manifest.Files()))
	expectedDirectories := map[string]bool{".": true}
	for _, file := range manifest.Files() {
		expected[file.Path()] = file
		for directory := filepath.ToSlash(filepath.Dir(file.Path())); directory != "."; directory = filepath.ToSlash(filepath.Dir(directory)) {
			expectedDirectories[directory] = true
		}
	}
	seen := make(map[string]bool, len(expected))
	inputs := make([]sourceplugin.TreeInput, 0, len(expected))
	actualFiles := 0
	if err := filepath.WalkDir(treeRoot, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			actualFiles++
			if actualFiles > c.limits.Tree.MaxFiles {
				return errCacheFileCount
			}
		}
		return nil
	}); errors.Is(err, errCacheFileCount) {
		return ResolvedRelease{}, cacheConflictAt("cache_entry_limit_exceeded", "/cache/limits/tree/maxFiles", stage)
	} else if err != nil {
		return ResolvedRelease{}, cacheConflictAt("cache_inventory_invalid", "/entry/tree", stage)
	}
	err = filepath.WalkDir(treeRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == treeRoot {
			return nil
		}
		if err := validateCacheContext(ctx, stage); err != nil {
			return err
		}
		relative, relErr := filepath.Rel(treeRoot, path)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return cacheConflictAt("cache_entry_type_invalid", "/entry/tree", stage)
		}
		if entry.IsDir() {
			if !expectedDirectories[relative] {
				return cacheConflictAt("cache_inventory_invalid", "/entry/tree", stage)
			}
			info, infoErr := entry.Info()
			if infoErr != nil || info.Mode().Perm() != 0o755 {
				return cacheConflictAt("cache_file_mode_mismatch", "/entry/tree", stage)
			}
			return nil
		}
		file, ok := expected[relative]
		if !ok {
			return cacheConflictAt("cache_inventory_invalid", "/entry/tree", stage)
		}
		mode := os.FileMode(0o644)
		if file.Mode() == sourceplugin.Mode0755 {
			mode = 0o755
		}
		content, readErr := readCacheFile(path, c.limits.Tree.MaxFileBytes, mode)
		if readErr != nil {
			if errors.Is(readErr, errCacheMode) {
				return cacheConflictAt("cache_file_mode_mismatch", treePointer(len(inputs), "mode"), stage)
			}
			if errors.Is(readErr, errCacheLimit) {
				return cacheConflictAt("cache_entry_limit_exceeded", "/cache/limits/tree/maxFileBytes", stage)
			}
			return projectCacheReadError(readErr, "cache_file_size_mismatch", treePointer(len(inputs), "content"), stage)
		}
		if int64(len(content)) != file.Size() {
			return cacheConflictAt("cache_file_size_mismatch", treePointer(len(inputs), "content"), stage)
		}
		if provenance.SHA256(content) != file.Digest() {
			return cacheConflictAt("cache_file_digest_mismatch", treePointer(len(inputs), "content"), stage)
		}
		seen[relative] = true
		inputs = append(inputs, sourceplugin.TreeInput{Path: relative, Content: content})
		return nil
	})
	if err != nil {
		var projected *Error
		if errors.As(err, &projected) {
			return ResolvedRelease{}, projected
		}
		return ResolvedRelease{}, cacheConflictAt("cache_inventory_invalid", "/entry/tree", stage)
	}
	if len(seen) != len(expected) {
		return ResolvedRelease{}, cacheConflictAt("cache_inventory_invalid", "/entry/tree", stage)
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Path < inputs[j].Path })
	tree, treeErr := sourceplugin.NewTree(manifest, inputs, c.limits.Tree)
	if treeErr != nil {
		return ResolvedRelease{}, cacheConflictAt("cache_inventory_invalid", "/entry/tree", stage)
	}
	if tree.Digest() != ref.TreeDigest() {
		return ResolvedRelease{}, cacheConflictAt("cache_tree_digest_mismatch", "/entry/tree", stage)
	}
	provider, providerErr := sourceplugin.NewProvider(manifest, tree)
	if providerErr != nil {
		return ResolvedRelease{}, cacheConflictAt("cache_inventory_invalid", "/entry/tree", stage)
	}
	return ResolvedRelease{ref: ref, manifest: manifest, tree: tree, provider: provider, valid: true}, nil
}

func ensureCacheRoot(root string) *Error {
	exists, componentErr := validateCacheRootComponents(root, StageCacheStore)
	if componentErr != nil {
		return componentErr
	}
	if !exists {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return cacheInternal("cache_root_access_failed", "/cache/root", StageCacheStore)
		}
		if err := os.Chmod(root, 0o700); err != nil {
			return cacheInternal("cache_root_access_failed", "/cache/root", StageCacheStore)
		}
		if _, err := validateCacheRootComponents(root, StageCacheStore); err != nil {
			return err
		}
	}
	if info, err := os.Lstat(root); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return cacheConflictAt("cache_root_type_invalid", "/cache/root", StageCacheStore)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return cacheInternal("cache_root_access_failed", "/cache/root", StageCacheStore)
	}
	return nil
}

func validateCacheRootComponents(root string, stage Stage) (bool, *Error) {
	volume := filepath.VolumeName(root)
	current := volume + string(os.PathSeparator)
	relative := strings.TrimPrefix(root, current)
	for _, component := range strings.Split(relative, string(os.PathSeparator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, cacheInternal("cache_root_access_failed", "/cache/root", stage)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, cacheConflictAt("cache_root_type_invalid", "/cache/root", stage)
		}
	}
	return true, nil
}

func writeCacheFile(path string, content []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func ensureCacheTreeDirectory(root, directory string) error {
	if directory == root {
		return os.Chmod(root, 0o755)
	}
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
		return fs.ErrInvalid
	}
	current := root
	for _, segment := range strings.Split(relative, string(os.PathSeparator)) {
		current = filepath.Join(current, segment)
		if err := os.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := os.Chmod(current, 0o755); err != nil {
			return err
		}
	}
	return nil
}

var errCacheMode = errors.New("cache mode mismatch")
var errCacheLimit = errors.New("cache size limit exceeded")
var errCacheFileCount = errors.New("cache file count exceeded")

func readCacheFile(path string, limit int64, mode os.FileMode) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fs.ErrInvalid
	}
	if info.Mode().Perm() != mode.Perm() {
		return nil, errCacheMode
	}
	if info.Size() < 0 || info.Size() > limit {
		return nil, errCacheLimit
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(content)) > limit {
		return nil, errCacheLimit
	}
	return content, nil
}

func projectCacheReadError(err error, invalidReason, pointer string, stage Stage) *Error {
	if errors.Is(err, errCacheMode) {
		return cacheConflictAt("cache_entry_mode_mismatch", pointer, stage)
	}
	if errors.Is(err, errCacheLimit) {
		return cacheConflictAt("cache_entry_limit_exceeded", pointer, stage)
	}
	return cacheConflictAt(invalidReason, pointer, stage)
}

func sameNames(entries []os.DirEntry, expected []string) bool {
	if len(entries) != len(expected) {
		return false
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	sort.Strings(names)
	sort.Strings(expected)
	for i := range names {
		if names[i] != expected[i] {
			return false
		}
	}
	return true
}

func withinCacheTree(root, candidate string) bool {
	return candidate != root && strings.HasPrefix(candidate, root+string(os.PathSeparator))
}

func validateResolvedRelease(resolved ResolvedRelease) *Error {
	identity := resolved.manifest.Identity()
	actual, err := NewRef(RefSpec{ProviderID: identity.ProviderID(), ModulePath: identity.ModulePath(), PackagePath: identity.PackagePath(), Version: identity.Version(), ManifestDigest: resolved.manifest.Digest(), TreeDigest: resolved.tree.Digest()})
	if err != nil || !actual.Equal(resolved.ref) {
		return releaseError(ErrReleaseConflict, "source_release_conflict", "resolved_ref_mismatch", firstRefMismatchPointer(resolved.ref, actual), StageCacheStore)
	}
	if _, ownerErr := sourceplugin.NewProvider(resolved.manifest, resolved.tree); ownerErr != nil {
		return releaseError(ErrReleaseInput, "source_release_invalid", "provider_invalid", "/release", StageCacheStore)
	}
	return nil
}

func cacheMiss() *Error {
	return releaseError(ErrReleaseUnavailable, "source_release_unavailable", "cache_miss", "/entry", StageCacheLoad)
}

func cacheConflictAt(reason, pointer string, stage Stage) *Error {
	return releaseError(ErrReleaseConflict, "source_release_conflict", reason, pointer, stage)
}

func cacheInternal(reason, pointer string, stage Stage) *Error {
	return releaseError(ErrReleaseInternal, "source_release_internal", reason, pointer, stage)
}

func validCacheRoot(root string) bool {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || !utf8.ValidString(root) || strings.ContainsRune(root, 0) {
		return false
	}
	volume := filepath.VolumeName(root)
	if root == volume+string(filepath.Separator) {
		return false
	}
	for _, character := range root {
		if unicode.Is(unicode.Cc, character) || unicode.Is(unicode.Cf, character) {
			return false
		}
	}
	return true
}

func validateCacheLimits(limits CacheLimits) string {
	switch {
	case limits.Tree.MaxFiles <= 0:
		return "/cache/limits/tree/maxFiles"
	case limits.Tree.MaxFileBytes <= 0:
		return "/cache/limits/tree/maxFileBytes"
	case limits.Tree.MaxTotalBytes <= 0:
		return "/cache/limits/tree/maxTotalBytes"
	case limits.Tree.MaxFileBytes > limits.Tree.MaxTotalBytes:
		return "/cache/limits/tree/maxFileBytes"
	case limits.MaxManifestBytes <= 0:
		return "/cache/limits/maxManifestBytes"
	case limits.MaxMarkerBytes <= 0:
		return "/cache/limits/maxMarkerBytes"
	case limits.MaxPathBytes <= 0:
		return "/cache/limits/maxPathBytes"
	case limits.Tree.MaxFiles > math.MaxInt-2:
		return "/cache/limits/tree/maxFiles"
	}
	return ""
}

type cacheMarkerWire struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Release    cacheReleaseWire `json:"release"`
}

type cacheReleaseWire struct {
	ManifestDigest string `json:"manifestDigest"`
	ModulePath     string `json:"modulePath"`
	PackagePath    string `json:"packagePath"`
	ProviderID     string `json:"providerId"`
	TreeDigest     string `json:"treeDigest"`
	Version        string `json:"version"`
}

func canonicalCacheMarker(ref Ref) ([]byte, error) {
	if !ref.isValid() {
		return nil, releaseError(ErrReleaseInput, "source_release_invalid", "ref_required", "/ref", StageCacheStore)
	}
	wire := cacheMarkerWire{APIVersion: cacheEntryAPIVersion, Kind: cacheEntryKind, Release: cacheReleaseWire{ProviderID: ref.ProviderID(), ModulePath: ref.ModulePath(), PackagePath: ref.PackagePath(), Version: ref.Version(), ManifestDigest: ref.ManifestDigest().String(), TreeDigest: ref.TreeDigest().String()}}
	raw, err := json.Marshal(wire)
	if err != nil {
		return nil, cacheInternal("cache_write_failed", "/entry/complete", StageCacheStore)
	}
	return append(raw, '\n'), nil
}

func parseCacheMarker(data []byte) (Ref, *Error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var wire cacheMarkerWire
	if err := decoder.Decode(&wire); err != nil || wire.APIVersion != cacheEntryAPIVersion || wire.Kind != cacheEntryKind {
		return Ref{}, cacheConflictAt("cache_marker_invalid", "/entry/complete", StageCacheLoad)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Ref{}, cacheConflictAt("cache_marker_invalid", "/entry/complete", StageCacheLoad)
	}
	manifestDigest, manifestErr := provenance.ParseDigest(wire.Release.ManifestDigest)
	treeDigest, treeErr := provenance.ParseDigest(wire.Release.TreeDigest)
	if manifestErr != nil || treeErr != nil {
		return Ref{}, cacheConflictAt("cache_marker_invalid", "/entry/complete", StageCacheLoad)
	}
	ref, refErr := NewRef(RefSpec{ProviderID: wire.Release.ProviderID, ModulePath: wire.Release.ModulePath, PackagePath: wire.Release.PackagePath, Version: wire.Release.Version, ManifestDigest: manifestDigest, TreeDigest: treeDigest})
	if refErr != nil {
		return Ref{}, cacheConflictAt("cache_marker_invalid", "/entry/complete", StageCacheLoad)
	}
	return ref, nil
}

func cacheEntryName(ref Ref) string {
	return strings.TrimPrefix(ref.ManifestDigest().String(), "sha256:") + "-" + strings.TrimPrefix(ref.TreeDigest().String(), "sha256:")
}

func storePolicyPointer(resolved ResolvedRelease, manifestBytes, markerBytes int, limits CacheLimits) string {
	files := resolved.manifest.Files()
	if len(files) > limits.Tree.MaxFiles {
		return "/cache/limits/tree/maxFiles"
	}
	var total int64
	for _, file := range files {
		if file.Size() > limits.Tree.MaxFileBytes {
			return "/cache/limits/tree/maxFileBytes"
		}
		if file.Size() < 0 || total > limits.Tree.MaxTotalBytes-file.Size() {
			return "/cache/limits/tree/maxTotalBytes"
		}
		total += file.Size()
		if len(file.Path()) > limits.MaxPathBytes {
			return "/cache/limits/maxPathBytes"
		}
	}
	if int64(manifestBytes) > limits.MaxManifestBytes {
		return "/cache/limits/maxManifestBytes"
	}
	if int64(markerBytes) > limits.MaxMarkerBytes {
		return "/cache/limits/maxMarkerBytes"
	}
	return ""
}

func loadManifestPolicyPointer(manifest sourceplugin.Manifest, manifestBytes, markerBytes int, limits CacheLimits) string {
	return storePolicyPointer(ResolvedRelease{manifest: manifest}, manifestBytes, markerBytes, limits)
}

func treePointer(index int, field string) string {
	return "/entry/tree/files/" + strconv.Itoa(index) + "/" + field
}

func loadFromOverride(loader cacheLoadOverride, ctx context.Context, ref Ref) (ResolvedRelease, error) {
	resolved, err := loader(ctx, ref)
	if err == nil {
		return resolved, nil
	}
	var projected *Error
	if errors.As(err, &projected) && projected != nil {
		return ResolvedRelease{}, projected
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ResolvedRelease{}, canceledError(err, StageCacheLoad)
	}
	return ResolvedRelease{}, cacheInternal("cache_backend_failed", "/entry", StageCacheLoad)
}

func validateCacheContext(ctx context.Context, stage Stage) *Error {
	if ctx == nil {
		return releaseError(ErrReleaseInput, "source_release_invalid", "context_required", "/context", stage)
	}
	err := ctx.Err()
	if err == context.Canceled || err == context.DeadlineExceeded {
		return canceledError(err, stage)
	}
	if err != nil {
		return cacheInternal("context_invalid", "/context", stage)
	}
	return nil
}

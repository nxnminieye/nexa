package release

import (
	"context"
	"reflect"
	"sort"

	"github.com/nxnminieye/nexa/sourceplugin"
)

type ExactResolver struct {
	cache       *DirectoryCache
	byRef       map[string]ResolvedRelease
	coordinates map[string]Ref
	valid       bool
}

type indexedRelease struct {
	resolved      ResolvedRelease
	coordinateKey string
	fullKey       string
}

func NewExactResolver(cache *DirectoryCache, providers ...sourceplugin.Provider) (*ExactResolver, error) {
	if cache != nil && !cache.valid {
		return nil, releaseError(ErrReleaseInput, "source_release_invalid", "cache_root_invalid", "/cache", StageResolverStatic)
	}
	type attempt struct {
		resolved ResolvedRelease
		err      *Error
	}
	attempts := make([]attempt, len(providers))
	for index, provider := range providers {
		resolved, err := snapshotProvider(provider, "/providers")
		attempts[index] = attempt{resolved: resolved, err: err}
	}
	errorPriority := map[string]int{
		"provider_nil": 0, "provider_manifest_panic": 1, "provider_tree_panic": 2, "provider_invalid": 3,
	}
	var failures []*Error
	var releases []ResolvedRelease
	for _, attempt := range attempts {
		if attempt.err != nil {
			failures = append(failures, attempt.err)
		} else {
			releases = append(releases, attempt.resolved)
		}
	}
	if len(failures) > 0 {
		sort.SliceStable(failures, func(i, j int) bool {
			left, right := errorPriority[failures[i].reason], errorPriority[failures[j].reason]
			if left != right {
				return left < right
			}
			return failures[i].reason < failures[j].reason
		})
		return nil, failures[0]
	}
	indexed := make([]indexedRelease, len(releases))
	for index, resolved := range releases {
		coordinateKey := resolved.ref.coordinateKey()
		indexed[index] = indexedRelease{
			resolved:      resolved,
			coordinateKey: coordinateKey,
			fullKey:       resolved.ref.fullKeyFromCoordinate(coordinateKey),
		}
	}
	sort.Slice(indexed, func(i, j int) bool { return indexed[i].fullKey < indexed[j].fullKey })
	for index := 1; index < len(indexed); index++ {
		if indexed[index-1].coordinateKey == indexed[index].coordinateKey && indexed[index-1].fullKey != indexed[index].fullKey {
			return nil, releaseError(ErrReleaseConflict, "source_release_conflict", "immutable_release_conflict", "/release", StageResolverStatic)
		}
	}
	for index := 1; index < len(indexed); index++ {
		if indexed[index-1].fullKey == indexed[index].fullKey {
			return nil, releaseError(ErrReleaseConflict, "source_release_conflict", "duplicate_release", "/release", StageResolverStatic)
		}
	}
	resolver := &ExactResolver{
		cache: cache, byRef: make(map[string]ResolvedRelease, len(indexed)),
		coordinates: make(map[string]Ref, len(indexed)), valid: true,
	}
	for _, candidate := range indexed {
		resolver.byRef[candidate.fullKey] = candidate.resolved
		resolver.coordinates[candidate.coordinateKey] = candidate.resolved.ref
	}
	return resolver, nil
}

func (r *ExactResolver) Resolve(ctx context.Context, ref Ref) (ResolvedRelease, error) {
	if r == nil || !r.valid {
		return ResolvedRelease{}, releaseError(ErrReleaseInput, "source_release_invalid", "resolver_required", "/resolver", StageResolverStatic)
	}
	if !ref.isValid() {
		return ResolvedRelease{}, releaseError(ErrReleaseInput, "source_release_invalid", "ref_required", "/ref", StageResolverStatic)
	}
	if nilLikeContext(ctx) {
		return ResolvedRelease{}, releaseError(ErrReleaseInput, "source_release_invalid", "context_required", "/context", StageResolverStatic)
	}
	contextErr, panicked := captureContextError(ctx)
	if panicked {
		return ResolvedRelease{}, releaseError(ErrReleaseInternal, "source_release_internal", "context_panic", "/context", StageResolverStatic)
	}
	if contextErr != nil {
		if contextErr == context.Canceled || contextErr == context.DeadlineExceeded {
			return ResolvedRelease{}, canceledError(contextErr, StageResolverStatic)
		}
		return ResolvedRelease{}, releaseError(ErrReleaseInternal, "source_release_internal", "context_panic", "/context", StageResolverStatic)
	}
	if resolved, ok := r.byRef[ref.fullKey()]; ok {
		return resolved, nil
	}
	if static, ok := r.coordinates[ref.coordinateKey()]; ok && !static.Equal(ref) {
		return ResolvedRelease{}, releaseError(ErrReleaseConflict, "source_release_conflict", "immutable_release_conflict", "/release", StageResolverStatic)
	}
	if r.cache == nil {
		return ResolvedRelease{}, releaseError(ErrReleaseUnavailable, "source_release_unavailable", "release_not_found", "/release", StageResolverStatic)
	}
	resolved, err := r.cache.Load(ctx, ref)
	if err != nil {
		if projected, ok := err.(*Error); ok && projected.reason == "cache_miss" {
			return ResolvedRelease{}, releaseError(ErrReleaseUnavailable, "source_release_unavailable", "release_not_found", "/release", StageResolverCache)
		}
		return ResolvedRelease{}, err
	}
	actual, snapshotErr := snapshotProvider(resolved.Provider(), "/release")
	if snapshotErr != nil {
		return ResolvedRelease{}, snapshotErr
	}
	if !actual.ref.Equal(ref) {
		return ResolvedRelease{}, releaseError(ErrReleaseConflict, "source_release_conflict", "resolved_ref_mismatch", firstRefMismatchPointer(ref, actual.ref), StageResolverCache)
	}
	return actual, nil
}

func (r *ExactResolver) Cache() (*DirectoryCache, bool) {
	if r == nil || !r.valid || r.cache == nil {
		return nil, false
	}
	return r.cache, true
}

func nilLikeContext(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	value := reflect.ValueOf(ctx)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func captureContextError(ctx context.Context) (err error, panicked bool) {
	defer func() {
		if recover() != nil {
			err = nil
			panicked = true
		}
	}()
	return ctx.Err(), false
}

func firstRefMismatchPointer(expected, actual Ref) string {
	switch {
	case expected.providerID != actual.providerID:
		return "/ref/providerId"
	case expected.modulePath != actual.modulePath:
		return "/ref/modulePath"
	case expected.packagePath != actual.packagePath:
		return "/ref/packagePath"
	case expected.version != actual.version:
		return "/ref/version"
	case expected.manifestDigest != actual.manifestDigest:
		return "/ref/manifestDigest"
	default:
		return "/ref/treeDigest"
	}
}

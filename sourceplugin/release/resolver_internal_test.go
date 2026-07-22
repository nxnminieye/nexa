package release

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nxnminieye/nexa/sourceplugin/internal/contract"
)

func TestRefIdentityProjectionRejectsUnknownReason(t *testing.T) {
	err := projectRefIdentityIssue(&contract.IdentityIssue{})
	assertInternalReleaseError(t, err, ErrReleaseInternal, "source_release_internal", "contract_issue_unmapped", "/ref", StageRef)
}

func TestRefIdentityProjectionRejectsUnknownAndIllegalPairs(t *testing.T) {
	legal := map[contract.IdentityIssue]struct {
		reason  string
		pointer string
	}{
		{Field: contract.IdentityProviderID, Reason: contract.IdentityProviderIDInvalid}:      {reason: "provider_id_invalid", pointer: "/ref/providerId"},
		{Field: contract.IdentityModulePath, Reason: contract.IdentityModulePathInvalid}:      {reason: "module_path_invalid", pointer: "/ref/modulePath"},
		{Field: contract.IdentityPackagePath, Reason: contract.IdentityPackagePathInvalid}:    {reason: "package_path_invalid", pointer: "/ref/packagePath"},
		{Field: contract.IdentityPackagePath, Reason: contract.IdentityPackageModuleMismatch}: {reason: "package_module_mismatch", pointer: "/ref/packagePath"},
		{Field: contract.IdentityVersion, Reason: contract.IdentityVersionInvalid}:            {reason: "version_invalid", pointer: "/ref/version"},
		{Field: contract.IdentityVersion, Reason: contract.IdentityModuleVersionMismatch}:     {reason: "module_version_mismatch", pointer: "/ref/version"},
	}
	fields := []contract.IdentityField{0, contract.IdentityProviderID, contract.IdentityModulePath, contract.IdentityPackagePath, contract.IdentityVersion, 255}
	reasons := []contract.IdentityReason{0, contract.IdentityProviderIDInvalid, contract.IdentityModulePathInvalid, contract.IdentityPackagePathInvalid, contract.IdentityPackageModuleMismatch, contract.IdentityVersionInvalid, contract.IdentityModuleVersionMismatch, 255}
	for _, field := range fields {
		for _, reason := range reasons {
			issue := contract.IdentityIssue{Field: field, Reason: reason}
			err := projectRefIdentityIssue(&issue)
			if expected, ok := legal[issue]; ok {
				assertInternalReleaseError(t, err, ErrReleaseInput, "source_release_invalid", expected.reason, expected.pointer, StageRef)
				continue
			}
			assertInternalReleaseError(t, err, ErrReleaseInternal, "source_release_internal", "contract_issue_unmapped", "/ref", StageRef)
		}
	}
}

func TestProviderProjectionUnknownOwnerAndReason(t *testing.T) {
	assertInternalReleaseError(t, projectProviderError(errors.New("hostile owner text"), "/provider"), ErrReleaseInternal, "source_release_internal", "contract_issue_unmapped", "/provider", StageProviderSnapshot)
	assertInternalReleaseError(t, projectProviderReason("future_provider_reason", "/provider"), ErrReleaseInternal, "source_release_internal", "contract_issue_unmapped", "/provider", StageProviderSnapshot)
}

func TestExactResolverPrivateCacheBackendMatrix(t *testing.T) {
	request := internalResolvedForCache(t, "request").Ref()
	wrong := internalResolvedForCacheWithIdentity(t, "sample.wrong", "wrong")
	tests := []struct {
		name    string
		load    cacheLoadOverride
		class   ErrorClass
		code    string
		reason  string
		pointer string
	}{
		{"unknown", func(context.Context, Ref) (ResolvedRelease, error) {
			return ResolvedRelease{}, fmt.Errorf("/secret/cache")
		}, ErrReleaseInternal, "source_release_internal", "cache_backend_failed", "/entry"},
		{"miss", func(context.Context, Ref) (ResolvedRelease, error) { return ResolvedRelease{}, cacheMiss() }, ErrReleaseUnavailable, "source_release_unavailable", "release_not_found", "/release"},
		{"wrong ref", func(context.Context, Ref) (ResolvedRelease, error) { return wrong, nil }, ErrReleaseConflict, "source_release_conflict", "resolved_ref_mismatch", "/ref/providerId"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cache := &DirectoryCache{valid: true, limits: DefaultCacheLimits(), loader: test.load}
			resolver, err := NewExactResolver(cache)
			if err != nil {
				t.Fatal(err)
			}
			_, err = resolver.Resolve(context.Background(), request)
			stage := StageCacheLoad
			if test.reason == "release_not_found" || test.reason == "resolved_ref_mismatch" {
				stage = StageResolverCache
			}
			assertInternalReleaseError(t, err, test.class, test.code, test.reason, test.pointer, stage)
		})
	}
}

func TestExactResolverPrivateCacheMidLoadCancellation(t *testing.T) {
	request := internalResolvedForCache(t, "request").Ref()
	ctx, cancel := context.WithCancel(context.Background())
	cache := &DirectoryCache{
		valid: true, limits: DefaultCacheLimits(),
		loader: func(context.Context, Ref) (ResolvedRelease, error) {
			cancel()
			return ResolvedRelease{}, context.Canceled
		},
	}
	resolver, err := NewExactResolver(cache)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(ctx, request)
	projected := assertInternalReleaseError(t, err, ErrReleaseCanceled, "source_release_canceled", "context_canceled", "/context", StageCacheLoad)
	if !errors.Is(projected, context.Canceled) {
		t.Fatal("mid-load cancellation lost context identity")
	}
}

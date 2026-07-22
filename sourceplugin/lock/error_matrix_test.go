package lock

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/internal/contract"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func TestPathIssueProjectionRejectsUnknownReason(t *testing.T) {
	err := projectPathIssue(&contract.PathIssue{}, "key_target_invalid", "/target", StageKey)
	assertInternalLockError(t, err, ErrLockInternal, "source_lock_internal", "owner_projection_failed", "/target", StageKey)
}

func TestIdentityIssueProjectionRejectsUnknownAndIllegalPairs(t *testing.T) {
	legal := map[contract.IdentityIssue]struct {
		reason  string
		pointer string
	}{
		{Field: contract.IdentityProviderID, Reason: contract.IdentityProviderIDInvalid}:      {reason: "provider_id_invalid", pointer: "/release/providerId"},
		{Field: contract.IdentityModulePath, Reason: contract.IdentityModulePathInvalid}:      {reason: "module_path_invalid", pointer: "/release/modulePath"},
		{Field: contract.IdentityPackagePath, Reason: contract.IdentityPackagePathInvalid}:    {reason: "package_path_invalid", pointer: "/release/packagePath"},
		{Field: contract.IdentityPackagePath, Reason: contract.IdentityPackageModuleMismatch}: {reason: "package_module_mismatch", pointer: "/release/packagePath"},
		{Field: contract.IdentityVersion, Reason: contract.IdentityVersionInvalid}:            {reason: "version_invalid", pointer: "/release/version"},
		{Field: contract.IdentityVersion, Reason: contract.IdentityModuleVersionMismatch}:     {reason: "module_version_mismatch", pointer: "/release/version"},
	}
	fields := []contract.IdentityField{0, contract.IdentityProviderID, contract.IdentityModulePath, contract.IdentityPackagePath, contract.IdentityVersion, 255}
	reasons := []contract.IdentityReason{0, contract.IdentityProviderIDInvalid, contract.IdentityModulePathInvalid, contract.IdentityPackagePathInvalid, contract.IdentityPackageModuleMismatch, contract.IdentityVersionInvalid, contract.IdentityModuleVersionMismatch, 255}
	for _, stage := range []Stage{StageParse, StageDerive, StageVerify} {
		for _, field := range fields {
			for _, reason := range reasons {
				issue := contract.IdentityIssue{Field: field, Reason: reason}
				err := projectIdentityIssue(&issue, stage, "/release")
				if expected, ok := legal[issue]; ok {
					assertInternalLockError(t, err, ErrLockInput, "source_lock_invalid", expected.reason, expected.pointer, stage)
					continue
				}
				assertInternalLockError(t, err, ErrLockInternal, "source_lock_internal", "owner_projection_failed", "/release", stage)
			}
		}
	}
}

func TestIdentityProjectionPrecedesDigestValidation(t *testing.T) {
	bad := "bad"
	badProvider := "Bad"
	document := &lockReleaseDocument{
		ProviderID: &badProvider, ModulePath: &bad, PackagePath: &bad, Version: &bad,
		ManifestDigest: &bad, TreeDigest: &bad,
	}
	_, err := refFromDocument(document)
	assertInternalLockError(t, err, ErrLockInput, "source_lock_invalid", "provider_id_invalid", "/release/providerId", StageParse)
	_, err = validateReleaseRef(release.Ref{}, StageDerive)
	assertInternalLockError(t, err, ErrLockInput, "source_lock_invalid", "provider_id_invalid", "/release/providerId", StageDerive)
}

func TestLockSafeIntegerDeriveCanonicalAndVerify(t *testing.T) {
	ref, resolved := internalLockRelease(t)
	const maxSafe = int64(1<<53 - 1)
	digest := provenance.SHA256([]byte("large logical file"))
	files := []BaselineFile{{path: "main.go", mode: sourceplugin.Mode0644, size: maxSafe, digest: digest}}
	snapshot, snapshotErr := newSnapshot(ref, "default", []string{"default"}, "services/sample", files, "", DefaultLimits(), StageDerive)
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	canonical, canonicalErr := snapshot.CanonicalJSON()
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	trusted, transformErr := jcs.Transform(bytes.TrimSuffix(canonical, []byte{'\n'}))
	if transformErr != nil || !bytes.Equal(trusted, bytes.TrimSuffix(canonical, []byte{'\n'})) || snapshot.Digest() != provenance.SHA256(trusted) {
		t.Fatalf("max-safe canonical mismatch trusted=%s canonical=%s digest=%s err=%v", trusted, canonical, snapshot.Digest(), transformErr)
	}

	for _, size := range []int64{1 << 53, 1<<53 + 1, 1<<63 - 1} {
		files[0].size = size
		_, sizeErr := newSnapshot(ref, "default", []string{"default"}, "services/sample", files, "", DefaultLimits(), StageDerive)
		assertInternalLockError(t, sizeErr, ErrLockInput, "source_lock_invalid", "tracked_file_size_invalid", "/trackedFiles/0/size", StageDerive)
	}

	verified, deriveErr := Derive(ref, resolved, "default", "services/sample", DefaultLimits())
	if deriveErr != nil {
		t.Fatal(deriveErr)
	}
	tampered := verified.snapshot
	tampered.trackedFiles = append([]BaselineFile(nil), tampered.trackedFiles...)
	tampered.trackedFiles[0].size = 1 << 53
	_, verifyErr := Verify(tampered, verified.Key(), resolved, DefaultLimits())
	assertInternalLockError(t, verifyErr, ErrLockInput, "source_lock_invalid", "tracked_file_size_invalid", "/trackedFiles/0/size", StageVerify)
}

func TestLockSafeIntegerPrecedence(t *testing.T) {
	ref, resolved := internalLockRelease(t)
	verified, err := Derive(ref, resolved, "default", "services/sample", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := verified.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	baseline := strings.TrimSuffix(string(canonical), "\n")
	digest := verified.TrackedFiles()[0].Digest().String()
	for _, test := range []struct {
		name    string
		mode    string
		size    string
		digest  string
		reason  string
		pointer string
	}{
		{name: "mode before size", mode: "0600", size: "9007199254740992", digest: "bad", reason: "tracked_file_mode_invalid", pointer: "/trackedFiles/0/mode"},
		{name: "size before digest", mode: "0644", size: "9007199254740992", digest: "bad", reason: "tracked_file_size_invalid", pointer: "/trackedFiles/0/size"},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := strings.Replace(baseline, `"mode":"0644"`, `"mode":"`+test.mode+`"`, 1)
			payload = strings.Replace(payload, `"size":5`, `"size":`+test.size, 1)
			payload = strings.Replace(payload, digest, test.digest, 1)
			_, err := Parse(verified.Key().RepositoryPath(), []byte(payload+"\n"), DefaultLimits())
			var projected *Error
			if !errors.As(err, &projected) || projected.Class() != ErrLockInput || projected.Code() != "source_lock_invalid" ||
				projected.Reason() != test.reason || projected.Pointer() != test.pointer || projected.Stage() != StageParse ||
				projected.Source() != verified.Key().RepositoryPath() || projected.Line() <= 0 || projected.Column() <= 0 {
				t.Fatalf("precedence error = %#v", err)
			}
		})
	}
}

func TestLockSafePointerGrammar(t *testing.T) {
	tests := []struct {
		pointer string
		want    string
	}{
		{pointer: "", want: ""},
		{pointer: "/release/providerId", want: "/release/providerId"},
		{pointer: "/release/providerId/hostile", want: "/release/providerId"},
		{pointer: "/release/a~1b", want: "/release"},
		{pointer: "/release/a~2b", want: "/release"},
		{pointer: "/profileClosure/0", want: "/profileClosure/0"},
		{pointer: "/profileClosure/01", want: "/profileClosure"},
		{pointer: "/trackedFiles/42/path", want: "/trackedFiles/42/path"},
		{pointer: "/trackedFiles/42/credential~1token", want: "/trackedFiles/42"},
		{pointer: "/trackedFiles/00/path", want: "/trackedFiles"},
		{pointer: "/trackedFiles/999999999999999999999999/path", want: "/trackedFiles"},
		{pointer: "/absolute~1credential", want: ""},
		{pointer: "not-a-pointer", want: ""},
	}
	for _, test := range tests {
		if got := safeLockDocumentPointer(test.pointer); got != test.want {
			t.Fatalf("safeLockDocumentPointer(%q) = %q, want %q", test.pointer, got, test.want)
		}
	}
}

func TestVerifyClosedBoundaryAndDigestPrecedence(t *testing.T) {
	ref, resolved := internalLockRelease(t)
	verified, err := Derive(ref, resolved, "default", "services/sample", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := verified.Snapshot()
	invalidLimits := DefaultLimits()
	invalidLimits.MaxDocumentBytes = 0
	tests := []struct {
		name    string
		snap    Snapshot
		key     Key
		owner   release.ResolvedRelease
		limits  Limits
		class   ErrorClass
		code    string
		reason  string
		pointer string
	}{
		{"snapshot before key", Snapshot{}, Key{}, release.ResolvedRelease{}, invalidLimits, ErrLockInput, "source_lock_invalid", "document_invalid", ""},
		{"key before owner", snapshot, Key{}, release.ResolvedRelease{}, invalidLimits, ErrLockInput, "source_lock_invalid", "key_provider_invalid", "/key"},
		{"owner before limits", snapshot, verified.Key(), release.ResolvedRelease{}, invalidLimits, ErrLockInput, "source_lock_invalid", "document_invalid", "/release"},
		{"limits after owner", snapshot, verified.Key(), resolved, invalidLimits, ErrLockInput, "source_lock_invalid", "lock_limit_invalid", "/limits/maxDocumentBytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Verify(test.snap, test.key, test.owner, test.limits)
			assertInternalLockError(t, err, test.class, test.code, test.reason, test.pointer, StageVerify)
		})
	}

	t.Run("digest after semantic owner comparison", func(t *testing.T) {
		tampered := snapshot
		tampered.digest = provenance.SHA256([]byte("tampered lock digest"))
		_, err := Verify(tampered, verified.Key(), resolved, DefaultLimits())
		assertInternalLockError(t, err, ErrLockConflict, "source_lock_conflict", "lock_digest_mismatch", "", StageVerify)

		compound := tampered
		compound.profileClosure = []string{"other", "default"}
		_, err = Verify(compound, verified.Key(), resolved, DefaultLimits())
		assertInternalLockError(t, err, ErrLockConflict, "source_lock_conflict", "profile_closure_mismatch", "/profileClosure", StageVerify)
	})
}

func internalLockRelease(t *testing.T) (release.Ref, release.ResolvedRelease) {
	t.Helper()
	data := []byte("owner")
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: "sample.internal-lock", ModulePath: "example.test/internal-lock", PackagePath: "example.test/internal-lock/source", Version: "v0.1.0"},
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
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := release.NewExactResolver(nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return ref, resolved
}

func assertInternalLockError(t *testing.T, err error, class ErrorClass, code, reason, pointer string, stage Stage) *Error {
	t.Helper()
	var projected *Error
	if !errors.As(err, &projected) || projected.Class() != class || projected.Code() != code || projected.Reason() != reason ||
		projected.Pointer() != pointer || projected.Stage() != stage || projected.Error() != class.Error() ||
		projected.Source() != "" || projected.Line() != 0 || projected.Column() != 0 || !errors.Is(projected, class) || errors.Unwrap(projected) != nil {
		t.Fatalf("error = %#v", err)
	}
	return projected
}

package lock

import (
	"errors"
	"sort"
	"strconv"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/internal/contract"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func Derive(expected release.Ref, resolved release.ResolvedRelease, profileID, target string, limits Limits) (VerifiedLock, error) {
	if pointer := validateLimits(limits); pointer != "" {
		return VerifiedLock{}, lockError(ErrLockInput, "source_lock_invalid", "lock_limit_invalid", pointer, StageDerive)
	}
	validatedExpected, expectedErr := validateReleaseRef(expected, StageDerive)
	if expectedErr != nil {
		return VerifiedLock{}, expectedErr
	}
	actual, resolvedErr := validateResolved(resolved, StageDerive)
	if resolvedErr != nil {
		return VerifiedLock{}, resolvedErr
	}
	if !validatedExpected.Equal(actual) {
		return VerifiedLock{}, lockError(ErrLockConflict, "source_lock_conflict", "release_mismatch", refMismatchPointer(validatedExpected, actual), StageDerive)
	}
	if !contract.ValidStableID(profileID) {
		return VerifiedLock{}, lockError(ErrLockInput, "source_lock_invalid", "profile_id_invalid", "/profileId", StageDerive)
	}
	if len(target) > limits.MaxTargetBytes {
		return VerifiedLock{}, lockError(ErrLockInput, "source_lock_invalid", "key_target_invalid", "/target", StageDerive)
	}
	if err := projectPathIssue(contract.ValidatePortablePath(target), "key_target_invalid", "/target", StageDerive); err != nil {
		return VerifiedLock{}, err
	}
	closure, err := resolved.Manifest().ResolveProfile(profileID)
	if err != nil {
		var owner *sourceplugin.Error
		if errors.As(err, &owner) && owner.Reason() == "profile_not_found" {
			return VerifiedLock{}, lockError(ErrLockInput, "source_lock_invalid", "profile_not_found", "/profileId", StageDerive)
		}
		return VerifiedLock{}, lockError(ErrLockInternal, "source_lock_internal", "owner_projection_failed", "/profileId", StageDerive)
	}
	profileClosure := closure.ProfileIDs()
	if len(profileClosure) == 0 || len(profileClosure) > limits.MaxProfileClosure {
		return VerifiedLock{}, lockError(ErrLockInput, "source_lock_invalid", "profile_closure_invalid", "/profileClosure", StageDerive)
	}
	ownerFiles := closure.Files()
	sort.Slice(ownerFiles, func(i, j int) bool { return ownerFiles[i].Path() < ownerFiles[j].Path() })
	if len(ownerFiles) > limits.MaxTrackedFiles {
		return VerifiedLock{}, lockError(ErrLockInput, "source_lock_invalid", "tracked_file_count_exceeded", "/trackedFiles", StageDerive)
	}
	baseline := make([]BaselineFile, len(ownerFiles))
	for index, file := range ownerFiles {
		treeFile, ok := resolved.Tree().Lookup(file.Path())
		if !ok {
			return VerifiedLock{}, lockError(ErrLockInternal, "source_lock_internal", "owner_projection_failed", "/trackedFiles/"+strconv.Itoa(index), StageDerive)
		}
		baseline[index] = BaselineFile{path: treeFile.Path(), mode: treeFile.Mode(), size: treeFile.Size(), digest: treeFile.Digest()}
	}
	snapshot, snapshotErr := newSnapshot(expected, profileID, profileClosure, target, baseline, "", limits, StageDerive)
	if snapshotErr != nil {
		return VerifiedLock{}, snapshotErr
	}
	return VerifiedLock{snapshot: snapshot, valid: true}, nil
}

func Verify(snapshot Snapshot, expectedKey Key, resolved release.ResolvedRelease, limits Limits) (VerifiedLock, error) {
	if !snapshot.valid {
		return VerifiedLock{}, lockError(ErrLockInput, "source_lock_invalid", "document_invalid", "", StageVerify)
	}
	if !expectedKey.valid {
		return VerifiedLock{}, lockError(ErrLockInput, "source_lock_invalid", "key_provider_invalid", "/key", StageVerify)
	}
	actual, resolvedErr := validateResolved(resolved, StageVerify)
	if resolvedErr != nil {
		return VerifiedLock{}, resolvedErr
	}
	if pointer := validateLimits(limits); pointer != "" {
		return VerifiedLock{}, lockError(ErrLockInput, "source_lock_invalid", "lock_limit_invalid", pointer, StageVerify)
	}
	if pointer := firstInvalidTrackedSize(snapshot.trackedFiles); pointer != "" {
		return VerifiedLock{}, lockError(ErrLockInput, "source_lock_invalid", "tracked_file_size_invalid", pointer, StageVerify)
	}
	if !snapshot.key.Equal(expectedKey) {
		return VerifiedLock{}, lockError(ErrLockConflict, "source_lock_conflict", "key_mismatch", "/key", StageVerify)
	}
	if !snapshot.release.Equal(actual) {
		return VerifiedLock{}, lockError(ErrLockConflict, "source_lock_conflict", "release_mismatch", refMismatchPointer(snapshot.release, actual), StageVerify)
	}
	derived, err := Derive(snapshot.release, resolved, snapshot.profileID, snapshot.target, limits)
	if err != nil {
		var projected *Error
		if errors.As(err, &projected) {
			copyError := *projected
			copyError.stage = StageVerify
			return VerifiedLock{}, &copyError
		}
		return VerifiedLock{}, lockError(ErrLockInternal, "source_lock_internal", "owner_projection_failed", "", StageVerify)
	}
	want := derived.snapshot
	if !equalStrings(snapshot.profileClosure, want.profileClosure) {
		return VerifiedLock{}, lockError(ErrLockConflict, "source_lock_conflict", "profile_closure_mismatch", "/profileClosure", StageVerify)
	}
	snapshotByPath := baselineByPath(snapshot.trackedFiles)
	wantByPath := baselineByPath(want.trackedFiles)
	for index, file := range want.trackedFiles {
		if _, ok := snapshotByPath[file.path]; !ok {
			return VerifiedLock{}, lockError(ErrLockConflict, "source_lock_conflict", "baseline_file_missing", "/trackedFiles/"+strconv.Itoa(index), StageVerify)
		}
	}
	for index, file := range snapshot.trackedFiles {
		if _, ok := wantByPath[file.path]; !ok {
			return VerifiedLock{}, lockError(ErrLockConflict, "source_lock_conflict", "baseline_file_extra", "/trackedFiles/"+strconv.Itoa(index), StageVerify)
		}
	}
	for index, file := range want.trackedFiles {
		candidate := snapshotByPath[file.path]
		if candidate.mode != file.mode {
			return VerifiedLock{}, lockError(ErrLockConflict, "source_lock_conflict", "baseline_file_mode_mismatch", "/trackedFiles/"+strconv.Itoa(index)+"/mode", StageVerify)
		}
	}
	for index, file := range want.trackedFiles {
		candidate := snapshotByPath[file.path]
		if candidate.size != file.size {
			return VerifiedLock{}, lockError(ErrLockConflict, "source_lock_conflict", "baseline_file_size_mismatch", "/trackedFiles/"+strconv.Itoa(index)+"/size", StageVerify)
		}
	}
	for index, file := range want.trackedFiles {
		candidate := snapshotByPath[file.path]
		if candidate.digest != file.digest {
			return VerifiedLock{}, lockError(ErrLockConflict, "source_lock_conflict", "baseline_file_digest_mismatch", "/trackedFiles/"+strconv.Itoa(index)+"/digest", StageVerify)
		}
	}
	if snapshot.digest != want.digest {
		return VerifiedLock{}, lockError(ErrLockConflict, "source_lock_conflict", "lock_digest_mismatch", "", StageVerify)
	}
	return VerifiedLock{snapshot: cloneSnapshot(snapshot), valid: true}, nil
}

func validateReleaseRef(ref release.Ref, stage Stage) (release.Ref, *Error) {
	if issue := contract.ValidateIdentity(ref.ProviderID(), ref.ModulePath(), ref.PackagePath(), ref.Version()); issue != nil {
		return release.Ref{}, projectIdentityIssue(issue, stage, "/release")
	}
	if _, err := provenance.ParseDigest(ref.ManifestDigest().String()); err != nil {
		return release.Ref{}, lockError(ErrLockInput, "source_lock_invalid", "manifest_digest_invalid", "/release/manifestDigest", stage)
	}
	if _, err := provenance.ParseDigest(ref.TreeDigest().String()); err != nil {
		return release.Ref{}, lockError(ErrLockInput, "source_lock_invalid", "tree_digest_invalid", "/release/treeDigest", stage)
	}
	validated, err := release.NewRef(release.RefSpec{
		ProviderID: ref.ProviderID(), ModulePath: ref.ModulePath(), PackagePath: ref.PackagePath(), Version: ref.Version(),
		ManifestDigest: ref.ManifestDigest(), TreeDigest: ref.TreeDigest(),
	})
	if err != nil {
		return release.Ref{}, lockError(ErrLockInternal, "source_lock_internal", "owner_projection_failed", "/release", stage)
	}
	return validated, nil
}

func validateResolved(resolved release.ResolvedRelease, stage Stage) (release.Ref, *Error) {
	provider := resolved.Provider()
	actual, err := release.FromProvider(provider)
	if err != nil {
		return release.Ref{}, lockError(ErrLockInput, "source_lock_invalid", "document_invalid", "/release", stage)
	}
	if !actual.Equal(resolved.Ref()) || resolved.Manifest().Digest() != actual.ManifestDigest() || resolved.Tree().Digest() != actual.TreeDigest() {
		return release.Ref{}, lockError(ErrLockConflict, "source_lock_conflict", "release_mismatch", refMismatchPointer(resolved.Ref(), actual), stage)
	}
	return actual, nil
}

func refMismatchPointer(expected, actual release.Ref) string {
	switch {
	case expected.ProviderID() != actual.ProviderID():
		return "/release/providerId"
	case expected.ModulePath() != actual.ModulePath():
		return "/release/modulePath"
	case expected.PackagePath() != actual.PackagePath():
		return "/release/packagePath"
	case expected.Version() != actual.Version():
		return "/release/version"
	case expected.ManifestDigest() != actual.ManifestDigest():
		return "/release/manifestDigest"
	default:
		return "/release/treeDigest"
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func baselineByPath(files []BaselineFile) map[string]BaselineFile {
	result := make(map[string]BaselineFile, len(files))
	for _, file := range files {
		result[file.path] = file
	}
	return result
}

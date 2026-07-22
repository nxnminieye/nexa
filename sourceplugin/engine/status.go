package engine

import (
	"context"
	"errors"
	"os"
	"path"
	"sort"

	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

type managedBaseline struct {
	verified lock.VerifiedLock
	resolved release.ResolvedRelease
}

func (e *Engine) Status(ctx context.Context, request ManagedRequest) (Status, error) {
	if err := validateContext(ctx, "status"); err != nil {
		return Status{}, err
	}
	root, err := validateRepositoryRoot(request.RepositoryRoot)
	if err != nil {
		return Status{}, err
	}
	baseline, exists, err := e.loadManaged(ctx, root, request.Key)
	if err != nil {
		return Status{}, err
	}
	if !exists {
		snapshot, snapshotErr := scanTarget(root, request.Key.Target(), e.treeLimits)
		if snapshotErr != nil {
			return Status{}, snapshotErr
		}
		return Status{state: ManagedStateNotManaged, snapshotDigest: snapshot.digest}, nil
	}
	snapshot, err := scanTarget(root, baseline.verified.Target(), e.treeLimits)
	if err != nil {
		return Status{}, err
	}
	deltas := compareBaseline(baseline.verified, snapshot)
	state := ManagedStateClean
	if len(deltas) > 0 {
		state = ManagedStateModified
	}
	return Status{state: state, deltas: deltas, snapshotDigest: snapshot.digest}, nil
}

func (e *Engine) loadManaged(ctx context.Context, root string, key lock.Key) (managedBaseline, bool, error) {
	if key.RepositoryPath() == "" {
		return managedBaseline{}, false, newError(ErrInput, "source_request_invalid", "key_required", "/key", "managed-read")
	}
	lockPath, err := safeJoin(root, key.RepositoryPath())
	if err != nil {
		return managedBaseline{}, false, err
	}
	info, statErr := os.Lstat(lockPath)
	if os.IsNotExist(statErr) {
		return managedBaseline{}, false, nil
	}
	if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > e.lockLimits.MaxDocumentBytes {
		return managedBaseline{}, false, newError(ErrInput, "source_lock_invalid", "lock_file_invalid", "/lock", "managed-read")
	}
	data, readErr := os.ReadFile(lockPath)
	if readErr != nil || int64(len(data)) != info.Size() {
		return managedBaseline{}, false, newError(ErrInput, "source_lock_invalid", "lock_read_failed", "/lock", "managed-read")
	}
	snapshot, parseErr := lock.Parse(key.RepositoryPath(), data, e.lockLimits)
	if parseErr != nil {
		return managedBaseline{}, false, projectOwnerError(parseErr, "managed-read")
	}
	resolved, resolveErr := e.resolver.Resolve(ctx, snapshot.Release())
	if resolveErr != nil {
		return managedBaseline{}, false, projectOwnerError(resolveErr, "managed-read")
	}
	verified, verifyErr := lock.Verify(snapshot, key, resolved, e.lockLimits)
	if verifyErr != nil {
		return managedBaseline{}, false, projectOwnerError(verifyErr, "managed-read")
	}
	closure, closureErr := resolved.Manifest().ResolveProfile(verified.ProfileID())
	if closureErr != nil {
		return managedBaseline{}, false, projectOwnerError(closureErr, "managed-read")
	}
	if err := e.resolveRequirements(ctx, closure); err != nil {
		return managedBaseline{}, false, err
	}
	return managedBaseline{verified: verified, resolved: resolved}, true, nil
}

func (e *Engine) resolveRequirements(ctx context.Context, closure sourceplugin.ProfileClosure) error {
	for _, requirement := range closure.BundleRequirements() {
		ref, err := release.NewRef(release.RefSpec{
			ProviderID: requirement.ProviderID(), ModulePath: requirement.ModulePath(), PackagePath: requirement.PackagePath(), Version: requirement.Version(),
			ManifestDigest: requirement.ManifestDigest(), TreeDigest: requirement.TreeDigest(),
		})
		if err != nil {
			return projectOwnerError(err, "requirements")
		}
		resolved, err := e.resolver.Resolve(ctx, ref)
		if err != nil {
			return projectOwnerError(err, "requirements")
		}
		if _, err := resolved.Manifest().ResolveProfile(requirement.ProfileID()); err != nil {
			return projectOwnerError(err, "requirements")
		}
	}
	return nil
}

func compareBaseline(verified lock.VerifiedLock, snapshot repositorySnapshot) []Delta {
	baseline := make(map[string]FileState)
	for _, file := range verified.TrackedFiles() {
		baseline[file.Path()] = baselineState(file.Mode(), file.Size(), file.Digest())
		addParentDirectories(baseline, file.Path())
	}
	paths := make([]string, 0, len(baseline)+len(snapshot.files))
	seen := make(map[string]bool)
	for path := range baseline {
		paths = append(paths, path)
		seen[path] = true
	}
	for path := range snapshot.files {
		if !seen[path] {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	deltas := make([]Delta, 0)
	for _, path := range paths {
		old, oldOK := baseline[path]
		local, localOK := snapshot.files[path]
		if !oldOK {
			deltas = append(deltas, Delta{path: path, kind: DeltaAdded, before: FileState{typeOf: FileAbsent}, after: cloneFileState(local)})
			continue
		}
		if !localOK {
			deltas = append(deltas, Delta{path: path, kind: DeltaDeleted, before: old, after: FileState{typeOf: FileAbsent}})
			continue
		}
		kind := DeltaKind("")
		switch {
		case local.typeOf != old.typeOf:
			kind = DeltaTypeChanged
		case old.typeOf == FileRegular && local.digest != old.digest:
			kind = DeltaModified
		case local.mode != old.mode:
			kind = DeltaModeChanged
		}
		if kind != "" {
			deltas = append(deltas, Delta{path: path, kind: kind, before: old, after: cloneFileState(local)})
		}
	}
	return deltas
}

func addParentDirectories(files map[string]FileState, filePath string) {
	for parent := path.Dir(filePath); parent != "."; parent = path.Dir(parent) {
		if _, exists := files[parent]; !exists {
			files[parent] = FileState{typeOf: FileDirectory, mode: 0o755}
		}
	}
}

func projectOwnerError(err error, stage string) error {
	var lockErr *lock.Error
	if errors.As(err, &lockErr) {
		class := ErrInput
		if lockErr.Class() == lock.ErrLockConflict {
			class = ErrConflict
		}
		if lockErr.Class() == lock.ErrLockInternal {
			class = ErrInternal
		}
		return newErrorWithCause(class, lockErr.Code(), lockErr.Reason(), lockErr.Pointer(), stage, err)
	}
	var releaseErr *release.Error
	if errors.As(err, &releaseErr) {
		class := ErrInput
		switch releaseErr.Class() {
		case release.ErrReleaseUnavailable:
			class = ErrUnavailable
		case release.ErrReleaseConflict:
			class = ErrConflict
		case release.ErrReleaseInternal:
			class = ErrInternal
		}
		return newErrorWithCause(class, releaseErr.Code(), releaseErr.Reason(), releaseErr.Pointer(), stage, err)
	}
	var sourceErr *sourceplugin.Error
	if errors.As(err, &sourceErr) {
		return newErrorWithCause(ErrInput, sourceErr.Code(), sourceErr.Reason(), sourceErr.Pointer(), stage, err)
	}
	return newErrorWithCause(ErrInternal, "source_engine_internal", "owner_error_unmapped", "", stage, err)
}

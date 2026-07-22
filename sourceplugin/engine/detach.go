package engine

import (
	"context"
	"errors"
	"os"

	"github.com/nxnminieye/nexa/sourceplugin/lock"
)

type DetachRequest struct{ ManagedRequest }

func (e *Engine) Detach(ctx context.Context, request DetachRequest) (Result, error) {
	if err := validateContext(ctx, "detach"); err != nil {
		return Result{}, err
	}
	root, err := validateRepositoryRoot(request.RepositoryRoot)
	if err != nil {
		return Result{}, err
	}
	if request.Key.RepositoryPath() == "" {
		return Result{}, newError(ErrInput, "source_request_invalid", "key_required", "/key", "detach")
	}
	snapshot, err := loadDetachSnapshot(root, request.Key, e.lockLimits)
	if err != nil {
		return Result{}, err
	}
	if err := e.applyDetachPublish(ctx, &repository{root: root}, snapshot); err != nil {
		return Result{}, err
	}
	status, err := e.Status(ctx, request.ManagedRequest)
	if err != nil {
		return Result{}, err
	}
	return Result{operation: sourceDetach, status: status, lockDigest: snapshot.Digest()}, nil
}

func loadDetachSnapshot(root string, key lock.Key, limits lock.Limits) (lock.Snapshot, error) {
	absolute, err := safeJoin(root, key.RepositoryPath())
	if err != nil {
		return lock.Snapshot{}, err
	}
	info, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		return lock.Snapshot{}, newError(ErrNotManaged, "source_not_managed", "lock_missing", "/key", "detach")
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > limits.MaxDocumentBytes {
		return lock.Snapshot{}, newError(ErrInput, "source_lock_invalid", "lock_file_invalid", "/lock", "detach")
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return lock.Snapshot{}, newError(ErrInput, "source_lock_invalid", "lock_read_failed", "/lock", "detach")
	}
	snapshot, err := lock.Parse(key.RepositoryPath(), data, limits)
	if err != nil {
		return lock.Snapshot{}, projectOwnerError(err, "detach")
	}
	if !snapshot.Key().Equal(key) {
		return lock.Snapshot{}, newError(ErrConflict, "source_lock_conflict", "key_mismatch", "/key", "detach")
	}
	return snapshot, nil
}

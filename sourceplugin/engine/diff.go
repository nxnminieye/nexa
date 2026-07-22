package engine

import "context"

func (e *Engine) Diff(ctx context.Context, request ManagedRequest) (Diff, error) {
	status, err := e.Status(ctx, request)
	if err != nil {
		return Diff{}, err
	}
	if status.state == ManagedStateNotManaged {
		return Diff{}, newError(ErrNotManaged, "source_not_managed", "lock_missing", "/key", "diff")
	}
	return Diff{deltas: cloneDeltas(status.deltas), snapshotDigest: status.snapshotDigest}, nil
}

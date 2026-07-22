package engine

import (
	"context"

	"github.com/nxnminieye/nexa/sourceplugin/lock"
)

type Check struct {
	status Status
	plan   Plan
}

func (c Check) Status() Status {
	return Status{state: c.status.state, deltas: cloneDeltas(c.status.deltas), snapshotDigest: c.status.snapshotDigest}
}
func (c Check) Plan() Plan     { return c.plan }
func (c Check) CanApply() bool { return c.plan.CanApply() }

func (e *Engine) Check(ctx context.Context, request PlanRequest) (Check, error) {
	if !request.Selection.valid {
		return Check{}, newError(ErrInput, "source_request_invalid", "selection_required", "/selection", "check")
	}
	key, err := lock.NewKey(request.Selection.release.ProviderID(), request.Selection.target)
	if err != nil {
		return Check{}, projectOwnerError(err, "check")
	}
	status, err := e.Status(ctx, ManagedRequest{RepositoryRoot: request.RepositoryRoot, Key: key})
	if err != nil {
		return Check{}, err
	}
	plan, err := e.Plan(ctx, request)
	if err != nil {
		return Check{}, err
	}
	return Check{status: status, plan: plan}, nil
}

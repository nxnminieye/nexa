package engine

import (
	"context"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
)

type WriteOptions struct{ ExpectedPlanDigest provenance.Digest }

type MaterializeRequest struct {
	PlanRequest
	WriteOptions WriteOptions
}

type Result struct {
	operation  PlanOperation
	status     Status
	planDigest provenance.Digest
	lockDigest provenance.Digest
}

func (r Result) Operation() PlanOperation { return r.operation }
func (r Result) Status() Status {
	return Status{state: r.status.state, deltas: cloneDeltas(r.status.deltas), snapshotDigest: r.status.snapshotDigest}
}
func (r Result) PlanDigest() provenance.Digest { return r.planDigest }
func (r Result) LockDigest() provenance.Digest { return r.lockDigest }

func (e *Engine) Materialize(ctx context.Context, request MaterializeRequest) (Result, error) {
	return e.executeLifecycle(ctx, request.PlanRequest, request.WriteOptions, PlanMaterialize)
}

func (e *Engine) executeLifecycle(ctx context.Context, request PlanRequest, writeOptions WriteOptions, command PlanOperation) (Result, error) {
	if err := validateContext(ctx, string(command)); err != nil {
		return Result{}, err
	}
	root, err := validateRepositoryRoot(request.RepositoryRoot)
	if err != nil {
		return Result{}, err
	}
	plan, err := e.Plan(ctx, request)
	if err != nil {
		return Result{}, err
	}
	if validDigest(writeOptions.ExpectedPlanDigest) && writeOptions.ExpectedPlanDigest != plan.digest {
		return Result{}, newError(ErrConflict, "source_transaction_conflict", "plan_digest_mismatch", "/expectedPlanDigest", string(command))
	}
	if writeOptions.ExpectedPlanDigest.String() != "" && !validDigest(writeOptions.ExpectedPlanDigest) {
		return Result{}, newError(ErrInput, "source_request_invalid", "plan_digest_invalid", "/expectedPlanDigest", string(command))
	}
	if !plan.CanApply() {
		return Result{}, newError(ErrConflict, "source_transaction_conflict", "plan_conflict", "/plan/conflicts", string(command))
	}
	switch command {
	case PlanMaterialize:
		if plan.operation == PlanUpgrade {
			return Result{}, newError(ErrInput, "source_transaction_invalid", "upgrade_required", "/selection", "materialize")
		}
	case PlanUpgrade:
		if plan.operation == PlanMaterialize {
			return Result{}, newError(ErrNotManaged, "source_not_managed", "lock_missing", "/selection", "upgrade")
		}
	}
	key, err := lock.NewKey(request.Selection.release.ProviderID(), request.Selection.target)
	if err != nil {
		return Result{}, projectOwnerError(err, string(command))
	}
	if plan.operation == PlanNoop {
		baseline, exists, err := e.loadManaged(ctx, root, key)
		if err != nil {
			return Result{}, err
		}
		if !exists {
			return Result{}, newError(ErrNotManaged, "source_not_managed", "lock_missing", "/selection", string(command))
		}
		status, err := e.Status(ctx, ManagedRequest{RepositoryRoot: root, Key: key})
		if err != nil {
			return Result{}, err
		}
		return Result{operation: PlanNoop, status: status, planDigest: plan.digest, lockDigest: baseline.verified.Digest()}, nil
	}
	resolved, err := e.resolver.Resolve(ctx, request.Selection.release)
	if err != nil {
		return Result{}, projectOwnerError(err, string(command))
	}
	closure, err := resolved.Manifest().ResolveProfile(request.Selection.profileID)
	if err != nil {
		return Result{}, projectOwnerError(err, string(command))
	}
	verified, err := lock.Derive(request.Selection.release, resolved, request.Selection.profileID, request.Selection.target, e.lockLimits)
	if err != nil {
		return Result{}, projectOwnerError(err, string(command))
	}
	prepared, err := e.preparePlanPublish(&repository{root: root}, plan, verified)
	if err != nil {
		return Result{}, err
	}
	defer prepared.cleanup()
	recipes := closure.Validations()
	if len(recipes) > 0 || len(closure.GoModuleRequirements()) > 0 {
		if err := prepareValidationModuleContext(root, request.Selection, closure, &prepared); err != nil {
			return Result{}, err
		}
	}
	if len(recipes) > 0 {
		if e.executor == nil {
			return Result{}, validationError(ErrInput, "source_validation_invalid", "executor_required", "/executor")
		}
		if err := validateGoToolchain(e.goToolchain); err != nil {
			return Result{}, err
		}
		if err := validatePreview(ctx, e.executor, e.goToolchain, prepared.previewRoot, recipes); err != nil {
			return Result{}, err
		}
	}
	if cache, ok := e.resolver.Cache(); ok {
		if err := cache.Store(ctx, resolved); err != nil {
			return Result{}, projectOwnerError(err, string(command))
		}
	}
	if err := e.applyPreparedPlan(ctx, &repository{root: root}, plan, verified, prepared); err != nil {
		return Result{}, err
	}
	status, err := e.Status(ctx, ManagedRequest{RepositoryRoot: root, Key: key})
	if err != nil {
		return Result{}, err
	}
	return Result{operation: plan.operation, status: status, planDigest: plan.digest, lockDigest: verified.Digest()}, nil
}

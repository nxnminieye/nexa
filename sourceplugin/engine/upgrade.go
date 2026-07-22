package engine

import "context"

type UpgradeRequest struct {
	PlanRequest
	WriteOptions WriteOptions
}

func (e *Engine) Upgrade(ctx context.Context, request UpgradeRequest) (Result, error) {
	return e.executeLifecycle(ctx, request.PlanRequest, request.WriteOptions, PlanUpgrade)
}

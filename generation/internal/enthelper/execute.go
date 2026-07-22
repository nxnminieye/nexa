package enthelper

import (
	"context"

	"github.com/nxnminieye/nexa/generation/internal/crudbuild"
	"github.com/nxnminieye/nexa/generation/internal/entipc"
	"github.com/nxnminieye/nexa/generation/internal/entityload"
)

func Execute(ctx context.Context, stdin []byte) ([]byte, error) {
	request, err := entipc.ParseRequest(entipc.HelperRequestSource(), stdin)
	if err != nil {
		return nil, err
	}
	entitySpec, err := request.EntitySpec()
	if err != nil {
		return nil, err
	}
	document, err := entityload.LoadCurrentProcess(ctx, entitySpec)
	if err != nil {
		return encodeDomain(request, err)
	}
	buildSpec, err := request.BuildSpec()
	if err != nil {
		return nil, err
	}
	plan, err := crudbuild.BuildPlan(document, buildSpec)
	if err != nil {
		return encodeDomain(request, err)
	}
	result, err := entipc.ResultFromPlan(plan)
	if err != nil {
		return nil, err
	}
	return entipc.EncodeResult(request, result)
}

func encodeDomain(request entipc.Request, ownerErr error) ([]byte, error) {
	result, recognized, err := entipc.ResultFromDomainError(ownerErr)
	if err != nil {
		return nil, err
	}
	if !recognized {
		return nil, ownerErr
	}
	return entipc.EncodeResult(request, result)
}

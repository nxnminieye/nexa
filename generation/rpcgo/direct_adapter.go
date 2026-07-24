package rpcgo

import (
	"context"
	"errors"

	"github.com/nxnminieye/nexa/generation/directwrite"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
)

type DirectRequest = RPCGoRequest
type DirectResult = RPCGoResult

func NewDirectRequest(serviceID string, graph toolchain.ModuleGraph, document protocol.Document, scopes []directwrite.OutputScope) (DirectRequest, error) {
	moduleIdentity, err := graph.ConsumerModule()
	if err != nil {
		return DirectRequest{}, err
	}
	if document.ServiceID() != serviceID {
		return DirectRequest{}, errors.New("RPC direct service identity is invalid")
	}
	canonical, err := protocol.CanonicalJSON(document)
	if err != nil {
		return DirectRequest{}, err
	}
	source, _ := provenance.ParseDomainSource("nexa/tool/rpc-go-request/protocol-ir.json")
	snapshot, err := protocol.ParseSnapshot(source, canonical)
	if err != nil {
		return DirectRequest{}, err
	}
	request := DirectRequest{APIVersion: RPCGoRequestAPIVersion, Kind: RPCGoRequestKind, ServiceID: serviceID, ModulePath: moduleIdentity.Path, ProtocolIR: snapshot, OutputScopes: scopes}
	if _, err := CanonicalRPCGoRequest(request); err != nil {
		return DirectRequest{}, err
	}
	return request, nil
}

func WriteDirect(ctx context.Context, request DirectRequest, options DirectOptions) (DirectResult, error) {
	result, err := RunDirectRPCGo(ctx, request, options)
	if err == nil {
		return result, nil
	}
	postLaunch := false
	var toolError *toolchain.Error
	if errors.As(err, &toolError) {
		postLaunch = toolError.Started() || toolError.MayHaveWritten()
	}
	return DirectResult{}, directFailure("invoke-tool", "tool_failed", options, err, postLaunch)
}

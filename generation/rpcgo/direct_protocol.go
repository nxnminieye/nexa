package rpcgo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/directwrite"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/module"
)

const (
	RPCGoRequestAPIVersion = "nexa.dev/rpc-go-request/v2"
	RPCGoRequestKind       = "RPCGoRequest"
	RPCGoResultAPIVersion  = "nexa.dev/rpc-go-result/v2"
	RPCGoResultKind        = "RPCGoResult"
	RPCGoResultGenerated   = "generated"
)

// RPCGoRequest is the exact typed direct RPC generator input.
type RPCGoRequest struct {
	APIVersion   string
	Kind         string
	ServiceID    string
	ModulePath   string
	ProtocolIR   protocol.Snapshot
	OutputScopes []directwrite.OutputScope
}

// RPCGoResult is the exact typed direct RPC generator result.
type RPCGoResult struct {
	APIVersion   string                    `json:"apiVersion"`
	Kind         string                    `json:"kind"`
	Status       string                    `json:"status"`
	ServiceID    string                    `json:"serviceId"`
	InputDigest  provenance.Digest         `json:"inputDigest"`
	OutputScopes []directwrite.OutputScope `json:"outputScopes"`
}

type rpcGoResultWire struct {
	APIVersion   string                    `json:"apiVersion"`
	Kind         string                    `json:"kind"`
	Status       string                    `json:"status"`
	ServiceID    string                    `json:"serviceId"`
	InputDigest  string                    `json:"inputDigest"`
	OutputScopes []directwrite.OutputScope `json:"outputScopes"`
}

type rpcGoRequestWire struct {
	APIVersion   string                    `json:"apiVersion"`
	Kind         string                    `json:"kind"`
	ServiceID    string                    `json:"serviceId"`
	ModulePath   string                    `json:"modulePath"`
	ProtocolIR   json.RawMessage           `json:"protocolIR"`
	OutputScopes []directwrite.OutputScope `json:"outputScopes"`
}

func CanonicalRPCGoRequest(input RPCGoRequest) ([]byte, error) {
	if input.APIVersion != RPCGoRequestAPIVersion || input.Kind != RPCGoRequestKind || !serviceIDPattern.MatchString(input.ServiceID) || module.CheckPath(input.ModulePath) != nil {
		return nil, errors.New("RPC Go request identity or module is invalid")
	}
	ir, err := input.ProtocolIR.CanonicalJSON()
	if err != nil {
		return nil, fmt.Errorf("RPC Go request ProtocolIR is invalid: %w", err)
	}
	var protocolIdentity struct {
		ServiceID string `json:"serviceId"`
	}
	if err := json.Unmarshal(ir, &protocolIdentity); err != nil || protocolIdentity.ServiceID != input.ServiceID {
		return nil, errors.New("RPC Go request service does not match ProtocolIR")
	}
	scopes, err := toolchain.NormalizeOutputScopes(input.OutputScopes)
	if err != nil {
		return nil, fmt.Errorf("RPC Go request output scopes are invalid: %w", err)
	}
	wire := rpcGoRequestWire{APIVersion: input.APIVersion, Kind: input.Kind, ServiceID: input.ServiceID, ModulePath: input.ModulePath, ProtocolIR: ir, OutputScopes: scopes}
	if err := validateRPCGoRequestSchema(wire); err != nil {
		return nil, fmt.Errorf("RPC Go request does not match schema: %w", err)
	}
	return canonicalRPCJSON(wire)
}

func ParseRPCGoRequest(data []byte) (RPCGoRequest, error) {
	var wire rpcGoRequestWire
	if err := strictRPCJSON(data, &wire); err != nil {
		return RPCGoRequest{}, fmt.Errorf("RPC Go request is invalid: %w", err)
	}
	if err := validateRPCGoRequestSchema(wire); err != nil {
		return RPCGoRequest{}, fmt.Errorf("RPC Go request does not match schema: %w", err)
	}
	source, _ := provenance.ParseDomainSource("nexa/tool/rpc-go-request/protocol-ir.json")
	ir, err := protocol.ParseSnapshot(source, wire.ProtocolIR)
	if err != nil {
		return RPCGoRequest{}, fmt.Errorf("RPC Go request ProtocolIR is invalid: %w", err)
	}
	result := RPCGoRequest{APIVersion: wire.APIVersion, Kind: wire.Kind, ServiceID: wire.ServiceID, ModulePath: wire.ModulePath, ProtocolIR: ir, OutputScopes: wire.OutputScopes}
	canonical, err := CanonicalRPCGoRequest(result)
	if err != nil {
		return RPCGoRequest{}, err
	}
	if !bytes.Equal(data, canonical) {
		return RPCGoRequest{}, errors.New("RPC Go request is not canonical")
	}
	result.OutputScopes, _ = toolchain.NormalizeOutputScopes(result.OutputScopes)
	return result, nil
}

func CanonicalRPCGoResult(input RPCGoResult) ([]byte, error) {
	if input.APIVersion != RPCGoResultAPIVersion || input.Kind != RPCGoResultKind || input.Status != RPCGoResultGenerated || !serviceIDPattern.MatchString(input.ServiceID) {
		return nil, errors.New("RPC Go result identity or status is invalid")
	}
	if _, err := provenance.ParseDigest(input.InputDigest.String()); err != nil {
		return nil, errors.New("RPC Go result input digest is invalid")
	}
	scopes, err := toolchain.NormalizeOutputScopes(input.OutputScopes)
	if err != nil {
		return nil, fmt.Errorf("RPC Go result output scopes are invalid: %w", err)
	}
	input.OutputScopes = scopes
	if err := validateRPCGoResultSchema(input); err != nil {
		return nil, fmt.Errorf("RPC Go result does not match schema: %w", err)
	}
	return canonicalRPCJSON(input)
}

func ParseRPCGoResult(data []byte) (RPCGoResult, error) {
	var wire rpcGoResultWire
	if err := strictRPCJSON(data, &wire); err != nil {
		return RPCGoResult{}, fmt.Errorf("RPC Go result is invalid: %w", err)
	}
	if err := validateRPCGoResultSchema(wire); err != nil {
		return RPCGoResult{}, fmt.Errorf("RPC Go result does not match schema: %w", err)
	}
	digest, err := provenance.ParseDigest(wire.InputDigest)
	if err != nil {
		return RPCGoResult{}, errors.New("RPC Go result input digest is invalid")
	}
	result := RPCGoResult{APIVersion: wire.APIVersion, Kind: wire.Kind, Status: wire.Status, ServiceID: wire.ServiceID, InputDigest: digest, OutputScopes: wire.OutputScopes}
	canonical, err := CanonicalRPCGoResult(result)
	if err != nil {
		return RPCGoResult{}, err
	}
	if !bytes.Equal(data, canonical) {
		return RPCGoResult{}, errors.New("RPC Go result is not canonical")
	}
	result.OutputScopes, _ = toolchain.NormalizeOutputScopes(result.OutputScopes)
	return result, nil
}

func strictRPCJSON(data []byte, target any) error {
	if !utf8.Valid(data) {
		return errors.New("JSON is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func canonicalRPCJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return jcs.Transform(encoded)
}

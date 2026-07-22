package host

import (
	"encoding/json"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/nexactl/plugin"
)

type Options struct {
	Name         string
	Version      string
	OperationIDs protocol.OperationIDGenerator
}

type Host struct {
	name         string
	version      string
	operationIDs protocol.OperationIDGenerator
	specs        []plugin.Spec
}

type Inspection struct {
	APIVersion   string                 `json:"apiVersion"`
	Binary       binaryInspection       `json:"binary"`
	GlobalFlags  []plugin.FlagSpec      `json:"globalFlags"`
	Plugins      []pluginInspection     `json:"plugins"`
	Capabilities []capabilityInspection `json:"capabilities"`
	Commands     []commandInspection    `json:"commands"`
}

type binaryInspection struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type pluginInspection struct {
	ID              string              `json:"id"`
	Version         string              `json:"version"`
	ContractVersion string              `json:"contractVersion"`
	Provides        []plugin.Capability `json:"provides,omitempty"`
	Requires        []plugin.Capability `json:"requires,omitempty"`
}

type capabilityInspection struct {
	ID               string `json:"id"`
	Version          string `json:"version"`
	ProviderPluginID string `json:"providerPluginId"`
}

type commandInspection struct {
	Path           []string                   `json:"path"`
	Summary        string                     `json:"summary"`
	Flags          []plugin.FlagSpec          `json:"flags,omitempty"`
	InputSchema    json.RawMessage            `json:"inputSchema"`
	OutputSchema   json.RawMessage            `json:"outputSchema"`
	SideEffect     plugin.SideEffect          `json:"sideEffect"`
	DelegatedTools []plugin.DelegatedToolSpec `json:"delegatedTools,omitempty"`
	OwnerPluginID  string                     `json:"ownerPluginId"`
}

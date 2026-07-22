package plugin

import (
	"context"
	"encoding/json"
)

const ContractVersion = "nexa.dev/nexactl-plugin/v1"

type Capability struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type Descriptor struct {
	ID              string       `json:"id"`
	Version         string       `json:"version"`
	ContractVersion string       `json:"contractVersion"`
	Provides        []Capability `json:"provides,omitempty"`
	Requires        []Capability `json:"requires,omitempty"`
}

type SideEffect string

const (
	SideEffectNone            SideEffect = "none"
	SideEffectRepositoryRead  SideEffect = "repository-read"
	SideEffectRepositoryWrite SideEffect = "repository-write"
)

type FlagType string

const (
	FlagString      FlagType = "string"
	FlagBool        FlagType = "bool"
	FlagInt         FlagType = "int"
	FlagStringSlice FlagType = "string-slice"
)

type FlagSpec struct {
	Name     string          `json:"name"`
	Type     FlagType        `json:"type"`
	Summary  string          `json:"summary"`
	Required bool            `json:"required,omitempty"`
	Default  json.RawMessage `json:"default,omitempty"`
}

type Invocation struct {
	Args  []string
	Flags map[string]any
}

type Handler func(context.Context, Invocation) (any, error)

type DelegatedToolSpec struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	// Inputs and Writes are compatibility metadata only. They do not authorize execution.
	Inputs []string `json:"inputs"`
	Writes []string `json:"writes"`
}

type CommandSpec struct {
	Path    []string
	Summary string
	Flags   []FlagSpec
	// InputSchema and OutputSchema are optional inspection metadata, not host-side gates.
	InputSchema    json.RawMessage
	OutputSchema   json.RawMessage
	SideEffect     SideEffect
	DelegatedTools []DelegatedToolSpec
	Run            Handler
}

type Spec struct {
	Descriptor Descriptor
	Commands   []CommandSpec
}

type Plugin interface {
	Spec() Spec
}

type staticPlugin struct {
	spec Spec
}

func NewStatic(spec Spec) (Plugin, error) {
	if err := ValidateSpec(spec); err != nil {
		return nil, err
	}

	return &staticPlugin{spec: cloneSpec(spec)}, nil
}

func (p *staticPlugin) Spec() Spec {
	return cloneSpec(p.spec)
}

func cloneSpec(spec Spec) Spec {
	return Spec{
		Descriptor: cloneDescriptor(spec.Descriptor),
		Commands:   cloneCommands(spec.Commands),
	}
}

func cloneDescriptor(descriptor Descriptor) Descriptor {
	descriptor.Provides = cloneCapabilities(descriptor.Provides)
	descriptor.Requires = cloneCapabilities(descriptor.Requires)
	return descriptor
}

func cloneCapabilities(capabilities []Capability) []Capability {
	if capabilities == nil {
		return nil
	}

	return append([]Capability(nil), capabilities...)
}

func cloneCommands(commands []CommandSpec) []CommandSpec {
	if commands == nil {
		return nil
	}

	cloned := make([]CommandSpec, len(commands))
	for i, command := range commands {
		cloned[i] = command
		cloned[i].Path = append([]string(nil), command.Path...)
		cloned[i].Flags = cloneFlags(command.Flags)
		cloned[i].InputSchema = cloneJSON(command.InputSchema)
		cloned[i].OutputSchema = cloneJSON(command.OutputSchema)
		cloned[i].DelegatedTools = cloneDelegatedTools(command.DelegatedTools)
	}
	return cloned
}

func cloneDelegatedTools(tools []DelegatedToolSpec) []DelegatedToolSpec {
	if tools == nil {
		return nil
	}
	result := make([]DelegatedToolSpec, len(tools))
	for index, tool := range tools {
		result[index] = tool
		result[index].Inputs = cloneStrings(tool.Inputs)
		result[index].Writes = cloneStrings(tool.Writes)
	}
	return result
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	result := make([]string, len(values))
	copy(result, values)
	return result
}

func cloneFlags(flags []FlagSpec) []FlagSpec {
	if flags == nil {
		return nil
	}

	cloned := make([]FlagSpec, len(flags))
	for i, flag := range flags {
		cloned[i] = flag
		cloned[i].Default = cloneJSON(flag.Default)
	}
	return cloned
}

func cloneJSON(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}

	return append(json.RawMessage(nil), value...)
}

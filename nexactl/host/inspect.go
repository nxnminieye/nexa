package host

import (
	"sort"

	"github.com/nxnminieye/nexa/nexactl/plugin"
)

func (h *Host) Inspect() Inspection {
	commands := h.commands()
	inspection := Inspection{
		APIVersion: "nexa.dev/cli-inspection/v1",
		Binary: binaryInspection{
			Name:    h.name,
			Version: h.version,
		},
		GlobalFlags: cloneFlags(globalFlags()),
		Plugins:     make([]pluginInspection, len(h.specs)),
		Capabilities: make(
			[]capabilityInspection,
			0,
			providedCapabilityCount(h.specs),
		),
		Commands: make([]commandInspection, len(commands)),
	}

	for i, spec := range h.specs {
		inspection.Plugins[i] = pluginInspection{
			ID:              spec.Descriptor.ID,
			Version:         spec.Descriptor.Version,
			ContractVersion: spec.Descriptor.ContractVersion,
			Provides:        cloneCapabilities(spec.Descriptor.Provides),
			Requires:        cloneCapabilities(spec.Descriptor.Requires),
		}
		for _, capability := range spec.Descriptor.Provides {
			inspection.Capabilities = append(inspection.Capabilities, capabilityInspection{
				ID:               capability.ID,
				Version:          capability.Version,
				ProviderPluginID: spec.Descriptor.ID,
			})
		}
	}
	sort.Slice(inspection.Capabilities, func(i, j int) bool {
		return inspection.Capabilities[i].ID < inspection.Capabilities[j].ID
	})

	for i, owned := range commands {
		inspection.Commands[i] = commandInspection{
			Path:           append([]string(nil), owned.command.Path...),
			Summary:        owned.command.Summary,
			Flags:          cloneFlags(owned.command.Flags),
			InputSchema:    append([]byte(nil), owned.command.InputSchema...),
			OutputSchema:   append([]byte(nil), owned.command.OutputSchema...),
			SideEffect:     owned.command.SideEffect,
			DelegatedTools: cloneDelegatedTools(owned.command.DelegatedTools),
			OwnerPluginID:  owned.pluginID,
		}
	}
	return inspection
}

func cloneDelegatedTools(tools []plugin.DelegatedToolSpec) []plugin.DelegatedToolSpec {
	if tools == nil {
		return nil
	}
	result := make([]plugin.DelegatedToolSpec, len(tools))
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

func cloneFlags(flags []plugin.FlagSpec) []plugin.FlagSpec {
	cloned := make([]plugin.FlagSpec, len(flags))
	for i, flag := range flags {
		cloned[i] = flag
		cloned[i].Default = append([]byte(nil), flag.Default...)
	}
	return cloned
}

func providedCapabilityCount(specs []plugin.Spec) int {
	count := 0
	for _, spec := range specs {
		count += len(spec.Descriptor.Provides)
	}
	return count
}

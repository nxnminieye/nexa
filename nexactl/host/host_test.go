package host_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
)

func TestInspectDerivesCompositionFromSpecs(t *testing.T) {
	providerSpec := inspectionSpec("zulu", []string{"facts", "check"})
	providerSpec.Descriptor.Provides = []plugin.Capability{{ID: "facts.service", Version: "v1.2.0"}}
	consumerSpec := inspectionSpec("alpha", []string{"service", "generate"})
	consumerSpec.Descriptor.Requires = []plugin.Capability{{ID: "facts.service", Version: "v1.1.0"}}
	h := mustHost(t, mustPlugin(t, providerSpec), mustPlugin(t, consumerSpec))

	inspection := h.Inspect()
	if inspection.APIVersion != "nexa.dev/cli-inspection/v1" {
		t.Fatalf("apiVersion = %q", inspection.APIVersion)
	}
	if inspection.Binary.Name != "nexactl" || inspection.Binary.Version != "v0.0.0-test" {
		t.Fatalf("binary = %#v", inspection.Binary)
	}
	if len(inspection.Plugins) != 2 || len(inspection.Commands) != 4 {
		t.Fatalf("unexpected inspection: %#v", inspection)
	}
	if got := []string{inspection.Plugins[0].ID, inspection.Plugins[1].ID}; !reflect.DeepEqual(got, []string{"alpha", "zulu"}) {
		t.Fatalf("plugin order = %v", got)
	}
	if got := inspection.Plugins[0].Requires; !reflect.DeepEqual(got, []plugin.Capability{{ID: "facts.service", Version: "v1.1.0"}}) {
		t.Fatalf("alpha requires = %#v", got)
	}
	if got := inspection.Plugins[1].Provides; !reflect.DeepEqual(got, []plugin.Capability{{ID: "facts.service", Version: "v1.2.0"}}) {
		t.Fatalf("zulu provides = %#v", got)
	}
	if got := inspection.Plugins[1].Version; got != "v1.0.0" {
		t.Fatalf("plugin version = %q", got)
	}
	if len(inspection.Capabilities) != 1 || inspection.Capabilities[0].ID != "facts.service" ||
		inspection.Capabilities[0].Version != "v1.2.0" || inspection.Capabilities[0].ProviderPluginID != "zulu" {
		t.Fatalf("capabilities = %#v", inspection.Capabilities)
	}

	commandPaths := make([][]string, len(inspection.Commands))
	for i, command := range inspection.Commands {
		commandPaths[i] = command.Path
	}
	wantPaths := [][]string{{"facts", "check"}, {"inspect"}, {"service", "generate"}, {"version"}}
	if !reflect.DeepEqual(commandPaths, wantPaths) {
		t.Fatalf("command paths = %#v, want %#v", commandPaths, wantPaths)
	}
	pluginCommand := inspection.Commands[2]
	if pluginCommand.OwnerPluginID != "alpha" || pluginCommand.Summary != "inspect alpha" {
		t.Fatalf("plugin command = %#v", pluginCommand)
	}
	if len(pluginCommand.Flags) != 4 || pluginCommand.Flags[0].Name != "name" || !pluginCommand.Flags[0].Required {
		t.Fatalf("command flags = %#v", pluginCommand.Flags)
	}
	if string(pluginCommand.Flags[3].Default) != `["stable","public"]` {
		t.Fatalf("labels default = %s", pluginCommand.Flags[3].Default)
	}
	if string(pluginCommand.InputSchema) != `{"type":"object","properties":{"name":{"type":"string"}}}` {
		t.Fatalf("input schema = %s", pluginCommand.InputSchema)
	}
	if string(pluginCommand.OutputSchema) != `{"type":"object","properties":{"accepted":{"type":"boolean"}}}` {
		t.Fatalf("output schema = %s", pluginCommand.OutputSchema)
	}
	if pluginCommand.SideEffect != plugin.SideEffectRepositoryWrite {
		t.Fatalf("side effect = %q", pluginCommand.SideEffect)
	}
	if !reflect.DeepEqual(pluginCommand.DelegatedTools, []plugin.DelegatedToolSpec{{ID: "goctl", Version: "v1.9.2", Inputs: []string{"protocol-ir"}, Writes: []string{"staging"}}}) {
		t.Fatalf("delegated tools = %#v", pluginCommand.DelegatedTools)
	}
	if inspection.Commands[1].OwnerPluginID != "nexactl.host" || inspection.Commands[3].OwnerPluginID != "nexactl.host" {
		t.Fatalf("builtin owners = %q, %q", inspection.Commands[1].OwnerPluginID, inspection.Commands[3].OwnerPluginID)
	}
	if len(inspection.Commands[1].InputSchema) == 0 || len(inspection.Commands[1].OutputSchema) == 0 {
		t.Fatalf("builtin schemas missing: %#v", inspection.Commands[1])
	}
}

func TestInspectProjectsGlobalFlags(t *testing.T) {
	h := mustHost(t)
	inspection := h.Inspect()

	if len(inspection.GlobalFlags) != 2 {
		t.Fatalf("global flags = %#v", inspection.GlobalFlags)
	}
	if got := []string{inspection.GlobalFlags[0].Name, inspection.GlobalFlags[1].Name}; !reflect.DeepEqual(got, []string{"help", "json"}) {
		t.Fatalf("global flag order = %v", got)
	}
	for _, flag := range inspection.GlobalFlags {
		if flag.Type != plugin.FlagBool || len(flag.Summary) == 0 {
			t.Fatalf("global flag = %#v", flag)
		}
	}
	if len(inspection.Plugins) != 0 || len(inspection.Commands) != 2 {
		t.Fatalf("zero-plugin inspection = %#v", inspection)
	}
}

func TestInspectEncodesOptionalNonAuthoritativeSchemas(t *testing.T) {
	spec := inspectionSpec("alpha", []string{"service", "generate"})
	spec.Commands[0].InputSchema = nil
	spec.Commands[0].OutputSchema = json.RawMessage(`null`)
	h := mustHost(t, mustPlugin(t, spec))

	payload, err := json.Marshal(h.Inspect())
	if err != nil {
		t.Fatalf("json.Marshal(Inspect()) error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("inspection output is not JSON: %v", err)
	}
}

func TestInspectReturnsIndependentDeepCopies(t *testing.T) {
	spec := inspectionSpec("alpha", []string{"service", "generate"})
	spec.Descriptor.Provides = []plugin.Capability{{ID: "facts.service", Version: "v1.2.0"}}
	h := mustHost(t, mustPlugin(t, spec))

	want, err := json.Marshal(h.Inspect())
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	mutated := h.Inspect()
	if len(mutated.GlobalFlags) != 2 || len(mutated.Plugins) != 1 || len(mutated.Capabilities) != 1 || len(mutated.Commands) != 3 {
		t.Fatalf("inspection is incomplete: %#v", mutated)
	}
	if len(mutated.Commands[1].Flags) != 4 || len(mutated.Commands[1].Flags[1].Default) == 0 {
		t.Fatalf("plugin command flags are incomplete: %#v", mutated.Commands[1])
	}
	mutated.GlobalFlags[0].Name = "changed"
	mutated.Plugins[0].Provides[0].ID = "changed.capability"
	mutated.Capabilities[0].ID = "changed.capability"
	mutated.Commands[1].Path[0] = "changed"
	mutated.Commands[1].Flags[0].Name = "changed"
	mutated.Commands[1].Flags[1].Default[0] = 'x'
	mutated.Commands[1].InputSchema[0] = '['
	mutated.Commands[1].OutputSchema[0] = '['
	mutated.Commands[1].DelegatedTools[0].ID = "changed-tool"
	mutated.Commands[1].DelegatedTools[0].Inputs[0] = "changed-input"
	mutated.Commands[1].DelegatedTools[0].Writes[0] = "changed-write"

	got, err := json.Marshal(h.Inspect())
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Inspect() changed after result mutation\n got: %s\nwant: %s", got, want)
	}
}

func mustPlugin(t *testing.T, spec plugin.Spec) plugin.Plugin {
	t.Helper()

	got, err := plugin.NewStatic(spec)
	if err != nil {
		t.Fatalf("plugin.NewStatic() error = %v", err)
	}
	return got
}

func mustHost(t *testing.T, plugins ...plugin.Plugin) *host.Host {
	t.Helper()

	got, err := host.New(host.Options{Version: "v0.0.0-test"}, plugins...)
	if err != nil {
		t.Fatalf("host.New() error = %v", err)
	}
	return got
}

func inspectionSpec(id string, path []string) plugin.Spec {
	spec := specWithCommand(id, path)
	spec.Commands[0].Summary = "inspect " + id
	spec.Commands[0].Flags = []plugin.FlagSpec{
		{Name: "name", Type: plugin.FlagString, Summary: "object name", Required: true},
		{Name: "force", Type: plugin.FlagBool, Summary: "force operation", Default: json.RawMessage(`false`)},
		{Name: "retries", Type: plugin.FlagInt, Summary: "retry count", Default: json.RawMessage(`2`)},
		{Name: "labels", Type: plugin.FlagStringSlice, Summary: "object labels", Default: json.RawMessage(`["stable","public"]`)},
	}
	spec.Commands[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)
	spec.Commands[0].OutputSchema = json.RawMessage(`{"type":"object","properties":{"accepted":{"type":"boolean"}}}`)
	spec.Commands[0].SideEffect = plugin.SideEffectRepositoryWrite
	spec.Commands[0].DelegatedTools = []plugin.DelegatedToolSpec{{ID: "goctl", Version: "v1.9.2", Inputs: []string{"protocol-ir"}, Writes: []string{"staging"}}}
	return spec
}

func specWithCommand(id string, path []string) plugin.Spec {
	return plugin.Spec{
		Descriptor: plugin.Descriptor{
			ID:              id,
			Version:         "v1.0.0",
			ContractVersion: plugin.ContractVersion,
		},
		Commands: []plugin.CommandSpec{
			{
				Path:         path,
				Summary:      "run " + id,
				InputSchema:  json.RawMessage(`{"type":"object"}`),
				OutputSchema: json.RawMessage(`{"type":"object"}`),
				SideEffect:   plugin.SideEffectNone,
				Run: func(context.Context, plugin.Invocation) (any, error) {
					return map[string]any{"plugin": id}, nil
				},
			},
		},
	}
}

func specProviding(id, capabilityID, version string) plugin.Spec {
	spec := specWithCommand(id, []string{id, "run"})
	spec.Descriptor.Provides = []plugin.Capability{{ID: capabilityID, Version: version}}
	return spec
}

func specRequiring(id, capabilityID, version string) plugin.Spec {
	spec := specWithCommand(id, []string{id, "run"})
	spec.Descriptor.Requires = []plugin.Capability{{ID: capabilityID, Version: version}}
	return spec
}

type rawPlugin struct {
	spec plugin.Spec
}

func (p *rawPlugin) Spec() plugin.Spec {
	return p.spec
}

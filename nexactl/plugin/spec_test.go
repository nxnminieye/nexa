package plugin_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/nexactl/plugin"
)

func validSpec() plugin.Spec {
	return plugin.Spec{
		Descriptor: plugin.Descriptor{
			ID:              "nexa.build.core",
			Version:         "v1.2.3",
			ContractVersion: plugin.ContractVersion,
			Provides: []plugin.Capability{
				{ID: "nexa.build.generate", Version: "v1.0.0"},
			},
			Requires: []plugin.Capability{
				{ID: "nexa.source.catalog", Version: "v2.1.0"},
			},
		},
		Commands: []plugin.CommandSpec{
			{
				Path:    []string{"generate", "service"},
				Summary: "generate a service",
				Flags: []plugin.FlagSpec{
					{
						Name:     "name",
						Type:     plugin.FlagString,
						Summary:  "service name",
						Required: true,
						Default:  json.RawMessage(`"orders"`),
					},
					{
						Name:    "dry-run",
						Type:    plugin.FlagBool,
						Summary: "show changes",
						Default: json.RawMessage(`false`),
					},
					{
						Name:    "retries",
						Type:    plugin.FlagInt,
						Summary: "retry count",
						Default: json.RawMessage(`2`),
					},
					{
						Name:    "labels",
						Type:    plugin.FlagStringSlice,
						Summary: "service labels",
						Default: json.RawMessage(`["public","stable"]`),
					},
				},
				InputSchema:  json.RawMessage(`{"type":"object"}`),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
				SideEffect:   plugin.SideEffectRepositoryWrite,
				DelegatedTools: []plugin.DelegatedToolSpec{
					{ID: "goctl", Version: "v1.9.2", Inputs: []string{"protocol-ir", "consumer-module"}, Writes: []string{"staging"}},
				},
				Run: func(context.Context, plugin.Invocation) (any, error) {
					return "ok", nil
				},
			},
		},
	}
}

func TestNewStaticRejectsInvalidSpec(t *testing.T) {
	spec := validSpec()
	spec.Commands[0].Run = nil

	got, err := plugin.NewStatic(spec)
	if err == nil {
		t.Fatal("NewStatic() error = nil")
	}
	if got != nil {
		t.Fatalf("NewStatic() plugin = %#v, want nil", got)
	}
}

func TestNewStaticCopiesConstructionInput(t *testing.T) {
	spec := validSpec()
	got, err := plugin.NewStatic(spec)
	if err != nil {
		t.Fatalf("NewStatic() error = %v", err)
	}

	spec.Descriptor.Provides[0].ID = "mutated.provide"
	spec.Descriptor.Requires[0].Version = "v9.9.9"
	spec.Commands[0].Summary = "mutated summary"
	spec.Commands[0].Path[0] = "mutated-path"
	spec.Commands[0].Flags[0].Name = "mutated-flag"
	spec.Commands[0].Flags[0].Default[0] = 'x'
	spec.Commands[0].InputSchema[0] = '['
	spec.Commands[0].OutputSchema[0] = '['
	spec.Commands[0].DelegatedTools[0].ID = "mutated-tool"
	spec.Commands[0].DelegatedTools[0].Inputs[0] = "mutated-input"
	spec.Commands[0].DelegatedTools[0].Writes[0] = "mutated-write"

	assertOriginalSpec(t, got.Spec())
}

func TestStaticPluginReturnsIndependentCopies(t *testing.T) {
	got, err := plugin.NewStatic(validSpec())
	if err != nil {
		t.Fatalf("NewStatic() error = %v", err)
	}

	first := got.Spec()
	first.Descriptor.Provides[0].ID = "mutated.provide"
	first.Descriptor.Requires[0].Version = "v9.9.9"
	first.Commands[0].Summary = "mutated summary"
	first.Commands[0].Path[0] = "mutated-path"
	first.Commands[0].Flags[0].Name = "mutated-flag"
	first.Commands[0].Flags[0].Default[0] = 'x'
	first.Commands[0].InputSchema[0] = '['
	first.Commands[0].OutputSchema[0] = '['
	first.Commands[0].DelegatedTools[0].Version = "mutated-version"
	first.Commands[0].DelegatedTools[0].Inputs[0] = "mutated-input"
	first.Commands[0].DelegatedTools[0].Writes[0] = "mutated-write"

	assertOriginalSpec(t, got.Spec())
}

func TestStaticPluginPreservesExplicitEmptyDelegatedScopes(t *testing.T) {
	spec := validSpec()
	spec.Commands[0].DelegatedTools[0].Writes = []string{}
	value, err := plugin.NewStatic(spec)
	if err != nil {
		t.Fatal(err)
	}
	writes := value.Spec().Commands[0].DelegatedTools[0].Writes
	if writes == nil || len(writes) != 0 {
		t.Fatalf("writes = %#v, want explicit empty slice", writes)
	}
}

func assertOriginalSpec(t *testing.T, spec plugin.Spec) {
	t.Helper()

	if got := spec.Descriptor.Provides[0].ID; got != "nexa.build.generate" {
		t.Fatalf("provided capability ID = %q", got)
	}
	if got := spec.Descriptor.Requires[0].Version; got != "v2.1.0" {
		t.Fatalf("required capability version = %q", got)
	}
	if got := spec.Commands[0].Summary; got != "generate a service" {
		t.Fatalf("command summary = %q", got)
	}
	if got := spec.Commands[0].Path[0]; got != "generate" {
		t.Fatalf("command path token = %q", got)
	}
	if got := spec.Commands[0].Flags[0].Name; got != "name" {
		t.Fatalf("flag name = %q", got)
	}
	if got := string(spec.Commands[0].Flags[0].Default); got != `"orders"` {
		t.Fatalf("flag default = %q", got)
	}
	if got := string(spec.Commands[0].InputSchema); got != `{"type":"object"}` {
		t.Fatalf("input schema = %q", got)
	}
	if got := string(spec.Commands[0].OutputSchema); got != `{"type":"object","properties":{"path":{"type":"string"}}}` {
		t.Fatalf("output schema = %q", got)
	}
	tool := spec.Commands[0].DelegatedTools[0]
	if tool.ID != "goctl" || tool.Version != "v1.9.2" || !reflect.DeepEqual(tool.Inputs, []string{"protocol-ir", "consumer-module"}) || !reflect.DeepEqual(tool.Writes, []string{"staging"}) {
		t.Fatalf("delegated tool = %#v", tool)
	}
}

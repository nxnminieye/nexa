package host

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/nxnminieye/nexa/nexactl/plugin"
)

func TestCommandTreeKeepsPluginHelpUniqueAfterCobraInitialization(t *testing.T) {
	privateHelp, err := plugin.NewStatic(plugin.Spec{
		Descriptor: plugin.Descriptor{
			ID:              "private-help",
			Version:         "v1.0.0",
			ContractVersion: plugin.ContractVersion,
		},
		Commands: []plugin.CommandSpec{
			{
				Path:         []string{"help"},
				Summary:      "run private help",
				InputSchema:  json.RawMessage(`{"type":"object"}`),
				OutputSchema: json.RawMessage(`{"type":"object"}`),
				SideEffect:   plugin.SideEffectNone,
				Run: func(context.Context, plugin.Invocation) (any, error) {
					return map[string]any{"owner": "private-help"}, nil
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("plugin.NewStatic() error = %v", err)
	}
	h, err := New(Options{Version: "v0.0.0-test"}, privateHelp)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	root := h.newCommandTree(&executionState{}, io.Discard)
	root.InitDefaultHelpCmd()
	seen := make(map[string]struct{})
	for _, command := range root.Commands() {
		name := command.Name()
		if _, exists := seen[name]; exists {
			t.Fatalf("runtime command name %q is ambiguous", name)
		}
		seen[name] = struct{}{}
	}
	for _, publicName := range []string{"help", "inspect", "version"} {
		if _, exists := seen[publicName]; !exists {
			t.Fatalf("runtime command %q is missing: %#v", publicName, seen)
		}
		delete(seen, publicName)
	}
	if len(seen) != 1 {
		t.Fatalf("internal command count = %d, want 1: %#v", len(seen), seen)
	}
	for internalName := range seen {
		if validLowerKebabToken(internalName) {
			t.Fatalf("internal command name %q can collide with a plugin path", internalName)
		}
	}

	inspection := h.Inspect()
	if len(inspection.Commands) != 3 {
		t.Fatalf("public commands = %#v", inspection.Commands)
	}
}

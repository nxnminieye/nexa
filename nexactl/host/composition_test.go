package host_test

import (
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
)

func TestNewRejectsDuplicateCommand(t *testing.T) {
	first := mustPlugin(t, specWithCommand("one", []string{"service", "check"}))
	second := mustPlugin(t, specWithCommand("two", []string{"service", "check"}))

	_, err := host.New(host.Options{Version: "v0.0.0-test"}, first, second)
	assertHostError(t, err, "command_conflict")
}

func TestNewRejectsMissingCapability(t *testing.T) {
	consumer := mustPlugin(t, specRequiring("consumer", "facts.service", "v1.0.0"))

	_, err := host.New(host.Options{Version: "v0.0.0-test"}, consumer)
	assertHostError(t, err, "plugin_dependency_missing")
}

func TestNewRejectsDuplicatePluginID(t *testing.T) {
	first := mustPlugin(t, specWithCommand("duplicate", []string{"first"}))
	second := mustPlugin(t, specWithCommand("duplicate", []string{"second"}))

	_, err := host.New(host.Options{Version: "v0.0.0-test"}, first, second)
	assertHostError(t, err, "plugin_id_conflict")
}

func TestNewRejectsDuplicateProvidedCapability(t *testing.T) {
	first := mustPlugin(t, specProviding("one", "facts.service", "v1.0.0"))
	second := mustPlugin(t, specProviding("two", "facts.service", "v1.1.0"))

	_, err := host.New(host.Options{Version: "v0.0.0-test"}, first, second)
	assertHostError(t, err, "capability_conflict")
}

func TestNewRejectsDependencyCycle(t *testing.T) {
	firstSpec := specProviding("one", "capability.one", "v1.0.0")
	firstSpec.Descriptor.Requires = []plugin.Capability{{ID: "capability.two", Version: "v1.0.0"}}
	secondSpec := specProviding("two", "capability.two", "v1.0.0")
	secondSpec.Descriptor.Requires = []plugin.Capability{{ID: "capability.one", Version: "v1.0.0"}}

	_, err := host.New(
		host.Options{Version: "v0.0.0-test"},
		mustPlugin(t, firstSpec),
		mustPlugin(t, secondSpec),
	)
	assertHostError(t, err, "plugin_dependency_cycle")
}

func TestNewAcceptsCompatibleCapability(t *testing.T) {
	provider := mustPlugin(t, specProviding("provider", "facts.service", "v1.2.0"))
	consumer := mustPlugin(t, specRequiring("consumer", "facts.service", "v1.1.0"))

	if _, err := host.New(host.Options{Version: "v0.0.0-test"}, consumer, provider); err != nil {
		t.Fatalf("host.New() error = %v", err)
	}
}

func TestNewRejectsIncompatibleCapability(t *testing.T) {
	tests := []struct {
		name            string
		providedVersion string
		requiredVersion string
	}{
		{name: "different major", providedVersion: "v2.0.0", requiredVersion: "v1.0.0"},
		{name: "provider too old", providedVersion: "v1.1.9", requiredVersion: "v1.2.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := mustPlugin(t, specProviding("provider", "facts.service", tt.providedVersion))
			consumer := mustPlugin(t, specRequiring("consumer", "facts.service", tt.requiredVersion))

			_, err := host.New(host.Options{Version: "v0.0.0-test"}, provider, consumer)
			assertHostError(t, err, "plugin_dependency_incompatible")
		})
	}
}

func TestNewRejectsReservedAndPrefixCommandConflicts(t *testing.T) {
	tests := []struct {
		name    string
		plugins []plugin.Plugin
	}{
		{name: "inspect builtin", plugins: []plugin.Plugin{mustPlugin(t, specWithCommand("one", []string{"inspect"}))}},
		{name: "version builtin child", plugins: []plugin.Plugin{mustPlugin(t, specWithCommand("one", []string{"version", "details"}))}},
		{
			name: "executable prefix",
			plugins: []plugin.Plugin{
				mustPlugin(t, specWithCommand("one", []string{"service"})),
				mustPlugin(t, specWithCommand("two", []string{"service", "check"})),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := host.New(host.Options{Version: "v0.0.0-test"}, tt.plugins...)
			assertHostError(t, err, "command_conflict")
		})
	}
}

func TestNewRejectsReservedFlags(t *testing.T) {
	for _, reserved := range []string{"json", "help"} {
		t.Run(reserved, func(t *testing.T) {
			spec := specWithCommand("one", []string{"service", "check"})
			spec.Commands[0].Flags = []plugin.FlagSpec{
				{Name: reserved, Type: plugin.FlagBool, Summary: "reserved"},
			}

			_, err := host.New(host.Options{Version: "v0.0.0-test"}, mustPlugin(t, spec))
			assertHostError(t, err, "flag_conflict")
		})
	}
}

func TestNewValidatesHostOptionsAndPlugins(t *testing.T) {
	tests := []struct {
		name    string
		options host.Options
		plugins []plugin.Plugin
		code    string
	}{
		{name: "empty version", options: host.Options{}, code: "host_version_invalid"},
		{name: "invalid version", options: host.Options{Version: "1.0.0"}, code: "host_version_invalid"},
		{name: "invalid name", options: host.Options{Name: "Nexa_CTL", Version: "v1.0.0"}, code: "host_name_invalid"},
		{name: "nil plugin", options: host.Options{Version: "v1.0.0"}, plugins: []plugin.Plugin{nil}, code: "plugin_invalid"},
		{name: "typed nil plugin", options: host.Options{Version: "v1.0.0"}, plugins: []plugin.Plugin{(*rawPlugin)(nil)}, code: "plugin_invalid"},
		{
			name:    "invalid plugin spec",
			options: host.Options{Version: "v1.0.0"},
			plugins: []plugin.Plugin{&rawPlugin{spec: plugin.Spec{}}},
			code:    "plugin_invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := host.New(tt.options, tt.plugins...)
			assertHostError(t, err, tt.code)
		})
	}
}

func TestNewUsesStableOrdering(t *testing.T) {
	zulu := mustPlugin(t, specProviding("zulu", "zulu.capability", "v1.0.0"))
	alpha := mustPlugin(t, specProviding("alpha", "alpha.capability", "v1.0.0"))

	h, err := host.New(host.Options{Version: "v0.0.0-test"}, zulu, alpha)
	if err != nil {
		t.Fatalf("host.New() error = %v", err)
	}
	inspection := h.Inspect()
	if got := []string{inspection.Plugins[0].ID, inspection.Plugins[1].ID}; got[0] != "alpha" || got[1] != "zulu" {
		t.Fatalf("plugin order = %v", got)
	}
	if got := []string{inspection.Capabilities[0].ID, inspection.Capabilities[1].ID}; got[0] != "alpha.capability" || got[1] != "zulu.capability" {
		t.Fatalf("capability order = %v", got)
	}
}

func TestNewConstructsZeroPluginHost(t *testing.T) {
	h, err := host.New(host.Options{Version: "v0.0.0-test"})
	if err != nil {
		t.Fatalf("host.New() error = %v", err)
	}
	inspection := h.Inspect()
	if inspection.Binary.Name != "nexactl" || inspection.Binary.Version != "v0.0.0-test" {
		t.Fatalf("binary = %#v", inspection.Binary)
	}
	if len(inspection.Plugins) != 0 || len(inspection.Capabilities) != 0 {
		t.Fatalf("unexpected plugin composition: %#v", inspection)
	}
}

func TestNewCopiesPluginSpecs(t *testing.T) {
	spec := specProviding("mutable", "facts.service", "v1.0.0")
	spec.Commands[0].DelegatedTools = []plugin.DelegatedToolSpec{{
		ID: "goctl", Version: "v1.9.2", Inputs: []string{"protocol-ir"}, Writes: []string{"staging"},
	}}
	raw := &rawPlugin{spec: spec}
	h, err := host.New(host.Options{Version: "v0.0.0-test"}, raw)
	if err != nil {
		t.Fatalf("host.New() error = %v", err)
	}

	raw.spec.Descriptor.ID = "changed"
	raw.spec.Descriptor.Provides[0].ID = "changed.capability"
	raw.spec.Commands[0].Path[0] = "changed"
	raw.spec.Commands[0].DelegatedTools[0].ID = "changed-tool"
	raw.spec.Commands[0].DelegatedTools[0].Inputs[0] = "changed-input"
	raw.spec.Commands[0].DelegatedTools[0].Writes[0] = "changed-write"
	inspection := h.Inspect()
	if inspection.Plugins[0].ID != "mutable" || inspection.Capabilities[0].ID != "facts.service" {
		t.Fatalf("host composition changed: %#v", inspection)
	}
	wantTool := plugin.DelegatedToolSpec{ID: "goctl", Version: "v1.9.2", Inputs: []string{"protocol-ir"}, Writes: []string{"staging"}}
	if !reflect.DeepEqual(inspection.Commands[1].DelegatedTools, []plugin.DelegatedToolSpec{wantTool}) {
		t.Fatalf("delegated tools changed: %#v", inspection.Commands[1].DelegatedTools)
	}
}

func assertHostError(t *testing.T, err error, wantCode string) {
	t.Helper()

	if err == nil {
		t.Fatalf("error = nil, want code %q", wantCode)
	}
	payload := protocol.Project(err)
	if payload.Code != wantCode {
		t.Fatalf("code = %q, want %q", payload.Code, wantCode)
	}
	if payload.Domain != "nexactl.host" {
		t.Fatalf("domain = %q", payload.Domain)
	}
	if payload.Category != protocol.CategoryInput {
		t.Fatalf("category = %q", payload.Category)
	}
}

package plugin_test

import (
	"encoding/json"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/nexactl/plugin"
)

func TestValidateSpecAcceptsValidSpec(t *testing.T) {
	if err := plugin.ValidateSpec(validSpec()); err != nil {
		t.Fatalf("ValidateSpec() error = %v", err)
	}
}

func TestValidateSpecAcceptsRepeatedTokensWhenFullPathsDiffer(t *testing.T) {
	spec := validSpec()
	repeated := spec.Commands[0]
	repeated.Path = []string{"generate", "generate"}
	spec.Commands = append(spec.Commands, repeated)

	if err := plugin.ValidateSpec(spec); err != nil {
		t.Fatalf("ValidateSpec() error = %v", err)
	}
}

func TestValidateSpecRejectsInvalidDescriptor(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*plugin.Spec)
		code   string
	}{
		{
			name: "plugin id",
			mutate: func(spec *plugin.Spec) {
				spec.Descriptor.ID = "Nexa.Build"
			},
			code: "plugin_id_invalid",
		},
		{
			name: "plugin version",
			mutate: func(spec *plugin.Spec) {
				spec.Descriptor.Version = "1.2.3"
			},
			code: "plugin_version_invalid",
		},
		{
			name: "contract version",
			mutate: func(spec *plugin.Spec) {
				spec.Descriptor.ContractVersion = "nexa.dev/nexactl-plugin/v2"
			},
			code: "contract_version_invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validSpec()
			tt.mutate(&spec)
			assertValidationCode(t, spec, tt.code)
		})
	}
}

func TestValidateSpecRejectsInvalidCapability(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*plugin.Spec)
		code   string
	}{
		{
			name: "invalid id",
			mutate: func(spec *plugin.Spec) {
				spec.Descriptor.Provides[0].ID = "Nexa.Build"
			},
			code: "capability_id_invalid",
		},
		{
			name: "version embedded in id",
			mutate: func(spec *plugin.Spec) {
				spec.Descriptor.Provides[0].ID = "nexa.build.v1"
			},
			code: "capability_id_invalid",
		},
		{
			name: "invalid version",
			mutate: func(spec *plugin.Spec) {
				spec.Descriptor.Provides[0].Version = "1.0.0"
			},
			code: "capability_version_invalid",
		},
		{
			name: "duplicate provided id",
			mutate: func(spec *plugin.Spec) {
				spec.Descriptor.Provides = append(spec.Descriptor.Provides, plugin.Capability{
					ID:      spec.Descriptor.Provides[0].ID,
					Version: "v2.0.0",
				})
			},
			code: "capability_duplicate",
		},
		{
			name: "duplicate required id",
			mutate: func(spec *plugin.Spec) {
				spec.Descriptor.Requires = append(spec.Descriptor.Requires, plugin.Capability{
					ID:      spec.Descriptor.Requires[0].ID,
					Version: "v3.0.0",
				})
			},
			code: "capability_duplicate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validSpec()
			tt.mutate(&spec)
			assertValidationCode(t, spec, tt.code)
		})
	}
}

func TestValidateSpecRejectsInvalidCommandPath(t *testing.T) {
	tests := []struct {
		name string
		path []string
	}{
		{name: "empty path", path: nil},
		{name: "empty token", path: []string{"generate", ""}},
		{name: "non kebab token", path: []string{"generate", "Service_Name"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validSpec()
			spec.Commands[0].Path = tt.path
			assertValidationCode(t, spec, "command_path_invalid")
		})
	}
}

func TestValidateSpecRejectsDuplicateCommandPath(t *testing.T) {
	spec := validSpec()
	duplicate := spec.Commands[0]
	spec.Commands = append(spec.Commands, duplicate)

	assertValidationCode(t, spec, "command_duplicate")
}

func TestValidateSpecRejectsInvalidFlag(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*plugin.Spec)
		code   string
	}{
		{
			name: "invalid name",
			mutate: func(spec *plugin.Spec) {
				spec.Commands[0].Flags[0].Name = "Service_Name"
			},
			code: "flag_name_invalid",
		},
		{
			name: "invalid type",
			mutate: func(spec *plugin.Spec) {
				spec.Commands[0].Flags[0].Type = plugin.FlagType("duration")
			},
			code: "flag_type_invalid",
		},
		{
			name: "duplicate name",
			mutate: func(spec *plugin.Spec) {
				duplicate := spec.Commands[0].Flags[0]
				spec.Commands[0].Flags = append(spec.Commands[0].Flags, duplicate)
			},
			code: "flag_duplicate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validSpec()
			tt.mutate(&spec)
			assertValidationCode(t, spec, tt.code)
		})
	}
}

func TestValidateSpecRejectsDefaultWithWrongStructuredType(t *testing.T) {
	tests := []struct {
		name       string
		flagType   plugin.FlagType
		defaultRaw string
	}{
		{name: "invalid json", flagType: plugin.FlagString, defaultRaw: `"unterminated`},
		{name: "string", flagType: plugin.FlagString, defaultRaw: `42`},
		{name: "bool", flagType: plugin.FlagBool, defaultRaw: `"false"`},
		{name: "integer", flagType: plugin.FlagInt, defaultRaw: `1.5`},
		{name: "string slice", flagType: plugin.FlagStringSlice, defaultRaw: `["ok",1]`},
		{name: "null", flagType: plugin.FlagString, defaultRaw: `null`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validSpec()
			spec.Commands[0].Flags[0].Type = tt.flagType
			spec.Commands[0].Flags[0].Default = json.RawMessage(tt.defaultRaw)
			assertValidationCode(t, spec, "flag_default_invalid")
		})
	}
}

func TestValidateSpecAllowsOmittedFlagDefault(t *testing.T) {
	spec := validSpec()
	spec.Commands[0].Flags[0].Default = nil

	if err := plugin.ValidateSpec(spec); err != nil {
		t.Fatalf("ValidateSpec() error = %v", err)
	}
}

func TestValidateSpecRejectsRuntimeSideEffect(t *testing.T) {
	spec := validSpec()
	spec.Commands[0].SideEffect = plugin.SideEffect("runtime-write")

	assertValidationCode(t, spec, "side_effect_invalid")
}

func TestValidateSpecAcceptsRepositorySideEffects(t *testing.T) {
	for _, sideEffect := range []plugin.SideEffect{
		plugin.SideEffectNone,
		plugin.SideEffectRepositoryRead,
		plugin.SideEffectRepositoryWrite,
	} {
		t.Run(string(sideEffect), func(t *testing.T) {
			spec := validSpec()
			spec.Commands[0].SideEffect = sideEffect
			if err := plugin.ValidateSpec(spec); err != nil {
				t.Fatalf("ValidateSpec() error = %v", err)
			}
		})
	}
}

func TestValidateSpecDoesNotRequireCommandSchemas(t *testing.T) {
	spec := validSpec()
	spec.Commands[0].InputSchema = nil
	spec.Commands[0].OutputSchema = nil

	if err := plugin.ValidateSpec(spec); err != nil {
		t.Fatalf("ValidateSpec() treats compatibility schemas as authority: %v", err)
	}
}

func TestValidateSpecAcceptsAnyJSONSchemaMetadataShape(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `true`, `"schema label"`} {
		t.Run(raw, func(t *testing.T) {
			spec := validSpec()
			spec.Commands[0].InputSchema = json.RawMessage(raw)
			if err := plugin.ValidateSpec(spec); err != nil {
				t.Fatalf("ValidateSpec() applies schema semantics to %s: %v", raw, err)
			}
		})
	}
}

func TestValidateSpecRejectsUnencodableSchemaMetadata(t *testing.T) {
	for _, raw := range []json.RawMessage{json.RawMessage{}, json.RawMessage(`{"type":`)} {
		t.Run(string(raw), func(t *testing.T) {
			spec := validSpec()
			spec.Commands[0].OutputSchema = raw
			assertValidationCode(t, spec, "command_schema_invalid")
		})
	}
}

func TestValidateSpecRejectsInvalidDelegatedToolMetadata(t *testing.T) {
	tests := []struct {
		name, code string
		mutate     func(*plugin.CommandSpec)
	}{
		{name: "id", code: "delegated_tool_id_invalid", mutate: func(c *plugin.CommandSpec) { c.DelegatedTools[0].ID = "GoCtl" }},
		{name: "version", code: "delegated_tool_version_invalid", mutate: func(c *plugin.CommandSpec) { c.DelegatedTools[0].Version = "" }},
		{name: "non-semver version", code: "delegated_tool_version_invalid", mutate: func(c *plugin.CommandSpec) { c.DelegatedTools[0].Version = "garbage" }},
		{name: "major-only version", code: "delegated_tool_version_invalid", mutate: func(c *plugin.CommandSpec) { c.DelegatedTools[0].Version = "v1" }},
		{name: "major-minor version", code: "delegated_tool_version_invalid", mutate: func(c *plugin.CommandSpec) { c.DelegatedTools[0].Version = "v1.2" }},
		{name: "version control", code: "delegated_tool_version_invalid", mutate: func(c *plugin.CommandSpec) { c.DelegatedTools[0].Version = "v1\nsecret" }},
		{name: "duplicate tool", code: "delegated_tool_duplicate", mutate: func(c *plugin.CommandSpec) { c.DelegatedTools = append(c.DelegatedTools, c.DelegatedTools[0]) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validSpec()
			test.mutate(&spec.Commands[0])
			assertValidationCode(t, spec, test.code)
		})
	}
}

func TestValidateSpecDoesNotTreatDelegatedScopesAsAuthority(t *testing.T) {
	spec := validSpec()
	spec.Commands[0].DelegatedTools[0].Inputs = nil
	spec.Commands[0].DelegatedTools[0].Writes = []string{"", "", "bad\x00scope"}

	if err := plugin.ValidateSpec(spec); err != nil {
		t.Fatalf("ValidateSpec() treats delegated scopes as authority: %v", err)
	}
}

func TestValidateSpecRejectsNilHandler(t *testing.T) {
	spec := validSpec()
	spec.Commands[0].Run = nil

	assertValidationCode(t, spec, "command_handler_missing")
}

func assertValidationCode(t *testing.T, spec plugin.Spec, wantCode string) {
	t.Helper()

	err := plugin.ValidateSpec(spec)
	if err == nil {
		t.Fatalf("ValidateSpec() error = nil, want code %q", wantCode)
	}
	payload := protocol.Project(err)
	if payload.Code != wantCode {
		t.Fatalf("validation code = %q, want %q", payload.Code, wantCode)
	}
	if payload.Domain != "nexactl.plugin" {
		t.Fatalf("validation domain = %q", payload.Domain)
	}
	if payload.Category != protocol.CategoryInput {
		t.Fatalf("validation category = %q", payload.Category)
	}
}

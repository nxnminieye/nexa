package sdkpython

import (
	"context"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/internal/sdkpythonassets"
	"github.com/nxnminieye/nexa/nexactl/plugin"
)

func TestPluginDescriptorAndCommandRoster(t *testing.T) {
	tests := []struct {
		name    string
		builder sdkpythonassets.WheelBuilder
		want    []commandContract
	}{
		{
			name: "production nil builder",
			want: []commandContract{
				{action: "write", flags: []string{"repo-root"}, sideEffect: plugin.SideEffectRepositoryWrite},
				{action: "check", flags: []string{"repo-root"}, sideEffect: plugin.SideEffectRepositoryRead},
			},
		},
		{
			name:    "typed nil builder",
			builder: (*pointerWheelBuilder)(nil),
			want: []commandContract{
				{action: "write", flags: []string{"repo-root"}, sideEffect: plugin.SideEffectRepositoryWrite},
				{action: "check", flags: []string{"repo-root"}, sideEffect: plugin.SideEffectRepositoryRead},
			},
		},
		{
			name:    "injected builder",
			builder: inertWheelBuilder{},
			want: []commandContract{
				{action: "write", flags: []string{"repo-root"}, sideEffect: plugin.SideEffectRepositoryWrite},
				{action: "check", flags: []string{"repo-root"}, sideEffect: plugin.SideEffectRepositoryRead},
				{action: "build", flags: []string{"repo-root", "python", "matrix-target", "wheelhouse", "work-dir", "out"}, sideEffect: plugin.SideEffectRepositoryWrite},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate, err := New(Options{WheelBuilder: test.builder})
			if err != nil {
				t.Fatal(err)
			}
			spec := candidate.Spec()
			wantDescriptor := plugin.Descriptor{
				ID:              "sdk-python-assets",
				Version:         "v0.1.0",
				ContractVersion: plugin.ContractVersion,
				Provides: []plugin.Capability{{
					ID:      "generation.sdk-python-assets",
					Version: "v1.0.0",
				}},
			}
			if !reflect.DeepEqual(spec.Descriptor, wantDescriptor) {
				t.Fatalf("descriptor = %#v, want %#v", spec.Descriptor, wantDescriptor)
			}
			if len(spec.Commands) != len(test.want) {
				t.Fatalf("commands = %d, want %d", len(spec.Commands), len(test.want))
			}
			for index, want := range test.want {
				command := spec.Commands[index]
				if !reflect.DeepEqual(command.Path, []string{"generation", "sdk-python-assets", want.action}) {
					t.Fatalf("command %d path = %#v", index, command.Path)
				}
				if command.SideEffect != want.sideEffect || command.Run == nil || len(command.InputSchema) == 0 || len(command.OutputSchema) == 0 {
					t.Fatalf("command %d contract = %#v", index, command)
				}
				gotFlags := make([]string, len(command.Flags))
				for flagIndex, flag := range command.Flags {
					gotFlags[flagIndex] = flag.Name
					if flag.Type != plugin.FlagString || !flag.Required || len(flag.Default) != 0 {
						t.Fatalf("command %d flag %d = %#v", index, flagIndex, flag)
					}
				}
				if !reflect.DeepEqual(gotFlags, want.flags) {
					t.Fatalf("command %d flags = %#v, want %#v", index, gotFlags, want.flags)
				}
			}
		})
	}
}

func TestPluginSpecReturnsDefensiveCopies(t *testing.T) {
	candidate, err := New(Options{WheelBuilder: inertWheelBuilder{}})
	if err != nil {
		t.Fatal(err)
	}
	first := candidate.Spec()
	first.Descriptor.Provides[0].ID = "mutated"
	first.Commands[0].Path[0] = "mutated"
	first.Commands[0].Flags[0].Name = "mutated"
	first.Commands[0].InputSchema[0] = '!'
	first.Commands[0].OutputSchema[0] = '!'

	second := candidate.Spec()
	if second.Descriptor.Provides[0].ID != "generation.sdk-python-assets" ||
		second.Commands[0].Path[0] != "generation" ||
		second.Commands[0].Flags[0].Name != "repo-root" ||
		second.Commands[0].InputSchema[0] != '{' ||
		second.Commands[0].OutputSchema[0] != '{' {
		t.Fatalf("plugin spec was mutated through caller-owned slices: %#v", second)
	}
}

type commandContract struct {
	action     string
	flags      []string
	sideEffect plugin.SideEffect
}

type inertWheelBuilder struct{}

func (inertWheelBuilder) Build(context.Context, sdkpythonassets.WheelBuildRequest) (sdkpythonassets.WheelBuildOutput, error) {
	panic("wheel builder must not run during plugin construction")
}

type pointerWheelBuilder struct{}

func (*pointerWheelBuilder) Build(context.Context, sdkpythonassets.WheelBuildRequest) (sdkpythonassets.WheelBuildOutput, error) {
	panic("typed nil wheel builder must not be invoked")
}

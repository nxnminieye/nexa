package host

import (
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/spf13/cobra"
)

type ownedCommand struct {
	pluginID string
	command  plugin.CommandSpec
}

func (h *Host) commands() []ownedCommand {
	commands := builtinCommands(h)
	for _, spec := range h.specs {
		for _, command := range spec.Commands {
			commands = append(commands, ownedCommand{pluginID: spec.Descriptor.ID, command: command})
		}
	}
	sort.Slice(commands, func(i, j int) bool {
		return comparePath(commands[i].command.Path, commands[j].command.Path) < 0
	})
	return commands
}

func builtinCommands(h *Host) []ownedCommand {
	inspectHandler := plugin.Handler(nil)
	versionHandler := plugin.Handler(nil)
	if h != nil {
		inspectHandler = func(context.Context, plugin.Invocation) (any, error) {
			return h.Inspect(), nil
		}
		versionHandler = func(context.Context, plugin.Invocation) (any, error) {
			return binaryInspection{Name: h.name, Version: h.version}, nil
		}
	}
	return []ownedCommand{
		{
			pluginID: "nexactl.host",
			command: plugin.CommandSpec{
				Path:         []string{"inspect"},
				Summary:      "inspect the compiled host composition",
				InputSchema:  json.RawMessage(`{"type":"object"}`),
				OutputSchema: json.RawMessage(`{"type":"object"}`),
				SideEffect:   plugin.SideEffectNone,
				Run:          inspectHandler,
			},
		},
		{
			pluginID: "nexactl.host",
			command: plugin.CommandSpec{
				Path:         []string{"version"},
				Summary:      "show the host version",
				InputSchema:  json.RawMessage(`{"type":"object"}`),
				OutputSchema: json.RawMessage(`{"type":"object"}`),
				SideEffect:   plugin.SideEffectNone,
				Run:          versionHandler,
			},
		},
	}
}

func globalFlags() []plugin.FlagSpec {
	return []plugin.FlagSpec{
		{
			Name:    "help",
			Type:    plugin.FlagBool,
			Summary: "show command help",
			Default: json.RawMessage(`false`),
		},
		{
			Name:    "json",
			Type:    plugin.FlagBool,
			Summary: "emit compact JSON",
			Default: json.RawMessage(`false`),
		},
	}
}

type flagBinding struct {
	name     string
	required bool
	read     func() any
}

func (h *Host) newCommandTree(state *executionState, humanOutput io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:                h.name,
		Short:              "Nexa repository control plane",
		SilenceErrors:      true,
		SilenceUsage:       true,
		DisableSuggestions: true,
		RunE: func(command *cobra.Command, args []string) error {
			if len(args) != 0 {
				return usageError("command_not_found", "command was not found")
			}
			return command.Help()
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(humanOutput)
	root.SetErr(io.Discard)
	root.SetHelpCommand(&cobra.Command{Use: "__nexactl_internal_help", Hidden: true})
	setStableFlagError(root)

	var compact bool
	root.PersistentFlags().BoolVar(&compact, "json", false, "emit compact JSON")

	nodes := map[string]*cobra.Command{"": root}
	for _, owned := range h.commands() {
		parent := root
		for index, token := range owned.command.Path {
			key := strings.Join(owned.command.Path[:index+1], "\x00")
			command, exists := nodes[key]
			if !exists {
				command = &cobra.Command{Use: token}
				setStableFlagError(command)
				parent.AddCommand(command)
				nodes[key] = command
			}
			parent = command
			if index == len(owned.command.Path)-1 {
				configureExecutableCommand(command, owned.command, state)
			}
		}
	}
	return root
}

func configureExecutableCommand(command *cobra.Command, spec plugin.CommandSpec, state *executionState) {
	command.Short = spec.Summary
	bindings := make([]flagBinding, 0, len(spec.Flags))
	for _, flag := range spec.Flags {
		bindings = append(bindings, bindFlag(command, flag))
	}
	command.RunE = func(command *cobra.Command, args []string) error {
		if err := command.Context().Err(); err != nil {
			return canceledError()
		}
		for _, binding := range bindings {
			if binding.required && !command.Flags().Changed(binding.name) {
				return usageError("flag_required", "a required command flag is missing")
			}
		}

		flags := make(map[string]any, len(bindings))
		for _, binding := range bindings {
			flags[binding.name] = binding.read()
		}
		state.invoked = true
		result, err, diagnostic := invokeHandler(
			command.Context(),
			spec.Run,
			plugin.Invocation{Args: append([]string(nil), args...), Flags: flags},
		)
		state.result = result
		state.diagnostic = diagnostic
		return normalizeExecutionError(err)
	}
}

func bindFlag(command *cobra.Command, spec plugin.FlagSpec) flagBinding {
	binding := flagBinding{name: spec.Name, required: spec.Required}
	switch spec.Type {
	case plugin.FlagString:
		var value string
		if len(spec.Default) != 0 {
			_ = json.Unmarshal(spec.Default, &value)
		}
		command.Flags().StringVar(&value, spec.Name, value, spec.Summary)
		binding.read = func() any { return value }
	case plugin.FlagBool:
		var value bool
		if len(spec.Default) != 0 {
			_ = json.Unmarshal(spec.Default, &value)
		}
		command.Flags().BoolVar(&value, spec.Name, value, spec.Summary)
		binding.read = func() any { return value }
	case plugin.FlagInt:
		var value int
		if len(spec.Default) != 0 {
			_ = json.Unmarshal(spec.Default, &value)
		}
		command.Flags().IntVar(&value, spec.Name, value, spec.Summary)
		binding.read = func() any { return value }
	case plugin.FlagStringSlice:
		var value []string
		if len(spec.Default) != 0 {
			_ = json.Unmarshal(spec.Default, &value)
		}
		value = append([]string(nil), value...)
		command.Flags().StringSliceVar(&value, spec.Name, value, spec.Summary)
		binding.read = func() any { return append([]string(nil), value...) }
	}
	return binding
}

func setStableFlagError(command *cobra.Command) {
	command.SetFlagErrorFunc(func(*cobra.Command, error) error {
		return usageError("flag_invalid", "command flags are invalid")
	})
}

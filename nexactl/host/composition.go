package host

import (
	"reflect"
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	"golang.org/x/mod/semver"
)

type capabilityOwner struct {
	pluginID   string
	capability plugin.Capability
}

func New(options Options, plugins ...plugin.Plugin) (*Host, error) {
	name := options.Name
	if name == "" {
		name = "nexactl"
	}
	if !validLowerKebabToken(name) {
		return nil, constructionError("host_name_invalid", "host name must be a lower kebab token")
	}
	if !semver.IsValid(options.Version) {
		return nil, constructionError("host_version_invalid", "host version must be a valid semantic version")
	}

	specs := make([]plugin.Spec, len(plugins))
	for i, candidate := range plugins {
		if isNil(candidate) {
			return nil, constructionError("plugin_invalid", "plugin must not be nil")
		}
		spec, ok := readPluginSpec(candidate)
		if !ok || plugin.ValidateSpec(spec) != nil {
			return nil, constructionError("plugin_invalid", "plugin specification is invalid")
		}
		specs[i] = cloneSpec(spec)
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].Descriptor.ID < specs[j].Descriptor.ID
	})

	if err := validatePluginIDs(specs); err != nil {
		return nil, err
	}
	if err := validateCommandComposition(specs); err != nil {
		return nil, err
	}
	if err := validateCapabilityComposition(specs); err != nil {
		return nil, err
	}

	operationIDs := options.OperationIDs
	if isNil(operationIDs) {
		operationIDs = protocol.RandomOperationIDGenerator{}
	}
	return &Host{
		name:         name,
		version:      options.Version,
		operationIDs: operationIDs,
		specs:        specs,
	}, nil
}

func validatePluginIDs(specs []plugin.Spec) error {
	for i := 1; i < len(specs); i++ {
		if specs[i-1].Descriptor.ID == specs[i].Descriptor.ID {
			return constructionError("plugin_id_conflict", "plugin IDs must be unique")
		}
	}
	return nil
}

func validateCommandComposition(specs []plugin.Spec) error {
	commands := builtinCommands(nil)
	for _, spec := range specs {
		for _, command := range spec.Commands {
			for _, flag := range command.Flags {
				if flag.Name == "json" || flag.Name == "help" {
					return constructionError("flag_conflict", "plugin command flags must not redefine host flags")
				}
			}
			commands = append(commands, ownedCommand{pluginID: spec.Descriptor.ID, command: command})
		}
	}

	sort.Slice(commands, func(i, j int) bool {
		return comparePath(commands[i].command.Path, commands[j].command.Path) < 0
	})
	for i := 0; i < len(commands); i++ {
		for j := i + 1; j < len(commands); j++ {
			if pathsConflict(commands[i].command.Path, commands[j].command.Path) {
				return constructionError("command_conflict", "executable command paths must not overlap")
			}
		}
	}
	return nil
}

func validateCapabilityComposition(specs []plugin.Spec) error {
	owners := make(map[string]capabilityOwner)
	for _, spec := range specs {
		for _, provided := range spec.Descriptor.Provides {
			if _, exists := owners[provided.ID]; exists {
				return constructionError("capability_conflict", "provided capabilities must have one owner")
			}
			owners[provided.ID] = capabilityOwner{pluginID: spec.Descriptor.ID, capability: provided}
		}
	}

	dependencies := make(map[string][]string, len(specs))
	for _, spec := range specs {
		dependencies[spec.Descriptor.ID] = nil
		for _, required := range spec.Descriptor.Requires {
			owner, exists := owners[required.ID]
			if !exists {
				return constructionError("plugin_dependency_missing", "a required plugin capability is missing")
			}
			if semver.Major(owner.capability.Version) != semver.Major(required.Version) ||
				semver.Compare(owner.capability.Version, required.Version) < 0 {
				return constructionError("plugin_dependency_incompatible", "a required plugin capability is incompatible")
			}
			dependencies[spec.Descriptor.ID] = append(dependencies[spec.Descriptor.ID], owner.pluginID)
		}
		sort.Strings(dependencies[spec.Descriptor.ID])
	}

	if dependencyCycle(dependencies) {
		return constructionError("plugin_dependency_cycle", "plugin dependencies must be acyclic")
	}
	return nil
}

func dependencyCycle(graph map[string][]string) bool {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(graph))
	ids := make([]string, 0, len(graph))
	for id := range graph {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var visit func(string) bool
	visit = func(id string) bool {
		switch state[id] {
		case visiting:
			return true
		case visited:
			return false
		}
		state[id] = visiting
		for _, dependency := range graph[id] {
			if visit(dependency) {
				return true
			}
		}
		state[id] = visited
		return false
	}
	for _, id := range ids {
		if visit(id) {
			return true
		}
	}
	return false
}

func pathsConflict(first, second []string) bool {
	shorter := len(first)
	if len(second) < shorter {
		shorter = len(second)
	}
	for i := 0; i < shorter; i++ {
		if first[i] != second[i] {
			return false
		}
	}
	return true
}

func comparePath(first, second []string) int {
	limit := len(first)
	if len(second) < limit {
		limit = len(second)
	}
	for i := 0; i < limit; i++ {
		if first[i] < second[i] {
			return -1
		}
		if first[i] > second[i] {
			return 1
		}
	}
	switch {
	case len(first) < len(second):
		return -1
	case len(first) > len(second):
		return 1
	default:
		return 0
	}
}

func validLowerKebabToken(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") || strings.Contains(value, "--") {
		return false
	}
	for _, r := range value {
		if r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') {
			continue
		}
		return false
	}
	return true
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func readPluginSpec(candidate plugin.Plugin) (spec plugin.Spec, ok bool) {
	ok = true
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	spec = candidate.Spec()
	return spec, ok
}

func cloneSpec(spec plugin.Spec) plugin.Spec {
	cloned := spec
	cloned.Descriptor.Provides = cloneCapabilities(spec.Descriptor.Provides)
	cloned.Descriptor.Requires = cloneCapabilities(spec.Descriptor.Requires)
	cloned.Commands = make([]plugin.CommandSpec, len(spec.Commands))
	for i, command := range spec.Commands {
		cloned.Commands[i] = command
		cloned.Commands[i].Path = append([]string(nil), command.Path...)
		cloned.Commands[i].Flags = make([]plugin.FlagSpec, len(command.Flags))
		for j, flag := range command.Flags {
			cloned.Commands[i].Flags[j] = flag
			cloned.Commands[i].Flags[j].Default = append([]byte(nil), flag.Default...)
		}
		cloned.Commands[i].InputSchema = append([]byte(nil), command.InputSchema...)
		cloned.Commands[i].OutputSchema = append([]byte(nil), command.OutputSchema...)
		cloned.Commands[i].DelegatedTools = cloneDelegatedTools(command.DelegatedTools)
	}
	return cloned
}

func cloneCapabilities(capabilities []plugin.Capability) []plugin.Capability {
	return append([]plugin.Capability(nil), capabilities...)
}

func constructionError(code, message string) error {
	return protocol.NewError(code, "nexactl.host", protocol.CategoryInput, message, "")
}

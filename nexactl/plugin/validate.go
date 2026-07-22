package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nxnminieye/nexa/cli/protocol"
	"golang.org/x/mod/semver"
)

const validationDomain = "nexactl.plugin"

var lowerKebabToken = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func ValidateSpec(spec Spec) error {
	if !validDottedID(spec.Descriptor.ID, false) {
		return validationError("plugin_id_invalid", "plugin ID must contain lower kebab segments separated by dots")
	}
	if !semver.IsValid(spec.Descriptor.Version) {
		return validationError("plugin_version_invalid", "plugin version must be a valid semantic version")
	}
	if spec.Descriptor.ContractVersion != ContractVersion {
		return validationError("contract_version_invalid", "plugin contract version is unsupported")
	}
	if err := validateCapabilities(spec.Descriptor.Provides); err != nil {
		return err
	}
	if err := validateCapabilities(spec.Descriptor.Requires); err != nil {
		return err
	}

	commandPaths := make(map[string]struct{}, len(spec.Commands))
	for i, command := range spec.Commands {
		if len(command.Path) == 0 {
			return validationError("command_path_invalid", fmt.Sprintf("command %d must have a path", i))
		}
		for _, token := range command.Path {
			if !lowerKebabToken.MatchString(token) {
				return validationError("command_path_invalid", fmt.Sprintf("command %d path must contain lower kebab tokens", i))
			}
		}

		pathKey := strings.Join(command.Path, "\x00")
		if _, exists := commandPaths[pathKey]; exists {
			return validationError("command_duplicate", fmt.Sprintf("command path %q is duplicated", strings.Join(command.Path, " ")))
		}
		commandPaths[pathKey] = struct{}{}

		if err := validateFlags(command.Flags); err != nil {
			return err
		}
		if err := validateDelegatedTools(command.DelegatedTools); err != nil {
			return err
		}
		if !validOptionalJSON(command.InputSchema) || !validOptionalJSON(command.OutputSchema) {
			return validationError("command_schema_invalid", fmt.Sprintf("command %q schema metadata must be valid JSON when provided", strings.Join(command.Path, " ")))
		}
		if !validSideEffect(command.SideEffect) {
			return validationError("side_effect_invalid", fmt.Sprintf("command %q has an unsupported side effect", strings.Join(command.Path, " ")))
		}
		if command.Run == nil {
			return validationError("command_handler_missing", fmt.Sprintf("command %q must have a handler", strings.Join(command.Path, " ")))
		}
	}

	return nil
}

func validateDelegatedTools(tools []DelegatedToolSpec) error {
	ids := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if !validDottedID(tool.ID, false) {
			return validationError("delegated_tool_id_invalid", "delegated tool ID must contain lower kebab segments separated by dots")
		}
		if !validDelegatedValue(tool.Version, 256) || !validPinnedToolVersion(tool.Version) {
			return validationError("delegated_tool_version_invalid", fmt.Sprintf("delegated tool %q version is invalid", tool.ID))
		}
		if _, duplicate := ids[tool.ID]; duplicate {
			return validationError("delegated_tool_duplicate", fmt.Sprintf("delegated tool %q is duplicated", tool.ID))
		}
		ids[tool.ID] = struct{}{}
	}
	return nil
}

func validPinnedToolVersion(value string) bool {
	if !semver.IsValid(value) {
		return false
	}
	core := strings.TrimPrefix(value, "v")
	if end := strings.IndexAny(core, "-+"); end >= 0 {
		core = core[:end]
	}
	return strings.Count(core, ".") == 2
}

func validDelegatedValue(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validateCapabilities(capabilities []Capability) error {
	ids := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if !validDottedID(capability.ID, true) {
			return validationError("capability_id_invalid", "capability ID must contain unversioned lower kebab segments separated by dots")
		}
		if !semver.IsValid(capability.Version) {
			return validationError("capability_version_invalid", fmt.Sprintf("capability %q version must be a valid semantic version", capability.ID))
		}
		if _, exists := ids[capability.ID]; exists {
			return validationError("capability_duplicate", fmt.Sprintf("capability %q is duplicated", capability.ID))
		}
		ids[capability.ID] = struct{}{}
	}

	return nil
}

func validDottedID(id string, rejectVersionSegments bool) bool {
	if id == "" {
		return false
	}

	for _, segment := range strings.Split(id, ".") {
		if !lowerKebabToken.MatchString(segment) {
			return false
		}
		if rejectVersionSegments && semver.IsValid(segment) {
			return false
		}
	}
	return true
}

func validateFlags(flags []FlagSpec) error {
	names := make(map[string]struct{}, len(flags))
	for _, flag := range flags {
		if !lowerKebabToken.MatchString(flag.Name) {
			return validationError("flag_name_invalid", "flag name must be a lower kebab token")
		}
		if _, exists := names[flag.Name]; exists {
			return validationError("flag_duplicate", fmt.Sprintf("flag %q is duplicated", flag.Name))
		}
		names[flag.Name] = struct{}{}

		if !validFlagType(flag.Type) {
			return validationError("flag_type_invalid", fmt.Sprintf("flag %q has an unsupported type", flag.Name))
		}
		if len(flag.Default) != 0 && !defaultMatchesType(flag.Default, flag.Type) {
			return validationError("flag_default_invalid", fmt.Sprintf("flag %q default does not match its type", flag.Name))
		}
	}

	return nil
}

func validFlagType(flagType FlagType) bool {
	switch flagType {
	case FlagString, FlagBool, FlagInt, FlagStringSlice:
		return true
	default:
		return false
	}
}

func defaultMatchesType(raw json.RawMessage, flagType FlagType) bool {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false
	}

	switch flagType {
	case FlagString:
		var value string
		return json.Unmarshal(raw, &value) == nil
	case FlagBool:
		var value bool
		return json.Unmarshal(raw, &value) == nil
	case FlagInt:
		var value int
		return json.Unmarshal(raw, &value) == nil
	case FlagStringSlice:
		var value []string
		return json.Unmarshal(raw, &value) == nil
	default:
		return false
	}
}

func validOptionalJSON(raw json.RawMessage) bool {
	return raw == nil || json.Valid(raw)
}

func validSideEffect(sideEffect SideEffect) bool {
	switch sideEffect {
	case SideEffectNone, SideEffectRepositoryRead, SideEffectRepositoryWrite:
		return true
	default:
		return false
	}
}

func validationError(code, message string) error {
	return protocol.NewError(code, validationDomain, protocol.CategoryInput, message, "")
}

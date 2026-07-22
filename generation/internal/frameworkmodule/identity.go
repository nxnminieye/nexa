package frameworkmodule

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const modulePath = "github.com/nxnminieye/nexa"

type ReplacementKind string

const (
	ReplacementNone    ReplacementKind = "none"
	ReplacementVersion ReplacementKind = "version"
	ReplacementLocal   ReplacementKind = "local"
)

type IdentitySpec struct {
	Module                              buildinput.ModuleRequirement
	ReplacementKind                     ReplacementKind
	ReplacementPath, ReplacementVersion string
	LocalPath                           string
}

type identityState struct {
	module                              buildinput.ModuleRequirement
	kind                                ReplacementKind
	replacementPath, replacementVersion string
	localPath                           string
}

type Identity struct{ state *identityState }

type Error struct {
	code, stage, reason, pointer string
	sentinel                     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return "framework module identity is invalid"
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.sentinel
}
func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}
func (e *Error) Stage() string {
	if e == nil {
		return ""
	}
	return e.stage
}
func (e *Error) Reason() string {
	if e == nil {
		return ""
	}
	return e.reason
}
func (e *Error) Pointer() string {
	if e == nil {
		return ""
	}
	return e.pointer
}

func identityError(reason, pointer string) *Error {
	return &Error{
		code: "framework_identity_invalid", stage: "framework-identity", reason: reason, pointer: pointer,
		sentinel: errors.New("framework_identity_invalid: " + reason),
	}
}

func Select(info *debug.BuildInfo) (Identity, error) {
	if info == nil {
		return Identity{}, identityError("build_info_unavailable", "/framework/buildInfo")
	}
	if info.Main.Path == modulePath {
		return identityFromBuildModule(info.Main)
	}
	candidates := make([]debug.Module, 0, 1)
	for _, dependency := range info.Deps {
		if dependency != nil && dependency.Path == modulePath {
			candidates = append(candidates, *dependency)
		}
	}
	if len(candidates) == 0 {
		return Identity{}, identityError("framework_module_missing", "/framework/module/path")
	}
	if len(candidates) != 1 {
		return Identity{}, identityError("framework_module_ambiguous", "/framework/module/path")
	}
	return identityFromBuildModule(candidates[0])
}

func identityFromBuildModule(selected debug.Module) (Identity, error) {
	if selected.Version == "(devel)" {
		return Identity{}, identityError("framework_module_devel", "/framework/module/version")
	}
	spec := IdentitySpec{Module: buildinput.ModuleRequirement{Path: selected.Path, Version: selected.Version}, ReplacementKind: ReplacementNone}
	if selected.Replace != nil {
		if selected.Replace.Version != "" && selected.Replace.Version != "(devel)" {
			spec.ReplacementKind = ReplacementVersion
			spec.ReplacementPath = selected.Replace.Path
			spec.ReplacementVersion = selected.Replace.Version
		} else {
			spec.ReplacementKind = ReplacementLocal
			spec.LocalPath = selected.Replace.Path
		}
	}
	return NewIdentity(spec)
}

func NewIdentity(spec IdentitySpec) (Identity, error) {
	if spec.Module.Path != modulePath {
		return Identity{}, identityError("framework_module_missing", "/framework/module/path")
	}
	if !semver.IsValid(spec.Module.Version) || semver.Canonical(spec.Module.Version) != spec.Module.Version || module.Check(spec.Module.Path, spec.Module.Version) != nil {
		return Identity{}, identityError("framework_module_version_invalid", "/framework/module/version")
	}
	state := &identityState{module: spec.Module, kind: spec.ReplacementKind}
	switch spec.ReplacementKind {
	case ReplacementNone:
		if spec.ReplacementPath != "" || spec.ReplacementVersion != "" || spec.LocalPath != "" {
			return Identity{}, identityError("framework_replacement_invalid", "/framework/module/replacement")
		}
	case ReplacementVersion:
		if spec.LocalPath != "" || !semver.IsValid(spec.ReplacementVersion) || semver.Canonical(spec.ReplacementVersion) != spec.ReplacementVersion || module.Check(spec.ReplacementPath, spec.ReplacementVersion) != nil {
			return Identity{}, identityError("framework_replacement_invalid", "/framework/module/replacement")
		}
		state.replacementPath, state.replacementVersion = spec.ReplacementPath, spec.ReplacementVersion
	case ReplacementLocal:
		if spec.ReplacementPath != "" || spec.ReplacementVersion != "" {
			return Identity{}, identityError("framework_replacement_invalid", "/framework/module/replacement")
		}
		local, err := canonicalDirectory(spec.LocalPath)
		if err != nil || local != spec.LocalPath {
			return Identity{}, identityError("framework_replacement_invalid", "/framework/module/replacement/localPath")
		}
		state.localPath = local
	default:
		return Identity{}, identityError("framework_replacement_invalid", "/framework/module/replacement")
	}
	return Identity{state: state}, nil
}

func (i Identity) Module() (buildinput.ModuleRequirement, error) {
	if i.state == nil {
		return buildinput.ModuleRequirement{}, identityError("framework_identity_state_invalid", "/framework")
	}
	return i.state.module, nil
}

func (i Identity) Replacement() (ReplacementKind, string, string, string, error) {
	if i.state == nil {
		return "", "", "", "", identityError("framework_identity_state_invalid", "/framework")
	}
	return i.state.kind, i.state.replacementPath, i.state.replacementVersion, i.state.localPath, nil
}

func canonicalDirectory(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", os.ErrInvalid
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", os.ErrInvalid
	}
	return filepath.Clean(canonical), nil
}

package sdkpython

import (
	"context"

	"github.com/nxnminieye/nexa/internal/sdkpythonassets"
	"github.com/nxnminieye/nexa/nexactl/plugin"
)

var (
	writeFlags = []plugin.FlagSpec{
		{Name: "repo-root", Type: plugin.FlagString, Summary: "Absolute Nexa repository root", Required: true},
	}
	checkFlags = []plugin.FlagSpec{
		{Name: "repo-root", Type: plugin.FlagString, Summary: "Absolute Nexa repository root", Required: true},
	}
	buildFlags = []plugin.FlagSpec{
		{Name: "repo-root", Type: plugin.FlagString, Summary: "Absolute Nexa repository root", Required: true},
		{Name: "python", Type: plugin.FlagString, Summary: "Absolute Python interpreter", Required: true},
		{Name: "matrix-target", Type: plugin.FlagString, Summary: "Python SDK build matrix target", Required: true},
		{Name: "wheelhouse", Type: plugin.FlagString, Summary: "Absolute offline wheelhouse root", Required: true},
		{Name: "work-dir", Type: plugin.FlagString, Summary: "Absolute Python SDK build work root", Required: true},
		{Name: "out", Type: plugin.FlagString, Summary: "Absolute Python SDK wheel output root", Required: true},
	}
)

func (a *adapter) commands() []plugin.CommandSpec {
	commands := []plugin.CommandSpec{
		a.command("write", writeFlags, a.schemas.writeInput, a.schemas.writeOutput, plugin.SideEffectRepositoryWrite),
		a.command("check", checkFlags, a.schemas.checkInput, a.schemas.checkOutput, plugin.SideEffectRepositoryRead),
	}
	if a.hasBuild {
		commands = append(commands, a.command("build", buildFlags, a.schemas.buildInput, a.schemas.buildOutput, plugin.SideEffectRepositoryWrite))
	}
	return commands
}

func (a *adapter) command(action string, flags []plugin.FlagSpec, input, output []byte, sideEffect plugin.SideEffect) plugin.CommandSpec {
	var handler plugin.Handler
	switch action {
	case "write":
		handler = a.write
	case "check":
		handler = a.check
	case "build":
		handler = a.build
	}
	return plugin.CommandSpec{
		Path:         []string{"generation", "sdk-python-assets", action},
		Summary:      "Python SDK assets " + action,
		Flags:        append([]plugin.FlagSpec(nil), flags...),
		InputSchema:  append([]byte(nil), input...),
		OutputSchema: append([]byte(nil), output...),
		SideEffect:   sideEffect,
		Run:          handler,
	}
}

func (a *adapter) write(ctx context.Context, invocation plugin.Invocation) (any, error) {
	values, err := parseInvocation(invocation, []invocationMember{
		{name: "repo-root", reason: sdkpythonassets.ReasonRepoRootInvalid, pointer: "/repo-root"},
	}, "unchanged")
	if err != nil {
		return nil, err
	}
	result, err := a.owner.Write(ctx, sdkpythonassets.WriteRequest{
		RepoRoot: values[0],
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (a *adapter) check(ctx context.Context, invocation plugin.Invocation) (any, error) {
	values, err := parseInvocation(invocation, []invocationMember{
		{name: "repo-root", reason: sdkpythonassets.ReasonRepoRootInvalid, pointer: "/repo-root"},
	}, "read-only")
	if err != nil {
		return nil, err
	}
	result, err := a.owner.Check(ctx, sdkpythonassets.CheckRequest{
		RepoRoot: values[0], Mode: sdkpythonassets.SourceTreeMode,
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (a *adapter) build(ctx context.Context, invocation plugin.Invocation) (any, error) {
	values, err := parseInvocation(invocation, []invocationMember{
		{name: "repo-root", reason: sdkpythonassets.ReasonRepoRootInvalid, pointer: "/repo-root"},
		{name: "python", reason: sdkpythonassets.ReasonPythonPathInvalid, pointer: "/python"},
		{name: "matrix-target", reason: sdkpythonassets.ReasonMatrixTargetInvalid, pointer: "/matrix-target"},
		{name: "wheelhouse", reason: sdkpythonassets.ReasonWheelhousePathInvalid, pointer: "/wheelhouse"},
		{name: "work-dir", reason: sdkpythonassets.ReasonWorkDirInvalid, pointer: "/work-dir"},
		{name: "out", reason: sdkpythonassets.ReasonOutPathInvalid, pointer: "/out"},
	}, "read-only")
	if err != nil {
		return nil, err
	}
	result, err := a.owner.Build(ctx, sdkpythonassets.BuildRequest{
		RepoRoot: values[0], Python: values[1], MatrixTarget: values[2],
		Wheelhouse: values[3], WorkDir: values[4], Out: values[5],
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type invocationMember struct {
	name, reason, pointer string
}

func parseInvocation(invocation plugin.Invocation, members []invocationMember, state string) ([]string, error) {
	if len(invocation.Args) != 0 {
		return nil, inputError(members[0], state)
	}
	values := make([]string, len(members))
	for index, member := range members {
		value, exists := invocation.Flags[member.name]
		if !exists {
			return nil, inputError(member, state)
		}
		text, ok := value.(string)
		if !ok {
			return nil, inputError(member, state)
		}
		values[index] = text
	}
	if len(invocation.Flags) != len(members) {
		return nil, inputError(members[0], state)
	}
	return values, nil
}

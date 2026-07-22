package source

import (
	"context"

	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin/engine"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

var selectionFlagSpecs = []plugin.FlagSpec{
	{Name: "repo-root", Type: plugin.FlagString, Summary: "Absolute consumer repository root", Required: true},
	{Name: "provider", Type: plugin.FlagString, Summary: "Source provider ID", Required: true},
	{Name: "version", Type: plugin.FlagString, Summary: "Exact source release version", Required: true},
	{Name: "profile", Type: plugin.FlagString, Summary: "Source profile ID", Required: true},
	{Name: "target", Type: plugin.FlagString, Summary: "Repository-relative target", Required: true},
	{Name: "manifest-digest", Type: plugin.FlagString, Summary: "Exact manifest digest", Required: true},
	{Name: "tree-digest", Type: plugin.FlagString, Summary: "Exact tree digest", Required: true},
}

var managedFlagSpecs = []plugin.FlagSpec{
	{Name: "repo-root", Type: plugin.FlagString, Summary: "Absolute consumer repository root", Required: true},
	{Name: "provider", Type: plugin.FlagString, Summary: "Source provider ID", Required: true},
	{Name: "target", Type: plugin.FlagString, Summary: "Repository-relative target", Required: true},
}

func (owner *adapter) commands() []plugin.CommandSpec {
	return []plugin.CommandSpec{
		owner.command("plan", plugin.SideEffectRepositoryRead, selectionFlagSpecs, owner.plan),
		owner.command("materialize", plugin.SideEffectRepositoryWrite, appendExpectedDigest(selectionFlagSpecs), owner.materialize),
		owner.command("status", plugin.SideEffectRepositoryRead, managedFlagSpecs, owner.status),
		owner.command("diff", plugin.SideEffectRepositoryRead, managedFlagSpecs, owner.diff),
		owner.command("upgrade", plugin.SideEffectRepositoryWrite, appendExpectedDigest(selectionFlagSpecs), owner.upgrade),
		owner.command("detach", plugin.SideEffectRepositoryWrite, managedFlagSpecs, owner.detach),
		owner.command("check", plugin.SideEffectRepositoryRead, selectionFlagSpecs, owner.check),
	}
}

func (owner *adapter) command(action string, sideEffect plugin.SideEffect, flags []plugin.FlagSpec, handler plugin.Handler) plugin.CommandSpec {
	input, output := commandSchemas(action, owner.releaseInspections...)
	return plugin.CommandSpec{
		Path: []string{"source", action}, Summary: "Source bundle " + action,
		Flags: cloneFlagSpecs(flags), InputSchema: input, OutputSchema: output, SideEffect: sideEffect, Run: handler,
	}
}

func appendExpectedDigest(flags []plugin.FlagSpec) []plugin.FlagSpec {
	result := cloneFlagSpecs(flags)
	return append(result, plugin.FlagSpec{Name: "expected-plan-digest", Type: plugin.FlagString, Summary: "Expected plan digest"})
}

func cloneFlagSpecs(flags []plugin.FlagSpec) []plugin.FlagSpec {
	return append([]plugin.FlagSpec(nil), flags...)
}

func (owner *adapter) plan(ctx context.Context, invocation plugin.Invocation) (any, error) {
	request, err := owner.planRequest(invocation)
	if err != nil {
		return nil, err
	}
	result, err := owner.engine.Plan(ctx, request)
	if err != nil {
		return nil, projectError(err)
	}
	return projectPlan(result), nil
}

func (owner *adapter) check(ctx context.Context, invocation plugin.Invocation) (any, error) {
	request, err := owner.planRequest(invocation)
	if err != nil {
		return nil, err
	}
	result, err := owner.engine.Check(ctx, request)
	if err != nil {
		return nil, projectError(err)
	}
	return projectCheck(result), nil
}

func (owner *adapter) status(ctx context.Context, invocation plugin.Invocation) (any, error) {
	request, err := owner.managedRequest(invocation)
	if err != nil {
		return nil, err
	}
	result, err := owner.engine.Status(ctx, request)
	if err != nil {
		return nil, projectError(err)
	}
	return projectStatus(result), nil
}

func (owner *adapter) diff(ctx context.Context, invocation plugin.Invocation) (any, error) {
	request, err := owner.managedRequest(invocation)
	if err != nil {
		return nil, err
	}
	result, err := owner.engine.Diff(ctx, request)
	if err != nil {
		return nil, projectError(err)
	}
	return projectDiff(result), nil
}

func (owner *adapter) materialize(ctx context.Context, invocation plugin.Invocation) (any, error) {
	request, err := owner.planRequest(invocation)
	if err != nil {
		return nil, err
	}
	write, err := writeOptions(invocation)
	if err != nil {
		return nil, err
	}
	result, err := owner.engine.Materialize(ctx, engine.MaterializeRequest{PlanRequest: request, WriteOptions: write})
	if err != nil {
		return nil, projectError(err)
	}
	return projectResult(result), nil
}

func (owner *adapter) upgrade(ctx context.Context, invocation plugin.Invocation) (any, error) {
	request, err := owner.planRequest(invocation)
	if err != nil {
		return nil, err
	}
	write, err := writeOptions(invocation)
	if err != nil {
		return nil, err
	}
	result, err := owner.engine.Upgrade(ctx, engine.UpgradeRequest{PlanRequest: request, WriteOptions: write})
	if err != nil {
		return nil, projectError(err)
	}
	return projectResult(result), nil
}

func (owner *adapter) detach(ctx context.Context, invocation plugin.Invocation) (any, error) {
	request, err := owner.managedRequest(invocation)
	if err != nil {
		return nil, err
	}
	result, err := owner.engine.Detach(ctx, engine.DetachRequest{ManagedRequest: request})
	if err != nil {
		return nil, projectError(err)
	}
	return projectResult(result), nil
}

func (owner *adapter) planRequest(invocation plugin.Invocation) (engine.PlanRequest, error) {
	if len(invocation.Args) != 0 {
		return engine.PlanRequest{}, malformedInputError("arguments_unsupported", "/args")
	}
	providerID, err := stringFlag(invocation, "provider", true)
	if err != nil {
		return engine.PlanRequest{}, err
	}
	version, err := stringFlag(invocation, "version", true)
	if err != nil {
		return engine.PlanRequest{}, err
	}
	manifestValue, err := stringFlag(invocation, "manifest-digest", true)
	if err != nil {
		return engine.PlanRequest{}, err
	}
	treeValue, err := stringFlag(invocation, "tree-digest", true)
	if err != nil {
		return engine.PlanRequest{}, err
	}
	manifestDigest, manifestErr := provenance.ParseDigest(manifestValue)
	if manifestErr != nil {
		return engine.PlanRequest{}, malformedInputError("manifest_digest_invalid", "/manifestDigest")
	}
	treeDigest, treeErr := provenance.ParseDigest(treeValue)
	if treeErr != nil {
		return engine.PlanRequest{}, malformedInputError("tree_digest_invalid", "/treeDigest")
	}
	ref, err := owner.refFor(providerID, version, manifestDigest, treeDigest)
	if err != nil {
		return engine.PlanRequest{}, err
	}
	profile, err := stringFlag(invocation, "profile", true)
	if err != nil {
		return engine.PlanRequest{}, err
	}
	target, err := stringFlag(invocation, "target", true)
	if err != nil {
		return engine.PlanRequest{}, err
	}
	selection, err := engine.NewSelection(engine.SelectionSpec{Release: ref, ProfileID: profile, Target: target})
	if err != nil {
		return engine.PlanRequest{}, projectError(err)
	}
	repositoryRoot, err := stringFlag(invocation, "repo-root", true)
	if err != nil {
		return engine.PlanRequest{}, err
	}
	return engine.PlanRequest{RepositoryRoot: repositoryRoot, Selection: selection}, nil
}

func (owner *adapter) managedRequest(invocation plugin.Invocation) (engine.ManagedRequest, error) {
	if len(invocation.Args) != 0 {
		return engine.ManagedRequest{}, malformedInputError("arguments_unsupported", "/args")
	}
	providerID, err := stringFlag(invocation, "provider", true)
	if err != nil {
		return engine.ManagedRequest{}, err
	}
	target, err := stringFlag(invocation, "target", true)
	if err != nil {
		return engine.ManagedRequest{}, err
	}
	key, err := lock.NewKey(providerID, target)
	if err != nil {
		return engine.ManagedRequest{}, projectError(err)
	}
	repositoryRoot, err := stringFlag(invocation, "repo-root", true)
	if err != nil {
		return engine.ManagedRequest{}, err
	}
	return engine.ManagedRequest{RepositoryRoot: repositoryRoot, Key: key}, nil
}

func (owner *adapter) refFor(providerID, version string, manifestDigest, treeDigest provenance.Digest) (release.Ref, error) {
	var coordinates []release.Ref
	for _, ref := range owner.releases {
		if ref.ProviderID() == providerID && ref.Version() == version {
			coordinates = append(coordinates, ref)
			if ref.ManifestDigest() == manifestDigest && ref.TreeDigest() == treeDigest {
				return ref, nil
			}
		}
	}
	if len(coordinates) == 0 {
		return release.Ref{}, unavailableProviderError()
	}
	if len(coordinates) != 1 {
		return release.Ref{}, malformedInputError("release_ambiguous", "/provider")
	}
	candidate := coordinates[0]
	ref, err := release.NewRef(release.RefSpec{
		ProviderID: providerID, ModulePath: candidate.ModulePath(), PackagePath: candidate.PackagePath(), Version: version,
		ManifestDigest: manifestDigest, TreeDigest: treeDigest,
	})
	if err != nil {
		return release.Ref{}, projectError(err)
	}
	return ref, nil
}

func writeOptions(invocation plugin.Invocation) (engine.WriteOptions, error) {
	value, err := stringFlag(invocation, "expected-plan-digest", false)
	if err != nil {
		return engine.WriteOptions{}, err
	}
	if value == "" {
		return engine.WriteOptions{}, nil
	}
	digest, err := provenance.ParseDigest(value)
	if err != nil {
		return engine.WriteOptions{}, malformedInputError("plan_digest_invalid", "/expectedPlanDigest")
	}
	return engine.WriteOptions{ExpectedPlanDigest: digest}, nil
}

func stringFlag(invocation plugin.Invocation, name string, required bool) (string, error) {
	value, ok := invocation.Flags[name]
	if !ok {
		if required {
			return "", malformedInputError("flag_required", "/"+name)
		}
		return "", nil
	}
	text, ok := value.(string)
	if !ok || required && text == "" {
		return "", malformedInputError("flag_invalid", "/"+name)
	}
	return text, nil
}

package apigo

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/directwrite"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
	sdkapi "github.com/nxnminieye/nexa/sdk/api"
)

type DirectSpec struct {
	CoreServiceID  string
	RepositoryRoot string
	ModuleGraph    toolchain.ModuleGraph
	HTTPAPIIR      httpapi.Document
	Rendered       []composition.RenderedArtifact
	CommandScopes  []directwrite.OutputScope
	ToolScopes     []directwrite.OutputScope
}

// WriteDirect writes the complete host-owned static set before invoking the
// disjoint direct API tool. Git remains the recovery boundary.
func WriteDirect(ctx context.Context, spec DirectSpec, options DirectOptions) (APIGoResult, error) {
	if ctx == nil || options.Runner == nil || options.RepositoryRoot != "" || len(options.OutputScopes) != 0 || len(options.Tool.Args) != 0 || len(options.Tool.InputScopes) != 0 || len(options.Tool.WriteScopes) != 0 || !serviceIDPattern.MatchString(spec.CoreServiceID) {
		return APIGoResult{}, apiDirectFailure("validate-input", "request_invalid", options, nil, false)
	}
	root, err := toolchain.CanonicalRepositoryRoot(spec.RepositoryRoot)
	if err != nil {
		return APIGoResult{}, apiDirectFailure("validate-input", "request_invalid", options, err, false)
	}
	moduleIdentity, err := spec.ModuleGraph.ConsumerModule()
	if err != nil {
		return APIGoResult{}, apiDirectFailure("validate-input", "module_graph_invalid", options, err, false)
	}
	commandScopes, err := toolchain.NormalizeOutputScopes(spec.CommandScopes)
	if err != nil {
		return APIGoResult{}, apiDirectFailure("resolve-output", "scope_invalid", options, err, false)
	}
	toolScopes, _, err := toolchain.OutputScopesSubset(spec.ToolScopes, commandScopes)
	if err != nil {
		return APIGoResult{}, apiDirectFailure("resolve-output", "scope_invalid", options, err, false)
	}
	hostScopes := subtractScopes(commandScopes, toolScopes)
	if len(hostScopes) == 0 {
		return APIGoResult{}, apiDirectFailure("resolve-output", "static_scope_invalid", options, nil, false)
	}
	manifestSpec, err := httpapi.ManifestSpec(spec.HTTPAPIIR)
	if err != nil {
		return APIGoResult{}, apiDirectFailure("validate-input", "manifest_invalid", options, err, false)
	}
	manifest, err := api.NewManifest(manifestSpec)
	if err != nil {
		return APIGoResult{}, apiDirectFailure("validate-input", "manifest_invalid", options, err, false)
	}
	runtimeContract, err := sdkapi.BuildRuntimeContract(manifest)
	if err != nil {
		return APIGoResult{}, apiDirectFailure("validate-input", "runtime_contract_invalid", options, err, false)
	}
	manifestJSON, err := manifest.CanonicalJSON()
	if err != nil {
		return APIGoResult{}, apiDirectFailure("validate-input", "manifest_invalid", options, err, false)
	}
	runtimeJSON, err := runtimeContract.CanonicalJSON()
	if err != nil {
		return APIGoResult{}, apiDirectFailure("validate-input", "runtime_contract_invalid", options, err, false)
	}
	_, refs, err := normalizeOwnerSources(options.Sources, spec.HTTPAPIIR.Sources(), spec.Rendered)
	if err != nil {
		return APIGoResult{}, apiDirectFailure("validate-input", "source_closure_invalid", options, err, false)
	}
	prepared, _, err := prepareStaticArtifacts(spec.CoreServiceID, spec.Rendered, refs)
	if err != nil {
		return APIGoResult{}, apiDirectFailure("validate-input", "static_artifact_invalid", options, err, false)
	}
	if err := validateDirectModuleImports(prepared, moduleIdentity.Path); err != nil {
		return APIGoResult{}, apiDirectFailure("validate-input", "module_path_mismatch", options, err, false)
	}
	prepared = append(prepared,
		staticArtifact{id: "api-manifest." + spec.CoreServiceID, path: path.Join("backend", spec.CoreServiceID, "generated/api-manifest.json"), content: manifestJSON},
		staticArtifact{id: "runtime-contract." + spec.CoreServiceID, path: path.Join("backend", spec.CoreServiceID, "generated/runtime-contract.json"), content: runtimeJSON},
	)
	sort.Slice(prepared, func(i, j int) bool { return prepared[i].id < prepared[j].id })
	writes := make([]directwrite.OutputFile, len(prepared))
	inputs := make([]StaticInput, len(prepared))
	apiEntry := path.Join("backend", spec.CoreServiceID, "desc/generated", spec.CoreServiceID+".generated.api")
	for index, item := range prepared {
		if !insideScopes(item.path, hostScopes) {
			return APIGoResult{}, apiDirectFailure("resolve-output", "static_scope_invalid", options, nil, false)
		}
		writes[index] = directwrite.OutputFile{Path: item.path, Content: append([]byte(nil), item.content...)}
		inputs[index] = StaticInput{ID: item.id, Path: item.path, Digest: provenance.SHA256(item.content)}
	}
	irJSON, err := httpapi.CanonicalJSON(spec.HTTPAPIIR)
	if err != nil {
		return APIGoResult{}, apiDirectFailure("validate-input", "request_invalid", options, err, false)
	}
	irSource, _ := provenance.ParseDomainSource("nexa/tool/api-go-request/http-api-ir.json")
	irSnapshot, err := httpapi.ParseSnapshot(irSource, irJSON)
	if err != nil {
		return APIGoResult{}, apiDirectFailure("validate-input", "request_invalid", options, err, false)
	}
	request := APIGoRequest{APIVersion: APIGoRequestAPIVersion, Kind: APIGoRequestKind, CoreServiceID: spec.CoreServiceID, ModulePath: moduleIdentity.Path, HTTPAPIIR: irSnapshot, APIEntry: apiEntry, StaticInputs: inputs, OutputScopes: toolScopes}
	if _, err := CanonicalAPIGoRequest(request); err != nil {
		return APIGoResult{}, apiDirectFailure("validate-input", "request_invalid", options, err, false)
	}
	excludedStatic := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		excludedStatic[input.Path] = struct{}{}
	}
	manual, err := snapshotManualScopeFilesExcept(root, commandScopes, excludedStatic)
	if err != nil {
		return APIGoResult{}, apiDirectFailure("validate-input", "repository_invalid", options, err, false)
	}
	report, err := directwrite.Write(ctx, root, directwrite.MutationSet{Scopes: hostScopes, Writes: writes})
	hostEvidence := directwrite.ChangeEvidenceComplete
	for _, scope := range hostScopes {
		if scope.Mode == directwrite.OutputModeReplaceTree {
			hostEvidence = directwrite.ChangeEvidenceHostOnly
		}
	}
	if err != nil {
		var typed *directwrite.Error
		if errors.As(err, &typed) {
			report, hostEvidence = typed.Report(), typed.ChangeEvidence()
		}
		return APIGoResult{}, apiDirectFailureWithReport("write", "static_write_failed", options, err, false, report, hostEvidence)
	}
	for _, input := range inputs {
		content, readErr := readStaticInput(root, input.Path)
		if readErr != nil || provenance.SHA256(content) != input.Digest {
			return APIGoResult{}, apiDirectFailureWithReport("post-validate", "static_input_changed", options, readErr, false, report, hostEvidence)
		}
	}
	tool := options.Tool
	tool.InputScopes, tool.WriteScopes = staticInputPaths(inputs), apiScopePaths(toolScopes)
	sort.Strings(tool.InputScopes)
	result, err := RunDirectAPIGo(ctx, request, DirectOptions{RepositoryRoot: root, Tool: tool, Runner: options.Runner, Environment: options.Environment, OutputScopes: commandScopes})
	if err != nil {
		return APIGoResult{}, apiDirectFailureWithReport("invoke-tool", "tool_failed", options, err, true, report, hostEvidence)
	}
	allowedStatic := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		allowedStatic[input.Path] = struct{}{}
	}
	if err := rejectNewUnmarkedFilesExcept(root, commandScopes, manual, allowedStatic); err != nil {
		return APIGoResult{}, apiDirectFailureWithReport("post-validate", "artifact_invalid", options, err, true, report, hostEvidence)
	}
	if err := verifyManualScopeFiles(root, manual); err != nil {
		return APIGoResult{}, apiDirectFailureWithReport("post-validate", "artifact_invalid", options, err, true, report, hostEvidence)
	}
	return result, nil
}

func subtractScopes(complete, subset []directwrite.OutputScope) []directwrite.OutputScope {
	selected := make(map[directwrite.OutputScope]struct{}, len(subset))
	for _, scope := range subset {
		selected[scope] = struct{}{}
	}
	result := make([]directwrite.OutputScope, 0, len(complete)-len(subset))
	for _, scope := range complete {
		if _, ok := selected[scope]; !ok {
			result = append(result, scope)
		}
	}
	return result
}

func insideScopes(name string, scopes []directwrite.OutputScope) bool {
	for _, scope := range scopes {
		if strings.HasPrefix(name, scope.Path+"/") {
			return true
		}
	}
	return false
}

func validateDirectModuleImports(values []staticArtifact, modulePath string) error {
	for _, value := range values {
		if path.Ext(value.path) != ".go" {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), value.path, value.content, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range parsed.Imports {
			name := strings.Trim(imported.Path.Value, "\"")
			if strings.Contains(name, "/backend/") && !strings.HasPrefix(name, strings.TrimSuffix(modulePath, "/")+"/") {
				return errors.New("rendered Go import does not use the consumer module")
			}
		}
	}
	return nil
}

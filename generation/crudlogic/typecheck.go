package crudlogic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

func Validate(ctx context.Context, plan Plan, input ValidationInput) (ValidatedPlan, error) {
	if ctx == nil || plan.state == nil || input.Runner == nil || !validToolIdentity(input.RPCGoTool) {
		return ValidatedPlan{}, invalid("validation_input_invalid", "/validation", nil)
	}
	if err := ctx.Err(); err != nil {
		return ValidatedPlan{}, err
	}
	goExecutable, provider, providerValues, err := validateGoProvider(input.GoTool, input.Environment)
	if err != nil {
		return ValidatedPlan{}, err
	}
	repository, err := canonicalDir(input.RepositoryRoot)
	if err != nil {
		return ValidatedPlan{}, invalid("repository_invalid", "/validation/repositoryRoot", err)
	}
	staging, err := canonicalDir(input.StagingRoot)
	if err != nil {
		return ValidatedPlan{}, invalid("staging_invalid", "/validation/stagingRoot", err)
	}
	if overlaps(repository, staging) {
		return ValidatedPlan{}, invalid("staging_invalid", "/validation/stagingRoot", nil)
	}
	entries, err := os.ReadDir(staging)
	if err != nil || len(entries) != 0 {
		return ValidatedPlan{}, invalid("staging_invalid", "/validation/stagingRoot", err)
	}
	resolvedPlan, module, err := resolveServiceImport(plan.state, repository)
	if err != nil {
		return ValidatedPlan{}, err
	}
	if len(resolvedPlan.protoContent) != 0 {
		protoTarget := filepath.Join(staging, filepath.FromSlash(resolvedPlan.protoPath))
		if err := os.MkdirAll(filepath.Dir(protoTarget), 0o700); err != nil {
			return ValidatedPlan{}, invalid("staging_write_failed", "/validation/stagingRoot", err)
		}
		if err := os.WriteFile(protoTarget, resolvedPlan.protoContent, 0o600); err != nil {
			return ValidatedPlan{}, invalid("staging_write_failed", "/validation/stagingRoot", err)
		}
		result, runErr := input.Runner.Run(ctx, toolchain.Request{RepositoryRoot: repository, StagingRoot: staging, WorkDir: staging, Tool: input.RPCGoTool, Args: []string{"generate", "--service", resolvedPlan.layout.ServiceID}, Environment: append([]toolchain.EnvVar(nil), input.Environment...), Stdin: append([]byte(nil), resolvedPlan.protoContent...)})
		if runErr != nil {
			return ValidatedPlan{}, invalid("rpc_go_generation_failed", "/validation/rpcGoTool", runErr)
		}
		if result.ToolID != input.RPCGoTool.ID || result.Version != input.RPCGoTool.Version || result.ExecutableVersion != input.RPCGoTool.Probe.ExpectedVersion || result.ExitCode != 0 {
			return ValidatedPlan{}, invalid("rpc_go_result_invalid", "/validation/rpcGoTool", nil)
		}
	}
	generatedOverlay, err := collectGeneratedRPCOverlay(staging, repository, resolvedPlan, module)
	if err != nil {
		return ValidatedPlan{}, invalid("rpc_go_result_invalid", "/validation/rpcGoTool", err)
	}
	if len(resolvedPlan.protoContent) != 0 && len(generatedOverlay) == 0 {
		return ValidatedPlan{}, invalid("rpc_go_dependency_missing", "/validation/rpcGoTool", nil)
	}
	candidateOverlay := map[string][]byte{}
	for _, candidate := range resolvedPlan.candidates {
		candidateOverlay[filepath.Join(repository, filepath.FromSlash(candidate.path))] = append([]byte(nil), candidate.content...)
	}
	overlay := mergeOverlay(generatedOverlay, candidateOverlay)
	wiringPath, wiringContent, err := renderWiringValidation(resolvedPlan, repository)
	if err != nil {
		return ValidatedPlan{}, err
	}
	if len(wiringContent) != 0 {
		overlay[wiringPath] = wiringContent
	}
	closedEnvironment, err := closedPackagesEnvironment(goExecutable, providerValues, staging)
	if err != nil {
		return ValidatedPlan{}, invalid("go_environment_invalid", "/validation/environment", err)
	}
	config := &packages.Config{Context: ctx, Dir: filepath.Join(repository, filepath.FromSlash(resolvedPlan.serviceRoot)), Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports | packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo, Overlay: overlay, Env: closedEnvironment, BuildFlags: []string{"-mod=readonly"}}
	loaded, err := packages.Load(config, "./internal/logic/...")
	if err != nil {
		return ValidatedPlan{}, invalid("typecheck_failed", "/validation/typecheck", err)
	}
	var diagnostics []string
	packages.Visit(loaded, nil, func(p *packages.Package) {
		for _, item := range p.Errors {
			diagnostics = append(diagnostics, item.Error())
		}
	})
	if len(diagnostics) != 0 {
		sort.Strings(diagnostics)
		return ValidatedPlan{}, invalid("typecheck_failed", "/validation/typecheck", errors.New(strings.Join(diagnostics, "; ")))
	}
	if len(resolvedPlan.protoContent) != 0 && !loadedGeneratedContracts(loaded, resolvedPlan.pbImport, resolvedPlan.crudSnapshot, resolvedPlan.protoNames, generatedOverlay) {
		return ValidatedPlan{}, invalid("rpc_go_dependency_missing", "/validation/typecheck", nil)
	}
	readFiles := collectReadFiles(repository, loaded, overlay)
	readFiles = appendValidatedFiles(readFiles, module.files...)
	canonicalInput := validationCanonicalInput{PlanDigest: resolvedPlan.digest, RPCGoTool: input.RPCGoTool, GoTool: input.GoTool, Environment: provider, WiringDigest: provenance.SHA256(wiringContent), ReadFiles: readFiles}
	for _, candidate := range resolvedPlan.candidates {
		canonicalInput.CandidateDigests = append(canonicalInput.CandidateDigests, candidate.digest)
	}
	canonical, err := canonicalValidation(canonicalInput)
	if err != nil {
		return ValidatedPlan{}, err
	}
	state := &validatedPlanState{plan: resolvedPlan, digest: provenance.SHA256(canonical)}
	helperID, helperPath := tenantHelperIdentity(resolvedPlan.layout)
	for _, c := range resolvedPlan.candidates {
		input := transaction.ArtifactInput{ID: c.id, Path: c.path, Owner: c.owner, Digest: c.digest, Sources: append([]provenance.SourceRef(nil), c.sources...), StalePolicy: map[bool]artifact.StalePolicy{true: artifact.StaleRetain, false: artifact.StaleDeleteIfUnmodified}[c.manual], CreateManual: c.manual && !c.overwrite, OverwriteManual: c.manual && c.overwrite}
		if c.id == helperID && c.path == helperPath && !c.manual {
			input.Probe = tenantHelperOwnershipProbe{id: c.id, path: c.path}
		}
		state.transactionInput = append(state.transactionInput, input)
	}
	return ValidatedPlan{state: state}, nil
}

type resolvedServiceModule struct {
	root, importPath, pbDirectory string
	files                         []validatedFile
}

func resolveServiceImport(input *planState, repository string) (*planState, resolvedServiceModule, error) {
	serviceRoot := filepath.Join(repository, filepath.FromSlash(input.serviceRoot))
	current := serviceRoot
	for {
		goModPath := filepath.Join(current, "go.mod")
		content, err := os.ReadFile(goModPath)
		if err == nil {
			parsed, parseErr := modfile.Parse(goModPath, content, nil)
			if parseErr != nil || parsed.Module == nil || parsed.Module.Mod.Path == "" {
				return nil, resolvedServiceModule{}, invalid("service_module_invalid", "/validation/repositoryRoot", parseErr)
			}
			relativeService, relErr := filepath.Rel(current, serviceRoot)
			if relErr != nil || relativeService == ".." || strings.HasPrefix(relativeService, ".."+string(filepath.Separator)) {
				return nil, resolvedServiceModule{}, invalid("service_module_invalid", "/validation/repositoryRoot", relErr)
			}
			serviceImport := parsed.Module.Mod.Path
			if relativeService != "." {
				serviceImport += "/" + filepath.ToSlash(relativeService)
			}
			resolved := *input
			resolved.serviceImport = serviceImport
			resolved.candidates = make([]candidate, len(input.candidates))
			for index, value := range input.candidates {
				resolved.candidates[index] = value
				resolved.candidates[index].content = bytes.ReplaceAll(value.content, []byte(serviceImportPlaceholder), []byte(serviceImport))
				resolved.candidates[index].digest = provenance.SHA256(resolved.candidates[index].content)
				resolved.candidates[index].sources = append([]provenance.SourceRef(nil), value.sources...)
			}
			relativeModule, relErr := filepath.Rel(repository, goModPath)
			if relErr != nil || !filepath.IsLocal(relativeModule) {
				return nil, resolvedServiceModule{}, invalid("service_module_invalid", "/validation/repositoryRoot", relErr)
			}
			module := resolvedServiceModule{root: current, importPath: parsed.Module.Mod.Path, files: []validatedFile{{Path: filepath.ToSlash(relativeModule), Digest: provenance.SHA256(content)}}}
			if input.pbImport == "" {
				module.pbDirectory = ""
			} else if input.pbImport == module.importPath {
				module.pbDirectory = current
			} else if strings.HasPrefix(input.pbImport, module.importPath+"/") {
				module.pbDirectory = filepath.Join(current, filepath.FromSlash(strings.TrimPrefix(input.pbImport, module.importPath+"/")))
			} else {
				return nil, resolvedServiceModule{}, invalid("service_module_invalid", "/validation/repositoryRoot", nil)
			}
			if module.pbDirectory != "" {
				if relativePB, relErr := filepath.Rel(repository, module.pbDirectory); relErr != nil || !filepath.IsLocal(relativePB) {
					return nil, resolvedServiceModule{}, invalid("service_module_invalid", "/validation/repositoryRoot", relErr)
				}
			}
			goSumPath := filepath.Join(current, "go.sum")
			if goSum, readErr := os.ReadFile(goSumPath); readErr == nil {
				relativeSum, relErr := filepath.Rel(repository, goSumPath)
				if relErr != nil || !filepath.IsLocal(relativeSum) {
					return nil, resolvedServiceModule{}, invalid("service_module_invalid", "/validation/repositoryRoot", relErr)
				}
				module.files = append(module.files, validatedFile{Path: filepath.ToSlash(relativeSum), Digest: provenance.SHA256(goSum)})
			} else if !errors.Is(readErr, os.ErrNotExist) {
				return nil, resolvedServiceModule{}, invalid("service_module_invalid", "/validation/repositoryRoot", readErr)
			}
			return &resolved, module, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, resolvedServiceModule{}, invalid("service_module_invalid", "/validation/repositoryRoot", err)
		}
		if current == repository {
			break
		}
		parent := filepath.Dir(current)
		if parent == current || !overlaps(repository, parent) {
			break
		}
		current = parent
	}
	return nil, resolvedServiceModule{}, invalid("service_module_invalid", "/validation/repositoryRoot", os.ErrNotExist)
}

func collectGeneratedRPCOverlay(staging, repository string, plan *planState, module resolvedServiceModule) (map[string][]byte, error) {
	result := map[string][]byte{}
	if len(plan.protoContent) == 0 {
		return result, nil
	}
	expectedPB, err := filepath.Rel(repository, module.pbDirectory)
	if err != nil || !filepath.IsLocal(expectedPB) {
		return nil, errors.New("invalid generated RPC Go package")
	}
	expectedPB = filepath.Clean(expectedPB)
	protoPath := filepath.Clean(filepath.FromSlash(plan.protoPath))
	protoSeen := len(plan.protoContent) == 0
	err = filepath.WalkDir(staging, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == staging || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return errors.New("invalid generated RPC output")
		}
		relative, relErr := filepath.Rel(staging, name)
		if relErr != nil || !filepath.IsLocal(relative) {
			return errors.New("invalid generated RPC path")
		}
		relative = filepath.Clean(relative)
		content, readErr := os.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		if relative == protoPath {
			if !bytes.Equal(content, plan.protoContent) {
				return errors.New("generated RPC input changed")
			}
			protoSeen = true
			return nil
		}
		if filepath.Ext(relative) != ".go" || filepath.Dir(relative) != expectedPB {
			return errors.New("generated RPC output escaped expected package")
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), name, content, parser.ParseComments)
		if parseErr != nil || parsed.Name == nil || parsed.Name.Name != plan.pbPackage || !ast.IsGenerated(parsed) || !generatedRPCProtoSourceAllowed(parsed, plan.protoPath) {
			return errors.New("generated RPC Go package invalid")
		}
		result[filepath.Join(repository, relative)] = append([]byte(nil), content...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !protoSeen {
		return nil, errors.New("generated RPC input missing")
	}
	return result, nil
}

func loadedGeneratedContracts(roots []*packages.Package, importPath string, snapshot crudproto.Snapshot, names protoGoNameSet, generated map[string][]byte) bool {
	var target *packages.Package
	packages.Visit(roots, nil, func(value *packages.Package) {
		if value.PkgPath == importPath {
			target = value
		}
	})
	if target == nil || target.Types == nil || target.Fset == nil || len(snapshot.Messages()) == 0 {
		return false
	}
	direct := func(name string) (*types.TypeName, bool) {
		object, ok := target.Types.Scope().Lookup(name).(*types.TypeName)
		if !ok || object.IsAlias() || object.Pos() == token.NoPos {
			return nil, false
		}
		filename := filepath.Clean(target.Fset.Position(object.Pos()).Filename)
		if _, ok := generated[filename]; !ok {
			return nil, false
		}
		return object, true
	}
	for _, message := range snapshot.Messages() {
		resolved, resolvedOK := names.message(message.Name())
		if !resolvedOK {
			return false
		}
		object, ok := direct(resolved.goName)
		if !ok {
			return false
		}
		if _, ok := object.Type().Underlying().(*types.Struct); !ok {
			return false
		}
	}
	for _, name := range snapshot.EnumNames() {
		resolved, resolvedOK := names.enum(name)
		if !resolvedOK {
			return false
		}
		object, ok := direct(resolved.goName)
		if !ok {
			return false
		}
		named, ok := object.Type().(*types.Named)
		if !ok {
			return false
		}
		basic, ok := named.Underlying().(*types.Basic)
		if !ok || basic.Kind() != types.Int32 {
			return false
		}
	}
	return true
}

const crudProtocolOptionsProtoPath = "nexa/protocol/v1/options.proto"

func generatedRPCProtoSourceAllowed(file *ast.File, targetProtoPath string) bool {
	return generatedFromProto(file, targetProtoPath) || generatedFromProto(file, crudProtocolOptionsProtoPath)
}

func generatedFromProto(file *ast.File, protoPath string) bool {
	want := "source: " + filepath.ToSlash(protoPath)
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if strings.TrimSpace(strings.TrimPrefix(comment.Text, "//")) == want {
				return true
			}
		}
	}
	return false
}

func mergeOverlay(generated, candidates map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(generated)+len(candidates))
	for name, content := range generated {
		result[name] = append([]byte(nil), content...)
	}
	for name, content := range candidates {
		result[name] = append([]byte(nil), content...)
	}
	return result
}

func renderWiringValidation(plan *planState, repository string) (string, []byte, error) {
	type wiringMethod struct {
		method     crudproto.Method
		methodName string
	}
	var methods []wiringMethod
	for _, service := range plan.crudSnapshot.Services() {
		entityName := strings.TrimSuffix(service.Name(), "CRUDService")
		for _, method := range service.Methods() {
			methods = append(methods, wiringMethod{method: method, methodName: method.Name() + entityName})
		}
	}
	if len(methods) == 0 {
		return "", nil, nil
	}
	var source strings.Builder
	source.WriteString("package logic\n\nimport (\n\t\"context\"\n")
	fmt.Fprintf(&source, "\tpb %q\n\t%q\n)\n\n", plan.pbImport, plan.serviceImport+"/internal/svc")
	source.WriteString("func nexaValidateCRUDWiring(ctx context.Context, svcCtx *svc.ServiceContext) {\n")
	for _, item := range methods {
		fmt.Fprintf(&source, "\t_, _ = New%sLogic(ctx, svcCtx).%s(&pb.%s{})\n", item.methodName, item.methodName, plan.protoMessageName(item.method.Input()))
	}
	source.WriteString("}\n")
	formatted, err := format.Source([]byte(source.String()))
	if err != nil {
		return "", nil, invalid("wiring_render_invalid", "/validation/typecheck", err)
	}
	path := filepath.Join(repository, filepath.FromSlash(plan.layout.LogicRoot), "zz_nexa_crud_wiring_validation.go")
	return path, formatted, nil
}

func validToolIdentity(value toolchain.Tool) bool {
	return value.ID != "" && value.Version != "" && value.Executable != "" && value.Probe.ExpectedVersion != ""
}

func validateGoProvider(value toolchain.Tool, environment []toolchain.EnvVar) (string, []validationEnvironment, map[string]string, error) {
	if !validToolIdentity(value) || value.ID != "go" || len(value.Args) != 0 || !sameStrings(value.InputScopes, []string{"repository", "scratch"}) || !sameStrings(value.WriteScopes, []string{"scratch"}) || !sameStrings(value.Probe.Args, []string{"version"}) {
		return "", nil, nil, invalid("go_tool_invalid", "/validation/goTool", nil)
	}
	executable, err := canonicalExecutable(value.Executable)
	if err != nil || executable != value.Executable {
		return "", nil, nil, invalid("go_tool_invalid", "/validation/goTool", err)
	}
	ambient, err := exec.LookPath("go")
	if err != nil {
		return "", nil, nil, invalid("go_tool_invalid", "/validation/goTool", err)
	}
	ambient, err = canonicalExecutable(ambient)
	if err != nil || ambient != executable {
		return "", nil, nil, invalid("go_tool_invalid", "/validation/goTool", err)
	}
	want := map[string]toolchain.EnvironmentRule{
		"PATH": {Name: "PATH", Source: toolchain.EnvironmentHost}, "GOROOT": {Name: "GOROOT", Source: toolchain.EnvironmentHost},
		"GOMODCACHE": {Name: "GOMODCACHE", Source: toolchain.EnvironmentHost}, "GOPROXY": {Name: "GOPROXY", Source: toolchain.EnvironmentHost},
		"GOSUMDB": {Name: "GOSUMDB", Source: toolchain.EnvironmentHost}, "HOME": {Name: "HOME", Source: toolchain.EnvironmentScratch},
		"TMPDIR": {Name: "TMPDIR", Source: toolchain.EnvironmentScratch}, "GOPATH": {Name: "GOPATH", Source: toolchain.EnvironmentScratch},
		"GOCACHE": {Name: "GOCACHE", Source: toolchain.EnvironmentScratch}, "GOWORK": {Name: "GOWORK", Source: toolchain.EnvironmentFixed, FixedValue: "off"},
		"GOENV": {Name: "GOENV", Source: toolchain.EnvironmentFixed, FixedValue: "off"}, "GOTOOLCHAIN": {Name: "GOTOOLCHAIN", Source: toolchain.EnvironmentFixed, FixedValue: "local"},
		"GOFLAGS": {Name: "GOFLAGS", Source: toolchain.EnvironmentFixed, FixedValue: ""}, "CGO_ENABLED": {Name: "CGO_ENABLED", Source: toolchain.EnvironmentFixed, FixedValue: "0"},
	}
	if len(value.Environment) != len(want) || len(environment) != len(want) {
		return "", nil, nil, invalid("go_environment_invalid", "/validation/environment", nil)
	}
	rules := make(map[string]toolchain.EnvironmentRule, len(value.Environment))
	for _, rule := range value.Environment {
		expected, ok := want[rule.Name]
		if !ok || expected != rule {
			return "", nil, nil, invalid("go_environment_invalid", "/validation/environment", nil)
		}
		if _, duplicate := rules[rule.Name]; duplicate {
			return "", nil, nil, invalid("go_environment_invalid", "/validation/environment", nil)
		}
		rules[rule.Name] = rule
	}
	values := make(map[string]string, len(environment))
	for _, item := range environment {
		rule, ok := rules[item.Name]
		if !ok || rule.Source == toolchain.EnvironmentFixed && item.Value != rule.FixedValue || rule.Source != toolchain.EnvironmentFixed && item.Value == "" {
			return "", nil, nil, invalid("go_environment_invalid", "/validation/environment", nil)
		}
		if _, duplicate := values[item.Name]; duplicate {
			return "", nil, nil, invalid("go_environment_invalid", "/validation/environment", nil)
		}
		values[item.Name] = item.Value
	}
	provider := make([]validationEnvironment, 0, len(rules)+1)
	for name, rule := range rules {
		semantic := rule.FixedValue
		switch {
		case rule.Source == toolchain.EnvironmentScratch:
			semantic = "$STAGING/" + name
		case rule.Source == toolchain.EnvironmentHost:
			semantic = "$HOST/" + name
		}
		provider = append(provider, validationEnvironment{Name: name, Source: rule.Source, Value: semantic})
	}
	provider = append(provider, validationEnvironment{Name: "GOPACKAGESDRIVER", Source: toolchain.EnvironmentFixed, Value: "off"})
	sort.Slice(provider, func(i, j int) bool { return provider[i].Name < provider[j].Name })
	return executable, provider, values, nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func canonicalExecutable(value string) (string, error) {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return "", os.ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(value)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", errors.Join(err, os.ErrInvalid)
	}
	return resolved, nil
}

func closedPackagesEnvironment(goExecutable string, provider map[string]string, staging string) ([]string, error) {
	root := filepath.Join(staging, ".nexa-typecheck")
	paths := map[string]string{
		"HOME": filepath.Join(root, "home"), "TMPDIR": filepath.Join(root, "tmp"), "GOPATH": filepath.Join(root, "gopath"),
		"GOCACHE": filepath.Join(root, "gocache"), "GOMODCACHE": filepath.Join(root, "gomodcache"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, err
		}
	}
	values := map[string]string{
		"PATH": filepath.Dir(goExecutable), "GOROOT": provider["GOROOT"], "GOPROXY": provider["GOPROXY"], "GOSUMDB": provider["GOSUMDB"],
		"HOME": paths["HOME"], "TMPDIR": paths["TMPDIR"], "GOPATH": paths["GOPATH"], "GOCACHE": paths["GOCACHE"], "GOMODCACHE": paths["GOMODCACHE"],
		"GOPACKAGESDRIVER": "off", "GOWORK": "off", "GOENV": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "", "CGO_ENABLED": "0",
	}
	result := make([]string, 0, len(values))
	for name, value := range values {
		result = append(result, name+"="+value)
	}
	sort.Strings(result)
	return result, nil
}

func canonicalValidation(input validationCanonicalInput) ([]byte, error) {
	type fileWire struct{ Path, Digest string }
	type environmentWire struct{ Name, Source, Value string }
	type ruleWire struct{ Name, Source, FixedValue string }
	type toolWire struct {
		ID, Version, ExpectedVersion              string
		Args, InputScopes, WriteScopes, ProbeArgs []string
		Environment                               []ruleWire
	}
	type wire struct {
		PlanDigest, WiringDigest string
		RPCGoTool, GoTool        toolWire
		Environment              []environmentWire
		Candidates               []string
		Files                    []fileWire
	}
	canonicalTool := func(input toolchain.Tool) toolWire {
		result := toolWire{ID: input.ID, Version: input.Version, ExpectedVersion: input.Probe.ExpectedVersion, Args: append([]string(nil), input.Args...), InputScopes: append([]string(nil), input.InputScopes...), WriteScopes: append([]string(nil), input.WriteScopes...), ProbeArgs: append([]string(nil), input.Probe.Args...)}
		for _, rule := range input.Environment {
			result.Environment = append(result.Environment, ruleWire{Name: rule.Name, Source: string(rule.Source), FixedValue: rule.FixedValue})
		}
		sort.Slice(result.Environment, func(i, j int) bool { return result.Environment[i].Name < result.Environment[j].Name })
		return result
	}
	value := wire{PlanDigest: input.PlanDigest.String(), WiringDigest: input.WiringDigest.String(), RPCGoTool: canonicalTool(input.RPCGoTool), GoTool: canonicalTool(input.GoTool)}
	for _, item := range input.Environment {
		value.Environment = append(value.Environment, environmentWire{Name: item.Name, Source: string(item.Source), Value: item.Value})
	}
	for _, digest := range input.CandidateDigests {
		value.Candidates = append(value.Candidates, digest.String())
	}
	for _, file := range input.ReadFiles {
		value.Files = append(value.Files, fileWire{file.Path, file.Digest.String()})
	}
	sort.Strings(value.Candidates)
	sort.Slice(value.Environment, func(i, j int) bool { return value.Environment[i].Name < value.Environment[j].Name })
	sort.Slice(value.Files, func(i, j int) bool { return value.Files[i].Path < value.Files[j].Path })
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, invalid("canonical_invalid", "/validation", err)
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		return nil, invalid("canonical_invalid", "/validation", err)
	}
	return canonical, nil
}

func canonicalDir(value string) (string, error) {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return "", os.ErrInvalid
	}
	absolute, err := filepath.EvalSymlinks(value)
	if err != nil {
		return "", err
	}
	if absolute != value {
		return "", os.ErrInvalid
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", errors.Join(err, os.ErrInvalid)
	}
	return absolute, nil
}
func overlaps(a, b string) bool {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && (rel == "." || filepath.IsLocal(rel)) {
			return true
		}
	}
	return false
}

func appendValidatedFiles(input []validatedFile, values ...validatedFile) []validatedFile {
	byPath := make(map[string]validatedFile, len(input)+len(values))
	for _, file := range append(append([]validatedFile(nil), input...), values...) {
		byPath[file.Path] = file
	}
	result := make([]validatedFile, 0, len(byPath))
	for _, file := range byPath {
		result = append(result, file)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func collectReadFiles(root string, loaded []*packages.Package, overlay map[string][]byte) []validatedFile {
	seen := map[string]bool{}
	var result []validatedFile
	packages.Visit(loaded, nil, func(p *packages.Package) {
		for _, name := range append(append([]string{}, p.GoFiles...), p.CompiledGoFiles...) {
			if seen[name] {
				continue
			}
			rel, err := filepath.Rel(root, name)
			if err != nil || !filepath.IsLocal(rel) {
				continue
			}
			content := overlay[name]
			if content == nil {
				content, _ = os.ReadFile(name)
			}
			if content == nil {
				continue
			}
			seen[name] = true
			result = append(result, validatedFile{filepath.ToSlash(rel), provenance.SHA256(content)})
		}
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

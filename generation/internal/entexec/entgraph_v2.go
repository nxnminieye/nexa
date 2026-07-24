package entexec

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/nxnminieye/nexa/generation/internal/entipc"
)

type EntGraphProcessSpec struct {
	RepositoryRoot, ModuleDir, ModulePath, SchemaDir string
	GoExecutable, ExpectedVersion                    string
	Request                                          []byte
	BuildTags                                        []string
	GOCACHE, GOMODCACHE, TempBase                    string
	BaseEnvironment                                  []string
	cleanupHook                                      func(string) error
	processHook                                      func(processEvent)
}

type EntGraphError struct{ code, reason, pointer, source string }

func (e *EntGraphError) Error() string {
	if e == nil {
		return ""
	}
	return "Ent graph projection failed"
}
func (e *EntGraphError) Owner() string { return "entityload" }
func (e *EntGraphError) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}
func (e *EntGraphError) Reason() string {
	if e == nil {
		return ""
	}
	return e.reason
}
func (e *EntGraphError) Pointer() string {
	if e == nil {
		return ""
	}
	return e.pointer
}
func (e *EntGraphError) Source() string {
	if e == nil {
		return ""
	}
	return e.source
}

func entGraphInputError(reason, pointer string) error {
	return &EntGraphError{code: "entity_input_invalid", reason: reason, pointer: pointer}
}
func entGraphLoadError(reason, source string) error {
	return &EntGraphError{code: "entity_graph_load_failed", reason: reason, source: source}
}

const (
	entGraphGoExecutableEnvironment = "NEXA_ENT_GO_EXECUTABLE"
	entGraphGoVersionEnvironment    = "NEXA_ENT_GO_VERSION"
)

type InvocationV2 struct {
	root, importer, overlay, toolchainTmp string
	environment                           []string
	owner                                 *ownedScratchRoot
	cleanupMu                             sync.Mutex
}

var entGraphBuildTagPattern = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)

func RunEntGraphV2(ctx context.Context, spec EntGraphProcessSpec) (result ProcessResult, resultErr error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return ProcessResult{}, newProcessError("tool_platform_unsupported", "platform", "process_tree_unsupported", "", "go", 0)
	}
	request, err := entipc.ParseRequestV2(entipc.HelperRequestV2Source(), spec.Request)
	if err != nil {
		return ProcessResult{}, err
	}
	if request.ModuleDir() != spec.ModuleDir || request.ModulePath() != spec.ModulePath || request.SchemaDir() != spec.SchemaDir || !equalStringSlices(request.BuildTags(), spec.BuildTags) {
		return ProcessResult{}, entGraphInputError("module_path_mismatch", "/modulePath")
	}
	canonicalExecutable, err := filepath.EvalSymlinks(spec.GoExecutable)
	if err != nil || canonicalExecutable != spec.GoExecutable {
		return ProcessResult{}, entGraphLoadError("helper_prepare_failed", spec.SchemaDir)
	}
	moduleRoot, tags, err := validateEntGraphCoordinatesV2(spec.RepositoryRoot, spec.ModuleDir, spec.BuildTags)
	if err != nil {
		return ProcessResult{}, err
	}
	invocation, err := PrepareInvocationV2(spec.RepositoryRoot, spec.GOCACHE, spec.GOMODCACHE, spec.TempBase, spec.BaseEnvironment)
	if err != nil {
		return ProcessResult{}, entGraphLoadError("helper_prepare_failed", spec.SchemaDir)
	}
	invocation.environment = replaceEnvironmentV2(invocation.environment, entGraphGoExecutableEnvironment, canonicalExecutable)
	invocation.environment = replaceEnvironmentV2(invocation.environment, entGraphGoVersionEnvironment, spec.ExpectedVersion)
	defer func() {
		cleanupErr := invocation.Cleanup()
		if spec.cleanupHook != nil {
			if hookErr := spec.cleanupHook(invocation.root); hookErr != nil {
				cleanupErr = entGraphLoadError("helper_cleanup_failed", spec.SchemaDir)
			}
		}
		if cleanupErr != nil {
			result = ProcessResult{}
			resultErr = entGraphLoadError("helper_cleanup_failed", spec.SchemaDir)
		}
	}()
	args := []string{"-C", moduleRoot, "run", "-mod=readonly"}
	if len(tags) > 0 {
		args = append(args, "-tags="+strings.Join(tags, ","))
	}
	args = append(args, "github.com/nxnminieye/nexa/generation/enthelperexec")
	rules := make([]ProcessEnvironmentRule, len(invocation.environment))
	environment := make([]ProcessEnvironment, len(invocation.environment))
	for i, item := range invocation.environment {
		name, value, _ := strings.Cut(item, "=")
		rules[i] = ProcessEnvironmentRule{Name: name, Source: EnvironmentFixed, FixedValue: value}
		environment[i] = ProcessEnvironment{Name: name, Value: value}
	}
	tool := ProcessTool{ID: "go", Version: "go", Executable: spec.GoExecutable, Args: nil, InputScopes: []string{"repository"}, WriteScopes: []string{}, Environment: rules, Probe: ProcessProbe{Args: []string{"version"}, ExpectedVersion: spec.ExpectedVersion}}
	result, err = RunProcess(ctx, ProcessSpec{RepositoryRoot: spec.RepositoryRoot, Direct: true, Tool: tool, Args: args, Environment: environment, Stdin: append([]byte(nil), spec.Request...), processHook: spec.processHook})
	if err != nil {
		return ProcessResult{}, entGraphLoadError("helper_execution_failed", spec.SchemaDir)
	}
	return result, nil
}

func validateEntGraphCoordinatesV2(repository, moduleDir string, buildTags []string) (string, []string, error) {
	if repository == "" || !filepath.IsAbs(repository) || (moduleDir != "." && (moduleDir == "" || strings.Contains(moduleDir, `\`) || path.Clean(moduleDir) != moduleDir || strings.HasPrefix(moduleDir, "../") || moduleDir == ".." || strings.HasPrefix(moduleDir, "/"))) {
		return "", nil, newProcessError("tool_input_invalid", "input", "repository_root_invalid", "/repositoryRoot", "go", 0)
	}
	canonicalRepository, err := canonicalExistingDirectory(repository)
	if err != nil {
		return "", nil, newProcessError("tool_input_invalid", "input", "repository_root_invalid", "/repositoryRoot", "go", 0)
	}
	moduleRoot := canonicalRepository
	if moduleDir != "." {
		moduleRoot = filepath.Join(canonicalRepository, filepath.FromSlash(moduleDir))
	}
	moduleRoot, err = canonicalExistingDirectory(moduleRoot)
	if err != nil || !pathContainedBy(moduleRoot, canonicalRepository) {
		return "", nil, newProcessError("tool_input_invalid", "input", "repository_root_invalid", "/repositoryRoot", "go", 0)
	}
	tags := append([]string(nil), buildTags...)
	seen := make(map[string]struct{}, len(tags))
	for index, tag := range tags {
		if !entGraphBuildTagPattern.MatchString(tag) {
			return "", nil, newProcessError("tool_input_invalid", "input", "build_tag_invalid", indexedPointer("/buildTags", index), "go", 0)
		}
		if _, exists := seen[tag]; exists {
			return "", nil, newProcessError("tool_input_invalid", "input", "build_tag_duplicate", indexedPointer("/buildTags", index), "go", 0)
		}
		seen[tag] = struct{}{}
	}
	sort.Strings(tags)
	return moduleRoot, tags, nil
}

func PrepareInvocationV2(repository, goCache, moduleCache, tempBase string, baseEnvironment []string) (*InvocationV2, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil, newProcessError("tool_platform_unsupported", "platform", "process_tree_unsupported", "", "go", 0)
	}
	repository, err := canonicalExistingDirectory(repository)
	if err != nil {
		return nil, newProcessError("tool_input_invalid", "input", "repository_root_invalid", "/repositoryRoot", "go", 0)
	}
	goCache, err = canonicalExistingDirectory(goCache)
	if err != nil || pathsOverlap(repository, goCache) {
		return nil, newProcessError("tool_input_invalid", "input", "environment_policy_invalid", "/environment/GOCACHE", "go", 0)
	}
	moduleCache, err = canonicalExistingDirectory(moduleCache)
	if err != nil || pathsOverlap(repository, moduleCache) || pathsOverlap(goCache, moduleCache) {
		return nil, newProcessError("tool_input_invalid", "input", "environment_policy_invalid", "/environment/GOMODCACHE", "go", 0)
	}
	tempBase, err = canonicalExistingDirectory(tempBase)
	if err != nil || pathsOverlap(repository, tempBase) || pathsOverlap(goCache, tempBase) || pathsOverlap(moduleCache, tempBase) {
		return nil, newProcessError("tool_input_invalid", "input", "environment_policy_invalid", "/environment/TMPDIR", "go", 0)
	}
	owner, err := createOwnedScratchRoot(tempBase, nil)
	if err != nil {
		return nil, newProcessError("tool_failed", "prepare", "helper_prepare_failed", "", "go", 0)
	}
	fail := func() (*InvocationV2, error) {
		_ = owner.cleanup()
		return nil, newProcessError("tool_failed", "prepare", "helper_prepare_failed", "", "go", 0)
	}
	root := owner.rootPath
	for _, name := range []string{"toolchain-tmp", "home", "config", "telemetry"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			return fail()
		}
	}
	values := selectV2Environment(baseEnvironment)
	values["GOCACHE"], values["GOMODCACHE"] = goCache, moduleCache
	values["HOME"], values["XDG_CONFIG_HOME"], values["TEST_TELEMETRY_DIR"] = filepath.Join(root, "home"), filepath.Join(root, "config"), filepath.Join(root, "telemetry")
	values["GOTMPDIR"], values["TMPDIR"] = filepath.Join(root, "toolchain-tmp"), filepath.Join(root, "toolchain-tmp")
	for name, value := range map[string]string{"GOWORK": "off", "GOENV": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "", "GOMOD": "", "GO111MODULE": "on", "GOEXPERIMENT": "", "GOCACHEPROG": "", "GOTOOLDIR": "", "GOPACKAGESDRIVER": "off"} {
		values[name] = value
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	environment := make([]string, len(names))
	for index, name := range names {
		environment[index] = name + "=" + values[name]
	}
	return &InvocationV2{root: root, importer: filepath.Join(root, "importer.go"), overlay: filepath.Join(root, "overlay.json"), toolchainTmp: filepath.Join(root, "toolchain-tmp"), environment: environment, owner: owner}, nil
}

func selectV2Environment(environment []string) map[string]string {
	allowed := map[string]bool{"PATH": true, "GOROOT": true, "GOPATH": true, "GOPROXY": true, "GOSUMDB": true, "GOPRIVATE": true, "GONOPROXY": true, "GONOSUMDB": true, "NETRC": true, "SSH_AUTH_SOCK": true, "GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true, "CGO_ENABLED": true, "CC": true, "CXX": true, "PKG_CONFIG": true}
	result := make(map[string]string)
	for _, item := range environment {
		name, value, ok := strings.Cut(item, "=")
		if ok && allowed[name] {
			result[name] = value
		}
	}
	return result
}

func (i *InvocationV2) Environment() []string {
	if i == nil {
		return nil
	}
	return append([]string(nil), i.environment...)
}
func (i *InvocationV2) Root() string {
	if i == nil {
		return ""
	}
	return i.root
}
func (i *InvocationV2) Cleanup() error {
	if i != nil {
		i.cleanupMu.Lock()
		defer i.cleanupMu.Unlock()
	}
	if i == nil || i.owner == nil {
		return nil
	}
	if err := i.owner.cleanup(); err != nil {
		return newProcessError("tool_failed", "cleanup", "helper_cleanup_failed", "", "go", 0)
	}
	i.owner = nil
	return nil
}

func RunImporterV2(ctx context.Context, moduleRoot, virtualDir, schemaDir, executable, expectedVersion string, source []byte, tags []string, environment []string) ([]byte, error) {
	moduleRoot, err := canonicalExistingDirectory(moduleRoot)
	if err != nil {
		return nil, entGraphInputError("module_root_invalid", "/moduleDir")
	}
	values := envMapV2(environment)
	tmp := values["GOTMPDIR"]
	invocationRoot := filepath.Dir(tmp)
	if tmp == "" || filepath.Base(tmp) != "toolchain-tmp" || filepath.Dir(values["HOME"]) != invocationRoot || filepath.Dir(values["XDG_CONFIG_HOME"]) != invocationRoot || filepath.Dir(values["TEST_TELEMETRY_DIR"]) != invocationRoot {
		return nil, entGraphLoadError("helper_prepare_failed", schemaDir)
	}
	invocationRoot, err = canonicalExistingDirectory(invocationRoot)
	if err != nil || pathsOverlap(moduleRoot, invocationRoot) {
		return nil, entGraphLoadError("helper_prepare_failed", schemaDir)
	}
	canonicalExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil || canonicalExecutable != executable || values[entGraphGoExecutableEnvironment] != executable || values[entGraphGoVersionEnvironment] != expectedVersion {
		return nil, entGraphLoadError("helper_prepare_failed", schemaDir)
	}
	virtualDir, err = canonicalExistingDirectory(virtualDir)
	if err != nil || !pathContainedBy(virtualDir, moduleRoot) {
		return nil, entGraphInputError("importer_visibility_invalid", "/schemaDir")
	}
	importer := filepath.Join(invocationRoot, "importer.go")
	overlay := filepath.Join(invocationRoot, "overlay.json")
	virtual := filepath.Join(virtualDir, "zz_nexa_ent_importer.go")
	if err := rejectVirtualCollisionV2(virtualDir, filepath.Base(virtual), schemaDir); err != nil {
		return nil, err
	}
	if err := os.WriteFile(importer, source, 0o600); err != nil {
		return nil, entGraphLoadError("helper_prepare_failed", schemaDir)
	}
	overlayBytes, _ := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{map[string]string{virtual: importer}})
	if err := os.WriteFile(overlay, overlayBytes, 0o600); err != nil {
		return nil, entGraphLoadError("helper_prepare_failed", schemaDir)
	}
	if err := rejectVirtualCollisionV2(virtualDir, filepath.Base(virtual), schemaDir); err != nil {
		return nil, err
	}
	probe := executeProcessWithLimits(ctx, executable, []string{"version"}, environment, nil, moduleRoot, MaxStdoutBytes, MaxStderrBytes, nil)
	if probe.contextErr != nil || probe.startErr != nil || probe.stdinErr != nil || probe.waitErr != nil || probe.exitCode != 0 || probe.stdoutOverflow || probe.stderrOverflow || strings.TrimSpace(string(probe.stdout)) != expectedVersion {
		return nil, entGraphLoadError("helper_execution_failed", schemaDir)
	}
	args := []string{"run", "-mod=readonly", "-overlay=" + overlay}
	sorted := append([]string(nil), tags...)
	sort.Strings(sorted)
	if len(sorted) > 0 {
		args = append(args, "-tags="+strings.Join(sorted, ","))
	}
	args = append(args, virtual)
	outcome := executeProcessWithLimits(ctx, executable, args, environment, nil, moduleRoot, MaxStdoutBytes, MaxStderrBytes, nil)
	if outcome.contextErr != nil {
		return nil, entGraphLoadError("helper_execution_failed", schemaDir)
	}
	if outcome.startErr != nil || outcome.stdinErr != nil || outcome.waitErr != nil || outcome.exitCode != 0 || outcome.stdoutOverflow || outcome.stderrOverflow {
		return nil, entGraphLoadError("helper_execution_failed", schemaDir)
	}
	return append([]byte(nil), outcome.stdout...), nil
}

func rejectVirtualCollisionV2(root, name, schemaDir string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return entGraphLoadError("helper_prepare_failed", schemaDir)
	}
	fold := strings.ToLower(name)
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == fold {
			return entGraphInputError("importer_visibility_invalid", "/schemaDir")
		}
	}
	return nil
}

func replaceEnvironmentV2(environment []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}
func envMapV2(environment []string) map[string]string {
	result := map[string]string{}
	for _, item := range environment {
		name, value, ok := strings.Cut(item, "=")
		if ok {
			result[name] = value
		}
	}
	return result
}

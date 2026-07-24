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
)

type EntGraphProcessSpec struct {
	RepositoryRoot, ModuleDir     string
	GoExecutable, ExpectedVersion string
	Request                       []byte
	BuildTags                     []string
	GOCACHE, GOMODCACHE, TempBase string
	BaseEnvironment               []string
	cleanupHook                   func(string) error
}

type InvocationV2 struct {
	root, importer, overlay, toolchainTmp string
	environment                           []string
	owner                                 *ownedScratchRoot
}

var entGraphBuildTagPattern = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)

func RunEntGraphV2(ctx context.Context, spec EntGraphProcessSpec) (result ProcessResult, resultErr error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return ProcessResult{}, newProcessError("tool_platform_unsupported", "platform", "process_tree_unsupported", "", "go", 0)
	}
	moduleRoot, tags, err := validateEntGraphCoordinatesV2(spec.RepositoryRoot, spec.ModuleDir, spec.BuildTags)
	if err != nil {
		return ProcessResult{}, err
	}
	invocation, err := PrepareInvocationV2(spec.RepositoryRoot, spec.GOCACHE, spec.GOMODCACHE, spec.TempBase, spec.BaseEnvironment)
	if err != nil {
		return ProcessResult{}, err
	}
	defer func() {
		cleanupErr := invocation.Cleanup()
		if spec.cleanupHook != nil {
			if hookErr := spec.cleanupHook(invocation.root); hookErr != nil {
				cleanupErr = hookErr
			}
		}
		if cleanupErr != nil {
			result = ProcessResult{}
			resultErr = cleanupErr
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
	return RunProcess(ctx, ProcessSpec{RepositoryRoot: spec.RepositoryRoot, Direct: true, Tool: tool, Args: args, Environment: environment, Stdin: append([]byte(nil), spec.Request...)})
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
	if i == nil || i.owner == nil {
		return nil
	}
	if err := i.owner.cleanup(); err != nil {
		return newProcessError("tool_failed", "cleanup", "helper_cleanup_failed", "", "go", 0)
	}
	i.owner = nil
	return nil
}

func RunImporterV2(ctx context.Context, moduleRoot string, source []byte, tags []string, environment []string) ([]byte, error) {
	moduleRoot, err := canonicalExistingDirectory(moduleRoot)
	if err != nil {
		return nil, newProcessError("tool_input_invalid", "input", "repository_root_invalid", "/moduleDir", "go", 0)
	}
	values := envMapV2(environment)
	tmp := values["GOTMPDIR"]
	invocationRoot := filepath.Dir(tmp)
	if tmp == "" || filepath.Base(tmp) != "toolchain-tmp" || filepath.Dir(values["HOME"]) != invocationRoot || filepath.Dir(values["XDG_CONFIG_HOME"]) != invocationRoot || filepath.Dir(values["TEST_TELEMETRY_DIR"]) != invocationRoot {
		return nil, newProcessError("tool_input_invalid", "input", "environment_policy_invalid", "/environment", "go", 0)
	}
	invocationRoot, err = canonicalExistingDirectory(invocationRoot)
	if err != nil || pathsOverlap(moduleRoot, invocationRoot) {
		return nil, newProcessError("tool_input_invalid", "input", "environment_policy_invalid", "/environment", "go", 0)
	}
	importer := filepath.Join(invocationRoot, "importer.go")
	overlay := filepath.Join(invocationRoot, "overlay.json")
	virtual := filepath.Join(moduleRoot, "zz_nexa_ent_importer.go")
	if err := rejectVirtualCollisionV2(moduleRoot, filepath.Base(virtual)); err != nil {
		return nil, err
	}
	if err := os.WriteFile(importer, source, 0o600); err != nil {
		return nil, newProcessError("tool_failed", "prepare", "helper_prepare_failed", "", "go", 0)
	}
	overlayBytes, _ := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{map[string]string{virtual: importer}})
	if err := os.WriteFile(overlay, overlayBytes, 0o600); err != nil {
		return nil, newProcessError("tool_failed", "prepare", "helper_prepare_failed", "", "go", 0)
	}
	args := []string{"run", "-mod=readonly", "-overlay=" + overlay}
	sorted := append([]string(nil), tags...)
	sort.Strings(sorted)
	if len(sorted) > 0 {
		args = append(args, "-tags="+strings.Join(sorted, ","))
	}
	args = append(args, virtual)
	outcome := executeProcessWithLimits(ctx, "go", args, environment, nil, moduleRoot, MaxStdoutBytes, MaxStderrBytes, nil)
	if outcome.contextErr != nil {
		return nil, contextProcessError(outcome.contextErr, "go")
	}
	if outcome.startErr != nil || outcome.stdinErr != nil || outcome.waitErr != nil || outcome.exitCode != 0 || outcome.stdoutOverflow || outcome.stderrOverflow {
		return nil, newProcessDiagnosticError("tool_failed", "execute", "helper_execution_failed", "", "go", outcome.exitCode, safeDiagnostic(outcome.stderr, diagnosticRedactions{paths: []string{moduleRoot, invocationRoot}}))
	}
	return append([]byte(nil), outcome.stdout...), nil
}

func rejectVirtualCollisionV2(root, name string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return newProcessError("tool_failed", "prepare", "helper_prepare_failed", "", "go", 0)
	}
	fold := strings.ToLower(name)
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == fold {
			return newProcessError("tool_input_invalid", "input", "importer_collision", "/schemaDir", "go", 0)
		}
	}
	return nil
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

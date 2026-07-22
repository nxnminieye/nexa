package buildinput

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const (
	moduleGraphAPIVersion = "nexa.dev/ent-helper-module-graph/v1"
	moduleGraphKind       = "EntHelperModuleGraph"

	MaxRetainedModuleRoots     = 64
	MaxRetainedParentDepth     = 64
	MaxRetainedBuildInputs     = 8192
	MaxRetainedBuildInputBytes = 16 << 20
	MaxRetainedBuildInputTotal = 256 << 20
)

var (
	errModuleGraphSnapshotInvalid = errors.New("module graph snapshot invalid")
	errModuleGraphReadbackInvalid = errors.New("module graph readback invalid")
	errBuildInputInvalid          = errors.New("build input invalid")
	errBuildInputReadbackInvalid  = errors.New("build input readback invalid")
	errBuildInputUnsupported      = errors.New("build input unsupported")
	errBuildInputSnapshotInvalid  = errors.New("build input snapshot invalid")
)

type Error struct {
	code, reason, stage, pointer, source, message string
	sentinel                                      error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	location := e.source + e.pointer
	if location == "" {
		return e.message
	}
	return fmt.Sprintf("%s: %s", location, e.message)
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

func (e *Error) Reason() string {
	if e == nil {
		return ""
	}
	return e.reason
}

func (e *Error) Stage() string {
	if e == nil {
		return ""
	}
	return e.stage
}

func (e *Error) Pointer() string {
	if e == nil {
		return ""
	}
	return e.pointer
}

func (e *Error) Source() string {
	if e == nil {
		return ""
	}
	return e.source
}

type ModuleRequirement struct {
	Path    string
	Version string
}

type ModuleReplacement struct {
	Kind           string
	Path           string
	Version        string
	RepositoryPath string
}

type ModuleContent struct {
	Kind     string
	Sum      string
	GoModSum string
}

type ModuleIdentity struct {
	Path        string
	Version     string
	Replacement ModuleReplacement
	Content     ModuleContent
}

type graphState struct {
	consumerModule ModuleRequirement
	goVersion      string
	toolchain      string
	helperDigest   provenance.Digest
	moduleSources  []provenance.Source
	modules        []ModuleIdentity
	toolModule     ModuleRequirement
}

type GraphSnapshot struct {
	state *graphState
}

type DiscoveryInput struct {
	RepositoryRoot      string
	ScratchRoot         string
	SchemaDir           provenance.DomainSource
	SchemaImportPath    string
	BuildTags           []string
	ToolModule          ModuleRequirement
	HelperDigest        provenance.Digest
	GoExecutableVersion string
	ModuleList          []byte
	PackageList         []byte
	readFile            semanticReadFunc
	filePresent         semanticPresenceFunc
}

type semanticReadFunc func(root, name string, limit int64, pointer string) ([]byte, error)

type semanticPresenceFunc func(root, name, pointer string) (bool, error)

type Compilation struct {
	graph             GraphSnapshot
	manifest          ManifestSnapshot
	executableVersion string
	valid             bool
}

type ManifestSnapshot struct {
	state *manifestState
}

type RetainedLocalModule struct {
	Role              string
	Module            ModuleIdentity
	RepositoryPath    string
	HasRepositoryPath bool
}

type RetainedBuildInput struct {
	Module RetainedLocalModule
	Path   string
	Kind   string
	Size   int64
	Digest provenance.Digest
}

type manifestState struct {
	buildTags           []string
	schemaImportPath    string
	goExecutableVersion string
	graphDigest         provenance.Digest
	localModules        []RetainedLocalModule
	inputs              []RetainedBuildInput
}

type goListModule struct {
	Path      string        `json:"Path"`
	Version   string        `json:"Version"`
	Main      bool          `json:"Main"`
	Dir       string        `json:"Dir"`
	GoMod     string        `json:"GoMod"`
	GoVersion string        `json:"GoVersion"`
	Sum       string        `json:"Sum"`
	GoModSum  string        `json:"GoModSum"`
	Replace   *goListModule `json:"Replace"`
}

type goListPackage struct {
	Dir          string        `json:"Dir"`
	ImportPath   string        `json:"ImportPath"`
	Standard     bool          `json:"Standard"`
	Module       *goListModule `json:"Module"`
	GoFiles      []string      `json:"GoFiles"`
	EmbedFiles   []string      `json:"EmbedFiles"`
	CgoFiles     []string      `json:"CgoFiles"`
	CFiles       []string      `json:"CFiles"`
	CXXFiles     []string      `json:"CXXFiles"`
	MFiles       []string      `json:"MFiles"`
	HFiles       []string      `json:"HFiles"`
	FFiles       []string      `json:"FFiles"`
	SFiles       []string      `json:"SFiles"`
	SwigFiles    []string      `json:"SwigFiles"`
	SwigCXXFiles []string      `json:"SwigCXXFiles"`
	SysoFiles    []string      `json:"SysoFiles"`
}

type localModuleDescriptor struct {
	root     string
	goMod    string
	retained RetainedLocalModule
}

func Compile(input DiscoveryInput) (Compilation, error) {
	repositoryRoot, scratchRoot, tags, err := preflightDiscovery(input)
	if err != nil {
		return Compilation{}, err
	}
	reader := input.readFile
	if reader == nil {
		reader = func(root, name string, limit int64, pointer string) ([]byte, error) {
			return readSemanticFileRooted(repositoryRoot, scratchRoot, root, name, limit, pointer)
		}
	}
	presence := input.filePresent
	if presence == nil {
		presence = func(root, name, pointer string) (bool, error) {
			return semanticFilePresentRooted(repositoryRoot, scratchRoot, root, name, pointer)
		}
	}
	if input.GoExecutableVersion == "" {
		return Compilation{}, buildInputError("compile_spec_invalid", "/buildInputs")
	}
	modules, err := decodeModuleStream(input.ModuleList)
	if err != nil {
		return Compilation{}, err
	}
	packages, err := decodePackageStream(input.PackageList)
	if err != nil {
		return Compilation{}, err
	}

	mainModule, descriptors, graphModules, moduleByKey, err := compileModules(repositoryRoot, scratchRoot, modules)
	if err != nil {
		return Compilation{}, err
	}
	if len(descriptors) > MaxRetainedModuleRoots {
		return Compilation{}, buildInputError("retained_module_invalid", "/retainedModules/"+strconv.Itoa(MaxRetainedModuleRoots))
	}
	consumerModule, err := identifyConsumerModule(repositoryRoot, input, packages, moduleByKey)
	if err != nil {
		return Compilation{}, err
	}
	if _, exists := moduleByKey[moduleKey(input.ToolModule.Path, input.ToolModule.Version)]; !exists || !validRequirement(input.ToolModule, false) {
		return Compilation{}, buildInputError("module_graph_state_invalid", "/moduleGraph")
	}

	consumerDescriptor, exists := descriptors[moduleKey(consumerModule.Path, consumerModule.Version)]
	if !exists || !consumerDescriptor.retained.HasRepositoryPath {
		return Compilation{}, buildInputError("module_graph_state_invalid", "/moduleGraph")
	}
	boundaryCandidates, err := compileModuleBoundaryCandidates(repositoryRoot, descriptors, presence)
	if err != nil {
		return Compilation{}, err
	}
	packageCandidates, err := compilePackageCandidates(packages, descriptors, moduleByKey, len(boundaryCandidates))
	if err != nil {
		return Compilation{}, err
	}
	candidates := append(boundaryCandidates, packageCandidates...)
	if err := validateRetainedCandidateCoordinates(candidates); err != nil {
		return Compilation{}, err
	}
	moduleSources, allInputs, boundaryFacts, err := readRetainedCandidates(candidates, reader)
	if err != nil {
		return Compilation{}, err
	}
	consumerFact, exists := boundaryFacts[moduleKey(consumerModule.Path, consumerModule.Version)]
	if !exists || consumerFact.modulePath != consumerModule.Path || consumerFact.goVersion == "" {
		return Compilation{}, buildInputError("normalized_module_file_invalid", "/buildInputs/normalized/goMod")
	}
	goVersion, toolchainVersion := consumerFact.goVersion, consumerFact.toolchainVersion
	sort.Slice(allInputs, func(i, j int) bool {
		left, right := allInputs[i], allInputs[j]
		if left.Module.Module.Path != right.Module.Module.Path {
			return left.Module.Module.Path < right.Module.Module.Path
		}
		if left.Module.Module.Version != right.Module.Module.Version {
			return left.Module.Module.Version < right.Module.Module.Version
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.Kind < right.Kind
	})
	localModules := make([]RetainedLocalModule, 0, len(descriptors))
	for _, descriptor := range descriptors {
		localModules = append(localModules, descriptor.retained)
	}
	sort.Slice(localModules, func(i, j int) bool {
		left, right := localModules[i], localModules[j]
		if left.Module.Path != right.Module.Path {
			return left.Module.Path < right.Module.Path
		}
		if left.Module.Version != right.Module.Version {
			return left.Module.Version < right.Module.Version
		}
		if left.Role != right.Role {
			return left.Role < right.Role
		}
		return left.RepositoryPath < right.RepositoryPath
	})

	graph := GraphSnapshot{state: &graphState{
		consumerModule: consumerModule,
		goVersion:      goVersion,
		toolchain:      toolchainVersion,
		helperDigest:   input.HelperDigest,
		moduleSources:  moduleSources,
		modules:        graphModules,
		toolModule:     input.ToolModule,
	}}
	graphJSON, err := CanonicalGraphSnapshot(graph)
	if err != nil {
		return Compilation{}, buildInputError("module_graph_state_invalid", "/moduleGraph")
	}
	manifest := ManifestSnapshot{state: &manifestState{
		buildTags:           tags,
		schemaImportPath:    input.SchemaImportPath,
		goExecutableVersion: input.GoExecutableVersion,
		graphDigest:         provenance.SHA256(graphJSON),
		localModules:        localModules,
		inputs:              allInputs,
	}}
	_ = mainModule
	return Compilation{graph: graph, manifest: manifest, executableVersion: input.GoExecutableVersion, valid: true}, nil
}

func Preflight(input DiscoveryInput) error {
	_, _, _, err := preflightDiscovery(input)
	return err
}

func preflightDiscovery(input DiscoveryInput) (string, string, []string, error) {
	repositoryRoot, err := filepath.Abs(input.RepositoryRoot)
	if err != nil || !filepath.IsAbs(input.RepositoryRoot) || filepath.Clean(input.RepositoryRoot) != repositoryRoot {
		return "", "", nil, buildInputError("compile_spec_invalid", "/buildInputs")
	}
	scratchRoot, err := filepath.Abs(input.ScratchRoot)
	if err != nil || !filepath.IsAbs(input.ScratchRoot) || filepath.Clean(input.ScratchRoot) != scratchRoot || rootsOverlap(repositoryRoot, scratchRoot) {
		return "", "", nil, buildInputError("compile_spec_invalid", "/buildInputs")
	}
	if input.SchemaDir.String() == "" {
		return "", "", nil, buildInputError("compile_spec_invalid", "/buildInputs")
	}
	if module.CheckImportPath(input.SchemaImportPath) != nil {
		return "", "", nil, buildInputError("schema_import_path_invalid", "/buildInputs/schemaImportPath")
	}
	if _, err := provenance.ParseDigest(input.HelperDigest.String()); err != nil {
		return "", "", nil, buildInputError("compile_spec_invalid", "/buildInputs")
	}
	if !validRequirement(input.ToolModule, false) {
		return "", "", nil, buildInputError("module_graph_state_invalid", "/moduleGraph")
	}
	tags, err := normalizeBuildTags(input.BuildTags)
	if err != nil {
		return "", "", nil, err
	}
	return repositoryRoot, scratchRoot, tags, nil
}

func rootsOverlap(left, right string) bool {
	for _, pair := range [][2]string{{left, right}, {right, left}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

func normalizeBuildTags(values []string) ([]string, error) {
	result := append([]string(nil), values...)
	seen := make(map[string]struct{}, len(result))
	for index, value := range result {
		if value == "" || strings.ContainsAny(value, ", \t\r\n") {
			return nil, buildInputError("build_tag_invalid", "/buildInputs/buildTags/"+strconv.Itoa(index))
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, buildInputError("build_tag_duplicate", "/buildInputs/buildTags/"+strconv.Itoa(index))
		}
		seen[value] = struct{}{}
	}
	sort.Strings(result)
	return result, nil
}

func decodeModuleStream(data []byte) ([]goListModule, error) {
	var result []goListModule
	if err := decodeJSONStream(data, func(raw json.RawMessage) error {
		var value goListModule
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		result = append(result, value)
		return nil
	}); err != nil || len(result) == 0 {
		return nil, buildInputError("module_list_output_invalid", "/moduleGraph")
	}
	return result, nil
}

func decodePackageStream(data []byte) ([]goListPackage, error) {
	var result []goListPackage
	if err := decodeJSONStream(data, func(raw json.RawMessage) error {
		var value goListPackage
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		result = append(result, value)
		return nil
	}); err != nil || len(result) == 0 {
		return nil, buildInputError("package_list_output_invalid", "/retainedInputs")
	}
	return result, nil
}

func decodeJSONStream(data []byte, consume func(json.RawMessage) error) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var raw json.RawMessage
		err := decoder.Decode(&raw)
		if err == io.EOF {
			return nil
		}
		if err != nil || len(raw) == 0 {
			return errors.New("invalid JSON stream")
		}
		document, err := strictdoc.ParseJSON("", raw)
		if err != nil {
			return err
		}
		if err := consume(document.JSON()); err != nil {
			return err
		}
	}
}

func compileModules(repositoryRoot, scratchRoot string, modules []goListModule) (goListModule, map[string]localModuleDescriptor, []ModuleIdentity, map[string]goListModule, error) {
	descriptors := make(map[string]localModuleDescriptor)
	moduleByKey := make(map[string]goListModule, len(modules))
	nonMain := make([]goListModule, 0, len(modules))
	var main goListModule
	mainCount := 0
	for _, item := range modules {
		if item.Path == "" || module.CheckPath(item.Path) != nil {
			return goListModule{}, nil, nil, nil, buildInputError("module_graph_state_invalid", "/moduleGraph")
		}
		key := moduleKey(item.Path, item.Version)
		if _, duplicate := moduleByKey[key]; duplicate {
			return goListModule{}, nil, nil, nil, buildInputError("module_graph_state_invalid", "/moduleGraph")
		}
		moduleByKey[key] = item
		if item.Main {
			mainCount++
			main = item
			if filepath.Clean(item.Dir) != scratchRoot || filepath.Clean(item.GoMod) != filepath.Join(scratchRoot, "go.mod") {
				return goListModule{}, nil, nil, nil, buildInputError("module_graph_state_invalid", "/moduleGraph")
			}
			descriptors[key] = localModuleDescriptor{
				root: item.Dir, goMod: item.GoMod,
				retained: RetainedLocalModule{Role: "scratch-main", Module: ModuleIdentity{Path: item.Path, Version: item.Version, Replacement: ModuleReplacement{Kind: "none"}, Content: ModuleContent{Kind: "local"}}},
			}
			continue
		}
		nonMain = append(nonMain, item)
	}
	if mainCount != 1 {
		return goListModule{}, nil, nil, nil, buildInputError("module_graph_state_invalid", "/moduleGraph")
	}
	sort.Slice(nonMain, func(i, j int) bool {
		return nonMain[i].Path < nonMain[j].Path || nonMain[i].Path == nonMain[j].Path && nonMain[i].Version < nonMain[j].Version
	})
	graphModules := make([]ModuleIdentity, 0, len(nonMain))
	for index, item := range nonMain {
		key := moduleKey(item.Path, item.Version)
		identity, descriptor, local, err := identityFromModule(repositoryRoot, item, index)
		if err != nil {
			return goListModule{}, nil, nil, nil, err
		}
		graphModules = append(graphModules, identity)
		if local {
			descriptors[key] = descriptor
		}
	}
	return main, descriptors, graphModules, moduleByKey, nil
}

func identityFromModule(repositoryRoot string, item goListModule, index int) (ModuleIdentity, localModuleDescriptor, bool, error) {
	base := "/moduleGraph/modules/" + strconv.Itoa(index) + "/content"
	identity := ModuleIdentity{Path: item.Path, Version: item.Version}
	if !validRequirement(ModuleRequirement{Path: item.Path, Version: item.Version}, false) {
		return ModuleIdentity{}, localModuleDescriptor{}, false, buildInputError("module_graph_state_invalid", "/moduleGraph")
	}
	if item.Replace != nil && item.Replace.Version == "" && item.Replace.Dir != "" {
		expectedGoMod := filepath.Join(item.Replace.Dir, "go.mod")
		if item.Replace.GoMod != expectedGoMod || item.GoMod != expectedGoMod {
			return ModuleIdentity{}, localModuleDescriptor{}, false, buildInputError("module_graph_state_invalid", "/moduleGraph")
		}
		rel, ok := repositoryRelative(repositoryRoot, item.Replace.Dir)
		if !ok || !validRepositoryCoordinate(rel) {
			return ModuleIdentity{}, localModuleDescriptor{}, false, buildInputError("module_content_variant_invalid", base+"/kind")
		}
		identity.Replacement = ModuleReplacement{Kind: "repository", RepositoryPath: rel}
		identity.Content = ModuleContent{Kind: "local"}
		return identity, localModuleDescriptor{
			root: item.Replace.Dir, goMod: expectedGoMod,
			retained: RetainedLocalModule{Role: "repository-module", Module: identity, RepositoryPath: rel, HasRepositoryPath: true},
		}, true, nil
	}
	selected := item
	if item.Replace != nil {
		identity.Replacement = ModuleReplacement{Kind: "version", Path: item.Replace.Path, Version: item.Replace.Version}
		identity.Content = ModuleContent{Kind: "remote", Sum: item.Replace.Sum, GoModSum: item.Replace.GoModSum}
		selected = *item.Replace
	} else {
		identity.Replacement = ModuleReplacement{Kind: "none"}
		identity.Content = ModuleContent{Kind: "remote", Sum: item.Sum, GoModSum: item.GoModSum}
	}
	if identity.Content.Sum != "" && !validH1Sum(identity.Content.Sum) {
		return ModuleIdentity{}, localModuleDescriptor{}, false, buildInputError("remote_sum_invalid", base+"/sum")
	}
	graphOnly := selected.Dir == "" && selected.GoMod == "" && identity.Content.Sum == "" && identity.Content.GoModSum == ""
	if !graphOnly && !validH1Sum(identity.Content.GoModSum) {
		return ModuleIdentity{}, localModuleDescriptor{}, false, buildInputError("remote_go_mod_sum_invalid", base+"/goModSum")
	}
	return identity, localModuleDescriptor{}, false, nil
}

func identifyConsumerModule(repositoryRoot string, input DiscoveryInput, packages []goListPackage, modules map[string]goListModule) (ModuleRequirement, error) {
	expectedDir := filepath.Join(repositoryRoot, filepath.FromSlash(input.SchemaDir.String()))
	seenPackages := make(map[string]struct{}, len(packages))
	var consumer ModuleRequirement
	found := false
	for index, pkg := range packages {
		if pkg.ImportPath == "" {
			return ModuleRequirement{}, buildInputError("package_identity_invalid", "/retainedInputs/packages/"+strconv.Itoa(index))
		}
		if _, duplicate := seenPackages[pkg.ImportPath]; duplicate {
			return ModuleRequirement{}, buildInputError("package_identity_duplicate", "/retainedInputs/packages/"+strconv.Itoa(index))
		}
		seenPackages[pkg.ImportPath] = struct{}{}
		if pkg.ImportPath != input.SchemaImportPath {
			continue
		}
		if filepath.Clean(pkg.Dir) != expectedDir || pkg.Module == nil {
			return ModuleRequirement{}, buildInputError("package_identity_invalid", "/retainedInputs/packages/"+strconv.Itoa(index))
		}
		candidate := ModuleRequirement{Path: pkg.Module.Path, Version: pkg.Module.Version}
		if _, exists := modules[moduleKey(candidate.Path, candidate.Version)]; !exists {
			return ModuleRequirement{}, buildInputError("package_identity_invalid", "/retainedInputs/packages/"+strconv.Itoa(index))
		}
		consumer, found = candidate, true
	}
	if !found {
		return ModuleRequirement{}, buildInputError("package_identity_invalid", "/retainedInputs")
	}
	return consumer, nil
}

type moduleBoundaryFact struct {
	modulePath       string
	goVersion        string
	toolchainVersion string
}

type retainedCandidate struct {
	module       RetainedLocalModule
	moduleRoot   string
	path         string
	kind         string
	absolute     string
	boundaryKey  string
	sourceRef    provenance.SourceRef
	hasSourceRef bool
}

func compileModuleBoundaryCandidates(repositoryRoot string, descriptors map[string]localModuleDescriptor, presence semanticPresenceFunc) ([]retainedCandidate, error) {
	keys := make([]string, 0, len(descriptors))
	for key := range descriptors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var candidates []retainedCandidate
	seenSource := map[string]struct{}{}
	for _, key := range keys {
		descriptor := descriptors[key]
		boundaries := []struct {
			name string
			kind string
		}{{"go.mod", "module-file"}, {"go.sum", "module-sum"}}
		for boundaryIndex, boundary := range boundaries {
			name := filepath.Join(descriptor.root, boundary.name)
			if boundaryIndex > 0 {
				present, presenceErr := presence(descriptor.root, name, "/retainedInputs/"+strconv.Itoa(len(candidates)-1))
				if presenceErr != nil {
					return nil, presenceErr
				}
				if !present {
					continue
				}
			}
			candidate := retainedCandidate{module: descriptor.retained, moduleRoot: descriptor.root, path: boundary.name, kind: boundary.kind, absolute: name, boundaryKey: key}
			if descriptor.retained.HasRepositoryPath {
				rel, ok := repositoryRelative(repositoryRoot, name)
				if !ok {
					return nil, buildInputError("module_graph_state_invalid", "/moduleGraph")
				}
				ref, refErr := provenance.RepositoryRef(rel, "")
				if refErr != nil {
					return nil, buildInputError("module_graph_state_invalid", "/moduleGraph")
				}
				if _, duplicate := seenSource[ref.String()]; duplicate {
					return nil, buildInputError("module_graph_state_invalid", "/moduleGraph")
				}
				seenSource[ref.String()] = struct{}{}
				candidate.sourceRef, candidate.hasSourceRef = ref, true
			}
			candidates = append(candidates, candidate)
		}
	}
	return candidates, nil
}

func compilePackageCandidates(packages []goListPackage, descriptors map[string]localModuleDescriptor, modules map[string]goListModule, boundaryCount int) ([]retainedCandidate, error) {
	var candidates []retainedCandidate
	for packageIndex, pkg := range packages {
		if pkg.Standard {
			continue
		}
		nativeGroups := []struct {
			name  string
			paths []string
		}{
			{"CgoFiles", pkg.CgoFiles}, {"CFiles", pkg.CFiles}, {"CXXFiles", pkg.CXXFiles},
			{"MFiles", pkg.MFiles}, {"HFiles", pkg.HFiles}, {"FFiles", pkg.FFiles},
			{"SFiles", pkg.SFiles}, {"SwigFiles", pkg.SwigFiles}, {"SwigCXXFiles", pkg.SwigCXXFiles},
			{"SysoFiles", pkg.SysoFiles},
		}
		for _, group := range nativeGroups {
			if len(group.paths) > 0 {
				return nil, buildInputUnsupportedError("/retainedInputs/packages/" + strconv.Itoa(packageIndex) + "/" + group.name + "/0")
			}
		}
		if pkg.Module == nil {
			return nil, buildInputError("package_identity_invalid", "/retainedInputs/packages/"+strconv.Itoa(packageIndex))
		}
		key := moduleKey(pkg.Module.Path, pkg.Module.Version)
		selected, exists := modules[key]
		if !exists || !sameSelectedModule(*pkg.Module, selected) {
			return nil, buildInputError("package_identity_invalid", "/retainedInputs/packages/"+strconv.Itoa(packageIndex))
		}
		descriptor, local := descriptors[key]
		if !local {
			continue
		}
		packageRel, ok := relativeWithin(descriptor.root, pkg.Dir)
		if !ok {
			return nil, buildInputError("package_identity_invalid", "/retainedInputs/packages/"+strconv.Itoa(packageIndex))
		}
		if boundaryCount+len(candidates)+len(pkg.GoFiles)+len(pkg.EmbedFiles) > MaxRetainedBuildInputs {
			return nil, buildInputError("retained_input_limit_exceeded", "/retainedInputs/"+strconv.Itoa(MaxRetainedBuildInputs))
		}
		members := []struct {
			kind  string
			paths []string
		}{{"go", pkg.GoFiles}, {"embed", pkg.EmbedFiles}}
		for _, group := range members {
			for _, member := range group.paths {
				if !fs.ValidPath(member) || member == "." {
					return nil, buildInputError("retained_path_invalid", "/retainedInputs/"+strconv.Itoa(boundaryCount+len(candidates))+"/path")
				}
				modulePath := filepath.ToSlash(filepath.Join(packageRel, filepath.FromSlash(member)))
				if !fs.ValidPath(modulePath) || modulePath == "." {
					return nil, buildInputError("retained_path_invalid", "/retainedInputs/"+strconv.Itoa(boundaryCount+len(candidates))+"/path")
				}
				candidates = append(candidates, retainedCandidate{module: descriptor.retained, moduleRoot: descriptor.root, path: modulePath, kind: group.kind, absolute: filepath.Join(pkg.Dir, filepath.FromSlash(member))})
			}
		}
	}
	return candidates, nil
}

func validateRetainedCandidateCoordinates(candidates []retainedCandidate) error {
	if len(candidates) > MaxRetainedBuildInputs {
		return buildInputError("retained_input_limit_exceeded", "/retainedInputs/"+strconv.Itoa(MaxRetainedBuildInputs))
	}
	for index, candidate := range candidates {
		for previousIndex := 0; previousIndex < index; previousIndex++ {
			previous := candidates[previousIndex]
			if moduleKey(previous.module.Module.Path, previous.module.Module.Version) != moduleKey(candidate.module.Module.Path, candidate.module.Module.Version) {
				continue
			}
			if previous.path == candidate.path {
				return buildInputError("retained_path_duplicate", "/retainedInputs/"+strconv.Itoa(index)+"/path")
			}
			if strictPathOverlap(previous.path, candidate.path) {
				return buildInputError("retained_path_invalid", "/retainedInputs/"+strconv.Itoa(index)+"/path")
			}
		}
	}
	return nil
}

func readRetainedCandidates(candidates []retainedCandidate, reader semanticReadFunc) ([]provenance.Source, []RetainedBuildInput, map[string]moduleBoundaryFact, error) {
	remaining := int64(MaxRetainedBuildInputTotal)
	sources := make([]provenance.Source, 0)
	inputs := make([]RetainedBuildInput, 0, len(candidates))
	facts := make(map[string]moduleBoundaryFact)
	for index, candidate := range candidates {
		limit := int64(MaxRetainedBuildInputBytes)
		if remaining < limit {
			limit = remaining
		}
		pointer := "/retainedInputs/" + strconv.Itoa(index)
		content, err := reader(candidate.moduleRoot, candidate.absolute, limit, pointer)
		if err != nil {
			return nil, nil, nil, err
		}
		if int64(len(content)) > limit {
			return nil, nil, nil, buildInputError("retained_input_limit_exceeded", pointer)
		}
		remaining -= int64(len(content))
		if candidate.boundaryKey != "" && candidate.kind == "module-file" {
			parsed, parseErr := modfile.Parse("go.mod", content, nil)
			if parseErr != nil || parsed.Module == nil || parsed.Module.Mod.Path != candidate.module.Module.Path {
				return nil, nil, nil, buildInputError("normalized_module_file_invalid", "/buildInputs/normalized/goMod")
			}
			fact := moduleBoundaryFact{modulePath: parsed.Module.Mod.Path}
			if parsed.Go != nil {
				fact.goVersion = parsed.Go.Version
			}
			if parsed.Toolchain != nil {
				fact.toolchainVersion = parsed.Toolchain.Name
			}
			facts[candidate.boundaryKey] = fact
		}
		digest := provenance.SHA256(content)
		inputs = append(inputs, RetainedBuildInput{Module: candidate.module, Path: candidate.path, Kind: candidate.kind, Size: int64(len(content)), Digest: digest})
		if candidate.hasSourceRef {
			sources = append(sources, provenance.Source{Ref: candidate.sourceRef, Digest: digest})
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Ref.String() < sources[j].Ref.String() })
	return sources, inputs, facts, nil
}

func strictPathOverlap(left, right string) bool {
	return strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func sameSelectedModule(left, right goListModule) bool {
	if left.Path != right.Path || left.Version != right.Version || left.Main != right.Main ||
		left.Dir != right.Dir || left.GoMod != right.GoMod ||
		left.GoVersion != right.GoVersion || left.Sum != right.Sum || left.GoModSum != right.GoModSum {
		return false
	}
	if (left.Replace == nil) != (right.Replace == nil) {
		return false
	}
	if left.Replace == nil {
		return true
	}
	return left.Replace.Path == right.Replace.Path && left.Replace.Version == right.Replace.Version &&
		left.Replace.Dir == right.Replace.Dir && left.Replace.GoMod == right.Replace.GoMod &&
		left.Replace.GoVersion == right.Replace.GoVersion && left.Replace.Sum == right.Replace.Sum && left.Replace.GoModSum == right.Replace.GoModSum
}

func readSemanticFile(root, name string, limit int64, pointer string) ([]byte, error) {
	return readSemanticFileRooted(root, "", root, name, limit, pointer)
}

func semanticFilePresentRooted(repositoryRoot, scratchRoot, root, name, pointer string) (bool, error) {
	path, err := semanticFilePath(repositoryRoot, scratchRoot, root, name, pointer)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, buildInputError("retained_input_read_failed", pointer)
	}
	return info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular(), nil
}

func readSemanticFileRooted(repositoryRoot, scratchRoot, root, name string, limit int64, pointer string) ([]byte, error) {
	path, err := semanticFilePath(repositoryRoot, scratchRoot, root, name, pointer)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, buildInputError("retained_input_read_failed", pointer)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, buildInputError("retained_input_symlink", pointer)
	}
	if !info.Mode().IsRegular() {
		return nil, buildInputError("retained_input_not_regular", pointer)
	}
	if info.Size() > limit {
		return nil, buildInputError("retained_input_limit_exceeded", pointer)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, buildInputError("retained_input_read_failed", pointer)
	}
	if int64(len(content)) > limit {
		return nil, buildInputError("retained_input_limit_exceeded", pointer)
	}
	return content, nil
}

func semanticFilePath(repositoryRoot, scratchRoot, root, name, pointer string) (string, error) {
	relative, ok := relativeWithin(root, name)
	if !ok {
		return "", buildInputError("retained_path_invalid", pointer+"/path")
	}
	anchor, walkPath := repositoryRoot, ""
	if selected, withinRepository := relativeWithin(repositoryRoot, root); withinRepository {
		walkPath = filepath.Join(selected, filepath.Dir(relative))
	} else if scratchRoot != "" && filepath.Clean(root) == filepath.Clean(scratchRoot) {
		anchor, walkPath = root, filepath.Dir(relative)
	} else {
		return "", buildInputError("retained_parent_drift", pointer)
	}
	current := anchor
	for _, component := range strings.Split(filepath.ToSlash(walkPath), "/") {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", buildInputError("retained_parent_drift", pointer)
		}
	}
	return name, nil
}

func repositoryRelative(root, name string) (string, bool) {
	rel, ok := relativeWithin(root, name)
	if !ok {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func relativeWithin(root, name string) (string, bool) {
	root = filepath.Clean(root)
	name = filepath.Clean(name)
	rel, err := filepath.Rel(root, name)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

func moduleKey(path, version string) string { return path + "\x00" + version }

func validRequirement(value ModuleRequirement, allowEmptyVersion bool) bool {
	if value.Path == "" || module.CheckPath(value.Path) != nil {
		return false
	}
	if allowEmptyVersion && value.Version == "" {
		return true
	}
	if !semver.IsValid(value.Version) || semver.Canonical(value.Version) != value.Version {
		return false
	}
	_, pathMajor, ok := module.SplitPathVersion(value.Path)
	return ok && module.CheckPathMajor(value.Version, pathMajor) == nil
}

func buildInputError(reason, pointer string) *Error {
	return &Error{
		code: "build_input_invalid", reason: reason, stage: "retain", pointer: pointer,
		message: "build input is invalid", sentinel: errBuildInputInvalid,
	}
}

func buildInputReadbackError(reason, pointer string) *Error {
	return &Error{
		code: "build_input_readback_invalid", reason: reason, stage: "retain-readback", pointer: pointer,
		message: "build input readback state is invalid", sentinel: errBuildInputReadbackInvalid,
	}
}

func buildInputUnsupportedError(pointer string) *Error {
	return &Error{
		code: "build_input_unsupported", reason: "native_input_unsupported", stage: "retain", pointer: pointer,
		message: "native build input is unsupported", sentinel: errBuildInputUnsupported,
	}
}

func manifestSnapshotError(reason, source, pointer string) *Error {
	return &Error{
		code: "build_input_snapshot_invalid", reason: reason, stage: "decode", pointer: pointer, source: source,
		message: "build input manifest snapshot is invalid", sentinel: errBuildInputSnapshotInvalid,
	}
}

func (c Compilation) Graph() (GraphSnapshot, error) {
	if !c.valid || c.graph.state == nil {
		return GraphSnapshot{}, buildInputReadbackError("compilation_state_invalid", "/buildInputCompilation")
	}
	return c.graph, nil
}

func (c Compilation) Manifest() (ManifestSnapshot, error) {
	if !c.valid || c.manifest.state == nil {
		return ManifestSnapshot{}, buildInputReadbackError("compilation_state_invalid", "/buildInputCompilation")
	}
	return c.manifest, nil
}

func (c Compilation) ExecutableVersion() (string, error) {
	if !c.valid || c.executableVersion == "" {
		return "", buildInputReadbackError("compilation_state_invalid", "/buildInputCompilation")
	}
	return c.executableVersion, nil
}

func (s ManifestSnapshot) BuildTags() ([]string, error) {
	if s.state == nil {
		return nil, buildInputReadbackError("manifest_snapshot_state_invalid", "/buildInputs")
	}
	return append([]string(nil), s.state.buildTags...), nil
}

func (s ManifestSnapshot) SchemaImportPath() (string, error) {
	if s.state == nil {
		return "", buildInputReadbackError("manifest_snapshot_state_invalid", "/buildInputs")
	}
	return s.state.schemaImportPath, nil
}

func (s ManifestSnapshot) GoExecutableVersion() (string, error) {
	if s.state == nil {
		return "", buildInputReadbackError("manifest_snapshot_state_invalid", "/buildInputs")
	}
	return s.state.goExecutableVersion, nil
}

func (s ManifestSnapshot) ModuleGraphDigest() (provenance.Digest, error) {
	if s.state == nil {
		return provenance.Digest{}, buildInputReadbackError("manifest_snapshot_state_invalid", "/buildInputs")
	}
	return s.state.graphDigest, nil
}

func (s ManifestSnapshot) LocalModules() ([]RetainedLocalModule, error) {
	if s.state == nil {
		return nil, buildInputReadbackError("manifest_snapshot_state_invalid", "/buildInputs")
	}
	return append([]RetainedLocalModule(nil), s.state.localModules...), nil
}

func (s ManifestSnapshot) Inputs() ([]RetainedBuildInput, error) {
	if s.state == nil {
		return nil, buildInputReadbackError("manifest_snapshot_state_invalid", "/buildInputs")
	}
	return append([]RetainedBuildInput(nil), s.state.inputs...), nil
}

func graphSnapshotError(reason, source, pointer string) *Error {
	return &Error{
		code:     "module_graph_snapshot_invalid",
		reason:   reason,
		stage:    "decode",
		pointer:  pointer,
		source:   source,
		message:  "module graph snapshot is invalid",
		sentinel: errModuleGraphSnapshotInvalid,
	}
}

func graphReadbackError() *Error {
	return &Error{
		code:     "module_graph_readback_invalid",
		reason:   "module_graph_state_invalid",
		stage:    "readback",
		pointer:  "/moduleGraph",
		message:  "module graph readback state is invalid",
		sentinel: errModuleGraphReadbackInvalid,
	}
}

func (s GraphSnapshot) ConsumerModule() (ModuleRequirement, error) {
	if s.state == nil {
		return ModuleRequirement{}, graphReadbackError()
	}
	return s.state.consumerModule, nil
}

func (s GraphSnapshot) GoVersion() (string, error) {
	if s.state == nil {
		return "", graphReadbackError()
	}
	return s.state.goVersion, nil
}

func (s GraphSnapshot) ToolchainVersion() (string, bool, error) {
	if s.state == nil {
		return "", false, graphReadbackError()
	}
	return s.state.toolchain, s.state.toolchain != "", nil
}

func (s GraphSnapshot) ToolModule() (ModuleRequirement, error) {
	if s.state == nil {
		return ModuleRequirement{}, graphReadbackError()
	}
	return s.state.toolModule, nil
}

func (s GraphSnapshot) HelperDigest() (provenance.Digest, error) {
	if s.state == nil {
		return provenance.Digest{}, graphReadbackError()
	}
	return s.state.helperDigest, nil
}

func (s GraphSnapshot) ModuleSources() ([]provenance.Source, error) {
	if s.state == nil {
		return nil, graphReadbackError()
	}
	return append([]provenance.Source(nil), s.state.moduleSources...), nil
}

func (s GraphSnapshot) Modules() ([]ModuleIdentity, error) {
	if s.state == nil {
		return nil, graphReadbackError()
	}
	return append([]ModuleIdentity(nil), s.state.modules...), nil
}

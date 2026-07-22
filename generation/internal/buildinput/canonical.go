package buildinput

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

type graphDocument struct {
	APIVersion     string                   `json:"apiVersion"`
	ConsumerModule requirementDocument      `json:"consumerModule"`
	GoVersion      string                   `json:"goVersion"`
	HelperDigest   string                   `json:"helperDigest"`
	Kind           string                   `json:"kind"`
	ModuleSources  []sourceDocument         `json:"moduleSources"`
	Modules        []moduleIdentityDocument `json:"modules"`
	ToolModule     requirementDocument      `json:"toolModule"`
	Toolchain      string                   `json:"toolchainVersion,omitempty"`
}

type requirementDocument struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

type sourceDocument struct {
	Digest string `json:"digest"`
	Ref    string `json:"ref"`
}

type moduleIdentityDocument struct {
	Content     moduleContentDocument `json:"content"`
	Path        string                `json:"path"`
	Replacement replacementDocument   `json:"replacement"`
	Version     string                `json:"version"`
}

type moduleContentDocument struct {
	GoModSum string `json:"goModSum,omitempty"`
	Kind     string `json:"kind"`
	Sum      string `json:"sum,omitempty"`
}

type replacementDocument struct {
	Kind           string `json:"kind"`
	Path           string `json:"path,omitempty"`
	RepositoryPath string `json:"repositoryPath,omitempty"`
	Version        string `json:"version,omitempty"`
}

type retainedLocalModuleDocument struct {
	Module         moduleIdentityDocument `json:"module"`
	RepositoryPath string                 `json:"repositoryPath,omitempty"`
	Role           string                 `json:"role"`
}

type retainedInputDocument struct {
	Digest string              `json:"digest"`
	Kind   string              `json:"kind"`
	Module requirementDocument `json:"module"`
	Path   string              `json:"path"`
	Size   int64               `json:"size"`
}

type manifestDocument struct {
	APIVersion          string                        `json:"apiVersion"`
	BuildTags           []string                      `json:"buildTags"`
	Digest              string                        `json:"digest"`
	GoExecutableVersion string                        `json:"goExecutableVersion"`
	Inputs              []retainedInputDocument       `json:"inputs"`
	Kind                string                        `json:"kind"`
	LocalModules        []retainedLocalModuleDocument `json:"localModules"`
	ModuleGraphDigest   string                        `json:"moduleGraphDigest"`
	SchemaImportPath    string                        `json:"schemaImportPath"`
}

type manifestPreimageDocument struct {
	APIVersion          string                        `json:"apiVersion"`
	BuildTags           []string                      `json:"buildTags"`
	GoExecutableVersion string                        `json:"goExecutableVersion"`
	Inputs              []retainedInputDocument       `json:"inputs"`
	Kind                string                        `json:"kind"`
	LocalModules        []retainedLocalModuleDocument `json:"localModules"`
	ModuleGraphDigest   string                        `json:"moduleGraphDigest"`
	SchemaImportPath    string                        `json:"schemaImportPath"`
}

//go:embed ent-helper-module-graph-v1.schema.json
var embeddedGraphSchema []byte

//go:embed retained-build-input-manifest-v1.schema.json
var embeddedManifestSchema []byte

func GraphSchema() []byte {
	return append([]byte(nil), embeddedGraphSchema...)
}

func ManifestSchema() []byte {
	return append([]byte(nil), embeddedManifestSchema...)
}

func ParseGraph(source provenance.DomainSource, data []byte) (GraphSnapshot, error) {
	if source.String() == "" {
		return GraphSnapshot{}, graphSnapshotError("document_invalid", "", "")
	}
	if !utf8.Valid(data) {
		return GraphSnapshot{}, graphSnapshotError("unicode_invalid", source.String(), "")
	}
	document, err := strictdoc.ParseJSON(source.String(), data)
	if err != nil {
		return GraphSnapshot{}, projectGraphDocumentError(source.String(), err, false)
	}
	if reason, pointer := graphShapeIssue(document.JSON()); reason != "" {
		return GraphSnapshot{}, graphSnapshotError(reason, source.String(), pointer)
	}
	var wire graphDocument
	if err := document.Decode(&wire); err != nil {
		return GraphSnapshot{}, projectGraphDocumentError(source.String(), err, true)
	}
	rawGraph := mustObjectDocument(document.JSON())
	rawModules := rawObjectArray(rawGraph["modules"])
	if wire.APIVersion != moduleGraphAPIVersion {
		return GraphSnapshot{}, graphSnapshotError("version_unsupported", source.String(), "/apiVersion")
	}
	if wire.Kind != moduleGraphKind {
		return GraphSnapshot{}, graphSnapshotError("kind_invalid", source.String(), "/kind")
	}
	helperDigest, err := provenance.ParseDigest(wire.HelperDigest)
	if err != nil {
		return GraphSnapshot{}, graphSnapshotError("canonical_invalid", source.String(), "/helperDigest")
	}
	if pointer := invalidRequirementPointer(wire.ConsumerModule, "/consumerModule"); pointer != "" {
		return GraphSnapshot{}, graphSnapshotError("module_identity_invalid", source.String(), pointer)
	}
	if pointer := invalidRequirementPointer(wire.ToolModule, "/toolModule"); pointer != "" {
		return GraphSnapshot{}, graphSnapshotError("module_identity_invalid", source.String(), pointer)
	}
	if !validGoDirective(wire.GoVersion) {
		return GraphSnapshot{}, graphSnapshotError("canonical_invalid", source.String(), "/goVersion")
	}
	if wire.Toolchain != "" && !validToolchainDirective(wire.Toolchain) {
		return GraphSnapshot{}, graphSnapshotError("canonical_invalid", source.String(), "/toolchainVersion")
	}
	state := &graphState{
		consumerModule: ModuleRequirement{Path: wire.ConsumerModule.Path, Version: wire.ConsumerModule.Version},
		goVersion:      wire.GoVersion,
		toolchain:      wire.Toolchain,
		helperDigest:   helperDigest,
		moduleSources:  make([]provenance.Source, len(wire.ModuleSources)),
		modules:        make([]ModuleIdentity, len(wire.Modules)),
		toolModule:     ModuleRequirement{Path: wire.ToolModule.Path, Version: wire.ToolModule.Version},
	}
	for index, item := range wire.ModuleSources {
		ref, refErr := provenance.ParseSourceRef(item.Ref)
		digest, digestErr := provenance.ParseDigest(item.Digest)
		base := "/moduleSources/" + strconv.Itoa(index)
		if refErr != nil {
			return GraphSnapshot{}, graphSnapshotError("source_ref_invalid", source.String(), base+"/ref")
		}
		if digestErr != nil {
			return GraphSnapshot{}, graphSnapshotError("source_digest_invalid", source.String(), base+"/digest")
		}
		state.moduleSources[index] = provenance.Source{Ref: ref, Digest: digest}
		if index > 0 {
			previous := state.moduleSources[index-1].Ref.String()
			if previous == ref.String() {
				return GraphSnapshot{}, graphSnapshotError("source_conflict", source.String(), base+"/ref")
			}
			if previous > ref.String() {
				return GraphSnapshot{}, graphSnapshotError("canonical_order_invalid", source.String(), "/moduleSources")
			}
		}
	}
	for index, item := range wire.Modules {
		if err := validateGraphModule(source.String(), index, item, rawModules[index]); err != nil {
			return GraphSnapshot{}, err
		}
		state.modules[index] = ModuleIdentity{
			Path: item.Path, Version: item.Version,
			Replacement: ModuleReplacement{Kind: item.Replacement.Kind, Path: item.Replacement.Path, Version: item.Replacement.Version, RepositoryPath: item.Replacement.RepositoryPath},
			Content:     ModuleContent{Kind: item.Content.Kind, Sum: item.Content.Sum, GoModSum: item.Content.GoModSum},
		}
		if index > 0 {
			previous := state.modules[index-1]
			current := state.modules[index]
			if previous.Path == current.Path && previous.Version == current.Version {
				return GraphSnapshot{}, graphSnapshotError("module_identity_duplicate", source.String(), "/modules/"+strconv.Itoa(index))
			}
			if previous.Path > current.Path || previous.Path == current.Path && previous.Version > current.Version {
				return GraphSnapshot{}, graphSnapshotError("canonical_order_invalid", source.String(), "/modules")
			}
		}
	}
	snapshot := GraphSnapshot{state: state}
	canonical, err := CanonicalGraphSnapshot(snapshot)
	if err != nil {
		return GraphSnapshot{}, graphSnapshotError("canonical_invalid", source.String(), "")
	}
	if !bytes.Equal(data, canonical) {
		return GraphSnapshot{}, graphSnapshotError("canonical_invalid", source.String(), "")
	}
	return snapshot, nil
}

func validateGraphModule(source string, index int, item moduleIdentityDocument, raw map[string]any) *Error {
	base := "/modules/" + strconv.Itoa(index)
	if pointer := invalidRequirementPointer(requirementDocument{Path: item.Path, Version: item.Version}, base); pointer != "" {
		return graphSnapshotError("module_identity_invalid", source, pointer)
	}
	rawReplacement, _ := raw["replacement"].(map[string]any)
	switch item.Replacement.Kind {
	case "none":
		for _, field := range []string{"path", "version", "repositoryPath"} {
			if _, present := rawReplacement[field]; present {
				return graphSnapshotError("replacement_variant_invalid", source, base+"/replacement/"+field)
			}
		}
	case "version":
		if _, present := rawReplacement["repositoryPath"]; present {
			return graphSnapshotError("replacement_variant_invalid", source, base+"/replacement/repositoryPath")
		}
		if pointer := invalidRequirementPointer(requirementDocument{Path: item.Replacement.Path, Version: item.Replacement.Version}, base+"/replacement"); pointer != "" {
			return graphSnapshotError("replacement_variant_invalid", source, pointer)
		}
	case "repository":
		for _, field := range []string{"path", "version"} {
			if _, present := rawReplacement[field]; present {
				return graphSnapshotError("replacement_variant_invalid", source, base+"/replacement/"+field)
			}
		}
		if !validRepositoryCoordinate(item.Replacement.RepositoryPath) {
			return graphSnapshotError("replacement_variant_invalid", source, base+"/replacement/repositoryPath")
		}
	default:
		return graphSnapshotError("replacement_variant_invalid", source, base+"/replacement/kind")
	}

	rawContent, _ := raw["content"].(map[string]any)
	switch item.Content.Kind {
	case "local":
		for _, field := range []string{"sum", "goModSum"} {
			if _, present := rawContent[field]; present {
				return graphSnapshotError("content_variant_invalid", source, base+"/content/"+field)
			}
		}
		if item.Replacement.Kind != "repository" {
			return graphSnapshotError("content_variant_invalid", source, base+"/content/kind")
		}
	case "remote":
		if item.Replacement.Kind == "repository" {
			return graphSnapshotError("content_variant_invalid", source, base+"/content/kind")
		}
		if _, present := rawContent["sum"]; present && !validH1Sum(item.Content.Sum) {
			return graphSnapshotError("remote_sum_invalid", source, base+"/content/sum")
		}
		if item.Content.GoModSum == "" && item.Content.Sum != "" || item.Content.GoModSum != "" && !validH1Sum(item.Content.GoModSum) {
			return graphSnapshotError("remote_go_mod_sum_invalid", source, base+"/content/goModSum")
		}
	default:
		return graphSnapshotError("content_variant_invalid", source, base+"/content/kind")
	}
	return nil
}

func validH1Sum(value string) bool {
	if !strings.HasPrefix(value, "h1:") {
		return false
	}
	encoded := strings.TrimPrefix(value, "h1:")
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	return err == nil && len(decoded) == 32 && base64.StdEncoding.EncodeToString(decoded) == encoded
}

func CanonicalGraphSnapshot(snapshot GraphSnapshot) ([]byte, error) {
	if snapshot.state == nil {
		return nil, graphReadbackError()
	}
	if reason, pointer := graphStateIssue(snapshot.state); reason != "" {
		return nil, graphSnapshotError(reason, "", pointer)
	}
	wire := graphDocument{
		APIVersion:     moduleGraphAPIVersion,
		ConsumerModule: requirementDocument{Path: snapshot.state.consumerModule.Path, Version: snapshot.state.consumerModule.Version},
		GoVersion:      snapshot.state.goVersion,
		HelperDigest:   snapshot.state.helperDigest.String(),
		Kind:           moduleGraphKind,
		ModuleSources:  make([]sourceDocument, len(snapshot.state.moduleSources)),
		Modules:        make([]moduleIdentityDocument, len(snapshot.state.modules)),
		ToolModule:     requirementDocument{Path: snapshot.state.toolModule.Path, Version: snapshot.state.toolModule.Version},
		Toolchain:      snapshot.state.toolchain,
	}
	for index, item := range snapshot.state.moduleSources {
		wire.ModuleSources[index] = sourceDocument{Digest: item.Digest.String(), Ref: item.Ref.String()}
	}
	for index, item := range snapshot.state.modules {
		wire.Modules[index] = moduleIdentityDocument{
			Content:     moduleContentDocument{GoModSum: item.Content.GoModSum, Kind: item.Content.Kind, Sum: item.Content.Sum},
			Path:        item.Path,
			Replacement: replacementDocument{Kind: item.Replacement.Kind, Path: item.Replacement.Path, RepositoryPath: item.Replacement.RepositoryPath, Version: item.Replacement.Version},
			Version:     item.Version,
		}
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, graphSnapshotError("canonical_invalid", "", "/moduleGraph")
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		return nil, graphSnapshotError("canonical_invalid", "", "/moduleGraph")
	}
	return canonical, nil
}

func graphStateIssue(state *graphState) (string, string) {
	if state == nil {
		return "canonical_invalid", "/moduleGraph"
	}
	if pointer := invalidRequirementPointer(requirementDocument{Path: state.consumerModule.Path, Version: state.consumerModule.Version}, "/consumerModule"); pointer != "" {
		return "module_identity_invalid", pointer
	}
	if pointer := invalidRequirementPointer(requirementDocument{Path: state.toolModule.Path, Version: state.toolModule.Version}, "/toolModule"); pointer != "" {
		return "module_identity_invalid", pointer
	}
	if !validGoDirective(state.goVersion) {
		return "canonical_invalid", "/goVersion"
	}
	if state.toolchain != "" && !validToolchainDirective(state.toolchain) {
		return "canonical_invalid", "/toolchainVersion"
	}
	if _, err := provenance.ParseDigest(state.helperDigest.String()); err != nil {
		return "canonical_invalid", "/helperDigest"
	}
	for index, item := range state.moduleSources {
		base := "/moduleSources/" + strconv.Itoa(index)
		if _, err := provenance.ParseSourceRef(item.Ref.String()); err != nil {
			return "source_ref_invalid", base + "/ref"
		}
		if _, err := provenance.ParseDigest(item.Digest.String()); err != nil {
			return "source_digest_invalid", base + "/digest"
		}
		if index > 0 {
			previous := state.moduleSources[index-1].Ref.String()
			if previous == item.Ref.String() {
				return "source_conflict", base + "/ref"
			}
			if previous > item.Ref.String() {
				return "canonical_order_invalid", "/moduleSources"
			}
		}
	}
	for index, item := range state.modules {
		if reason, pointer := graphModuleStateIssue(index, item); reason != "" {
			return reason, pointer
		}
		if index > 0 {
			previous := state.modules[index-1]
			if previous.Path == item.Path && previous.Version == item.Version {
				return "module_identity_duplicate", "/modules/" + strconv.Itoa(index)
			}
			if previous.Path > item.Path || previous.Path == item.Path && previous.Version > item.Version {
				return "canonical_order_invalid", "/modules"
			}
		}
	}
	return "", ""
}

func graphModuleStateIssue(index int, item ModuleIdentity) (string, string) {
	base := "/modules/" + strconv.Itoa(index)
	if pointer := invalidRequirementPointer(requirementDocument{Path: item.Path, Version: item.Version}, base); pointer != "" {
		return "module_identity_invalid", pointer
	}
	switch item.Replacement.Kind {
	case "none":
		if item.Replacement.Path != "" {
			return "replacement_variant_invalid", base + "/replacement/path"
		}
		if item.Replacement.Version != "" {
			return "replacement_variant_invalid", base + "/replacement/version"
		}
		if item.Replacement.RepositoryPath != "" {
			return "replacement_variant_invalid", base + "/replacement/repositoryPath"
		}
	case "version":
		if item.Replacement.RepositoryPath != "" {
			return "replacement_variant_invalid", base + "/replacement/repositoryPath"
		}
		if pointer := invalidRequirementPointer(requirementDocument{Path: item.Replacement.Path, Version: item.Replacement.Version}, base+"/replacement"); pointer != "" {
			return "replacement_variant_invalid", pointer
		}
	case "repository":
		if item.Replacement.Path != "" {
			return "replacement_variant_invalid", base + "/replacement/path"
		}
		if item.Replacement.Version != "" {
			return "replacement_variant_invalid", base + "/replacement/version"
		}
		if !validRepositoryCoordinate(item.Replacement.RepositoryPath) {
			return "replacement_variant_invalid", base + "/replacement/repositoryPath"
		}
	default:
		return "replacement_variant_invalid", base + "/replacement/kind"
	}
	switch item.Content.Kind {
	case "local":
		if item.Content.Sum != "" {
			return "content_variant_invalid", base + "/content/sum"
		}
		if item.Content.GoModSum != "" {
			return "content_variant_invalid", base + "/content/goModSum"
		}
		if item.Replacement.Kind != "repository" {
			return "content_variant_invalid", base + "/content/kind"
		}
	case "remote":
		if item.Replacement.Kind == "repository" {
			return "content_variant_invalid", base + "/content/kind"
		}
		if item.Content.Sum != "" && !validH1Sum(item.Content.Sum) {
			return "remote_sum_invalid", base + "/content/sum"
		}
		if item.Content.GoModSum == "" && item.Content.Sum != "" || item.Content.GoModSum != "" && !validH1Sum(item.Content.GoModSum) {
			return "remote_go_mod_sum_invalid", base + "/content/goModSum"
		}
	default:
		return "content_variant_invalid", base + "/content/kind"
	}
	return "", ""
}

func CanonicalGraph(compilation Compilation) ([]byte, error) {
	graph, err := compilation.Graph()
	if err != nil {
		return nil, err
	}
	return CanonicalGraphSnapshot(graph)
}

func CanonicalManifest(compilation Compilation) ([]byte, error) {
	manifest, err := compilation.Manifest()
	if err != nil {
		return nil, err
	}
	return canonicalManifestState(manifest.state, false)
}

func CanonicalSnapshot(snapshot ManifestSnapshot) ([]byte, error) {
	if snapshot.state == nil {
		return nil, buildInputReadbackError("manifest_snapshot_state_invalid", "/buildInputs")
	}
	return canonicalManifestState(snapshot.state, true)
}

func ManifestDigest(snapshot ManifestSnapshot) (provenance.Digest, error) {
	if snapshot.state == nil {
		return provenance.Digest{}, buildInputReadbackError("manifest_snapshot_state_invalid", "/buildInputs")
	}
	normalized, err := normalizeManifestState(snapshot.state, true, "")
	if err != nil {
		return provenance.Digest{}, err
	}
	encoded, err := json.Marshal(manifestPreimageFromState(normalized))
	if err != nil {
		return provenance.Digest{}, manifestSnapshotError("canonical_invalid", "", "/buildInputs")
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		return provenance.Digest{}, manifestSnapshotError("canonical_invalid", "", "/buildInputs")
	}
	return provenance.SHA256(canonical), nil
}

func canonicalManifestState(state *manifestState, snapshot bool) ([]byte, error) {
	normalized, err := normalizeManifestState(state, snapshot, "")
	if err != nil {
		return nil, err
	}
	preimage := manifestPreimageFromState(normalized)
	preimageJSON, err := json.Marshal(preimage)
	if err != nil {
		return nil, manifestCanonicalError(snapshot, "", "/buildInputs")
	}
	preimageJCS, err := jcs.Transform(preimageJSON)
	if err != nil {
		return nil, manifestCanonicalError(snapshot, "", "/buildInputs")
	}
	document := manifestDocument{
		APIVersion: preimage.APIVersion, BuildTags: preimage.BuildTags,
		Digest: provenance.SHA256(preimageJCS).String(), GoExecutableVersion: preimage.GoExecutableVersion,
		Inputs: preimage.Inputs, Kind: preimage.Kind, LocalModules: preimage.LocalModules,
		ModuleGraphDigest: preimage.ModuleGraphDigest, SchemaImportPath: preimage.SchemaImportPath,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, manifestCanonicalError(snapshot, "", "/buildInputs")
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		return nil, manifestCanonicalError(snapshot, "", "/buildInputs")
	}
	return canonical, nil
}

func ParseManifest(source provenance.DomainSource, data []byte) (ManifestSnapshot, error) {
	if source.String() == "" {
		return ManifestSnapshot{}, manifestSnapshotError("document_invalid", "", "")
	}
	if !utf8.Valid(data) {
		return ManifestSnapshot{}, manifestSnapshotError("unicode_invalid", source.String(), "")
	}
	document, err := strictdoc.ParseJSON(source.String(), data)
	if err != nil {
		return ManifestSnapshot{}, projectManifestDocumentError(source.String(), err, false)
	}
	if reason, pointer := manifestShapeIssue(document.JSON()); reason != "" {
		return ManifestSnapshot{}, manifestSnapshotError(reason, source.String(), pointer)
	}
	var wire manifestDocument
	if err := document.Decode(&wire); err != nil {
		return ManifestSnapshot{}, projectManifestDocumentError(source.String(), err, true)
	}
	rawManifest := mustObjectDocument(document.JSON())
	rawLocalModules := rawObjectArray(rawManifest["localModules"])
	if wire.APIVersion != "nexa.dev/retained-build-input-manifest/v1" {
		return ManifestSnapshot{}, manifestSnapshotError("version_unsupported", source.String(), "/apiVersion")
	}
	if wire.Kind != "RetainedBuildInputManifest" {
		return ManifestSnapshot{}, manifestSnapshotError("kind_invalid", source.String(), "/kind")
	}
	if module.CheckImportPath(wire.SchemaImportPath) != nil {
		return ManifestSnapshot{}, manifestSnapshotError("schema_import_path_invalid", source.String(), "/schemaImportPath")
	}
	if wire.GoExecutableVersion == "" {
		return ManifestSnapshot{}, manifestSnapshotError("tool_version_invalid", source.String(), "/goExecutableVersion")
	}
	graphDigest, err := provenance.ParseDigest(wire.ModuleGraphDigest)
	if err != nil {
		return ManifestSnapshot{}, manifestSnapshotError("module_graph_digest_invalid", source.String(), "/moduleGraphDigest")
	}
	seenTags := make(map[string]struct{}, len(wire.BuildTags))
	for index, tag := range wire.BuildTags {
		if tag == "" || strings.ContainsAny(tag, ", \t\r\n") {
			return ManifestSnapshot{}, manifestSnapshotError("build_tag_invalid", source.String(), "/buildTags/"+strconv.Itoa(index))
		}
		if _, duplicate := seenTags[tag]; duplicate {
			return ManifestSnapshot{}, manifestSnapshotError("build_tag_duplicate", source.String(), "/buildTags/"+strconv.Itoa(index))
		}
		seenTags[tag] = struct{}{}
	}
	state := &manifestState{
		buildTags: append([]string{}, wire.BuildTags...), schemaImportPath: wire.SchemaImportPath,
		goExecutableVersion: wire.GoExecutableVersion, graphDigest: graphDigest,
		localModules: make([]RetainedLocalModule, len(wire.LocalModules)),
		inputs:       make([]RetainedBuildInput, len(wire.Inputs)),
	}
	for index, item := range wire.LocalModules {
		_, hasRepositoryPath := rawLocalModules[index]["repositoryPath"]
		state.localModules[index] = RetainedLocalModule{
			Role: item.Role, RepositoryPath: item.RepositoryPath, HasRepositoryPath: hasRepositoryPath,
			Module: moduleIdentityFromDocument(item.Module),
		}
	}
	moduleIndex := make(map[string]RetainedLocalModule, len(state.localModules))
	for index, item := range state.localModules {
		if suffix := retainedLocalModuleSnapshotIssue(item, rawLocalModules[index]); suffix != "" {
			return ManifestSnapshot{}, manifestSnapshotError("retained_module_invalid", source.String(), "/localModules/"+strconv.Itoa(index)+suffix)
		}
		key := moduleKey(item.Module.Path, item.Module.Version)
		if _, duplicate := moduleIndex[key]; duplicate {
			return ManifestSnapshot{}, manifestSnapshotError("retained_module_duplicate", source.String(), "/localModules/"+strconv.Itoa(index)+"/module")
		}
		moduleIndex[key] = item
	}
	if len(state.localModules) > MaxRetainedModuleRoots {
		return ManifestSnapshot{}, manifestSnapshotError("retained_module_invalid", source.String(), "/localModules/"+strconv.Itoa(MaxRetainedModuleRoots))
	}

	// Resolve every reference and validate every module-relative coordinate
	// before inspecting any input payload metadata. This preserves the frozen
	// snapshot error order for compound-invalid documents.
	for index, item := range wire.Inputs {
		resolved, exists := moduleIndex[moduleKey(item.Module.Path, item.Module.Version)]
		if !exists {
			return ManifestSnapshot{}, manifestSnapshotError("retained_input_invalid", source.String(), "/inputs/"+strconv.Itoa(index)+"/module")
		}
		state.inputs[index] = RetainedBuildInput{Module: resolved, Path: item.Path, Kind: item.Kind, Size: item.Size}
	}
	for index, item := range state.inputs {
		base := "/inputs/" + strconv.Itoa(index)
		for previousIndex := 0; previousIndex < index; previousIndex++ {
			previous := state.inputs[previousIndex]
			if moduleKey(previous.Module.Module.Path, previous.Module.Module.Version) != moduleKey(item.Module.Module.Path, item.Module.Module.Version) {
				continue
			}
			if previous.Path == item.Path {
				return ManifestSnapshot{}, manifestSnapshotError("retained_input_duplicate", source.String(), base+"/path")
			}
			if strictPathOverlap(previous.Path, item.Path) {
				return ManifestSnapshot{}, manifestSnapshotError("retained_input_invalid", source.String(), base+"/path")
			}
		}
		if !fs.ValidPath(item.Path) || item.Path == "." {
			return ManifestSnapshot{}, manifestSnapshotError("retained_input_invalid", source.String(), base+"/path")
		}
	}
	var totalSize int64
	for index, item := range wire.Inputs {
		base := "/inputs/" + strconv.Itoa(index)
		if index >= MaxRetainedBuildInputs {
			return ManifestSnapshot{}, manifestSnapshotError("retained_input_invalid", source.String(), base)
		}
		if item.Kind != "go" && item.Kind != "embed" && item.Kind != "module-file" && item.Kind != "module-sum" {
			return ManifestSnapshot{}, manifestSnapshotError("retained_input_invalid", source.String(), base+"/kind")
		}
		if item.Size < 0 || item.Size > MaxRetainedBuildInputBytes {
			return ManifestSnapshot{}, manifestSnapshotError("retained_input_invalid", source.String(), base+"/size")
		}
		totalSize += item.Size
		if totalSize > MaxRetainedBuildInputTotal {
			return ManifestSnapshot{}, manifestSnapshotError("retained_input_invalid", source.String(), base+"/size")
		}
		digest, digestErr := provenance.ParseDigest(item.Digest)
		if digestErr != nil {
			return ManifestSnapshot{}, manifestSnapshotError("retained_input_invalid", source.String(), base+"/digest")
		}
		state.inputs[index].Digest = digest
	}
	normalized, err := normalizeManifestState(state, true, source.String())
	if err != nil {
		return ManifestSnapshot{}, err
	}
	if !sameManifestOrder(state, normalized) {
		return ManifestSnapshot{}, manifestSnapshotError("canonical_order_invalid", source.String(), "")
	}
	canonical, err := canonicalManifestState(normalized, true)
	if err != nil {
		return ManifestSnapshot{}, err
	}
	var canonicalWire manifestDocument
	if json.Unmarshal(canonical, &canonicalWire) != nil {
		return ManifestSnapshot{}, manifestSnapshotError("canonical_invalid", source.String(), "")
	}
	if wire.Digest != canonicalWire.Digest {
		return ManifestSnapshot{}, manifestSnapshotError("digest_mismatch", source.String(), "/digest")
	}
	if !bytes.Equal(data, canonical) {
		return ManifestSnapshot{}, manifestSnapshotError("canonical_invalid", source.String(), "")
	}
	return ManifestSnapshot{state: normalized}, nil
}

func normalizeManifestState(state *manifestState, snapshot bool, source string) (*manifestState, error) {
	if state == nil {
		return nil, manifestSemanticError(snapshot, source, "manifest_snapshot_state_invalid", "/buildInputs")
	}
	normalized := &manifestState{
		buildTags: append([]string{}, state.buildTags...), schemaImportPath: state.schemaImportPath,
		goExecutableVersion: state.goExecutableVersion, graphDigest: state.graphDigest,
		localModules: append([]RetainedLocalModule(nil), state.localModules...),
		inputs:       append([]RetainedBuildInput(nil), state.inputs...),
	}
	if module.CheckImportPath(normalized.schemaImportPath) != nil {
		return nil, manifestSemanticError(snapshot, source, "schema_import_path_invalid", "/schemaImportPath")
	}
	if normalized.goExecutableVersion == "" {
		return nil, manifestSemanticError(snapshot, source, "tool_version_invalid", "/goExecutableVersion")
	}
	if _, err := provenance.ParseDigest(normalized.graphDigest.String()); err != nil {
		return nil, manifestSemanticError(snapshot, source, "module_graph_digest_invalid", "/moduleGraphDigest")
	}
	seenTags := make(map[string]struct{}, len(normalized.buildTags))
	for index, tag := range normalized.buildTags {
		if tag == "" || strings.ContainsAny(tag, ", \t\r\n") {
			return nil, manifestSemanticError(snapshot, source, "build_tag_invalid", manifestTagPointer(snapshot, index))
		}
		if _, duplicate := seenTags[tag]; duplicate {
			return nil, manifestSemanticError(snapshot, source, "build_tag_duplicate", manifestTagPointer(snapshot, index))
		}
		seenTags[tag] = struct{}{}
	}
	sort.Strings(normalized.buildTags)
	modules := make(map[string]RetainedLocalModule, len(normalized.localModules))
	for index, item := range normalized.localModules {
		if suffix := retainedLocalModuleIssue(item); suffix != "" {
			pointer := manifestModulePointer(snapshot, index)
			if snapshot {
				pointer += suffix
			}
			return nil, manifestSemanticError(snapshot, source, "retained_module_invalid", pointer)
		}
		key := moduleKey(item.Module.Path, item.Module.Version)
		if _, duplicate := modules[key]; duplicate {
			pointer := manifestModulePointer(snapshot, index)
			if snapshot {
				pointer += "/module"
			}
			return nil, manifestSemanticError(snapshot, source, "retained_module_duplicate", pointer)
		}
		modules[key] = item
	}
	if len(normalized.localModules) > MaxRetainedModuleRoots {
		return nil, manifestSemanticError(snapshot, source, "retained_module_invalid", manifestModulePointer(snapshot, MaxRetainedModuleRoots))
	}
	for index, item := range normalized.inputs {
		base := manifestInputPointer(snapshot, index)
		resolved, exists := modules[moduleKey(item.Module.Module.Path, item.Module.Module.Version)]
		if !exists || resolved != item.Module {
			reason := "retained_module_invalid"
			if snapshot {
				reason = "retained_input_invalid"
			}
			return nil, manifestSemanticError(snapshot, source, reason, base+"/module")
		}
		for previousIndex := 0; previousIndex < index; previousIndex++ {
			previous := normalized.inputs[previousIndex]
			if moduleKey(previous.Module.Module.Path, previous.Module.Module.Version) != moduleKey(item.Module.Module.Path, item.Module.Module.Version) {
				continue
			}
			if previous.Path == item.Path {
				reason := "retained_path_duplicate"
				if snapshot {
					reason = "retained_input_duplicate"
				}
				return nil, manifestSemanticError(snapshot, source, reason, base+"/path")
			}
			if strictPathOverlap(previous.Path, item.Path) {
				reason := "retained_path_invalid"
				if snapshot {
					reason = "retained_input_invalid"
				}
				return nil, manifestSemanticError(snapshot, source, reason, base+"/path")
			}
		}
		if !fs.ValidPath(item.Path) || item.Path == "." {
			reason := "retained_path_invalid"
			if snapshot {
				reason = "retained_input_invalid"
			}
			return nil, manifestSemanticError(snapshot, source, reason, base+"/path")
		}
	}
	var totalSize int64
	for index, item := range normalized.inputs {
		base := manifestInputPointer(snapshot, index)
		if index >= MaxRetainedBuildInputs {
			reason, pointer := "retained_input_limit_exceeded", base
			if snapshot {
				reason = "retained_input_invalid"
			}
			return nil, manifestSemanticError(snapshot, source, reason, pointer)
		}
		if item.Kind != "go" && item.Kind != "embed" && item.Kind != "module-file" && item.Kind != "module-sum" {
			reason := "retained_kind_invalid"
			if snapshot {
				reason = "retained_input_invalid"
			}
			return nil, manifestSemanticError(snapshot, source, reason, base+"/kind")
		}
		if item.Size < 0 || item.Size > MaxRetainedBuildInputBytes {
			reason := "retained_input_limit_exceeded"
			pointer := base
			if snapshot {
				reason = "retained_input_invalid"
				pointer += "/size"
			}
			return nil, manifestSemanticError(snapshot, source, reason, pointer)
		}
		totalSize += item.Size
		if totalSize > MaxRetainedBuildInputTotal {
			reason := "retained_input_limit_exceeded"
			pointer := base
			if snapshot {
				reason = "retained_input_invalid"
				pointer += "/size"
			}
			return nil, manifestSemanticError(snapshot, source, reason, pointer)
		}
		if _, err := provenance.ParseDigest(item.Digest.String()); err != nil {
			reason := "retained_input_digest_drift"
			if snapshot {
				reason = "retained_input_invalid"
			}
			return nil, manifestSemanticError(snapshot, source, reason, base+"/digest")
		}
	}
	sort.Slice(normalized.localModules, func(i, j int) bool {
		left, right := normalized.localModules[i], normalized.localModules[j]
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
	sort.Slice(normalized.inputs, func(i, j int) bool {
		left, right := normalized.inputs[i], normalized.inputs[j]
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
	return normalized, nil
}

func retainedLocalModuleIssue(item RetainedLocalModule) string {
	if item.Module.Path == "" || module.CheckPath(item.Module.Path) != nil || item.Module.Content.Kind != "local" || item.Module.Content.Sum != "" || item.Module.Content.GoModSum != "" {
		return "/module"
	}
	switch item.Role {
	case "scratch-main":
		if item.HasRepositoryPath || item.RepositoryPath != "" {
			return "/repositoryPath"
		}
		if item.Module.Version != "" || item.Module.Replacement.Kind != "none" || item.Module.Replacement.Path != "" || item.Module.Replacement.Version != "" || item.Module.Replacement.RepositoryPath != "" {
			return "/module"
		}
		return ""
	case "repository-module":
		if !item.HasRepositoryPath || !validRepositoryCoordinate(item.RepositoryPath) {
			return "/repositoryPath"
		}
		if item.Module.Replacement.Kind != "repository" || item.Module.Replacement.RepositoryPath != item.RepositoryPath || invalidRequirementPointer(requirementDocument{Path: item.Module.Path, Version: item.Module.Version}, "") != "" {
			return "/module"
		}
		return ""
	default:
		return "/role"
	}
}

func retainedLocalModuleSnapshotIssue(item RetainedLocalModule, raw map[string]any) string {
	moduleObject, _ := raw["module"].(map[string]any)
	contentObject, _ := moduleObject["content"].(map[string]any)
	replacementObject, _ := moduleObject["replacement"].(map[string]any)
	if item.Module.Path == "" || module.CheckPath(item.Module.Path) != nil {
		return "/module/path"
	}
	switch item.Role {
	case "scratch-main":
		if item.Module.Version != "" {
			return "/module/version"
		}
	case "repository-module":
		if pointer := invalidRequirementPointer(requirementDocument{Path: item.Module.Path, Version: item.Module.Version}, "/module"); pointer != "" {
			return pointer
		}
	default:
		return "/role"
	}
	if item.Module.Content.Kind != "local" {
		return "/module/content/kind"
	}
	for _, field := range []string{"sum", "goModSum"} {
		if _, present := contentObject[field]; present {
			return "/module/content/" + field
		}
	}
	switch item.Module.Replacement.Kind {
	case "none":
		for _, field := range []string{"path", "version", "repositoryPath"} {
			if _, present := replacementObject[field]; present {
				return "/module/replacement/" + field
			}
		}
	case "repository":
		for _, field := range []string{"path", "version"} {
			if _, present := replacementObject[field]; present {
				return "/module/replacement/" + field
			}
		}
		if !validRepositoryCoordinate(item.Module.Replacement.RepositoryPath) {
			return "/module/replacement/repositoryPath"
		}
	default:
		return "/module/replacement/kind"
	}
	switch item.Role {
	case "scratch-main":
		if item.HasRepositoryPath {
			return "/repositoryPath"
		}
		if item.Module.Replacement.Kind != "none" {
			return "/module/replacement/kind"
		}
	case "repository-module":
		if !item.HasRepositoryPath || !validRepositoryCoordinate(item.RepositoryPath) {
			return "/repositoryPath"
		}
		if item.Module.Replacement.Kind != "repository" {
			return "/module/replacement/kind"
		}
		if item.Module.Replacement.RepositoryPath != item.RepositoryPath {
			return "/module/replacement/repositoryPath"
		}
	}
	return ""
}

func manifestPreimageFromState(state *manifestState) manifestPreimageDocument {
	document := manifestPreimageDocument{
		APIVersion: "nexa.dev/retained-build-input-manifest/v1", BuildTags: append([]string{}, state.buildTags...),
		GoExecutableVersion: state.goExecutableVersion, Inputs: make([]retainedInputDocument, len(state.inputs)),
		Kind: "RetainedBuildInputManifest", LocalModules: make([]retainedLocalModuleDocument, len(state.localModules)),
		ModuleGraphDigest: state.graphDigest.String(), SchemaImportPath: state.schemaImportPath,
	}
	for index, item := range state.localModules {
		document.LocalModules[index] = retainedLocalModuleDocument{Module: moduleIdentityToDocument(item.Module), RepositoryPath: item.RepositoryPath, Role: item.Role}
	}
	for index, item := range state.inputs {
		document.Inputs[index] = retainedInputDocument{
			Digest: item.Digest.String(), Kind: item.Kind,
			Module: requirementDocument{Path: item.Module.Module.Path, Version: item.Module.Module.Version},
			Path:   item.Path, Size: item.Size,
		}
	}
	return document
}

func moduleIdentityToDocument(item ModuleIdentity) moduleIdentityDocument {
	return moduleIdentityDocument{
		Content:     moduleContentDocument{GoModSum: item.Content.GoModSum, Kind: item.Content.Kind, Sum: item.Content.Sum},
		Path:        item.Path,
		Replacement: replacementDocument{Kind: item.Replacement.Kind, Path: item.Replacement.Path, RepositoryPath: item.Replacement.RepositoryPath, Version: item.Replacement.Version},
		Version:     item.Version,
	}
}

func moduleIdentityFromDocument(item moduleIdentityDocument) ModuleIdentity {
	return ModuleIdentity{
		Path: item.Path, Version: item.Version,
		Replacement: ModuleReplacement{Kind: item.Replacement.Kind, Path: item.Replacement.Path, RepositoryPath: item.Replacement.RepositoryPath, Version: item.Replacement.Version},
		Content:     ModuleContent{Kind: item.Content.Kind, Sum: item.Content.Sum, GoModSum: item.Content.GoModSum},
	}
}

func sameManifestOrder(authored, normalized *manifestState) bool {
	return reflect.DeepEqual(authored.buildTags, normalized.buildTags) && reflect.DeepEqual(authored.localModules, normalized.localModules) && reflect.DeepEqual(authored.inputs, normalized.inputs)
}

func manifestSemanticError(snapshot bool, source, reason, pointer string) *Error {
	if snapshot {
		return manifestSnapshotError(reason, source, pointer)
	}
	return buildInputError(reason, pointer)
}

func manifestCanonicalError(snapshot bool, source, pointer string) *Error {
	if snapshot {
		return manifestSnapshotError("canonical_invalid", source, pointer)
	}
	return buildInputError("manifest_canonical_invalid", pointer)
}

func manifestTagPointer(snapshot bool, index int) string {
	if snapshot {
		return "/buildTags/" + strconv.Itoa(index)
	}
	return "/buildInputs/buildTags/" + strconv.Itoa(index)
}

func manifestModulePointer(snapshot bool, index int) string {
	if snapshot {
		return "/localModules/" + strconv.Itoa(index)
	}
	return "/retainedModules/" + strconv.Itoa(index)
}

func manifestInputPointer(snapshot bool, index int) string {
	if snapshot {
		return "/inputs/" + strconv.Itoa(index)
	}
	return "/retainedInputs/" + strconv.Itoa(index)
}

func projectManifestDocumentError(source string, err error, typed bool) *Error {
	var documentError *strictdoc.Error
	if !errors.As(err, &documentError) {
		return manifestSnapshotError("document_invalid", source, "")
	}
	reason := documentError.Code
	if typed && reason == "document_invalid" {
		reason = "document_type_invalid"
	}
	switch reason {
	case "document_invalid", "document_type_invalid", "document_unknown_field", "document_duplicate_key", "document_trailing_input":
	default:
		reason = "document_invalid"
	}
	return manifestSnapshotError(reason, source, documentError.Pointer)
}

func manifestShapeIssue(data []byte) (string, string) {
	var root map[string]any
	if json.Unmarshal(data, &root) != nil || root == nil {
		return "document_type_invalid", ""
	}
	required := []string{"apiVersion", "buildTags", "digest", "goExecutableVersion", "inputs", "kind", "localModules", "moduleGraphDigest", "schemaImportPath"}
	if pointer := firstMissing(root, "", required...); pointer != "" {
		return "document_required_missing", pointer
	}
	for _, name := range []string{"apiVersion", "digest", "goExecutableVersion", "kind", "moduleGraphDigest", "schemaImportPath"} {
		if _, ok := root[name].(string); !ok {
			return "document_type_invalid", "/" + name
		}
	}
	tags, ok := root["buildTags"].([]any)
	if !ok {
		return "document_type_invalid", "/buildTags"
	}
	for index, tag := range tags {
		if _, ok := tag.(string); !ok {
			return "document_type_invalid", "/buildTags/" + strconv.Itoa(index)
		}
	}
	localModules, ok := root["localModules"].([]any)
	if !ok {
		return "document_type_invalid", "/localModules"
	}
	for index, value := range localModules {
		base := "/localModules/" + strconv.Itoa(index)
		object, ok := value.(map[string]any)
		if !ok {
			return "document_type_invalid", base
		}
		if pointer := firstMissing(object, base, "module", "role"); pointer != "" {
			return "document_required_missing", pointer
		}
		if _, ok := object["role"].(string); !ok {
			return "document_type_invalid", base + "/role"
		}
		if repositoryPath, exists := object["repositoryPath"]; exists {
			if _, ok := repositoryPath.(string); !ok {
				return "document_type_invalid", base + "/repositoryPath"
			}
		}
		moduleObject, ok := object["module"].(map[string]any)
		if !ok {
			return "document_type_invalid", base + "/module"
		}
		if reason, pointer := moduleIdentityShapeIssue(moduleObject, base+"/module"); reason != "" {
			return reason, pointer
		}
	}
	inputs, ok := root["inputs"].([]any)
	if !ok {
		return "document_type_invalid", "/inputs"
	}
	for index, value := range inputs {
		base := "/inputs/" + strconv.Itoa(index)
		object, ok := value.(map[string]any)
		if !ok {
			return "document_type_invalid", base
		}
		if pointer := firstMissing(object, base, "digest", "kind", "module", "path", "size"); pointer != "" {
			return "document_required_missing", pointer
		}
		for _, name := range []string{"digest", "kind", "path"} {
			if _, ok := object[name].(string); !ok {
				return "document_type_invalid", base + "/" + name
			}
		}
		if _, ok := object["size"].(float64); !ok {
			return "document_type_invalid", base + "/size"
		}
		moduleRef, ok := object["module"].(map[string]any)
		if !ok {
			return "document_type_invalid", base + "/module"
		}
		if pointer := firstMissing(moduleRef, base+"/module", "path", "version"); pointer != "" {
			return "document_required_missing", pointer
		}
		for _, name := range []string{"path", "version"} {
			if _, ok := moduleRef[name].(string); !ok {
				return "document_type_invalid", base + "/module/" + name
			}
		}
	}
	return "", ""
}

func moduleIdentityShapeIssue(object map[string]any, base string) (string, string) {
	if pointer := firstMissing(object, base, "content", "path", "replacement", "version"); pointer != "" {
		return "document_required_missing", pointer
	}
	for _, name := range []string{"path", "version"} {
		if _, ok := object[name].(string); !ok {
			return "document_type_invalid", base + "/" + name
		}
	}
	for _, name := range []string{"content", "replacement"} {
		nested, ok := object[name].(map[string]any)
		if !ok {
			return "document_type_invalid", base + "/" + name
		}
		if pointer := firstMissing(nested, base+"/"+name, "kind"); pointer != "" {
			return "document_required_missing", pointer
		}
		kind, ok := nested["kind"].(string)
		if !ok {
			return "document_type_invalid", base + "/" + name + "/kind"
		}
		required := []string(nil)
		if name == "content" && kind == "remote" {
			if _, present := nested["sum"]; present {
				required = []string{"goModSum"}
			}
		}
		if name == "replacement" {
			switch kind {
			case "version":
				required = []string{"path", "version"}
			case "repository":
				required = []string{"repositoryPath"}
			}
		}
		if pointer := firstMissing(nested, base+"/"+name, required...); pointer != "" {
			return "document_required_missing", pointer
		}
		for _, field := range required {
			if _, ok := nested[field].(string); !ok {
				return "document_type_invalid", base + "/" + name + "/" + field
			}
		}
	}
	return "", ""
}

func projectGraphDocumentError(source string, err error, typed bool) *Error {
	var documentError *strictdoc.Error
	if !errors.As(err, &documentError) {
		return graphSnapshotError("document_invalid", source, "")
	}
	reason := documentError.Code
	if typed && reason == "document_invalid" {
		reason = "document_type_invalid"
	}
	switch reason {
	case "document_invalid", "document_type_invalid", "document_unknown_field", "document_duplicate_key", "document_trailing_input":
	default:
		reason = "document_invalid"
	}
	return graphSnapshotError(reason, source, documentError.Pointer)
}

func graphShapeIssue(data []byte) (string, string) {
	var root map[string]any
	if json.Unmarshal(data, &root) != nil || root == nil {
		return "document_type_invalid", ""
	}
	if pointer := firstMissing(root, "", "apiVersion", "kind", "consumerModule", "goVersion", "helperDigest", "moduleSources", "modules", "toolModule"); pointer != "" {
		return "document_required_missing", pointer
	}
	for _, name := range []string{"apiVersion", "kind", "goVersion", "helperDigest"} {
		if _, ok := root[name].(string); !ok {
			return "document_type_invalid", "/" + name
		}
	}
	if value, exists := root["toolchainVersion"]; exists {
		if _, ok := value.(string); !ok {
			return "document_type_invalid", "/toolchainVersion"
		}
	}
	for _, name := range []string{"consumerModule", "toolModule"} {
		object, ok := root[name].(map[string]any)
		if !ok {
			return "document_type_invalid", "/" + name
		}
		if pointer := firstMissing(object, "/"+name, "path", "version"); pointer != "" {
			return "document_required_missing", pointer
		}
		for _, field := range []string{"path", "version"} {
			if _, ok := object[field].(string); !ok {
				return "document_type_invalid", "/" + name + "/" + field
			}
		}
	}
	sources, ok := root["moduleSources"].([]any)
	if !ok {
		return "document_type_invalid", "/moduleSources"
	}
	for index, value := range sources {
		base := "/moduleSources/" + strconv.Itoa(index)
		object, ok := value.(map[string]any)
		if !ok {
			return "document_type_invalid", base
		}
		if pointer := firstMissing(object, base, "digest", "ref"); pointer != "" {
			return "document_required_missing", pointer
		}
		for _, field := range []string{"digest", "ref"} {
			if _, ok := object[field].(string); !ok {
				return "document_type_invalid", base + "/" + field
			}
		}
	}
	modules, ok := root["modules"].([]any)
	if !ok {
		return "document_type_invalid", "/modules"
	}
	for index, value := range modules {
		base := "/modules/" + strconv.Itoa(index)
		object, ok := value.(map[string]any)
		if !ok {
			return "document_type_invalid", base
		}
		if pointer := firstMissing(object, base, "content", "path", "replacement", "version"); pointer != "" {
			return "document_required_missing", pointer
		}
		for _, field := range []string{"path", "version"} {
			if _, ok := object[field].(string); !ok {
				return "document_type_invalid", base + "/" + field
			}
		}
		for _, name := range []string{"content", "replacement"} {
			nested, ok := object[name].(map[string]any)
			if !ok {
				return "document_type_invalid", base + "/" + name
			}
			if pointer := firstMissing(nested, base+"/"+name, "kind"); pointer != "" {
				return "document_required_missing", pointer
			}
			kind, ok := nested["kind"].(string)
			if !ok {
				return "document_type_invalid", base + "/" + name + "/kind"
			}
			required := []string(nil)
			if name == "content" && kind == "remote" {
				if _, present := nested["sum"]; present {
					required = []string{"goModSum"}
				}
			}
			if name == "replacement" {
				switch kind {
				case "version":
					required = []string{"path", "version"}
				case "repository":
					required = []string{"repositoryPath"}
				}
			}
			if pointer := firstMissing(nested, base+"/"+name, required...); pointer != "" {
				return "document_required_missing", pointer
			}
			for _, field := range required {
				if _, ok := nested[field].(string); !ok {
					return "document_type_invalid", base + "/" + name + "/" + field
				}
			}
		}
	}
	return "", ""
}

func firstMissing(object map[string]any, base string, names ...string) string {
	for _, name := range names {
		if _, exists := object[name]; !exists {
			return base + "/" + name
		}
	}
	return ""
}

func mustObjectDocument(data []byte) map[string]any {
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	return result
}

func rawObjectArray(value any) []map[string]any {
	values, _ := value.([]any)
	result := make([]map[string]any, len(values))
	for index, item := range values {
		result[index], _ = item.(map[string]any)
	}
	return result
}

func invalidRequirementPointer(value requirementDocument, base string) string {
	if value.Path == "" || module.CheckPath(value.Path) != nil {
		return base + "/path"
	}
	if !semver.IsValid(value.Version) || semver.Canonical(value.Version) != value.Version {
		return base + "/version"
	}
	_, pathMajor, ok := module.SplitPathVersion(value.Path)
	if !ok || module.CheckPathMajor(value.Version, pathMajor) != nil {
		return base + "/version"
	}
	return ""
}

var (
	goDirectivePattern        = regexp.MustCompile(`^(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:\.(?:0|[1-9][0-9]*))?$`)
	toolchainDirectivePattern = regexp.MustCompile(`^go(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:\.(?:0|[1-9][0-9]*))?(?:-[0-9A-Za-z.-]+)?$`)
)

func validGoDirective(value string) bool { return goDirectivePattern.MatchString(value) }

func validToolchainDirective(value string) bool { return toolchainDirectivePattern.MatchString(value) }

func validRepositoryCoordinate(value string) bool {
	if value == "." {
		return true
	}
	if !fs.ValidPath(value) || strings.Contains(value, `\`) {
		return false
	}
	_, err := provenance.ParseDomainSource(value)
	return err == nil
}

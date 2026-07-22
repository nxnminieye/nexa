package entexec

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"github.com/nxnminieye/nexa/generation/internal/frameworkmodule"
	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

func Project(spec ProjectSpec) (scratch *Scratch, resultErr error) {
	repository, err := canonicalExistingDirectory(spec.RepositoryRoot)
	if err != nil {
		return nil, projectError("repository_root_invalid", "/repositoryRoot")
	}
	staging, err := canonicalExistingDirectory(spec.StagingRoot)
	if err != nil {
		return nil, projectError("staging_root_invalid", "/stagingRoot")
	}
	scratchParent, err := canonicalExistingDirectory(spec.ScratchParent)
	if err != nil {
		return nil, projectError("scratch_parent_invalid", "/scratchParent")
	}
	if pathsOverlap(repository, staging) {
		return nil, projectError("root_overlap", "/stagingRoot")
	}
	if pathsOverlap(repository, scratchParent) || pathsOverlap(staging, scratchParent) {
		return nil, projectError("root_overlap", "/scratchParent")
	}
	if spec.Location.state == nil || spec.Location.state.repositoryRoot != repository {
		return nil, projectError("location_state_invalid", "/location")
	}
	tags, err := normalizeBuildTags(spec.BuildTags)
	if err != nil {
		return nil, err
	}
	helperPath, helperBytes, err := validateHelper(spec.Helper)
	if err != nil {
		return nil, err
	}
	frameworkModule, err := spec.Framework.Module()
	if err != nil {
		return nil, projectError("tool_module_invalid", "/toolModule/path")
	}
	kind, replacementPath, replacementVersion, localPath, err := spec.Framework.Replacement()
	if err != nil {
		return nil, projectError("tool_module_invalid", "/toolModule/path")
	}
	if kind == frameworkmodule.ReplacementLocal && (!pathContainedBy(localPath, repository) || localPath == repository) {
		return nil, projectError("framework_local_replacement_outside_repository", "/framework/module/replacement/localPath")
	}
	var frameworkBinding *localModuleBinding
	if kind == frameworkmodule.ReplacementLocal {
		binding, bindingErr := readFrameworkLocalModule(repository, localPath, frameworkModule.Path, spec.projectionHook)
		if bindingErr != nil {
			return nil, bindingErr
		}
		frameworkBinding = &binding
		defer frameworkBinding.close()
	}

	owner, err := createOwnedScratchRoot(scratchParent, spec.projectionHook)
	if err != nil {
		return nil, err
	}
	root := owner.rootPath
	owned := true
	defer func() {
		if owned && resultErr != nil {
			if cleanupErr := owner.cleanup(); cleanupErr != nil {
				scratch = nil
				if typed, ok := cleanupErr.(*Error); ok && typed.Reason() == "cleanup_identity_invalid" {
					resultErr = cleanupErr
				} else {
					resultErr = cleanupError("partial_projection_cleanup_failed")
				}
			}
		}
	}()

	moduleBytes, err := rereadLocated(spec.Location.state, spec.Location.state.moduleFile, MaxModuleFileBytes, "module")
	if err != nil {
		return nil, err
	}
	consumer, err := modfile.Parse("go.mod", moduleBytes, nil)
	if err != nil {
		return nil, projectModuleParseError(moduleBytes, err)
	}
	if consumer.Module == nil || consumer.Module.Mod.Path != spec.Location.state.consumerModule.Path {
		return nil, projectError("module_path_invalid", "/moduleFile/module")
	}
	if consumer.Go == nil || consumer.Go.Version == "" || !validGoDirective(consumer.Go.Version) {
		return nil, projectError("go_directive_invalid", "/moduleFile/go")
	}
	if consumer.Toolchain != nil && !validToolchainDirective(consumer.Toolchain.Name) {
		return nil, projectError("toolchain_directive_invalid", "/moduleFile/toolchain")
	}
	if len(consumer.Godebug) != 0 || len(consumer.Retract) != 0 || len(consumer.Tool) != 0 || len(consumer.Ignore) != 0 {
		return nil, projectError("directive_unsupported", "/moduleFile/directive/0")
	}

	projected := new(modfile.File)
	if projected.AddModuleStmt(ScratchModulePath) != nil || projected.AddGoStmt(consumer.Go.Version) != nil {
		return nil, projectError("scratch_write_failed", "/scratch")
	}
	if consumer.Toolchain != nil && projected.AddToolchainStmt(consumer.Toolchain.Name) != nil {
		return nil, projectError("scratch_write_failed", "/scratch")
	}
	if projected.AddRequire(spec.Location.state.consumerModule.Path, spec.Location.state.consumerModule.Version) != nil || projected.AddRequire(frameworkModule.Path, frameworkModule.Version) != nil {
		return nil, projectError("tool_module_invalid", "/toolModule/version")
	}
	if err := projected.AddReplace(spec.Location.state.consumerModule.Path, "", spec.Location.state.moduleDir, ""); err != nil {
		return nil, projectError("scratch_write_failed", "/scratch")
	}
	if err := addFrameworkReplacement(projected, frameworkModule.Path, frameworkModule.Version, kind, replacementPath, replacementVersion, localPath); err != nil {
		return nil, err
	}
	localBindings, err := addConsumerReplacements(projected, consumer.Replace, spec.Location.state, frameworkModule.Path, frameworkModule.Version, kind, replacementPath, replacementVersion, localPath, spec.projectionHook)
	if err != nil {
		return nil, err
	}
	defer closeLocalModuleBindings(localBindings)
	if err := addConsumerExcludes(projected, consumer.Exclude, spec.Location.state.consumerModule.Path, spec.Location.state.consumerModule.Version); err != nil {
		return nil, err
	}
	projected.SortBlocks()
	projected.Cleanup()
	formatted, err := projected.Format()
	if err != nil {
		return nil, projectError("scratch_write_failed", "/scratch")
	}
	if err := writeScratchFile(owner.rootHandle, "go.mod", formatted, 0o644); err != nil {
		return nil, projectError("scratch_write_failed", "/scratch")
	}
	if spec.Location.state.hasModuleSum {
		sum, err := rereadLocated(spec.Location.state, spec.Location.state.moduleSum, MaxModuleSumBytes, "sum")
		if err != nil {
			return nil, err
		}
		if err := writeScratchFile(owner.rootHandle, "go.sum", sum, 0o644); err != nil {
			return nil, projectError("scratch_write_failed", "/scratch")
		}
	}
	if spec.projectionHook != nil {
		spec.projectionHook(projectionEvent{Name: "before-helper-write", Root: root})
	}
	if err := writeScratchFile(owner.rootHandle, helperPath, helperBytes, 0o644); err != nil {
		return nil, projectError("scratch_write_failed", "/scratch")
	}
	if spec.projectionHook != nil {
		spec.projectionHook(projectionEvent{Name: "before-local-source-recheck", Root: root})
	}
	if frameworkBinding != nil {
		if err := verifyLocalModuleBinding(repository, frameworkBinding); err != nil {
			return nil, projectError("tool_module_invalid", "/toolModule/path")
		}
	}
	for index := range localBindings {
		if err := verifyLocalModuleBinding(repository, &localBindings[index]); err != nil {
			return nil, err
		}
	}
	if err := owner.validatePathIdentity(); err != nil {
		return nil, err
	}
	state := &scratchState{
		root: root, parent: scratchParent, staging: staging, location: spec.Location, buildTags: tags, owner: owner,
		toolModule:   buildinput.ModuleRequirement{Path: frameworkModule.Path, Version: frameworkModule.Version},
		helperDigest: spec.Helper.Digest,
	}
	owned = false
	return &Scratch{state: state}, nil
}

func validGoDirective(value string) bool {
	file := new(modfile.File)
	return file.AddModuleStmt("example.com/nexa-validation") == nil && file.AddGoStmt(value) == nil
}

func validToolchainDirective(value string) bool {
	file := new(modfile.File)
	return file.AddModuleStmt("example.com/nexa-validation") == nil && file.AddGoStmt("1.25.0") == nil && file.AddToolchainStmt(value) == nil
}

func projectModuleParseError(data []byte, parseErr error) error {
	issue, ok := firstStructuredModfileIssue(parseErr)
	if !ok {
		return projectError("module_file_parse_failed", "/moduleFile")
	}
	verb := issue.Verb
	if verb == "" {
		verb = directiveAtLine(data, issue.Pos.Line)
	}
	switch verb {
	case "go":
		return projectError("go_directive_invalid", "/moduleFile/go")
	case "toolchain":
		return projectError("toolchain_directive_invalid", "/moduleFile/toolchain")
	case "replace":
		return projectError("replace_invalid", "/moduleFile/replace/"+strconv.Itoa(directiveIndex(data, "replace", issue.Pos.Line)))
	case "exclude":
		return projectError("exclude_invalid", "/moduleFile/exclude/"+strconv.Itoa(directiveIndex(data, "exclude", issue.Pos.Line)))
	default:
		return projectError("module_file_parse_failed", "/moduleFile")
	}
}

func firstStructuredModfileIssue(err error) (modfile.Error, bool) {
	var issues []modfile.Error
	switch value := err.(type) {
	case modfile.ErrorList:
		issues = append(issues, value...)
	case *modfile.Error:
		issues = append(issues, *value)
	default:
		return modfile.Error{}, false
	}
	if len(issues) == 0 {
		return modfile.Error{}, false
	}
	sort.SliceStable(issues, func(i, j int) bool {
		left, right := issues[i].Pos, issues[j].Pos
		if left.Byte != right.Byte {
			return left.Byte < right.Byte
		}
		return left.Line < right.Line
	})
	issue := issues[0]
	if issue.Verb == "" {
		var nested *modfile.Error
		if errors.As(issue.Err, &nested) {
			issue.Verb = nested.Verb
		}
	}
	return issue, true
}

func directiveAtLine(data []byte, lineNumber int) string {
	lines := bytes.Split(data, []byte{'\n'})
	if lineNumber < 1 || lineNumber > len(lines) {
		return ""
	}
	activeBlock := ""
	for index := 0; index < lineNumber; index++ {
		fields := bytes.Fields(lines[index])
		if len(fields) == 0 || bytes.HasPrefix(fields[0], []byte("//")) {
			continue
		}
		if activeBlock != "" {
			if len(fields) == 1 && string(fields[0]) == ")" {
				activeBlock = ""
				continue
			}
			if index == lineNumber-1 {
				return activeBlock
			}
			continue
		}
		verb := firstDirectiveToken(lines[index])
		if index == lineNumber-1 && verb != "" {
			return verb
		}
		if verb != "" && len(fields) == 2 && string(fields[1]) == "(" {
			activeBlock = verb
		}
	}
	return ""
}

func firstDirectiveToken(line []byte) string {
	fields := bytes.Fields(line)
	if len(fields) == 0 || bytes.HasPrefix(fields[0], []byte("//")) {
		return ""
	}
	switch verb := string(fields[0]); verb {
	case "go", "toolchain", "replace", "exclude":
		return verb
	default:
		return ""
	}
}

func directiveIndex(data []byte, verb string, throughLine int) int {
	lines := bytes.Split(data, []byte{'\n'})
	if throughLine > len(lines) {
		throughLine = len(lines)
	}
	index := 0
	activeBlock := ""
	for line := 0; line < throughLine-1; line++ {
		fields := bytes.Fields(lines[line])
		if len(fields) == 0 || bytes.HasPrefix(fields[0], []byte("//")) {
			continue
		}
		if activeBlock != "" {
			if len(fields) == 1 && string(fields[0]) == ")" {
				activeBlock = ""
				continue
			}
			if activeBlock == verb {
				index++
			}
			continue
		}
		lineVerb := firstDirectiveToken(lines[line])
		if lineVerb != "" && len(fields) == 2 && string(fields[1]) == "(" {
			activeBlock = lineVerb
			continue
		}
		if lineVerb == verb {
			index++
		}
	}
	return index
}

func normalizeBuildTags(values []string) ([]string, error) {
	result := append([]string(nil), values...)
	seen := make(map[string]struct{}, len(result))
	for index, value := range result {
		if !validBuildTag(value) {
			return nil, projectError("build_tag_invalid", "/buildTags/"+strconv.Itoa(index))
		}
		if _, exists := seen[value]; exists {
			return nil, projectError("build_tag_duplicate", "/buildTags/"+strconv.Itoa(index))
		}
		seen[value] = struct{}{}
	}
	sort.Strings(result)
	return result, nil
}

func validBuildTag(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func validateHelper(helper HelperSource) (string, []byte, error) {
	source, err := provenance.ParseDomainSource(helper.Path)
	if err != nil || source.String() != "cmd/enthelper/main.go" {
		return "", nil, projectError("helper_path_invalid", "/helper/path")
	}
	if len(helper.Bytes) > MaxHelperSourceBytes {
		return "", nil, projectError("helper_size_exceeded", "/helper/bytes")
	}
	if helper.Digest != provenance.SHA256(helper.Bytes) {
		return "", nil, projectError("helper_digest_mismatch", "/helper/digest")
	}
	return filepath.FromSlash(source.String()), append([]byte(nil), helper.Bytes...), nil
}

func rereadLocated(location *locationState, identity fileIdentity, limit int64, family string) ([]byte, error) {
	reasonPrefix := map[string]string{"module": "module_file", "sum": "module_sum"}[family]
	root, err := os.OpenRoot(location.repositoryRoot)
	if err != nil {
		return nil, projectError(reasonPrefix+"_read_failed", map[string]string{"module": "/moduleFile", "sum": "/moduleSum"}[family])
	}
	defer root.Close()
	actual, data, present, err := readLocatedFile(root, filepath.FromSlash(identity.repositoryPath), limit, family)
	if err != nil {
		return nil, projectErrorFromLocate(err, family)
	}
	if !present {
		return nil, projectError(reasonPrefix+"_read_failed", map[string]string{"module": "/moduleFile", "sum": "/moduleSum"}[family])
	}
	if actual.digest != identity.digest || actual.size != identity.size {
		return nil, projectError(reasonPrefix+"_digest_drift", map[string]string{"module": "/moduleFile", "sum": "/moduleSum"}[family])
	}
	return data, nil
}

func projectErrorFromLocate(err error, family string) error {
	var typed *Error
	pointer := map[string]string{"module": "/moduleFile", "sum": "/moduleSum"}[family]
	if errors.As(err, &typed) {
		return projectError(typed.reason, pointer)
	}
	return projectError(map[string]string{"module": "module_file", "sum": "module_sum"}[family]+"_read_failed", pointer)
}

func addFrameworkReplacement(file *modfile.File, path, version string, kind frameworkmodule.ReplacementKind, replacementPath, replacementVersion, localPath string) error {
	switch kind {
	case frameworkmodule.ReplacementNone:
		return nil
	case frameworkmodule.ReplacementVersion:
		if err := file.AddReplace(path, version, replacementPath, replacementVersion); err != nil {
			return projectError("tool_module_invalid", "/toolModule/version")
		}
	case frameworkmodule.ReplacementLocal:
		if err := file.AddReplace(path, version, localPath, ""); err != nil {
			return projectError("tool_module_invalid", "/toolModule/version")
		}
	default:
		return projectError("tool_module_invalid", "/toolModule/path")
	}
	return nil
}

type indexedReplacement struct {
	value *modfile.Replace
	index int
}

func addConsumerReplacements(file *modfile.File, input []*modfile.Replace, location *locationState, frameworkPath, frameworkVersion string, frameworkKind frameworkmodule.ReplacementKind, frameworkReplacementPath, frameworkReplacementVersion, frameworkLocalPath string, hook func(projectionEvent)) (_ []localModuleBinding, resultErr error) {
	seen := make(map[string]struct{}, len(input))
	replacements := make([]indexedReplacement, len(input))
	for index, replacement := range input {
		key := replacement.Old.Path + "\x00" + replacement.Old.Version
		if _, duplicate := seen[key]; duplicate {
			return nil, projectError("replace_duplicate", "/moduleFile/replace/"+strconv.Itoa(index))
		}
		seen[key] = struct{}{}
		replacements[index] = indexedReplacement{value: replacement, index: index}
	}
	sort.Slice(replacements, func(i, j int) bool {
		left, right := replacements[i].value, replacements[j].value
		if left.Old.Path != right.Old.Path {
			return left.Old.Path < right.Old.Path
		}
		if left.Old.Version != right.Old.Version {
			return left.Old.Version < right.Old.Version
		}
		if left.New.Path != right.New.Path {
			return left.New.Path < right.New.Path
		}
		return left.New.Version < right.New.Version
	})
	bindings := make([]localModuleBinding, 0)
	defer func() {
		if resultErr != nil {
			closeLocalModuleBindings(bindings)
		}
	}()
	for _, entry := range replacements {
		replacement := entry.value
		pointer := "/moduleFile/replace/" + strconv.Itoa(entry.index)
		if replacement.Old.Version == "" {
			if module.CheckPath(replacement.Old.Path) != nil {
				return nil, projectError("replace_invalid", pointer)
			}
		} else if module.Check(replacement.Old.Path, replacement.Old.Version) != nil {
			return nil, projectError("replace_invalid", pointer)
		}
		if replacement.Old.Path == location.consumerModule.Path {
			return nil, projectError("replace_invalid", pointer)
		}
		selectedFrameworkCoordinate := replacement.Old.Path == frameworkPath && (replacement.Old.Version == "" || replacement.Old.Version == frameworkVersion)
		if selectedFrameworkCoordinate {
			if !frameworkDirectiveMatches(replacement, location, frameworkVersion, frameworkKind, frameworkReplacementPath, frameworkReplacementVersion, frameworkLocalPath) {
				return nil, projectError("replace_invalid", pointer)
			}
			continue
		}
		newPath, newVersion := replacement.New.Path, replacement.New.Version
		if modfile.IsDirectoryPath(newPath) {
			binding, bindingErr := readConsumerLocalModule(location.repositoryRoot, location.moduleDir, newPath, replacement.Old.Path, pointer, hook)
			if bindingErr != nil {
				return nil, bindingErr
			}
			bindings = append(bindings, binding)
			newPath, newVersion = binding.root, ""
		} else if module.Check(newPath, newVersion) != nil {
			return nil, projectError("replace_invalid", pointer)
		}
		if err := file.AddReplace(replacement.Old.Path, replacement.Old.Version, newPath, newVersion); err != nil {
			return nil, projectError("replace_invalid", pointer)
		}
	}
	return bindings, nil
}

func frameworkDirectiveMatches(replacement *modfile.Replace, location *locationState, frameworkVersion string, kind frameworkmodule.ReplacementKind, replacementPath, replacementVersion, localPath string) bool {
	if replacement.Old.Version != "" && replacement.Old.Version != frameworkVersion {
		return false
	}
	switch kind {
	case frameworkmodule.ReplacementVersion:
		return replacement.New.Path == replacementPath && replacement.New.Version == replacementVersion
	case frameworkmodule.ReplacementLocal:
		if !modfile.IsDirectoryPath(replacement.New.Path) || replacement.New.Version != "" {
			return false
		}
		resolved := replacement.New.Path
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(location.moduleDir, filepath.FromSlash(resolved))
		}
		absolute, err := filepath.Abs(resolved)
		return err == nil && filepath.Clean(absolute) == localPath
	default:
		return false
	}
}

func addConsumerExcludes(file *modfile.File, input []*modfile.Exclude, consumerPath, consumerVersion string) error {
	type indexedExclude struct {
		value *modfile.Exclude
		index int
	}
	seen := make(map[string]struct{}, len(input))
	excludes := make([]indexedExclude, len(input))
	for index, exclusion := range input {
		key := exclusion.Mod.Path + "\x00" + exclusion.Mod.Version
		if _, duplicate := seen[key]; duplicate {
			return projectError("exclude_duplicate", "/moduleFile/exclude/"+strconv.Itoa(index))
		}
		seen[key] = struct{}{}
		excludes[index] = indexedExclude{value: exclusion, index: index}
	}
	sort.Slice(excludes, func(i, j int) bool {
		left, right := excludes[i].value.Mod, excludes[j].value.Mod
		return left.Path < right.Path || left.Path == right.Path && left.Version < right.Version
	})
	for _, entry := range excludes {
		exclusion, index := entry.value, entry.index
		if module.Check(exclusion.Mod.Path, exclusion.Mod.Version) != nil || exclusion.Mod.Path == consumerPath && exclusion.Mod.Version == consumerVersion {
			return projectError("exclude_invalid", "/moduleFile/exclude/"+strconv.Itoa(index))
		}
		if err := file.AddExclude(exclusion.Mod.Path, exclusion.Mod.Version); err != nil {
			return projectError("exclude_invalid", "/moduleFile/exclude/"+strconv.Itoa(index))
		}
	}
	return nil
}

func readFrameworkLocalModule(repository, root, expected string, hook func(projectionEvent)) (localModuleBinding, error) {
	binding, err := readLocalModuleBinding(repository, repository, root, expected, "/toolModule/path", hook)
	if err != nil {
		return localModuleBinding{}, projectError("tool_module_invalid", "/toolModule/path")
	}
	return binding, nil
}

func readConsumerLocalModule(repository, moduleDir, replacement, expected, pointer string, hook func(projectionEvent)) (localModuleBinding, error) {
	return readLocalModuleBinding(repository, moduleDir, replacement, expected, pointer, hook)
}

func readLocalModuleBinding(repository, base, candidate, expected, pointer string, hook func(projectionEvent)) (localModuleBinding, error) {
	resolved := candidate
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(base, filepath.FromSlash(resolved))
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return localModuleBinding{}, projectError("replace_invalid", pointer)
	}
	absolute = filepath.Clean(absolute)
	if !pathContainedBy(absolute, repository) {
		return localModuleBinding{}, projectError("replace_escape", pointer)
	}
	relative, err := filepath.Rel(repository, absolute)
	if err != nil || filepath.IsAbs(relative) {
		return localModuleBinding{}, projectError("replace_escape", pointer)
	}
	moduleRoot, repositoryInfo, moduleRootInfo, components, err := openLocalModuleRoot(repository, relative, pointer, hook)
	if err != nil {
		return localModuleBinding{}, err
	}
	retained := true
	defer func() {
		if retained {
			_ = moduleRoot.Close()
		}
	}()
	modulePath := "go.mod"
	if relative != "." {
		modulePath = filepath.Join(relative, modulePath)
	}
	identity, data, readErr := readLocalModuleFile(moduleRoot, "go.mod", modulePath, pointer)
	if readErr != nil {
		return localModuleBinding{}, readErr
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return localModuleBinding{}, projectError("replace_invalid", pointer)
	}
	if parsed.Module == nil || parsed.Module.Mod.Path != expected {
		return localModuleBinding{}, projectError("replace_module_mismatch", pointer)
	}
	retained = false
	return localModuleBinding{
		root: absolute, expectedModule: expected, pointer: pointer, moduleFile: identity,
		repositoryInfo: repositoryInfo, moduleRootInfo: moduleRootInfo, components: components, moduleRoot: moduleRoot,
	}, nil
}

func openLocalModuleRoot(repository, relative, pointer string, hook func(projectionEvent)) (*os.Root, os.FileInfo, os.FileInfo, []directoryIdentity, error) {
	current, err := os.OpenRoot(repository)
	if err != nil {
		return nil, nil, nil, nil, projectError("replace_invalid", pointer)
	}
	owned := true
	defer func() {
		if owned {
			_ = current.Close()
		}
	}()
	repositoryInfo, err := current.Stat(".")
	if err != nil || !repositoryInfo.IsDir() {
		return nil, nil, nil, nil, projectError("replace_invalid", pointer)
	}
	components := make([]directoryIdentity, 0)
	currentPath := ""
	if relative != "." {
		for _, component := range strings.Split(filepath.ToSlash(relative), "/") {
			if component == "" || component == "." || component == ".." || filepath.Base(component) != component {
				return nil, nil, nil, nil, projectError("replace_invalid", pointer)
			}
			beforeInfo, statErr := current.Lstat(component)
			if statErr != nil {
				return nil, nil, nil, nil, projectError("replace_invalid", pointer)
			}
			if beforeInfo.Mode()&os.ModeSymlink != 0 {
				return nil, nil, nil, nil, projectError("replace_symlink", pointer)
			}
			if !beforeInfo.IsDir() {
				return nil, nil, nil, nil, projectError("replace_invalid", pointer)
			}
			if currentPath == "" {
				currentPath = component
			} else {
				currentPath = filepath.Join(currentPath, component)
			}
			if hook != nil {
				hook(projectionEvent{Name: "before-local-component-bind", Root: filepath.Join(repository, currentPath)})
			}
			next, openErr := current.OpenRoot(component)
			afterInfo, afterErr := current.Lstat(component)
			if afterErr == nil && afterInfo.Mode()&os.ModeSymlink != 0 {
				if next != nil {
					_ = next.Close()
				}
				return nil, nil, nil, nil, projectError("replace_symlink", pointer)
			}
			if openErr != nil || afterErr != nil || next == nil {
				if next != nil {
					_ = next.Close()
				}
				return nil, nil, nil, nil, projectError("replace_invalid", pointer)
			}
			handleInfo, handleErr := next.Stat(".")
			if handleErr != nil || !handleInfo.IsDir() || !afterInfo.IsDir() || !os.SameFile(beforeInfo, handleInfo) || !os.SameFile(beforeInfo, afterInfo) {
				_ = next.Close()
				return nil, nil, nil, nil, projectError("replace_invalid", pointer)
			}
			components = append(components, directoryIdentity{repositoryPath: filepath.ToSlash(currentPath), info: handleInfo})
			if err := current.Close(); err != nil {
				_ = next.Close()
				return nil, nil, nil, nil, projectError("replace_invalid", pointer)
			}
			current = next
		}
	}
	moduleRootInfo, err := current.Stat(".")
	if err != nil || !moduleRootInfo.IsDir() {
		return nil, nil, nil, nil, projectError("replace_invalid", pointer)
	}
	owned = false
	return current, repositoryInfo, moduleRootInfo, components, nil
}

func readLocalModuleFile(root *os.Root, path, repositoryPath, pointer string) (fileIdentity, []byte, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return fileIdentity{}, nil, projectError("replace_invalid", pointer)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fileIdentity{}, nil, projectError("replace_symlink", pointer)
	}
	if !info.Mode().IsRegular() || info.Size() > MaxModuleFileBytes {
		return fileIdentity{}, nil, projectError("replace_invalid", pointer)
	}
	file, err := root.Open(path)
	if err != nil {
		return fileIdentity{}, nil, projectError("replace_invalid", pointer)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return fileIdentity{}, nil, projectError("replace_invalid", pointer)
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxModuleFileBytes+1))
	if err != nil || len(data) > MaxModuleFileBytes {
		return fileIdentity{}, nil, projectError("replace_invalid", pointer)
	}
	ref, err := provenance.RepositoryRef(filepath.ToSlash(repositoryPath), "")
	if err != nil {
		return fileIdentity{}, nil, projectError("replace_invalid", pointer)
	}
	return fileIdentity{repositoryPath: ref.Path(), digest: provenance.SHA256(data), size: int64(len(data)), info: opened}, data, nil
}

func verifyLocalModuleBinding(repository string, binding *localModuleBinding) error {
	if binding == nil {
		return projectError("replace_invalid", "/moduleFile")
	}
	if binding.moduleRoot == nil {
		return projectError("replace_invalid", binding.pointer)
	}
	relative, err := filepath.Rel(repository, binding.root)
	if err != nil || filepath.IsAbs(relative) {
		return projectError("replace_invalid", binding.pointer)
	}
	currentRoot, repositoryInfo, moduleRootInfo, components, err := openLocalModuleRoot(repository, relative, binding.pointer, nil)
	if err != nil {
		return err
	}
	defer currentRoot.Close()
	if !os.SameFile(repositoryInfo, binding.repositoryInfo) || !os.SameFile(moduleRootInfo, binding.moduleRootInfo) || len(components) != len(binding.components) {
		return projectError("replace_invalid", binding.pointer)
	}
	for index := range components {
		if components[index].repositoryPath != binding.components[index].repositoryPath || !os.SameFile(components[index].info, binding.components[index].info) {
			return projectError("replace_invalid", binding.pointer)
		}
	}
	retainedInfo, err := binding.moduleRoot.Stat(".")
	if err != nil || !os.SameFile(retainedInfo, binding.moduleRootInfo) {
		return projectError("replace_invalid", binding.pointer)
	}
	actual, data, err := readLocalModuleFile(binding.moduleRoot, "go.mod", binding.moduleFile.repositoryPath, binding.pointer)
	if err != nil || actual.size != binding.moduleFile.size || actual.digest != binding.moduleFile.digest || !os.SameFile(actual.info, binding.moduleFile.info) {
		return projectError("replace_invalid", binding.pointer)
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil || parsed.Module == nil || parsed.Module.Mod.Path != binding.expectedModule {
		return projectError("replace_invalid", binding.pointer)
	}
	return nil
}

func (binding *localModuleBinding) close() {
	if binding == nil || binding.moduleRoot == nil {
		return
	}
	_ = binding.moduleRoot.Close()
	binding.moduleRoot = nil
}

func closeLocalModuleBindings(bindings []localModuleBinding) {
	for index := range bindings {
		bindings[index].close()
	}
}

func writeScratchFile(root *os.Root, relative string, data []byte, mode os.FileMode) error {
	source, err := provenance.ParseDomainSource(filepath.ToSlash(relative))
	if err != nil {
		return os.ErrInvalid
	}
	relative = filepath.FromSlash(source.String())
	directory := filepath.Dir(relative)
	if directory != "." {
		current := ""
		for _, component := range strings.Split(filepath.ToSlash(directory), "/") {
			if current == "" {
				current = component
			} else {
				current = filepath.Join(current, component)
			}
			info, statErr := root.Lstat(current)
			if errors.Is(statErr, os.ErrNotExist) {
				if mkdirErr := root.Mkdir(current, 0o755); mkdirErr != nil {
					return mkdirErr
				}
				info, statErr = root.Lstat(current)
			}
			if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return os.ErrInvalid
			}
		}
	}
	if _, err := root.Lstat(relative); !errors.Is(err, os.ErrNotExist) {
		return os.ErrExist
	}
	file, err := root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	info, err := root.Lstat(relative)
	if err != nil || !info.Mode().IsRegular() {
		return os.ErrInvalid
	}
	readbackFile, err := root.Open(relative)
	if err != nil {
		return err
	}
	readback, readErr := io.ReadAll(io.LimitReader(readbackFile, int64(len(data))+1))
	stat, statErr := readbackFile.Stat()
	closeErr := readbackFile.Close()
	if readErr != nil || statErr != nil || closeErr != nil || !os.SameFile(info, stat) || !bytes.Equal(readback, data) {
		return os.ErrInvalid
	}
	return nil
}

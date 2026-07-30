package engine

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/release"
	"golang.org/x/mod/modfile"
)

type moduleFileSnapshot struct {
	path    string
	pointer string
	exists  bool
	mode    os.FileMode
	content []byte
}

func prepareValidationModuleContext(
	repositoryRoot string,
	selection Selection,
	closure sourceplugin.ProfileClosure,
	prepared *preparedPublish,
) error {
	if prepared == nil || prepared.previewRoot == "" {
		return validationError(ErrInput, "source_validation_invalid", "preview_root_invalid", "/previewRoot")
	}
	targetModule := filepath.Join(prepared.previewRoot, "go.mod")
	targetInfo, targetErr := os.Lstat(targetModule)
	switch {
	case targetErr == nil && (!targetInfo.Mode().IsRegular() || targetInfo.Mode()&os.ModeSymlink != 0):
		return validationError(ErrInput, "source_validation_invalid", "target_module_invalid", "/target/go.mod")
	case targetErr != nil && !errors.Is(targetErr, os.ErrNotExist):
		return validationError(ErrInput, "source_validation_invalid", "target_module_invalid", "/target/go.mod")
	}
	if err := rejectNestedTargetModules(prepared.previewRoot); err != nil {
		return err
	}
	if targetErr == nil {
		data, err := os.ReadFile(targetModule)
		if err != nil {
			return validationError(ErrInput, "source_validation_invalid", "target_module_invalid", "/target/go.mod")
		}
		active, err := modfile.Parse(targetModule, data, nil)
		if err != nil {
			return validationError(ErrInput, "source_validation_invalid", "target_module_malformed", "/target/go.mod")
		}
		return validateGoModuleRequirements(active, closure.GoModuleRequirements())
	}

	rootSnapshots := make([]moduleFileSnapshot, 0, 3)
	for _, name := range []string{"go.mod", "go.sum", "go.work"} {
		snapshot, err := snapshotModuleFile(repositoryRoot, name)
		if err != nil {
			return err
		}
		rootSnapshots = append(rootSnapshots, snapshot)
	}
	if !rootSnapshots[0].exists {
		return validationError(ErrInput, "source_validation_invalid", "repository_module_missing", "/repository/go.mod")
	}
	if !rootSnapshots[0].mode.IsRegular() || rootSnapshots[0].mode&os.ModeSymlink != 0 {
		return validationError(ErrInput, "source_validation_invalid", "repository_module_invalid", "/repository/go.mod")
	}
	for index, name := range []string{"go.sum", "go.work"} {
		snapshot := rootSnapshots[index+1]
		if snapshot.exists && (!snapshot.mode.IsRegular() || snapshot.mode&os.ModeSymlink != 0) {
			return validationError(ErrInput, "source_validation_invalid", "repository_"+strings.TrimPrefix(name, "go.")+"_invalid", "/repository/"+name)
		}
	}

	stagedModule, err := modfile.Parse("go.mod", rootSnapshots[0].content, nil)
	if err != nil {
		return validationError(ErrInput, "source_validation_invalid", "repository_module_malformed", "/repository/go.mod")
	}
	selectedModule := selection.release.ModulePath()
	for _, replacement := range stagedModule.Replace {
		if replacement.Old.Path == selectedModule {
			return validationError(ErrInput, "source_validation_invalid", "provider_module_replace_conflict", "/repository/go.mod")
		}
	}
	for _, replacement := range append([]*modfile.Replace(nil), stagedModule.Replace...) {
		if err := stagedModule.DropReplace(replacement.Old.Path, replacement.Old.Version); err != nil {
			return validationError(ErrInternal, "source_validation_internal", "module_projection_failed", "")
		}
	}
	if rootSnapshots[2].exists {
		if err := projectSelectedWorkspaceReplace(repositoryRoot, rootSnapshots[2].content, selection.release, stagedModule); err != nil {
			return err
		}
	}
	if err := validateGoModuleRequirements(stagedModule, closure.GoModuleRequirements()); err != nil {
		return err
	}
	formatted, err := stagedModule.Format()
	if err != nil {
		return validationError(ErrInternal, "source_validation_internal", "module_projection_failed", "")
	}
	previewRepository := filepath.Join(prepared.root, "preview", "repository")
	if err := writePublishBytes(filepath.Join(previewRepository, "go.mod"), formatted, 0o600); err != nil {
		return err
	}
	if rootSnapshots[1].exists {
		if err := writePublishBytes(filepath.Join(previewRepository, "go.sum"), rootSnapshots[1].content, 0o600); err != nil {
			return err
		}
	}
	prepared.moduleSnapshots = rootSnapshots
	return nil
}

func rejectNestedTargetModules(targetRoot string) error {
	return filepath.WalkDir(targetRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return validationError(ErrInput, "source_validation_invalid", "target_module_invalid", "/target")
		}
		if path != filepath.Join(targetRoot, "go.mod") && entry.Name() == "go.mod" {
			return validationError(ErrInput, "source_validation_invalid", "nested_module_unsupported", "/target")
		}
		return nil
	})
}

func snapshotModuleFile(root, name string) (moduleFileSnapshot, error) {
	path := filepath.Join(root, name)
	snapshot := moduleFileSnapshot{path: path, pointer: "/repository/" + name}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, nil
	}
	if err != nil {
		return moduleFileSnapshot{}, validationError(ErrInput, "source_validation_invalid", "repository_metadata_read_failed", snapshot.pointer)
	}
	snapshot.exists = true
	snapshot.mode = info.Mode()
	if info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		snapshot.content, err = os.ReadFile(path)
		if err != nil {
			return moduleFileSnapshot{}, validationError(ErrInput, "source_validation_invalid", "repository_metadata_read_failed", snapshot.pointer)
		}
	}
	return snapshot, nil
}

func verifyModuleFileSnapshots(snapshots []moduleFileSnapshot) error {
	for _, expected := range snapshots {
		actual, err := os.Lstat(expected.path)
		if !expected.exists {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return snapshotChanged(expected.pointer, "transaction")
		}
		if err != nil || actual.Mode() != expected.mode {
			return snapshotChanged(expected.pointer, "transaction")
		}
		if expected.mode.IsRegular() && expected.mode&os.ModeSymlink == 0 {
			content, readErr := os.ReadFile(expected.path)
			if readErr != nil || !bytes.Equal(content, expected.content) {
				return snapshotChanged(expected.pointer, "transaction")
			}
		}
	}
	return nil
}

func validateGoModuleRequirements(moduleFile *modfile.File, requirements []sourceplugin.GoModuleRequirement) error {
	required := make(map[string]string, len(moduleFile.Require))
	for _, directive := range moduleFile.Require {
		required[directive.Mod.Path] = directive.Mod.Version
	}
	for index, requirement := range requirements {
		version, ok := required[requirement.ModulePath()]
		pointer := "/requiresGoModules/" + strconv.Itoa(index)
		if !ok {
			return validationError(ErrInput, "source_validation_invalid", "module_requirement_missing", pointer)
		}
		if version != requirement.Version() {
			return validationError(ErrInput, "source_validation_invalid", "module_requirement_mismatch", pointer)
		}
	}
	return nil
}

func projectSelectedWorkspaceReplace(root string, data []byte, selected release.Ref, stagedModule *modfile.File) error {
	workspace, err := modfile.ParseWork("go.work", data, nil)
	if err != nil {
		return validationError(ErrInput, "source_validation_invalid", "repository_workspace_malformed", "/repository/go.work")
	}
	matches := make([]*modfile.Replace, 0, 1)
	for _, replacement := range workspace.Replace {
		if replacement.Old.Path == selected.ModulePath() {
			matches = append(matches, replacement)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	if len(matches) != 1 {
		return validationError(ErrInput, "source_validation_invalid", "workspace_provider_replace_conflict", "/repository/go.work")
	}
	replacement := matches[0]
	requiredVersion := ""
	for _, directive := range stagedModule.Require {
		if directive.Mod.Path == selected.ModulePath() {
			requiredVersion = directive.Mod.Version
			break
		}
	}
	if replacement.Old.Version == "" || requiredVersion == "" || replacement.Old.Version != requiredVersion {
		return validationError(ErrInput, "source_validation_invalid", "workspace_provider_replace_mismatch", "/repository/go.work")
	}
	if replacement.New.Version != "" || replacement.New.Path == "" {
		return validationError(ErrInput, "source_validation_invalid", "workspace_provider_replace_invalid", "/repository/go.work")
	}
	localRoot := replacement.New.Path
	if !filepath.IsAbs(localRoot) {
		localRoot = filepath.Join(root, filepath.FromSlash(localRoot))
	}
	localRoot = filepath.Clean(localRoot)
	evaluated, err := filepath.EvalSymlinks(localRoot)
	if err != nil || evaluated != localRoot {
		return validationError(ErrInput, "source_validation_invalid", "workspace_provider_local_invalid", "/repository/go.work")
	}
	info, err := os.Lstat(localRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return validationError(ErrInput, "source_validation_invalid", "workspace_provider_local_invalid", "/repository/go.work")
	}
	localModulePath := filepath.Join(localRoot, "go.mod")
	moduleInfo, err := os.Lstat(localModulePath)
	if err != nil || !moduleInfo.Mode().IsRegular() || moduleInfo.Mode()&os.ModeSymlink != 0 {
		return validationError(ErrInput, "source_validation_invalid", "workspace_provider_local_invalid", "/repository/go.work")
	}
	moduleData, err := os.ReadFile(localModulePath)
	if err != nil {
		return validationError(ErrInput, "source_validation_invalid", "workspace_provider_local_invalid", "/repository/go.work")
	}
	localModule, err := modfile.Parse(localModulePath, moduleData, nil)
	if err != nil || localModule.Module == nil || localModule.Module.Mod.Path != selected.ModulePath() {
		return validationError(ErrInput, "source_validation_invalid", "workspace_provider_local_mismatch", "/repository/go.work")
	}
	if err := stagedModule.AddReplace(selected.ModulePath(), requiredVersion, localRoot, ""); err != nil {
		return validationError(ErrInternal, "source_validation_internal", "module_projection_failed", "")
	}
	return nil
}

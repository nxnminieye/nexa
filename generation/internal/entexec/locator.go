package entexec

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

func Locate(spec LocateSpec) (Location, error) {
	repository, err := canonicalExistingDirectory(spec.RepositoryRoot)
	if err != nil {
		return Location{}, locateError("repository_root_invalid", "/repositoryRoot")
	}
	if spec.SchemaDir.String() == "" {
		return Location{}, locateError("schema_dir_invalid", "/schemaDir")
	}
	schemaRelative := filepath.FromSlash(spec.SchemaDir.String())
	schemaAbsolute := filepath.Join(repository, schemaRelative)
	if !pathContainedBy(schemaAbsolute, repository) {
		return Location{}, locateError("schema_dir_escape", "/schemaDir")
	}
	root, err := os.OpenRoot(repository)
	if err != nil {
		return Location{}, locateError("repository_root_invalid", "/repositoryRoot")
	}
	defer root.Close()
	if err := validateDirectoryChain(root, schemaRelative); err != nil {
		return Location{}, err
	}

	current := schemaRelative
	for {
		candidate := filepath.Join(current, "go.mod")
		identity, data, present, readErr := readLocatedFile(root, candidate, MaxModuleFileBytes, "module")
		if readErr != nil {
			return Location{}, readErr
		}
		if present {
			modulePath := modfile.ModulePath(data)
			if modulePath == "" {
				return Location{}, locateError("module_file_parse_failed", "/moduleFile")
			}
			if module.CheckPath(modulePath) != nil {
				return Location{}, locateError("module_path_invalid", "/moduleFile/module")
			}
			version, versionErr := syntheticModuleVersion(modulePath)
			if versionErr != nil {
				return Location{}, locateError("module_major_invalid", "/moduleFile/module")
			}
			moduleDir := filepath.Join(repository, current)
			schemaWithinModule, relErr := filepath.Rel(moduleDir, schemaAbsolute)
			if relErr != nil || strings.HasPrefix(schemaWithinModule, "..") {
				return Location{}, locateError("schema_dir_escape", "/schemaDir")
			}
			importPath := modulePath
			if schemaWithinModule != "." {
				importPath += "/" + filepath.ToSlash(schemaWithinModule)
			}
			state := &locationState{
				repositoryRoot: repository, moduleDir: moduleDir, schemaDir: spec.SchemaDir, schemaImportPath: importPath,
				consumerModule: buildinput.ModuleRequirement{Path: modulePath, Version: version}, moduleFile: identity,
			}
			sumIdentity, _, sumPresent, sumErr := readLocatedFile(root, filepath.Join(current, "go.sum"), MaxModuleSumBytes, "sum")
			if sumErr != nil {
				return Location{}, sumErr
			}
			state.moduleSum, state.hasModuleSum = sumIdentity, sumPresent
			return Location{state: state}, nil
		}
		if current == "." || current == "" {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return Location{}, locateError("module_not_found", "/schemaDir")
}

func validateDirectoryChain(root *os.Root, relative string) error {
	current := ""
	for _, component := range strings.Split(filepath.ToSlash(relative), "/") {
		if current == "" {
			current = component
		} else {
			current = filepath.Join(current, component)
		}
		info, err := root.Lstat(current)
		if err != nil {
			return locateError("schema_dir_not_directory", "/schemaDir")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return locateError("schema_dir_symlink", "/schemaDir")
		}
		if !info.IsDir() {
			return locateError("schema_dir_not_directory", "/schemaDir")
		}
	}
	return nil
}

func readLocatedFile(root *os.Root, path string, limit int64, family string) (fileIdentity, []byte, bool, error) {
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileIdentity{}, nil, false, nil
	}
	pointer := map[string]string{"module": "/moduleFile", "sum": "/moduleSum"}[family]
	reasonPrefix := map[string]string{"module": "module_file", "sum": "module_sum"}[family]
	if err != nil {
		return fileIdentity{}, nil, false, locateError(reasonPrefix+"_read_failed", pointer)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fileIdentity{}, nil, false, locateError(reasonPrefix+"_symlink", pointer)
	}
	if !info.Mode().IsRegular() {
		return fileIdentity{}, nil, false, locateError(reasonPrefix+"_not_regular", pointer)
	}
	if info.Size() > limit {
		return fileIdentity{}, nil, false, locateError(reasonPrefix+"_size_exceeded", pointer)
	}
	file, err := root.Open(path)
	if err != nil {
		return fileIdentity{}, nil, false, locateError(reasonPrefix+"_read_failed", pointer)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return fileIdentity{}, nil, false, locateError(reasonPrefix+"_read_failed", pointer)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return fileIdentity{}, nil, false, locateError(reasonPrefix+"_read_failed", pointer)
	}
	if int64(len(data)) > limit {
		return fileIdentity{}, nil, false, locateError(reasonPrefix+"_size_exceeded", pointer)
	}
	ref, err := provenance.RepositoryRef(filepath.ToSlash(path), "")
	if err != nil {
		return fileIdentity{}, nil, false, locateError(reasonPrefix+"_read_failed", pointer)
	}
	return fileIdentity{repositoryPath: ref.Path(), digest: provenance.SHA256(data), size: int64(len(data))}, data, true, nil
}

func syntheticModuleVersion(path string) (string, error) {
	_, major, ok := module.SplitPathVersion(path)
	if !ok {
		return "", fmt.Errorf("invalid module path")
	}
	if major == "" {
		return "v0.0.0", nil
	}
	major = strings.TrimPrefix(strings.TrimPrefix(major, "/v"), ".v")
	number, err := strconv.Atoi(major)
	if err != nil || number < 1 {
		return "", fmt.Errorf("invalid module major")
	}
	return "v" + strconv.Itoa(number) + ".0.0", nil
}

package integration_test

import (
	"archive/zip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	modmodule "golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

const nexaModulePath = "github.com/nxnminieye/nexa"

func prepareHermeticExternalModule(t *testing.T, temporary, moduleRoot string) []string {
	t.Helper()

	moduleFilePath := filepath.Join(moduleRoot, "go.mod")
	moduleFile := readModuleFile(t, moduleFilePath)
	rootModuleFile := readModuleFile(t, filepath.Join(repositoryRoot(t), "go.mod"))

	addLocalModuleReplace(t, moduleFile, moduleRoot, nexaModulePath, repositoryRoot(t))
	existingRequirements := make(map[string]struct{}, len(moduleFile.Require))
	for _, requirement := range moduleFile.Require {
		existingRequirements[requirement.Mod.Path] = struct{}{}
	}
	for _, requirement := range rootModuleFile.Require {
		if _, exists := existingRequirements[requirement.Mod.Path]; exists {
			continue
		}
		moduleFile.AddNewRequire(requirement.Mod.Path, requirement.Mod.Version, true)
		existingRequirements[requirement.Mod.Path] = struct{}{}
	}

	moduleCache := rootModuleCache(t)
	dependencyRoot := filepath.Join(temporary, "module-sources")
	for _, requirement := range rootModuleFile.Require {
		escapedPath, err := modmodule.EscapePath(requirement.Mod.Path)
		if err != nil {
			t.Fatalf("escape module path %q: %v", requirement.Mod.Path, err)
		}
		escapedVersion, err := modmodule.EscapeVersion(requirement.Mod.Version)
		if err != nil {
			t.Fatalf("escape module version %q: %v", requirement.Mod.Version, err)
		}
		destination := filepath.Join(dependencyRoot, filepath.FromSlash(escapedPath+"@"+escapedVersion))
		source := filepath.Join(moduleCache, filepath.FromSlash(escapedPath+"@"+escapedVersion))
		archive := filepath.Join(moduleCache, "cache", "download", filepath.FromSlash(escapedPath), "@v", escapedVersion+".zip")
		module := modmodule.Version{Path: requirement.Mod.Path, Version: requirement.Mod.Version}
		if err := materializeHermeticModuleSource(source, archive, destination, module); err != nil {
			t.Fatalf("copy module %s@%s: %v", module.Path, module.Version, err)
		}
		addLocalModuleReplace(t, moduleFile, moduleRoot, requirement.Mod.Path, destination)
	}

	formatted, err := moduleFile.Format()
	if err != nil {
		t.Fatalf("format external go.mod: %v", err)
	}
	if err := os.WriteFile(moduleFilePath, formatted, 0o644); err != nil {
		t.Fatalf("write external go.mod: %v", err)
	}
	environment := overriddenEnvironment(
		isolatedExternalGoEnvironment(t, temporary),
		"GOPROXY=file://"+filepath.ToSlash(filepath.Join(moduleCache, "cache", "download")),
		"GOSUMDB=off",
	)
	download := exec.Command("go", "mod", "download")
	download.Dir = moduleRoot
	download.Env = environment
	if output, err := download.CombinedOutput(); err != nil {
		t.Fatalf("download external module dependencies: %v\n%s", err, output)
	}
	return environment
}

func materializeHermeticModuleSource(source, archive, destination string, module modmodule.Version) error {
	if err := modmodule.Check(module.Path, module.Version); err != nil {
		return fmt.Errorf("invalid module identity %s@%s: %w", module.Path, module.Version, err)
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("module destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect module destination %s: %w", destination, err)
	}

	sourceInfo, sourceErr := os.Lstat(source)
	switch {
	case sourceErr == nil:
		if !sourceInfo.IsDir() {
			return fmt.Errorf("module source is not a directory: %s", source)
		}
		return materializeHermeticModuleDirectory(source, destination, module)
	case !os.IsNotExist(sourceErr):
		return fmt.Errorf("inspect module source %s: %w", source, sourceErr)
	default:
		return materializeHermeticModuleArchive(archive, destination, module)
	}
}

func materializeHermeticModuleDirectory(source, destination string, module modmodule.Version) error {
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create module destination parent: %w", err)
	}
	temporaryArchive, err := os.CreateTemp(parent, ".nexa-module-*.zip")
	if err != nil {
		return fmt.Errorf("create temporary module archive: %w", err)
	}
	temporaryArchivePath := temporaryArchive.Name()
	defer os.Remove(temporaryArchivePath)

	createErr := modzip.CreateFromDir(temporaryArchive, module, source)
	closeErr := temporaryArchive.Close()
	if createErr != nil {
		return fmt.Errorf("create checked module archive: %w", createErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close checked module archive: %w", closeErr)
	}
	return materializeHermeticModuleArchive(temporaryArchivePath, destination, module)
}

func materializeHermeticModuleArchive(archive, destination string, module modmodule.Version) error {
	archiveInfo, err := os.Lstat(archive)
	if err != nil {
		return fmt.Errorf("inspect module archive %s: %w", archive, err)
	}
	if !archiveInfo.Mode().IsRegular() {
		return fmt.Errorf("module archive is not a regular file: %s", archive)
	}
	if _, err := modzip.CheckZip(module, archive); err != nil {
		return fmt.Errorf("validate module archive: %w", err)
	}
	if err := modzip.Unzip(destination, module, archive); err != nil {
		_ = os.RemoveAll(destination)
		return fmt.Errorf("extract module archive: %w", err)
	}
	moduleFileInfo, err := os.Lstat(filepath.Join(destination, "go.mod"))
	if err != nil || !moduleFileInfo.Mode().IsRegular() {
		_ = os.RemoveAll(destination)
		return fmt.Errorf("materialized module %s@%s has no regular go.mod", module.Path, module.Version)
	}
	return nil
}

func readModuleFile(t *testing.T, path string) *modfile.File {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	parsed, err := modfile.Parse(path, content, nil)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return parsed
}

func addLocalModuleReplace(t *testing.T, moduleFile *modfile.File, moduleRoot, modulePath, sourceRoot string) {
	t.Helper()
	resolvedModuleRoot, err := filepath.EvalSymlinks(moduleRoot)
	if err != nil {
		t.Fatalf("resolve external module root: %v", err)
	}
	resolvedSourceRoot, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		t.Fatalf("resolve local replacement for %s: %v", modulePath, err)
	}
	relative, err := filepath.Rel(resolvedModuleRoot, resolvedSourceRoot)
	if err != nil {
		t.Fatalf("make local replacement for %s relative: %v", modulePath, err)
	}
	if filepath.IsAbs(relative) {
		t.Fatalf("local replacement for %s is not relative: %q", modulePath, relative)
	}
	if err := moduleFile.AddReplace(modulePath, "", filepath.ToSlash(relative), ""); err != nil {
		t.Fatalf("add local replacement for %s: %v", modulePath, err)
	}
}

func rootModuleCache(t *testing.T) string {
	t.Helper()
	command := exec.Command("go", "env", "GOMODCACHE")
	command.Dir = repositoryRoot(t)
	command.Env = overriddenEnvironment(
		os.Environ(),
		"GOWORK=off",
		"GOENV=off",
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
		"GOSUMDB=off",
	)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("locate root module cache: %v", err)
	}
	path := strings.TrimSpace(string(output))
	if path == "" || !filepath.IsAbs(path) {
		t.Fatalf("root module cache is not absolute: %q", path)
	}
	return path
}

func overriddenEnvironment(environment []string, overrides ...string) []string {
	overridden := make(map[string]struct{}, len(overrides))
	for _, entry := range overrides {
		name, _, _ := strings.Cut(entry, "=")
		overridden[name] = struct{}{}
	}
	result := make([]string, 0, len(environment)+len(overrides))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, exists := overridden[name]; !exists {
			result = append(result, entry)
		}
	}
	return append(result, overrides...)
}

func isolatedExternalGoEnvironment(t *testing.T, temporary string) []string {
	t.Helper()
	paths := map[string]string{
		"GOMODCACHE":      filepath.Join(temporary, "gomodcache"),
		"GOCACHE":         filepath.Join(temporary, "gocache"),
		"HOME":            filepath.Join(temporary, "home"),
		"XDG_CONFIG_HOME": filepath.Join(temporary, "xdg-config"),
		"XDG_CACHE_HOME":  filepath.Join(temporary, "xdg-cache"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("create isolated Go environment: %v", err)
		}
	}
	environment := []string{
		"GOWORK=off",
		"GOENV=off",
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOMODCACHE=" + paths["GOMODCACHE"],
		"GOCACHE=" + paths["GOCACHE"],
		"HOME=" + paths["HOME"],
		"XDG_CONFIG_HOME=" + paths["XDG_CONFIG_HOME"],
		"XDG_CACHE_HOME=" + paths["XDG_CACHE_HOME"],
	}
	for _, name := range []string{"PATH", "TMPDIR", "TEMP", "TMP", "SYSTEMROOT", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(name); ok && value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}

func TestHermeticModuleArchiveBuildsCanonicalUppercaseModuleOffline(t *testing.T) {
	temporary := t.TempDir()
	module := modmodule.Version{Path: "example.com/Upper", Version: "v1.0.0-RC1"}
	if err := modmodule.Check(module.Path, module.Version); err != nil {
		t.Fatalf("fixture module is invalid: %v", err)
	}

	source := filepath.Join(temporary, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConsumerFile(t, filepath.Join(source, "go.mod"), "module "+module.Path+"\n\ngo 1.25.0\n")
	writeConsumerFile(t, filepath.Join(source, "upper.go"), "package upper\n\nfunc Value() string { return \"uppercase-module\" }\n")
	archive := filepath.Join(temporary, "module.zip")
	createCanonicalModuleArchive(t, archive, source, module)

	destination := filepath.Join(temporary, "module-source")
	if err := materializeHermeticModuleSource(
		filepath.Join(temporary, "missing-extracted-source"),
		archive,
		destination,
		module,
	); err != nil {
		t.Fatalf("materialize canonical module archive: %v", err)
	}

	consumer := filepath.Join(temporary, "consumer")
	if err := os.MkdirAll(consumer, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConsumerFile(t, filepath.Join(consumer, "go.mod"), "module example.com/offline-consumer\n\ngo 1.25.0\n")
	moduleFile := readModuleFile(t, filepath.Join(consumer, "go.mod"))
	moduleFile.AddNewRequire(module.Path, module.Version, false)
	addLocalModuleReplace(t, moduleFile, consumer, module.Path, destination)
	formatted, err := moduleFile.Format()
	if err != nil {
		t.Fatalf("format offline consumer module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), formatted, 0o644); err != nil {
		t.Fatalf("write offline consumer module: %v", err)
	}
	writeConsumerFile(t, filepath.Join(consumer, "upper_test.go"), `package consumer_test

import (
	"testing"

	upper "example.com/Upper"
)

func TestUppercaseModule(t *testing.T) {
	if got := upper.Value(); got != "uppercase-module" {
		t.Fatalf("Value() = %q", got)
	}
}
`)

	command := exec.Command("go", "test", "-mod=readonly", "./...")
	command.Dir = consumer
	command.Env = isolatedExternalGoEnvironment(t, filepath.Join(temporary, "go-environment"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("execute canonical uppercase module with GOPROXY=off: %v\n%s", err, output)
	}
}

func TestHermeticModuleArchiveRejectsNonPortablePaths(t *testing.T) {
	module := modmodule.Version{Path: "example.com/portable", Version: "v1.0.0"}
	tests := []struct {
		name    string
		entries []moduleArchiveEntry
	}{
		{
			name: "case-fold collision",
			entries: []moduleArchiveEntry{
				{name: "go.mod", content: "module " + module.Path + "\n\ngo 1.25.0\n"},
				{name: "A.go", content: "package portable\n"},
				{name: "a.go", content: "package portable\n"},
			},
		},
		{
			name: "file and directory collision",
			entries: []moduleArchiveEntry{
				{name: "go.mod", content: "module " + module.Path + "\n\ngo 1.25.0\n"},
				{name: "path", content: "regular file"},
				{name: "path/file.go", content: "package portable\n"},
			},
		},
		{
			name: "unclean path",
			entries: []moduleArchiveEntry{
				{name: "go.mod", content: "module " + module.Path + "\n\ngo 1.25.0\n"},
				{name: "dir/../file.go", content: "package portable\n"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			temporary := t.TempDir()
			archive := filepath.Join(temporary, "module.zip")
			createRawModuleArchive(t, archive, module, test.entries)
			if _, err := modzip.CheckZip(module, archive); err == nil {
				t.Fatal("fixture unexpectedly satisfies the portable module zip contract")
			}

			destination := filepath.Join(temporary, "destination")
			if err := materializeHermeticModuleSource(
				filepath.Join(temporary, "missing-extracted-source"),
				archive,
				destination,
				module,
			); err == nil {
				t.Fatal("non-portable module archive was accepted")
			}
			assertDirectoryAbsentOrEmpty(t, destination)
		})
	}
}

func TestHermeticModuleDirectoryOmitsSymlinks(t *testing.T) {
	temporary := t.TempDir()
	module := modmodule.Version{Path: "example.com/directory", Version: "v1.0.0"}
	source := filepath.Join(temporary, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConsumerFile(t, filepath.Join(source, "go.mod"), "module "+module.Path+"\n\ngo 1.25.0\n")
	writeConsumerFile(t, filepath.Join(source, "directory.go"), "package directory\n")
	outside := filepath.Join(temporary, "outside")
	writeConsumerFile(t, outside, "outside")
	if err := os.Symlink(outside, filepath.Join(source, "outside-link")); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(temporary, "destination")
	if err := materializeHermeticModuleSource(source, filepath.Join(temporary, "missing.zip"), destination, module); err != nil {
		t.Fatalf("materialize checked module directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "go.mod")); err != nil {
		t.Fatalf("materialized module has no go.mod: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(destination, "outside-link")); !os.IsNotExist(err) {
		t.Fatalf("symlink entered materialized module: %v", err)
	}
}

func TestHermeticModuleArchiveRejectsUncompressedSizeBeyondLimit(t *testing.T) {
	temporary := t.TempDir()
	module := modmodule.Version{Path: "example.com/bounded", Version: "v1.0.0"}
	archive := filepath.Join(temporary, "module.zip")
	archiveFile, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archiveFile)
	header := &zip.FileHeader{
		Name:               module.Path + "@" + module.Version + "/payload.bin",
		Method:             zip.Store,
		CompressedSize64:   0,
		UncompressedSize64: uint64(modzip.MaxZipFile) + 1,
	}
	if _, err := writer.CreateRaw(header); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}
	checked, checkErr := modzip.CheckZip(module, archive)
	if checkErr == nil || checked.SizeError == nil {
		t.Fatalf("fixture does not exceed the module archive limit: checked=%#v err=%v", checked, checkErr)
	}

	destination := filepath.Join(temporary, "destination")
	if err := materializeHermeticModuleSource(
		filepath.Join(temporary, "missing-extracted-source"),
		archive,
		destination,
		module,
	); err == nil {
		t.Fatal("oversized module archive was accepted")
	}
	assertDirectoryAbsentOrEmpty(t, destination)
}

type moduleArchiveEntry struct {
	name    string
	content string
}

func createCanonicalModuleArchive(t *testing.T, archivePath, source string, module modmodule.Version) {
	t.Helper()
	archive, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	createErr := modzip.CreateFromDir(archive, module, source)
	closeErr := archive.Close()
	if createErr != nil || closeErr != nil {
		t.Fatalf("create canonical module archive: create=%v close=%v", createErr, closeErr)
	}
	if _, err := modzip.CheckZip(module, archivePath); err != nil {
		t.Fatalf("created module archive is not canonical: %v", err)
	}
}

func createRawModuleArchive(t *testing.T, archivePath string, module modmodule.Version, entries []moduleArchiveEntry) {
	t.Helper()
	archive, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archive)
	for _, entry := range entries {
		file, err := writer.Create(module.Path + "@" + module.Version + "/" + entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(entry.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertDirectoryAbsentOrEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("read materialization destination: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed materialization left %d entries", len(entries))
	}
}

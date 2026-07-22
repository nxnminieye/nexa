package apigo

import (
	"context"
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	generationapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
	sdkapi "github.com/nxnminieye/nexa/sdk/api"
	goctlspec "github.com/zeromicro/go-zero/tools/goctl/api/spec"
	goctlparser "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/parser"
)

var artifactIDPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,127}$`)

type staticArtifact struct {
	id, path, owner string
	content         []byte
	sources         []provenance.SourceRef
}

func stageStaticArtifacts(staging, modulePath string, values []staticArtifact) error {
	if err := os.WriteFile(path.Join(staging, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.25.0\n"), 0o600); err != nil {
		return err
	}
	for _, value := range values {
		name := path.Join(staging, value.path)
		if err := os.MkdirAll(path.Dir(name), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(name, value.content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func verifyArtifacts(ctx context.Context, staging, modulePath string, prepared []staticArtifact, inventory resultDocument, refs []provenance.SourceRef, inputDigest provenance.Digest) ([]transaction.ArtifactInput, map[string][]byte, error) {
	root, err := os.OpenRoot(staging)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	preparedByPath := make(map[string]staticArtifact, len(prepared))
	for _, value := range prepared {
		preparedByPath[value.path] = value
	}
	result := make([]transaction.ArtifactInput, len(inventory.Artifacts))
	contents := make(map[string][]byte, len(inventory.Artifacts))
	goFiles := map[string][]*ast.File{}
	packageNames := map[string]string{}
	fileSet := token.NewFileSet()
	seenIDs, seenPaths := map[string]bool{}, map[string]bool{}
	previousID := ""
	corePrefix := path.Join("backend", inventory.CoreServiceID) + "/"
	aggregateID := "api.aggregate." + inventory.CoreServiceID
	aggregatePath := path.Join("backend", inventory.CoreServiceID, "desc/generated", inventory.CoreServiceID+".generated.api")
	hasGeneratedAPIFragment := false
	for _, value := range prepared {
		if path.Ext(value.path) == ".api" && value.id != aggregateID {
			hasGeneratedAPIFragment = true
			break
		}
	}
	for index, item := range inventory.Artifacts {
		if !artifactIDPattern.MatchString(item.ID) || previousID != "" && item.ID <= previousID || seenIDs[item.ID] {
			return nil, nil, errors.New("artifact id invalid")
		}
		previousID, seenIDs[item.ID] = item.ID, true
		if !fs.ValidPath(item.Path) || item.Path == "." || !strings.HasPrefix(item.Path, corePrefix) || seenPaths[item.Path] || path.Ext(item.Path) != ".go" && path.Ext(item.Path) != ".api" {
			return nil, nil, errors.New("artifact path invalid")
		}
		seenPaths[item.Path] = true
		if item.Role != roleGenerated && item.Role != roleManual || item.Role == roleManual && path.Ext(item.Path) != ".go" {
			return nil, nil, errors.New("artifact role invalid")
		}
		content, err := readArtifact(root, item.Path)
		if err != nil {
			return nil, nil, err
		}
		digest, err := provenance.ParseDigest(item.Digest)
		if err != nil || digest != provenance.SHA256(content) {
			return nil, nil, errors.New("artifact digest invalid")
		}
		owner := generatedOwner
		sources := append([]provenance.SourceRef(nil), refs...)
		preparedValue, isPrepared := preparedByPath[item.Path]
		if isPrepared {
			if item.ID != preparedValue.id || item.Role != roleGenerated || !strings.EqualFold(item.Digest, provenance.SHA256(preparedValue.content).String()) || string(content) != string(preparedValue.content) {
				return nil, nil, errors.New("static artifact changed")
			}
			owner = preparedValue.owner
			sources = append([]provenance.SourceRef(nil), preparedValue.sources...)
		}
		var probe transaction.OwnershipProbe
		probeKind := projectionGo
		switch path.Ext(item.Path) {
		case ".go":
			parsed, err := parser.ParseFile(fileSet, item.Path, content, parser.ParseComments|parser.AllErrors)
			if err != nil || !isPrepared && item.Role == roleGenerated && !ast.IsGenerated(parsed) || item.Role == roleManual && ast.IsGenerated(parsed) {
				return nil, nil, errors.New("Go artifact invalid")
			}
			directory := path.Dir(item.Path)
			if existing := packageNames[directory]; existing != "" && existing != parsed.Name.Name {
				return nil, nil, errors.New("Go package invalid")
			}
			packageNames[directory] = parsed.Name.Name
			goFiles[directory] = append(goFiles[directory], parsed)
		case ".api":
			probeKind = projectionAPI
			parsed, err := goctlparser.Parse(path.Join(staging, item.Path), nil)
			var validationErr error
			if err == nil {
				validationErr = parsed.Validate()
			}
			allowEmptyAggregate := isPrepared && item.ID == aggregateID && item.Path == aggregatePath && !hasGeneratedAPIFragment && errors.Is(validationErr, goctlspec.ErrMissingService)
			if err != nil || validationErr != nil && !allowEmptyAggregate {
				return nil, nil, errors.New("API artifact invalid")
			}
		}
		input := transaction.ArtifactInput{
			ID: item.ID, Path: item.Path, Owner: owner, Digest: digest,
			Sources: sources, StalePolicy: artifact.StaleDeleteIfUnmodified,
		}
		if item.Role == roleManual {
			input.Owner = manualOwner
			input.StalePolicy = artifact.StaleRetain
			input.CreateManual = true
		} else {
			probe = generatedOwnershipProbe{id: item.ID, path: item.Path, inputDigest: inputDigest, contentDigest: digest, kind: probeKind, generatedMarker: !isPrepared && path.Ext(item.Path) == ".go"}
			input.Probe = probe
		}
		result[index] = input
		contents[item.Path] = append([]byte(nil), content...)
	}
	for _, value := range prepared {
		if !seenPaths[value.path] {
			return nil, nil, errors.New("static artifact omitted")
		}
	}
	if err := rejectUnlistedLanguageFiles(root, seenPaths); err != nil {
		return nil, nil, err
	}
	directories := make([]string, 0, len(goFiles))
	for directory := range goFiles {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	checker := &stagedImporter{prefix: modulePath, files: goFiles, fileSet: fileSet, fallback: importer.Default(), packages: map[string]*types.Package{}, checking: map[string]bool{}}
	for _, directory := range directories {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if _, err := checker.check(directory); err != nil {
			return nil, nil, err
		}
	}
	return result, contents, nil
}

func rejectUnlistedLanguageFiles(root *os.Root, listed map[string]bool) error {
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr == nil && entry.IsDir() && name == ".nexa-env" {
			return fs.SkipDir
		}
		if walkErr != nil || entry.IsDir() || name == "go.mod" || name == "go.sum" {
			return walkErr
		}
		if (path.Ext(name) == ".go" || path.Ext(name) == ".api") && !listed[name] {
			return errors.New("unlisted artifact")
		}
		return nil
	})
}

type stagedImporter struct {
	prefix   string
	files    map[string][]*ast.File
	fileSet  *token.FileSet
	fallback types.Importer
	packages map[string]*types.Package
	checking map[string]bool
}

func (i *stagedImporter) Import(importPath string) (*types.Package, error) {
	prefix := strings.TrimSuffix(i.prefix, "/") + "/"
	if strings.HasPrefix(importPath, prefix) {
		directory := strings.TrimPrefix(importPath, prefix)
		if _, ok := i.files[directory]; ok {
			return i.check(directory)
		}
	}
	value, err := i.fallback.Import(importPath)
	if err == nil {
		return value, nil
	}
	// The helper already completed a real staged go test. The independent
	// adapter check only needs external package identities while checking the
	// generated package declarations and all local package relations.
	stub := types.NewPackage(importPath, path.Base(importPath))
	stub.MarkComplete()
	return stub, nil
}

func (i *stagedImporter) check(directory string) (*types.Package, error) {
	if value := i.packages[directory]; value != nil {
		return value, nil
	}
	if i.checking[directory] {
		return nil, errors.New("generated package import cycle")
	}
	i.checking[directory] = true
	defer delete(i.checking, directory)
	config := types.Config{Importer: i, IgnoreFuncBodies: true}
	value, err := config.Check(strings.TrimSuffix(i.prefix, "/")+"/"+directory, i.fileSet, i.files[directory], nil)
	if err != nil {
		return nil, err
	}
	i.packages[directory] = value
	return value, nil
}

func readArtifact(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > toolchain.MaxStdoutBytes {
		return nil, errors.New("artifact file invalid")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(io.LimitReader(file, toolchain.MaxStdoutBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(content) > toolchain.MaxStdoutBytes {
		return nil, errors.Join(readErr, closeErr)
	}
	return content, nil
}

type projectionKind uint8

const (
	projectionGo projectionKind = iota + 1
	projectionAPI
	projectionAPIManifest
	projectionRuntimeContract
)

type generatedOwnershipProbe struct {
	id, path        string
	inputDigest     provenance.Digest
	contentDigest   provenance.Digest
	kind            projectionKind
	generatedMarker bool
}

func (p generatedOwnershipProbe) Inspect(name string, content []byte, expected transaction.Ownership) (bool, error) {
	if name != p.path || expected.GeneratorID != generatorID || expected.ArtifactID != p.id || expected.InputDigest != p.inputDigest || provenance.SHA256(content) != p.contentDigest {
		return false, nil
	}
	switch p.kind {
	case projectionGo:
		parsed, err := parser.ParseFile(token.NewFileSet(), name, content, parser.ParseComments|parser.AllErrors)
		return err == nil && (!p.generatedMarker || ast.IsGenerated(parsed)), nil
	case projectionAPI:
		value := goctlparser.New(name, content)
		_ = value.Parse()
		return value.CheckErrors() == nil, nil
	case projectionAPIManifest:
		manifest, err := generationapi.Parse(name, content)
		if err != nil {
			return false, nil
		}
		canonical, err := manifest.CanonicalJSON()
		return err == nil && string(canonical) == string(content), nil
	case projectionRuntimeContract:
		contract, err := sdkapi.ParseRuntimeContract(content)
		if err != nil {
			return false, nil
		}
		canonical, err := contract.CanonicalJSON()
		return err == nil && string(canonical) == string(content), nil
	default:
		return false, nil
	}
}

// StaleOwnershipProbes returns artifact-bound probes for the previous API Go
// manifest. The manifest identity and digest stay part of every probe, while
// the owner parser validates the artifact kind before deletion or update.
func StaleOwnershipProbes(previous artifact.Manifest) []transaction.OwnershipProbe {
	if previous.Generator().ID() != generatorID || previous.Generator().Version() != generatorVersion {
		return nil
	}
	artifacts := previous.Artifacts()
	result := make([]transaction.OwnershipProbe, 0, len(artifacts))
	for _, item := range artifacts {
		if item.StalePolicy() != artifact.StaleDeleteIfUnmodified {
			continue
		}
		kind, ok := staleProjectionKind(item)
		if !ok {
			continue
		}
		result = append(result, generatedOwnershipProbe{
			id: item.ID(), path: item.Path(), inputDigest: previous.InputDigest(), contentDigest: item.Digest(), kind: kind,
		})
	}
	return result
}

func staleProjectionKind(item artifact.Artifact) (projectionKind, bool) {
	switch {
	case strings.HasPrefix(item.ID(), "api-manifest.") && path.Ext(item.Path()) == ".json":
		return projectionAPIManifest, true
	case strings.HasPrefix(item.ID(), "runtime-contract.") && path.Ext(item.Path()) == ".json":
		return projectionRuntimeContract, true
	case path.Ext(item.Path()) == ".api":
		return projectionAPI, true
	case path.Ext(item.Path()) == ".go":
		return projectionGo, true
	default:
		return 0, false
	}
}

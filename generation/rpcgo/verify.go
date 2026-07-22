package rpcgo

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/bufbuild/protocompile"
	protoparser "github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
)

var artifactIDPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,127}$`)

func verifyArtifacts(ctx context.Context, staging string, document protocol.Document, inventory resultDocument) ([]transaction.ArtifactInput, map[string][]byte, error) {
	root, err := os.OpenRoot(staging)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	refs := protocolSourceRefs(document)
	ownershipDigest, err := artifact.ComputeInputDigest(artifact.GeneratorSpec{ID: generatorID, Version: generatorVersion}, document.Sources())
	if err != nil {
		return nil, nil, err
	}
	result := make([]transaction.ArtifactInput, len(inventory.Artifacts))
	contents := make(map[string][]byte, len(inventory.Artifacts))
	protoFiles := map[string]string{}
	packageNames := map[string]string{}
	fileSet := token.NewFileSet()
	seenIDs, seenPaths := map[string]bool{}, map[string]bool{}
	previousID := ""
	for index, item := range inventory.Artifacts {
		if !artifactIDPattern.MatchString(item.ID) || previousID != "" && item.ID <= previousID || seenIDs[item.ID] {
			return nil, nil, errors.New("artifact id invalid")
		}
		previousID, seenIDs[item.ID] = item.ID, true
		if !fs.ValidPath(item.Path) || item.Path == "." || strings.HasPrefix(item.Path, ".nexa-env/") || seenPaths[item.Path] || path.Ext(item.Path) != ".go" && path.Ext(item.Path) != ".proto" {
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
		owner, stale := generatedOwner, artifact.StaleDeleteIfUnmodified
		var probe transaction.OwnershipProbe
		switch path.Ext(item.Path) {
		case ".go":
			parsed, err := parser.ParseFile(fileSet, item.Path, content, parser.ParseComments|parser.AllErrors)
			if err != nil || item.Role == roleGenerated && !ast.IsGenerated(parsed) || item.Role == roleManual && ast.IsGenerated(parsed) {
				return nil, nil, errors.New("Go artifact invalid")
			}
			directory := path.Dir(item.Path)
			if existing := packageNames[directory]; existing != "" && existing != parsed.Name.Name {
				return nil, nil, errors.New("Go package invalid")
			}
			packageNames[directory] = parsed.Name.Name
		case ".proto":
			protoFiles[item.Path] = string(content)
		}
		if item.Role == roleManual {
			owner, stale = manualOwner, artifact.StaleRetain
		} else {
			probe = generatedOwnershipProbe{id: item.ID, path: item.Path, inputDigest: ownershipDigest, contentDigest: digest}
		}
		result[index] = transaction.ArtifactInput{ID: item.ID, Path: item.Path, Owner: owner, Digest: digest, Sources: append([]provenance.SourceRef(nil), refs...), StalePolicy: stale, Probe: probe, CreateManual: item.Role == roleManual}
		contents[item.Path] = append([]byte(nil), content...)
	}
	if err := compileProtoArtifacts(ctx, protoFiles); err != nil {
		return nil, nil, err
	}
	return result, contents, nil
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

func compileProtoArtifacts(ctx context.Context, files map[string]string) error {
	if len(files) == 0 {
		return nil
	}
	entries := make([]string, 0, len(files))
	for name := range files {
		entries = append(entries, name)
	}
	sort.Strings(entries)
	resolver := &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(files)}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(resolver)}
	_, err := compiler.Compile(ctx, entries...)
	return err
}

func protocolSourceRefs(document protocol.Document) []provenance.SourceRef {
	sources := document.Sources()
	result := make([]provenance.SourceRef, len(sources))
	for index, source := range sources {
		result[index] = source.Ref
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}

type generatedOwnershipProbe struct {
	id, path                   string
	inputDigest, contentDigest provenance.Digest
}

func (p generatedOwnershipProbe) Inspect(name string, content []byte, expected transaction.Ownership) (bool, error) {
	if name != p.path || expected.GeneratorID != generatorID || expected.ArtifactID != p.id || expected.InputDigest != p.inputDigest || provenance.SHA256(content) != p.contentDigest {
		return false, nil
	}
	switch path.Ext(name) {
	case ".go":
		parsed, err := parser.ParseFile(token.NewFileSet(), name, content, parser.ParseComments|parser.AllErrors)
		return err == nil && ast.IsGenerated(parsed), nil
	case ".proto":
		_, err := protoparser.Parse(name, bytes.NewReader(content), reporter.NewHandler(nil))
		return err == nil, nil
	default:
		return false, nil
	}
}

// StaleOwnershipProbes returns artifact-bound probes for the previous RPC Go
// manifest. They authorize only unchanged, parseable artifacts produced by
// the same generator contract.
func StaleOwnershipProbes(previous artifact.Manifest) []transaction.OwnershipProbe {
	if previous.Generator().ID() != generatorID || previous.Generator().Version() != generatorVersion {
		return nil
	}
	artifacts := previous.Artifacts()
	result := make([]transaction.OwnershipProbe, 0, len(artifacts))
	for _, item := range artifacts {
		if item.StalePolicy() != artifact.StaleDeleteIfUnmodified || path.Ext(item.Path()) != ".go" && path.Ext(item.Path()) != ".proto" {
			continue
		}
		result = append(result, generatedOwnershipProbe{
			id: item.ID(), path: item.Path(), inputDigest: previous.InputDigest(), contentDigest: item.Digest(),
		})
	}
	return result
}

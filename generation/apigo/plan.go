package apigo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
	sdkapi "github.com/nxnminieye/nexa/sdk/api"
	"golang.org/x/mod/module"
)

var serviceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func Plan(ctx context.Context, document httpapi.Document, rendered []composition.RenderedArtifact, options Options) (artifacts []transaction.ArtifactInput, planErr error) {
	if ctx == nil || !serviceIDPattern.MatchString(options.CoreServiceID) || options.RepositoryRoot == "" || options.StagingRoot == "" || options.Emit == nil || options.Runner == nil || options.Tool.ID == "" || options.Tool.Version == "" || options.Tool.Executable == "" || options.Tool.Probe.ExpectedVersion == "" {
		return nil, failure("input", "request_invalid", options, nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, failure("input", "operation_canceled", options, err)
	}
	repositoryRoot, err := canonicalDirectory(options.RepositoryRoot)
	if err != nil {
		return nil, failure("input", "request_invalid", options, err)
	}
	stagingRoot, err := canonicalDirectory(options.StagingRoot)
	if err != nil {
		return nil, failure("input", "request_invalid", options, err)
	}
	overlap, err := directoriesOverlap(repositoryRoot, stagingRoot)
	if err != nil {
		return nil, failure("input", "request_invalid", options, err)
	}
	if overlap {
		return nil, failure("input", "request_invalid", options, os.ErrInvalid)
	}

	manifestSpec, err := httpapi.ManifestSpec(document)
	if err != nil {
		return nil, failure("manifest", contractReason(err, "manifest_invalid"), options, err)
	}
	manifest, err := api.NewManifest(manifestSpec)
	if err != nil {
		return nil, failure("manifest", contractReason(err, "manifest_invalid"), options, err)
	}
	runtimeContract, err := sdkapi.BuildRuntimeContract(manifest)
	if err != nil {
		// This is a runtime capability failure, not an API adapter failure.
		return nil, err
	}
	manifestJSON, err := manifest.CanonicalJSON()
	if err != nil {
		return nil, failure("manifest", "manifest_invalid", options, err)
	}
	runtimeJSON, err := runtimeContract.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	stdin, err := httpapi.CanonicalJSON(document)
	if err != nil || len(stdin) > toolchain.MaxStdinBytes {
		return nil, failure("input", "api_input_invalid", options, err)
	}

	sources, refs, err := normalizeOwnerSources(options.Sources, document.Sources(), rendered)
	if err != nil {
		return nil, failure("input", "source_closure_invalid", options, err)
	}
	ownershipDigest, err := artifact.ComputeInputDigest(artifact.GeneratorSpec{ID: generatorID, Version: generatorVersion}, sources)
	if err != nil {
		return nil, failure("input", "source_closure_invalid", options, err)
	}
	prepared, modulePath, err := prepareStaticArtifacts(options.CoreServiceID, rendered, refs)
	if err != nil {
		return nil, failure("input", "static_artifact_invalid", options, err)
	}

	if err := stageStaticArtifacts(stagingRoot, modulePath, prepared); err != nil {
		return nil, failure("staging", "staging_create_failed", options, err)
	}

	result, err := options.Runner.Run(ctx, toolchain.Request{
		RepositoryRoot: repositoryRoot,
		StagingRoot:    stagingRoot,
		WorkDir:        stagingRoot,
		Tool:           options.Tool,
		Args:           []string{"generate", "--core-service", options.CoreServiceID},
		Environment:    append([]toolchain.EnvVar(nil), options.Environment...),
		Stdin:          append([]byte(nil), stdin...),
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, failure("generate", "operation_canceled", options, err)
		}
		return nil, failure("generate", "tool_failed", options, err)
	}
	if len(result.Stdout) > toolchain.MaxStdoutBytes {
		return nil, failure("result", "result_output_limit", options, nil)
	}
	if !validResultIdentity(result, options) {
		return nil, failure("result", "tool_result_invalid", options, nil)
	}
	if result.ExitCode != 0 {
		return nil, failureWithExit("generate", "tool_failed", options, result.ExitCode)
	}
	inventory, err := parseResult(result.Stdout, options.CoreServiceID, provenance.SHA256(stdin))
	if err != nil {
		return nil, failure("result", "result_invalid", options, err)
	}
	var verifiedContent map[string][]byte
	artifacts, verifiedContent, err = verifyArtifacts(ctx, stagingRoot, modulePath, prepared, inventory, refs, ownershipDigest)
	if err != nil {
		if ctx.Err() != nil {
			return nil, failure("verify", "operation_canceled", options, err)
		}
		return nil, failure("verify", "artifact_invalid", options, err)
	}
	for _, value := range artifacts {
		if err := options.Emit(value.Path, verifiedContent[value.Path]); err != nil {
			return nil, failure("staging", "staging_create_failed", options, err)
		}
	}
	for _, projection := range []struct {
		id, path string
		content  []byte
		kind     projectionKind
	}{
		{"api-manifest." + options.CoreServiceID, path.Join("backend", options.CoreServiceID, "generated/api-manifest.json"), manifestJSON, projectionAPIManifest},
		{"runtime-contract." + options.CoreServiceID, path.Join("backend", options.CoreServiceID, "generated/runtime-contract.json"), runtimeJSON, projectionRuntimeContract},
	} {
		if err := options.Emit(projection.path, projection.content); err != nil {
			return nil, failure("staging", "staging_create_failed", options, err)
		}
		artifacts = append(artifacts, projectionArtifact(projection.id, projection.path, projection.content, refs, ownershipDigest, projection.kind))
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].ID < artifacts[j].ID })
	return artifacts, nil
}

func canonicalDirectory(directoryPath string) (string, error) {
	directory, err := filepath.Abs(directoryPath)
	if err != nil {
		return "", err
	}
	directory, err = filepath.EvalSymlinks(filepath.Clean(directory))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", os.ErrInvalid
	}
	return directory, nil
}

func directoriesOverlap(left, right string) (bool, error) {
	for _, pair := range [][2]string{{left, right}, {right, left}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err != nil {
			return false, err
		}
		if relative == "." || filepath.IsLocal(relative) {
			return true, nil
		}
	}
	return false, nil
}

func validResultIdentity(result toolchain.Result, options Options) bool {
	return result.ToolID == options.Tool.ID && result.Version == options.Tool.Version && result.ExecutableVersion == options.Tool.Probe.ExpectedVersion
}

func parseResult(data []byte, serviceID string, inputDigest provenance.Digest) (resultDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document resultDocument
	if err := decoder.Decode(&document); err != nil {
		return resultDocument{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return resultDocument{}, errors.New("result has trailing data")
	}
	canonical, err := jcs.Transform(data)
	if err != nil || !bytes.Equal(canonical, data) {
		return resultDocument{}, errors.New("result is not canonical")
	}
	parsedDigest, digestErr := provenance.ParseDigest(document.InputDigest)
	if document.APIVersion != resultAPIVersion || document.Kind != resultKind || document.CoreServiceID != serviceID || digestErr != nil || parsedDigest != inputDigest || !document.GoTestPassed || len(document.Artifacts) == 0 {
		return resultDocument{}, io.ErrUnexpectedEOF
	}
	return document, nil
}

func projectionArtifact(id, artifactPath string, content []byte, refs []provenance.SourceRef, inputDigest provenance.Digest, kind projectionKind) transaction.ArtifactInput {
	return transaction.ArtifactInput{
		ID: id, Path: artifactPath, Owner: generatedOwner,
		Digest: provenance.SHA256(content), Sources: append([]provenance.SourceRef(nil), refs...),
		StalePolicy: artifact.StaleDeleteIfUnmodified,
		Probe:       generatedOwnershipProbe{id: id, path: artifactPath, inputDigest: inputDigest, contentDigest: provenance.SHA256(content), kind: kind},
	}
}

func normalizeOwnerSources(values, apiSources []provenance.Source, rendered []composition.RenderedArtifact) ([]provenance.Source, []provenance.SourceRef, error) {
	result := append([]provenance.Source(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i].Ref.String() < result[j].Ref.String() })
	set := make(map[string]provenance.Source, len(result))
	expected := make(map[string]struct{}, len(apiSources))
	for _, source := range apiSources {
		expected[source.Ref.String()] = struct{}{}
	}
	for _, value := range rendered {
		for _, ref := range value.Sources {
			expected[ref.String()] = struct{}{}
		}
	}
	refs := make([]provenance.SourceRef, len(result))
	for index, source := range result {
		ref, refErr := provenance.ParseSourceRef(source.Ref.String())
		digest, digestErr := provenance.ParseDigest(source.Digest.String())
		if refErr != nil || digestErr != nil || ref != source.Ref || digest != source.Digest {
			return nil, nil, errors.New("source invalid")
		}
		if _, duplicate := set[ref.String()]; duplicate {
			return nil, nil, errors.New("source duplicate")
		}
		set[ref.String()] = source
		refs[index] = ref
	}
	if len(set) != len(expected) {
		return nil, nil, errors.New("source closure is not exact")
	}
	for ref := range expected {
		if _, ok := set[ref]; !ok {
			return nil, nil, errors.New("source omitted")
		}
	}
	for _, source := range apiSources {
		declared, ok := set[source.Ref.String()]
		if !ok || declared.Digest != source.Digest {
			return nil, nil, errors.New("API source omitted")
		}
	}
	for _, value := range rendered {
		for _, ref := range value.Sources {
			if _, ok := set[ref.String()]; !ok {
				return nil, nil, errors.New("composition source omitted")
			}
		}
	}
	return result, refs, nil
}

type reasonOwner interface{ Reason() string }

func contractReason(err error, fallback string) string {
	var owner reasonOwner
	if errors.As(err, &owner) && owner.Reason() != "" {
		return owner.Reason()
	}
	return fallback
}

func sourceRefs(values []provenance.Source) []provenance.SourceRef {
	result := make([]provenance.SourceRef, len(values))
	for index, value := range values {
		result[index] = value.Ref
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}

func prepareStaticArtifacts(coreServiceID string, rendered []composition.RenderedArtifact, refs []provenance.SourceRef) ([]staticArtifact, string, error) {
	coreRoot := path.Join("backend", coreServiceID)
	values := make([]staticArtifact, 0, len(rendered)+1)
	apiImports := make([]string, 0)
	modulePath := "example.invalid/nexa/api/" + coreServiceID
	seenIDs, seenPaths := map[string]struct{}{}, map[string]struct{}{}
	for _, value := range rendered {
		if !artifactIDPattern.MatchString(value.ID) || !fs.ValidPath(value.Path) || value.Path == "." || value.Owner == "" || len(value.Content) == 0 || len(value.Content) > toolchain.MaxStdoutBytes || !strings.HasPrefix(value.Path, coreRoot+"/") || path.Ext(value.Path) != ".go" && path.Ext(value.Path) != ".api" {
			return nil, "", errors.New("composition artifact invalid")
		}
		if _, duplicate := seenIDs[value.ID]; duplicate {
			return nil, "", errors.New("composition artifact id duplicate")
		}
		if _, duplicate := seenPaths[value.Path]; duplicate {
			return nil, "", errors.New("composition artifact path duplicate")
		}
		seenIDs[value.ID], seenPaths[value.Path] = struct{}{}, struct{}{}
		seenSources := map[string]struct{}{}
		for _, source := range value.Sources {
			parsed, err := provenance.ParseSourceRef(source.String())
			if err != nil || parsed != source {
				return nil, "", errors.New("composition artifact source invalid")
			}
			if _, duplicate := seenSources[source.String()]; duplicate {
				return nil, "", errors.New("composition artifact source duplicate")
			}
			seenSources[source.String()] = struct{}{}
		}
		values = append(values, staticArtifact{id: value.ID, path: value.Path, owner: value.Owner, content: append([]byte(nil), value.Content...), sources: append([]provenance.SourceRef(nil), value.Sources...)})
		if path.Ext(value.Path) == ".api" {
			if path.Dir(value.Path) != path.Join(coreRoot, "desc/generated") {
				return nil, "", errors.New("composition API path invalid")
			}
			apiImports = append(apiImports, path.Base(value.Path))
		}
		if discovered := discoverModulePath(value.Content, coreRoot); discovered != "" {
			modulePath = discovered
		}
	}
	sort.Strings(apiImports)
	var aggregate strings.Builder
	aggregate.WriteString("syntax = \"v1\"\n")
	if len(apiImports) > 0 {
		aggregate.WriteString("import (\n")
		for _, value := range apiImports {
			aggregate.WriteString("  \"")
			aggregate.WriteString(value)
			aggregate.WriteString("\"\n")
		}
		aggregate.WriteString(")\n")
	}
	aggregateID := "api.aggregate." + coreServiceID
	aggregatePath := path.Join(coreRoot, "desc/generated", coreServiceID+".generated.api")
	if _, duplicate := seenIDs[aggregateID]; duplicate {
		return nil, "", errors.New("aggregate artifact id duplicate")
	}
	if _, duplicate := seenPaths[aggregatePath]; duplicate {
		return nil, "", errors.New("aggregate artifact path duplicate")
	}
	values = append(values, staticArtifact{
		id: aggregateID, path: aggregatePath,
		owner: generatedOwner, content: []byte(aggregate.String()), sources: append([]provenance.SourceRef(nil), refs...),
	})
	sort.Slice(values, func(i, j int) bool { return values[i].id < values[j].id })
	return values, modulePath, nil
}

func discoverModulePath(content []byte, coreRoot string) string {
	marker := "/" + strings.Trim(coreRoot, "/") + "/"
	file, err := parser.ParseFile(token.NewFileSet(), "artifact.go", content, parser.ImportsOnly)
	if err != nil {
		return ""
	}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		index := strings.Index(importPath, marker)
		if index <= 0 {
			continue
		}
		candidate := importPath[:index]
		if module.CheckPath(candidate) == nil {
			return candidate
		}
	}
	return ""
}

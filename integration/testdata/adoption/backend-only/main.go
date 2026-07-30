package main

import (
	"bytes"
	"encoding/json"
	"fmt"

	generationapi "github.com/nxnminieye/nexa/generation/api"
	generationhttpapi "github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/runtime/crud"
	"github.com/nxnminieye/nexa/sourceplugin"
)

func main() {
	if _, err := exerciseFrameworkSurfaces(); err != nil {
		panic(err)
	}
}

type frameworkSurfaceResult struct {
	GenerationTypes int
	RenderedBytes   int
	SourceFiles     int
	SourceProfiles  int
	TreeFiles       int
	CRUDLimit       int64
}

func exerciseFrameworkSurfaces() (frameworkSurfaceResult, error) {
	generationSchemas, renderedBytes, err := exerciseGeneration()
	if err != nil {
		return frameworkSurfaceResult{}, err
	}
	sourceFiles, sourceProfiles, treeFiles, err := exerciseSourceBundle()
	if err != nil {
		return frameworkSurfaceResult{}, err
	}
	policy, err := crud.NewWindowPolicy(crud.WindowPolicySpec{MinLimit: 1, MaxLimit: 100, MaxOffset: 1000})
	if err != nil {
		return frameworkSurfaceResult{}, fmt.Errorf("create CRUD window policy: %w", err)
	}
	window, err := policy.Check(20, 0)
	if err != nil || window.Limit() != 20 || window.Offset() != 0 {
		return frameworkSurfaceResult{}, fmt.Errorf("check CRUD window")
	}
	return frameworkSurfaceResult{
		GenerationTypes: generationSchemas,
		RenderedBytes:   renderedBytes,
		SourceFiles:     sourceFiles,
		SourceProfiles:  sourceProfiles,
		TreeFiles:       treeFiles,
		CRUDLimit:       window.Limit(),
	}, nil
}

func exerciseGeneration() (int, int, error) {
	operationRef, err := provenance.RepositoryRef("backend/core/api/desc/starter.api", "GetStarter")
	if err != nil {
		return 0, 0, err
	}
	fieldRef, err := provenance.RepositoryRef("backend/core/api/desc/starter.api", "GetStarter.id")
	if err != nil {
		return 0, 0, err
	}
	sources := []provenance.Source{
		{Ref: operationRef, Digest: provenance.SHA256([]byte("starter operation"))},
		{Ref: fieldRef, Digest: provenance.SHA256([]byte("starter field"))},
	}
	generatedProvenance, err := generationhttpapi.NewGeneratedProvenance(sources)
	if err != nil {
		return 0, 0, fmt.Errorf("create generated provenance: %w", err)
	}
	firstSource, err := sourcecomment.ParseSourceRef("proto://backend/core/rpc/starter.proto#starter.v1.Starter.Get")
	if err != nil {
		return 0, 0, err
	}
	messageSource, err := sourcecomment.ParseSourceRef("proto://backend/core/rpc/starter.proto#starter.v1.StarterRequest")
	if err != nil {
		return 0, 0, err
	}
	fieldSource, err := sourcecomment.ParseSourceRef("proto://backend/core/rpc/starter.proto#starter.v1.StarterRequest.id")
	if err != nil {
		return 0, 0, err
	}
	document, err := generationhttpapi.NewGeneratedDocument(generationhttpapi.GeneratedDocumentSpec{
		Types: []generationhttpapi.GeneratedTypeSpec{{
			SemanticID: "starter.v1.StarterRequest", Name: "StarterRequest", FirstSource: messageSource,
			Shape: generationhttpapi.ValueTypeSpec{Kind: generationhttpapi.ValueObject}, Provenance: generatedProvenance,
			Fields: []generationhttpapi.GeneratedFieldSpec{{
				SemanticID: "starter.v1.StarterRequest.id", FirstSource: fieldSource, Path: []string{"ID"}, Required: true,
				ValueType:  generationhttpapi.ValueTypeSpec{Kind: generationhttpapi.ValueScalar, Name: "string"},
				Provenance: generatedProvenance,
			}},
		}},
		Operations: []generationhttpapi.GeneratedOperationSpec{{
			ID: "starter.get", Method: generationapi.MethodGET, Path: "/starters/{id}", RequestType: "StarterRequest",
			Auth: generationhttpapi.AuthSpec{Mode: generationapi.AuthNone}, Provenance: generatedProvenance, FirstSource: firstSource,
		}},
	})
	if err != nil {
		return 0, 0, fmt.Errorf("create generated HTTP document: %w", err)
	}
	rendered, err := generationhttpapi.RenderGenerated(document)
	if err != nil {
		return 0, 0, fmt.Errorf("render generated HTTP document: %w", err)
	}
	if err := generationhttpapi.VerifyRenderedGenerated("starter.generated.api", rendered, document); err != nil {
		return 0, 0, fmt.Errorf("verify generated HTTP document: %w", err)
	}
	return len(document.Types()), len(rendered), nil
}

func exerciseSourceBundle() (int, int, int, error) {
	content := []byte("package starter\n")
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{
			ProviderID: "example.starter", ModulePath: "example.com/starter", PackagePath: "example.com/starter/source", Version: "v0.1.0",
		},
		Files:    []sourceplugin.FileSpec{{Path: "starter/starter.go", Mode: sourceplugin.Mode0644, Size: int64(len(content)), Digest: provenance.SHA256(content)}},
		Profiles: []sourceplugin.ProfileSpec{{ID: "backend", Files: []string{"starter/starter.go"}}},
	})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("create source manifest: %w", err)
	}
	canonical, err := manifest.CanonicalJSON()
	if err != nil || !json.Valid(canonical) {
		return 0, 0, 0, fmt.Errorf("encode source manifest: %w", err)
	}
	parsed, err := sourceplugin.Parse("starter-source.json", canonical)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse source manifest: %w", err)
	}
	roundTrip, err := parsed.CanonicalJSON()
	if err != nil || !bytes.Equal(canonical, roundTrip) {
		return 0, 0, 0, fmt.Errorf("source manifest round-trip changed")
	}
	tree, err := sourceplugin.NewTree(parsed, []sourceplugin.TreeInput{{Path: "starter/starter.go", Content: content}}, sourceplugin.DefaultTreeLimits())
	if err != nil {
		return 0, 0, 0, fmt.Errorf("create source tree: %w", err)
	}
	provider, err := sourceplugin.NewProvider(parsed, tree)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("create source provider: %w", err)
	}
	return len(provider.Manifest().Files()), len(provider.Manifest().Profiles()), provider.Tree().Len(), nil
}

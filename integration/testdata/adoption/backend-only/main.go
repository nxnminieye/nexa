package main

import (
	"bytes"
	"encoding/json"
	"fmt"

	generationapi "github.com/nxnminieye/nexa/generation/api"
	generationhttpapi "github.com/nxnminieye/nexa/generation/httpapi"
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
	GenerationSchemas int
	RenderedBytes     int
	SourceFiles       int
	SourceProfiles    int
	TreeFiles         int
	CRUDLimit         int64
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
		GenerationSchemas: generationSchemas,
		RenderedBytes:     renderedBytes,
		SourceFiles:       sourceFiles,
		SourceProfiles:    sourceProfiles,
		TreeFiles:         treeFiles,
		CRUDLimit:         window.Limit(),
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
	manifest, err := generationapi.NewManifest(generationapi.ManifestSpec{
		Sources: sources,
		Schemas: []generationapi.SchemaSpec{
			{ID: "scalar.string", Kind: generationapi.SchemaString},
			{
				ID: "starter.request", Kind: generationapi.SchemaObject,
				Provenance: &generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{operationRef}},
				Fields: []generationapi.FieldSpec{{
					Name: "id", SchemaRef: "scalar.string", Required: true,
					Provenance: generationapi.NodeProvenanceSpec{Kind: generationapi.NodeCanonical, Refs: []provenance.SourceRef{fieldRef}},
				}},
			},
		},
	})
	if err != nil {
		return 0, 0, fmt.Errorf("create generation manifest: %w", err)
	}
	canonical, err := manifest.CanonicalJSON()
	if err != nil {
		return 0, 0, fmt.Errorf("encode generation manifest: %w", err)
	}
	parsed, err := generationapi.Parse("starter-api-manifest.json", canonical)
	if err != nil {
		return 0, 0, fmt.Errorf("parse generation manifest: %w", err)
	}
	roundTrip, err := parsed.CanonicalJSON()
	if err != nil || !bytes.Equal(canonical, roundTrip) {
		return 0, 0, fmt.Errorf("generation manifest round-trip changed")
	}

	generatedProvenance, err := generationhttpapi.NewGeneratedProvenance(sources)
	if err != nil {
		return 0, 0, fmt.Errorf("create generated provenance: %w", err)
	}
	document, err := generationhttpapi.NewGeneratedDocument(generationhttpapi.GeneratedDocumentSpec{
		Types: []generationhttpapi.GeneratedTypeSpec{{
			Name: "StarterRequest", Shape: generationhttpapi.ValueTypeSpec{Kind: generationhttpapi.ValueObject}, Provenance: generatedProvenance,
			Fields: []generationhttpapi.GeneratedFieldSpec{{
				Path: []string{"ID"}, Required: true,
				ValueType:  generationhttpapi.ValueTypeSpec{Kind: generationhttpapi.ValueScalar, Name: "string"},
				Binding:    &generationhttpapi.BindingSpec{Location: generationapi.RequestBindingPath, Name: "id"},
				Provenance: generatedProvenance,
			}},
		}},
		Operations: []generationhttpapi.GeneratedOperationSpec{{
			ID: "starter.get", Method: generationapi.MethodGET, Path: "/starter/{id}", RequestType: "StarterRequest",
			ResponseBody: generationapi.ResponseBodyNone, Auth: generationhttpapi.AuthSpec{Mode: generationapi.AuthNone}, Provenance: generatedProvenance,
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
	return len(parsed.Schemas()), len(rendered), nil
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

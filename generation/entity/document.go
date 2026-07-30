package entity

import (
	"fmt"

	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

// AdoptLoadedDocument crosses the internal immutable owner boundary. It does not parse or load data.
func AdoptLoadedDocument(value entityvalue.Document) (Document, error) {
	if !value.Valid() || value.APIVersion() != APIVersion {
		return Document{}, irError("canonical_invalid", "/document", "")
	}
	document := Document{state: value}
	facts, err := typedFactGraph(document)
	if err != nil {
		return Document{}, irError("canonical_invalid", "/factGraph", "")
	}
	document.factGraph = facts
	return document, nil
}

// AdoptLoadedDocumentWithFactGraph retains the source-comment graph emitted by
// the Ent adapter after the same typed projection has been validated.
func AdoptLoadedDocumentWithFactGraph(value entityvalue.Document, facts sourcecomment.FactGraph) (Document, error) {
	document, err := AdoptLoadedDocument(value)
	if err != nil {
		return Document{}, err
	}
	if !facts.Valid() {
		return Document{}, irError("canonical_invalid", "/factGraph", "")
	}
	document.factGraph = facts
	return document, nil
}

// AdoptLoadedDocumentError projects only the internal EntityIR value owner's validation errors.
func AdoptLoadedDocumentError(err error, source provenance.DomainSource) error {
	owner, ok := err.(*entityvalue.Error)
	if !ok || owner == nil {
		return err
	}
	return irError(owner.Reason(), owner.Pointer(), source.String())
}

func typedFactGraph(document Document) (sourcecomment.FactGraph, error) {
	values := make([]sourcecomment.EntityGraphNode, 0)
	for _, item := range document.Entities() {
		source, err := entityFactSource(item.Source(), item.Name())
		if err != nil {
			return sourcecomment.FactGraph{}, err
		}
		meta := item.Meta()
		var crud *sourcecomment.CRUDOperations
		if operations, present := item.CRUD(); present {
			copyOperations := operations
			crud = &copyOperations
		}
		values = append(values, sourcecomment.EntityGraphNode{
			SemanticID: item.Name(), Kind: sourcecomment.NodeSchema, Source: source,
			Location: sourcecomment.Location{File: source.Path(), Line: 1, Column: 1}, NativeCanonical: item.CanonicalSourceJSON(),
			Schema: &meta, CRUD: crud,
		})
		for _, field := range item.Fields() {
			fieldSource, fieldErr := entityFactSource(field.Source(), item.Name()+"."+field.Name())
			if fieldErr != nil {
				return sourcecomment.FactGraph{}, fieldErr
			}
			fieldMeta := field.Meta()
			values = append(values, sourcecomment.EntityGraphNode{
				SemanticID: item.Name() + "." + field.Name(), Kind: sourcecomment.NodeField, Source: fieldSource,
				Location: sourcecomment.Location{File: fieldSource.Path(), Line: 1, Column: 1}, NativeCanonical: field.CanonicalSourceJSON(),
				Field: &fieldMeta,
			})
		}
	}
	graph, diagnostics := sourcecomment.BuildEntityFactGraph(values)
	if len(diagnostics) != 0 {
		return sourcecomment.FactGraph{}, fmt.Errorf("typed source-comment graph is invalid: %s", diagnostics[0].Suggestion)
	}
	return graph, nil
}

func entityFactSource(source provenance.Source, symbol string) (sourcecomment.SourceRef, error) {
	if source.Ref.Path() == "" || symbol == "" {
		return sourcecomment.SourceRef{}, fmt.Errorf("entity source-comment reference is invalid")
	}
	return sourcecomment.ParseSourceRef("ent://" + source.Ref.Path() + "#" + symbol)
}

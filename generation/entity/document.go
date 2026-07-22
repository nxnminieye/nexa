package entity

import (
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/provenance"
)

// AdoptLoadedDocument crosses the internal immutable owner boundary. It does not parse or load data.
func AdoptLoadedDocument(value entityvalue.Document) (Document, error) {
	if !value.Valid() || value.APIVersion() != APIVersion {
		return Document{}, irError("canonical_invalid", "/document", "")
	}
	return Document{state: value}, nil
}

// AdoptLoadedDocumentError projects only the internal EntityIR value owner's validation errors.
func AdoptLoadedDocumentError(err error, source provenance.DomainSource) error {
	owner, ok := err.(*entityvalue.Error)
	if !ok || owner == nil {
		return err
	}
	return irError(owner.Reason(), owner.Pointer(), source.String())
}

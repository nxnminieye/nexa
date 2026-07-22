package crudproto

import (
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/crudbuild"
)

func Build(entities entity.Document, options BuildOptions) (Document, LockProposal, error) {
	var existing *crudbuild.Lock
	if options.ExistingLock != nil {
		value := options.ExistingLock.state
		existing = &value
	}
	document, proposal, err := crudbuild.Build(entities, crudbuild.Spec{
		ServiceID: options.ServiceID, ProtoPackage: options.ProtoPackage, GoPackage: options.GoPackage, ExistingLock: existing,
		MultiTenant: crudbuild.MultiTenantConfig{Enabled: options.MultiTenant.Enabled},
	})
	if err != nil {
		return Document{}, LockProposal{}, wrapError(err)
	}
	return Document{state: document}, LockProposal{state: proposal}, nil
}

func Render(document Document) ([]byte, error) {
	result, err := crudbuild.Render(document.state)
	if err != nil {
		return nil, wrapError(err)
	}
	return result, nil
}

func CanonicalJSON(document Document) ([]byte, error) {
	if document.state.APIVersion() != APIVersion {
		return nil, newStateError("document_state_invalid", "/document")
	}
	return document.state.CanonicalJSON(), nil
}

package composition

import (
	"github.com/nxnminieye/nexa/generation/httpapi"
)

func GeneratedAPI(document Document) (httpapi.Document, error) {
	if document.state == nil {
		return httpapi.Document{}, invalid("document_invalid", "", "/document", "composition document is invalid")
	}
	spec := httpapi.GeneratedDocumentSpec{Facts: document.state.facts}
	for _, projected := range document.state.types {
		value := httpapi.GeneratedTypeSpec{SemanticID: projected.semanticID, Name: projected.name, Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: projected.provenance, FirstSource: projected.firstSource}
		for _, field := range projected.fields {
			value.Fields = append(value.Fields, httpapi.GeneratedFieldSpec{SemanticID: field.semanticID, Path: []string{exportedIdentifier(field.jsonName)}, Required: field.required, ValueType: field.valueType, Provenance: field.provenance, FirstSource: field.firstSource})
		}
		spec.Types = append(spec.Types, value)
	}
	for _, operation := range document.state.operations {
		spec.Operations = append(spec.Operations, httpapi.GeneratedOperationSpec{ID: operation.operationID, Method: operation.httpMethod, Path: operation.path, RequestType: operation.requestType, ResponseType: operation.responseType, Auth: httpapi.AuthSpec{Mode: operation.auth}, Permission: operation.permission, Provenance: operation.provenance, FirstSource: operation.firstSource})
	}
	generated, err := httpapi.NewGeneratedDocument(spec)
	if err != nil {
		return httpapi.Document{}, invalid("generated_api_invalid", "", "/document", err.Error())
	}
	if err := httpapi.ValidateConvention(generated); err != nil {
		return httpapi.Document{}, invalid("generated_api_convention_invalid", "", "/document", err.Error())
	}
	return generated, nil
}

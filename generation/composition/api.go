package composition

import (
	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/provenance"
)

func GeneratedAPI(document Document) (httpapi.Document, error) {
	if document.state == nil {
		return httpapi.Document{}, invalid("document_invalid", "", "/document", "composition document is invalid")
	}
	spec := httpapi.GeneratedDocumentSpec{}
	for _, projected := range document.state.types {
		value := httpapi.GeneratedTypeSpec{Name: projected.name, Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: projected.provenance}
		for _, field := range projected.fields {
			value.Fields = append(value.Fields, httpapi.GeneratedFieldSpec{Path: []string{exportedIdentifier(field.jsonName)}, Required: field.required, ValueType: field.valueType, Binding: &httpapi.BindingSpec{Location: api.RequestBindingBody, Name: field.jsonName}, Provenance: field.provenance})
		}
		spec.Types = append(spec.Types, value)
	}
	for _, operation := range document.state.operations {
		request := httpapi.GeneratedTypeSpec{Name: operation.requestType, Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: operation.requestProvenance}
		for _, binding := range operation.requestFields {
			owner, err := fieldProvenance(operation, binding)
			if err != nil {
				return httpapi.Document{}, err
			}
			location := api.RequestBindingBody
			if pathBinding(operation.proxy.Path(), binding.httpField) {
				location = api.RequestBindingPath
			} else if operation.proxy.Method() == protocol.MethodGET || operation.proxy.Method() == protocol.MethodDELETE {
				location = api.RequestBindingQuery
			}
			request.Fields = append(request.Fields, httpapi.GeneratedFieldSpec{Path: []string{exportedIdentifier(binding.httpField)}, Required: binding.required, ValueType: binding.valueType, Binding: &httpapi.BindingSpec{Location: location, Name: binding.httpField}, Provenance: owner})
		}
		response := httpapi.GeneratedTypeSpec{Name: operation.responseType, Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: operation.responseProvenance}
		for _, binding := range operation.responseFields {
			owner, err := fieldProvenance(operation, binding)
			if err != nil {
				return httpapi.Document{}, err
			}
			response.Fields = append(response.Fields, httpapi.GeneratedFieldSpec{Path: []string{exportedIdentifier(binding.httpField)}, Required: binding.required, ValueType: binding.valueType, Binding: &httpapi.BindingSpec{Location: api.RequestBindingBody, Name: binding.httpField}, Provenance: owner})
		}
		auth := operation.proxy.Auth()
		authSpec := httpapi.AuthSpec{Mode: api.AuthMode(auth.Mode())}
		for _, credential := range auth.Credentials() {
			authSpec.Credentials = append(authSpec.Credentials, httpapi.CredentialSpec{ID: credential.ID(), Type: api.CredentialType(credential.Type()), Location: api.CredentialLocation(credential.Location()), Name: credential.Name()})
		}
		spec.Types = append(spec.Types, request, response)
		spec.Operations = append(spec.Operations, httpapi.GeneratedOperationSpec{ID: operation.proxy.OperationID(), Method: apiMethod(operation.proxy.Method()), Path: operation.proxy.Path(), RequestType: operation.requestType, ResponseBody: api.ResponseBodyJSON, ResponseType: operation.responseType, Auth: authSpec, Permission: operation.proxy.Permission(), Capability: &httpapi.CapabilitySpec{ID: CapabilityID, APIVersion: CapabilityVersion}, ErrorProjections: append([]api.ErrorProjectionSpec(nil), operation.errorProjections...), Provenance: operation.operationProvenance})
	}
	generated, err := httpapi.NewGeneratedDocument(spec)
	if err != nil {
		return httpapi.Document{}, invalid("generated_api_invalid", "", "/document", "generated HTTP API projection is invalid")
	}
	return generated, nil
}

func fieldProvenance(operation *operationState, binding resolvedBinding) (httpapi.NodeProvenance, error) {
	field := binding.fields[len(binding.fields)-1]
	value, err := httpapi.NewGeneratedProvenance([]provenance.Source{operation.method.Source(), field.Source(), operation.bindingSource})
	if err != nil {
		return httpapi.NodeProvenance{}, invalid("provenance_invalid", field.FilePath(), "", "proxy field provenance is invalid")
	}
	return value, nil
}

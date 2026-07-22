package api

import (
	"encoding/json"
	"strconv"

	generationapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

// ParseRuntimeContract parses the bounded canonical runtime projection.
func ParseRuntimeContract(data []byte) (RuntimeContract, error) {
	limits := RuntimeContractLimits()
	if len(data) > limits.RawBytes {
		return RuntimeContract{}, newRuntimeContractInvalid("size_limit_exceeded", "/runtimeContract")
	}
	parser := requestParser{
		data:      data,
		maxDepth:  limits.JSONDepth,
		maxNodes:  limits.JSONNodes,
		semantics: runtimeContractJSONSemantics(),
		allowNull: true,
		newError: func(reason, _ string) *Error {
			return newRuntimeContractInvalid(reason, "/runtimeContract")
		},
	}
	value, err := parser.parseAnyValue("", parser.semantics.RootDepth())
	if err != nil {
		return RuntimeContract{}, err
	}
	parser.skipWhitespace()
	if parser.offset != len(data) {
		return RuntimeContract{}, newRuntimeContractInvalid("trailing_input", "/runtimeContract")
	}
	schema, err := runtimeContractDocumentSchema()
	if err != nil || schema.Validate(value) != nil {
		return RuntimeContract{}, newRuntimeContractInvalid("runtime_contract_schema_invalid", "/runtimeContract")
	}
	canonical, err := canonicalRuntimeContractInput(data, value)
	if err != nil {
		return RuntimeContract{}, newRuntimeContractInvalid("runtime_contract_schema_invalid", "/runtimeContract")
	}
	if !canonical {
		return RuntimeContract{}, newRuntimeContractInvalid("runtime_contract_noncanonical", "/runtimeContract")
	}
	model, err := runtimeModelFromParsed(value)
	value = nil
	if err != nil {
		return RuntimeContract{}, err
	}
	if issue := validateRuntimeModel(model); issue != nil {
		return RuntimeContract{}, newRuntimeContractInvalid(issue.reason, issue.pointer)
	}
	return RuntimeContract{model: model}, nil
}

func runtimeContractJSONSemantics() JSONLimitSemantics {
	return JSONLimitSemantics{
		rootDepth:         0,
		inclusive:         true,
		countsRoot:        true,
		countsValues:      true,
		countsMemberNames: false,
	}
}

func runtimeModelFromParsed(value any) (*runtimeModel, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return nil, runtimeContractProjectionError()
	}
	traceValue, ok := root["trace"].(map[string]any)
	if !ok {
		return nil, runtimeContractProjectionError()
	}
	manifestVersion, manifestVersionOK := traceValue["apiManifestVersion"].(string)
	manifestDigest, manifestDigestOK := traceValue["apiManifestCanonicalDigest"].(string)
	sourceDigest, sourceDigestOK := traceValue["sourceDigest"].(string)
	if !manifestVersionOK || !manifestDigestOK || !sourceDigestOK {
		return nil, runtimeContractProjectionError()
	}
	if _, err := provenance.ParseDigest(manifestDigest); err != nil {
		return nil, runtimeContractProjectionError()
	}
	if _, err := provenance.ParseDigest(sourceDigest); err != nil {
		return nil, runtimeContractProjectionError()
	}

	schemaValues, ok := root["schemas"].([]any)
	if !ok {
		return nil, runtimeContractProjectionError()
	}
	operationValues, ok := root["operations"].(map[string]any)
	if !ok {
		return nil, runtimeContractProjectionError()
	}
	model := &runtimeModel{
		trace: runtimeContractTraceDocument{
			APIManifestVersion: manifestVersion, APIManifestCanonicalDigest: manifestDigest, SourceDigest: sourceDigest,
		},
		schemas:    make([]runtimeSchema, len(schemaValues)),
		operations: make(map[string]runtimeOperation, len(operationValues)),
	}
	for index, value := range schemaValues {
		input, ok := value.(map[string]any)
		if !ok {
			return nil, runtimeContractProjectionError()
		}
		kind, ok := input["kind"].(string)
		if !ok {
			return nil, runtimeContractProjectionError()
		}
		row := runtimeSchema{kind: generationapi.SchemaKind(kind), items: -1}
		if itemValue, present := input["items"]; present {
			items, ok := runtimeParsedSchemaIndex(itemValue)
			if !ok {
				return nil, runtimeContractProjectionError()
			}
			row.items = items
		}
		if fieldValue, present := input["fields"]; present {
			fields, ok := fieldValue.(map[string]any)
			if !ok {
				return nil, runtimeContractProjectionError()
			}
			row.fields = make(map[string]runtimeField, len(fields))
			for name, value := range fields {
				field, ok := value.(map[string]any)
				if !ok {
					return nil, runtimeContractProjectionError()
				}
				required, requiredOK := field["required"].(bool)
				schemaIndex, schemaOK := runtimeParsedSchemaIndex(field["schema"])
				if !requiredOK || !schemaOK {
					return nil, runtimeContractProjectionError()
				}
				row.fields[name] = runtimeField{required: required, schema: schemaIndex}
			}
		}
		model.schemas[index] = row
	}

	for id, value := range operationValues {
		input, ok := value.(map[string]any)
		if !ok {
			return nil, runtimeContractProjectionError()
		}
		method, methodOK := input["method"].(string)
		permission, permissionOK := input["permission"].(string)
		pathValues, pathOK := input["pathSegments"].([]any)
		requestValue, requestOK := input["request"].(map[string]any)
		responseValue, responseOK := input["response"].(map[string]any)
		authValue, authOK := input["auth"].(map[string]any)
		errorValues, errorsOK := input["errorProjections"].(map[string]any)
		if !methodOK || !permissionOK || !pathOK || !requestOK || !responseOK || !authOK || !errorsOK {
			return nil, runtimeContractProjectionError()
		}
		requestSchema, requestSchemaOK := runtimeParsedSchemaIndex(requestValue["schema"])
		bindingValues, bindingsOK := requestValue["bindings"].(map[string]any)
		responseBody, responseBodyOK := responseValue["body"].(string)
		authMode, authModeOK := authValue["mode"].(string)
		credentialValues, credentialsOK := authValue["credentials"].(map[string]any)
		if !requestSchemaOK || !bindingsOK || !responseBodyOK || !authModeOK || !credentialsOK {
			return nil, runtimeContractProjectionError()
		}
		operation := runtimeOperation{
			id:               id,
			method:           generationapi.HTTPMethod(method),
			pathSegments:     make([]runtimePathSegment, len(pathValues)),
			request:          runtimeRequest{schema: requestSchema, bindings: make(map[string]runtimeBinding, len(bindingValues))},
			response:         runtimeResponse{body: generationapi.ResponseBodyMode(responseBody)},
			auth:             runtimeAuth{mode: generationapi.AuthMode(authMode), credentials: make(map[string]runtimeCredential, len(credentialValues))},
			permission:       permission,
			errorProjections: make(map[string]map[string]runtimeErrorTarget, len(errorValues)),
		}
		for index, value := range pathValues {
			segment, ok := value.(map[string]any)
			if !ok {
				return nil, runtimeContractProjectionError()
			}
			if literal, present := segment["literal"]; present {
				operation.pathSegments[index].literal, ok = literal.(string)
			} else if field, present := segment["field"]; present {
				operation.pathSegments[index].field, ok = field.(string)
			} else {
				ok = false
			}
			if !ok {
				return nil, runtimeContractProjectionError()
			}
		}
		for field, value := range bindingValues {
			binding, ok := value.(map[string]any)
			if !ok {
				return nil, runtimeContractProjectionError()
			}
			location, locationOK := binding["in"].(string)
			name, nameOK := binding["name"].(string)
			if !locationOK || !nameOK {
				return nil, runtimeContractProjectionError()
			}
			operation.request.bindings[field] = runtimeBinding{location: generationapi.RequestBindingLocation(location), name: name}
		}
		if schemaValue, present := responseValue["schema"]; present {
			operation.response.schema, ok = runtimeParsedSchemaIndex(schemaValue)
			if !ok {
				return nil, runtimeContractProjectionError()
			}
			operation.response.hasSchema = true
		}
		for credentialID, value := range credentialValues {
			credential, ok := value.(map[string]any)
			if !ok {
				return nil, runtimeContractProjectionError()
			}
			typeID, typeOK := credential["type"].(string)
			location, locationOK := credential["in"].(string)
			name, nameOK := credential["name"].(string)
			if !typeOK || !locationOK || !nameOK {
				return nil, runtimeContractProjectionError()
			}
			operation.auth.credentials[credentialID] = runtimeCredential{
				typeID: generationapi.CredentialType(typeID), location: generationapi.CredentialLocation(location), name: name,
			}
		}
		if value, present := input["capability"]; present {
			capability, ok := value.(map[string]any)
			if !ok {
				return nil, runtimeContractProjectionError()
			}
			capabilityID, idOK := capability["id"].(string)
			apiVersion, versionOK := capability["apiVersion"].(string)
			if !idOK || !versionOK {
				return nil, runtimeContractProjectionError()
			}
			operation.capability = &runtimeCapability{id: capabilityID, apiVersion: apiVersion}
		}
		for domain, value := range errorValues {
			codeValues, ok := value.(map[string]any)
			if !ok {
				return nil, runtimeContractProjectionError()
			}
			targets := make(map[string]runtimeErrorTarget, len(codeValues))
			for code, value := range codeValues {
				target, ok := value.(map[string]any)
				if !ok {
					return nil, runtimeContractProjectionError()
				}
				targetDomain, domainOK := target["domain"].(string)
				targetCode, codeOK := target["code"].(string)
				httpStatus, statusOK := runtimeParsedBoundedInt(target["httpStatus"])
				if !domainOK || !codeOK || !statusOK {
					return nil, runtimeContractProjectionError()
				}
				targets[code] = runtimeErrorTarget{domain: targetDomain, code: targetCode, httpStatus: httpStatus}
			}
			operation.errorProjections[domain] = targets
		}
		model.operations[id] = operation
	}
	return model, nil
}

func runtimeParsedBoundedInt(value any) (int, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	result, err := strconv.Atoi(string(number))
	return result, err == nil
}

func runtimeParsedSchemaIndex(value any) (int, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	result, err := strconv.ParseInt(string(number), 10, 64)
	// Every representable local index is below the public node limit. Larger
	// JSON integers use one invalid sentinel so native validation owns the
	// stable relation reason and pointer on both 32-bit and 64-bit hosts.
	if err != nil || result < 0 || result >= int64(RuntimeContractLimits().JSONNodes) {
		return -1, true
	}
	return int(result), true
}

func runtimeContractProjectionError() *Error {
	return newRuntimeContractInvalid("runtime_contract_schema_invalid", "/runtimeContract")
}

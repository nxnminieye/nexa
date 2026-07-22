package httpapi

import (
	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

func validateSnapshotSemantics(wire wireDocument, sources []provenance.Source) error {
	sourceIndex := make(map[string]provenance.Source, len(sources))
	for _, source := range sources {
		sourceIndex[source.Ref.String()] = source
	}
	types := make([]*typeState, 0, len(wire.Types))
	typeNames := map[string]bool{}
	previousType := ""
	for _, input := range wire.Types {
		if input.Name == "" || typeNames[input.Name] || previousType != "" && input.Name <= previousType {
			return invalid("snapshot_type_invalid", "", "/types", "snapshot types are not uniquely sorted")
		}
		previousType, typeNames[input.Name] = input.Name, true
		shape, err := snapshotValue(input.Shape)
		if err != nil || shape.kind != ValueObject {
			return invalid("snapshot_type_invalid", "", "/types", "snapshot type shape is invalid")
		}
		owner, err := snapshotProvenance(input.Provenance, sourceIndex, "type:"+input.Name, canonicalTypeNode{APIVersion: typeNodeVersion, Kind: "type", Name: input.Name, Shape: canonicalValueOf(shape)})
		if err != nil {
			return err
		}
		state := &typeState{name: input.Name, shape: shape, provenance: owner, fieldIndex: map[string]int{}}
		previousField := ""
		for _, fieldInput := range input.Fields {
			key := pathKey(fieldInput.Path)
			if key == "" || previousField != "" && key <= previousField {
				return invalid("snapshot_field_invalid", "", "/types/fields", "snapshot fields are not uniquely sorted")
			}
			previousField = key
			value, err := snapshotValue(fieldInput.ValueType)
			if err != nil {
				return err
			}
			envelope := canonicalFieldNode{APIVersion: fieldNodeVersion, Kind: "field", OwnerType: input.Name, Path: append([]string(nil), fieldInput.Path...), Required: fieldInput.Required, ValueType: canonicalValueOf(value)}
			field := &fieldState{ownerType: input.Name, path: append([]string(nil), fieldInput.Path...), required: fieldInput.Required, valueType: value}
			if fieldInput.Binding != nil {
				if fieldInput.Binding.Name == "" || !validBindingLocation(fieldInput.Binding.Location) {
					return invalid("snapshot_binding_invalid", "", "/types/fields/binding", "snapshot field binding is invalid")
				}
				field.binding, field.hasBinding = Binding{location: fieldInput.Binding.Location, name: fieldInput.Binding.Name}, true
				envelope.Binding = &canonicalBinding{Location: string(fieldInput.Binding.Location), Name: fieldInput.Binding.Name}
			}
			if fieldInput.Origin != nil {
				ref, refErr := provenance.ParseSourceRef(fieldInput.Origin.Ref)
				digest, digestErr := provenance.ParseDigest(fieldInput.Origin.Digest)
				source, ok := sourceIndex[fieldInput.Origin.Ref]
				if refErr != nil || digestErr != nil || !ok || source.Digest != digest {
					return invalid("snapshot_origin_invalid", "", "/types/fields/origin", "snapshot field origin is invalid")
				}
				field.origin, field.hasOrigin = provenance.Source{Ref: ref, Digest: digest}, true
				envelope.Origin = &canonicalOrigin{Ref: ref.String(), Digest: digest.String()}
			}
			field.provenance, err = snapshotProvenance(fieldInput.Provenance, sourceIndex, "field:"+input.Name+"."+key, envelope)
			if err != nil {
				return err
			}
			if field.hasOrigin {
				for _, source := range field.provenance.sources {
					if source.Ref == field.origin.Ref {
						return invalid("snapshot_origin_redundant", "", "/types/fields/origin", "snapshot origin duplicates node provenance")
					}
				}
			}
			state.fieldIndex[key] = len(state.fields)
			state.fields = append(state.fields, field)
		}
		types = append(types, state)
	}
	operations := make([]*operationState, 0, len(wire.Operations))
	previousOperation := ""
	operationIDs, routes := map[string]bool{}, map[string]bool{}
	for _, input := range wire.Operations {
		if input.ID == "" || operationIDs[input.ID] || previousOperation != "" && input.ID <= previousOperation {
			return invalid("snapshot_operation_invalid", "", "/operations", "snapshot operations are not uniquely sorted")
		}
		previousOperation, operationIDs[input.ID] = input.ID, true
		routeKey := string(input.Method) + "\x00" + input.Path
		if routes[routeKey] {
			return invalid("snapshot_route_duplicate", "", "/operations/path", "snapshot route is duplicated")
		}
		routes[routeKey] = true
		state := &operationState{id: input.ID, method: input.Method, path: input.Path, requestType: input.RequestType, responseBody: input.ResponseBody, responseType: input.ResponseType, permission: input.Permission, auth: Auth{mode: input.Auth.Mode, credentials: make([]Credential, len(input.Auth.Credentials))}, errorProjections: append([]api.ErrorProjectionSpec(nil), input.ErrorProjections...)}
		previousCredential := ""
		for index, credential := range input.Auth.Credentials {
			if previousCredential != "" && credential.ID <= previousCredential {
				return invalid("snapshot_credential_order_invalid", "", "/operations/auth/credentials", "snapshot credentials are not uniquely sorted")
			}
			previousCredential = credential.ID
			state.auth.credentials[index] = Credential{id: credential.ID, typeID: credential.Type, location: credential.Location, name: credential.Name}
		}
		previousError := ""
		for _, projection := range input.ErrorProjections {
			key := projection.Match.Domain + "\x00" + projection.Match.Code
			if previousError != "" && key <= previousError {
				return invalid("snapshot_error_order_invalid", "", "/operations/errorProjections", "snapshot error projections are not uniquely sorted")
			}
			previousError = key
		}
		if input.Capability != nil {
			state.capability, state.hasCapability = Capability{id: input.Capability.ID, apiVersion: input.Capability.APIVersion}, true
		}
		envelope := canonicalRouteNode{APIVersion: routeNodeVersion, Kind: "route", OperationID: input.ID, Method: string(input.Method), Path: input.Path, RequestType: input.RequestType, ResponseBody: string(input.ResponseBody), ResponseType: input.ResponseType, Auth: canonicalAuth{Mode: string(input.Auth.Mode), Credentials: make([]canonicalCredential, len(input.Auth.Credentials))}, Permission: input.Permission, ErrorProjections: canonicalErrors(input.ErrorProjections)}
		for index, credential := range input.Auth.Credentials {
			envelope.Auth.Credentials[index] = canonicalCredential{ID: credential.ID, Type: string(credential.Type), Location: string(credential.Location), Name: credential.Name}
		}
		if input.Capability != nil {
			envelope.Capability = &canonicalCapability{ID: input.Capability.ID, APIVersion: input.Capability.APIVersion}
		}
		var err error
		state.provenance, err = snapshotProvenance(input.Provenance, sourceIndex, "route:"+string(input.Method)+" "+input.Path, envelope)
		if err != nil {
			return err
		}
		operations = append(operations, state)
	}
	for _, operation := range operations {
		if !typeNames[operation.requestType] || operation.responseBody == api.ResponseBodyJSON && !typeNames[operation.responseType] || operation.responseBody == api.ResponseBodyNone && operation.responseType != "" {
			return invalid("snapshot_operation_type_invalid", "", "/operations", "snapshot operation type relation is invalid")
		}
	}
	document, err := newDocument(types, operations, sources)
	if err != nil {
		return err
	}
	manifest, err := ManifestSpec(document)
	if err != nil {
		return err
	}
	if _, err := api.NewManifest(manifest); err != nil {
		return invalid("snapshot_manifest_invalid", "", "/operations", err.Error())
	}
	return nil
}

func snapshotValue(input wireValue) (ValueType, error) {
	spec := ValueTypeSpec{Kind: input.Kind, Name: input.Name}
	if input.Element != nil {
		value := wireValueSpec(*input.Element)
		spec.Element = &value
	}
	if input.Key != nil {
		value := wireValueSpec(*input.Key)
		spec.Key = &value
	}
	if input.Value != nil {
		value := wireValueSpec(*input.Value)
		spec.Value = &value
	}
	return valueFromSpec(spec)
}
func wireValueSpec(input wireValue) ValueTypeSpec {
	result := ValueTypeSpec{Kind: input.Kind, Name: input.Name}
	if input.Element != nil {
		value := wireValueSpec(*input.Element)
		result.Element = &value
	}
	if input.Key != nil {
		value := wireValueSpec(*input.Key)
		result.Key = &value
	}
	if input.Value != nil {
		value := wireValueSpec(*input.Value)
		result.Value = &value
	}
	return result
}

func snapshotProvenance(input wireProvenance, sources map[string]provenance.Source, fragment string, envelope any) (NodeProvenance, error) {
	minimum := 1
	if input.Kind == NodeFactGenerated {
		minimum = 2
	} else if input.Kind != NodeFactNative {
		return NodeProvenance{}, invalid("snapshot_provenance_invalid", "", "/provenance/kind", "snapshot provenance kind is invalid")
	}
	if len(input.Sources) < minimum {
		return NodeProvenance{}, invalid("snapshot_provenance_invalid", "", "/provenance/sources", "snapshot provenance source count is invalid")
	}
	values := make([]provenance.Source, len(input.Sources))
	previous := ""
	for index, refString := range input.Sources {
		if previous != "" && refString <= previous {
			return NodeProvenance{}, invalid("snapshot_provenance_invalid", "", "/provenance/sources", "snapshot provenance sources are not uniquely sorted")
		}
		source, ok := sources[refString]
		if !ok {
			return NodeProvenance{}, invalid("snapshot_source_unresolved", "", "/provenance/sources", "snapshot provenance source is unresolved")
		}
		previous, values[index] = refString, source
	}
	result := NodeProvenance{kind: input.Kind, sources: values}
	if input.Kind == NodeFactNative {
		source := values[0]
		if source.Ref.Fragment() != fragment {
			return NodeProvenance{}, invalid("snapshot_native_ref_invalid", source.Ref.Path(), "/provenance/sources", "snapshot native source fragment is invalid")
		}
		canonical, err := canonicalize(envelope)
		if err != nil || provenance.SHA256(canonical) != source.Digest {
			return NodeProvenance{}, invalid("snapshot_native_digest_mismatch", source.Ref.Path(), "/provenance/sources", "snapshot native owner digest does not match")
		}
		result.canonical = canonical
	}
	return result, nil
}

type snapshotCanonicalErrorMatch struct {
	Domain string `json:"domain"`
	Code   string `json:"code"`
}
type snapshotCanonicalErrorTarget struct {
	Domain     string `json:"domain"`
	Code       string `json:"code"`
	HTTPStatus int    `json:"httpStatus"`
}
type snapshotCanonicalError struct {
	Match   snapshotCanonicalErrorMatch  `json:"match"`
	Project snapshotCanonicalErrorTarget `json:"project"`
}

func canonicalErrors(values []api.ErrorProjectionSpec) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = snapshotCanonicalError{Match: snapshotCanonicalErrorMatch{Domain: value.Match.Domain, Code: value.Match.Code}, Project: snapshotCanonicalErrorTarget{Domain: value.Project.Domain, Code: value.Project.Code, HTTPStatus: value.Project.HTTPStatus}}
	}
	return result
}
func validBindingLocation(value api.RequestBindingLocation) bool {
	return value == api.RequestBindingPath || value == api.RequestBindingQuery || value == api.RequestBindingHeader || value == api.RequestBindingBody
}

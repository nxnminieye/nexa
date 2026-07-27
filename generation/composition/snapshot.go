package composition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/gowebpki/jcs"
	"golang.org/x/mod/module"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/provenance"
)

func ParseSnapshot(source provenance.DomainSource, data []byte) (Snapshot, error) {
	if source.String() == "" {
		return Snapshot{}, invalid("snapshot_source_invalid", "", "", "composition snapshot source is required")
	}
	var envelope struct {
		APIVersion string `json:"apiVersion"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Snapshot{}, invalid("document_invalid", source.String(), "", "composition snapshot is invalid")
	}
	if envelope.APIVersion != APIVersionV1 && envelope.APIVersion != APIVersionV2 {
		return Snapshot{}, invalid("version_unsupported", source.String(), "/apiVersion", "composition snapshot version is unsupported")
	}
	var schemaValue any
	if err := json.Unmarshal(data, &schemaValue); err != nil {
		return Snapshot{}, invalid("document_invalid", source.String(), "", "composition snapshot schema is invalid")
	}
	if err := validateSnapshotSchema(envelope.APIVersion, schemaValue); err != nil {
		return Snapshot{}, invalid("document_invalid", source.String(), "", err.Error())
	}
	wire, err := decodeSnapshotWire(envelope.APIVersion, data)
	if err != nil {
		return Snapshot{}, invalid("document_invalid", source.String(), "", "composition snapshot is invalid")
	}
	if wire.Kind != Kind {
		return Snapshot{}, invalid("kind_invalid", source.String(), "/kind", "composition snapshot kind is invalid")
	}
	canonical, err := canonicalize(wire)
	if err != nil || !bytes.Equal(canonical, data) {
		return Snapshot{}, invalid("canonical_order_invalid", source.String(), "", "composition snapshot is not canonical")
	}
	if err := validateSnapshot(wire, source.String()); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{state: &snapshotState{canonical: append([]byte(nil), canonical...)}}, nil
}

func decodeSnapshotWire(version string, data []byte) (wireDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if version == APIVersionV1 {
		var value wireDocumentV1
		if err := decoder.Decode(&value); err != nil {
			return wireDocument{}, err
		}
		if err := ensureSnapshotEOF(decoder); err != nil {
			return wireDocument{}, err
		}
		return value.document(), nil
	}
	var value wireDocumentV2
	if err := decoder.Decode(&value); err != nil {
		return wireDocument{}, err
	}
	if err := ensureSnapshotEOF(decoder); err != nil {
		return wireDocument{}, err
	}
	return value.document(), nil
}

func (s Snapshot) CanonicalJSON() ([]byte, error) {
	if s.state == nil {
		return nil, invalid("snapshot_invalid", "", "/snapshot", "composition snapshot is invalid")
	}
	return append([]byte(nil), s.state.canonical...), nil
}

func validateSnapshot(wire wireDocument, source string) error {
	if !serviceIDPattern.MatchString(wire.CoreServiceID) || module.CheckPath(wire.ConsumerModulePath) != nil {
		return invalid("build_options_invalid", source, "/coreServiceId", "composition build options are invalid")
	}
	sources := make([]provenance.Source, len(wire.Sources))
	sourceIndex := make(map[string]provenance.Source, len(wire.Sources))
	previous := ""
	for index, item := range wire.Sources {
		ref, refErr := provenance.ParseSourceRef(item.Ref)
		digest, digestErr := provenance.ParseDigest(item.Digest)
		if refErr != nil || digestErr != nil || previous != "" && item.Ref <= previous {
			return invalid("source_invalid", source, "/sources", "composition snapshot source is invalid")
		}
		previous = item.Ref
		sources[index] = provenance.Source{Ref: ref, Digest: digest}
		sourceIndex[item.Ref] = sources[index]
	}
	digest, err := api.ComputeSourceDigest(sources)
	if err != nil || digest.String() != wire.SourceDigest {
		return invalid("source_digest_mismatch", source, "/sourceDigest", "composition snapshot source digest does not match")
	}
	used := map[string]bool{}
	typeIndex := map[string]wireProjectedType{}
	if wire.APIVersion == APIVersionV2 {
		if wire.Types == nil {
			return invalid("type_closure_invalid", source, "/types", "composition projected type closure is missing")
		}
		var typeErr error
		typeIndex, typeErr = validateSnapshotTypes(*wire.Types, source, sourceIndex, used)
		if typeErr != nil {
			return typeErr
		}
	} else if wire.Types != nil {
		return invalid("type_closure_invalid", source, "/types", "composition v1 snapshot cannot contain projected types")
	}
	previous = ""
	tenantTypes := map[string]string{}
	for index, operation := range wire.Operations {
		pointer := fmt.Sprintf("/operations/%d", index)
		if previous != "" && operation.ID <= previous {
			return invalid("canonical_order_invalid", source, pointer+"/id", "composition operation order is invalid")
		}
		previous = operation.ID
		if err := validateSnapshotOperation(wire.APIVersion, operation, source, pointer, sourceIndex, used, typeIndex); err != nil {
			return err
		}
		for _, binding := range operation.ContextFields {
			if binding.Source != protocol.ContextTenantID {
				continue
			}
			if existing := tenantTypes[operation.ServiceID]; existing != "" && existing != binding.ValueType.Name {
				return invalid("tenant_context_type_mixed", source, pointer+"/contextFields", "tenant context type is inconsistent within the service")
			}
			tenantTypes[operation.ServiceID] = binding.ValueType.Name
		}
	}
	if err := validateProjectedTypeClosure(wire, typeIndex, source); err != nil {
		return err
	}
	if err := validateSnapshotHTTPAPI(wire, sourceIndex, source); err != nil {
		return err
	}
	if len(used) != len(sourceIndex) {
		return invalid("source_closure_invalid", source, "/sources", "composition snapshot source closure is invalid")
	}
	return nil
}

func validateSnapshotTypes(types []wireProjectedType, source string, sources map[string]provenance.Source, used map[string]bool) (map[string]wireProjectedType, error) {
	index := make(map[string]wireProjectedType, len(types))
	previous := ""
	for typeIndex, projected := range types {
		pointer := fmt.Sprintf("/types/%d", typeIndex)
		if previous != "" && projected.Name <= previous {
			return nil, invalid("canonical_order_invalid", source, pointer+"/name", "composition projected type order is invalid")
		}
		previous = projected.Name
		if !serviceIDPattern.MatchString(projected.ServiceID) || projected.MessageFullName == "" || projected.Name != exportedIdentifier(projected.ServiceID+"."+projected.MessageFullName) {
			return nil, invalid("projected_type_invalid", source, pointer, "composition projected type identity is invalid")
		}
		if _, duplicate := index[projected.Name]; duplicate {
			return nil, invalid("projected_type_collision", source, pointer+"/name", "composition projected type name is duplicated")
		}
		messageSource, ok := sources[projected.MessageSourceRef]
		if !ok || messageSource.Ref.Fragment() != "message:"+projected.MessageFullName {
			return nil, invalid("source_closure_invalid", source, pointer+"/messageSourceRef", "composition projected message source is invalid")
		}
		messageNode, messageErr := canonicalize(struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			FullName   string `json:"fullName"`
		}{APIVersion: "nexa.dev/proto-message-node/v1", Kind: "message", FullName: projected.MessageFullName})
		if messageErr != nil || messageSource.Digest != provenance.SHA256(messageNode) {
			return nil, invalid("projected_message_source_mismatch", source, pointer+"/messageSourceRef", "composition projected message source does not match its canonical Proto node")
		}
		bindingRef, ok := sourceRefForFragment(sources, "service:"+projected.ServiceID+"/binding:"+CapabilityID+"@"+CapabilityVersion)
		if !ok {
			return nil, invalid("source_closure_invalid", source, pointer+"/provenance", "composition projected type binding source is missing")
		}
		if err := validateExactRefs(projected.Provenance, []string{bindingRef, projected.MessageSourceRef}, source, pointer+"/provenance", sources, used); err != nil {
			return nil, err
		}
		previousNumber := 0
		for fieldIndex, field := range projected.Fields {
			fieldPointer := fmt.Sprintf("%s/fields/%d", pointer, fieldIndex)
			if field.Number <= previousNumber || field.ID != fmt.Sprintf("%s#%d", projected.MessageFullName, field.Number) {
				return nil, invalid("canonical_order_invalid", source, fieldPointer, "composition projected field order or identity is invalid")
			}
			previousNumber = field.Number
			fieldSource, fieldOK := sources[field.FieldSourceRef]
			if !fieldOK || fieldSource.Ref.Fragment() != "field:"+projected.MessageFullName+"."+field.ProtoName {
				return nil, invalid("source_closure_invalid", source, fieldPointer+"/fieldSourceRef", "composition projected field source is invalid")
			}
			if err := validateValue(field.ValueType); err != nil {
				return nil, invalid("value_type_invalid", source, fieldPointer+"/valueType", "composition projected field value type is invalid")
			}
			if err := validateExactRefs(field.Provenance, []string{bindingRef, projected.MessageSourceRef, field.FieldSourceRef}, source, fieldPointer+"/provenance", sources, used); err != nil {
				return nil, err
			}
		}
		index[projected.Name] = projected
	}
	for _, projected := range types {
		for _, field := range projected.Fields {
			if err := validateValueRefs(field.ValueType, projected.ServiceID, index); err != nil {
				return nil, invalid("type_closure_invalid", source, "/types", err.Error())
			}
			if !projectedFieldSourceMatches(projected, field, index, sources[field.FieldSourceRef]) {
				return nil, invalid("projected_field_source_mismatch", source, "/types", "composition projected field shape does not match its Proto source")
			}
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if visiting[name] {
			return invalid("message_graph_recursive", source, "/types", "composition projected message graph is recursive")
		}
		if visited[name] {
			return nil
		}
		visiting[name] = true
		for _, field := range index[name].Fields {
			for _, ref := range wireValueRefs(field.ValueType) {
				if err := visit(ref); err != nil {
					return err
				}
			}
		}
		delete(visiting, name)
		visited[name] = true
		return nil
	}
	for name := range index {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return index, nil
}

func projectedFieldSourceMatches(projected wireProjectedType, field wireProjectedField, types map[string]wireProjectedType, source provenance.Source) bool {
	base, cardinality, presence, ok := projectedFieldShape(field.ValueType, field.Required)
	if !ok || !protoRuntimeValueMatches(field.ProtoType, base, projected.ServiceID, types) {
		return false
	}
	if field.ProtoType.Kind == protocol.TypeMessage && cardinality == protocol.CardinalitySingular && presence != protocol.PresenceExplicit {
		return false
	}
	return protoFieldSourceMatches(projected.MessageFullName+"."+field.ProtoName, field.Number, field.JSONName, cardinality, presence, field.ProtoType, source)
}

func projectedFieldShape(value wireValue, required bool) (wireValue, protocol.Cardinality, protocol.Presence, bool) {
	switch value.Kind {
	case httpapi.ValueScalar, httpapi.ValueRef:
		return value, protocol.CardinalitySingular, protocol.PresenceImplicit, required
	case httpapi.ValueOptional:
		if required || value.Element == nil || !baseValue(*value.Element) {
			return wireValue{}, "", "", false
		}
		return *value.Element, protocol.CardinalitySingular, protocol.PresenceExplicit, true
	case httpapi.ValueArray:
		if !required || value.Element == nil || !baseValue(*value.Element) {
			return wireValue{}, "", "", false
		}
		return *value.Element, protocol.CardinalityRepeated, protocol.PresenceImplicit, true
	default:
		return wireValue{}, "", "", false
	}
}

func baseValue(value wireValue) bool {
	return (value.Kind == httpapi.ValueScalar || value.Kind == httpapi.ValueRef) && value.Name != "" && value.Element == nil
}

func protoRuntimeValueMatches(protoType wireProtoType, value wireValue, serviceID string, types map[string]wireProjectedType) bool {
	if protoType.Name == "" || !baseValue(value) {
		return false
	}
	switch protoType.Kind {
	case protocol.TypeScalar:
		name, ok := projectedScalarName(protoType.Name)
		return ok && value.Kind == httpapi.ValueScalar && value.Name == name
	case protocol.TypeEnum:
		return value.Kind == httpapi.ValueScalar && value.Name == "int32"
	case protocol.TypeMessage:
		projected, ok := types[value.Name]
		return ok && value.Kind == httpapi.ValueRef && projected.ServiceID == serviceID && projected.MessageFullName == protoType.Name
	default:
		return false
	}
}

func projectedScalarName(name string) (string, bool) {
	switch name {
	case "double":
		return "float64", true
	case "float":
		return "float32", true
	case "sint32", "sfixed32":
		return "int32", true
	case "sint64", "sfixed64":
		return "int64", true
	case "fixed32":
		return "uint32", true
	case "fixed64":
		return "uint64", true
	case "bool", "string", "bytes", "int32", "int64", "uint32", "uint64":
		return name, true
	default:
		return "", false
	}
}

func protoFieldSourceMatches(fullName string, number int, jsonName string, cardinality protocol.Cardinality, presence protocol.Presence, protoType wireProtoType, source provenance.Source) bool {
	node := map[string]any{
		"apiVersion":  "nexa.dev/proto-field-node/v1",
		"kind":        "field",
		"fullName":    fullName,
		"number":      number,
		"jsonName":    jsonName,
		"cardinality": cardinality,
		"presence":    presence,
		"type":        map[string]any{"kind": protoType.Kind, "name": protoType.Name},
	}
	encoded, err := json.Marshal(node)
	if err != nil {
		return false
	}
	canonical, err := jcs.Transform(encoded)
	return err == nil && provenance.SHA256(canonical) == source.Digest
}

func validateProjectedTypeClosure(wire wireDocument, types map[string]wireProjectedType, source string) error {
	if wire.APIVersion == APIVersionV1 {
		return nil
	}
	reachable := map[string]bool{}
	var visit func(wireValue)
	visit = func(value wireValue) {
		if value.Kind == httpapi.ValueRef && !reachable[value.Name] {
			reachable[value.Name] = true
			for _, field := range types[value.Name].Fields {
				visit(field.ValueType)
			}
		}
		if value.Element != nil {
			visit(*value.Element)
		}
	}
	for _, operation := range wire.Operations {
		for _, field := range operation.RequestFields {
			visit(field.ValueType)
		}
		for _, field := range operation.ResponseFields {
			visit(field.ValueType)
		}
	}
	if len(reachable) != len(types) {
		return invalid("type_closure_invalid", source, "/types", "composition projected type closure contains unreachable types")
	}
	return nil
}

func validateSnapshotHTTPAPI(wire wireDocument, sources map[string]provenance.Source, source string) error {
	spec := httpapi.GeneratedDocumentSpec{}
	if wire.Types != nil {
		for _, projected := range *wire.Types {
			owner, err := snapshotProvenance(projected.Provenance, sources)
			if err != nil {
				return invalid("provenance_invalid", source, "/types", "composition projected type provenance is invalid")
			}
			value := httpapi.GeneratedTypeSpec{Name: projected.Name, Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: owner}
			for _, field := range projected.Fields {
				fieldOwner, ownerErr := snapshotProvenance(field.Provenance, sources)
				if ownerErr != nil {
					return invalid("provenance_invalid", source, "/types/fields", "composition projected field provenance is invalid")
				}
				value.Fields = append(value.Fields, httpapi.GeneratedFieldSpec{Path: []string{exportedIdentifier(field.JSONName)}, Required: field.Required, ValueType: snapshotValue(field.ValueType), Provenance: fieldOwner})
			}
			spec.Types = append(spec.Types, value)
		}
	}
	for _, operation := range wire.Operations {
		requestOwner, err := snapshotProvenance(operation.RequestProvenance, sources)
		if err != nil {
			return invalid("provenance_invalid", source, "/operations", "composition request provenance is invalid")
		}
		responseOwner, err := snapshotProvenance(operation.ResponseProvenance, sources)
		if err != nil {
			return invalid("provenance_invalid", source, "/operations", "composition response provenance is invalid")
		}
		operationOwner, err := snapshotProvenance(operation.OperationProvenance, sources)
		if err != nil {
			return invalid("provenance_invalid", source, "/operations", "composition operation provenance is invalid")
		}
		request := httpapi.GeneratedTypeSpec{Name: operation.RequestType, Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: requestOwner}
		for _, binding := range operation.RequestFields {
			owner, ownerErr := snapshotProvenance(binding.Provenance, sources)
			if ownerErr != nil {
				return invalid("provenance_invalid", source, "/operations/requestFields", "composition request field provenance is invalid")
			}
			location := api.RequestBindingBody
			if pathBinding(operation.Path, binding.HTTPField) {
				location = api.RequestBindingPath
			} else if operation.Method == protocol.MethodGET || operation.Method == protocol.MethodDELETE {
				location = api.RequestBindingQuery
			}
			request.Fields = append(request.Fields, httpapi.GeneratedFieldSpec{Path: []string{exportedIdentifier(binding.HTTPField)}, Required: binding.Required, ValueType: snapshotValue(binding.ValueType), Binding: &httpapi.BindingSpec{Location: location, Name: binding.HTTPField}, Provenance: owner})
		}
		response := httpapi.GeneratedTypeSpec{Name: operation.ResponseType, Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: responseOwner}
		for _, binding := range operation.ResponseFields {
			owner, ownerErr := snapshotProvenance(binding.Provenance, sources)
			if ownerErr != nil {
				return invalid("provenance_invalid", source, "/operations/responseFields", "composition response field provenance is invalid")
			}
			response.Fields = append(response.Fields, httpapi.GeneratedFieldSpec{Path: []string{exportedIdentifier(binding.HTTPField)}, Required: binding.Required, ValueType: snapshotValue(binding.ValueType), Provenance: owner})
		}
		auth := httpapi.AuthSpec{Mode: api.AuthMode(operation.Auth.Mode)}
		for _, credential := range operation.Auth.Credentials {
			auth.Credentials = append(auth.Credentials, httpapi.CredentialSpec{ID: credential.ID, Type: api.CredentialType(credential.Type), Location: api.CredentialLocation(credential.Location), Name: credential.Name})
		}
		projections := make([]api.ErrorProjectionSpec, len(operation.Errors))
		for index, projection := range operation.Errors {
			projections[index] = api.ErrorProjectionSpec{Match: api.ErrorMatchSpec{Domain: projection.Match.Domain, Code: projection.Match.Code}, Project: api.ErrorTargetSpec{Domain: projection.Project.Domain, Code: projection.Project.Code, HTTPStatus: projection.Project.HTTPStatus}}
		}
		spec.Types = append(spec.Types, request, response)
		spec.Operations = append(spec.Operations, httpapi.GeneratedOperationSpec{ID: operation.ID, Method: apiMethod(operation.Method), Path: operation.Path, RequestType: operation.RequestType, ResponseBody: api.ResponseBodyJSON, ResponseType: operation.ResponseType, Auth: auth, Permission: operation.Permission, Capability: &httpapi.CapabilitySpec{ID: CapabilityID, APIVersion: CapabilityVersion}, ErrorProjections: projections, Provenance: operationOwner})
	}
	generated, err := httpapi.NewGeneratedDocument(spec)
	if err != nil {
		return invalid("generated_api_invalid", source, "/operations", "composition snapshot HTTP API projection is invalid")
	}
	manifestSpec, err := httpapi.ManifestSpec(generated)
	if err != nil {
		return invalid("generated_api_invalid", source, "/operations", "composition snapshot HTTP API manifest projection is invalid")
	}
	if _, err := api.NewManifest(manifestSpec); err != nil {
		return invalid("generated_api_invalid", source, "/operations", "composition snapshot HTTP API semantics are invalid")
	}
	return nil
}

func snapshotProvenance(value wireProvenance, sources map[string]provenance.Source) (httpapi.NodeProvenance, error) {
	items := make([]provenance.Source, len(value.Sources))
	for index, ref := range value.Sources {
		items[index] = sources[ref]
	}
	return httpapi.NewGeneratedProvenance(items)
}

func snapshotValue(value wireValue) httpapi.ValueTypeSpec {
	result := httpapi.ValueTypeSpec{Kind: value.Kind, Name: value.Name}
	if value.Element != nil {
		item := snapshotValue(*value.Element)
		result.Element = &item
	}
	return result
}

func validateSnapshotOperation(version string, operation wireOperation, source, pointer string, sources map[string]provenance.Source, used map[string]bool, types map[string]wireProjectedType) error {
	if operation.ID == "" || !serviceIDPattern.MatchString(operation.ServiceID) || operation.RequestType != exportedIdentifier(operation.ID)+"Request" || operation.ResponseType != exportedIdentifier(operation.ID)+"Response" || operation.MethodFullName == "" || operation.InputName == "" || operation.OutputName == "" {
		return invalid("operation_invalid", source, pointer, "composition operation is invalid")
	}
	methodRef, ok := sourceRefForFragment(sources, "method:"+operation.MethodFullName)
	if !ok {
		return invalid("source_closure_invalid", source, pointer+"/operationProvenance", "composition method source is missing")
	}
	bindingFragment := "service:" + operation.ServiceID + "/binding:" + CapabilityID + "@" + CapabilityVersion
	bindingRef, ok := sourceRefForFragment(sources, bindingFragment)
	if !ok {
		return invalid("source_closure_invalid", source, pointer+"/operationProvenance", "composition capability binding source is missing")
	}
	if err := validateExactRefs(operation.OperationProvenance, []string{methodRef, bindingRef}, source, pointer+"/operationProvenance", sources, used); err != nil {
		return err
	}
	requestMessageRef, ok := sourceRefForFragment(sources, "message:"+operation.InputName)
	if !ok {
		return invalid("source_closure_invalid", source, pointer+"/requestProvenance", "composition request message source is missing")
	}
	responseMessageRef, ok := sourceRefForFragment(sources, "message:"+operation.OutputName)
	if !ok {
		return invalid("source_closure_invalid", source, pointer+"/responseProvenance", "composition response message source is missing")
	}
	if err := validateExactRefs(operation.RequestProvenance, []string{methodRef, bindingRef, requestMessageRef}, source, pointer+"/requestProvenance", sources, used); err != nil {
		return err
	}
	if err := validateExactRefs(operation.ResponseProvenance, []string{methodRef, bindingRef, responseMessageRef}, source, pointer+"/responseProvenance", sources, used); err != nil {
		return err
	}
	for index := range operation.Auth.Credentials {
		if index > 0 && operation.Auth.Credentials[index-1].ID >= operation.Auth.Credentials[index].ID {
			return invalid("canonical_order_invalid", source, pointer+"/auth/credentials", "composition credential order is invalid")
		}
	}
	requestDestinations, requestFields := map[string]bool{}, map[string]bool{}
	for index, binding := range operation.RequestFields {
		itemPointer := fmt.Sprintf("%s/requestFields/%d", pointer, index)
		if index > 0 && fieldBindingKey(operation.RequestFields[index-1]) >= fieldBindingKey(binding) {
			return invalid("canonical_order_invalid", source, itemPointer, "composition request binding order is invalid")
		}
		if err := validateFieldBinding(version, binding, operation.InputName, operation.ServiceID, methodRef, bindingRef, source, itemPointer, sources, used, types); err != nil {
			return err
		}
		key := pathKey(binding.RPCPath)
		if requestDestinations[key] || requestFields[binding.HTTPField] {
			return invalid("many_to_one_mapping", source, itemPointer, "composition request binding is duplicated")
		}
		requestDestinations[key], requestFields[binding.HTTPField] = true, true
	}
	contextSources := map[protocol.ContextValue]bool{}
	for index, binding := range operation.ContextFields {
		itemPointer := fmt.Sprintf("%s/contextFields/%d", pointer, index)
		if index > 0 && contextBindingKey(operation.ContextFields[index-1]) >= contextBindingKey(binding) {
			return invalid("canonical_order_invalid", source, itemPointer, "composition context binding order is invalid")
		}
		if err := validatePath(version, binding.RPCPath, operation.InputName, source, itemPointer+"/rpcPath", sources, used); err != nil {
			return err
		}
		if err := validateValue(binding.ValueType); err != nil {
			return invalid("value_type_invalid", source, itemPointer+"/valueType", "composition value type is invalid")
		}
		valueType := snapshotValue(binding.ValueType)
		if !validContextValueType(binding.Source, valueType) {
			return invalid("context_binding_type_invalid", source, itemPointer+"/valueType", "composition context binding type is invalid")
		}
		if version == APIVersionV2 {
			terminal := binding.RPCPath[len(binding.RPCPath)-1]
			if terminal.Cardinality != protocol.CardinalitySingular || terminal.Presence != protocol.PresenceImplicit || terminal.TypeKind != protocol.TypeScalar || !binding.Required || !terminalValueMatches(terminal, binding.ValueType, binding.Required, operation.ServiceID, types) {
				return invalid("context_binding_type_invalid", source, itemPointer+"/valueType", "composition context binding witness is invalid")
			}
		}
		leafRef := binding.RPCPath[len(binding.RPCPath)-1].SourceRef
		if err := validateExactRefs(binding.Provenance, []string{methodRef, bindingRef, leafRef}, source, itemPointer+"/provenance", sources, used); err != nil {
			return err
		}
		key := pathKey(binding.RPCPath)
		if requestDestinations[key] || contextSources[binding.Source] {
			return invalid("many_to_one_mapping", source, itemPointer, "composition context binding is duplicated")
		}
		requestDestinations[key], contextSources[binding.Source] = true, true
	}
	responseDestinations, responseFields := map[string]bool{}, map[string]bool{}
	for index, binding := range operation.ResponseFields {
		itemPointer := fmt.Sprintf("%s/responseFields/%d", pointer, index)
		if index > 0 && fieldBindingKey(operation.ResponseFields[index-1]) >= fieldBindingKey(binding) {
			return invalid("canonical_order_invalid", source, itemPointer, "composition response binding order is invalid")
		}
		if err := validateFieldBinding(version, binding, operation.OutputName, operation.ServiceID, methodRef, bindingRef, source, itemPointer, sources, used, types); err != nil {
			return err
		}
		key := pathKey(binding.RPCPath)
		if responseDestinations[key] || responseFields[binding.HTTPField] {
			return invalid("many_to_one_mapping", source, itemPointer, "composition response binding is duplicated")
		}
		responseDestinations[key], responseFields[binding.HTTPField] = true, true
	}
	for index, projection := range operation.Errors {
		if index > 0 && errorKey(operation.Errors[index-1]) >= errorKey(projection) {
			return invalid("canonical_order_invalid", source, pointer+"/errors", "composition error projection order is invalid")
		}
	}
	if len(operation.Errors) > 0 && (!contextSources[protocol.ContextRequestID] || !contextSources[protocol.ContextTraceID]) {
		return invalid("error_context_binding_missing", source, pointer+"/contextFields", "error projection requires request-id and trace-id context bindings")
	}
	return nil
}

func validateFieldBinding(version string, binding wireFieldBinding, owner, serviceID, methodRef, bindingRef, source, pointer string, sources map[string]provenance.Source, used map[string]bool, types map[string]wireProjectedType) error {
	if binding.HTTPField == "" {
		return invalid("field_binding_invalid", source, pointer+"/httpField", "composition field binding is invalid")
	}
	if err := validatePath(version, binding.RPCPath, owner, source, pointer+"/rpcPath", sources, used); err != nil {
		return err
	}
	if err := validateValue(binding.ValueType); err != nil {
		return invalid("value_type_invalid", source, pointer+"/valueType", "composition value type is invalid")
	}
	if err := validateValueRefs(binding.ValueType, serviceID, types); err != nil {
		return invalid("type_closure_invalid", source, pointer+"/valueType", err.Error())
	}
	terminal := binding.RPCPath[len(binding.RPCPath)-1]
	if version == APIVersionV2 && !terminalValueMatches(terminal, binding.ValueType, binding.Required, serviceID, types) {
		return invalid("field_binding_type_mismatch", source, pointer+"/valueType", "composition field binding does not match its terminal Proto witness")
	}
	if (terminal.TypeKind == protocol.TypeMessage || binding.ValueType.Kind == httpapi.ValueArray) && !binding.Required {
		return invalid("field_binding_invalid", source, pointer+"/required", "composition collection and object bindings are required")
	}
	leafRef := binding.RPCPath[len(binding.RPCPath)-1].SourceRef
	return validateExactRefs(binding.Provenance, []string{methodRef, bindingRef, leafRef}, source, pointer+"/provenance", sources, used)
}

func validatePath(version string, path []wirePathSegment, root, source, pointer string, sources map[string]provenance.Source, used map[string]bool) error {
	if len(path) == 0 {
		return invalid("field_path_invalid", source, pointer, "composition typed path is empty")
	}
	expectedOwner := root
	for index, segment := range path {
		nameIndex := strings.LastIndex(segment.FullName, ".")
		if nameIndex <= 0 {
			return invalid("field_path_invalid", source, pointer, "composition typed path is invalid")
		}
		owner := segment.FullName[:nameIndex]
		if segment.ID != fmt.Sprintf("%s#%d", owner, segment.Number) || owner != expectedOwner {
			return invalid("field_path_invalid", source, pointer, "composition typed path is invalid")
		}
		if version == APIVersionV2 {
			if !validPathWitness(segment) {
				return invalid("field_path_invalid", source, pointer, "composition typed path witness is invalid")
			}
			fieldSource, ok := sources[segment.SourceRef]
			if !ok || !protoFieldSourceMatches(segment.FullName, segment.Number, segment.JSONName, segment.Cardinality, segment.Presence, wireProtoType{Kind: segment.TypeKind, Name: segment.TypeName}, fieldSource) {
				return invalid("field_path_source_mismatch", source, pointer, "composition typed path witness does not match its Proto source")
			}
		}
		if index+1 < len(path) {
			if segment.TypeKind != protocol.TypeMessage || segment.TypeName == "" || version == APIVersionV2 && (segment.Cardinality != protocol.CardinalitySingular || segment.Presence != protocol.PresenceExplicit) {
				return invalid("field_path_invalid", source, pointer, "composition typed path is not message-connected")
			}
			expectedOwner = segment.TypeName
		} else if segment.TypeKind != protocol.TypeScalar && segment.TypeKind != protocol.TypeEnum && segment.TypeKind != protocol.TypeMessage {
			return invalid("field_path_invalid", source, pointer, "composition typed path does not end at a value field")
		}
		value, ok := sources[segment.SourceRef]
		if !ok || value.Ref.Fragment() != "field:"+segment.FullName {
			return invalid("source_closure_invalid", source, pointer, "composition typed path source is invalid")
		}
		used[segment.SourceRef] = true
	}
	return nil
}

func validPathWitness(segment wirePathSegment) bool {
	if segment.JSONName == "" || segment.TypeName == "" {
		return false
	}
	if segment.TypeKind != protocol.TypeScalar && segment.TypeKind != protocol.TypeEnum && segment.TypeKind != protocol.TypeMessage {
		return false
	}
	switch segment.Cardinality {
	case protocol.CardinalityRepeated:
		return segment.Presence == protocol.PresenceImplicit
	case protocol.CardinalitySingular:
		if segment.Presence != protocol.PresenceImplicit && segment.Presence != protocol.PresenceExplicit {
			return false
		}
		return segment.TypeKind != protocol.TypeMessage || segment.Presence == protocol.PresenceExplicit
	default:
		return false
	}
}

func terminalValueMatches(terminal wirePathSegment, value wireValue, required bool, serviceID string, types map[string]wireProjectedType) bool {
	if !validPathWitness(terminal) {
		return false
	}
	base := value
	switch {
	case terminal.Cardinality == protocol.CardinalityRepeated:
		if !required || value.Kind != httpapi.ValueArray || value.Element == nil || !baseValue(*value.Element) {
			return false
		}
		base = *value.Element
	case terminal.TypeKind == protocol.TypeMessage:
		if !required || !baseValue(value) {
			return false
		}
	case terminal.Presence == protocol.PresenceExplicit:
		if required || value.Kind != httpapi.ValueOptional || value.Element == nil || !baseValue(*value.Element) {
			return false
		}
		base = *value.Element
	default:
		if !required || !baseValue(value) {
			return false
		}
	}
	return protoRuntimeValueMatches(wireProtoType{Kind: terminal.TypeKind, Name: terminal.TypeName}, base, serviceID, types)
}

func validateExactRefs(value wireProvenance, expected []string, source, pointer string, sources map[string]provenance.Source, used map[string]bool) error {
	sort.Strings(expected)
	if len(value.Sources) != len(expected) || !sort.StringsAreSorted(value.Sources) {
		return invalid("provenance_invalid", source, pointer, "composition provenance is invalid")
	}
	for index, ref := range value.Sources {
		if index > 0 && value.Sources[index-1] == ref || ref != expected[index] {
			return invalid("provenance_invalid", source, pointer, "composition provenance is invalid")
		}
		if _, ok := sources[ref]; !ok {
			return invalid("source_closure_invalid", source, pointer, "composition provenance source is missing")
		}
		used[ref] = true
	}
	return nil
}

func sourceRefForFragment(sources map[string]provenance.Source, fragment string) (string, bool) {
	result := ""
	for ref, source := range sources {
		if source.Ref.Fragment() != fragment {
			continue
		}
		if result != "" {
			return "", false
		}
		result = ref
	}
	return result, result != ""
}

func validateValue(value wireValue) error {
	switch value.Kind {
	case httpapi.ValueScalar, httpapi.ValueRef:
		if value.Name == "" || value.Element != nil {
			return fmt.Errorf("invalid named value")
		}
	case httpapi.ValueArray, httpapi.ValueOptional:
		if value.Name != "" || value.Element == nil {
			return fmt.Errorf("invalid wrapper")
		}
		return validateValue(*value.Element)
	default:
		return fmt.Errorf("unsupported value")
	}
	return nil
}

func validateValueRefs(value wireValue, serviceID string, types map[string]wireProjectedType) error {
	if value.Kind == httpapi.ValueRef {
		projected, ok := types[value.Name]
		if !ok {
			return fmt.Errorf("composition projected value reference is missing")
		}
		if projected.ServiceID != serviceID {
			return fmt.Errorf("composition projected value reference crosses service ownership")
		}
	}
	if value.Element != nil {
		return validateValueRefs(*value.Element, serviceID, types)
	}
	return nil
}

func wireValueRefs(value wireValue) []string {
	if value.Kind == httpapi.ValueRef {
		return []string{value.Name}
	}
	if value.Element != nil {
		return wireValueRefs(*value.Element)
	}
	return nil
}

func ensureSnapshotEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else {
		return err
	}
}

package composition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

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
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire wireDocument
	if err := decoder.Decode(&wire); err != nil {
		return Snapshot{}, invalid("document_invalid", source.String(), "", "composition snapshot is invalid")
	}
	if err := ensureSnapshotEOF(decoder); err != nil {
		return Snapshot{}, invalid("document_invalid", source.String(), "", "composition snapshot is invalid")
	}
	var schemaValue any
	if err := json.Unmarshal(data, &schemaValue); err != nil {
		return Snapshot{}, invalid("document_invalid", source.String(), "", "composition snapshot schema is invalid")
	}
	if err := validateSnapshotSchema(schemaValue); err != nil {
		return Snapshot{}, invalid("document_invalid", source.String(), "", err.Error())
	}
	if wire.APIVersion != APIVersion {
		return Snapshot{}, invalid("version_unsupported", source.String(), "/apiVersion", "composition snapshot version is unsupported")
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
	previous = ""
	for index, operation := range wire.Operations {
		pointer := fmt.Sprintf("/operations/%d", index)
		if previous != "" && operation.ID <= previous {
			return invalid("canonical_order_invalid", source, pointer+"/id", "composition operation order is invalid")
		}
		previous = operation.ID
		if err := validateSnapshotOperation(operation, source, pointer, sourceIndex, used); err != nil {
			return err
		}
	}
	if err := validateSnapshotHTTPAPI(wire, sourceIndex, source); err != nil {
		return err
	}
	if len(used) != len(sourceIndex) {
		return invalid("source_closure_invalid", source, "/sources", "composition snapshot source closure is invalid")
	}
	return nil
}

func validateSnapshotHTTPAPI(wire wireDocument, sources map[string]provenance.Source, source string) error {
	spec := httpapi.GeneratedDocumentSpec{}
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

func validateSnapshotOperation(operation wireOperation, source, pointer string, sources map[string]provenance.Source, used map[string]bool) error {
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
		if err := validateFieldBinding(binding, operation.InputName, methodRef, bindingRef, source, itemPointer, sources, used); err != nil {
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
		if err := validatePath(binding.RPCPath, operation.InputName, source, itemPointer+"/rpcPath", sources, used); err != nil {
			return err
		}
		if err := validateValue(binding.ValueType); err != nil {
			return invalid("value_type_invalid", source, itemPointer+"/valueType", "composition value type is invalid")
		}
		expected := "string"
		if binding.Source == protocol.ContextTenantID {
			expected = "int64"
		}
		if binding.ValueType.Kind != httpapi.ValueScalar || binding.ValueType.Name != expected {
			return invalid("context_binding_type_invalid", source, itemPointer+"/valueType", "composition context binding type is invalid")
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
		if err := validateFieldBinding(binding, operation.OutputName, methodRef, bindingRef, source, itemPointer, sources, used); err != nil {
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

func validateFieldBinding(binding wireFieldBinding, owner, methodRef, bindingRef, source, pointer string, sources map[string]provenance.Source, used map[string]bool) error {
	if binding.HTTPField == "" {
		return invalid("field_binding_invalid", source, pointer+"/httpField", "composition field binding is invalid")
	}
	if err := validatePath(binding.RPCPath, owner, source, pointer+"/rpcPath", sources, used); err != nil {
		return err
	}
	if err := validateValue(binding.ValueType); err != nil {
		return invalid("value_type_invalid", source, pointer+"/valueType", "composition value type is invalid")
	}
	leafRef := binding.RPCPath[len(binding.RPCPath)-1].SourceRef
	return validateExactRefs(binding.Provenance, []string{methodRef, bindingRef, leafRef}, source, pointer+"/provenance", sources, used)
}

func validatePath(path []wirePathSegment, root, source, pointer string, sources map[string]provenance.Source, used map[string]bool) error {
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
		if index+1 < len(path) {
			if segment.TypeKind != protocol.TypeMessage || segment.TypeName == "" {
				return invalid("field_path_invalid", source, pointer, "composition typed path is not message-connected")
			}
			expectedOwner = segment.TypeName
		} else if segment.TypeKind != protocol.TypeScalar && segment.TypeKind != protocol.TypeEnum {
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
	case httpapi.ValueScalar:
		if value.Name == "" || value.Element != nil {
			return fmt.Errorf("invalid scalar")
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

func ensureSnapshotEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else {
		return err
	}
}

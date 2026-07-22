package crudbuild

import (
	"bytes"

	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

func ParseSnapshot(source provenance.DomainSource, data []byte) (Snapshot, error) {
	if !validateIRCollectionPresence(data) {
		return Snapshot{}, lockError("document_required_missing", "", source.String())
	}
	var wire wireDocument
	if err := strictdoc.DecodeJSON(source.String(), data, &wire); err != nil {
		if owner, ok := err.(*strictdoc.Error); ok {
			return Snapshot{}, lockError(owner.Code, owner.Pointer, source.String())
		}
		return Snapshot{}, lockError("document_invalid", "", source.String())
	}
	if wire.APIVersion != APIVersion {
		return Snapshot{}, lockError("version_unsupported", "/apiVersion", source.String())
	}
	if wire.Kind != Kind {
		return Snapshot{}, lockError("kind_invalid", "/kind", source.String())
	}
	if !serviceIDPattern.MatchString(wire.ServiceID) || !protoPackagePattern.MatchString(wire.ProtoPackage) || !goPackagePattern.MatchString(wire.GoPackage) {
		return Snapshot{}, lockError("document_invalid", "", source.String())
	}
	state := &documentState{serviceID: wire.ServiceID, protoPackage: wire.ProtoPackage, goPackage: wire.GoPackage, imports: append([]string(nil), wire.Imports...), enums: []*enumState{}, messages: []*messageState{}, services: []*serviceState{}, tenantEntityIDs: append([]string(nil), wire.TenantEntities...), sources: []provenance.Source{}}
	if !strictlySortedStrings(state.tenantEntityIDs) && len(state.tenantEntityIDs) > 1 {
		return Snapshot{}, lockError("canonical_order_invalid", "/tenantEntities", source.String())
	}
	for index, value := range state.tenantEntityIDs {
		if value == "" {
			return Snapshot{}, lockError("document_invalid", "/tenantEntities/"+itoa(index), source.String())
		}
	}
	if !strictlySortedStrings(state.imports) && len(state.imports) > 1 {
		return Snapshot{}, lockError("canonical_order_invalid", "/imports", source.String())
	}
	importSet := make(map[string]struct{}, len(state.imports))
	for importIndex, value := range state.imports {
		switch value {
		case "google/protobuf/field_mask.proto", "google/protobuf/struct.proto", "google/protobuf/timestamp.proto", "nexa/protocol/v1/options.proto":
			importSet[value] = struct{}{}
		default:
			return Snapshot{}, lockError("document_invalid", "/imports/"+itoa(importIndex), source.String())
		}
	}
	sourceSet := make(map[string]provenance.Source, len(wire.Sources))
	for sourceIndex, sourceWire := range wire.Sources {
		if sourceIndex > 0 && sourceWire.Ref <= wire.Sources[sourceIndex-1].Ref {
			return Snapshot{}, lockError("canonical_order_invalid", "/sources/"+itoa(sourceIndex), source.String())
		}
		ref, err := provenance.ParseSourceRef(sourceWire.Ref)
		if err != nil {
			return Snapshot{}, lockError("source_ref_invalid", "/sources/"+itoa(sourceIndex)+"/ref", source.String())
		}
		digest, err := provenance.ParseDigest(sourceWire.Digest)
		if err != nil {
			return Snapshot{}, lockError("source_digest_invalid", "/sources/"+itoa(sourceIndex)+"/digest", source.String())
		}
		value := provenance.Source{Ref: ref, Digest: digest}
		state.sources = append(state.sources, value)
		sourceSet[ref.String()] = value
	}
	typeNames, globalSymbols := map[string]struct{}{}, map[string]struct{}{}
	for _, item := range wire.Enums {
		if _, duplicate := globalSymbols[item.Name]; duplicate {
			return Snapshot{}, lockError("history_duplicate", "/enums", source.String())
		}
		typeNames[item.Name], globalSymbols[item.Name] = struct{}{}, struct{}{}
	}
	for _, item := range wire.Messages {
		if _, duplicate := globalSymbols[item.Name]; duplicate {
			return Snapshot{}, lockError("history_duplicate", "/messages", source.String())
		}
		typeNames[item.Name], globalSymbols[item.Name] = struct{}{}, struct{}{}
	}
	seenEnumIDs, seenEnumNames := map[string]struct{}{}, map[string]struct{}{}
	for enumIndex, enumWire := range wire.Enums {
		if enumIndex > 0 && enumWire.Name <= wire.Enums[enumIndex-1].Name {
			return Snapshot{}, lockError("canonical_order_invalid", "/enums/"+itoa(enumIndex), source.String())
		}
		if enumWire.ID == "" || !protoSymbolPattern.MatchString(enumWire.Name) {
			return Snapshot{}, lockError("wire_name_invalid", "/enums/"+itoa(enumIndex), source.String())
		}
		if _, duplicate := seenEnumIDs[enumWire.ID]; duplicate {
			return Snapshot{}, lockError("history_duplicate", "/enums/"+itoa(enumIndex)+"/id", source.String())
		}
		if _, duplicate := seenEnumNames[enumWire.Name]; duplicate {
			return Snapshot{}, lockError("history_duplicate", "/enums/"+itoa(enumIndex)+"/name", source.String())
		}
		seenEnumIDs[enumWire.ID], seenEnumNames[enumWire.Name] = struct{}{}, struct{}{}
		enum := &enumState{id: enumWire.ID, name: enumWire.Name, values: []*enumValueState{}, reservedNames: append([]string(nil), enumWire.ReservedNames...), reservedNumbers: append([]int32(nil), enumWire.ReservedNumbers...)}
		if !strictlySortedStrings(enum.reservedNames) && len(enum.reservedNames) > 1 || !strictlySortedNumbers(enum.reservedNumbers) {
			return Snapshot{}, lockError("canonical_order_invalid", "/enums/"+itoa(enumIndex)+"/reservedNames", source.String())
		}
		for reservedIndex, value := range enum.reservedNames {
			if !protoSymbolPattern.MatchString(value) {
				return Snapshot{}, lockError("reservation_invalid", "/enums/"+itoa(enumIndex)+"/reservedNames/"+itoa(reservedIndex), source.String())
			}
		}
		for reservedIndex, value := range enum.reservedNumbers {
			if !legalEnumNumber(value) {
				return Snapshot{}, lockError("reservation_invalid", "/enums/"+itoa(enumIndex)+"/reservedNumbers/"+itoa(reservedIndex), source.String())
			}
		}
		seenValues := map[string]struct{}{}
		previousNumber := int32(-1)
		for valueIndex, value := range enumWire.Values {
			if value.ID == "" || !protoSymbolPattern.MatchString(value.Name) || !legalEnumNumber(value.Number) || value.Number <= previousNumber {
				return Snapshot{}, lockError("wire_type_invalid", "/enums/"+itoa(enumIndex)+"/values/"+itoa(valueIndex), source.String())
			}
			previousNumber = value.Number
			if _, duplicate := seenValues[value.Name]; duplicate {
				return Snapshot{}, lockError("history_duplicate", "/enums/"+itoa(enumIndex)+"/values/"+itoa(valueIndex)+"/name", source.String())
			}
			if containsName(enum.reservedNames, value.Name) || containsNumber(enum.reservedNumbers, value.Number) {
				return Snapshot{}, lockError("reservation_invalid", "/enums/"+itoa(enumIndex)+"/values/"+itoa(valueIndex), source.String())
			}
			seenValues[value.Name] = struct{}{}
			enum.values = append(enum.values, &enumValueState{id: value.ID, name: value.Name, number: value.Number})
		}
		if len(enum.values) == 0 || enum.values[0].number != 0 {
			return Snapshot{}, lockError("wire_type_invalid", "/enums/"+itoa(enumIndex)+"/values", source.String())
		}
		state.enums = append(state.enums, enum)
	}
	usedSources := map[string]provenance.Source{}
	seenMessageIDs, seenMessageNames := map[string]struct{}{}, map[string]struct{}{}
	for messageIndex, messageWire := range wire.Messages {
		if messageIndex > 0 && messageWire.Name <= wire.Messages[messageIndex-1].Name {
			return Snapshot{}, lockError("canonical_order_invalid", "/messages/"+itoa(messageIndex), source.String())
		}
		messagePointer := "/messages/" + itoa(messageIndex)
		if messageWire.ID == "" || !protoSymbolPattern.MatchString(messageWire.Name) {
			return Snapshot{}, lockError("wire_name_invalid", messagePointer+"/name", source.String())
		}
		if _, duplicate := seenMessageIDs[messageWire.ID]; duplicate {
			return Snapshot{}, lockError("history_duplicate", messagePointer+"/id", source.String())
		}
		if _, duplicate := seenMessageNames[messageWire.Name]; duplicate {
			return Snapshot{}, lockError("history_duplicate", messagePointer+"/name", source.String())
		}
		seenMessageIDs[messageWire.ID], seenMessageNames[messageWire.Name] = struct{}{}, struct{}{}
		message := &messageState{id: messageWire.ID, name: messageWire.Name, reservedNames: append([]string(nil), messageWire.ReservedNames...), reservedNumbers: append([]int32(nil), messageWire.ReservedNumbers...)}
		if !strictlySortedStrings(message.reservedNames) && len(message.reservedNames) > 1 || !strictlySortedNumbers(message.reservedNumbers) {
			return Snapshot{}, lockError("canonical_order_invalid", messagePointer+"/reservedNames", source.String())
		}
		for reservedIndex, value := range message.reservedNames {
			if !protoFieldPattern.MatchString(value) {
				return Snapshot{}, lockError("reservation_invalid", messagePointer+"/reservedNames/"+itoa(reservedIndex), source.String())
			}
		}
		for reservedIndex, value := range message.reservedNumbers {
			if !legalNumber(value) {
				return Snapshot{}, lockError("reservation_invalid", messagePointer+"/reservedNumbers/"+itoa(reservedIndex), source.String())
			}
		}
		previousNumber := int32(0)
		seenFieldIDs, seenFieldNames := map[string]struct{}{}, map[string]struct{}{}
		for fieldIndex, fieldWire := range messageWire.Fields {
			fieldPointer := messagePointer + "/fields/" + itoa(fieldIndex)
			if fieldWire.Number <= previousNumber {
				return Snapshot{}, lockError("canonical_order_invalid", fieldPointer, source.String())
			}
			previousNumber = fieldWire.Number
			if fieldWire.ID == "" || !protoFieldPattern.MatchString(fieldWire.Name) {
				return Snapshot{}, lockError("wire_name_invalid", fieldPointer+"/name", source.String())
			}
			if !legalNumber(fieldWire.Number) {
				return Snapshot{}, lockError("wire_number_invalid", fieldPointer+"/number", source.String())
			}
			if fieldWire.Repeated && fieldWire.Optional {
				return Snapshot{}, lockError("wire_type_invalid", fieldPointer+"/optional", source.String())
			}
			if fieldWire.TenantContext && (!fieldWire.Internal || fieldWire.Type != "int64" || fieldWire.Name != "tenant_id" || fieldWire.Repeated || fieldWire.Optional) {
				return Snapshot{}, lockError("wire_type_invalid", fieldPointer+"/tenantContext", source.String())
			}
			if _, duplicate := seenFieldIDs[fieldWire.ID]; duplicate {
				return Snapshot{}, lockError("history_duplicate", fieldPointer+"/id", source.String())
			}
			if _, duplicate := seenFieldNames[fieldWire.Name]; duplicate {
				return Snapshot{}, lockError("history_duplicate", fieldPointer+"/name", source.String())
			}
			seenFieldIDs[fieldWire.ID], seenFieldNames[fieldWire.Name] = struct{}{}, struct{}{}
			if containsName(message.reservedNames, fieldWire.Name) || containsNumber(message.reservedNumbers, fieldWire.Number) {
				return Snapshot{}, lockError("reservation_invalid", fieldPointer, source.String())
			}
			if !validWireType(fieldWire.Type) {
				return Snapshot{}, lockError("wire_type_invalid", fieldPointer+"/type", source.String())
			}
			if requiredImport := importForType(fieldWire.Type); requiredImport != "" {
				if _, ok := importSet[requiredImport]; !ok {
					return Snapshot{}, lockError("document_invalid", fieldPointer+"/type", source.String())
				}
			} else if !builtinWireType(fieldWire.Type) {
				if _, ok := typeNames[fieldWire.Type]; !ok {
					return Snapshot{}, lockError("wire_type_invalid", fieldPointer+"/type", source.String())
				}
			}
			ref, err := provenance.ParseSourceRef(fieldWire.Source.Ref)
			if err != nil {
				return Snapshot{}, lockError("source_ref_invalid", fieldPointer+"/source/ref", source.String())
			}
			digest, err := provenance.ParseDigest(fieldWire.Source.Digest)
			if err != nil {
				return Snapshot{}, lockError("source_digest_invalid", fieldPointer+"/source/digest", source.String())
			}
			fieldSource := provenance.Source{Ref: ref, Digest: digest}
			declared, ok := sourceSet[ref.String()]
			if !ok || declared.Digest != digest {
				return Snapshot{}, lockError("source_digest_invalid", fieldPointer+"/source", source.String())
			}
			usedSources[ref.String()] = fieldSource
			message.fields = append(message.fields, &fieldState{id: fieldWire.ID, name: fieldWire.Name, number: fieldWire.Number, wireType: fieldWire.Type, repeated: fieldWire.Repeated, optional: fieldWire.Optional, internal: fieldWire.Internal, tenantContext: fieldWire.TenantContext, source: fieldSource})
		}
		state.messages = append(state.messages, message)
	}
	seenServiceIDs, seenServiceNames := map[string]struct{}{}, map[string]struct{}{}
	usedTenantContextFields := map[string]struct{}{}
	for serviceIndex, serviceWire := range wire.Services {
		if serviceIndex > 0 && serviceWire.Name <= wire.Services[serviceIndex-1].Name {
			return Snapshot{}, lockError("canonical_order_invalid", "/services/"+itoa(serviceIndex), source.String())
		}
		servicePointer := "/services/" + itoa(serviceIndex)
		if serviceWire.ID == "" || !protoSymbolPattern.MatchString(serviceWire.Name) {
			return Snapshot{}, lockError("wire_name_invalid", servicePointer+"/name", source.String())
		}
		if _, duplicate := globalSymbols[serviceWire.Name]; duplicate {
			return Snapshot{}, lockError("history_duplicate", servicePointer+"/name", source.String())
		}
		globalSymbols[serviceWire.Name] = struct{}{}
		if _, duplicate := seenServiceIDs[serviceWire.ID]; duplicate {
			return Snapshot{}, lockError("history_duplicate", servicePointer+"/id", source.String())
		}
		if _, duplicate := seenServiceNames[serviceWire.Name]; duplicate {
			return Snapshot{}, lockError("history_duplicate", servicePointer+"/name", source.String())
		}
		seenServiceIDs[serviceWire.ID], seenServiceNames[serviceWire.Name] = struct{}{}, struct{}{}
		service := &serviceState{id: serviceWire.ID, name: serviceWire.Name}
		seenMethodIDs, seenMethodNames := map[string]struct{}{}, map[string]struct{}{}
		for methodIndex, methodWire := range serviceWire.Methods {
			methodPointer := servicePointer + "/methods/" + itoa(methodIndex)
			if methodWire.ID == "" || !protoSymbolPattern.MatchString(methodWire.Name) {
				return Snapshot{}, lockError("wire_name_invalid", methodPointer+"/name", source.String())
			}
			if _, duplicate := seenMethodIDs[methodWire.ID]; duplicate {
				return Snapshot{}, lockError("history_duplicate", methodPointer+"/id", source.String())
			}
			if _, duplicate := seenMethodNames[methodWire.Name]; duplicate {
				return Snapshot{}, lockError("history_duplicate", methodPointer+"/name", source.String())
			}
			seenMethodIDs[methodWire.ID], seenMethodNames[methodWire.Name] = struct{}{}, struct{}{}
			if _, ok := seenMessageNames[methodWire.Input]; !ok {
				return Snapshot{}, lockError("document_invalid", methodPointer+"/input", source.String())
			}
			if _, ok := seenMessageNames[methodWire.Output]; !ok {
				return Snapshot{}, lockError("document_invalid", methodPointer+"/output", source.String())
			}
			context := &rpcContextState{contextFields: []*contextBindingState{}}
			for bindingIndex, binding := range methodWire.RPCContext.ContextFields {
				bindingPointer := methodPointer + "/rpcContext/contextFields/" + itoa(bindingIndex)
				if _, ok := importSet["nexa/protocol/v1/options.proto"]; !ok {
					return Snapshot{}, lockError("document_invalid", bindingPointer, source.String())
				}
				if binding.Source != string(ContextTenantID) || binding.RPCField != "tenant_id" || len(context.contextFields) != 0 {
					return Snapshot{}, lockError("document_invalid", bindingPointer, source.String())
				}
				request := stateMessageByName(state, methodWire.Input)
				if request == nil || !hasTenantContextField(request, binding.RPCField) {
					return Snapshot{}, lockError("document_invalid", bindingPointer+"/rpcField", source.String())
				}
				usedTenantContextFields[methodWire.Input+"\x00"+binding.RPCField] = struct{}{}
				context.contextFields = append(context.contextFields, &contextBindingState{source: ContextTenantID, rpcField: binding.RPCField})
			}
			service.methods = append(service.methods, &methodState{id: methodWire.ID, name: methodWire.Name, input: methodWire.Input, output: methodWire.Output, rpcContext: context})
		}
		state.services = append(state.services, service)
	}
	tenantSet := make(map[string]struct{}, len(state.tenantEntityIDs))
	for _, id := range state.tenantEntityIDs {
		tenantSet[id] = struct{}{}
	}
	for serviceIndex, service := range state.services {
		entityID := service.id
		if suffix := "/service:crud"; len(entityID) > len(suffix) && entityID[len(entityID)-len(suffix):] == suffix {
			entityID = entityID[:len(entityID)-len(suffix)]
		}
		_, tenant := tenantSet[entityID]
		for methodIndex, method := range service.methods {
			hasContext := method.rpcContext != nil && len(method.rpcContext.contextFields) != 0
			if tenant != hasContext {
				return Snapshot{}, lockError("document_invalid", "/services/"+itoa(serviceIndex)+"/methods/"+itoa(methodIndex)+"/rpcContext", source.String())
			}
		}
	}
	for messageIndex, message := range state.messages {
		for fieldIndex, field := range message.fields {
			if !field.tenantContext {
				continue
			}
			if _, used := usedTenantContextFields[message.name+"\x00"+field.name]; !used {
				return Snapshot{}, lockError("document_invalid", "/messages/"+itoa(messageIndex)+"/fields/"+itoa(fieldIndex)+"/tenantContext", source.String())
			}
		}
	}
	if len(usedSources) != len(sourceSet) {
		return Snapshot{}, lockError("source_digest_invalid", "/sources", source.String())
	}
	canonical, err := canonicalJSON(wire)
	if err != nil {
		return Snapshot{}, lockError("canonical_invalid", "", source.String())
	}
	if !bytes.Equal(canonical, data) {
		return Snapshot{}, lockError("canonical_invalid", "", source.String())
	}
	state.canonical = append([]byte(nil), canonical...)
	return Snapshot{state: &snapshotState{document: state, canonical: canonical}}, nil
}

func stateMessageByName(state *documentState, name string) *messageState {
	for _, message := range state.messages {
		if message.name == name {
			return message
		}
	}
	return nil
}

func hasTenantContextField(message *messageState, name string) bool {
	for _, field := range message.fields {
		if field.name == name && field.tenantContext && field.internal && field.wireType == "int64" {
			return true
		}
	}
	return false
}

func importForType(value string) string {
	switch value {
	case "google.protobuf.FieldMask":
		return "google/protobuf/field_mask.proto"
	case "google.protobuf.Value":
		return "google/protobuf/struct.proto"
	case "google.protobuf.Timestamp":
		return "google/protobuf/timestamp.proto"
	}
	return ""
}

func builtinWireType(value string) bool {
	switch value {
	case "bool", "int64", "uint64", "float", "double", "string", "bytes":
		return true
	}
	return false
}

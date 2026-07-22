package crudbuild

import (
	"bytes"
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

func ParseLock(source provenance.DomainSource, data []byte) (Lock, error) {
	if !validateLockCollectionPresence(data) {
		return Lock{}, lockError("document_required_missing", "", source.String())
	}
	var wire wireLock
	if err := strictdoc.DecodeJSON(source.String(), data, &wire); err != nil {
		if owner, ok := err.(*strictdoc.Error); ok {
			return Lock{}, lockError(owner.Code, owner.Pointer, source.String())
		}
		return Lock{}, lockError("document_invalid", "", source.String())
	}
	state, err := decodeLockWire(wire, source.String())
	if err != nil {
		return Lock{}, err
	}
	canonical, canonicalErr := canonicalJSON(wire)
	if canonicalErr != nil {
		return Lock{}, lockError("canonical_invalid", "", source.String())
	}
	if !bytes.Equal(canonical, data) {
		return Lock{}, lockError("canonical_invalid", "", source.String())
	}
	state.canonical = canonical
	return Lock{state: state}, nil
}

func decodeLockWire(wire wireLock, source string) (*lockState, error) {
	if wire.APIVersion != LockAPIVersion {
		return nil, lockError("version_unsupported", "/apiVersion", source)
	}
	if wire.Kind != LockKind {
		return nil, lockError("kind_invalid", "/kind", source)
	}
	if !serviceIDPattern.MatchString(wire.ServiceID) {
		return nil, lockError("service_id_invalid", "/serviceId", source)
	}
	state := &lockState{serviceID: wire.ServiceID, schemas: make([]*lockSchemaState, 0, len(wire.Schemas))}
	previousSchema := ""
	for schemaIndex, schemaWire := range wire.Schemas {
		schemaPointer := "/schemas/" + itoa(schemaIndex)
		if !strings.HasPrefix(schemaWire.ID, "schema:") || schemaWire.ID == "schema:" {
			return nil, lockError("schema_id_invalid", schemaPointer+"/id", source)
		}
		if schemaWire.ID <= previousSchema {
			return nil, lockError("canonical_order_invalid", schemaPointer+"/id", source)
		}
		previousSchema = schemaWire.ID
		schema := &lockSchemaState{id: schemaWire.ID, enums: make([]*lockEnumState, 0, len(schemaWire.Enums)), messages: make([]*lockMessageState, 0, len(schemaWire.Messages))}
		previousEnum := ""
		for enumIndex, enumWire := range schemaWire.Enums {
			enumPointer := schemaPointer + "/enums/" + itoa(enumIndex)
			if !strings.HasPrefix(enumWire.ID, schemaWire.ID+"/") {
				return nil, lockError("message_id_invalid", enumPointer+"/id", source)
			}
			if enumWire.ID <= previousEnum {
				return nil, lockError("canonical_order_invalid", enumPointer+"/id", source)
			}
			previousEnum = enumWire.ID
			enum := &lockEnumState{id: enumWire.ID, active: enumWire.Active, reservedNames: append([]string(nil), enumWire.ReservedNames...), reservedNumbers: append([]int32(nil), enumWire.ReservedNumbers...)}
			if !strictlySortedStrings(enum.reservedNames) || !strictlySortedNumbers(enum.reservedNumbers) {
				return nil, lockError("canonical_order_invalid", enumPointer+"/reservedNames", source)
			}
			for reservedIndex, name := range enum.reservedNames {
				if !protoSymbolPattern.MatchString(name) {
					return nil, lockError("reservation_invalid", enumPointer+"/reservedNames/"+itoa(reservedIndex), source)
				}
			}
			for reservedIndex, number := range enum.reservedNumbers {
				if !legalEnumNumber(number) {
					return nil, lockError("reservation_invalid", enumPointer+"/reservedNumbers/"+itoa(reservedIndex), source)
				}
			}
			seenIDs, seenNames, seenNumbers := map[string]struct{}{}, map[string]struct{}{}, map[int32]struct{}{}
			current, err := decodeEnumAssignments(enumWire.Current, enumPointer+"/current", source, enumWire.ID, seenIDs, seenNames, seenNumbers)
			if err != nil {
				return nil, err
			}
			retired, err := decodeEnumAssignments(enumWire.Retired, enumPointer+"/retired", source, enumWire.ID, seenIDs, seenNames, seenNumbers)
			if err != nil {
				return nil, err
			}
			enum.current, enum.retired = current, retired
			for index, assignment := range current {
				if containsName(enum.reservedNames, assignment.wireName) || containsNumber(enum.reservedNumbers, assignment.number) {
					return nil, lockError("reservation_invalid", enumPointer+"/current/"+itoa(index), source)
				}
			}
			for index, assignment := range retired {
				if !containsName(enum.reservedNames, assignment.wireName) || !containsNumber(enum.reservedNumbers, assignment.number) {
					return nil, lockError("reservation_invalid", enumPointer+"/retired/"+itoa(index), source)
				}
			}
			if !enum.active && len(enum.current) > 0 {
				return nil, lockError("history_duplicate", enumPointer+"/active", source)
			}
			schema.enums = append(schema.enums, enum)
		}
		previousMessage := ""
		for messageIndex, messageWire := range schemaWire.Messages {
			messagePointer := schemaPointer + "/messages/" + itoa(messageIndex)
			if !strings.HasPrefix(messageWire.ID, schemaWire.ID+"/") {
				return nil, lockError("message_id_invalid", messagePointer+"/id", source)
			}
			if messageWire.ID <= previousMessage {
				return nil, lockError("canonical_order_invalid", messagePointer+"/id", source)
			}
			previousMessage = messageWire.ID
			message := &lockMessageState{id: messageWire.ID, active: messageWire.Active, reservedNames: append([]string(nil), messageWire.ReservedNames...), reservedNumbers: append([]int32(nil), messageWire.ReservedNumbers...)}
			if !strictlySortedStrings(message.reservedNames) || !strictlySortedNumbers(message.reservedNumbers) {
				return nil, lockError("canonical_order_invalid", messagePointer+"/reservedNames", source)
			}
			for reservedIndex, name := range message.reservedNames {
				if !protoFieldPattern.MatchString(name) {
					return nil, lockError("reservation_invalid", messagePointer+"/reservedNames/"+itoa(reservedIndex), source)
				}
			}
			for reservedIndex, number := range message.reservedNumbers {
				if !legalNumber(number) {
					return nil, lockError("reservation_invalid", messagePointer+"/reservedNumbers/"+itoa(reservedIndex), source)
				}
			}
			seenIDs, seenNames, seenNumbers := map[string]struct{}{}, map[string]struct{}{}, map[int32]struct{}{}
			current, err := decodeAssignments(messageWire.Current, messagePointer+"/current", source, schemaWire.ID, seenIDs, seenNames, seenNumbers)
			if err != nil {
				return nil, err
			}
			retired, err := decodeAssignments(messageWire.Retired, messagePointer+"/retired", source, schemaWire.ID, seenIDs, seenNames, seenNumbers)
			if err != nil {
				return nil, err
			}
			message.current, message.retired = current, retired
			for index, assignment := range current {
				if containsName(message.reservedNames, assignment.wireName) || containsNumber(message.reservedNumbers, assignment.number) {
					return nil, lockError("reservation_invalid", messagePointer+"/current/"+itoa(index), source)
				}
			}
			for index, assignment := range retired {
				if !containsName(message.reservedNames, assignment.wireName) || !containsNumber(message.reservedNumbers, assignment.number) {
					return nil, lockError("reservation_invalid", messagePointer+"/retired/"+itoa(index), source)
				}
			}
			if message.active != (len(message.current) > 0) && len(message.current) > 0 {
				return nil, lockError("history_duplicate", messagePointer+"/active", source)
			}
			schema.messages = append(schema.messages, message)
		}
		state.schemas = append(state.schemas, schema)
	}
	return state, nil
}

func decodeEnumAssignments(values []wireEnumAssignment, pointer, source, enumID string, seenIDs map[string]struct{}, seenNames map[string]struct{}, seenNumbers map[int32]struct{}) ([]*enumAssignmentState, error) {
	result := make([]*enumAssignmentState, 0, len(values))
	previous := ""
	for index, value := range values {
		itemPointer := pointer + "/" + itoa(index)
		if !strings.HasPrefix(value.ValueID, strings.TrimSuffix(enumID, "/enum")+"/enum-value:") {
			return nil, lockError("field_id_invalid", itemPointer+"/valueId", source)
		}
		if value.ValueID <= previous {
			return nil, lockError("canonical_order_invalid", itemPointer+"/valueId", source)
		}
		previous = value.ValueID
		if _, duplicate := seenIDs[value.ValueID]; duplicate {
			return nil, lockError("history_duplicate", itemPointer+"/valueId", source)
		}
		seenIDs[value.ValueID] = struct{}{}
		if !protoSymbolPattern.MatchString(value.WireName) {
			return nil, lockError("wire_name_invalid", itemPointer+"/wireName", source)
		}
		if _, duplicate := seenNames[value.WireName]; duplicate {
			return nil, lockError("history_duplicate", itemPointer+"/wireName", source)
		}
		seenNames[value.WireName] = struct{}{}
		if !legalEnumNumber(value.Number) {
			return nil, lockError("wire_number_invalid", itemPointer+"/number", source)
		}
		if _, duplicate := seenNumbers[value.Number]; duplicate {
			return nil, lockError("history_duplicate", itemPointer+"/number", source)
		}
		seenNumbers[value.Number] = struct{}{}
		if value.SemanticValue == "" && !strings.HasSuffix(value.ValueID, ":unspecified") {
			return nil, lockError("wire_type_invalid", itemPointer+"/semanticValue", source)
		}
		result = append(result, &enumAssignmentState{valueID: value.ValueID, wireName: value.WireName, number: value.Number, semantic: value.SemanticValue})
	}
	return result, nil
}

func decodeAssignments(values []wireAssignment, pointer, source, schemaID string, seenIDs map[string]struct{}, seenNames map[string]struct{}, seenNumbers map[int32]struct{}) ([]*assignmentState, error) {
	result := make([]*assignmentState, 0, len(values))
	previous := ""
	for index, value := range values {
		itemPointer := pointer + "/" + itoa(index)
		if !strings.HasPrefix(value.FieldID, schemaID+"/") {
			return nil, lockError("field_id_invalid", itemPointer+"/fieldId", source)
		}
		if value.FieldID <= previous {
			return nil, lockError("canonical_order_invalid", itemPointer+"/fieldId", source)
		}
		previous = value.FieldID
		if _, duplicate := seenIDs[value.FieldID]; duplicate {
			return nil, lockError("history_duplicate", itemPointer+"/fieldId", source)
		}
		seenIDs[value.FieldID] = struct{}{}
		if !protoFieldPattern.MatchString(value.WireName) {
			return nil, lockError("wire_name_invalid", itemPointer+"/wireName", source)
		}
		if _, duplicate := seenNames[value.WireName]; duplicate {
			return nil, lockError("history_duplicate", itemPointer+"/wireName", source)
		}
		seenNames[value.WireName] = struct{}{}
		if !legalNumber(value.Number) {
			return nil, lockError("wire_number_invalid", itemPointer+"/number", source)
		}
		if _, duplicate := seenNumbers[value.Number]; duplicate {
			return nil, lockError("history_duplicate", itemPointer+"/number", source)
		}
		seenNumbers[value.Number] = struct{}{}
		if !validWireType(value.WireType) {
			return nil, lockError("wire_type_invalid", itemPointer+"/wireType", source)
		}
		ref, err := provenance.ParseSourceRef(value.SourceRef)
		if err != nil {
			return nil, lockError("source_ref_invalid", itemPointer+"/sourceRef", source)
		}
		digest, err := provenance.ParseDigest(value.SourceDigest)
		if err != nil {
			return nil, lockError("source_digest_invalid", itemPointer+"/sourceDigest", source)
		}
		result = append(result, &assignmentState{fieldID: value.FieldID, wireName: value.WireName, number: value.Number, wireType: value.WireType, source: provenance.Source{Ref: ref, Digest: digest}})
	}
	return result, nil
}

func validWireType(value string) bool {
	switch value {
	case "bool", "int64", "uint64", "float", "double", "string", "bytes", "google.protobuf.Timestamp", "google.protobuf.Value", "google.protobuf.FieldMask":
		return true
	}
	return protoSymbolPattern.MatchString(value)
}
func legalNumber(value int32) bool {
	return value > 0 && value <= 536870911 && (value < 19000 || value > 19999)
}
func legalEnumNumber(value int32) bool { return value >= 0 }
func strictlySortedStrings(values []string) bool {
	return sort.StringsAreSorted(values) && !hasDuplicateString(values)
}
func strictlySortedNumbers(values []int32) bool {
	for i, value := range values {
		if i > 0 && value <= values[i-1] {
			return false
		}
	}
	return true
}
func hasDuplicateString(values []string) bool {
	for i, value := range values {
		if i > 0 && value == values[i-1] {
			return true
		}
	}
	return false
}

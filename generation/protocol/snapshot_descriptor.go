package protocol

import (
	"strings"
)

type snapshotDescriptorIndex struct {
	messages map[string]*snapshotMessageDescriptor
	enums    map[string]struct{}
}

type snapshotMessageDescriptor struct {
	filePath string
	fields   map[int]canonicalField
}

func buildSnapshotDescriptorIndex(document canonicalDocument) (*snapshotDescriptorIndex, error) {
	index := &snapshotDescriptorIndex{messages: map[string]*snapshotMessageDescriptor{}, enums: map[string]struct{}{}}
	for _, file := range document.Files {
		for _, message := range file.Messages {
			if message.FullName == "" || index.messages[message.FullName] != nil {
				return nil, protocolError("protocol_snapshot_invalid", "message_descriptor_invalid", file.Path, "/messages", "Protocol snapshot message descriptor is invalid")
			}
			descriptor := &snapshotMessageDescriptor{filePath: file.Path, fields: map[int]canonicalField{}}
			for _, field := range message.Fields {
				if _, duplicate := descriptor.fields[field.Number]; duplicate {
					return nil, protocolError("protocol_snapshot_invalid", "field_descriptor_invalid", file.Path, "/messages/fields", "Protocol snapshot field descriptor is invalid")
				}
				descriptor.fields[field.Number] = field
			}
			index.messages[message.FullName] = descriptor
		}
		for _, enum := range file.Enums {
			if enum.FullName == "" {
				return nil, protocolError("protocol_snapshot_invalid", "enum_descriptor_invalid", file.Path, "/enums", "Protocol snapshot enum descriptor is invalid")
			}
			if _, duplicate := index.enums[enum.FullName]; duplicate {
				return nil, protocolError("protocol_snapshot_invalid", "enum_descriptor_invalid", file.Path, "/enums", "Protocol snapshot enum descriptor is invalid")
			}
			index.enums[enum.FullName] = struct{}{}
		}
	}
	return index, nil
}

func validateSnapshotField(messageName string, field canonicalField, index *snapshotDescriptorIndex, filePath string) error {
	if !strings.HasPrefix(field.FullName, messageName+".") || strings.TrimPrefix(field.FullName, messageName+".") == "" {
		return protocolError("protocol_snapshot_invalid", "field_descriptor_invalid", filePath, "/messages/fields", "Protocol snapshot field owner is invalid")
	}
	if !validSnapshotType(field.Type, index, true) {
		return protocolError("protocol_snapshot_invalid", "field_type_invalid", filePath, "/messages/fields/type", "Protocol snapshot field type is invalid")
	}
	switch field.Presence {
	case PresenceMap:
		if field.Cardinality != CardinalityRepeated || field.Type.Kind != TypeMap || field.Oneof != "" {
			return protocolError("protocol_snapshot_invalid", "field_presence_invalid", filePath, "/messages/fields/presence", "Protocol snapshot field presence is invalid")
		}
	case PresenceOneof:
		if field.Cardinality != CardinalitySingular || field.Type.Kind == TypeMap || field.Oneof == "" {
			return protocolError("protocol_snapshot_invalid", "field_presence_invalid", filePath, "/messages/fields/presence", "Protocol snapshot field presence is invalid")
		}
	case PresenceExplicit:
		if field.Cardinality != CardinalitySingular || field.Type.Kind == TypeMap || field.Oneof != "" {
			return protocolError("protocol_snapshot_invalid", "field_presence_invalid", filePath, "/messages/fields/presence", "Protocol snapshot field presence is invalid")
		}
	case PresenceImplicit:
		if field.Type.Kind == TypeMap || field.Oneof != "" || field.Cardinality == CardinalitySingular && field.Type.Kind == TypeMessage {
			return protocolError("protocol_snapshot_invalid", "field_presence_invalid", filePath, "/messages/fields/presence", "Protocol snapshot field presence is invalid")
		}
	default:
		return protocolError("protocol_snapshot_invalid", "field_presence_invalid", filePath, "/messages/fields/presence", "Protocol snapshot field presence is invalid")
	}
	return nil
}

func validSnapshotType(value canonicalType, index *snapshotDescriptorIndex, allowMap bool) bool {
	switch value.Kind {
	case TypeScalar:
		return validProtoScalar(value.Name) && value.Key == nil && value.Value == nil
	case TypeEnum:
		_, ok := index.enums[value.Name]
		return ok && value.Key == nil && value.Value == nil
	case TypeMessage:
		return index.messages[value.Name] != nil && value.Key == nil && value.Value == nil
	case TypeMap:
		return allowMap && value.Name == "" && value.Key != nil && value.Value != nil && value.Key.Kind == TypeScalar && validMapKeyScalar(value.Key.Name) && validSnapshotType(*value.Value, index, false)
	default:
		return false
	}
}

func validProtoScalar(value string) bool {
	switch value {
	case "double", "float", "int64", "uint64", "int32", "fixed64", "fixed32", "bool", "string", "bytes", "uint32", "sfixed32", "sfixed64", "sint32", "sint64":
		return true
	}
	return false
}
func validMapKeyScalar(value string) bool {
	switch value {
	case "int64", "uint64", "int32", "fixed64", "fixed32", "bool", "string", "uint32", "sfixed32", "sfixed64", "sint32", "sint64":
		return true
	}
	return false
}

package crudbuild

import (
	"encoding/json"
	"sort"

	"github.com/gowebpki/jcs"
)

type wireSource struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type wireField struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Number        int32      `json:"number"`
	Type          string     `json:"type"`
	Repeated      bool       `json:"repeated"`
	Optional      bool       `json:"optional"`
	Internal      bool       `json:"internal"`
	TenantContext bool       `json:"tenantContext"`
	Source        wireSource `json:"source"`
}
type wireMessage struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Fields          []wireField `json:"fields"`
	ReservedNames   []string    `json:"reservedNames"`
	ReservedNumbers []int32     `json:"reservedNumbers"`
}
type wireEnumValue struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Number int32  `json:"number"`
}
type wireEnum struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Values          []wireEnumValue `json:"values"`
	ReservedNames   []string        `json:"reservedNames"`
	ReservedNumbers []int32         `json:"reservedNumbers"`
}
type wireMethod struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Input      string         `json:"input"`
	Output     string         `json:"output"`
	RPCContext wireRPCContext `json:"rpcContext"`
}
type wireRPCContext struct {
	ContextFields []wireContextBinding `json:"contextFields"`
}
type wireContextBinding struct {
	Source   string `json:"source"`
	RPCField string `json:"rpcField"`
}
type wireService struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Methods []wireMethod `json:"methods"`
}
type wireDocument struct {
	APIVersion     string        `json:"apiVersion"`
	Kind           string        `json:"kind"`
	ServiceID      string        `json:"serviceId"`
	ProtoPackage   string        `json:"protoPackage"`
	GoPackage      string        `json:"goPackage"`
	Imports        []string      `json:"imports"`
	Enums          []wireEnum    `json:"enums"`
	Messages       []wireMessage `json:"messages"`
	Services       []wireService `json:"services"`
	TenantEntities []string      `json:"tenantEntities"`
	Sources        []wireSource  `json:"sources"`
}

type wireAssignment struct {
	FieldID      string `json:"fieldId"`
	WireName     string `json:"wireName"`
	Number       int32  `json:"number"`
	WireType     string `json:"wireType"`
	SourceRef    string `json:"sourceRef"`
	SourceDigest string `json:"sourceDigest"`
}
type wireLockMessage struct {
	ID              string           `json:"id"`
	Active          bool             `json:"active"`
	Current         []wireAssignment `json:"current"`
	Retired         []wireAssignment `json:"retired"`
	ReservedNames   []string         `json:"reservedNames"`
	ReservedNumbers []int32          `json:"reservedNumbers"`
}
type wireEnumAssignment struct {
	ValueID       string `json:"valueId"`
	WireName      string `json:"wireName"`
	Number        int32  `json:"number"`
	SemanticValue string `json:"semanticValue"`
}
type wireLockEnum struct {
	ID              string               `json:"id"`
	Active          bool                 `json:"active"`
	Current         []wireEnumAssignment `json:"current"`
	Retired         []wireEnumAssignment `json:"retired"`
	ReservedNames   []string             `json:"reservedNames"`
	ReservedNumbers []int32              `json:"reservedNumbers"`
}
type wireLockSchema struct {
	ID       string            `json:"id"`
	Enums    []wireLockEnum    `json:"enums"`
	Messages []wireLockMessage `json:"messages"`
}
type wireLock struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	ServiceID  string           `json:"serviceId"`
	Schemas    []wireLockSchema `json:"schemas"`
}

func canonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return jcs.Transform(encoded)
}

func documentWire(state *documentState) wireDocument {
	result := wireDocument{APIVersion: APIVersion, Kind: Kind, ServiceID: state.serviceID, ProtoPackage: state.protoPackage, GoPackage: state.goPackage, Imports: append([]string{}, state.imports...), Enums: []wireEnum{}, Messages: []wireMessage{}, Services: []wireService{}, TenantEntities: append([]string{}, state.tenantEntityIDs...), Sources: []wireSource{}}
	for _, value := range state.enums {
		item := wireEnum{ID: value.id, Name: value.name, Values: []wireEnumValue{}, ReservedNames: append([]string{}, value.reservedNames...), ReservedNumbers: append([]int32{}, value.reservedNumbers...)}
		for _, enumValue := range value.values {
			item.Values = append(item.Values, wireEnumValue{ID: enumValue.id, Name: enumValue.name, Number: enumValue.number})
		}
		result.Enums = append(result.Enums, item)
	}
	for _, value := range state.messages {
		item := wireMessage{ID: value.id, Name: value.name, Fields: []wireField{}, ReservedNames: append([]string{}, value.reservedNames...), ReservedNumbers: append([]int32{}, value.reservedNumbers...)}
		for _, field := range value.fields {
			item.Fields = append(item.Fields, wireField{ID: field.id, Name: field.name, Number: field.number, Type: field.wireType, Repeated: field.repeated, Optional: field.optional, Internal: field.internal, TenantContext: field.tenantContext, Source: wireSource{Ref: field.source.Ref.String(), Digest: field.source.Digest.String()}})
		}
		result.Messages = append(result.Messages, item)
	}
	for _, value := range state.services {
		item := wireService{ID: value.id, Name: value.name, Methods: []wireMethod{}}
		for _, method := range value.methods {
			contextFields := []wireContextBinding{}
			if method.rpcContext != nil {
				for _, binding := range method.rpcContext.contextFields {
					contextFields = append(contextFields, wireContextBinding{Source: string(binding.source), RPCField: binding.rpcField})
				}
			}
			item.Methods = append(item.Methods, wireMethod{ID: method.id, Name: method.name, Input: method.input, Output: method.output, RPCContext: wireRPCContext{ContextFields: contextFields}})
		}
		result.Services = append(result.Services, item)
	}
	for _, source := range state.sources {
		result.Sources = append(result.Sources, wireSource{Ref: source.Ref.String(), Digest: source.Digest.String()})
	}
	return result
}

func lockWire(state *lockState) wireLock {
	result := wireLock{APIVersion: LockAPIVersion, Kind: LockKind, ServiceID: state.serviceID, Schemas: []wireLockSchema{}}
	for _, schema := range state.schemas {
		schemaWire := wireLockSchema{ID: schema.id, Enums: []wireLockEnum{}, Messages: []wireLockMessage{}}
		for _, enum := range schema.enums {
			enumWire := wireLockEnum{ID: enum.id, Active: enum.active, Current: []wireEnumAssignment{}, Retired: []wireEnumAssignment{}, ReservedNames: append([]string{}, enum.reservedNames...), ReservedNumbers: append([]int32{}, enum.reservedNumbers...)}
			for _, assignment := range enum.current {
				enumWire.Current = append(enumWire.Current, enumAssignmentWire(assignment))
			}
			for _, assignment := range enum.retired {
				enumWire.Retired = append(enumWire.Retired, enumAssignmentWire(assignment))
			}
			schemaWire.Enums = append(schemaWire.Enums, enumWire)
		}
		for _, message := range schema.messages {
			messageWire := wireLockMessage{ID: message.id, Active: message.active, Current: []wireAssignment{}, Retired: []wireAssignment{}, ReservedNames: append([]string{}, message.reservedNames...), ReservedNumbers: append([]int32{}, message.reservedNumbers...)}
			for _, assignment := range message.current {
				messageWire.Current = append(messageWire.Current, assignmentWire(assignment))
			}
			for _, assignment := range message.retired {
				messageWire.Retired = append(messageWire.Retired, assignmentWire(assignment))
			}
			schemaWire.Messages = append(schemaWire.Messages, messageWire)
		}
		result.Schemas = append(result.Schemas, schemaWire)
	}
	return result
}

func enumAssignmentWire(value *enumAssignmentState) wireEnumAssignment {
	return wireEnumAssignment{ValueID: value.valueID, WireName: value.wireName, Number: value.number, SemanticValue: value.semantic}
}

func assignmentWire(value *assignmentState) wireAssignment {
	return wireAssignment{FieldID: value.fieldID, WireName: value.wireName, Number: value.number, WireType: value.wireType, SourceRef: value.source.Ref.String(), SourceDigest: value.source.Digest.String()}
}

func finalizeDocument(state *documentState) (Document, error) {
	canonical, err := canonicalJSON(documentWire(state))
	if err != nil {
		return Document{}, buildError("canonical_invalid", "/document")
	}
	state.canonical = canonical
	return Document{state: state}, nil
}

func finalizeLock(state *lockState) (Lock, error) {
	sortLock(state)
	canonical, err := canonicalJSON(lockWire(state))
	if err != nil {
		return Lock{}, lockError("canonical_invalid", "", "")
	}
	state.canonical = canonical
	return Lock{state: state}, nil
}

func sortLock(state *lockState) {
	sort.SliceStable(state.schemas, func(i, j int) bool { return state.schemas[i].id < state.schemas[j].id })
	for _, schema := range state.schemas {
		sort.SliceStable(schema.enums, func(i, j int) bool { return schema.enums[i].id < schema.enums[j].id })
		for _, enum := range schema.enums {
			sort.SliceStable(enum.current, func(i, j int) bool { return enum.current[i].valueID < enum.current[j].valueID })
			sort.SliceStable(enum.retired, func(i, j int) bool { return enum.retired[i].valueID < enum.retired[j].valueID })
			sort.Strings(enum.reservedNames)
			sort.SliceStable(enum.reservedNumbers, func(i, j int) bool { return enum.reservedNumbers[i] < enum.reservedNumbers[j] })
		}
		sort.SliceStable(schema.messages, func(i, j int) bool { return schema.messages[i].id < schema.messages[j].id })
		for _, message := range schema.messages {
			sort.SliceStable(message.current, func(i, j int) bool { return message.current[i].fieldID < message.current[j].fieldID })
			sort.SliceStable(message.retired, func(i, j int) bool { return message.retired[i].fieldID < message.retired[j].fieldID })
			sort.Strings(message.reservedNames)
			sort.SliceStable(message.reservedNumbers, func(i, j int) bool { return message.reservedNumbers[i] < message.reservedNumbers[j] })
		}
	}
}

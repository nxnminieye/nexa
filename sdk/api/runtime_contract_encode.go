package api

import (
	"sort"
	"strconv"
)

type runtimeContractEncodingStats struct {
	rawBytes int
	nodes    int
}

type runtimeContractEncoder struct {
	rawLimit  int
	nodeLimit int
	rawBytes  int
	nodes     int
	output    []byte
	collect   bool
}

func measureRuntimeContract(model *runtimeModel) runtimeContractEncodingStats {
	limits := RuntimeContractLimits()
	encoder := runtimeContractEncoder{rawLimit: limits.RawBytes, nodeLimit: limits.JSONNodes}
	encoder.encode(model)
	return runtimeContractEncodingStats{rawBytes: encoder.rawBytes, nodes: encoder.nodes}
}

func encodeRuntimeContract(model *runtimeModel) ([]byte, error) {
	stats := measureRuntimeContract(model)
	limits := RuntimeContractLimits()
	if stats.nodes > limits.JSONNodes || stats.rawBytes > limits.RawBytes {
		return nil, newRuntimeContractInvalid("runtime_contract_schema_invalid", "/runtimeContract")
	}
	encoder := runtimeContractEncoder{
		rawLimit:  limits.RawBytes,
		nodeLimit: limits.JSONNodes,
		output:    make([]byte, 0, stats.rawBytes),
		collect:   true,
	}
	encoder.encode(model)
	if encoder.nodes != stats.nodes || encoder.rawBytes != stats.rawBytes || len(encoder.output) != stats.rawBytes {
		return nil, newRuntimeContractInvalid("runtime_contract_schema_invalid", "/runtimeContract")
	}
	return encoder.output, nil
}

func (e *runtimeContractEncoder) encode(model *runtimeModel) {
	e.beginObject()
	e.member("apiVersion", true)
	e.stringValue(RuntimeContractAPIVersion)
	e.member("operations", false)
	e.operationsValue(model.operations)
	e.member("schemas", false)
	e.schemasValue(model.schemas)
	e.member("trace", false)
	e.traceValue(model.trace)
	e.endObject()
}

func (e *runtimeContractEncoder) traceValue(trace runtimeContractTraceDocument) {
	e.beginObject()
	e.member("apiManifestCanonicalDigest", true)
	e.stringValue(trace.APIManifestCanonicalDigest)
	e.member("apiManifestVersion", false)
	e.stringValue(trace.APIManifestVersion)
	e.member("sourceDigest", false)
	e.stringValue(trace.SourceDigest)
	e.endObject()
}

func (e *runtimeContractEncoder) schemasValue(schemas []runtimeSchema) {
	e.beginArray()
	for index, schema := range schemas {
		if index != 0 {
			e.writeByte(',')
		}
		e.schemaValue(schema)
	}
	e.endArray()
}

func (e *runtimeContractEncoder) schemaValue(schema runtimeSchema) {
	e.beginObject()
	switch schema.kind {
	case "array":
		e.member("items", true)
		e.intValue(schema.items)
		e.member("kind", false)
		e.stringValue(string(schema.kind))
	case "object":
		e.member("fields", true)
		e.fieldsValue(schema.fields)
		e.member("kind", false)
		e.stringValue(string(schema.kind))
	default:
		e.member("kind", true)
		e.stringValue(string(schema.kind))
	}
	e.endObject()
}

func (e *runtimeContractEncoder) fieldsValue(fields map[string]runtimeField) {
	e.beginObject()
	for index, name := range sortedRuntimeMapKeys(fields) {
		e.member(name, index == 0)
		field := fields[name]
		e.beginObject()
		e.member("required", true)
		e.boolValue(field.required)
		e.member("schema", false)
		e.intValue(field.schema)
		e.endObject()
	}
	e.endObject()
}

func (e *runtimeContractEncoder) operationsValue(operations map[string]runtimeOperation) {
	e.beginObject()
	for index, id := range sortedRuntimeMapKeys(operations) {
		e.member(id, index == 0)
		e.operationValue(operations[id])
	}
	e.endObject()
}

func (e *runtimeContractEncoder) operationValue(operation runtimeOperation) {
	e.beginObject()
	e.member("auth", true)
	e.authValue(operation.auth)
	if operation.capability != nil {
		e.member("capability", false)
		e.capabilityValue(*operation.capability)
	}
	e.member("errorProjections", false)
	e.errorProjectionsValue(operation.errorProjections)
	e.member("method", false)
	e.stringValue(string(operation.method))
	e.member("pathSegments", false)
	e.pathSegmentsValue(operation.pathSegments)
	e.member("permission", false)
	e.stringValue(operation.permission)
	e.member("request", false)
	e.requestValue(operation.request)
	e.member("response", false)
	e.responseValue(operation.response)
	e.endObject()
}

func (e *runtimeContractEncoder) authValue(auth runtimeAuth) {
	e.beginObject()
	e.member("credentials", true)
	e.credentialsValue(auth.credentials)
	e.member("mode", false)
	e.stringValue(string(auth.mode))
	e.endObject()
}

func (e *runtimeContractEncoder) credentialsValue(credentials map[string]runtimeCredential) {
	e.beginObject()
	for index, id := range sortedRuntimeMapKeys(credentials) {
		e.member(id, index == 0)
		credential := credentials[id]
		e.beginObject()
		e.member("in", true)
		e.stringValue(string(credential.location))
		e.member("name", false)
		e.stringValue(credential.name)
		e.member("type", false)
		e.stringValue(string(credential.typeID))
		e.endObject()
	}
	e.endObject()
}

func (e *runtimeContractEncoder) capabilityValue(capability runtimeCapability) {
	e.beginObject()
	e.member("apiVersion", true)
	e.stringValue(capability.apiVersion)
	e.member("id", false)
	e.stringValue(capability.id)
	e.endObject()
}

func (e *runtimeContractEncoder) errorProjectionsValue(projections map[string]map[string]runtimeErrorTarget) {
	e.beginObject()
	for domainIndex, domain := range sortedRuntimeMapKeys(projections) {
		e.member(domain, domainIndex == 0)
		codes := projections[domain]
		e.beginObject()
		for codeIndex, code := range sortedRuntimeMapKeys(codes) {
			e.member(code, codeIndex == 0)
			target := codes[code]
			e.beginObject()
			e.member("code", true)
			e.stringValue(target.code)
			e.member("domain", false)
			e.stringValue(target.domain)
			e.member("httpStatus", false)
			e.intValue(target.httpStatus)
			e.endObject()
		}
		e.endObject()
	}
	e.endObject()
}

func (e *runtimeContractEncoder) pathSegmentsValue(segments []runtimePathSegment) {
	e.beginArray()
	for index, segment := range segments {
		if index != 0 {
			e.writeByte(',')
		}
		e.beginObject()
		if segment.field != "" {
			e.member("field", true)
			e.stringValue(segment.field)
		} else {
			e.member("literal", true)
			e.stringValue(segment.literal)
		}
		e.endObject()
	}
	e.endArray()
}

func (e *runtimeContractEncoder) requestValue(request runtimeRequest) {
	e.beginObject()
	e.member("bindings", true)
	e.bindingsValue(request.bindings)
	e.member("schema", false)
	e.intValue(request.schema)
	e.endObject()
}

func (e *runtimeContractEncoder) bindingsValue(bindings map[string]runtimeBinding) {
	e.beginObject()
	for index, field := range sortedRuntimeMapKeys(bindings) {
		e.member(field, index == 0)
		binding := bindings[field]
		e.beginObject()
		e.member("in", true)
		e.stringValue(string(binding.location))
		e.member("name", false)
		e.stringValue(binding.name)
		e.endObject()
	}
	e.endObject()
}

func (e *runtimeContractEncoder) responseValue(response runtimeResponse) {
	e.beginObject()
	e.member("body", true)
	e.stringValue(string(response.body))
	if response.hasSchema {
		e.member("schema", false)
		e.intValue(response.schema)
	}
	e.endObject()
}

func (e *runtimeContractEncoder) beginObject() {
	e.node()
	e.writeByte('{')
}

func (e *runtimeContractEncoder) endObject() { e.writeByte('}') }

func (e *runtimeContractEncoder) beginArray() {
	e.node()
	e.writeByte('[')
}

func (e *runtimeContractEncoder) endArray() { e.writeByte(']') }

func (e *runtimeContractEncoder) member(name string, first bool) {
	if !first {
		e.writeByte(',')
	}
	writeCanonicalJSONString(e, name)
	e.writeByte(':')
}

func (e *runtimeContractEncoder) stringValue(value string) {
	e.node()
	writeCanonicalJSONString(e, value)
}

func (e *runtimeContractEncoder) intValue(value int) {
	e.node()
	var buffer [32]byte
	e.writeString(string(strconv.AppendInt(buffer[:0], int64(value), 10)))
}

func (e *runtimeContractEncoder) boolValue(value bool) {
	e.node()
	if value {
		e.writeString("true")
		return
	}
	e.writeString("false")
}

func (e *runtimeContractEncoder) node() {
	if e.nodes <= e.nodeLimit {
		e.nodes++
	}
}

func (e *runtimeContractEncoder) writeByte(value byte) bool {
	if e.rawBytes > e.rawLimit {
		return false
	}
	e.rawBytes++
	if e.collect {
		e.output = append(e.output, value)
	}
	return e.rawBytes <= e.rawLimit
}

func (e *runtimeContractEncoder) writeString(value string) bool {
	if e.rawBytes > e.rawLimit {
		return false
	}
	remaining := e.rawLimit + 1 - e.rawBytes
	if len(value) >= remaining {
		if e.collect {
			e.output = append(e.output, value[:remaining]...)
		}
		e.rawBytes = e.rawLimit + 1
		return false
	}
	e.rawBytes += len(value)
	if e.collect {
		e.output = append(e.output, value...)
	}
	return true
}

type runtimeCanonicalSink interface {
	writeByte(byte) bool
	writeString(string) bool
}

func writeCanonicalJSONString(sink runtimeCanonicalSink, value string) bool {
	if !sink.writeByte('"') {
		return false
	}
	start := 0
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 0x20 && character != '"' && character != '\\' {
			continue
		}
		if !sink.writeString(value[start:index]) {
			return false
		}
		switch character {
		case '"', '\\':
			if !sink.writeString("\\" + string(character)) {
				return false
			}
		case '\b':
			if !sink.writeString("\\b") {
				return false
			}
		case '\f':
			if !sink.writeString("\\f") {
				return false
			}
		case '\n':
			if !sink.writeString("\\n") {
				return false
			}
		case '\r':
			if !sink.writeString("\\r") {
				return false
			}
		case '\t':
			if !sink.writeString("\\t") {
				return false
			}
		default:
			const hex = "0123456789abcdef"
			escaped := [6]byte{'\\', 'u', '0', '0', hex[character>>4], hex[character&0x0f]}
			if !sink.writeString(string(escaped[:])) {
				return false
			}
		}
		start = index + 1
	}
	return sink.writeString(value[start:]) && sink.writeByte('"')
}

func sortedRuntimeMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool { return lessUTF16(keys[left], keys[right]) })
	return keys
}

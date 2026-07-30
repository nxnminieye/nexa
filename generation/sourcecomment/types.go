package sourcecomment

import (
	"encoding/json"
	"strconv"
)

type Stage string

const (
	StageEnt       Stage = "ent"
	StageProto     Stage = "proto"
	StageAPI       Stage = "api"
	StagePage      Stage = "page"
	StageGenerated Stage = "generated"
)

func (s Stage) order() int {
	switch s {
	case StageEnt:
		return 0
	case StageProto:
		return 1
	case StageAPI:
		return 2
	case StagePage:
		return 3
	case StageGenerated:
		return 4
	default:
		return -1
	}
}

type NodeKind string

const (
	NodeSchema       NodeKind = "schema"
	NodeField        NodeKind = "field"
	NodeMessage      NodeKind = "message"
	NodeProtoField   NodeKind = "proto-field"
	NodeRPC          NodeKind = "rpc"
	NodeAPIType      NodeKind = "api-type"
	NodeAPIField     NodeKind = "api-field"
	NodeAPIOperation NodeKind = "api-operation"
	NodePage         NodeKind = "page"
	NodePageField    NodeKind = "page-field"
)

type Location struct {
	File   string
	Line   int
	Column int
}

type Target struct {
	SemanticID string
	Kind       NodeKind
	Stage      Stage
	Source     SourceRef
}

type ValueKind string

const (
	ValueString  ValueKind = "string"
	ValueBoolean ValueKind = "boolean"
	ValueInteger ValueKind = "integer"
	ValueList    ValueKind = "list"
	ValueObject  ValueKind = "object"
)

type Reference struct{ Target, Display string }

type Value struct {
	kind      ValueKind
	text      string
	boolean   bool
	integer   int64
	elements  []Value
	reference Reference
}

func StringValue(value string) Value { return Value{kind: ValueString, text: value} }
func BooleanValue(value bool) Value  { return Value{kind: ValueBoolean, boolean: value} }
func IntegerValue(value int64) Value { return Value{kind: ValueInteger, integer: value} }
func ListValue(values ...Value) Value {
	return Value{kind: ValueList, elements: cloneValues(values)}
}
func ReferenceValue(target, display string) Value {
	return Value{kind: ValueObject, reference: Reference{Target: target, Display: display}}
}

func (v Value) Kind() ValueKind { return v.kind }
func (v Value) String() (string, bool) {
	return v.text, v.kind == ValueString
}
func (v Value) Boolean() (bool, bool)  { return v.boolean, v.kind == ValueBoolean }
func (v Value) Integer() (int64, bool) { return v.integer, v.kind == ValueInteger }
func (v Value) Elements() ([]Value, bool) {
	if v.kind != ValueList {
		return nil, false
	}
	return cloneValues(v.elements), true
}
func (v Value) Reference() (Reference, bool) { return v.reference, v.kind == ValueObject }

func (v Value) canonical() any {
	switch v.kind {
	case ValueString:
		return v.text
	case ValueBoolean:
		return v.boolean
	case ValueInteger:
		return v.integer
	case ValueList:
		values := make([]any, len(v.elements))
		for index, item := range v.elements {
			values[index] = item.canonical()
		}
		return values
	case ValueObject:
		return struct {
			Target  string `json:"target"`
			Display string `json:"display"`
		}{Target: v.reference.Target, Display: v.reference.Display}
	default:
		return nil
	}
}

func (v Value) display() string {
	encoded, err := json.Marshal(v.canonical())
	if err != nil {
		return ""
	}
	return string(encoded)
}

func cloneValues(input []Value) []Value {
	result := make([]Value, len(input))
	for index, value := range input {
		result[index] = value
		result[index].elements = cloneValues(value.elements)
	}
	return result
}

type Directive struct {
	key      string
	value    Value
	location Location
}

func (d Directive) Key() string        { return d.key }
func (d Directive) Value() Value       { return cloneValue(d.value) }
func (d Directive) Location() Location { return d.location }
func cloneValue(value Value) Value     { value.elements = cloneValues(value.elements); return value }
func canonicalInteger(value json.Number) (int64, bool) {
	if value.String() == "-0" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(value.String(), 10, 64)
	return parsed, err == nil
}

package api

import (
	"fmt"
	"sort"

	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	ValueObject   = "object"
	ValueScalar   = "scalar"
	ValueRef      = "ref"
	ValueArray    = "array"
	ValueOptional = "optional"
)

// Closure is the canonical operation/type subset needed by a frontend page.
// It is an in-process input to the frontend builder, not a serialized HTTP snapshot.
type Closure struct {
	ConventionValue string
	FactGraphValue  sourcecomment.FactGraph
	Types           []ClosureType
	Operations      []ClosureOperation
	Sources         []provenance.Source
}

type ClosureType struct {
	NameValue    string
	ShapeValue   ClosureValue
	FieldsValue  []ClosureField
	SourcesValue []provenance.Source
}

type ClosureField struct {
	PathValue      []string
	RequiredValue  bool
	ValueTypeValue ClosureValue
	SourcesValue   []provenance.Source
}

type ClosureValue struct {
	KindValue       string
	NameValue       string
	ElementValuePtr *ClosureValue
}

type ClosureOperation struct {
	IDValue           string
	MethodValue       HTTPMethod
	PathValue         string
	AuthValue         AuthMode
	PermissionValue   string
	RequestTypeValue  string
	ResponseTypeValue string
	SourcesValue      []provenance.Source
}

// MergeClosures combines independently authored canonical API closures for one
// frontend application. It never changes operation or field semantics.
func MergeClosures(inputs ...Closure) (Closure, error) {
	if len(inputs) == 0 {
		return Closure{}, fmt.Errorf("at least one API closure is required")
	}
	result := Closure{ConventionValue: inputs[0].Convention()}
	graphs := make([]sourcecomment.FactGraph, 0, len(inputs))
	types := map[string]bool{}
	operations := map[string]bool{}
	sources := map[string]provenance.Source{}
	for index, input := range inputs {
		if input.Convention() == "" || input.Convention() != result.ConventionValue {
			return Closure{}, fmt.Errorf("API closure %d uses a different HTTP Convention", index)
		}
		graphs = append(graphs, input.FactGraph())
		for _, value := range input.Types {
			if types[value.Name()] {
				return Closure{}, fmt.Errorf("API closure type %s is duplicated", value.Name())
			}
			types[value.Name()] = true
			result.Types = append(result.Types, cloneClosureType(value))
		}
		for _, operation := range input.Operations {
			if operations[operation.ID()] {
				return Closure{}, fmt.Errorf("API closure operation %s is duplicated", operation.ID())
			}
			operations[operation.ID()] = true
			result.Operations = append(result.Operations, cloneClosureOperation(operation))
		}
		for _, source := range input.SourcesCopy() {
			sources[source.Ref.String()] = source
		}
	}
	facts, diagnostics := sourcecomment.MergeGraphs(sourcecomment.StandardRegistry(), graphs...)
	if len(diagnostics) != 0 {
		return Closure{}, fmt.Errorf("merge API closure facts: %s: %s", diagnostics[0].Code, diagnostics[0].Suggestion)
	}
	result.FactGraphValue = facts
	for _, source := range sources {
		result.Sources = append(result.Sources, source)
	}
	sort.Slice(result.Types, func(i, j int) bool { return result.Types[i].Name() < result.Types[j].Name() })
	sort.Slice(result.Operations, func(i, j int) bool { return result.Operations[i].ID() < result.Operations[j].ID() })
	sort.Slice(result.Sources, func(i, j int) bool { return result.Sources[i].Ref.String() < result.Sources[j].Ref.String() })
	return result, nil
}

func (d Closure) Operation(id string) (ClosureOperation, bool) {
	for _, operation := range d.Operations {
		if operation.ID() == id {
			return cloneClosureOperation(operation), true
		}
	}
	return ClosureOperation{}, false
}

func (d Closure) Convention() string { return d.ConventionValue }

func (d Closure) FactGraph() sourcecomment.FactGraph { return d.FactGraphValue }

func (d Closure) Type(name string) (ClosureType, bool) {
	for _, value := range d.Types {
		if value.Name() == name {
			return cloneClosureType(value), true
		}
	}
	return ClosureType{}, false
}

func (d Closure) TypesCopy() []ClosureType {
	result := make([]ClosureType, len(d.Types))
	for index, value := range d.Types {
		result[index] = cloneClosureType(value)
	}
	return result
}

func (d Closure) SourcesCopy() []provenance.Source {
	return append([]provenance.Source(nil), d.Sources...)
}

func (t ClosureType) Field(path string) (ClosureField, bool) {
	for _, field := range t.FieldsValue {
		if joinClosurePath(field.Path()) == path {
			return cloneClosureField(field), true
		}
	}
	return ClosureField{}, false
}

func (t ClosureType) FieldsCopy() []ClosureField {
	result := make([]ClosureField, len(t.FieldsValue))
	for index, field := range t.FieldsValue {
		result[index] = cloneClosureField(field)
	}
	return result
}

func (t ClosureType) Name() string           { return t.NameValue }
func (t ClosureType) Shape() ClosureValue    { return cloneClosureValue(t.ShapeValue) }
func (t ClosureType) Fields() []ClosureField { return t.FieldsCopy() }
func (t ClosureType) Sources() []provenance.Source {
	return append([]provenance.Source(nil), t.SourcesValue...)
}
func (f ClosureField) Path() []string          { return append([]string(nil), f.PathValue...) }
func (f ClosureField) Required() bool          { return f.RequiredValue }
func (f ClosureField) ValueType() ClosureValue { return cloneClosureValue(f.ValueTypeValue) }
func (f ClosureField) Sources() []provenance.Source {
	return append([]provenance.Source(nil), f.SourcesValue...)
}
func (v ClosureValue) Kind() string { return v.KindValue }
func (v ClosureValue) Name() string { return v.NameValue }
func (v ClosureValue) Element() (ClosureValue, bool) {
	if v.ElementValuePtr == nil {
		return ClosureValue{}, false
	}
	return cloneClosureValue(*v.ElementValuePtr), true
}

func (o ClosureOperation) ID() string           { return o.IDValue }
func (o ClosureOperation) Method() HTTPMethod   { return o.MethodValue }
func (o ClosureOperation) Path() string         { return o.PathValue }
func (o ClosureOperation) Auth() AuthMode       { return o.AuthValue }
func (o ClosureOperation) Permission() string   { return o.PermissionValue }
func (o ClosureOperation) RequestType() string  { return o.RequestTypeValue }
func (o ClosureOperation) ResponseType() string { return o.ResponseTypeValue }
func (o ClosureOperation) Sources() []provenance.Source {
	return append([]provenance.Source(nil), o.SourcesValue...)
}

func joinClosurePath(path []string) string {
	result := ""
	for index, part := range path {
		if index > 0 {
			result += "."
		}
		result += part
	}
	return result
}

func cloneClosureOperation(input ClosureOperation) ClosureOperation {
	result := input
	result.SourcesValue = append([]provenance.Source(nil), input.SourcesValue...)
	return result
}

func cloneClosureType(input ClosureType) ClosureType {
	result := input
	result.ShapeValue = cloneClosureValue(input.ShapeValue)
	result.FieldsValue = make([]ClosureField, len(input.FieldsValue))
	for index, field := range input.FieldsValue {
		result.FieldsValue[index] = cloneClosureField(field)
	}
	result.SourcesValue = append([]provenance.Source(nil), input.SourcesValue...)
	return result
}

func cloneClosureField(input ClosureField) ClosureField {
	result := input
	result.PathValue = append([]string(nil), input.PathValue...)
	result.ValueTypeValue = cloneClosureValue(input.ValueTypeValue)
	result.SourcesValue = append([]provenance.Source(nil), input.SourcesValue...)
	return result
}

func cloneClosureValue(input ClosureValue) ClosureValue {
	result := input
	if input.ElementValuePtr != nil {
		element := cloneClosureValue(*input.ElementValuePtr)
		result.ElementValuePtr = &element
	}
	return result
}

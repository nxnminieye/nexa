package sourcecomment

// EntityGraphNode is the typed compiler boundary for facts already validated
// from an Ent source. It is not an authoring representation: adapters parse
// @nexa comments before constructing it, and EntityIR may use it only to
// retain the validated FactGraph across an in-process compiler boundary.
type EntityGraphNode struct {
	SemanticID      string
	Kind            NodeKind
	Source          SourceRef
	Location        Location
	NativeCanonical []byte
	Schema          *SchemaFacts
	Field           *FieldFacts
	CRUD            *CRUDOperations
}

// BuildEntityFactGraph materializes the closed registry facts represented by
// typed EntityIR projections. It deliberately has no string-map input.
func BuildEntityFactGraph(values []EntityGraphNode) (FactGraph, []Diagnostic) {
	nodes := make([]NodeInput, len(values))
	for index, value := range values {
		node := NodeInput{
			SemanticID:      value.SemanticID,
			Kind:            value.Kind,
			Stage:           StageEnt,
			Source:          value.Source,
			Location:        value.Location,
			NativeCanonical: append([]byte(nil), value.NativeCanonical...),
		}
		if value.Schema != nil {
			node.Facts = appendSchemaFactDirectives(node.Facts, *value.Schema, value.Location)
		}
		if value.Field != nil {
			node.Facts = appendFieldFactDirectives(node.Facts, *value.Field, value.Location)
		}
		if value.CRUD != nil {
			operations := value.CRUD.Operations()
			items := make([]Value, len(operations))
			for operationIndex, operation := range operations {
				items[operationIndex] = StringValue(string(operation))
			}
			node.Facts = append(node.Facts, Directive{key: "crud.operations", value: ListValue(items...), location: value.Location})
		}
		nodes[index] = node
	}
	return BuildGraph(StandardRegistry(), BuildInput{Nodes: nodes})
}

func appendSchemaFactDirectives(result []Directive, value SchemaFacts, location Location) []Directive {
	return append(result,
		Directive{key: "label.zh-CN", value: StringValue(value.Label.ZhCN), location: location},
		Directive{key: "label.en-US", value: StringValue(value.Label.EnUS), location: location},
		Directive{key: "description.zh-CN", value: StringValue(value.Description.ZhCN), location: location},
		Directive{key: "description.en-US", value: StringValue(value.Description.EnUS), location: location},
		Directive{key: "scope", value: StringValue(string(value.Scope)), location: location},
	)
}

func appendFieldFactDirectives(result []Directive, value FieldFacts, location Location) []Directive {
	result = append(result,
		Directive{key: "label.zh-CN", value: StringValue(value.Label.ZhCN), location: location},
		Directive{key: "label.en-US", value: StringValue(value.Label.EnUS), location: location},
		Directive{key: "description.zh-CN", value: StringValue(value.Description.ZhCN), location: location},
		Directive{key: "description.en-US", value: StringValue(value.Description.EnUS), location: location},
		Directive{key: "ui.control", value: StringValue(string(value.Control)), location: location},
		Directive{key: "visibility", value: StringValue(string(value.Visibility)), location: location},
	)
	if value.Reference != nil {
		result = append(result, Directive{key: "ui.reference", value: ReferenceValue(value.Reference.Target, value.Reference.Display), location: location})
	}
	if value.CRUD != nil {
		result = append(result,
			Directive{key: "crud.read", value: StringValue(string(value.CRUD.Read)), location: location},
			Directive{key: "crud.mutation", value: StringValue(string(value.CRUD.Mutation)), location: location},
		)
	}
	return result
}

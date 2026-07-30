package httpapi

import "github.com/nxnminieye/nexa/generation/sourcecomment"

func Merge(native Document, generated ...Document) (Document, error) {
	if native.state == nil || !documentHasOnly(native, NodeFactNative) {
		return Document{}, invalid("merge_native_invalid", "", "", "merge requires a native document as its first input")
	}
	types := append([]*typeState(nil), native.state.types...)
	operations := append([]*operationState(nil), native.state.operations...)
	factGraphs := []sourcecomment.FactGraph{native.state.factGraph}
	typeNames, operationIDs, routes := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, item := range types {
		typeNames[item.name] = true
	}
	for _, item := range operations {
		operationIDs[item.id], routes[string(item.method)+"\x00"+item.path] = true, true
	}
	for _, document := range generated {
		if document.state == nil || !documentHasOnly(document, NodeFactGenerated) {
			return Document{}, invalid("merge_generated_invalid", "", "", "merge generated inputs must contain only generated nodes")
		}
		if document.state.factGraph.Valid() {
			factGraphs = append(factGraphs, document.state.factGraph)
		}
		for _, item := range document.state.types {
			if typeNames[item.name] {
				return Document{}, invalid("type_collision", "", "", "merged HTTP API type is duplicated")
			}
			typeNames[item.name], types = true, append(types, item)
		}
		for _, item := range document.state.operations {
			key := string(item.method) + "\x00" + item.path
			if operationIDs[item.id] || routes[key] {
				return Document{}, invalid("operation_collision", "", "", "merged HTTP API operation or route is duplicated")
			}
			operationIDs[item.id], routes[key], operations = true, true, append(operations, item)
		}
	}
	facts, diagnostics := sourcecomment.MergeGraphs(sourcecomment.StandardRegistry(), factGraphs...)
	if len(diagnostics) > 0 {
		return Document{}, invalid("fact_graph_merge_invalid", diagnostics[0].File, "", diagnostics[0].Suggestion)
	}
	return newDocument(types, operations, nil, facts)
}

func documentHasOnly(document Document, kind NodeFactKind) bool {
	for _, item := range document.state.types {
		if item.provenance.kind != kind {
			return false
		}
		for _, field := range item.fields {
			if field.provenance.kind != kind {
				return false
			}
		}
	}
	for _, item := range document.state.operations {
		if item.provenance.kind != kind {
			return false
		}
	}
	return true
}

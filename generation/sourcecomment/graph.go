package sourcecomment

import (
	"bytes"
	"strings"
)

type NodeInput struct {
	SemanticID             string
	Kind                   NodeKind
	Stage                  Stage
	Source                 SourceRef
	Location               Location
	NativeCanonical        []byte
	TransformedIdentifiers []string
	Facts                  []Directive
	SourceDirective        *SourceRef
	SourceLocation         Location
}

type ProjectionExpectation struct {
	Downstream              SourceRef
	Upstream                SourceRef
	SemanticID              string
	Kind                    NodeKind
	ExpectedNativeCanonical []byte
	Location                Location
}

type BuildInput struct {
	Nodes          []NodeInput
	Projections    []ProjectionExpectation
	InheritedFacts []InheritedFactExpectation
}

type InheritedFactExpectation struct {
	ID          FactID
	Value       Value
	FirstSource SourceRef
	Location    Location
}

type FactID struct{ SemanticID, Key string }

func (id FactID) String() string {
	if id.SemanticID == "" || id.Key == "" {
		return ""
	}
	return id.SemanticID + ":" + id.Key
}

type Fact struct {
	id          FactID
	value       Value
	firstSource SourceRef
	location    Location
}

func (f Fact) ID() FactID             { return f.id }
func (f Fact) Value() Value           { return cloneValue(f.value) }
func (f Fact) FirstSource() SourceRef { return f.firstSource }
func (f Fact) Location() Location     { return f.location }

type graphNode struct {
	semanticID  string
	kind        NodeKind
	stage       Stage
	source      SourceRef
	native      []byte
	transformed []string
}
type FactGraph struct {
	nodes     []graphNode
	facts     []Fact
	factIndex map[string]int
	input     BuildInput
	valid     bool
}

func (g FactGraph) Valid() bool { return g.valid }

func (g FactGraph) Facts() []Fact {
	result := make([]Fact, len(g.facts))
	copy(result, g.facts)
	for index := range result {
		result[index].value = cloneValue(result[index].value)
	}
	return result
}
func (g FactGraph) Fact(id FactID) (Fact, bool) {
	index, ok := g.factIndex[id.String()]
	if !ok {
		return Fact{}, false
	}
	value := g.facts[index]
	value.value = cloneValue(value.value)
	return value, true
}

func BuildGraph(registry Registry, input BuildInput) (FactGraph, []Diagnostic) {
	graph := FactGraph{factIndex: map[string]int{}}
	var diagnostics []Diagnostic
	nodesBySource := map[string]NodeInput{}
	nodesByStageIdentity := map[string]NodeInput{}
	identifiers := map[string]NodeInput{}
	sourceFolds := map[string]NodeInput{}
	for _, node := range input.Nodes {
		location := node.Location
		if location.File == "" {
			location.File = node.Source.Path()
		}
		if node.SemanticID == "" || !node.Source.Valid() || node.Stage != node.Source.Stage() || node.Stage == StageGenerated || node.Kind == "" {
			diagnostics = append(diagnostics, diagnostic(CodeInvalidTarget, location, "provide a valid authored semantic node and matching source stage"))
			continue
		}
		if previous, exists := nodesBySource[node.Source.String()]; exists {
			item := diagnostic(CodeSemanticCollision, location, "give every source node one unique canonical source reference")
			item.EarliestSource = previous.Source.String()
			diagnostics = append(diagnostics, item)
			continue
		}
		foldedSource := strings.ToLower(string(node.Source.Stage()) + "://" + node.Source.Path())
		if previous, exists := sourceFolds[foldedSource]; exists && previous.Source.Path() != node.Source.Path() {
			item := diagnostic(CodeSemanticCollision, location, "use source paths with unique canonical casing")
			item.EarliestSource = previous.Source.String()
			diagnostics = append(diagnostics, item)
			continue
		}
		if _, exists := sourceFolds[foldedSource]; !exists {
			sourceFolds[foldedSource] = node
		}
		stageIdentity := string(node.Stage) + "\x00" + strings.ToLower(node.SemanticID)
		if previous, exists := nodesByStageIdentity[stageIdentity]; exists {
			item := diagnostic(CodeSemanticCollision, location, "rename the local node to avoid exact or case-fold identity collision")
			item.EarliestSource = previous.Source.String()
			diagnostics = append(diagnostics, item)
			continue
		}
		nodesBySource[node.Source.String()], nodesByStageIdentity[stageIdentity] = node, node
		for _, identifier := range append([]string{node.SemanticID}, node.TransformedIdentifiers...) {
			key := identifierCollisionScope(node) + "\x00" + strings.ToLower(identifier)
			if previous, exists := identifiers[key]; exists && previous.Source.String() != node.Source.String() {
				item := diagnostic(CodeSemanticCollision, location, "rename the local node to avoid a transformed identifier collision")
				item.EarliestSource = previous.Source.String()
				diagnostics = append(diagnostics, item)
			} else {
				identifiers[key] = node
			}
		}
		graph.nodes = append(graph.nodes, graphNode{semanticID: node.SemanticID, kind: node.Kind, stage: node.Stage, source: node.Source, native: append([]byte(nil), node.NativeCanonical...), transformed: append([]string(nil), node.TransformedIdentifiers...)})
	}

	projected := map[string]ProjectionExpectation{}
	for _, expected := range input.Projections {
		projected[expected.Downstream.String()] = expected
		downstream, present := nodesBySource[expected.Downstream.String()]
		upstream, sourcePresent := nodesBySource[expected.Upstream.String()]
		location := expected.Location
		if location.File == "" {
			location.File = expected.Downstream.Path()
		}
		if !present {
			item := diagnostic(CodeInheritedNodeChanged, location, "restore the projected node and regenerate from its first source")
			item.EarliestSource, item.Expected = expected.Upstream.String(), expected.SemanticID
			diagnostics = append(diagnostics, item)
			continue
		}
		if !sourcePresent {
			item := diagnostic(CodeSourceMismatch, downstream.Location, "include the uniquely resolvable first source in the source graph")
			item.EarliestSource = expected.Upstream.String()
			diagnostics = append(diagnostics, item)
		}
		if downstream.SemanticID != expected.SemanticID || downstream.Kind != expected.Kind || !bytes.Equal(downstream.NativeCanonical, expected.ExpectedNativeCanonical) {
			item := diagnostic(CodeInheritedNodeChanged, downstream.Location, "restore the inherited native structure and modify the first source instead")
			item.Node, item.EarliestSource, item.Expected, item.Actual = downstream.SemanticID, expected.Upstream.String(), string(expected.ExpectedNativeCanonical), string(downstream.NativeCanonical)
			diagnostics = append(diagnostics, item)
		}
		if downstream.SourceDirective == nil || downstream.SourceDirective.String() != expected.Upstream.String() {
			item := diagnostic(CodeSourceMismatch, downstream.SourceLocation, "restore the compiler-generated $source reference")
			item.Node, item.EarliestSource, item.Expected = downstream.SemanticID, expected.Upstream.String(), expected.Upstream.String()
			if downstream.SourceDirective != nil {
				item.Actual = downstream.SourceDirective.String()
			}
			diagnostics = append(diagnostics, item)
		}
		_ = upstream
	}
	for _, node := range input.Nodes {
		if node.SourceDirective != nil {
			if _, expected := projected[node.Source.String()]; !expected {
				item := diagnostic(CodeSourceMismatch, node.SourceLocation, "remove $source from a local node")
				item.Node, item.Actual = node.SemanticID, node.SourceDirective.String()
				diagnostics = append(diagnostics, item)
			}
		}
	}

	ordered := append([]NodeInput(nil), input.Nodes...)
	sortNodeInputs(ordered)
	for _, node := range ordered {
		expectation, inheritedNode := projected[node.Source.String()]
		var upstream NodeInput
		if inheritedNode {
			upstream = nodesBySource[expectation.Upstream.String()]
		}
		seen := map[string]bool{}
		for _, directive := range node.Facts {
			factID := FactID{SemanticID: node.SemanticID, Key: directive.key}
			if seen[directive.key] {
				item := diagnostic(CodeDuplicateFact, directive.location, "remove the duplicate fact declaration")
				item.Node, item.FactID, item.EarliestSource = node.SemanticID, factID.String(), node.Source.String()
				diagnostics = append(diagnostics, item)
				continue
			}
			seen[directive.key] = true
			entry, exists := registry.lookup(directive.key)
			if !exists {
				item := diagnostic(CodeUnknownKey, directive.location, "register the fact in the canonical Nexa contract before authoring it")
				item.FactID = factID.String()
				diagnostics = append(diagnostics, item)
				continue
			}
			if code, suggestion := entry.validate(Target{SemanticID: node.SemanticID, Kind: node.Kind, Stage: node.Stage, Source: node.Source}, directive.value); code != "" {
				item := diagnostic(code, directive.location, suggestion)
				item.FactID = factID.String()
				diagnostics = append(diagnostics, item)
				continue
			}
			if existingIndex, inherited := graph.factIndex[factID.String()]; inherited {
				existing := graph.facts[existingIndex]
				item := diagnostic(CodeInheritedFactChanged, directive.location, "modify the fact at its first source and regenerate downstream")
				item.FactID, item.EarliestSource, item.Expected, item.Actual = factID.String(), existing.firstSource.String(), existing.value.display(), directive.value.display()
				diagnostics = append(diagnostics, item)
				continue
			}
			if inheritedNode && upstream.Source.Valid() {
				if _, allowedUpstream := entry.targets[upstream.Kind]; allowedUpstream && upstream.Stage.order() >= entry.earliest.order() {
					item := diagnostic(CodeMisplacedFact, directive.location, "move the fact to the existing earlier semantic source")
					item.FactID, item.EarliestSource = factID.String(), upstream.Source.String()
					diagnostics = append(diagnostics, item)
					continue
				}
			}
			graph.factIndex[factID.String()] = len(graph.facts)
			graph.facts = append(graph.facts, Fact{id: factID, value: entry.canonicalize(directive.value), firstSource: node.Source, location: directive.location})
		}
	}
	validateHTTPProjectionPairs(&graph, &diagnostics)
	validateReferenceFacts(&graph, &diagnostics)
	for _, expected := range input.InheritedFacts {
		actualIndex, present := graph.factIndex[expected.ID.String()]
		if !present {
			item := diagnostic(CodeInheritedFactChanged, expected.Location, "restore the inherited fact or modify it at its first source")
			item.FactID, item.EarliestSource, item.Expected, item.Actual = expected.ID.String(), expected.FirstSource.String(), expected.Value.display(), "<missing>"
			diagnostics = append(diagnostics, item)
			continue
		}
		actual := graph.facts[actualIndex]
		if actual.firstSource.String() != expected.FirstSource.String() || actual.value.display() != expected.Value.display() {
			item := diagnostic(CodeInheritedFactChanged, expected.Location, "restore the inherited fact or modify it at its first source")
			item.FactID, item.EarliestSource, item.Expected, item.Actual = expected.ID.String(), expected.FirstSource.String(), expected.Value.display(), actual.value.display()
			diagnostics = append(diagnostics, item)
		}
	}
	sortGraph(&graph)
	sortDiagnostics(diagnostics)
	if len(diagnostics) > 0 {
		return FactGraph{}, diagnostics
	}
	graph.input = cloneBuildInput(input)
	graph.valid = true
	return graph, diagnostics
}

func validateHTTPProjectionPairs(graph *FactGraph, diagnostics *[]Diagnostic) {
	type pair struct{ method, path *Fact }
	pairs := map[string]*pair{}
	for index := range graph.facts {
		fact := &graph.facts[index]
		if fact.id.Key != "http.method" && fact.id.Key != "http.path" {
			continue
		}
		value := pairs[fact.id.SemanticID]
		if value == nil {
			value = &pair{}
			pairs[fact.id.SemanticID] = value
		}
		if fact.id.Key == "http.method" {
			value.method = fact
		} else {
			value.path = fact
		}
	}
	for semanticID, value := range pairs {
		if value.method != nil && value.path != nil {
			continue
		}
		present := value.method
		missing := "http.path"
		if present == nil {
			present, missing = value.path, "http.method"
		}
		item := diagnostic(CodeInvalidValue, present.location, "declare http.method and http.path together on the Proto RPC")
		item.Node, item.FactID, item.EarliestSource, item.Expected, item.Actual = semanticID, semanticID+":"+missing, present.firstSource.String(), missing, "<missing>"
		*diagnostics = append(*diagnostics, item)
	}
}

func cloneBuildInput(input BuildInput) BuildInput {
	result := BuildInput{
		Nodes:          make([]NodeInput, len(input.Nodes)),
		Projections:    make([]ProjectionExpectation, len(input.Projections)),
		InheritedFacts: make([]InheritedFactExpectation, len(input.InheritedFacts)),
	}
	for index, node := range input.Nodes {
		result.Nodes[index] = node
		result.Nodes[index].NativeCanonical = append([]byte(nil), node.NativeCanonical...)
		result.Nodes[index].TransformedIdentifiers = append([]string(nil), node.TransformedIdentifiers...)
		result.Nodes[index].Facts = make([]Directive, len(node.Facts))
		for factIndex, fact := range node.Facts {
			result.Nodes[index].Facts[factIndex] = fact
			result.Nodes[index].Facts[factIndex].value = cloneValue(fact.value)
		}
		if node.SourceDirective != nil {
			source := *node.SourceDirective
			result.Nodes[index].SourceDirective = &source
		}
	}
	for index, projection := range input.Projections {
		result.Projections[index] = projection
		result.Projections[index].ExpectedNativeCanonical = append([]byte(nil), projection.ExpectedNativeCanonical...)
	}
	for index, fact := range input.InheritedFacts {
		result.InheritedFacts[index] = fact
		result.InheritedFacts[index].Value = cloneValue(fact.Value)
	}
	return result
}

func identifierCollisionScope(node NodeInput) string {
	scope := string(node.Stage) + "\x00" + string(node.Kind)
	switch node.Kind {
	case NodeField, NodeProtoField, NodeAPIField, NodePageField:
		if separator := strings.LastIndexByte(node.SemanticID, '.'); separator > 0 {
			return scope + "\x00" + strings.ToLower(node.SemanticID[:separator])
		}
	}
	return scope
}

func validateReferenceFacts(graph *FactGraph, diagnostics *[]Diagnostic) {
	nodes := make(map[string][]graphNode)
	for _, node := range graph.nodes {
		nodes[node.semanticID] = append(nodes[node.semanticID], node)
	}
	for _, fact := range graph.facts {
		if fact.id.Key != "ui.reference" {
			continue
		}
		reference, ok := fact.value.Reference()
		if !ok {
			continue
		}
		var sourceStage Stage
		for _, node := range graph.nodes {
			if node.semanticID == fact.id.SemanticID && node.source.String() == fact.firstSource.String() {
				sourceStage = node.stage
				break
			}
		}
		resolve := func(semanticID string, kinds ...NodeKind) bool {
			for _, node := range nodes[semanticID] {
				if sourceStage.order() >= 0 && node.stage.order() > sourceStage.order() {
					continue
				}
				for _, kind := range kinds {
					if node.kind == kind {
						return true
					}
				}
			}
			return false
		}
		if !resolve(reference.Target, NodeSchema, NodeMessage, NodeAPIType) {
			item := diagnostic(CodeInvalidValue, fact.location, "reference an existing canonical target node at the same or an earlier stage")
			item.FactID, item.EarliestSource, item.Expected, item.Actual = fact.id.String(), fact.firstSource.String(), reference.Target, "<unresolved>"
			*diagnostics = append(*diagnostics, item)
			continue
		}
		displayID := reference.Target + "." + reference.Display
		if !resolve(displayID, NodeField, NodeProtoField, NodeAPIField) {
			item := diagnostic(CodeInvalidValue, fact.location, "reference an existing canonical display field on the target node")
			item.FactID, item.EarliestSource, item.Expected, item.Actual = fact.id.String(), fact.firstSource.String(), displayID, "<unresolved>"
			*diagnostics = append(*diagnostics, item)
		}
	}
}

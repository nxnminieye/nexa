package sourcecomment

import "reflect"

// MergeGraphs combines independently validated source graphs and re-runs the
// full registry, first-source, projection, and collision validation.
func MergeGraphs(registry Registry, graphs ...FactGraph) (FactGraph, []Diagnostic) {
	combined := BuildInput{}
	nodes := map[string]NodeInput{}
	projections := map[string]ProjectionExpectation{}
	facts := map[string]InheritedFactExpectation{}
	for _, graph := range graphs {
		if !graph.valid {
			item := diagnostic(CodeInvalidTarget, Location{}, "build and validate every source graph before merging it")
			item.Expected, item.Actual = "validated FactGraph", "invalid FactGraph"
			return FactGraph{}, []Diagnostic{item}
		}
		input := cloneBuildInput(graph.input)
		for _, node := range input.Nodes {
			key := node.Source.String()
			if previous, exists := nodes[key]; exists && reflect.DeepEqual(previous, node) {
				continue
			}
			if _, exists := nodes[key]; !exists {
				nodes[key] = node
			}
			combined.Nodes = append(combined.Nodes, node)
		}
		for _, projection := range input.Projections {
			key := projection.Downstream.String()
			if previous, exists := projections[key]; exists && reflect.DeepEqual(previous, projection) {
				continue
			}
			if _, exists := projections[key]; !exists {
				projections[key] = projection
			}
			combined.Projections = append(combined.Projections, projection)
		}
		for _, fact := range input.InheritedFacts {
			key := fact.ID.String()
			if previous, exists := facts[key]; exists && reflect.DeepEqual(previous, fact) {
				continue
			}
			if _, exists := facts[key]; !exists {
				facts[key] = fact
			}
			combined.InheritedFacts = append(combined.InheritedFacts, fact)
		}
	}
	return BuildGraph(registry, combined)
}

package sourcecomment

// MergeGraphs combines independently validated source graphs and re-runs the
// full registry, first-source, projection, and collision validation.
func MergeGraphs(registry Registry, graphs ...FactGraph) (FactGraph, []Diagnostic) {
	combined := BuildInput{}
	for _, graph := range graphs {
		if !graph.valid {
			item := diagnostic(CodeInvalidTarget, Location{}, "build and validate every source graph before merging it")
			item.Expected, item.Actual = "validated FactGraph", "invalid FactGraph"
			return FactGraph{}, []Diagnostic{item}
		}
		input := cloneBuildInput(graph.input)
		combined.Nodes = append(combined.Nodes, input.Nodes...)
		combined.Projections = append(combined.Projections, input.Projections...)
		combined.InheritedFacts = append(combined.InheritedFacts, input.InheritedFacts...)
	}
	return BuildGraph(registry, combined)
}

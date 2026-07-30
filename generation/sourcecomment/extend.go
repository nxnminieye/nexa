package sourcecomment

// ExtendGraph composes a validated upstream graph with nodes and facts authored
// at the next source stage. All validation is delegated to BuildGraph so an
// extension cannot bypass projection, first-source, or collision rules.
func ExtendGraph(registry Registry, upstream FactGraph, input BuildInput) (FactGraph, []Diagnostic) {
	if !upstream.valid {
		location := Location{}
		if len(input.Nodes) > 0 {
			location = input.Nodes[0].Location
		}
		item := diagnostic(CodeInvalidTarget, location, "build and validate the upstream fact graph before extending it")
		item.Expected, item.Actual = "validated upstream FactGraph", "invalid FactGraph"
		return FactGraph{}, []Diagnostic{item}
	}

	combined := cloneBuildInput(upstream.input)
	downstream := cloneBuildInput(input)
	combined.Nodes = append(combined.Nodes, downstream.Nodes...)
	combined.Projections = append(combined.Projections, downstream.Projections...)
	combined.InheritedFacts = append(combined.InheritedFacts, downstream.InheritedFacts...)
	return BuildGraph(registry, combined)
}

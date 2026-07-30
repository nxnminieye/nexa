package sourcecomment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

type canonicalDocument struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Nodes      []canonicalNode `json:"nodes"`
	Facts      []canonicalFact `json:"facts"`
}
type canonicalNode struct {
	SemanticID   string   `json:"semanticID"`
	Kind         NodeKind `json:"kind"`
	Stage        Stage    `json:"stage"`
	Source       string   `json:"source"`
	NativeDigest string   `json:"nativeDigest"`
	Identifiers  []string `json:"identifiers,omitempty"`
}
type canonicalFact struct {
	FactID      string `json:"factID"`
	SemanticID  string `json:"semanticID"`
	Key         string `json:"key"`
	Value       any    `json:"value"`
	FirstSource string `json:"firstSource"`
}

func (g FactGraph) CanonicalJSON() ([]byte, error) {
	if !g.valid {
		return nil, errors.New("source comment fact graph is invalid")
	}
	document := canonicalDocument{APIVersion: Contract, Kind: "FactGraph", Nodes: make([]canonicalNode, len(g.nodes)), Facts: make([]canonicalFact, len(g.facts))}
	for index, node := range g.nodes {
		identifiers := append([]string(nil), node.transformed...)
		sort.Strings(identifiers)
		nativeDigest := sha256.Sum256(node.native)
		document.Nodes[index] = canonicalNode{SemanticID: node.semanticID, Kind: node.kind, Stage: node.stage, Source: node.source.String(), NativeDigest: "sha256:" + hex.EncodeToString(nativeDigest[:]), Identifiers: identifiers}
	}
	for index, fact := range g.facts {
		document.Facts[index] = canonicalFact{FactID: fact.id.String(), SemanticID: fact.id.SemanticID, Key: fact.id.Key, Value: fact.value.canonical(), FirstSource: fact.firstSource.String()}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func (g FactGraph) Digest() (string, error) {
	canonical, err := g.CanonicalJSON()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func sortNodeInputs(values []NodeInput) {
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].Stage.order() != values[j].Stage.order() {
			return values[i].Stage.order() < values[j].Stage.order()
		}
		return values[i].Source.String() < values[j].Source.String()
	})
}
func sortGraph(graph *FactGraph) {
	sort.SliceStable(graph.nodes, func(i, j int) bool { return graph.nodes[i].source.String() < graph.nodes[j].source.String() })
	sort.SliceStable(graph.facts, func(i, j int) bool { return graph.facts[i].id.String() < graph.facts[j].id.String() })
	graph.factIndex = make(map[string]int, len(graph.facts))
	for index, fact := range graph.facts {
		graph.factIndex[fact.id.String()] = index
	}
}

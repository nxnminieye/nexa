package sourcecomment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

const ProjectionLockContract = "nexa.dev/source-projection-lock/v1"

type ProjectionLockNode struct {
	Downstream   SourceRef
	FirstSource  SourceRef
	SemanticID   string
	Kind         NodeKind
	NativeDigest string
}

type ProjectionLockFact struct {
	ID          FactID
	FirstSource SourceRef
	ValueDigest string
}

type ProjectionLock struct {
	nodes []ProjectionLockNode
	facts []ProjectionLockFact
	valid bool
}

type projectionLockDocument struct {
	APIVersion string                       `json:"apiVersion"`
	Kind       string                       `json:"kind"`
	Nodes      []projectionLockNodeDocument `json:"nodes"`
	Facts      []projectionLockFactDocument `json:"facts"`
}

type projectionLockNodeDocument struct {
	Downstream   string   `json:"downstream"`
	FirstSource  string   `json:"firstSource"`
	SemanticID   string   `json:"semanticID"`
	Kind         NodeKind `json:"kind"`
	NativeDigest string   `json:"nativeDigest"`
}

type projectionLockFactDocument struct {
	FactID      string `json:"factID"`
	FirstSource string `json:"firstSource"`
	ValueDigest string `json:"valueDigest"`
}

// ProjectionLock returns the single lock for every inherited projection and
// fact already validated as part of this graph.
func (g FactGraph) ProjectionLock() (ProjectionLock, error) {
	if !g.valid {
		return ProjectionLock{}, errors.New("fact graph is invalid")
	}
	return NewProjectionLock(g.input.Projections, g.input.InheritedFacts)
}

func NewProjectionLock(projections []ProjectionExpectation, facts []InheritedFactExpectation) (ProjectionLock, error) {
	lock := ProjectionLock{nodes: make([]ProjectionLockNode, len(projections)), facts: make([]ProjectionLockFact, len(facts))}
	for index, value := range projections {
		if !value.Downstream.Valid() || !value.Upstream.Valid() || value.SemanticID == "" || value.Kind == "" || value.Downstream.Stage().order() <= value.Upstream.Stage().order() {
			return ProjectionLock{}, fmt.Errorf("projection lock node %d is invalid", index)
		}
		lock.nodes[index] = ProjectionLockNode{
			Downstream: value.Downstream, FirstSource: value.Upstream, SemanticID: value.SemanticID,
			Kind: value.Kind, NativeDigest: digestBytes(value.ExpectedNativeCanonical),
		}
	}
	for index, value := range facts {
		if value.ID.String() == "" || !value.FirstSource.Valid() || value.Value.Kind() == "" {
			return ProjectionLock{}, fmt.Errorf("projection lock fact %d is invalid", index)
		}
		encoded, err := json.Marshal(value.Value.canonical())
		if err != nil {
			return ProjectionLock{}, err
		}
		lock.facts[index] = ProjectionLockFact{ID: value.ID, FirstSource: value.FirstSource, ValueDigest: digestBytes(encoded)}
	}
	if err := validateProjectionLock(&lock); err != nil {
		return ProjectionLock{}, err
	}
	lock.valid = true
	return lock, nil
}

func ParseProjectionLock(data []byte) (ProjectionLock, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document projectionLockDocument
	if err := decoder.Decode(&document); err != nil {
		return ProjectionLock{}, fmt.Errorf("decode projection lock: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ProjectionLock{}, errors.New("decode projection lock: trailing JSON")
	}
	if document.APIVersion != ProjectionLockContract || document.Kind != "SourceProjectionLock" || document.Nodes == nil || document.Facts == nil {
		return ProjectionLock{}, errors.New("projection lock identity is invalid")
	}
	lock := ProjectionLock{nodes: make([]ProjectionLockNode, len(document.Nodes)), facts: make([]ProjectionLockFact, len(document.Facts))}
	for index, value := range document.Nodes {
		downstream, err := ParseSourceRef(value.Downstream)
		if err != nil {
			return ProjectionLock{}, fmt.Errorf("projection lock node %d downstream: %w", index, err)
		}
		firstSource, err := ParseSourceRef(value.FirstSource)
		if err != nil {
			return ProjectionLock{}, fmt.Errorf("projection lock node %d first source: %w", index, err)
		}
		lock.nodes[index] = ProjectionLockNode{Downstream: downstream, FirstSource: firstSource, SemanticID: value.SemanticID, Kind: value.Kind, NativeDigest: value.NativeDigest}
	}
	for index, value := range document.Facts {
		semanticID, key, ok := splitFactID(value.FactID)
		if !ok {
			return ProjectionLock{}, fmt.Errorf("projection lock fact %d id is invalid", index)
		}
		firstSource, err := ParseSourceRef(value.FirstSource)
		if err != nil {
			return ProjectionLock{}, fmt.Errorf("projection lock fact %d first source: %w", index, err)
		}
		lock.facts[index] = ProjectionLockFact{ID: FactID{SemanticID: semanticID, Key: key}, FirstSource: firstSource, ValueDigest: value.ValueDigest}
	}
	if err := validateProjectionLock(&lock); err != nil {
		return ProjectionLock{}, err
	}
	lock.valid = true
	canonical, err := lock.CanonicalJSON()
	if err != nil {
		return ProjectionLock{}, err
	}
	if !bytes.Equal(data, canonical) {
		return ProjectionLock{}, errors.New("projection lock is not canonical")
	}
	return lock, nil
}

func (l ProjectionLock) Nodes() []ProjectionLockNode {
	return append([]ProjectionLockNode(nil), l.nodes...)
}

func (l ProjectionLock) Facts() []ProjectionLockFact {
	return append([]ProjectionLockFact(nil), l.facts...)
}

// ValidateFactGraph verifies the projection identities recorded when a
// generated artifact was produced. Local downstream nodes are intentionally
// outside this lock; only inherited nodes and first-source facts are checked.
func (l ProjectionLock) ValidateFactGraph(graph FactGraph) error {
	if !l.valid || !graph.valid {
		return errors.New("projection lock or fact graph is invalid")
	}
	nodes := make(map[string]graphNode, len(graph.nodes))
	for _, node := range graph.nodes {
		nodes[node.source.String()] = node
	}
	inputs := make(map[string]NodeInput, len(graph.input.Nodes))
	for _, node := range graph.input.Nodes {
		inputs[node.Source.String()] = node
	}
	projections := make(map[string]ProjectionExpectation, len(graph.input.Projections))
	for _, projection := range graph.input.Projections {
		projections[projection.Downstream.String()] = projection
	}
	for _, expected := range l.nodes {
		if _, present := nodes[expected.FirstSource.String()]; !present {
			return fmt.Errorf("first source %s for projected node %s is missing", expected.FirstSource.String(), expected.Downstream.String())
		}
		projection, present := projections[expected.Downstream.String()]
		if !present || projection.Upstream.String() != expected.FirstSource.String() {
			return fmt.Errorf("projected node %s does not retain first source %s", expected.Downstream.String(), expected.FirstSource.String())
		}
		downstreamInput, present := inputs[expected.Downstream.String()]
		if !present || downstreamInput.SourceDirective == nil || downstreamInput.SourceDirective.String() != expected.FirstSource.String() {
			return fmt.Errorf("projected node %s has an invalid $source for %s", expected.Downstream.String(), expected.FirstSource.String())
		}
		actual, present := nodes[expected.Downstream.String()]
		if !present {
			return fmt.Errorf("projected node %s is missing", expected.Downstream.String())
		}
		if actual.semanticID != expected.SemanticID || actual.kind != expected.Kind || digestBytes(actual.native) != expected.NativeDigest {
			return fmt.Errorf("projected node %s differs from its first source %s", expected.Downstream.String(), expected.FirstSource.String())
		}
	}
	for _, expected := range l.facts {
		actual, present := graph.Fact(expected.ID)
		if !present {
			return fmt.Errorf("inherited fact %s is missing", expected.ID.String())
		}
		encoded, err := json.Marshal(actual.Value().canonical())
		if err != nil {
			return err
		}
		if actual.FirstSource().String() != expected.FirstSource.String() || digestBytes(encoded) != expected.ValueDigest {
			return fmt.Errorf("inherited fact %s differs from its first source %s", expected.ID.String(), expected.FirstSource.String())
		}
	}
	return nil
}

func (l ProjectionLock) CanonicalJSON() ([]byte, error) {
	if !l.valid {
		return nil, errors.New("projection lock is invalid")
	}
	document := projectionLockDocument{APIVersion: ProjectionLockContract, Kind: "SourceProjectionLock", Nodes: make([]projectionLockNodeDocument, len(l.nodes)), Facts: make([]projectionLockFactDocument, len(l.facts))}
	for index, value := range l.nodes {
		document.Nodes[index] = projectionLockNodeDocument{Downstream: value.Downstream.String(), FirstSource: value.FirstSource.String(), SemanticID: value.SemanticID, Kind: value.Kind, NativeDigest: value.NativeDigest}
	}
	for index, value := range l.facts {
		document.Facts[index] = projectionLockFactDocument{FactID: value.ID.String(), FirstSource: value.FirstSource.String(), ValueDigest: value.ValueDigest}
	}
	return appendMustNewline(json.Marshal(document))
}

func validateProjectionLock(lock *ProjectionLock) error {
	sort.Slice(lock.nodes, func(i, j int) bool { return lock.nodes[i].Downstream.String() < lock.nodes[j].Downstream.String() })
	sort.Slice(lock.facts, func(i, j int) bool { return lock.facts[i].ID.String() < lock.facts[j].ID.String() })
	seenNodes := map[string]bool{}
	for index, value := range lock.nodes {
		if !value.Downstream.Valid() || !value.FirstSource.Valid() || value.SemanticID == "" || value.Kind == "" || value.Downstream.Stage().order() <= value.FirstSource.Stage().order() || !validDigest(value.NativeDigest) || seenNodes[value.Downstream.String()] {
			return fmt.Errorf("projection lock node %d is invalid", index)
		}
		seenNodes[value.Downstream.String()] = true
	}
	seenFacts := map[string]bool{}
	for index, value := range lock.facts {
		if value.ID.String() == "" || !value.FirstSource.Valid() || !validDigest(value.ValueDigest) || seenFacts[value.ID.String()] {
			return fmt.Errorf("projection lock fact %d is invalid", index)
		}
		seenFacts[value.ID.String()] = true
	}
	return nil
}

func splitFactID(value string) (string, string, bool) {
	for index := len(value) - 1; index > 0; index-- {
		if value[index] == ':' && index < len(value)-1 {
			return value[:index], value[index+1:], true
		}
	}
	return "", "", false
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	_, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil
}

func appendMustNewline(value []byte, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	return append(value, '\n'), nil
}

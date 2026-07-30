package sourcecomment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const FrontendSourceContract = "nexa.dev/frontend-source/v1"

var pageIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

type frontendSourceDocument struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	ID         string `yaml:"id"`
}

func ParseFrontendSource(path string, data []byte) (FactGraph, []Diagnostic, error) {
	if path == "" || !strings.HasSuffix(path, ".yaml") || len(data) == 0 || bytes.IndexByte(data, 0) >= 0 {
		return FactGraph{}, nil, fmt.Errorf("frontend source path or content is invalid")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var document frontendSourceDocument
	if err := decoder.Decode(&document); err != nil {
		return FactGraph{}, nil, fmt.Errorf("parse frontend source %s: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return FactGraph{}, nil, fmt.Errorf("frontend source %s contains multiple YAML documents", path)
	} else if err != io.EOF {
		return FactGraph{}, nil, fmt.Errorf("parse frontend source %s trailing document: %w", path, err)
	}
	if document.APIVersion != FrontendSourceContract || document.Kind != "Page" || !pageIDPattern.MatchString(document.ID) {
		return FactGraph{}, nil, fmt.Errorf("frontend source %s identity is invalid", path)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return FactGraph{}, nil, fmt.Errorf("parse frontend source %s: %w", path, err)
	}
	idLine, err := validateFrontendYAMLNode(&root)
	if err != nil {
		return FactGraph{}, nil, fmt.Errorf("frontend source %s: %w", path, err)
	}
	source, err := ParseSourceRef("page://" + path + "#" + document.ID)
	if err != nil {
		return FactGraph{}, nil, err
	}
	target := Target{SemanticID: document.ID, Kind: NodePage, Stage: StagePage, Source: source}
	lines := frontendDirectiveLines(path, data, idLine, target)
	parsed, diagnostics := ParseFile(StandardRegistry(), path, lines)
	if len(diagnostics) > 0 {
		return FactGraph{}, diagnostics, nil
	}
	native, err := json.Marshal(struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		ID         string `json:"id"`
	}{APIVersion: document.APIVersion, Kind: document.Kind, ID: document.ID})
	if err != nil {
		return FactGraph{}, nil, err
	}
	input := NodeInput{SemanticID: document.ID, Kind: NodePage, Stage: StagePage, Source: source, Location: Location{File: path, Line: idLine, Column: 1}, NativeCanonical: native}
	for _, fact := range parsed.Facts() {
		input.Facts = append(input.Facts, fact.Directive())
	}
	for _, binding := range parsed.Sources() {
		value := binding.Source()
		input.SourceDirective = &value
		input.SourceLocation = binding.Location()
	}
	graph, diagnostics := BuildGraph(StandardRegistry(), BuildInput{Nodes: []NodeInput{input}})
	return graph, diagnostics, nil
}

func (g FactGraph) PageEntity(pageID string) (string, bool) {
	return g.optionalString(pageID, "ui.entity")
}

func (g FactGraph) PageSize(pageID string) (int, bool) {
	fact, ok := g.Fact(FactID{SemanticID: pageID, Key: "ui.pageSize"})
	if !ok {
		return 0, false
	}
	value, valid := fact.Value().Integer()
	return int(value), valid
}

func (g FactGraph) PageString(pageID, key string) (string, bool) {
	switch key {
	case "ui.extensionComponent", "route.path", "route.name", "route.icon":
		return g.optionalString(pageID, key)
	default:
		return "", false
	}
}

func (g FactGraph) PageMenuOrder(pageID string) (int, bool) {
	fact, ok := g.Fact(FactID{SemanticID: pageID, Key: "menu.order"})
	if !ok {
		return 0, false
	}
	value, valid := fact.Value().Integer()
	return int(value), valid
}

func validateFrontendYAMLNode(root *yaml.Node) (int, error) {
	if root == nil || len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return 0, fmt.Errorf("root must be one mapping")
	}
	mapping := root.Content[0]
	if mapping.Tag != "!!map" || mapping.Anchor != "" || len(mapping.Content) != 6 {
		return 0, fmt.Errorf("root must contain exactly apiVersion, kind, and id")
	}
	expected := []string{"apiVersion", "kind", "id"}
	idLine := 0
	for index, key := range expected {
		name, value := mapping.Content[index*2], mapping.Content[index*2+1]
		if name.Kind != yaml.ScalarNode || name.Tag != "!!str" || name.Value != key || name.Anchor != "" || value.Kind != yaml.ScalarNode || value.Tag != "!!str" || value.Anchor != "" || value.Alias != nil {
			return 0, fmt.Errorf("fields must use canonical order and string scalars")
		}
		if key == "id" {
			idLine = name.Line
		}
	}
	return idLine, nil
}

func frontendDirectiveLines(path string, data []byte, idLine int, target Target) []Line {
	rawLines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	bound := make(map[int]bool)
	for line := idLine - 1; line >= 1; line-- {
		trimmed := strings.TrimSpace(rawLines[line-1])
		if strings.HasPrefix(trimmed, "#") {
			bound[line] = true
			continue
		}
		break
	}
	result := make([]Line, 0)
	for index, raw := range rawLines {
		if !strings.Contains(raw, "@nexa") {
			continue
		}
		line := index + 1
		var semanticTarget *Target
		if bound[line] && !strings.Contains(raw, " @nexa $contract:") {
			copyTarget := target
			semanticTarget = &copyTarget
		}
		result = append(result, Line{Text: raw, CommentPrefix: "#", Location: Location{File: path, Line: line, Column: 1}, Target: semanticTarget})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Location.Line < result[j].Location.Line })
	return result
}

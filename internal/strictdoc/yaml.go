package strictdoc

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"
)

var jsonNumberPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

type sourceLocation struct {
	line   int
	column int
}

type documentLocations struct {
	keys   map[string]sourceLocation
	values map[string]sourceLocation
}

func newDocumentLocations() *documentLocations {
	return &documentLocations{
		keys:   make(map[string]sourceLocation),
		values: make(map[string]sourceLocation),
	}
}

func (l *documentLocations) clone() *documentLocations {
	if l == nil {
		return nil
	}
	result := newDocumentLocations()
	for pointer, location := range l.keys {
		result.keys[pointer] = location
	}
	for pointer, location := range l.values {
		result.values[pointer] = location
	}
	return result
}

func (l *documentLocations) recordKeyAt(pointer string, line, column int) {
	if l != nil {
		l.keys[pointer] = sourceLocation{line: line, column: column}
	}
}

func (l *documentLocations) recordValueAt(pointer string, line, column int) {
	if l != nil {
		l.values[pointer] = sourceLocation{line: line, column: column}
	}
}

func (l *documentLocations) recordKey(pointer string, node *yaml.Node) {
	l.recordKeyAt(pointer, node.Line, node.Column)
}

func (l *documentLocations) recordValue(pointer string, node *yaml.Node) {
	l.recordValueAt(pointer, node.Line, node.Column)
}

func (l *documentLocations) value(pointer string) (int, int) {
	if l == nil {
		return 0, 0
	}
	location := l.values[pointer]
	return location.line, location.column
}

func DecodeYAML(source string, data []byte, out any) error {
	document, err := ParseYAML(source, data)
	if err != nil {
		return err
	}
	return document.Decode(out)
}

func ParseYAML(source string, data []byte) (Document, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if err == io.EOF {
			return Document{}, documentError("document_invalid", source, "", 1, 1, "document is empty")
		}
		return Document{}, documentError("document_invalid", source, "", 0, 0, "document is not valid YAML")
	}
	if len(document.Content) != 1 {
		return Document{}, documentError("document_invalid", source, "", document.Line, document.Column, "document must contain one value")
	}

	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err == nil {
		line, column := yamlLocation(&trailing)
		return Document{}, documentError(
			"document_trailing_input", source, "", line, column,
			"document contains a second YAML document",
		)
	} else if err != io.EOF {
		return Document{}, documentError("document_invalid", source, "", 0, 0, "document is not valid YAML")
	}

	locations := newDocumentLocations()
	normalized, err := normalizeYAMLNode(source, document.Content[0], "", locations)
	if err != nil {
		return Document{}, err
	}
	return newDocument(source, normalized, locations)
}

func normalizeYAMLNode(
	source string,
	node *yaml.Node,
	pointer string,
	locations *documentLocations,
) (any, error) {
	locations.recordValue(pointer, node)
	if node.Kind == yaml.AliasNode || node.Alias != nil {
		return nil, nodeError(
			"document_alias_forbidden", source, pointer, node,
			"YAML aliases are not supported",
		)
	}

	tag := node.ShortTag()
	if tag != "!!map" && tag != "!!seq" && tag != "!!str" && tag != "!!bool" &&
		tag != "!!int" && tag != "!!float" && tag != "!!null" {
		if len(tag) > 0 && tag[0] == '!' && (len(tag) == 1 || tag[1] != '!') {
			return nil, nodeError(
				"document_tag_forbidden", source, pointer, node,
				"custom YAML tags are not supported",
			)
		}
		return nil, nodeError(
			"document_invalid", source, pointer, node,
			"YAML value is outside the supported JSON-compatible subset",
		)
	}

	switch node.Kind {
	case yaml.MappingNode:
		return normalizeYAMLMapping(source, node, pointer, locations)
	case yaml.SequenceNode:
		result := make([]any, 0, len(node.Content))
		for index, childNode := range node.Content {
			child, err := normalizeYAMLNode(
				source,
				childNode,
				joinPointer(pointer, strconv.Itoa(index)),
				locations,
			)
			if err != nil {
				return nil, err
			}
			result = append(result, child)
		}
		return result, nil
	case yaml.ScalarNode:
		return normalizeYAMLScalar(source, node, pointer)
	default:
		return nil, nodeError("document_invalid", source, pointer, node, "unsupported YAML node")
	}
}

func normalizeYAMLMapping(
	source string,
	node *yaml.Node,
	pointer string,
	locations *documentLocations,
) (any, error) {
	if len(node.Content)%2 != 0 {
		return nil, nodeError("document_invalid", source, pointer, node, "mapping has an incomplete entry")
	}
	keys := make([]string, len(node.Content)/2)
	seen := make(map[string]struct{}, len(keys))
	for index := 0; index < len(node.Content); index += 2 {
		keyNode := node.Content[index]
		key, keyPointer, err := normalizeYAMLMappingKey(source, keyNode, pointer, locations)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, nodeError(
				"document_duplicate_key", source, keyPointer, keyNode,
				"mapping key appears more than once",
			)
		}
		seen[key] = struct{}{}
		keys[index/2] = key
	}

	result := make(map[string]any, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		valueNode := node.Content[index+1]
		key := keys[index/2]
		keyPointer := joinPointer(pointer, key)
		value, err := normalizeYAMLNode(source, valueNode, keyPointer, locations)
		if err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, nil
}

func normalizeYAMLMappingKey(
	source string,
	node *yaml.Node,
	pointer string,
	locations *documentLocations,
) (string, string, error) {
	key := node.Value
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		key = node.Alias.Value
	}
	keyPointer := joinPointer(pointer, key)
	locations.recordKey(keyPointer, node)
	if node.Kind == yaml.AliasNode || node.Alias != nil {
		return "", keyPointer, nodeError(
			"document_alias_forbidden", source, keyPointer, node,
			"YAML aliases are not supported",
		)
	}
	if node.ShortTag() == "!!merge" {
		return "", keyPointer, nodeError(
			"document_merge_key_forbidden", source, keyPointer, node,
			"YAML merge keys are not supported",
		)
	}
	if isCustomYAMLTag(node.ShortTag()) {
		return "", keyPointer, nodeError(
			"document_tag_forbidden", source, keyPointer, node,
			"custom YAML tags are not supported",
		)
	}
	if node.Kind != yaml.ScalarNode || node.ShortTag() != "!!str" {
		return "", keyPointer, nodeError(
			"document_invalid", source, keyPointer, node,
			"mapping keys must be strings",
		)
	}
	return key, keyPointer, nil
}

func isCustomYAMLTag(tag string) bool {
	return len(tag) > 0 && tag[0] == '!' && (len(tag) == 1 || tag[1] != '!')
}

func normalizeYAMLScalar(source string, node *yaml.Node, pointer string) (any, error) {
	switch node.ShortTag() {
	case "!!str":
		return node.Value, nil
	case "!!bool":
		if node.Value == "true" {
			return true, nil
		}
		if node.Value == "false" {
			return false, nil
		}
	case "!!null":
		if node.Value == "null" {
			return nil, nil
		}
	case "!!int", "!!float":
		if jsonNumberPattern.MatchString(node.Value) {
			return json.Number(node.Value), nil
		}
	}
	return nil, nodeError(
		"document_invalid", source, pointer, node,
		"scalar is not a valid JSON-compatible YAML value",
	)
}

func nodeError(code, source, pointer string, node *yaml.Node, message string) error {
	return documentError(code, source, pointer, node.Line, node.Column, message)
}

func yamlLocation(node *yaml.Node) (int, int) {
	for node != nil && node.Line == 0 && len(node.Content) > 0 {
		node = node.Content[0]
	}
	if node == nil {
		return 0, 0
	}
	return node.Line, node.Column
}

package api

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type sourcePosition struct{ line, column int }

func applySourceLocation(err *Error, data []byte) {
	if err == nil || err.line > 0 {
		return
	}
	locations := sourceLocations(data)
	pointer := err.pointer
	for {
		if location, exists := locations[pointer]; exists {
			err.line, err.column = location.line, location.column
			return
		}
		if pointer == "" {
			return
		}
		if slash := strings.LastIndex(pointer, "/"); slash >= 0 {
			pointer = pointer[:slash]
		} else {
			pointer = ""
		}
	}
}

func sourceLocations(data []byte) map[string]sourcePosition {
	locations := make(map[string]sourcePosition)
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil || len(document.Content) == 0 {
		return locations
	}
	recordNodeLocations(document.Content[0], "", locations)
	return locations
}

func recordNodeLocations(node *yaml.Node, pointer string, locations map[string]sourcePosition) {
	locations[pointer] = sourcePosition{line: node.Line, column: node.Column}
	switch node.Kind {
	case yaml.MappingNode:
		for index := 0; index+1 < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			child := pointer + "/" + escapePointer(key.Value)
			locations[child] = sourcePosition{line: value.Line, column: value.Column}
			recordNodeLocations(value, child, locations)
		}
	case yaml.SequenceNode:
		for index, value := range node.Content {
			child := pointer + "/" + strconv.Itoa(index)
			locations[child] = sourcePosition{line: value.Line, column: value.Column}
			recordNodeLocations(value, child, locations)
		}
	}
}

func escapePointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

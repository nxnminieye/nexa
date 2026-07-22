package strictdoc

import (
	"encoding/json"
	"sort"
	"strconv"
)

type Document struct {
	source     string
	json       []byte
	normalized any
	locations  *documentLocations
}

func newDocument(source string, normalized any, locations *documentLocations) (Document, error) {
	encoded, err := json.Marshal(normalized)
	if err != nil {
		line, column := locations.value("")
		return Document{}, documentError(
			"document_invalid", source, "", line, column,
			"document cannot be represented as JSON",
		)
	}
	return Document{
		source:     source,
		json:       encoded,
		normalized: normalized,
		locations:  locations.clone(),
	}, nil
}

func (d Document) JSON() []byte {
	return append([]byte(nil), d.json...)
}

func (d Document) Location(pointer string) (line, column int, ok bool) {
	if d.locations == nil {
		return 0, 0, false
	}
	location, ok := d.locations.values[pointer]
	if !ok {
		return 0, 0, false
	}
	return location.line, location.column, true
}

func (d Document) Decode(out any) error {
	if len(d.json) == 0 {
		return documentError("document_invalid", d.source, "", 1, 1, "document is empty")
	}
	return decodeTypedJSON(d.source, d.json, out, d.locations)
}

// DecodeExact decodes with the base strict decoder before enforcing exact
// member spelling for closed, roundtrip-stable DTOs.
func (d Document) DecodeExact(out any) error {
	decodeErr := d.Decode(out)
	if decodeErr != nil {
		typed, ok := decodeErr.(*Error)
		if !ok || typed.Code != "document_invalid" || typed.Pointer == "" {
			return decodeErr
		}
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		if decodeErr != nil {
			return decodeErr
		}
		return documentError("destination_invalid", d.source, "", 0, 0, "decode destination is not roundtrip-stable")
	}
	projected, _, err := preflightJSON(d.source, encoded)
	if err != nil {
		if decodeErr != nil {
			return decodeErr
		}
		return documentError("destination_invalid", d.source, "", 0, 0, "decode destination is not roundtrip-stable")
	}
	if pointer := firstMissingExactMember(d.normalized, projected, ""); pointer != "" {
		line, column := d.locations.value(pointer)
		return documentError(
			"document_unknown_field", d.source, pointer, line, column,
			"document member spelling is not exact",
		)
	}
	return decodeErr
}

func firstMissingExactMember(input, projected any, pointer string) string {
	switch input := input.(type) {
	case map[string]any:
		output, ok := projected.(map[string]any)
		if !ok {
			return ""
		}
		keys := make([]string, 0, len(input))
		for key := range input {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			outputValue, exists := output[key]
			if !exists {
				return joinPointer(pointer, key)
			}
			if nested := firstMissingExactMember(input[key], outputValue, joinPointer(pointer, key)); nested != "" {
				return nested
			}
		}
	case []any:
		output, ok := projected.([]any)
		if !ok {
			return ""
		}
		for index := 0; index < len(input) && index < len(output); index++ {
			if nested := firstMissingExactMember(input[index], output[index], joinPointer(pointer, strconv.Itoa(index))); nested != "" {
				return nested
			}
		}
	}
	return ""
}

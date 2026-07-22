package strictdoc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

type jsonPositionCursor struct {
	data         []byte
	offset       int
	line, column int
}

func newJSONPositionCursor(data []byte) *jsonPositionCursor {
	return &jsonPositionCursor{data: data, line: 1, column: 1}
}

func (c *jsonPositionCursor) lineColumn(offset int64) (int, int) {
	limit := int(offset)
	if limit < 0 {
		limit = 0
	}
	if limit > len(c.data) {
		limit = len(c.data)
	}
	if limit < c.offset {
		return lineColumn(c.data, int64(limit))
	}
	for _, character := range c.data[c.offset:limit] {
		if character == '\n' {
			c.line, c.column = c.line+1, 1
			continue
		}
		c.column++
	}
	c.offset = limit
	return c.line, c.column
}

func DecodeJSON(source string, data []byte, out any) error {
	document, err := ParseJSON(source, data)
	if err != nil {
		return err
	}
	return document.Decode(out)
}

// DecodeJSONExact strictly decodes a closed, roundtrip-stable JSON DTO and
// requires exact member spelling.
func DecodeJSONExact(source string, data []byte, out any) error {
	document, err := ParseJSON(source, data)
	if err != nil {
		return err
	}
	return document.DecodeExact(out)
}

func ParseJSON(source string, data []byte) (Document, error) {
	normalized, locations, err := preflightJSON(source, data)
	if err != nil {
		return Document{}, err
	}
	document, err := newDocument(source, normalized, locations)
	if err != nil {
		return Document{}, err
	}
	return document, nil
}

func preflightJSON(source string, data []byte) (any, *documentLocations, error) {
	if !validRawJSONUnicode(data) {
		return nil, nil, documentError(
			"document_unicode_invalid", source, "", 1, 1,
			"document contains invalid Unicode",
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	locations := newDocumentLocations()
	positions := newJSONPositionCursor(data)
	value, err := readJSONValue(source, data, decoder, "", locations, positions)
	if err != nil {
		return nil, nil, err
	}
	if _, err := decoder.Token(); err == nil {
		line, column := positions.lineColumn(decoder.InputOffset())
		return nil, nil, documentError(
			"document_trailing_input", source, "", line, column,
			"document contains a second JSON value",
		)
	} else if err != io.EOF {
		return nil, nil, jsonDecodeError(source, data, "", decoder.InputOffset(), err)
	}
	return value, locations, nil
}

func validRawJSONUnicode(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	for index := 0; index < len(data); index++ {
		if data[index] != '"' {
			continue
		}
		index++
		for index < len(data) && data[index] != '"' {
			if data[index] != '\\' {
				index++
				continue
			}
			index++
			if index >= len(data) {
				return true
			}
			if data[index] != 'u' || index+4 >= len(data) {
				index++
				continue
			}
			codeUnit, ok := jsonHexCodeUnit(data[index+1 : index+5])
			if !ok {
				index += 5
				continue
			}
			index += 5
			switch {
			case codeUnit >= 0xd800 && codeUnit <= 0xdbff:
				if index+5 >= len(data) || data[index] != '\\' || data[index+1] != 'u' {
					return false
				}
				low, lowOK := jsonHexCodeUnit(data[index+2 : index+6])
				if !lowOK || low < 0xdc00 || low > 0xdfff {
					return false
				}
				index += 6
			case codeUnit >= 0xdc00 && codeUnit <= 0xdfff:
				return false
			}
		}
	}
	return true
}

func jsonHexCodeUnit(encoded []byte) (uint16, bool) {
	if len(encoded) != 4 {
		return 0, false
	}
	var value uint16
	for _, current := range encoded {
		value <<= 4
		switch {
		case current >= '0' && current <= '9':
			value |= uint16(current - '0')
		case current >= 'a' && current <= 'f':
			value |= uint16(current-'a') + 10
		case current >= 'A' && current <= 'F':
			value |= uint16(current-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func readJSONValue(
	source string,
	data []byte,
	decoder *json.Decoder,
	pointer string,
	locations *documentLocations,
	positions *jsonPositionCursor,
) (any, error) {
	valueOffset := nextJSONTokenOffset(data, decoder.InputOffset())
	token, err := decoder.Token()
	if err != nil {
		if err == io.EOF {
			line, column := positions.lineColumn(decoder.InputOffset())
			return nil, documentError("document_invalid", source, pointer, line, column, "document is empty")
		}
		return nil, jsonDecodeError(source, data, pointer, decoder.InputOffset(), err)
	}
	line, column := positions.lineColumn(valueOffset)
	locations.recordValueAt(pointer, line, column)

	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		result := make(map[string]any)
		for decoder.More() {
			keyOffset := nextJSONTokenOffset(data, decoder.InputOffset())
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, jsonDecodeError(source, data, pointer, decoder.InputOffset(), err)
			}
			key, ok := keyToken.(string)
			if !ok {
				line, column := positions.lineColumn(decoder.InputOffset())
				return nil, documentError("document_invalid", source, pointer, line, column, "object key is not a string")
			}
			childPointer := joinPointer(pointer, key)
			keyLine, keyColumn := positions.lineColumn(keyOffset)
			locations.recordKeyAt(childPointer, keyLine, keyColumn)
			if _, duplicate := result[key]; duplicate {
				line, column := positions.lineColumn(decoder.InputOffset())
				return nil, documentError(
					"document_duplicate_key", source, childPointer, line, column,
					"object key appears more than once",
				)
			}
			child, err := readJSONValue(source, data, decoder, childPointer, locations, positions)
			if err != nil {
				return nil, err
			}
			result[key] = child
		}
		if _, err := decoder.Token(); err != nil {
			return nil, jsonDecodeError(source, data, pointer, decoder.InputOffset(), err)
		}
		return result, nil
	case '[':
		result := make([]any, 0)
		for index := 0; decoder.More(); index++ {
			child, err := readJSONValue(source, data, decoder, joinPointer(pointer, strconv.Itoa(index)), locations, positions)
			if err != nil {
				return nil, err
			}
			result = append(result, child)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, jsonDecodeError(source, data, pointer, decoder.InputOffset(), err)
		}
		return result, nil
	default:
		line, column := positions.lineColumn(decoder.InputOffset())
		return nil, documentError("document_invalid", source, pointer, line, column, "unexpected closing delimiter")
	}
}

func nextJSONTokenOffset(data []byte, offset int64) int64 {
	index := int(offset)
	if index < 0 {
		index = 0
	}
	if index > len(data) {
		index = len(data)
	}
	for index < len(data) {
		switch data[index] {
		case ' ', '\t', '\r', '\n', ':', ',':
			index++
		default:
			return int64(index)
		}
	}
	return int64(index)
}

func decodeTypedJSON(
	source string,
	data []byte,
	out any,
	locations *documentLocations,
) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		if _, ok := err.(*json.InvalidUnmarshalError); ok {
			return documentError("destination_invalid", source, "", 0, 0, "decode destination is invalid")
		}
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			field := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
			pointer := pointerForField(locations, field)
			line, column := locationForPointer(locations, pointer)
			return documentError("document_unknown_field", source, pointer, line, column, "document contains an unknown field")
		}
		var typeError *json.UnmarshalTypeError
		if errors.As(err, &typeError) {
			pointer := pointerFromFieldPath(typeError.Field)
			line, column := locationForPointer(locations, pointer)
			return documentError("document_invalid", source, pointer, line, column, "document value is incompatible with the target type")
		}
		line, column := lineColumn(data, decoder.InputOffset())
		return documentError(
			"document_invalid", source, "", line, column,
			"document value is incompatible with the target type",
		)
	}
	if err := requireJSONEOF(decoder); err != nil {
		line, column := lineColumn(data, decoder.InputOffset())
		return documentError("document_trailing_input", source, "", line, column, "document contains trailing input")
	}
	return nil
}

func pointerForField(locations *documentLocations, field string) string {
	wantSuffix := joinPointer("", field)
	match := ""
	if locations != nil {
		for pointer := range locations.values {
			if pointer == wantSuffix || strings.HasSuffix(pointer, wantSuffix) {
				if match != "" {
					return wantSuffix
				}
				match = pointer
			}
		}
	}
	if match != "" {
		return match
	}
	return wantSuffix
}

func pointerFromFieldPath(field string) string {
	pointer := ""
	for component := range strings.SplitSeq(field, ".") {
		if component != "" {
			pointer = joinPointer(pointer, component)
		}
	}
	return pointer
}

func locationForPointer(locations *documentLocations, pointer string) (int, int) {
	if locations == nil {
		return 0, 0
	}
	return locations.value(pointer)
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("second value")
	}
	return err
}

func jsonDecodeError(source string, data []byte, pointer string, offset int64, err error) error {
	var syntaxError *json.SyntaxError
	if ok := asJSONSyntaxError(err, &syntaxError); ok {
		offset = syntaxError.Offset
	}
	line, column := lineColumn(data, offset)
	return documentError("document_invalid", source, pointer, line, column, "document is not valid JSON")
}

func asJSONSyntaxError(err error, target **json.SyntaxError) bool {
	syntaxError, ok := err.(*json.SyntaxError)
	if ok {
		*target = syntaxError
	}
	return ok
}

func lineColumn(data []byte, offset int64) (int, int) {
	limit := int(offset)
	if limit < 0 {
		limit = 0
	}
	if limit > len(data) {
		limit = len(data)
	}
	line, column := 1, 1
	for _, character := range data[:limit] {
		if character == '\n' {
			line, column = line+1, 1
			continue
		}
		column++
	}
	return line, column
}

func joinPointer(pointer, component string) string {
	escaped := strings.ReplaceAll(component, "~", "~0")
	escaped = strings.ReplaceAll(escaped, "/", "~1")
	return pointer + "/" + escaped
}

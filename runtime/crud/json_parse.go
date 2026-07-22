package crud

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const maxJSONObjectBytes = 1 << 20

func parseAndNormalizeJSONObject(data []byte) ([]byte, error) {
	if len(data) > maxJSONObjectBytes {
		return nil, jsonObjectInvalid("size_limit_exceeded", "")
	}
	if !utf8.Valid(data) {
		return nil, jsonObjectInvalid("unicode_invalid", "")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, jsonObjectInvalid("syntax_invalid", "")
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, jsonObjectInvalid("trailing_input", "")
	}
	if value == nil {
		return nil, jsonObjectInvalid("null_forbidden", "")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, jsonObjectInvalid("root_not_object", "")
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		return nil, jsonObjectInvalid("syntax_invalid", "")
	}
	return normalized, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("additional JSON value")
}

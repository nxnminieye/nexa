package crud

import (
	"database/sql/driver"
	"encoding/json"
	"reflect"
)

// JSONObject is an immutable normalized JSON object value.
type JSONObject struct {
	normalized []byte
}

func ParseJSONObject(data []byte) (JSONObject, error) {
	normalized, err := parseAndNormalizeJSONObject(data)
	if err != nil {
		return JSONObject{}, err
	}
	return JSONObject{normalized: append([]byte(nil), normalized...)}, nil
}

func NewJSONObject(value any) (JSONObject, error) {
	if isNilValue(value) {
		return JSONObject{}, jsonObjectEncodeFailed("input_nil")
	}
	encoded, marshalErr, panicked := marshalJSONObject(value)
	if panicked {
		return JSONObject{}, jsonObjectEncodeFailed("marshal_panic")
	}
	if marshalErr != nil {
		return JSONObject{}, jsonObjectEncodeFailed("marshal_failed")
	}
	return ParseJSONObject(encoded)
}

func (o JSONObject) Bytes() ([]byte, error) {
	if len(o.normalized) == 0 {
		return nil, jsonObjectInvalid("zero_value", "")
	}
	return append([]byte(nil), o.normalized...), nil
}

func (o JSONObject) String() (string, error) {
	if len(o.normalized) == 0 {
		return "", jsonObjectInvalid("zero_value", "")
	}
	return string(o.normalized), nil
}

func (o JSONObject) MarshalJSON() ([]byte, error) {
	return o.Bytes()
}

func (o *JSONObject) UnmarshalJSON(data []byte) error {
	if o == nil {
		return jsonObjectInvalid("receiver_nil", "")
	}
	if len(data) > maxJSONObjectBytes {
		return jsonObjectInvalid("size_limit_exceeded", "")
	}
	parsed, err := ParseJSONObject(data)
	if err != nil {
		return err
	}
	*o = parsed
	return nil
}

func (o *JSONObject) Scan(src any) error {
	if o == nil {
		return jsonObjectInvalid("receiver_nil", "")
	}

	var parsed JSONObject
	var err error
	switch value := src.(type) {
	case nil:
		return jsonObjectScanFailed("source_null")
	case string:
		if len(value) > maxJSONObjectBytes {
			return jsonObjectInvalid("size_limit_exceeded", "")
		}
		parsed, err = ParseJSONObject([]byte(value))
	case []byte:
		if len(value) > maxJSONObjectBytes {
			return jsonObjectInvalid("size_limit_exceeded", "")
		}
		parsed, err = ParseJSONObject(value)
	default:
		return jsonObjectScanFailed("source_type_invalid")
	}
	if err != nil {
		return err
	}
	*o = parsed
	return nil
}

func (o JSONObject) Value() (driver.Value, error) {
	value, err := o.String()
	if err != nil {
		return nil, err
	}
	return value, nil
}

func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	case reflect.UnsafePointer:
		return reflected.IsZero()
	default:
		return false
	}
}

func marshalJSONObject(value any) (encoded []byte, err error, panicked bool) {
	panicked = true
	defer func() {
		if panicked {
			_ = recover()
			encoded = nil
			err = nil
		}
	}()
	encoded, err = json.Marshal(value)
	panicked = false
	return encoded, err, false
}

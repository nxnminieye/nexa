package s3

import (
	"io"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ObjectRef identifies one object without carrying transport configuration.
type ObjectRef struct {
	bucket string
	key    string
}

func NewObjectRef(bucket, key string) (ObjectRef, error) {
	if !validText(bucket) {
		return ObjectRef{}, validationError("bucket_invalid", "/bucket")
	}
	if !validText(key) {
		return ObjectRef{}, validationError("key_invalid", "/key")
	}
	return ObjectRef{bucket: bucket, key: key}, nil
}

func (r ObjectRef) Bucket() string { return r.bucket }
func (r ObjectRef) Key() string    { return r.key }
func (r ObjectRef) Valid() bool    { return validText(r.bucket) && validText(r.key) }

func validText(value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func validBody(body io.ReadSeeker) bool { return !nilInterface(body) }

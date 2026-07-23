package s3_test

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/nxnminieye/nexa/runtime/s3"
)

type nilReadSeeker struct{}

func (*nilReadSeeker) Read([]byte) (int, error)       { return 0, io.EOF }
func (*nilReadSeeker) Seek(int64, int) (int64, error) { return 0, nil }

func TestObjectRefValidationAndZeroValue(t *testing.T) {
	ref, err := s3.NewObjectRef("bucket", "folder/object")
	if err != nil || !ref.Valid() || ref.Bucket() != "bucket" || ref.Key() != "folder/object" {
		t.Fatalf("reference = %#v, %v", ref, err)
	}
	for _, test := range []struct{ bucket, key, reason, pointer string }{
		{"", "key", "bucket_invalid", "/bucket"},
		{" bucket", "key", "bucket_invalid", "/bucket"},
		{"bucket", "", "key_invalid", "/key"},
		{"bucket", "bad\x00key", "key_invalid", "/key"},
	} {
		_, err := s3.NewObjectRef(test.bucket, test.key)
		assertTypedError(t, err, s3.ErrValidation, "validation_failed", test.reason, test.pointer)
	}
	if (s3.ObjectRef{}).Valid() {
		t.Fatal("zero ObjectRef is valid")
	}
}

func TestPutRequestOwnershipCopiesAndTypedNil(t *testing.T) {
	ref, _ := s3.NewObjectRef("bucket", "key")
	metadata := map[string]string{"owner": "caller"}
	body := bytes.NewReader([]byte("value"))
	request, err := s3.NewPutRequest(s3.PutRequestSpec{Ref: ref, Body: body, ContentType: "text/plain", Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	metadata["owner"] = "mutated"
	copyOne := request.Metadata()
	copyOne["owner"] = "again"
	if request.Metadata()["owner"] != "caller" || request.Body() != body {
		t.Fatal("request did not freeze metadata or preserve caller body")
	}
	if contentType, ok := request.ContentType(); !ok || contentType != "text/plain" {
		t.Fatalf("content type = %q,%t", contentType, ok)
	}
	var typedNil *nilReadSeeker
	_, err = s3.NewPutRequest(s3.PutRequestSpec{Ref: ref, Body: typedNil})
	assertTypedError(t, err, s3.ErrValidation, "validation_failed", "body_nil", "/body")
	if (s3.PutRequest{}).Valid() {
		t.Fatal("zero PutRequest is valid")
	}
}

func TestResultCopiesAndOptionalZeroValues(t *testing.T) {
	body := io.NopCloser(bytes.NewReader(nil))
	metadata := map[string]string{"a": "b"}
	result, err := s3.NewReadResult(s3.ReadResultSpec{Body: body, Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	metadata["a"] = "changed"
	copyOne := result.Metadata()
	copyOne["a"] = "again"
	if result.Metadata()["a"] != "b" || !result.Valid() || result.ContentLength() != 0 {
		t.Fatalf("read result = %#v", result)
	}
	if _, ok := result.ContentType(); ok {
		t.Fatal("empty content type is present")
	}
	write := s3.NewWriteResult(s3.WriteResultSpec{})
	if _, ok := write.ETag(); ok {
		t.Fatal("empty ETag is present")
	}
}

func TestObjectInfoCopiesMetadataAndExposesOptionalValues(t *testing.T) {
	metadata := map[string]string{"owner": "caller"}
	lastModified := time.Date(2026, time.July, 23, 10, 0, 0, 0, time.UTC)
	info, err := s3.NewObjectInfo(s3.ObjectInfoSpec{
		ContentLength: 7,
		ContentType:   "text/plain",
		ETag:          "etag",
		VersionID:     "version",
		LastModified:  lastModified,
		Metadata:      metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata["owner"] = "mutated"
	copyOne := info.Metadata()
	copyOne["owner"] = "again"
	if info.ContentLength() != 7 || info.Metadata()["owner"] != "caller" {
		t.Fatalf("object info = %#v", info)
	}
	if value, ok := info.ContentType(); !ok || value != "text/plain" {
		t.Fatalf("content type = %q,%t", value, ok)
	}
	if value, ok := info.ETag(); !ok || value != "etag" {
		t.Fatalf("ETag = %q,%t", value, ok)
	}
	if value, ok := info.VersionID(); !ok || value != "version" {
		t.Fatalf("version ID = %q,%t", value, ok)
	}
	if value, ok := info.LastModified(); !ok || !value.Equal(lastModified) {
		t.Fatalf("last modified = %v,%t", value, ok)
	}
}

func TestObjectInfoValidationAndOptionalZeroValues(t *testing.T) {
	info, err := s3.NewObjectInfo(s3.ObjectInfoSpec{})
	if err != nil || info.ContentLength() != 0 || info.Metadata() != nil {
		t.Fatalf("zero object info = %#v, %v", info, err)
	}
	if _, ok := info.ContentType(); ok {
		t.Fatal("empty content type is present")
	}
	if _, ok := info.ETag(); ok {
		t.Fatal("empty ETag is present")
	}
	if _, ok := info.VersionID(); ok {
		t.Fatal("empty version ID is present")
	}
	if _, ok := info.LastModified(); ok {
		t.Fatal("zero last modified is present")
	}
	_, err = s3.NewObjectInfo(s3.ObjectInfoSpec{ContentLength: -1})
	assertTypedError(t, err, s3.ErrValidation, "validation_failed", "content_length_invalid", "/contentLength")
	_, err = s3.NewObjectInfo(s3.ObjectInfoSpec{Metadata: map[string]string{"": "invalid"}})
	assertTypedError(t, err, s3.ErrValidation, "validation_failed", "metadata_invalid", "/metadata")
}

func assertTypedError(t *testing.T, err, sentinel error, code, reason, pointer string) {
	t.Helper()
	var typed *s3.Error
	if !errors.Is(err, sentinel) || !errors.As(err, &typed) {
		t.Fatalf("error = %v", err)
	}
	if typed.Code() != code || typed.Reason() != reason || typed.Pointer() != pointer {
		t.Fatalf("typed error = %q,%q,%q", typed.Code(), typed.Reason(), typed.Pointer())
	}
}

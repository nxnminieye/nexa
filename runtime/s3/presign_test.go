package s3_test

import (
	"errors"
	"testing"
	"time"

	"github.com/nxnminieye/nexa/runtime/s3"
)

func TestPresignRequestsValidateAndFreezeValues(t *testing.T) {
	ref, _ := s3.NewObjectRef("bucket", "key")
	upload, err := s3.NewPresignUploadRequest(s3.PresignUploadRequestSpec{Ref: ref, ContentType: "application/json", Expires: 5 * time.Minute})
	if err != nil || !upload.Valid() || upload.Ref() != ref || upload.Expires() != 5*time.Minute {
		t.Fatalf("upload = %#v, %v", upload, err)
	}
	if contentType, ok := upload.ContentType(); !ok || contentType != "application/json" {
		t.Fatalf("content type = %q,%t", contentType, ok)
	}
	download, err := s3.NewPresignDownloadRequest(s3.PresignDownloadRequestSpec{Ref: ref, Expires: time.Minute})
	if err != nil || !download.Valid() || download.Ref() != ref || download.Expires() != time.Minute {
		t.Fatalf("download = %#v, %v", download, err)
	}
	for _, err := range []error{
		presignUploadError(s3.PresignUploadRequestSpec{Ref: ref}),
		presignUploadError(s3.PresignUploadRequestSpec{Ref: ref, ContentType: " invalid", Expires: time.Second}),
		presignDownloadError(s3.PresignDownloadRequestSpec{Ref: ref, Expires: -time.Second}),
		presignDownloadError(s3.PresignDownloadRequestSpec{Expires: time.Second}),
	} {
		if !errors.Is(err, s3.ErrValidation) {
			t.Fatalf("validation error = %v", err)
		}
	}
	if (s3.PresignUploadRequest{}).Valid() || (s3.PresignDownloadRequest{}).Valid() {
		t.Fatal("zero presign request is valid")
	}
}

func TestPresignResultOnlyExposesValidatedURL(t *testing.T) {
	headers := map[string][]string{"Content-Type": {"application/json"}}
	result, err := s3.NewPresignResultWithHeaders("https://objects.example.test/bucket/key?signature=value", headers)
	if err != nil || result.URL() != "https://objects.example.test/bucket/key?signature=value" {
		t.Fatalf("result = %#v, %v", result, err)
	}
	headers["Content-Type"][0] = "mutated/input"
	copyOne := result.Headers()
	copyOne["Content-Type"][0] = "mutated/output"
	if result.Headers()["Content-Type"][0] != "application/json" {
		t.Fatal("presign headers were not defensively copied")
	}
	for _, value := range []string{"", "/relative", "ftp://objects.example.test/key", "https://"} {
		if _, err := s3.NewPresignResult(value); !errors.Is(err, s3.ErrValidation) {
			t.Fatalf("URL %q error = %v", value, err)
		}
	}
	if (s3.PresignResult{}).URL() != "" {
		t.Fatal("zero result URL is non-empty")
	}
	if (s3.PresignResult{}).Headers() != nil {
		t.Fatal("zero result headers are non-nil")
	}
}

func presignUploadError(spec s3.PresignUploadRequestSpec) error {
	_, err := s3.NewPresignUploadRequest(spec)
	return err
}

func presignDownloadError(spec s3.PresignDownloadRequestSpec) error {
	_, err := s3.NewPresignDownloadRequest(spec)
	return err
}

package s3

import (
	"context"
	"net/url"
	"time"
	"unicode/utf8"
)

// Presigner creates time-limited upload, download, metadata, and delete URLs
// without exposing a provider-specific request or result type.
type Presigner interface {
	PresignUpload(context.Context, PresignUploadRequest) (PresignResult, error)
	PresignDownload(context.Context, PresignDownloadRequest) (PresignResult, error)
	PresignHead(context.Context, PresignHeadRequest) (PresignResult, error)
	PresignDelete(context.Context, PresignDeleteRequest) (PresignResult, error)
}

type PresignUploadRequestSpec struct {
	Ref         ObjectRef
	ContentType string
	Expires     time.Duration
}

type PresignUploadRequest struct {
	ref         ObjectRef
	contentType string
	expires     time.Duration
}

func NewPresignUploadRequest(spec PresignUploadRequestSpec) (PresignUploadRequest, error) {
	if !spec.Ref.Valid() {
		return PresignUploadRequest{}, validationError("object_ref_invalid", "/ref")
	}
	if spec.ContentType != "" && !validText(spec.ContentType) {
		return PresignUploadRequest{}, validationError("content_type_invalid", "/contentType")
	}
	if spec.Expires <= 0 {
		return PresignUploadRequest{}, validationError("expires_invalid", "/expires")
	}
	return PresignUploadRequest{ref: spec.Ref, contentType: spec.ContentType, expires: spec.Expires}, nil
}

func (r PresignUploadRequest) Ref() ObjectRef              { return r.ref }
func (r PresignUploadRequest) ContentType() (string, bool) { return r.contentType, r.contentType != "" }
func (r PresignUploadRequest) Expires() time.Duration      { return r.expires }
func (r PresignUploadRequest) Valid() bool                 { return r.ref.Valid() && r.expires > 0 }

type PresignDownloadRequestSpec struct {
	Ref     ObjectRef
	Expires time.Duration
}

type PresignDownloadRequest struct {
	ref     ObjectRef
	expires time.Duration
}

func NewPresignDownloadRequest(spec PresignDownloadRequestSpec) (PresignDownloadRequest, error) {
	if !spec.Ref.Valid() {
		return PresignDownloadRequest{}, validationError("object_ref_invalid", "/ref")
	}
	if spec.Expires <= 0 {
		return PresignDownloadRequest{}, validationError("expires_invalid", "/expires")
	}
	return PresignDownloadRequest{ref: spec.Ref, expires: spec.Expires}, nil
}

func (r PresignDownloadRequest) Ref() ObjectRef         { return r.ref }
func (r PresignDownloadRequest) Expires() time.Duration { return r.expires }
func (r PresignDownloadRequest) Valid() bool            { return r.ref.Valid() && r.expires > 0 }

type PresignHeadRequestSpec struct {
	Ref     ObjectRef
	Expires time.Duration
}

type PresignHeadRequest struct {
	ref     ObjectRef
	expires time.Duration
}

func NewPresignHeadRequest(spec PresignHeadRequestSpec) (PresignHeadRequest, error) {
	if !spec.Ref.Valid() {
		return PresignHeadRequest{}, validationError("object_ref_invalid", "/ref")
	}
	if spec.Expires <= 0 {
		return PresignHeadRequest{}, validationError("expires_invalid", "/expires")
	}
	return PresignHeadRequest{ref: spec.Ref, expires: spec.Expires}, nil
}

func (r PresignHeadRequest) Ref() ObjectRef         { return r.ref }
func (r PresignHeadRequest) Expires() time.Duration { return r.expires }
func (r PresignHeadRequest) Valid() bool            { return r.ref.Valid() && r.expires > 0 }

type PresignDeleteRequestSpec struct {
	Ref     ObjectRef
	Expires time.Duration
}

type PresignDeleteRequest struct {
	ref     ObjectRef
	expires time.Duration
}

func NewPresignDeleteRequest(spec PresignDeleteRequestSpec) (PresignDeleteRequest, error) {
	if !spec.Ref.Valid() {
		return PresignDeleteRequest{}, validationError("object_ref_invalid", "/ref")
	}
	if spec.Expires <= 0 {
		return PresignDeleteRequest{}, validationError("expires_invalid", "/expires")
	}
	return PresignDeleteRequest{ref: spec.Ref, expires: spec.Expires}, nil
}

func (r PresignDeleteRequest) Ref() ObjectRef         { return r.ref }
func (r PresignDeleteRequest) Expires() time.Duration { return r.expires }
func (r PresignDeleteRequest) Valid() bool            { return r.ref.Valid() && r.expires > 0 }

type PresignResult struct {
	url     string
	headers map[string][]string
}

func NewPresignResult(value string) (PresignResult, error) {
	return NewPresignResultWithHeaders(value, nil)
}

func NewPresignResultWithHeaders(value string, headers map[string][]string) (PresignResult, error) {
	parsed, err := url.Parse(value)
	if value == "" || !utf8.ValidString(value) || err != nil || !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return PresignResult{}, validationError("url_invalid", "/url")
	}
	return PresignResult{url: value, headers: cloneHeaders(headers)}, nil
}

func (r PresignResult) URL() string                  { return r.url }
func (r PresignResult) Headers() map[string][]string { return cloneHeaders(r.headers) }

func cloneHeaders(input map[string][]string) map[string][]string {
	if input == nil {
		return nil
	}
	result := make(map[string][]string, len(input))
	for key, values := range input {
		if values == nil {
			result[key] = nil
			continue
		}
		result[key] = append([]string(nil), values...)
	}
	return result
}

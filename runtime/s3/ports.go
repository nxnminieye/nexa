package s3

import (
	"context"
	"io"
	"time"
	"unicode/utf8"
)

type Reader interface {
	Get(context.Context, ObjectRef) (ReadResult, error)
}

type Writer interface {
	Put(context.Context, PutRequest) (WriteResult, error)
}

type Inspector interface {
	Head(context.Context, ObjectRef) (ObjectInfo, error)
}

type Deleter interface {
	Delete(context.Context, ObjectRef) error
}

type Store interface {
	Reader
	Writer
	Inspector
	Deleter
}

type PutRequestSpec struct {
	Ref         ObjectRef
	Body        io.ReadSeeker
	ContentType string
	Metadata    map[string]string
}

type PutRequest struct {
	ref         ObjectRef
	body        io.ReadSeeker
	contentType string
	metadata    map[string]string
}

func NewPutRequest(spec PutRequestSpec) (PutRequest, error) {
	if !spec.Ref.Valid() {
		return PutRequest{}, validationError("object_ref_invalid", "/ref")
	}
	if !validBody(spec.Body) {
		return PutRequest{}, validationError("body_nil", "/body")
	}
	if spec.ContentType != "" && !validText(spec.ContentType) {
		return PutRequest{}, validationError("content_type_invalid", "/contentType")
	}
	metadata, err := copyMetadata(spec.Metadata, "/metadata")
	if err != nil {
		return PutRequest{}, err
	}
	return PutRequest{ref: spec.Ref, body: spec.Body, contentType: spec.ContentType, metadata: metadata}, nil
}

func (r PutRequest) Ref() ObjectRef              { return r.ref }
func (r PutRequest) Body() io.ReadSeeker         { return r.body }
func (r PutRequest) Valid() bool                 { return r.ref.Valid() && validBody(r.body) }
func (r PutRequest) Metadata() map[string]string { return cloneMap(r.metadata) }
func (r PutRequest) ContentType() (string, bool) { return r.contentType, r.contentType != "" }

type ReadResultSpec struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
	ETag          string
	VersionID     string
	LastModified  time.Time
	Metadata      map[string]string
}

type ReadResult struct {
	body          io.ReadCloser
	contentType   string
	contentLength int64
	etag          string
	versionID     string
	lastModified  time.Time
	metadata      map[string]string
}

func NewReadResult(spec ReadResultSpec) (ReadResult, error) {
	if nilInterface(spec.Body) {
		return ReadResult{}, validationError("body_nil", "/body")
	}
	if spec.ContentLength < 0 {
		return ReadResult{}, validationError("content_length_invalid", "/contentLength")
	}
	metadata, err := copyMetadata(spec.Metadata, "/metadata")
	if err != nil {
		return ReadResult{}, err
	}
	return ReadResult{body: spec.Body, contentType: spec.ContentType, contentLength: spec.ContentLength, etag: spec.ETag, versionID: spec.VersionID, lastModified: spec.LastModified, metadata: metadata}, nil
}

func (r ReadResult) Body() io.ReadCloser             { return r.body }
func (r ReadResult) ContentLength() int64            { return r.contentLength }
func (r ReadResult) Metadata() map[string]string     { return cloneMap(r.metadata) }
func (r ReadResult) ContentType() (string, bool)     { return r.contentType, r.contentType != "" }
func (r ReadResult) ETag() (string, bool)            { return r.etag, r.etag != "" }
func (r ReadResult) VersionID() (string, bool)       { return r.versionID, r.versionID != "" }
func (r ReadResult) LastModified() (time.Time, bool) { return r.lastModified, !r.lastModified.IsZero() }
func (r ReadResult) Valid() bool                     { return !nilInterface(r.body) && r.contentLength >= 0 }

type ObjectInfoSpec struct {
	ContentType   string
	ContentLength int64
	ETag          string
	VersionID     string
	LastModified  time.Time
	Metadata      map[string]string
}

type ObjectInfo struct {
	contentType   string
	contentLength int64
	etag          string
	versionID     string
	lastModified  time.Time
	metadata      map[string]string
}

func NewObjectInfo(spec ObjectInfoSpec) (ObjectInfo, error) {
	if spec.ContentLength < 0 {
		return ObjectInfo{}, validationError("content_length_invalid", "/contentLength")
	}
	metadata, err := copyMetadata(spec.Metadata, "/metadata")
	if err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{contentType: spec.ContentType, contentLength: spec.ContentLength, etag: spec.ETag, versionID: spec.VersionID, lastModified: spec.LastModified, metadata: metadata}, nil
}

func (i ObjectInfo) ContentLength() int64            { return i.contentLength }
func (i ObjectInfo) Metadata() map[string]string     { return cloneMap(i.metadata) }
func (i ObjectInfo) ContentType() (string, bool)     { return i.contentType, i.contentType != "" }
func (i ObjectInfo) ETag() (string, bool)            { return i.etag, i.etag != "" }
func (i ObjectInfo) VersionID() (string, bool)       { return i.versionID, i.versionID != "" }
func (i ObjectInfo) LastModified() (time.Time, bool) { return i.lastModified, !i.lastModified.IsZero() }

type WriteResultSpec struct {
	ETag      string
	VersionID string
}

type WriteResult struct {
	etag      string
	versionID string
}

func NewWriteResult(spec WriteResultSpec) WriteResult {
	return WriteResult{etag: spec.ETag, versionID: spec.VersionID}
}

func (r WriteResult) ETag() (string, bool)      { return r.etag, r.etag != "" }
func (r WriteResult) VersionID() (string, bool) { return r.versionID, r.versionID != "" }

func copyMetadata(input map[string]string, pointer string) (map[string]string, error) {
	if input == nil {
		return nil, nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		if !validText(key) || !utf8.ValidString(value) {
			return nil, validationError("metadata_invalid", pointer)
		}
		result[key] = value
	}
	return result, nil
}

func cloneMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

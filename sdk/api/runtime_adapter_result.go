package api

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/cli/protocol"
	generationapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

const RuntimeAdapterResultAPIVersion = "nexa.dev/runtime-api-adapter-result/v1"

// RuntimeAdapterHeader is one ordered logical header projection.
type RuntimeAdapterHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// RuntimeAdapterRequest is one observed logical request.
type RuntimeAdapterRequest struct {
	Method  string                 `json:"method"`
	URL     string                 `json:"url"`
	Headers []RuntimeAdapterHeader `json:"headers"`
	Body    *string                `json:"body"`
}

// Digest returns the canonical logical-request digest used by conformance
// adapters without projecting request values into their result documents.
func (r RuntimeAdapterRequest) Digest() (provenance.Digest, error) {
	switch r.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return provenance.Digest{}, errors.New("runtime adapter request is invalid")
	}
	if r.URL == "" {
		return provenance.Digest{}, errors.New("runtime adapter request is invalid")
	}
	request := r
	request.Headers = append(make([]RuntimeAdapterHeader, 0, len(r.Headers)), r.Headers...)
	encoded, err := json.Marshal(request)
	if err != nil {
		return provenance.Digest{}, errors.New("runtime adapter request is invalid")
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		return provenance.Digest{}, errors.New("runtime adapter request is invalid")
	}
	return provenance.SHA256(canonical), nil
}

// RuntimeAdapterSuccess is the complete successful SDK projection.
type RuntimeAdapterSuccess struct {
	APIOperationID string                         `json:"apiOperationId"`
	HTTPStatus     int                            `json:"httpStatus"`
	ResponseBody   generationapi.ResponseBodyMode `json:"responseBody"`
	HasJSON        bool                           `json:"hasJSON"`
	CanonicalJSON  string                         `json:"canonicalJSON"`
}

// RuntimeAdapterError is the complete stable SDK error projection.
type RuntimeAdapterError struct {
	Domain              string            `json:"domain"`
	Code                string            `json:"code"`
	Message             string            `json:"message"`
	Category            protocol.Category `json:"category"`
	Retryable           bool              `json:"retryable"`
	APIOperationID      string            `json:"apiOperationId"`
	RequestID           string            `json:"requestId"`
	TraceID             string            `json:"traceId"`
	Reason              string            `json:"reason"`
	Pointer             string            `json:"pointer"`
	HTTPStatus          int               `json:"httpStatus"`
	RemoteDomain        string            `json:"remoteDomain"`
	RemoteCode          string            `json:"remoteCode"`
	RemoteDetailsAbsent bool              `json:"remoteDetailsAbsent"`
}

// RuntimeAdapterOutcome contains exactly one success or error projection.
type RuntimeAdapterOutcome struct {
	Success *RuntimeAdapterSuccess `json:"success,omitempty"`
	Error   *RuntimeAdapterError   `json:"error,omitempty"`
}

// RuntimeAdapterCaseResult is one complete observable adapter row.
type RuntimeAdapterCaseResult struct {
	Name           string                `json:"name"`
	RequestDigest  string                `json:"requestDigest"`
	ProviderCalls  int                   `json:"providerCalls"`
	TransportCalls int                   `json:"transportCalls"`
	BodyReadCalls  int                   `json:"bodyReadCalls"`
	BodyCloseCalls int                   `json:"bodyCloseCalls"`
	Outcome        RuntimeAdapterOutcome `json:"outcome"`
}

type runtimeAdapterResultDocument struct {
	APIVersion string                     `json:"apiVersion"`
	Cases      []RuntimeAdapterCaseResult `json:"cases"`
}

// RuntimeAdapterResult is an immutable canonical conformance result.
type RuntimeAdapterResult struct{ document runtimeAdapterResultDocument }

// NewRuntimeAdapterResult validates and freezes complete result rows.
func NewRuntimeAdapterResult(cases []RuntimeAdapterCaseResult) (RuntimeAdapterResult, error) {
	cloned := cloneRuntimeAdapterCaseResults(cases)
	if len(cloned) == 0 {
		return RuntimeAdapterResult{}, errors.New("runtime adapter result is invalid")
	}
	seen := make(map[string]struct{}, len(cloned))
	for _, row := range cloned {
		if row.Name == "" || !validRuntimeAdapterRequestDigest(row.RequestDigest) {
			return RuntimeAdapterResult{}, errors.New("runtime adapter result is invalid")
		}
		if _, duplicate := seen[row.Name]; duplicate {
			return RuntimeAdapterResult{}, errors.New("runtime adapter result is invalid")
		}
		seen[row.Name] = struct{}{}
		if (row.Outcome.Success == nil) == (row.Outcome.Error == nil) {
			return RuntimeAdapterResult{}, errors.New("runtime adapter result is invalid")
		}
		if !validRuntimeAdapterOutcome(row.Outcome) {
			return RuntimeAdapterResult{}, errors.New("runtime adapter result is invalid")
		}
	}
	result := RuntimeAdapterResult{document: runtimeAdapterResultDocument{APIVersion: RuntimeAdapterResultAPIVersion, Cases: cloned}}
	if _, err := result.CanonicalJSON(); err != nil {
		return RuntimeAdapterResult{}, err
	}
	return result, nil
}

func validRuntimeAdapterOutcome(outcome RuntimeAdapterOutcome) bool {
	if outcome.Success != nil {
		success := outcome.Success
		switch success.ResponseBody {
		case generationapi.ResponseBodyJSON:
			return success.HasJSON && validRuntimeAdapterCanonicalJSON(success.CanonicalJSON)
		case generationapi.ResponseBodyNone:
			return !success.HasJSON && success.CanonicalJSON == ""
		default:
			return false
		}
	}
	if outcome.Error != nil {
		status := outcome.Error.HTTPStatus
		return status == 0 || status >= 200 && status <= 599
	}
	return false
}

func validRuntimeAdapterCanonicalJSON(value string) bool {
	if value == "" {
		return false
	}
	if _, err := strictdoc.ParseJSON("runtime-adapter-success.json", []byte(value)); err != nil {
		return false
	}
	canonical, err := jcs.Transform([]byte(value))
	return err == nil && bytes.Equal(canonical, []byte(value))
}

func validRuntimeAdapterRequestDigest(value string) bool {
	if value == "absent" {
		return true
	}
	_, err := provenance.ParseDigest(value)
	return err == nil
}

// ParseRuntimeAdapterResult parses a strict canonical result document.
func ParseRuntimeAdapterResult(data []byte) (RuntimeAdapterResult, error) {
	document, err := strictdoc.ParseJSON("runtime-adapter-result.json", data)
	if err != nil {
		return RuntimeAdapterResult{}, errors.New("runtime adapter result is invalid")
	}
	var generic any
	decoder := json.NewDecoder(bytes.NewReader(document.JSON()))
	decoder.UseNumber()
	if err := decoder.Decode(&generic); err != nil {
		return RuntimeAdapterResult{}, errors.New("runtime adapter result is invalid")
	}
	schema, err := runtimeAdapterResultDocumentSchema()
	if err != nil || schema.Validate(generic) != nil {
		return RuntimeAdapterResult{}, errors.New("runtime adapter result is invalid")
	}
	var decoded runtimeAdapterResultDocument
	if err := document.Decode(&decoded); err != nil || decoded.APIVersion != RuntimeAdapterResultAPIVersion {
		return RuntimeAdapterResult{}, errors.New("runtime adapter result is invalid")
	}
	result, err := NewRuntimeAdapterResult(decoded.Cases)
	if err != nil {
		return RuntimeAdapterResult{}, err
	}
	canonical, err := result.CanonicalJSON()
	if err != nil || !bytes.Equal(canonical, data) {
		return RuntimeAdapterResult{}, errors.New("runtime adapter result is invalid")
	}
	return result, nil
}

// Cases returns independent result rows.
func (r RuntimeAdapterResult) Cases() []RuntimeAdapterCaseResult {
	return cloneRuntimeAdapterCaseResults(r.document.Cases)
}

// CanonicalJSON returns exact RFC 8785 bytes.
func (r RuntimeAdapterResult) CanonicalJSON() ([]byte, error) {
	if r.document.APIVersion != RuntimeAdapterResultAPIVersion || len(r.document.Cases) == 0 {
		return nil, errors.New("runtime adapter result is invalid")
	}
	encoded, err := json.Marshal(r.document)
	if err != nil {
		return nil, errors.New("runtime adapter result is invalid")
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		return nil, errors.New("runtime adapter result is invalid")
	}
	var generic any
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	if err := decoder.Decode(&generic); err != nil {
		return nil, errors.New("runtime adapter result is invalid")
	}
	schema, err := runtimeAdapterResultDocumentSchema()
	if err != nil || schema.Validate(generic) != nil {
		return nil, errors.New("runtime adapter result is invalid")
	}
	return canonical, nil
}

func cloneRuntimeAdapterCaseResults(input []RuntimeAdapterCaseResult) []RuntimeAdapterCaseResult {
	result := make([]RuntimeAdapterCaseResult, len(input))
	for index, row := range input {
		result[index] = row
		if row.Outcome.Success != nil {
			success := *row.Outcome.Success
			result[index].Outcome.Success = &success
		}
		if row.Outcome.Error != nil {
			failure := *row.Outcome.Error
			result[index].Outcome.Error = &failure
		}
	}
	return result
}

func cloneRuntimeAdapterHeaders(input []RuntimeAdapterHeader) []RuntimeAdapterHeader {
	if input == nil {
		return nil
	}
	return append(make([]RuntimeAdapterHeader, 0, len(input)), input...)
}

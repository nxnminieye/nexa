package api

import generationapi "github.com/nxnminieye/nexa/generation/api"

// Result is one immutable successful API response projection.
type Result struct {
	apiOperationID string
	httpStatus     int
	responseBody   generationapi.ResponseBodyMode
	json           []byte
	hasJSON        bool
}

// APIOperationID returns the selected operation ID, or empty for a zero Result.
func (r Result) APIOperationID() string { return r.apiOperationID }

// HTTPStatus returns the observed status, or zero for a zero Result.
func (r Result) HTTPStatus() int { return r.httpStatus }

// ResponseBody returns the declared response mode, or its zero value for a zero Result.
func (r Result) ResponseBody() generationapi.ResponseBodyMode { return r.responseBody }

// JSON returns independent canonical JSON bytes when the response mode carries JSON.
// It returns nil and false for non-JSON and zero results.
func (r Result) JSON() ([]byte, bool) {
	if !r.hasJSON {
		return nil, false
	}
	return append([]byte(nil), r.json...), true
}

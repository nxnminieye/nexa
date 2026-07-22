package governance

import (
	"encoding/json"
	"sort"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/cli/protocol"
)

const validationDomain = "nexactl.governance"

// Issue identifies one stable, machine-readable validation failure.
type Issue struct {
	Code    string `json:"code"`
	Object  string `json:"object,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

type issueDetails struct {
	errorCode string
	issues    []Issue
}

func (d issueDetails) ErrorCode() string { return d.errorCode }

func (d issueDetails) CanonicalJSON() ([]byte, error) {
	document := struct {
		Issues []Issue `json:"issues"`
	}{Issues: append([]Issue(nil), d.issues...)}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return jcs.Transform(encoded)
}

func validationError(code, message, recommendedAction string, issues []Issue) error {
	ordered := append([]Issue(nil), issues...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Object != ordered[j].Object {
			return ordered[i].Object < ordered[j].Object
		}
		if ordered[i].Field != ordered[j].Field {
			return ordered[i].Field < ordered[j].Field
		}
		return ordered[i].Code < ordered[j].Code
	})
	projected, err := protocol.NewErrorWithDetails(
		code,
		validationDomain,
		protocol.CategoryInput,
		message,
		recommendedAction,
		issueDetails{errorCode: code, issues: ordered},
	)
	if err != nil {
		return err
	}
	return projected
}

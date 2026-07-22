package protocol

import (
	"encoding/json"
	"io"
)

const EnvelopeVersion = "nexa.dev/cli-envelope/v1"

type Envelope struct {
	APIVersion  string        `json:"apiVersion"`
	OK          bool          `json:"ok"`
	OperationID string        `json:"operationId"`
	Result      any           `json:"result,omitempty"`
	Error       *ErrorPayload `json:"error,omitempty"`
}

func Success(operationID string, result any) Envelope {
	return Envelope{
		APIVersion:  EnvelopeVersion,
		OK:          true,
		OperationID: operationID,
		Result:      result,
	}
}

func Failure(operationID string, err error) Envelope {
	payload := Project(err)
	return Envelope{
		APIVersion:  EnvelopeVersion,
		OK:          false,
		OperationID: operationID,
		Error:       &payload,
	}
}

func Encode(writer io.Writer, envelope Envelope, compact bool) error {
	encoder := json.NewEncoder(writer)
	if !compact {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(envelope)
}

package protocol

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"strings"
)

const (
	operationIDPrefix      = "op_"
	operationIDRandomBytes = 16
)

// SentinelOperationID identifies failures that happen before a random ID can be created.
const SentinelOperationID = operationIDPrefix + "00000000000000000000000000000000"

type OperationIDGenerator interface {
	NewOperationID() (string, error)
}

type OperationIDGeneratorFunc func() (string, error)

func (fn OperationIDGeneratorFunc) NewOperationID() (string, error) {
	return fn()
}

type RandomOperationIDGenerator struct{}

func (RandomOperationIDGenerator) NewOperationID() (string, error) {
	var randomBytes [operationIDRandomBytes]byte
	if _, err := io.ReadFull(rand.Reader, randomBytes[:]); err != nil {
		return "", err
	}
	return operationIDPrefix + hex.EncodeToString(randomBytes[:]), nil
}

// IsValidOperationID reports whether an ID matches the CLI protocol's stable shape.
func IsValidOperationID(operationID string) bool {
	if len(operationID) != len(operationIDPrefix)+hex.EncodedLen(operationIDRandomBytes) ||
		!strings.HasPrefix(operationID, operationIDPrefix) {
		return false
	}
	for _, character := range operationID[len(operationIDPrefix):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

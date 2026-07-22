package source

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/engine"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

const errorDomain = "nexactl.source"

type errorDetails struct {
	errorCode string
	Stage     string   `json:"stage,omitempty"`
	Reason    string   `json:"reason"`
	Pointer   string   `json:"pointer,omitempty"`
	Source    string   `json:"source,omitempty"`
	Line      int      `json:"line,omitempty"`
	Column    int      `json:"column,omitempty"`
	Cycle     []string `json:"cycle,omitempty"`
}

func (details errorDetails) ErrorCode() string { return details.errorCode }

func (details errorDetails) CanonicalJSON() ([]byte, error) {
	details.Cycle = append([]string(nil), details.Cycle...)
	encoded, err := json.Marshal(details)
	if err != nil {
		return nil, err
	}
	return jcs.Transform(encoded)
}

type sourceOwnerError interface {
	error
	Class() sourceplugin.ErrorClass
	Code() string
	Reason() string
	Source() string
	Pointer() string
	Line() int
	Column() int
	Cycle() []string
}

type releaseOwnerError interface {
	error
	Class() release.ErrorClass
	Code() string
	Reason() string
	Pointer() string
	Stage() release.Stage
}

type lockOwnerError interface {
	error
	Class() lock.ErrorClass
	Code() string
	Reason() string
	Source() string
	Pointer() string
	Line() int
	Column() int
	Stage() lock.Stage
}

type engineOwnerError interface {
	error
	Class() engine.ErrorClass
	Code() string
	Reason() string
	Pointer() string
	Stage() string
}

func projectError(err error) error {
	if err == nil {
		return nil
	}
	var engineError engineOwnerError
	if errors.As(err, &engineError) {
		category := protocol.CategoryInternal
		switch engineError.Class() {
		case engine.ErrInput, engine.ErrNotManaged:
			category = protocol.CategoryInput
		case engine.ErrConflict:
			category = protocol.CategoryConflict
		case engine.ErrUnavailable:
			category = protocol.CategoryUnavailable
		case engine.ErrExternal:
			category = protocol.CategoryExternal
		case engine.ErrCanceled:
			category = protocol.CategoryCanceled
		}
		projected := projectedError(engineError.Code(), category, engineError.Reason(), engineError.Pointer(), engineError.Stage(), "", 0, 0, nil)
		return withProjectedCause(projected, errors.Unwrap(engineError))
	}
	var already *protocol.Error
	if errors.As(err, &already) && already != nil {
		return already
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return projectedError("operation_canceled", protocol.CategoryCanceled, "context_canceled", "/context", "context", "", 0, 0, nil)
	}
	var sourceError sourceOwnerError
	if errors.As(err, &sourceError) {
		category := protocol.CategoryInput
		if sourceError.Class() == sourceplugin.ErrContractInternal {
			category = protocol.CategoryInternal
		}
		return projectedError(sourceError.Code(), category, sourceError.Reason(), sourceError.Pointer(), "source-contract", sourceError.Source(), sourceError.Line(), sourceError.Column(), sourceError.Cycle())
	}
	var releaseError releaseOwnerError
	if errors.As(err, &releaseError) {
		category := protocol.CategoryInternal
		switch releaseError.Class() {
		case release.ErrReleaseInput:
			category = protocol.CategoryInput
		case release.ErrReleaseUnavailable:
			category = protocol.CategoryUnavailable
		case release.ErrReleaseConflict:
			category = protocol.CategoryConflict
		case release.ErrReleaseCanceled:
			category = protocol.CategoryCanceled
		}
		return projectedError(releaseError.Code(), category, releaseError.Reason(), releaseError.Pointer(), string(releaseError.Stage()), "", 0, 0, nil)
	}
	var lockError lockOwnerError
	if errors.As(err, &lockError) {
		category := protocol.CategoryInternal
		switch lockError.Class() {
		case lock.ErrLockInput:
			category = protocol.CategoryInput
		case lock.ErrLockConflict:
			category = protocol.CategoryConflict
		}
		return projectedError(lockError.Code(), category, lockError.Reason(), lockError.Pointer(), string(lockError.Stage()), lockError.Source(), lockError.Line(), lockError.Column(), nil)
	}
	return projectedError("internal_error", protocol.CategoryInternal, "owner_error_unrecognized", "", "command", "", 0, 0, nil)
}

type projectedCause struct {
	projected error
	cause     error
}

func (e *projectedCause) Error() string { return e.projected.Error() }

func (e *projectedCause) Unwrap() []error { return []error{e.projected, e.cause} }

func withProjectedCause(projected, cause error) error {
	if cause == nil {
		return projected
	}
	return &projectedCause{projected: projected, cause: cause}
}

func unavailableProviderError() error {
	return projectedError("capability_unavailable", protocol.CategoryUnavailable, "provider_missing", "/provider", "provider", "", 0, 0, nil)
}

func malformedInputError(reason, pointer string) error {
	return projectedError("source_input_invalid", protocol.CategoryInput, reason, pointer, "input", "", 0, 0, nil)
}

func projectedError(code string, category protocol.Category, reason, pointer, stage, source string, line, column int, cycle []string) error {
	if !safeToken(code) {
		code = "internal_error"
		category = protocol.CategoryInternal
	}
	if !safeToken(reason) {
		reason = "owner_error_invalid"
		category = protocol.CategoryInternal
	}
	if !safeToken(stage) {
		stage = ""
	}
	if !safePointer(pointer) {
		pointer = ""
	}
	if !safeRelativeSource(source) {
		source = ""
		line, column = 0, 0
	}
	message := "source command failed"
	switch category {
	case protocol.CategoryInput:
		message = "source command input is invalid"
	case protocol.CategoryConflict:
		message = "source command conflicts with repository state"
	case protocol.CategoryUnavailable:
		message = "source capability is unavailable"
	case protocol.CategoryExternal:
		message = "source validation failed"
	case protocol.CategoryCanceled:
		message = "source command was canceled"
	case protocol.CategoryInternal:
		message = "source command failed"
	}
	projected, err := protocol.NewErrorWithDetails(
		code, errorDomain, category, message, "",
		errorDetails{errorCode: code, Stage: stage, Reason: reason, Pointer: pointer, Source: source, Line: line, Column: column, Cycle: safeCycle(cycle)},
	)
	if err != nil {
		return protocol.NewError("internal_error", errorDomain, protocol.CategoryInternal, "source command failed", "")
	}
	return projected
}

func safeToken(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

func safePointer(value string) bool {
	return value == "" || (len(value) <= 1024 && strings.HasPrefix(value, "/") && utf8.ValidString(value) && !containsControl(value))
}

func safeRelativeSource(value string) bool {
	return value != "" && len(value) <= 1024 && utf8.ValidString(value) && !filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.HasPrefix(value, "..") && !containsControl(value)
}

func safeCycle(values []string) []string {
	if len(values) > 128 {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !safeToken(value) {
			return nil
		}
		result = append(result, value)
	}
	return result
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

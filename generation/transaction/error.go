package transaction

import (
	"errors"
	"fmt"
)

var errControlSourceInvalid = errors.New("generation transaction control source invalid")
var errPlanInvalid = errors.New("generation transaction plan invalid")
var errWriteFailed = errors.New("generation transaction write failed")

type Error struct {
	code     string
	stage    string
	reason   string
	pointer  string
	message  string
	sentinel error
	cause    error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.pointer == "" {
		return e.message
	}
	return fmt.Sprintf("%s: %s", e.pointer, e.message)
}

func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}

func (e *Error) Stage() string {
	if e == nil {
		return ""
	}
	return e.stage
}

func (e *Error) Reason() string {
	if e == nil {
		return ""
	}
	return e.reason
}

func (e *Error) Pointer() string {
	if e == nil {
		return ""
	}
	return e.pointer
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return errors.Join(e.sentinel, e.cause)
}

func controlSourceError(reason, pointer string) *Error {
	messages := map[string]string{
		"role_invalid":           "control source role is invalid",
		"id_invalid":             "control source identifier is invalid",
		"id_duplicate":           "control source identifier is duplicated",
		"path_invalid":           "control source path is invalid",
		"owner_invalid":          "control source owner is invalid",
		"before_source_invalid":  "control source prior source is invalid",
		"after_empty":            "control source next content is empty",
		"after_digest_mismatch":  "control source next digest does not match content",
		"source_ref_invalid":     "control source input reference is invalid",
		"source_ref_duplicate":   "control source input reference is duplicated",
		"source_ref_unresolved":  "control source input reference is not declared",
		"artifact_path_alias":    "control source aliases an artifact path",
		"manifest_path_alias":    "control source aliases the manifest path",
		"control_path_duplicate": "control source path is duplicated",
	}
	return &Error{
		code: "transaction_control_source_invalid", stage: "input", reason: reason,
		pointer: pointer, message: messages[reason], sentinel: errControlSourceInvalid,
	}
}

func planInputError(reason, pointer string) *Error {
	messages := map[string]string{
		"repository_invalid":          "repository root is invalid",
		"generator_invalid":           "generator identity is invalid",
		"previous_generator_mismatch": "previous manifest belongs to another generator",
		"source_invalid":              "generation source is invalid",
		"source_duplicate":            "generation source is duplicated",
		"artifact_id_invalid":         "artifact identifier is invalid",
		"artifact_id_duplicate":       "artifact identifier is duplicated",
		"artifact_path_invalid":       "artifact path is invalid",
		"artifact_path_duplicate":     "artifact path is duplicated",
		"artifact_owner_invalid":      "artifact owner is invalid",
		"artifact_source_invalid":     "artifact source reference is invalid",
		"artifact_source_duplicate":   "artifact source reference is duplicated",
		"artifact_source_unresolved":  "artifact source reference is not declared",
		"stale_policy_invalid":        "artifact stale policy is invalid",
		"manifest_path_invalid":       "manifest path is invalid",
		"manifest_path_alias":         "manifest path aliases an artifact",
		"current_path_unsafe":         "current artifact path is unsafe",
		"current_read_failed":         "current artifact cannot be read",
		"feature_unsupported":         "generation plan feature is not available",
		"canonical_invalid":           "generation plan cannot be canonicalized",
		"candidate_invalid":           "generation candidate is invalid",
		"source_revalidation_missing": "generation source revalidation is required",
	}
	message := messages[reason]
	if message == "" {
		message = "generation plan input is invalid"
	}
	return &Error{code: "transaction_plan_invalid", stage: "input", reason: reason, pointer: pointer, message: message, sentinel: errPlanInvalid}
}

func evaluationError(stage, reason, pointer string) *Error {
	return evaluationCauseError(stage, reason, pointer, nil)
}

func evaluationCauseError(stage, reason, pointer string, cause error) *Error {
	messages := map[string]string{
		"ownership_probe_failed": "artifact ownership cannot be inspected",
		"current_read_failed":    "current artifact cannot be read",
		"plan_invalid":           "generation plan is invalid",
		"repository_invalid":     "repository root is invalid",
		"canonical_invalid":      "generation result cannot be canonicalized",
	}
	return &Error{
		code: "transaction_" + stage + "_failed", stage: stage, reason: reason, pointer: pointer,
		message: messages[reason], sentinel: errPlanInvalid, cause: cause,
	}
}

func writeError(reason, pointer string) *Error {
	return writeCauseError(reason, pointer, nil)
}

func writeCauseError(reason, pointer string, cause error) *Error {
	messages := map[string]string{
		"plan_invalid":               "generation plan is invalid",
		"plan_digest_mismatch":       "generation plan digest does not match",
		"plan_conflict":              "generation plan contains conflicts",
		"repository_invalid":         "repository root is invalid",
		"current_changed_after_plan": "repository state changed after planning",
		"stage_failed":               "generation outputs cannot be staged",
		"commit_failed":              "generation outputs cannot be committed",
		"cancelled":                  "generation write was cancelled",
	}
	return &Error{code: "transaction_write_failed", stage: "write", reason: reason, pointer: pointer, message: messages[reason], sentinel: errWriteFailed, cause: cause}
}

package directwrite

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/gowebpki/jcs"
)

const (
	GenerationResultAPIVersion       = "nexa.dev/generation-result/v2"
	GenerationResultKind             = "GenerationResult"
	GenerationResultStatusGenerated  = "generated"
	GenerationErrorDetailsAPIVersion = "nexa.dev/generation-error-details/v2"
	GenerationErrorDetailsKind       = "GenerationErrorDetails"
)

// FailureStage is the command stage that failed.
type FailureStage string

const (
	FailureStageLoad          FailureStage = "load"
	FailureStageValidateInput FailureStage = "validate-input"
	FailureStageResolveOutput FailureStage = "resolve-output"
	FailureStageInvokeTool    FailureStage = "invoke-tool"
	FailureStageWrite         FailureStage = "write"
	FailureStagePostValidate  FailureStage = "post-validate"
)

// ScopeState reports whether the complete output scope set was resolved.
type ScopeState string

const (
	ScopeStateUnresolved ScopeState = "unresolved"
	ScopeStateResolved   ScopeState = "resolved"
)

// ChangeEvidence describes whether completed paths cover all possible changes.
type ChangeEvidence string

const (
	ChangeEvidenceComplete ChangeEvidence = "complete"
	ChangeEvidenceHostOnly ChangeEvidence = "host-only"
)

// GenerationResult is the exact successful generation result v2 document.
type GenerationResult struct {
	APIVersion   string        `json:"apiVersion"`
	Kind         string        `json:"kind"`
	Status       string        `json:"status"`
	OutputScopes []OutputScope `json:"outputScopes"`
}

// GenerationErrorDetails is the closed error.details v2 document.
type GenerationErrorDetails struct {
	APIVersion       string         `json:"apiVersion"`
	Kind             string         `json:"kind"`
	Stage            FailureStage   `json:"stage"`
	ScopeState       ScopeState     `json:"scopeState"`
	OutputScopes     []OutputScope  `json:"outputScopes"`
	CompletedWrites  []string       `json:"completedWrites"`
	CompletedDeletes []string       `json:"completedDeletes"`
	ChangeEvidence   ChangeEvidence `json:"changeEvidence"`
}

// CanonicalGenerationResult validates and projects a deterministic result.
func CanonicalGenerationResult(input GenerationResult) ([]byte, error) {
	input, err := normalizeGenerationResult(input)
	if err != nil {
		return nil, err
	}
	if err := validateGenerationResultSchema(input); err != nil {
		return nil, fmt.Errorf("generation result document does not match schema: %w", err)
	}
	return canonicalJSON(input)
}

// ParseGenerationResult strictly parses a canonical result document.
func ParseGenerationResult(data []byte) (GenerationResult, error) {
	var result GenerationResult
	if err := strictJSON(data, &result); err != nil {
		return GenerationResult{}, fmt.Errorf("generation result document is invalid: %w", err)
	}
	if err := validateGenerationResultSchema(result); err != nil {
		return GenerationResult{}, fmt.Errorf("generation result document does not match schema: %w", err)
	}
	canonical, err := CanonicalGenerationResult(result)
	if err != nil {
		return GenerationResult{}, err
	}
	if !bytes.Equal(data, canonical) {
		return GenerationResult{}, errors.New("generation result document is not canonical")
	}
	return result, nil
}

// CanonicalGenerationErrorDetails validates and projects deterministic details.
func CanonicalGenerationErrorDetails(input GenerationErrorDetails) ([]byte, error) {
	input, err := normalizeGenerationErrorDetails(input)
	if err != nil {
		return nil, err
	}
	if err := validateGenerationErrorDetailsSchema(input); err != nil {
		return nil, fmt.Errorf("generation error details document does not match schema: %w", err)
	}
	return canonicalJSON(input)
}

// ParseGenerationErrorDetails strictly parses a canonical error-details document.
func ParseGenerationErrorDetails(data []byte) (GenerationErrorDetails, error) {
	var details GenerationErrorDetails
	if err := strictJSON(data, &details); err != nil {
		return GenerationErrorDetails{}, fmt.Errorf("generation error details document is invalid: %w", err)
	}
	if err := validateGenerationErrorDetailsSchema(details); err != nil {
		return GenerationErrorDetails{}, fmt.Errorf("generation error details document does not match schema: %w", err)
	}
	canonical, err := CanonicalGenerationErrorDetails(details)
	if err != nil {
		return GenerationErrorDetails{}, err
	}
	if !bytes.Equal(data, canonical) {
		return GenerationErrorDetails{}, errors.New("generation error details document is not canonical")
	}
	return details, nil
}

func normalizeGenerationResult(input GenerationResult) (GenerationResult, error) {
	if input.APIVersion != GenerationResultAPIVersion || input.Kind != GenerationResultKind || input.Status != GenerationResultStatusGenerated {
		return GenerationResult{}, errors.New("generation result identity or status is invalid")
	}
	scopes, err := normalizeWireScopes(input.OutputScopes, true)
	if err != nil {
		return GenerationResult{}, err
	}
	input.OutputScopes = scopes
	return input, nil
}

func normalizeGenerationErrorDetails(input GenerationErrorDetails) (GenerationErrorDetails, error) {
	if input.APIVersion != GenerationErrorDetailsAPIVersion || input.Kind != GenerationErrorDetailsKind {
		return GenerationErrorDetails{}, errors.New("generation error details identity is invalid")
	}
	if !validFailureStage(input.Stage) || (input.ScopeState != ScopeStateUnresolved && input.ScopeState != ScopeStateResolved) || (input.ChangeEvidence != ChangeEvidenceComplete && input.ChangeEvidence != ChangeEvidenceHostOnly) {
		return GenerationErrorDetails{}, errors.New("generation error details enum is invalid")
	}
	if input.ScopeState == ScopeStateUnresolved {
		if len(input.OutputScopes) != 0 || len(input.CompletedWrites) != 0 || len(input.CompletedDeletes) != 0 {
			return GenerationErrorDetails{}, errors.New("unresolved scope state requires empty scopes and completed paths")
		}
		input.OutputScopes = []OutputScope{}
		input.CompletedWrites = []string{}
		input.CompletedDeletes = []string{}
		return input, nil
	}
	scopes, err := normalizeWireScopes(input.OutputScopes, true)
	if err != nil {
		return GenerationErrorDetails{}, err
	}
	writes := make([]OutputFile, len(input.CompletedWrites))
	for index, item := range input.CompletedWrites {
		writes[index].Path = item
	}
	set, err := normalizeMutations(MutationSet{Scopes: scopes, Writes: writes, Deletes: input.CompletedDeletes})
	if err != nil {
		return GenerationErrorDetails{}, fmt.Errorf("completed path is invalid: %w", err)
	}
	input.OutputScopes = set.scopes
	input.CompletedWrites = make([]string, len(set.writes))
	for index, item := range set.writes {
		input.CompletedWrites[index] = item.Path
	}
	input.CompletedDeletes = set.deletes
	return input, nil
}

func normalizeWireScopes(scopes []OutputScope, require bool) ([]OutputScope, error) {
	if !require && len(scopes) == 0 {
		return []OutputScope{}, nil
	}
	set, err := normalizeMutations(MutationSet{Scopes: scopes})
	if err != nil {
		return nil, err
	}
	return set.scopes, nil
}

func validFailureStage(stage FailureStage) bool {
	switch stage {
	case FailureStageLoad, FailureStageValidateInput, FailureStageResolveOutput, FailureStageInvokeTool, FailureStageWrite, FailureStagePostValidate:
		return true
	default:
		return false
	}
}

func strictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func canonicalJSON(input any) ([]byte, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return jcs.Transform(encoded)
}

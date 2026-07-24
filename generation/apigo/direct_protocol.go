package apigo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"unicode/utf8"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/directwrite"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/module"
)

const (
	APIGoRequestAPIVersion = "nexa.dev/api-go-request/v2"
	APIGoRequestKind       = "APIGoRequest"
	APIGoResultAPIVersion  = "nexa.dev/api-go-result/v2"
	APIGoResultKind        = "APIGoResult"
	APIGoResultGenerated   = "generated"
)

var staticInputIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)

type StaticInput struct {
	ID     string            `json:"id"`
	Path   string            `json:"path"`
	Digest provenance.Digest `json:"digest"`
}

type staticInputWire struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// APIGoRequest is the exact typed direct API generator input.
type APIGoRequest struct {
	APIVersion    string
	Kind          string
	CoreServiceID string
	ModulePath    string
	HTTPAPIIR     httpapi.Snapshot
	APIEntry      string
	StaticInputs  []StaticInput
	OutputScopes  []directwrite.OutputScope
}

type APIGoResult struct {
	APIVersion    string                    `json:"apiVersion"`
	Kind          string                    `json:"kind"`
	Status        string                    `json:"status"`
	CoreServiceID string                    `json:"coreServiceId"`
	InputDigest   provenance.Digest         `json:"inputDigest"`
	OutputScopes  []directwrite.OutputScope `json:"outputScopes"`
}

type apiGoResultWire struct {
	APIVersion    string                    `json:"apiVersion"`
	Kind          string                    `json:"kind"`
	Status        string                    `json:"status"`
	CoreServiceID string                    `json:"coreServiceId"`
	InputDigest   string                    `json:"inputDigest"`
	OutputScopes  []directwrite.OutputScope `json:"outputScopes"`
}

type apiGoRequestWire struct {
	APIVersion    string                    `json:"apiVersion"`
	Kind          string                    `json:"kind"`
	CoreServiceID string                    `json:"coreServiceId"`
	ModulePath    string                    `json:"modulePath"`
	HTTPAPIIR     json.RawMessage           `json:"httpAPIIR"`
	APIEntry      string                    `json:"apiEntry"`
	StaticInputs  []staticInputWire         `json:"staticInputs"`
	OutputScopes  []directwrite.OutputScope `json:"outputScopes"`
}

func CanonicalAPIGoRequest(input APIGoRequest) ([]byte, error) {
	if input.APIVersion != APIGoRequestAPIVersion || input.Kind != APIGoRequestKind || !serviceIDPattern.MatchString(input.CoreServiceID) || module.CheckPath(input.ModulePath) != nil {
		return nil, errors.New("API Go request identity or module is invalid")
	}
	ir, err := input.HTTPAPIIR.CanonicalJSON()
	if err != nil {
		return nil, fmt.Errorf("API Go request HTTPAPIIR is invalid: %w", err)
	}
	if filepath.Ext(input.APIEntry) != ".api" || toolchain.ValidateRepositoryPath(input.APIEntry) != nil {
		return nil, errors.New("API Go request entry is invalid")
	}
	staticInputs, err := normalizeStaticInputs(input.StaticInputs)
	if err != nil {
		return nil, err
	}
	entryDeclared := false
	for _, item := range staticInputs {
		entryDeclared = entryDeclared || item.Path == input.APIEntry
	}
	if !entryDeclared {
		return nil, errors.New("API Go request entry is not a declared static input")
	}
	scopes, err := toolchain.NormalizeOutputScopes(input.OutputScopes)
	if err != nil {
		return nil, fmt.Errorf("API Go request output scopes are invalid: %w", err)
	}
	wireInputs := make([]staticInputWire, len(staticInputs))
	for index, item := range staticInputs {
		wireInputs[index] = staticInputWire{ID: item.ID, Path: item.Path, Digest: item.Digest.String()}
	}
	wire := apiGoRequestWire{APIVersion: input.APIVersion, Kind: input.Kind, CoreServiceID: input.CoreServiceID, ModulePath: input.ModulePath, HTTPAPIIR: ir, APIEntry: input.APIEntry, StaticInputs: wireInputs, OutputScopes: scopes}
	if err := validateAPIGoRequestSchema(wire); err != nil {
		return nil, fmt.Errorf("API Go request does not match schema: %w", err)
	}
	return canonicalAPIJSON(wire)
}

func ParseAPIGoRequest(data []byte) (APIGoRequest, error) {
	var wire apiGoRequestWire
	if err := strictAPIJSON(data, &wire); err != nil {
		return APIGoRequest{}, fmt.Errorf("API Go request is invalid: %w", err)
	}
	if err := validateAPIGoRequestSchema(wire); err != nil {
		return APIGoRequest{}, fmt.Errorf("API Go request does not match schema: %w", err)
	}
	source, _ := provenance.ParseDomainSource("nexa/tool/api-go-request/http-api-ir.json")
	ir, err := httpapi.ParseSnapshot(source, wire.HTTPAPIIR)
	if err != nil {
		return APIGoRequest{}, fmt.Errorf("API Go request HTTPAPIIR is invalid: %w", err)
	}
	staticInputs := make([]StaticInput, len(wire.StaticInputs))
	for index, item := range wire.StaticInputs {
		digest, digestErr := provenance.ParseDigest(item.Digest)
		if digestErr != nil {
			return APIGoRequest{}, errors.New("API Go request static input digest is invalid")
		}
		staticInputs[index] = StaticInput{ID: item.ID, Path: item.Path, Digest: digest}
	}
	result := APIGoRequest{APIVersion: wire.APIVersion, Kind: wire.Kind, CoreServiceID: wire.CoreServiceID, ModulePath: wire.ModulePath, HTTPAPIIR: ir, APIEntry: wire.APIEntry, StaticInputs: staticInputs, OutputScopes: wire.OutputScopes}
	canonical, err := CanonicalAPIGoRequest(result)
	if err != nil {
		return APIGoRequest{}, err
	}
	if !bytes.Equal(data, canonical) {
		return APIGoRequest{}, errors.New("API Go request is not canonical")
	}
	result.StaticInputs, _ = normalizeStaticInputs(result.StaticInputs)
	result.OutputScopes, _ = toolchain.NormalizeOutputScopes(result.OutputScopes)
	return result, nil
}

func CanonicalAPIGoResult(input APIGoResult) ([]byte, error) {
	if input.APIVersion != APIGoResultAPIVersion || input.Kind != APIGoResultKind || input.Status != APIGoResultGenerated || !serviceIDPattern.MatchString(input.CoreServiceID) {
		return nil, errors.New("API Go result identity or status is invalid")
	}
	if _, err := provenance.ParseDigest(input.InputDigest.String()); err != nil {
		return nil, errors.New("API Go result input digest is invalid")
	}
	scopes, err := toolchain.NormalizeOutputScopes(input.OutputScopes)
	if err != nil {
		return nil, fmt.Errorf("API Go result output scopes are invalid: %w", err)
	}
	input.OutputScopes = scopes
	if err := validateAPIGoResultSchema(input); err != nil {
		return nil, fmt.Errorf("API Go result does not match schema: %w", err)
	}
	return canonicalAPIJSON(input)
}

func ParseAPIGoResult(data []byte) (APIGoResult, error) {
	var wire apiGoResultWire
	if err := strictAPIJSON(data, &wire); err != nil {
		return APIGoResult{}, fmt.Errorf("API Go result is invalid: %w", err)
	}
	if err := validateAPIGoResultSchema(wire); err != nil {
		return APIGoResult{}, fmt.Errorf("API Go result does not match schema: %w", err)
	}
	digest, err := provenance.ParseDigest(wire.InputDigest)
	if err != nil {
		return APIGoResult{}, errors.New("API Go result input digest is invalid")
	}
	result := APIGoResult{APIVersion: wire.APIVersion, Kind: wire.Kind, Status: wire.Status, CoreServiceID: wire.CoreServiceID, InputDigest: digest, OutputScopes: wire.OutputScopes}
	canonical, err := CanonicalAPIGoResult(result)
	if err != nil {
		return APIGoResult{}, err
	}
	if !bytes.Equal(data, canonical) {
		return APIGoResult{}, errors.New("API Go result is not canonical")
	}
	result.OutputScopes, _ = toolchain.NormalizeOutputScopes(result.OutputScopes)
	return result, nil
}

func normalizeStaticInputs(input []StaticInput) ([]StaticInput, error) {
	result := append([]StaticInput(nil), input...)
	ids, paths := make(map[string]struct{}, len(result)), make(map[string]struct{}, len(result))
	for _, item := range result {
		if !staticInputIDPattern.MatchString(item.ID) || toolchain.ValidateRepositoryPath(item.Path) != nil {
			return nil, errors.New("API Go request static input is invalid")
		}
		if _, err := provenance.ParseDigest(item.Digest.String()); err != nil {
			return nil, errors.New("API Go request static input digest is invalid")
		}
		if _, duplicate := ids[item.ID]; duplicate {
			return nil, errors.New("API Go request static inputs are duplicated")
		}
		if _, duplicate := paths[item.Path]; duplicate {
			return nil, errors.New("API Go request static inputs are duplicated")
		}
		ids[item.ID], paths[item.Path] = struct{}{}, struct{}{}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ID == result[j].ID {
			return result[i].Path < result[j].Path
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func strictAPIJSON(data []byte, target any) error {
	if !utf8.Valid(data) {
		return errors.New("JSON is not valid UTF-8")
	}
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
func canonicalAPIJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return jcs.Transform(encoded)
}

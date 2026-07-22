package entity

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/text/unicode/norm"
)

const MaxEntHelperErrorPointerBytes = 512

type EntHelperErrorField string

const (
	EntHelperErrorFieldNone    EntHelperErrorField = ""
	EntHelperErrorFieldCode    EntHelperErrorField = "code"
	EntHelperErrorFieldReason  EntHelperErrorField = "reason"
	EntHelperErrorFieldPointer EntHelperErrorField = "pointer"
	EntHelperErrorFieldSource  EntHelperErrorField = "source"
)

type EntHelperErrorProjection struct {
	code    string
	reason  string
	pointer string
	source  string
}

func (p EntHelperErrorProjection) Code() string    { return p.code }
func (p EntHelperErrorProjection) Reason() string  { return p.reason }
func (p EntHelperErrorProjection) Pointer() string { return p.pointer }
func (p EntHelperErrorProjection) Source() string  { return p.source }

type EntHelperErrorValidationError struct{ field EntHelperErrorField }

func (e *EntHelperErrorValidationError) Error() string {
	if e == nil {
		return ""
	}
	return "invalid Entity helper error projection"
}

func (e *EntHelperErrorValidationError) Field() EntHelperErrorField {
	if e == nil {
		return EntHelperErrorFieldNone
	}
	return e.field
}

func ProjectEntHelperError(err error) (EntHelperErrorProjection, bool) {
	owner, ok := err.(*Error)
	if !ok || owner == nil || owner.code == "entity_snapshot_invalid" {
		return EntHelperErrorProjection{}, false
	}
	projection, validationErr := ParseEntHelperErrorProjection(owner.code, owner.reason, owner.pointer, owner.source)
	if validationErr != nil {
		return EntHelperErrorProjection{}, false
	}
	return projection, true
}

func ParseEntHelperErrorProjection(code, reason, pointer, source string) (EntHelperErrorProjection, *EntHelperErrorValidationError) {
	if !validErrorToken(code) || !knownEntityHelperCode(code) {
		return invalidEntHelperProjection(EntHelperErrorFieldCode)
	}
	if !validErrorToken(reason) || !knownEntityHelperReason(reason) {
		return invalidEntHelperProjection(EntHelperErrorFieldReason)
	}
	if !validHelperPointer(pointer) {
		return invalidEntHelperProjection(EntHelperErrorFieldPointer)
	}
	if source != "" {
		if _, err := provenance.ParseDomainSource(source); err != nil {
			return invalidEntHelperProjection(EntHelperErrorFieldSource)
		}
	}
	if !entityHelperCodeSupportsReason(code, reason) {
		return invalidEntHelperProjection(EntHelperErrorFieldReason)
	}
	if !knownEntityHelperPointer(code, reason, pointer) {
		return invalidEntHelperProjection(EntHelperErrorFieldPointer)
	}
	if !knownEntityHelperSource(code, source) {
		return invalidEntHelperProjection(EntHelperErrorFieldSource)
	}
	return EntHelperErrorProjection{code: code, reason: reason, pointer: pointer, source: source}, nil
}

func invalidEntHelperProjection(field EntHelperErrorField) (EntHelperErrorProjection, *EntHelperErrorValidationError) {
	return EntHelperErrorProjection{}, &EntHelperErrorValidationError{field: field}
}

var errorTokenPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func validErrorToken(value string) bool { return errorTokenPattern.MatchString(value) }

func knownEntityHelperCode(code string) bool {
	return code == "entity_input_invalid" || code == "entity_graph_load_failed" || code == "entity_ir_invalid"
}

var entityInputPointers = map[string]string{
	"repository_root_invalid": "/repositoryRoot",
	"scratch_root_invalid":    "/scratchRoot",
	"scratch_root_overlap":    "/scratchRoot",
	"scratch_cwd_mismatch":    "/scratchRoot",
	"module_dir_invalid":      "/moduleDir",
	"module_dir_escape":       "/moduleDir",
	"schema_dir_invalid":      "/schemaDir",
	"schema_dir_escape":       "/schemaDir",
	"schema_position_escape":  "/schemaDir",
	"build_tag_invalid":       "/buildTags/",
	"build_tag_duplicate":     "/buildTags/",
	"verified_run_invalid":    "/verifiedRun",
}

var entityIRReasons = map[string]struct{}{
	"entity_name_invalid": {}, "entity_id_duplicate": {}, "schema_meta_missing": {},
	"entity_kind_unsupported": {}, "field_name_invalid": {}, "field_id_duplicate": {}, "field_meta_missing": {},
	"field_type_unsupported": {}, "enum_invalid": {}, "enum_duplicate": {}, "identity_missing": {},
	"identity_composite_unsupported": {}, "identity_strategy_invalid": {}, "policy_conflict": {}, "policy_presence_conflict": {},
	"edge_name_invalid": {}, "edge_id_duplicate": {}, "edge_target_invalid": {}, "edge_direction_invalid": {}, "edge_inverse_invalid": {}, "edge_bound_field_invalid": {}, "edge_target_missing": {}, "edge_bound_field_missing": {}, "edge_inverse_not_closed": {},
	"logical_reference_edge_conflict": {}, "physical_display_edge_invalid": {}, "physical_display_field_missing": {}, "localized_text_conflict": {},
	"source_ref_invalid": {}, "source_digest_invalid": {}, "source_conflict": {},
	"source_closure_invalid": {}, "canonical_invalid": {},
}

func knownEntityHelperReason(reason string) bool {
	if _, ok := entityInputPointers[reason]; ok {
		return true
	}
	if reason == "graph_load_failed" || reason == "graph_panic" || reason == "source_projection_failed" || reason == "source_projection_drift" {
		return true
	}
	_, ok := entityIRReasons[reason]
	return ok
}

func entityHelperCodeSupportsReason(code, reason string) bool {
	switch code {
	case "entity_input_invalid":
		_, ok := entityInputPointers[reason]
		return ok
	case "entity_graph_load_failed":
		return reason == "graph_load_failed" || reason == "graph_panic" || reason == "source_projection_failed" || reason == "source_projection_drift"
	case "entity_ir_invalid":
		_, ok := entityIRReasons[reason]
		return ok
	default:
		return false
	}
}

func knownEntityHelperPointer(code, reason, pointer string) bool {
	switch code {
	case "entity_input_invalid":
		expected := entityInputPointers[reason]
		if reason == "build_tag_invalid" || reason == "build_tag_duplicate" {
			return strings.HasPrefix(pointer, expected) && canonicalIndex(strings.TrimPrefix(pointer, expected))
		}
		return pointer == expected
	case "entity_graph_load_failed":
		return pointer == ""
	case "entity_ir_invalid":
		return validEntityIRPointer(reason, pointer)
	default:
		return false
	}
}

func knownEntityHelperSource(code, source string) bool {
	if code == "entity_input_invalid" {
		return source == ""
	}
	if source == "" {
		return false
	}
	_, err := provenance.ParseDomainSource(source)
	return err == nil
}

func validEntityIRPointer(reason, pointer string) bool {
	if reason == "canonical_invalid" {
		if pointer == "/document" || pointer == "/sources" {
			return true
		}
		parts := splitCanonicalPointer(pointer)
		return len(parts) == 2 && parts[0] == "entities" && canonicalIndex(parts[1]) ||
			len(parts) == 4 && parts[0] == "entities" && canonicalIndex(parts[1]) && (parts[2] == "fields" || parts[2] == "edges") && canonicalIndex(parts[3])
	}
	if reason == "source_closure_invalid" {
		return pointer == "/sources"
	}
	parts := splitCanonicalPointer(pointer)
	if len(parts) == 3 && parts[0] == "executionModuleSources" && canonicalIndex(parts[1]) {
		switch reason {
		case "source_ref_invalid", "source_conflict":
			return parts[2] == "ref"
		case "source_digest_invalid":
			return parts[2] == "digest"
		}
	}
	if reason == "source_conflict" {
		if len(parts) == 3 && parts[0] == "entities" && canonicalIndex(parts[1]) && parts[2] == "sourceRef" {
			return true
		}
		if len(parts) >= 5 && parts[0] == "entities" && canonicalIndex(parts[1]) && parts[2] == "fields" && canonicalIndex(parts[3]) {
			return len(parts) == 5 && parts[4] == "sourceRef"
		}
		if len(parts) >= 5 && parts[0] == "entities" && canonicalIndex(parts[1]) && parts[2] == "edges" && canonicalIndex(parts[3]) {
			return len(parts) == 5 && parts[4] == "sourceRef"
		}
		return false
	}
	if len(parts) < 3 || parts[0] != "entities" || !canonicalIndex(parts[1]) {
		return false
	}
	if reason == "localized_text_conflict" {
		joined := strings.Join(parts[2:], "/")
		return joined == "schemaMeta/payload/label" || joined == "schemaMeta/payload/description" ||
			len(parts) >= 6 && parts[2] == "fields" && canonicalIndex(parts[3]) &&
				(strings.Join(parts[4:], "/") == "fieldMeta/payload/label" || strings.Join(parts[4:], "/") == "fieldMeta/payload/description")
	}
	if len(parts) == 3 {
		switch reason {
		case "entity_name_invalid":
			return parts[2] == "name"
		case "entity_id_duplicate":
			return parts[2] == "id"
		case "schema_meta_missing":
			return parts[2] == "schemaMeta"
		case "entity_kind_unsupported":
			return parts[2] == "kind"
		case "identity_missing", "identity_composite_unsupported", "identity_strategy_invalid":
			return parts[2] == "identity"
		case "field_type_unsupported":
			return parts[2] == "identity"
		case "source_ref_invalid", "source_digest_invalid":
			return parts[2] == "sourceRef"
		}
	}
	if len(parts) >= 5 && parts[2] == "fields" && canonicalIndex(parts[3]) {
		switch reason {
		case "field_name_invalid":
			return len(parts) == 5 && parts[4] == "name"
		case "field_id_duplicate":
			return len(parts) == 5 && parts[4] == "id"
		case "field_meta_missing":
			return len(parts) == 5 && parts[4] == "fieldMeta"
		case "field_type_unsupported":
			return len(parts) == 5 && parts[4] == "type"
		case "enum_invalid":
			return len(parts) == 5 && parts[4] == "enumValues" ||
				len(parts) == 6 && parts[4] == "enumValues" && canonicalIndex(parts[5])
		case "enum_duplicate":
			return len(parts) == 7 && parts[4] == "enumValues" && canonicalIndex(parts[5]) && (parts[6] == "name" || parts[6] == "value")
		case "policy_conflict", "policy_presence_conflict":
			return strings.Join(parts[4:], "/") == "fieldMeta/payload/crud"
		case "logical_reference_edge_conflict":
			return strings.Join(parts[4:], "/") == "fieldMeta/payload/logicalReference"
		case "physical_display_edge_invalid":
			return strings.Join(parts[4:], "/") == "fieldMeta/payload/physicalDisplay"
		case "physical_display_field_missing":
			return strings.Join(parts[4:], "/") == "fieldMeta/payload/physicalDisplay/field"
		case "source_ref_invalid", "source_digest_invalid":
			return len(parts) == 5 && parts[4] == "sourceRef"
		}
	}
	if len(parts) >= 5 && parts[2] == "edges" && canonicalIndex(parts[3]) {
		switch reason {
		case "edge_name_invalid":
			return len(parts) == 5 && parts[4] == "name"
		case "edge_id_duplicate":
			return len(parts) == 5 && parts[4] == "id"
		case "edge_target_invalid", "edge_target_missing":
			return len(parts) == 5 && parts[4] == "targetEntityId"
		case "edge_direction_invalid":
			return len(parts) == 5 && parts[4] == "direction"
		case "edge_inverse_invalid", "edge_inverse_not_closed":
			return len(parts) == 5 && parts[4] == "inverseName"
		case "edge_bound_field_invalid", "edge_bound_field_missing":
			return len(parts) == 5 && parts[4] == "boundFieldId"
		case "source_ref_invalid", "source_digest_invalid", "source_conflict":
			return len(parts) == 5 && parts[4] == "sourceRef"
		}
	}
	return false
}

func splitCanonicalPointer(pointer string) []string {
	if pointer == "" || pointer[0] != '/' {
		return nil
	}
	parts := strings.Split(pointer[1:], "/")
	for _, part := range parts {
		for index := 0; index < len(part); index++ {
			if part[index] != '~' {
				continue
			}
			if index+1 >= len(part) || (part[index+1] != '0' && part[index+1] != '1') {
				return nil
			}
			index++
		}
	}
	return parts
}

func canonicalIndex(value string) bool {
	if value == "0" {
		return true
	}
	if value == "" || value[0] < '1' || value[0] > '9' {
		return false
	}
	for index := 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	_, err := strconv.Atoi(value)
	return err == nil
}

func validHelperPointer(pointer string) bool {
	if !utf8.ValidString(pointer) || len(pointer) > MaxEntHelperErrorPointerBytes || !norm.NFC.IsNormalString(pointer) {
		return false
	}
	for _, character := range pointer {
		if unicode.Is(unicode.Cc, character) {
			return false
		}
	}
	return pointer == "" || splitCanonicalPointer(pointer) != nil
}

package crudbuild

import "strings"

type DomainField string

const (
	DomainFieldNone    DomainField = ""
	DomainFieldCode    DomainField = "code"
	DomainFieldReason  DomainField = "reason"
	DomainFieldPointer DomainField = "pointer"
	DomainFieldSource  DomainField = "source"
)

type DomainProjection struct{ code, reason, pointer string }

func (p DomainProjection) Code() string    { return p.code }
func (p DomainProjection) Reason() string  { return p.reason }
func (p DomainProjection) Pointer() string { return p.pointer }
func (p DomainProjection) Source() string  { return "" }

type DomainValidationError struct{ field DomainField }

func (e *DomainValidationError) Error() string { return "invalid CRUD helper error projection" }
func (e *DomainValidationError) Field() DomainField {
	if e == nil {
		return DomainFieldNone
	}
	return e.field
}

func ProjectEntHelperError(err error) (DomainProjection, bool) {
	owner, ok := err.(*Error)
	if !ok || owner == nil || owner.code == "crud_lock_invalid" {
		return DomainProjection{}, false
	}
	projection, validationErr := ParseEntHelperErrorProjection(owner.code, owner.reason, owner.pointer, owner.source)
	if validationErr != nil {
		return DomainProjection{}, false
	}
	return projection, true
}
func ParseEntHelperErrorProjection(code, reason, pointer, source string) (DomainProjection, *DomainValidationError) {
	reasons, ok := helperReasons[code]
	if !ok {
		return invalidDomain(DomainFieldCode)
	}
	if _, ok := reasons[reason]; !ok {
		return invalidDomain(DomainFieldReason)
	}
	if !validDomainPointer(code, reason, pointer) {
		return invalidDomain(DomainFieldPointer)
	}
	if source != "" {
		return invalidDomain(DomainFieldSource)
	}
	return DomainProjection{code: code, reason: reason, pointer: pointer}, nil
}
func invalidDomain(field DomainField) (DomainProjection, *DomainValidationError) {
	return DomainProjection{}, &DomainValidationError{field: field}
}

var helperReasons = map[string]map[string]struct{}{
	"crud_build_invalid":        setOf("document_state_invalid", "service_id_invalid", "proto_package_invalid", "go_package_invalid", "crud_operation_invalid", "identity_type_unsupported", "field_type_unsupported", "read_policy_conflict", "mutation_policy_conflict", "message_identity_duplicate", "method_identity_duplicate", "multi_tenant_disabled", "source_closure_invalid", "canonical_invalid", "schema_key_invalid", "schema_key_collision", "schema_key_too_long"),
	"crud_wire_invalid":         setOf("field_identity_duplicate", "wire_name_invalid", "wire_name_duplicate", "wire_number_exhausted", "wire_number_duplicate", "wire_number_reserved", "wire_type_unsupported"),
	"crud_compatibility_failed": setOf("compatibility_lock_missing", "lock_service_mismatch", "lock_schema_mismatch", "published_baseline_mismatch", "wire_incompatible", "retired_assignment_conflict", "reservation_conflict", "lock_digest_mismatch"),
	"crud_render_invalid":       setOf("proto_symbol_invalid", "proto_symbol_duplicate", "proto_import_invalid", "proto_artifact_path_invalid", "proto_artifact_owner_invalid", "render_failed", "render_canonical_invalid"),
	"crud_proto_compile_failed": setOf("proto_compile_failed"),
}

func setOf(values ...string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
func validDomainPointer(code, reason, pointer string) bool {
	switch code {
	case "crud_build_invalid":
		switch reason {
		case "document_state_invalid":
			return pointer == "/entities" || pointer == "/entities/0/identity"
		case "service_id_invalid":
			return pointer == "/serviceId"
		case "proto_package_invalid":
			return pointer == "/protoPackage"
		case "go_package_invalid":
			return pointer == "/goPackage"
		case "source_closure_invalid":
			return pointer == "/sources"
		case "canonical_invalid":
			return pointer == "/document" || pointer == "/requestDigest" || pointer == "/plan"
		default:
			return strings.HasPrefix(pointer, "/entities/") || strings.HasPrefix(pointer, "/messages/") || strings.HasPrefix(pointer, "/methods/")
		}
	case "crud_wire_invalid":
		return strings.HasPrefix(pointer, "/messages/") || strings.HasPrefix(pointer, "/enums/")
	case "crud_compatibility_failed":
		return strings.HasPrefix(pointer, "/existingLock") || strings.HasPrefix(pointer, "/publishedArtifact") || strings.HasPrefix(pointer, "/messages/")
	case "crud_render_invalid":
		return strings.HasPrefix(pointer, "/messages/") || strings.HasPrefix(pointer, "/services/") || strings.HasPrefix(pointer, "/imports/") || strings.HasPrefix(pointer, "/protoArtifact/") || strings.HasPrefix(pointer, "/lockProposal/") || pointer == "/document"
	case "crud_proto_compile_failed":
		return pointer == "/protoArtifact/bytes" || pointer == "/fragments"
	}
	return false
}

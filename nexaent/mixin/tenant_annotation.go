package mixin

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"entgo.io/ent/schema"
	"github.com/nxnminieye/nexa/internal/strictdoc"
)

const (
	// TenantAnnotationName is the strict framework-owned tenant field marker.
	TenantAnnotationName = "nexa.dev/ent-tenant-field/v1"
	tenantAnnotationKind = "EntTenantField"
)

//go:embed tenant-field-v1.schema.json
var tenantAnnotationSchema string

// TenantAnnotationSchema returns a fresh copy of the marker JSON Schema.
func TenantAnnotationSchema() []byte { return []byte(tenantAnnotationSchema) }

type tenantAnnotation struct{ duplicate bool }

func (tenantAnnotation) Name() string { return TenantAnnotationName }

func (tenantAnnotation) Merge(schema.Annotation) schema.Annotation {
	return tenantAnnotation{duplicate: true}
}

func (a tenantAnnotation) MarshalJSON() ([]byte, error) {
	if a.duplicate {
		return json.Marshal(struct {
			APIVersion string `json:"apiVersion"`
			Duplicate  bool   `json:"duplicate"`
			Kind       string `json:"kind"`
		}{TenantAnnotationName, true, tenantAnnotationKind})
	}
	return json.Marshal(struct {
		APIVersion string         `json:"apiVersion"`
		Kind       string         `json:"kind"`
		Payload    map[string]any `json:"payload"`
	}{TenantAnnotationName, tenantAnnotationKind, map[string]any{}})
}

type tenantEnvelopeProbe struct {
	APIVersion json.RawMessage `json:"apiVersion"`
	Kind       json.RawMessage `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	Duplicate  json.RawMessage `json:"duplicate"`
}

type strictTenantEnvelope struct {
	APIVersion *string         `json:"apiVersion"`
	Kind       *string         `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
}

// DecodeTenantAnnotation strictly validates a tenant marker transport envelope.
func DecodeTenantAnnotation(data []byte) error {
	document, err := strictdoc.ParseJSON(TenantAnnotationName, data)
	if err != nil {
		return err
	}
	normalized := document.JSON()
	if len(normalized) == 0 || normalized[0] != '{' {
		return fmt.Errorf("%s: document_invalid", TenantAnnotationName)
	}
	var probe tenantEnvelopeProbe
	if err := document.DecodeExact(&probe); err != nil {
		return err
	}
	if len(probe.Duplicate) != 0 {
		return fmt.Errorf("%s/duplicate: duplicate_annotation", TenantAnnotationName)
	}
	var envelope strictTenantEnvelope
	if err := document.DecodeExact(&envelope); err != nil {
		return err
	}
	if envelope.APIVersion == nil || *envelope.APIVersion != TenantAnnotationName {
		return fmt.Errorf("%s/apiVersion: document_identity_invalid", TenantAnnotationName)
	}
	if envelope.Kind == nil || *envelope.Kind != tenantAnnotationKind {
		return fmt.Errorf("%s/kind: document_identity_invalid", TenantAnnotationName)
	}
	if len(envelope.Payload) == 0 || envelope.Payload[0] != '{' {
		return fmt.Errorf("%s/payload: document_type_invalid", TenantAnnotationName)
	}
	var payload struct{}
	if err := strictdoc.DecodeJSONExact(TenantAnnotationName, envelope.Payload, &payload); err != nil {
		return err
	}
	return nil
}

var _ schema.Merger = tenantAnnotation{}

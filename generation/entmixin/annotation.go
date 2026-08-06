// Package entmixin provides framework-owned Ent mixins for standard fields.
package entmixin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"entgo.io/ent/schema"
)

const (
	// FieldAnnotationName identifies the closed annotation carried by standard mixin fields.
	FieldAnnotationName = "nexa.dev/ent-mixin-field/v1"
	fieldAnnotationKind = "EntMixinField"
)

// FieldProfile identifies one framework-defined standard field.
type FieldProfile string

const (
	ProfileTenant    FieldProfile = "tenant"
	ProfileCreatedAt FieldProfile = "created-at"
	ProfileUpdatedAt FieldProfile = "updated-at"
	ProfileSort      FieldProfile = "sort"
	ProfileStatus    FieldProfile = "status"
	ProfileDeletedAt FieldProfile = "deleted-at"
)

// FieldMetadata is the validated standard metadata for a mixin field.
type FieldMetadata struct {
	Profile         FieldProfile
	LabelZhCN       string
	LabelEnUS       string
	DescriptionZhCN string
	DescriptionEnUS string
	Control         string
	Visibility      string
	Tenant          bool
}

// Directives returns the standard supplemental facts for the field.
func (m FieldMetadata) Directives() []string {
	result := []string{
		"label.zh-CN: " + strconv.Quote(m.LabelZhCN),
		"label.en-US: " + strconv.Quote(m.LabelEnUS),
		"description.zh-CN: " + strconv.Quote(m.DescriptionZhCN),
		"description.en-US: " + strconv.Quote(m.DescriptionEnUS),
		"ui.control: " + strconv.Quote(m.Control),
		"visibility: " + strconv.Quote(m.Visibility),
	}
	if m.Tenant {
		return result
	}
	return append(result, "crud.read: \"exclude\"", "crud.mutation: \"none\"")
}

type fieldAnnotation struct {
	Profile FieldProfile
}

type fieldAnnotationWire struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Profile    FieldProfile `json:"profile"`
}

func (fieldAnnotation) Name() string { return FieldAnnotationName }

func (a fieldAnnotation) MarshalJSON() ([]byte, error) {
	if _, err := metadataFor(a.Profile); err != nil {
		return nil, err
	}
	return json.Marshal(fieldAnnotationWire{APIVersion: FieldAnnotationName, Kind: fieldAnnotationKind, Profile: a.Profile})
}

func standardField(profile FieldProfile) schema.Annotation {
	return fieldAnnotation{Profile: profile}
}

// DecodeFieldAnnotation validates an Ent-loaded standard mixin annotation.
func DecodeFieldAnnotation(value any) (FieldMetadata, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return FieldMetadata{}, fmt.Errorf("encode Ent mixin field annotation: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire fieldAnnotationWire
	if err := decoder.Decode(&wire); err != nil {
		return FieldMetadata{}, fmt.Errorf("decode Ent mixin field annotation: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return FieldMetadata{}, fmt.Errorf("decode Ent mixin field annotation: trailing JSON")
	}
	if wire.APIVersion != FieldAnnotationName || wire.Kind != fieldAnnotationKind {
		return FieldMetadata{}, fmt.Errorf("Ent mixin field annotation identity is invalid")
	}
	return metadataFor(wire.Profile)
}

func metadataFor(profile FieldProfile) (FieldMetadata, error) {
	values := map[FieldProfile]FieldMetadata{
		ProfileTenant: {
			Profile: profile, LabelZhCN: "租户 ID", LabelEnUS: "Tenant ID",
			DescriptionZhCN: "记录所属租户的内部标识。", DescriptionEnUS: "Internal identifier of the tenant that owns the record.",
			Control: "readonly", Visibility: "internal", Tenant: true,
		},
		ProfileCreatedAt: {
			Profile: profile, LabelZhCN: "创建时间", LabelEnUS: "Created At",
			DescriptionZhCN: "记录的创建时间。", DescriptionEnUS: "Time when the record was created.",
			Control: "datetime", Visibility: "internal",
		},
		ProfileUpdatedAt: {
			Profile: profile, LabelZhCN: "更新时间", LabelEnUS: "Updated At",
			DescriptionZhCN: "记录最后一次更新的时间。", DescriptionEnUS: "Time when the record was last updated.",
			Control: "datetime", Visibility: "internal",
		},
		ProfileSort: {
			Profile: profile, LabelZhCN: "排序", LabelEnUS: "Sort Order",
			DescriptionZhCN: "记录的排序值。", DescriptionEnUS: "Sort value for the record.",
			Control: "number", Visibility: "internal",
		},
		ProfileStatus: {
			Profile: profile, LabelZhCN: "状态", LabelEnUS: "Status",
			DescriptionZhCN: "记录的启用状态。", DescriptionEnUS: "Enablement status of the record.",
			Control: "select", Visibility: "internal",
		},
		ProfileDeletedAt: {
			Profile: profile, LabelZhCN: "删除时间", LabelEnUS: "Deleted At",
			DescriptionZhCN: "记录被软删除的时间。", DescriptionEnUS: "Time when the record was soft-deleted.",
			Control: "datetime", Visibility: "internal",
		},
	}
	metadata, ok := values[profile]
	if !ok {
		return FieldMetadata{}, fmt.Errorf("Ent mixin field profile %q is invalid", profile)
	}
	return metadata, nil
}

var _ schema.Annotation = fieldAnnotation{}

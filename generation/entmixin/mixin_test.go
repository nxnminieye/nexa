package entmixin_test

import (
	"testing"

	"entgo.io/ent"
	"github.com/nxnminieye/nexa/generation/entmixin"
)

func TestStandardMixinsExposeStableDescriptorsAndMetadata(t *testing.T) {
	tests := []struct {
		name     string
		mixin    ent.Mixin
		fields   []string
		profiles []entmixin.FieldProfile
	}{
		{name: "tenant", mixin: entmixin.Tenant{}, fields: []string{"tenant_id"}, profiles: []entmixin.FieldProfile{entmixin.ProfileTenant}},
		{name: "time audit", mixin: entmixin.TimeAuditMixin{}, fields: []string{"created_at", "updated_at"}, profiles: []entmixin.FieldProfile{entmixin.ProfileCreatedAt, entmixin.ProfileUpdatedAt}},
		{name: "sort", mixin: entmixin.SortMixin{}, fields: []string{"sort"}, profiles: []entmixin.FieldProfile{entmixin.ProfileSort}},
		{name: "status", mixin: entmixin.StatusMixin{}, fields: []string{"status"}, profiles: []entmixin.FieldProfile{entmixin.ProfileStatus}},
		{name: "soft delete", mixin: entmixin.SoftDeleteMixin{}, fields: []string{"deleted_at"}, profiles: []entmixin.FieldProfile{entmixin.ProfileDeletedAt}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fields := test.mixin.Fields()
			if len(fields) != len(test.fields) {
				t.Fatalf("fields = %d, want %d", len(fields), len(test.fields))
			}
			for index, item := range fields {
				descriptor := item.Descriptor()
				if descriptor.Name != test.fields[index] || descriptor.Comment == "" {
					t.Fatalf("descriptor = %+v", descriptor)
				}
				if len(descriptor.Annotations) != 1 {
					t.Fatalf("annotations = %#v", descriptor.Annotations)
				}
				metadata, err := entmixin.DecodeFieldAnnotation(descriptor.Annotations[0])
				if err != nil {
					t.Fatal(err)
				}
				if metadata.Profile != test.profiles[index] || len(metadata.Directives()) != 6 {
					t.Fatalf("metadata = %#v", metadata)
				}
			}
		})
	}
}

func TestStandardMixinFieldSemantics(t *testing.T) {
	tenant := entmixin.Tenant{}.Fields()[0].Descriptor()
	if tenant.Name != "tenant_id" || tenant.Optional || !tenant.Immutable || len(tenant.Validators) != 1 {
		t.Fatalf("tenant = %+v", tenant)
	}
	created := entmixin.TimeAuditMixin{}.Fields()[0].Descriptor()
	updated := entmixin.TimeAuditMixin{}.Fields()[1].Descriptor()
	if !created.Immutable || created.Default == nil || updated.UpdateDefault == nil {
		t.Fatalf("created = %+v updated = %+v", created, updated)
	}
	status := entmixin.StatusMixin{}.Fields()[0].Descriptor()
	if status.Default != "enabled" {
		t.Fatalf("status default = %#v", status.Default)
	}
	deleted := entmixin.SoftDeleteMixin{}.Fields()[0].Descriptor()
	if !deleted.Optional || !deleted.Nillable {
		t.Fatalf("deleted_at = %+v", deleted)
	}
}

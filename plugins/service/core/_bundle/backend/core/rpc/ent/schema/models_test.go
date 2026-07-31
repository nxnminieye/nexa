package schema

import (
	"fmt"
	"reflect"
	"testing"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	entschema "entgo.io/ent/schema"
)

func TestIAMIndexesAndOwnershipChecks(t *testing.T) {
	assertUniqueIndex(t, IdentityAccount{}.Indexes(), []string{"identity_source_code", "external_subject"}, "external_subject <> ''")
	assertUniqueIndex(t, Tenant{}.Indexes(), []string{"code"}, "")
	assertUniqueIndex(t, TenantMember{}.Indexes(), []string{"tenant_id", "identity_account_id"}, "")
	assertUniqueIndex(t, Role{}.Indexes(), []string{"tenant_id", "code"}, "")
	assertUniqueIndex(t, TenantMemberRoleGrant{}.Indexes(), []string{"tenant_id", "tenant_member_id", "role_id"}, "")
	assertUniqueIndex(t, ManagedTenantMemberRoleGrant{}.Indexes(), []string{"tenant_id", "tenant_member_id", "role_id", "source_owner"}, "")
	assertUniqueIndex(t, RolePermissionGrant{}.Indexes(), []string{"tenant_id", "role_id", "permission_action_id"}, "")
	assertUniqueIndex(t, RoleMenuGrant{}.Indexes(), []string{"tenant_id", "role_id", "menu_id"}, "")
	assertUniqueIndex(t, CatalogSourceState{}.Indexes(), []string{"source_id"}, "")
	assertFieldDefault(t, Tenant{}.Fields(), "version", "1")
	assertFieldDefault(t, TenantMember{}.Fields(), "version", "1")
	assertFieldDefault(t, Role{}.Fields(), "version", "1")
	assertFieldDefault(t, IdentityAccount{}.Fields(), "credential_version", "1")

	for name, model := range map[string]interface{ Annotations() []entschema.Annotation }{
		"role": Role{}, "managed member role": ManagedTenantMemberRoleGrant{}, "menu": Menu{},
		"permission resource": PermissionResource{}, "permission action": PermissionAction{},
	} {
		if !hasSQLCheck(model.Annotations()) {
			t.Fatalf("%s has no SQL ownership check", name)
		}
	}
}

func TestManualMemberRoleGrantCarriesTenantOnEveryRelation(t *testing.T) {
	edges := TenantMemberRoleGrant{}.Edges()
	want := []struct{ name, field string }{
		{"tenant", "tenant_id"},
		{"member", "tenant_member_id"},
		{"role", "role_id"},
	}
	if len(edges) != len(want) {
		t.Fatalf("manual member role grant edges = %d, want %d", len(edges), len(want))
	}
	for index, expected := range want {
		descriptor := edges[index].Descriptor()
		if descriptor.Name != expected.name || descriptor.Field != expected.field || !descriptor.Unique || !descriptor.Required {
			t.Fatalf("manual member role grant edge %d = %#v", index, descriptor)
		}
	}
}

func TestPermissionActionReferencesResourceByStableIdentity(t *testing.T) {
	edges := PermissionAction{}.Edges()
	if len(edges) != 2 {
		t.Fatalf("permission action edges = %d, want 2", len(edges))
	}
	descriptor := edges[0].Descriptor()
	if descriptor.Name != "resource" || descriptor.Field != "permission_resource_id" || !descriptor.Unique || !descriptor.Required {
		t.Fatalf("permission action resource edge = %#v", descriptor)
	}
}

func assertFieldDefault(t *testing.T, fields []ent.Field, name, want string) {
	t.Helper()
	for _, value := range fields {
		descriptor := value.Descriptor()
		if descriptor.Name == name {
			if got := fmt.Sprint(descriptor.Default); got != want {
				t.Fatalf("%s default = %s, want %s", name, got, want)
			}
			return
		}
	}
	t.Fatalf("field %s missing", name)
}

func assertUniqueIndex(t *testing.T, indexes []ent.Index, fields []string, where string) {
	t.Helper()
	for _, value := range indexes {
		descriptor := value.Descriptor()
		if !descriptor.Unique || !reflect.DeepEqual(descriptor.Fields, fields) {
			continue
		}
		if where == "" {
			return
		}
		for _, annotation := range descriptor.Annotations {
			switch sqlAnnotation := annotation.(type) {
			case entsql.IndexAnnotation:
				if sqlAnnotation.Where == where {
					return
				}
			case *entsql.IndexAnnotation:
				if sqlAnnotation.Where == where {
					return
				}
			}
		}
	}
	t.Fatalf("missing unique index fields=%v where=%q", fields, where)
}

func hasSQLCheck(values []entschema.Annotation) bool {
	for _, value := range values {
		annotation, ok := value.(entsql.Annotation)
		if ok && (annotation.Check != "" || len(annotation.Checks) > 0) {
			return true
		}
	}
	return false
}

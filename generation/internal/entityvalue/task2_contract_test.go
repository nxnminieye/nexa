package entityvalue

import (
	"testing"

	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
)

func TestTask2CRUDPolicyPresenceIsBidirectional(t *testing.T) {
	withoutCRUD := task2Entity(t, "Audit", nil, task2Field("name", task2Meta("audit.name", nexaent.VisibilityPublic, nexaent.UIHintText, &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: nexaent.MutationCreate})))
	if _, err := NewDocument(Projection{Entities: []EntityProjection{withoutCRUD}}); err == nil {
		t.Fatal("non-CRUD entity accepted field CRUD policy")
	}

	crud := task2CRUD(t, nexaent.CRUDCreate)
	withCRUD := task2Entity(t, "Account", &crud, task2Field("name", task2Meta("account.name", nexaent.VisibilityPublic, nexaent.UIHintText, nil)))
	if _, err := NewDocument(Projection{Entities: []EntityProjection{withCRUD}}); err == nil {
		t.Fatal("CRUD entity accepted missing field CRUD policy")
	}
}

func TestTenantMarkerOwnsFrameworkInternalCRUDPolicyAbsence(t *testing.T) {
	crud := task2CRUD(t, nexaent.CRUDCreate)
	tenant := task2Field("tenant_id", task2Meta("account.tenant_id", nexaent.VisibilityInternal, nexaent.UIHintReadonly, nil))
	tenant.Immutable = true
	tenant.IsTenantField = true
	if _, err := NewDocument(Projection{Entities: []EntityProjection{task2Entity(t, "Account", &crud, tenant)}}); err != nil {
		t.Fatalf("tenant marker with nil CRUD policy rejected: %v", err)
	}

	ordinary := tenant
	ordinary.IsTenantField = false
	if _, err := NewDocument(Projection{Entities: []EntityProjection{task2Entity(t, "Account", &crud, ordinary)}}); err == nil {
		t.Fatal("ordinary CRUD field without policy was accepted")
	}

	authored := tenant
	authored.Meta.CRUD = &nexaent.CRUDFieldPolicy{Read: nexaent.ReadExclude, Mutation: nexaent.MutationNone}
	if _, err := NewDocument(Projection{Entities: []EntityProjection{task2Entity(t, "Account", &crud, authored)}}); err == nil {
		t.Fatal("tenant marker with authored CRUD policy was accepted")
	}
}

func TestTask2CRUDPolicyMatrix(t *testing.T) {
	crud := task2CRUD(t, nexaent.CRUDCreate, nexaent.CRUDUpdate)
	tests := []struct {
		name  string
		field FieldProjection
	}{
		{"internal read", task2Field("value", task2Meta("matrix.internal_read", nexaent.VisibilityInternal, nexaent.UIHintText, &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: nexaent.MutationNone}))},
		{"internal mutation", task2Field("value", task2Meta("matrix.internal_mutation", nexaent.VisibilityInternal, nexaent.UIHintText, &nexaent.CRUDFieldPolicy{Read: nexaent.ReadExclude, Mutation: nexaent.MutationCreate}))},
		{"sensitive flag", task2Field("value", task2Meta("matrix.sensitive_flag", nexaent.VisibilitySensitive, nexaent.UIHintSensitive, &nexaent.CRUDFieldPolicy{Read: nexaent.ReadExclude, Mutation: nexaent.MutationCreate}))},
		{"sensitive hint", func() FieldProjection {
			f := task2Field("value", task2Meta("matrix.sensitive_hint", nexaent.VisibilitySensitive, nexaent.UIHintText, &nexaent.CRUDFieldPolicy{Read: nexaent.ReadExclude, Mutation: nexaent.MutationCreate}))
			f.Sensitive = true
			return f
		}()},
		{"readonly mutation", task2Field("value", task2Meta("matrix.readonly", nexaent.VisibilityPublic, nexaent.UIHintReadonly, &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: nexaent.MutationUpdate}))},
		{"immutable update", func() FieldProjection {
			f := task2Field("value", task2Meta("matrix.immutable", nexaent.VisibilityPublic, nexaent.UIHintText, &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: nexaent.MutationCreateUpdate}))
			f.Immutable = true
			return f
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entity := task2Entity(t, "Matrix", &crud, test.field)
			if _, err := NewDocument(Projection{Entities: []EntityProjection{entity}}); err == nil {
				t.Fatal("invalid CRUD policy matrix row was accepted")
			}
		})
	}

	identity := task2Field("account_id", task2Meta("matrix.identity", nexaent.VisibilityPublic, nexaent.UIHintReadonly, &nexaent.CRUDFieldPolicy{Read: nexaent.ReadExclude, Mutation: nexaent.MutationNone}))
	identity.IsIdentity = true
	entity := task2Entity(t, "Matrix", &crud, identity)
	entity.Identity = IdentityProjection{Kind: "field", Name: "account_id", Type: "int64"}
	if _, err := NewDocument(Projection{Entities: []EntityProjection{entity}}); err == nil {
		t.Fatal("CRUD identity with read exclude was accepted")
	}
}

func TestTask2SensitiveTupleIsAllOrNothingForCRUDAndNonCRUD(t *testing.T) {
	tests := []struct {
		name       string
		sensitive  bool
		visibility nexaent.FieldVisibility
		hint       nexaent.UIHint
		valid      bool
	}{
		{name: "none", visibility: nexaent.VisibilityPublic, hint: nexaent.UIHintText, valid: true},
		{name: "ent only", sensitive: true, visibility: nexaent.VisibilityPublic, hint: nexaent.UIHintText},
		{name: "visibility only", visibility: nexaent.VisibilitySensitive, hint: nexaent.UIHintText},
		{name: "hint only", visibility: nexaent.VisibilityPublic, hint: nexaent.UIHintSensitive},
		{name: "ent and visibility", sensitive: true, visibility: nexaent.VisibilitySensitive, hint: nexaent.UIHintText},
		{name: "ent and hint", sensitive: true, visibility: nexaent.VisibilityPublic, hint: nexaent.UIHintSensitive},
		{name: "visibility and hint", visibility: nexaent.VisibilitySensitive, hint: nexaent.UIHintSensitive},
		{name: "all", sensitive: true, visibility: nexaent.VisibilitySensitive, hint: nexaent.UIHintSensitive, valid: true},
	}
	for _, withCRUD := range []bool{false, true} {
		contextName := "non-crud"
		if withCRUD {
			contextName = "crud"
		}
		for _, test := range tests {
			t.Run(contextName+"/"+test.name, func(t *testing.T) {
				var crud *nexaent.CRUDSpec
				var policy *nexaent.CRUDFieldPolicy
				if withCRUD {
					value := task2CRUD(t, nexaent.CRUDCreate)
					crud = &value
					read := nexaent.ReadInclude
					if test.visibility == nexaent.VisibilitySensitive {
						read = nexaent.ReadExclude
					}
					policy = &nexaent.CRUDFieldPolicy{Read: read, Mutation: nexaent.MutationCreate}
				}
				field := task2Field("secret", task2Meta("sensitive."+test.name, test.visibility, test.hint, policy))
				field.Sensitive = test.sensitive
				_, err := NewDocument(Projection{Entities: []EntityProjection{task2Entity(t, "SecretRecord", crud, field)}})
				if test.valid && err != nil {
					t.Fatalf("valid sensitive tuple rejected: %v", err)
				}
				if !test.valid {
					owner, ok := err.(*Error)
					if !ok || owner.Reason() != "policy_conflict" {
						t.Fatalf("invalid sensitive tuple error = %T %v", err, err)
					}
				}
			})
		}
	}
}

func TestTask2CRUDCreateAllowsRequiredInternalExcludedField(t *testing.T) {
	crud := task2CRUD(t, nexaent.CRUDCreate)
	field := task2Field("required_internal", task2Meta("matrix.required_internal", nexaent.VisibilityInternal, nexaent.UIHintText, &nexaent.CRUDFieldPolicy{Read: nexaent.ReadExclude, Mutation: nexaent.MutationNone}))
	entity := task2Entity(t, "NeutralRecord", &crud, field)
	entity.Meta.Scope = nexaent.ScopeTenant
	if _, err := NewDocument(Projection{Entities: []EntityProjection{entity}}); err != nil {
		t.Fatalf("required internal excluded field blocked CRUDCreate: %v", err)
	}
}

func TestTask2LocalizedKeysRejectOnlyConflictingText(t *testing.T) {
	first := task2Entity(t, "First", nil, task2Field("name", task2Meta("shared", nexaent.VisibilityPublic, nexaent.UIHintText, nil)))
	second := task2Entity(t, "Second", nil, task2Field("name", task2Meta("shared", nexaent.VisibilityPublic, nexaent.UIHintText, nil)))
	if _, err := NewDocument(Projection{Entities: []EntityProjection{first, second}}); err != nil {
		t.Fatalf("identical localized text sharing one key was rejected: %v", err)
	}
	second.Fields[0].Meta.Label.ZhCN = "conflict"
	if _, err := NewDocument(Projection{Entities: []EntityProjection{first, second}}); err == nil {
		t.Fatal("conflicting localized text sharing one key was accepted")
	}
}

func TestTask2EdgeClosureUsesDirectionSpecificBoundFieldOwners(t *testing.T) {
	if _, err := NewDocument(task2BoundEdgeProjection(t)); err != nil {
		t.Fatalf("valid directional bound fields rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Projection)
		reason string
	}{
		{name: "to foreign field", reason: "edge_bound_field_invalid", mutate: func(value *Projection) {
			value.Entities[0].Edges = nil
			value.Entities[1].Edges[0].InverseName = ""
			value.Entities[1].Edges[0].BoundFieldID = "schema:Other/field:foreign_id"
		}},
		{name: "from foreign field", reason: "edge_bound_field_invalid", mutate: func(value *Projection) {
			value.Entities[0].Edges[0].InverseName = ""
			value.Entities[1].Edges = nil
			value.Entities[0].Edges[0].BoundFieldID = "schema:Other/field:foreign_id"
		}},
		{name: "same direction inverse", reason: "edge_inverse_not_closed", mutate: func(value *Projection) {
			value.Entities[0].Edges[0].Direction = "to"
			value.Entities[0].Edges[0].BoundFieldID = ""
		}},
		{name: "inverse bound field mismatch", reason: "edge_inverse_not_closed", mutate: func(value *Projection) {
			value.Entities[0].Edges[0].BoundFieldID = "schema:Member/field:other_id"
		}},
		{name: "edge bound field present inverse missing", reason: "edge_inverse_not_closed", mutate: func(value *Projection) {
			value.Entities[1].Edges[0].BoundFieldID = ""
		}},
		{name: "edge bound field missing inverse present", reason: "edge_inverse_not_closed", mutate: func(value *Projection) {
			value.Entities[0].Edges[0].BoundFieldID = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projection := task2BoundEdgeProjection(t)
			test.mutate(&projection)
			_, err := NewDocument(projection)
			owner, ok := err.(*Error)
			if !ok || owner.Reason() != test.reason {
				t.Fatalf("error = %T %v, want %s", err, err, test.reason)
			}
		})
	}
}

func task2BoundEdgeProjection(t *testing.T) Projection {
	t.Helper()
	account := task2Entity(t, "Account", nil)
	member := task2Entity(t, "Member", nil,
		task2Field("account_id", task2Meta("member.account_id", nexaent.VisibilityPublic, nexaent.UIHintNumber, nil)),
		task2Field("other_id", task2Meta("member.other_id", nexaent.VisibilityPublic, nexaent.UIHintNumber, nil)),
	)
	other := task2Entity(t, "Other", nil, task2Field("foreign_id", task2Meta("other.foreign_id", nexaent.VisibilityPublic, nexaent.UIHintNumber, nil)))
	accountEdgeRef, err := provenance.RepositoryRef(account.SourceRef.Path(), "schema:Account/edge:members")
	if err != nil {
		t.Fatal(err)
	}
	memberEdgeRef, err := provenance.RepositoryRef(member.SourceRef.Path(), "schema:Member/edge:account")
	if err != nil {
		t.Fatal(err)
	}
	account.Edges = []EdgeProjection{{Name: "members", SourceRef: accountEdgeRef, TargetEntityID: "schema:Member", Direction: "from", InverseName: "account", BoundFieldID: "schema:Member/field:account_id"}}
	member.Edges = []EdgeProjection{{Name: "account", SourceRef: memberEdgeRef, TargetEntityID: "schema:Account", Direction: "to", InverseName: "members", BoundFieldID: "schema:Member/field:account_id", Unique: true}}
	return Projection{Entities: []EntityProjection{account, member, other}}
}

func task2Entity(t *testing.T, name string, crud *nexaent.CRUDSpec, fields ...FieldProjection) EntityProjection {
	t.Helper()
	ref, err := provenance.RepositoryRef("ent/schema/"+name+".go", "schema:"+name)
	if err != nil {
		t.Fatal(err)
	}
	for index := range fields {
		fields[index].SourceRef, err = provenance.RepositoryRef(ref.Path(), "schema:"+name+"/field:"+fields[index].Name)
		if err != nil {
			t.Fatal(err)
		}
	}
	return EntityProjection{
		Name: name, SourceRef: ref, Meta: nexaent.SchemaMeta{
			Label: nexaent.LocalizedText{Key: name + ".label", ZhCN: name, EnUS: name}, Description: nexaent.LocalizedText{Key: name + ".description", ZhCN: name + " description", EnUS: name + " description"},
			Identity: nexaent.IdentityEntID, Scope: nexaent.ScopeGlobal,
		},
		CRUD: crud, Identity: IdentityProjection{Kind: "implicit", Name: "id", Type: "int64"}, Fields: fields,
	}
}

func task2Field(name string, meta nexaent.FieldMeta) FieldProjection {
	return FieldProjection{Name: name, Type: "string", Meta: meta}
}

func task2Meta(key string, visibility nexaent.FieldVisibility, hint nexaent.UIHint, crud *nexaent.CRUDFieldPolicy) nexaent.FieldMeta {
	return nexaent.FieldMeta{
		Label: nexaent.LocalizedText{Key: key, ZhCN: key, EnUS: key}, Description: nexaent.LocalizedText{Key: key + ".description", ZhCN: key + " description", EnUS: key + " description"},
		UIHint: hint, Visibility: visibility, CRUD: crud,
	}
}

func task2CRUD(t *testing.T, operations ...nexaent.CRUDOperation) nexaent.CRUDSpec {
	t.Helper()
	encoded, err := nexaent.CRUD(operations...).CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	result, err := nexaent.DecodeCRUD(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

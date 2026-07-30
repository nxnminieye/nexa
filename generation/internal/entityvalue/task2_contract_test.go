package entityvalue

import (
	"testing"

	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

func TestTask2CRUDPolicyPresenceIsBidirectional(t *testing.T) {
	withoutCRUD := task2Entity(t, "Audit", nil, task2Field("name", task2Meta("audit.name", sourcecomment.VisibilityPublic, sourcecomment.UIControlText, &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadInclude, Mutation: sourcecomment.MutationCreate})))
	if _, err := NewDocument(Projection{Entities: []EntityProjection{withoutCRUD}}); err == nil {
		t.Fatal("non-CRUD entity accepted field CRUD policy")
	}

	crud := task2CRUD(t, sourcecomment.CRUDCreate)
	withCRUD := task2Entity(t, "Account", &crud, task2Field("name", task2Meta("account.name", sourcecomment.VisibilityPublic, sourcecomment.UIControlText, nil)))
	if _, err := NewDocument(Projection{Entities: []EntityProjection{withCRUD}}); err == nil {
		t.Fatal("CRUD entity accepted missing field CRUD policy")
	}
}

func TestTenantMarkerOwnsFrameworkInternalCRUDPolicyAbsence(t *testing.T) {
	crud := task2CRUD(t, sourcecomment.CRUDCreate)
	tenant := task2Field("tenant_id", task2Meta("account.tenant_id", sourcecomment.VisibilityInternal, sourcecomment.UIControlReadonly, nil))
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
	authored.Meta.CRUD = &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadExclude, Mutation: sourcecomment.MutationNone}
	if _, err := NewDocument(Projection{Entities: []EntityProjection{task2Entity(t, "Account", &crud, authored)}}); err == nil {
		t.Fatal("tenant marker with authored CRUD policy was accepted")
	}
}

func TestTask2CRUDPolicyMatrix(t *testing.T) {
	crud := task2CRUD(t, sourcecomment.CRUDCreate, sourcecomment.CRUDUpdate)
	tests := []struct {
		name  string
		field FieldProjection
	}{
		{"internal read", task2Field("value", task2Meta("matrix.internal_read", sourcecomment.VisibilityInternal, sourcecomment.UIControlText, &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadInclude, Mutation: sourcecomment.MutationNone}))},
		{"internal mutation", task2Field("value", task2Meta("matrix.internal_mutation", sourcecomment.VisibilityInternal, sourcecomment.UIControlText, &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadExclude, Mutation: sourcecomment.MutationCreate}))},
		{"sensitive flag", task2Field("value", task2Meta("matrix.sensitive_flag", sourcecomment.VisibilitySensitive, sourcecomment.UIControlSensitive, &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadExclude, Mutation: sourcecomment.MutationCreate}))},
		{"sensitive hint", func() FieldProjection {
			f := task2Field("value", task2Meta("matrix.sensitive_hint", sourcecomment.VisibilitySensitive, sourcecomment.UIControlText, &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadExclude, Mutation: sourcecomment.MutationCreate}))
			f.Sensitive = true
			return f
		}()},
		{"readonly mutation", task2Field("value", task2Meta("matrix.readonly", sourcecomment.VisibilityPublic, sourcecomment.UIControlReadonly, &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadInclude, Mutation: sourcecomment.MutationUpdate}))},
		{"immutable update", func() FieldProjection {
			f := task2Field("value", task2Meta("matrix.immutable", sourcecomment.VisibilityPublic, sourcecomment.UIControlText, &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadInclude, Mutation: sourcecomment.MutationCreateUpdate}))
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

	identity := task2Field("account_id", task2Meta("matrix.identity", sourcecomment.VisibilityPublic, sourcecomment.UIControlReadonly, &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadExclude, Mutation: sourcecomment.MutationNone}))
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
		visibility sourcecomment.FieldVisibility
		hint       sourcecomment.UIControl
		valid      bool
	}{
		{name: "none", visibility: sourcecomment.VisibilityPublic, hint: sourcecomment.UIControlText, valid: true},
		{name: "ent only", sensitive: true, visibility: sourcecomment.VisibilityPublic, hint: sourcecomment.UIControlText},
		{name: "visibility only", visibility: sourcecomment.VisibilitySensitive, hint: sourcecomment.UIControlText},
		{name: "hint only", visibility: sourcecomment.VisibilityPublic, hint: sourcecomment.UIControlSensitive},
		{name: "ent and visibility", sensitive: true, visibility: sourcecomment.VisibilitySensitive, hint: sourcecomment.UIControlText},
		{name: "ent and hint", sensitive: true, visibility: sourcecomment.VisibilityPublic, hint: sourcecomment.UIControlSensitive},
		{name: "visibility and hint", visibility: sourcecomment.VisibilitySensitive, hint: sourcecomment.UIControlSensitive},
		{name: "all", sensitive: true, visibility: sourcecomment.VisibilitySensitive, hint: sourcecomment.UIControlSensitive, valid: true},
	}
	for _, withCRUD := range []bool{false, true} {
		contextName := "non-crud"
		if withCRUD {
			contextName = "crud"
		}
		for _, test := range tests {
			t.Run(contextName+"/"+test.name, func(t *testing.T) {
				var crud *sourcecomment.CRUDOperations
				var policy *sourcecomment.CRUDFieldPolicy
				if withCRUD {
					value := task2CRUD(t, sourcecomment.CRUDCreate)
					crud = &value
					read := sourcecomment.ReadInclude
					if test.visibility == sourcecomment.VisibilitySensitive {
						read = sourcecomment.ReadExclude
					}
					policy = &sourcecomment.CRUDFieldPolicy{Read: read, Mutation: sourcecomment.MutationCreate}
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
	crud := task2CRUD(t, sourcecomment.CRUDCreate)
	field := task2Field("required_internal", task2Meta("matrix.required_internal", sourcecomment.VisibilityInternal, sourcecomment.UIControlText, &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadExclude, Mutation: sourcecomment.MutationNone}))
	entity := task2Entity(t, "NeutralRecord", &crud, field)
	entity.Meta.Scope = sourcecomment.ScopeTenant
	if _, err := NewDocument(Projection{Entities: []EntityProjection{entity}}); err != nil {
		t.Fatalf("required internal excluded field blocked CRUDCreate: %v", err)
	}
}

func TestTask2LocalizedKeysRejectOnlyConflictingText(t *testing.T) {
	first := task2Entity(t, "First", nil, task2Field("name", task2Meta("shared", sourcecomment.VisibilityPublic, sourcecomment.UIControlText, nil)))
	second := task2Entity(t, "Second", nil, task2Field("name", task2Meta("shared", sourcecomment.VisibilityPublic, sourcecomment.UIControlText, nil)))
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
		task2Field("account_id", task2Meta("member.account_id", sourcecomment.VisibilityPublic, sourcecomment.UIControlNumber, nil)),
		task2Field("other_id", task2Meta("member.other_id", sourcecomment.VisibilityPublic, sourcecomment.UIControlNumber, nil)),
	)
	other := task2Entity(t, "Other", nil, task2Field("foreign_id", task2Meta("other.foreign_id", sourcecomment.VisibilityPublic, sourcecomment.UIControlNumber, nil)))
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

func task2Entity(t *testing.T, name string, crud *sourcecomment.CRUDOperations, fields ...FieldProjection) EntityProjection {
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
		Name: name, SourceRef: ref, Meta: sourcecomment.SchemaFacts{
			Label: sourcecomment.LocalizedText{Key: name + ".label", ZhCN: name, EnUS: name}, Description: sourcecomment.LocalizedText{Key: name + ".description", ZhCN: name + " description", EnUS: name + " description"},
			Scope: sourcecomment.ScopeGlobal,
		},
		CRUD: crud, Identity: IdentityProjection{Kind: "implicit", Name: "id", Type: "int64"}, Fields: fields,
	}
}

func task2Field(name string, meta sourcecomment.FieldFacts) FieldProjection {
	return FieldProjection{Name: name, Type: "string", Meta: meta}
}

func task2Meta(key string, visibility sourcecomment.FieldVisibility, hint sourcecomment.UIControl, crud *sourcecomment.CRUDFieldPolicy) sourcecomment.FieldFacts {
	return sourcecomment.FieldFacts{
		Label: sourcecomment.LocalizedText{Key: key, ZhCN: key, EnUS: key}, Description: sourcecomment.LocalizedText{Key: key + ".description", ZhCN: key + " description", EnUS: key + " description"},
		Control: hint, Visibility: visibility, CRUD: crud,
	}
}

func task2CRUD(t *testing.T, operations ...sourcecomment.CRUDOperation) sourcecomment.CRUDOperations {
	t.Helper()
	result, err := sourcecomment.NewCRUDOperations(operations...)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

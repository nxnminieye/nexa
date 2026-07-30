package entity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

func TestTypedProjectionCanonicalSnapshotRoundTrip(t *testing.T) {
	accountRef := mustRepositoryRef(t, "ent/schema/account.go", "schema:Account")
	nameRef := mustRepositoryRef(t, "ent/schema/account.go", "schema:Account/field:name")
	auditRef := mustRepositoryRef(t, "ent/schema/audit_entry.go", "schema:AuditEntry")
	crud := mustCRUDSpec(t, sourcecomment.CRUDGet, sourcecomment.CRUDUpdate)
	projection := entityvalue.Projection{Entities: []entityvalue.EntityProjection{
		{
			Name: "AuditEntry", SourceRef: auditRef, Meta: validSchemaFacts("audit_entry"),
			Identity: entityvalue.IdentityProjection{Kind: string(IdentityImplicit), Name: "id", Type: string(ScalarInt64)},
		},
		{
			Name: "Account", SourceRef: accountRef, Meta: validSchemaFacts("account"), CRUD: &crud,
			Identity: entityvalue.IdentityProjection{Kind: string(IdentityImplicit), Name: "id", Type: string(ScalarInt64)},
			Fields: []entityvalue.FieldProjection{{
				Name: "name", SourceRef: nameRef, Type: string(ScalarString), Meta: validFieldFacts("account.name"),
			}},
		},
	}}

	value, err := entityvalue.NewDocument(projection)
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	document, err := AdoptLoadedDocument(value)
	if err != nil {
		t.Fatalf("AdoptLoadedDocument() error = %v", err)
	}
	if !document.FactGraph().Valid() {
		t.Fatal("EntityIR did not retain a typed source-comment graph")
	}
	if scope, ok := document.FactGraph().Fact(sourcecomment.FactID{SemanticID: "Account", Key: "scope"}); !ok {
		t.Fatal("EntityIR source-comment graph lost schema facts")
	} else if value, _ := scope.Value().String(); value != string(sourcecomment.ScopeTenant) {
		t.Fatalf("Account scope fact = %q", value)
	}
	if _, ok := document.FactGraph().Fact(sourcecomment.FactID{SemanticID: "Account.name", Key: "ui.control"}); !ok {
		t.Fatal("EntityIR source-comment graph lost field facts")
	}
	canonical, err := CanonicalJSON(document)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	source := mustDomainSource(t, "quality/entity-ir.json")
	snapshot, err := ParseSnapshot(source, canonical)
	if err != nil {
		t.Fatalf("ParseSnapshot() error = %v\n%s", err, canonical)
	}
	roundTrip, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(roundTrip, canonical) {
		t.Fatalf("snapshot canonical = %s, %v", roundTrip, err)
	}

	entities := document.Entities()
	if len(entities) != 2 || entities[0].ID() != "schema:Account" || entities[1].ID() != "schema:AuditEntry" {
		t.Fatalf("canonical entities = %#v", entities)
	}
	account, ok := document.Entity("schema:Account")
	if !ok {
		t.Fatal("Account missing")
	}
	gotCRUD, ok := account.CRUD()
	if !ok || !equalCRUDOperations(gotCRUD.Operations(), []sourcecomment.CRUDOperation{sourcecomment.CRUDGet, sourcecomment.CRUDUpdate}) {
		t.Fatalf("Account CRUD = %#v, %v", gotCRUD.Operations(), ok)
	}
	audit, ok := document.Entity("schema:AuditEntry")
	if !ok {
		t.Fatal("AuditEntry missing")
	}
	if _, ok := audit.CRUD(); ok {
		t.Fatal("annotation absence became CRUD opt-in")
	}
	readAccount, ok := snapshot.Entity("schema:Account")
	if !ok {
		t.Fatal("snapshot Account missing")
	}
	readCRUD, ok := readAccount.CRUD()
	if !ok || !equalCRUDOperations(readCRUD.Operations(), gotCRUD.Operations()) {
		t.Fatalf("snapshot CRUD = %#v, %v", readCRUD.Operations(), ok)
	}
}

func TestTask2EdgeCanonicalSnapshotRoundTrip(t *testing.T) {
	accountRef := mustRepositoryRef(t, "ent/schema/account.go", "schema:Account")
	accountNameRef := mustRepositoryRef(t, "ent/schema/account.go", "schema:Account/field:name")
	accountMembersRef := mustRepositoryRef(t, "ent/schema/account.go", "schema:Account/edge:members")
	memberRef := mustRepositoryRef(t, "ent/schema/member.go", "schema:Member")
	memberAccountRef := mustRepositoryRef(t, "ent/schema/member.go", "schema:Member/field:account_id")
	memberEdgeRef := mustRepositoryRef(t, "ent/schema/member.go", "schema:Member/edge:account")
	accountNameFacts := validFieldFacts("account.name")
	accountNameFacts.CRUD = nil
	memberAccountFacts := validFieldFacts("member.account")
	memberAccountFacts.CRUD = nil
	memberAccountFacts.Reference = &sourcecomment.ResolvedReference{Target: "Account", Display: "name"}
	projection := entityvalue.Projection{Entities: []entityvalue.EntityProjection{
		{Name: "Account", SourceRef: accountRef, Meta: validSchemaFacts("account"), Identity: entityvalue.IdentityProjection{Kind: string(IdentityImplicit), Name: "id", Type: string(ScalarInt64)}, Fields: []entityvalue.FieldProjection{{Name: "name", SourceRef: accountNameRef, Type: string(ScalarString), Meta: accountNameFacts}}, Edges: []entityvalue.EdgeProjection{{Name: "members", SourceRef: accountMembersRef, TargetEntityID: "schema:Member", Direction: string(EdgeDirectionFrom), InverseName: "account", BoundFieldID: "schema:Member/field:account_id", Optional: true}}},
		{Name: "Member", SourceRef: memberRef, Meta: validSchemaFacts("member"), Identity: entityvalue.IdentityProjection{Kind: string(IdentityImplicit), Name: "id", Type: string(ScalarInt64)}, Fields: []entityvalue.FieldProjection{{Name: "account_id", SourceRef: memberAccountRef, Type: string(ScalarInt64), Meta: memberAccountFacts}}, Edges: []entityvalue.EdgeProjection{{Name: "account", SourceRef: memberEdgeRef, TargetEntityID: "schema:Account", Direction: string(EdgeDirectionTo), InverseName: "members", BoundFieldID: "schema:Member/field:account_id", Unique: true}}},
	}}
	value, err := entityvalue.NewDocument(projection)
	if err != nil {
		t.Fatal(err)
	}
	document, err := AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	member, ok := document.Entity("schema:Member")
	if !ok {
		t.Fatal("Member missing")
	}
	edge, ok := member.Edge("schema:Member/edge:account")
	if !ok || edge.Direction() != EdgeDirectionTo || edge.TargetEntityID() != "schema:Account" || edge.Unique() != true {
		t.Fatalf("edge = %#v, %v", edge, ok)
	}
	if inverse, present := edge.InverseName(); !present || inverse != "members" {
		t.Fatalf("inverse = %q, %v", inverse, present)
	}
	if bound, present := edge.BoundFieldID(); !present || bound != "schema:Member/field:account_id" {
		t.Fatalf("bound field = %q, %v", bound, present)
	}
	canonical, err := CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canonical, []byte(`"edges":[`)) {
		t.Fatalf("canonical edges missing: %s", canonical)
	}
	snapshot, err := ParseSnapshot(mustDomainSource(t, "quality/entity-ir.json"), canonical)
	if err != nil {
		t.Fatalf("ParseSnapshot: %v\n%s", err, canonical)
	}
	readMember, ok := snapshot.Entity("schema:Member")
	if !ok {
		t.Fatal("snapshot Member missing")
	}
	readEdge, ok := readMember.Edge("schema:Member/edge:account")
	if !ok || readEdge.Direction() != EdgeDirectionTo || readEdge.TargetEntityID() != "schema:Account" {
		t.Fatalf("snapshot edge = %#v, %v", readEdge, ok)
	}
	if readEdge.SourceRef() != memberEdgeRef || readEdge.Source().Digest.String() == "" || len(readEdge.CanonicalSourceJSON()) == 0 {
		t.Fatalf("snapshot edge source facts are incomplete: %#v", readEdge)
	}
	if roundTrip, err := snapshot.CanonicalJSON(); err != nil || !bytes.Equal(roundTrip, canonical) {
		t.Fatalf("snapshot round trip = %s, %v", roundTrip, err)
	}

	memberAccountFacts.Reference.Display = "mutated"
	field, _ := member.Field("schema:Member/field:account_id")
	first := field.Meta()
	first.Reference.Display = "changed"
	second := field.Meta()
	if second.Reference == nil || second.Reference.Display != "name" {
		t.Fatalf("Reference aliased: %#v", second)
	}
}

func TestEntityIRDefensiveCopiesAndZeroState(t *testing.T) {
	ref := mustRepositoryRef(t, "ent/schema/account.go", "schema:Account")
	meta := validSchemaFacts("account")
	value, err := entityvalue.NewDocument(entityvalue.Projection{Entities: []entityvalue.EntityProjection{{
		Name: "Account", SourceRef: ref, Meta: meta,
		Identity: entityvalue.IdentityProjection{Kind: string(IdentityImplicit), Name: "id", Type: string(ScalarInt64)},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	document, err := AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}

	entities := document.Entities()
	entities[0] = Entity{}
	if entity, ok := document.Entity("schema:Account"); !ok || entity.Name() != "Account" {
		t.Fatal("Entities() aliased document state")
	}
	first, _ := CanonicalJSON(document)
	first[0] = '!'
	second, _ := CanonicalJSON(document)
	if len(second) == 0 || second[0] == '!' {
		t.Fatal("CanonicalJSON() returned aliased bytes")
	}
	sources := document.Sources()
	if len(sources) != 1 {
		t.Fatalf("Sources() length = %d", len(sources))
	}
	sources[0] = provenance.Source{}
	if source, ok := document.Source(ref); !ok || source.Digest.String() == "" {
		t.Fatal("Sources() aliased source closure")
	}

	if (Document{}).APIVersion() != "" || len((Document{}).Entities()) != 0 {
		t.Fatal("zero Document exposed state")
	}
	if _, err := CanonicalJSON(Document{}); err == nil {
		t.Fatal("zero Document canonicalized")
	}
	if (Snapshot{}).APIVersion() != "" || len((Snapshot{}).Entities()) != 0 {
		t.Fatal("zero Snapshot exposed state")
	}
	if _, err := (Snapshot{}).CanonicalJSON(); err == nil {
		t.Fatal("zero Snapshot canonicalized")
	}
}

func TestEntityIRFieldCRUDPolicyDefensiveCopies(t *testing.T) {
	entityRef := mustRepositoryRef(t, "ent/schema/account.go", "schema:Account")
	fieldRef := mustRepositoryRef(t, "ent/schema/account.go", "schema:Account/field:name")
	policy := &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadInclude, Mutation: sourcecomment.MutationCreateUpdate}
	fieldFacts := validFieldFacts("account.name")
	fieldFacts.CRUD = policy
	crud := mustCRUDSpec(t, sourcecomment.CRUDCreate, sourcecomment.CRUDUpdate)
	value, err := entityvalue.NewDocument(entityvalue.Projection{Entities: []entityvalue.EntityProjection{{
		Name: "Account", SourceRef: entityRef, Meta: validSchemaFacts("account"), CRUD: &crud,
		Identity: entityvalue.IdentityProjection{Kind: string(IdentityImplicit), Name: "id", Type: string(ScalarInt64)},
		Fields:   []entityvalue.FieldProjection{{Name: "name", SourceRef: fieldRef, Type: string(ScalarString), Meta: fieldFacts}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	document, err := AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	policy.Read = sourcecomment.ReadExclude
	field, ok := document.Entities()[0].Field("schema:Account/field:name")
	if !ok || field.Meta().CRUD == nil || field.Meta().CRUD.Read != sourcecomment.ReadInclude {
		t.Fatalf("constructor retained CRUD pointer: %#v", field.Meta().CRUD)
	}
	returned := field.Meta()
	returned.CRUD.Mutation = sourcecomment.MutationNone
	if got := field.Meta().CRUD; got == nil || got.Mutation != sourcecomment.MutationCreateUpdate {
		t.Fatalf("Meta() returned aliased CRUD pointer: %#v", got)
	}
}

func TestEntityIRRejectsInvalidTypedProjection(t *testing.T) {
	ref := mustRepositoryRef(t, "ent/schema/account.go", "schema:Account")
	tests := []struct {
		name       string
		projection entityvalue.Projection
	}{
		{name: "missing name", projection: entityvalue.Projection{Entities: []entityvalue.EntityProjection{{SourceRef: ref, Meta: validSchemaFacts("account")}}}},
		{
			name: "invalid scalar",
			projection: entityvalue.Projection{Entities: []entityvalue.EntityProjection{{
				Name: "Account", SourceRef: ref, Meta: validSchemaFacts("account"),
				Identity: entityvalue.IdentityProjection{Kind: string(IdentityImplicit), Name: "id", Type: "decimal"},
			}}},
		},
		{name: "duplicate", projection: entityvalue.Projection{Entities: []entityvalue.EntityProjection{
			{Name: "Account", SourceRef: ref, Meta: validSchemaFacts("account"), Identity: entityvalue.IdentityProjection{Kind: string(IdentityImplicit), Name: "id", Type: string(ScalarInt64)}},
			{Name: "Account", SourceRef: ref, Meta: validSchemaFacts("account"), Identity: entityvalue.IdentityProjection{Kind: string(IdentityImplicit), Name: "id", Type: string(ScalarInt64)}},
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := entityvalue.NewDocument(test.projection); err == nil {
				t.Fatal("invalid projection accepted")
			}
		})
	}
}

func TestEntityIRRejectsFieldOwnedByDifferentSourceFile(t *testing.T) {
	entityRef := mustRepositoryRef(t, "ent/schema/account.go", "schema:Account")
	fieldRef := mustRepositoryRef(t, "ent/schema/other.go", "schema:Account/field:name")
	_, err := entityvalue.NewDocument(entityvalue.Projection{Entities: []entityvalue.EntityProjection{{
		Name: "Account", SourceRef: entityRef, Meta: validSchemaFacts("account"),
		Identity: entityvalue.IdentityProjection{Kind: string(IdentityImplicit), Name: "id", Type: string(ScalarInt64)},
		Fields: []entityvalue.FieldProjection{{
			Name: "name", SourceRef: fieldRef, Type: string(ScalarString), Meta: validFieldFacts("account.name"),
		}},
	}}})
	owner, ok := err.(*entityvalue.Error)
	if !ok || owner == nil {
		t.Fatalf("NewDocument() error = %T %v, want *entityvalue.Error", err, err)
	}
	if owner.Reason() != "source_ref_invalid" || owner.Pointer() != "/entities/0/fields/0/sourceRef" {
		t.Fatalf("error = %q %q", owner.Reason(), owner.Pointer())
	}
}

func TestEntityIRNodeDigestsAndSourceClosure(t *testing.T) {
	entityRef := mustRepositoryRef(t, "ent/schema/account.go", "schema:Account")
	fieldRef := mustRepositoryRef(t, "ent/schema/account.go", "schema:Account/field:name")
	crud := mustCRUDSpec(t, sourcecomment.CRUDGet)
	value, err := entityvalue.NewDocument(entityvalue.Projection{Entities: []entityvalue.EntityProjection{{
		Name: "Account", SourceRef: entityRef,
		Meta: sourcecomment.SchemaFacts{
			Label:       sourcecomment.LocalizedText{Key: "account.label", ZhCN: "账户", EnUS: "Account"},
			Description: sourcecomment.LocalizedText{Key: "account.description", ZhCN: "账户记录", EnUS: "Account record"},
			Scope:       sourcecomment.ScopeTenant,
		},
		CRUD:     &crud,
		Identity: entityvalue.IdentityProjection{Kind: string(IdentityImplicit), Name: "id", Type: string(ScalarInt64)},
		Fields: []entityvalue.FieldProjection{{
			Name: "name", SourceRef: fieldRef, Type: string(ScalarString),
			Meta: sourcecomment.FieldFacts{
				Label:       sourcecomment.LocalizedText{Key: "account.name.label", ZhCN: "名称", EnUS: "Name"},
				Description: sourcecomment.LocalizedText{Key: "account.name.description", ZhCN: "账户名称", EnUS: "Account name"},
				Control:     sourcecomment.UIControlText, Visibility: sourcecomment.VisibilityPublic,
				CRUD: &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadInclude, Mutation: sourcecomment.MutationCreateUpdate},
			},
		}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	document, err := AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	account, _ := document.Entity("schema:Account")
	if got := account.Source().Digest.String(); got != "sha256:54896a9f7a98c6efdd889dc91e1fe86dd5e71d3cb91b45404719d0cda593f590" {
		t.Fatalf("entity digest = %s", got)
	}
	name, _ := account.Field("schema:Account/field:name")
	if got := name.Source().Digest.String(); got != "sha256:7d853d5d3c712453e8c7f7fa5273e592ae81fab7fae0b9fcbd99e81e43b1d2aa" {
		t.Fatalf("field digest = %s", got)
	}
	entityBytes := account.CanonicalSourceJSON()
	entityBytes[0] = '!'
	if account.CanonicalSourceJSON()[0] == '!' {
		t.Fatal("entity canonical source bytes were aliased")
	}

	sources := document.Sources()
	if len(sources) != 2 || sources[0].Ref.String() >= sources[1].Ref.String() {
		t.Fatalf("source closure = %#v", sources)
	}
	type sourcePair struct {
		Digest string `json:"digest"`
		Ref    string `json:"ref"`
	}
	pairs := make([]sourcePair, len(sources))
	for index, source := range sources {
		pairs[index] = sourcePair{Digest: source.Digest.String(), Ref: source.Ref.String()}
	}
	encoded, err := json.Marshal(struct {
		APIVersion string       `json:"apiVersion"`
		Sources    []sourcePair `json:"sources"`
	}{APIVersion: SourceSetAPIVersion, Sources: pairs})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := document.SourceDigest(), provenance.SHA256(canonical); got != want {
		t.Fatalf("source digest = %s, want %s", got.String(), want.String())
	}
}

func TestEntityIRErrorProjectionRejectsProducerImpossibleTuples(t *testing.T) {
	source := "ent/schema"
	projection, validationErr := ParseEntHelperErrorProjection(
		"entity_ir_invalid", "field_type_unsupported", "/entities/0/fields/0/type", source,
	)
	if validationErr != nil || projection.Code() != "entity_ir_invalid" || projection.Source() != source {
		t.Fatalf("valid projection = %#v, %v", projection, validationErr)
	}

	tests := []struct {
		name, code, reason, pointer, source string
		field                               EntHelperErrorField
	}{
		{name: "snapshot code", code: "entity_snapshot_invalid", reason: "document_invalid", field: EntHelperErrorFieldCode},
		{name: "removed crud reason", code: "entity_ir_invalid", reason: "crud_invalid", pointer: "/entities/0/crud", source: source, field: EntHelperErrorFieldReason},
		{name: "enum tail", code: "entity_ir_invalid", reason: "enum_invalid", pointer: "/entities/0/fields/0/enumValues/impossible/tail", source: source, field: EntHelperErrorFieldPointer},
		{name: "index overflow", code: "entity_ir_invalid", reason: "field_type_unsupported", pointer: "/entities/" + strings.Repeat("9", 100) + "/fields/0/type", source: source, field: EntHelperErrorFieldPointer},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseEntHelperErrorProjection(test.code, test.reason, test.pointer, test.source)
			if err == nil || err.Field() != test.field {
				t.Fatalf("validation error = %#v", err)
			}
		})
	}

	direct := irError("field_type_unsupported", "/entities/0/fields/0/type", source)
	if _, ok := ProjectEntHelperError(direct); !ok {
		t.Fatal("direct owner error did not project")
	}
	if _, ok := ProjectEntHelperError(fmt.Errorf("wrapped: %w", direct)); ok {
		t.Fatal("wrapped owner error projected")
	}
	if _, ok := ProjectEntHelperError((*Error)(nil)); ok {
		t.Fatal("typed nil owner error projected")
	}
}

func TestAdoptLoadedDocumentErrorProjectsOnlyInternalEntityValueErrors(t *testing.T) {
	source, err := provenance.ParseDomainSource("ent/schema")
	if err != nil {
		t.Fatal(err)
	}
	ref := mustRepositoryRef(t, "ent/schema/account.go", "schema:Account")
	_, internalErr := entityvalue.NewDocument(entityvalue.Projection{Entities: []entityvalue.EntityProjection{{SourceRef: ref}}})
	projected := AdoptLoadedDocumentError(internalErr, source)
	owner, ok := projected.(*Error)
	if !ok || owner.Code() != "entity_ir_invalid" || owner.Reason() != "entity_name_invalid" || owner.Pointer() != "/entities/0/name" || owner.Source() != source.String() {
		t.Fatalf("projected error = %T %#v", projected, projected)
	}
	raw := errors.New("raw")
	if got := AdoptLoadedDocumentError(raw, source); got != raw {
		t.Fatalf("raw error changed to %T %v", got, got)
	}
	var typedNil *entityvalue.Error
	if got := AdoptLoadedDocumentError(typedNil, source); got != typedNil {
		t.Fatalf("typed nil changed to %T %v", got, got)
	}
}

func TestEntityIRProducerSourceTuplesRemainTransportClosed(t *testing.T) {
	for _, test := range []struct {
		reason  string
		pointer string
	}{
		{reason: "canonical_invalid", pointer: "/sources"},
		{reason: "source_ref_invalid", pointer: "/executionModuleSources/0/ref"},
		{reason: "source_digest_invalid", pointer: "/executionModuleSources/0/digest"},
		{reason: "source_conflict", pointer: "/executionModuleSources/1/ref"},
		{reason: "source_digest_invalid", pointer: "/entities/0/sourceRef"},
		{reason: "source_digest_invalid", pointer: "/entities/0/fields/0/sourceRef"},
		{reason: "source_digest_invalid", pointer: "/entities/0/edges/0/sourceRef"},
	} {
		t.Run(test.reason+test.pointer, func(t *testing.T) {
			projection, validationErr := ParseEntHelperErrorProjection("entity_ir_invalid", test.reason, test.pointer, "ent/schema")
			if validationErr != nil || projection.Reason() != test.reason || projection.Pointer() != test.pointer {
				t.Fatalf("tuple rejected: %#v, %v", projection, validationErr)
			}
		})
	}
}

func validSchemaFacts(prefix string) sourcecomment.SchemaFacts {
	return sourcecomment.SchemaFacts{
		Label:       sourcecomment.LocalizedText{Key: prefix + ".label", ZhCN: "标签", EnUS: "Label"},
		Description: sourcecomment.LocalizedText{Key: prefix + ".description", ZhCN: "说明", EnUS: "Description"},
		Scope:       sourcecomment.ScopeTenant,
	}
}

func validFieldFacts(prefix string) sourcecomment.FieldFacts {
	return sourcecomment.FieldFacts{
		Label:       sourcecomment.LocalizedText{Key: prefix + ".label", ZhCN: "名称", EnUS: "Name"},
		Description: sourcecomment.LocalizedText{Key: prefix + ".description", ZhCN: "名称说明", EnUS: "Name description"},
		Control:     sourcecomment.UIControlText,
		Visibility:  sourcecomment.VisibilityPublic,
		CRUD:        &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadInclude, Mutation: sourcecomment.MutationCreateUpdate},
	}
}

func mustCRUDSpec(t *testing.T, operations ...sourcecomment.CRUDOperation) sourcecomment.CRUDOperations {
	t.Helper()
	spec, err := sourcecomment.NewCRUDOperations(operations...)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func mustRepositoryRef(t *testing.T, path, fragment string) provenance.SourceRef {
	t.Helper()
	ref, err := provenance.RepositoryRef(path, fragment)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func mustDomainSource(t *testing.T, value string) provenance.DomainSource {
	t.Helper()
	source, err := provenance.ParseDomainSource(value)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func equalCRUDOperations(left, right []sourcecomment.CRUDOperation) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

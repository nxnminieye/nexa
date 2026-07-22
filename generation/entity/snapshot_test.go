package entity_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const emptyEntityIR = `{"apiVersion":"nexa.dev/entity-ir/v2","entities":[],"kind":"EntityIR","sourceDigest":"sha256:ce3235a0208c8c09b12d97f35cd5305bc7b3e5d66371cc774136e90367827f61","sources":[]}`

func TestEntityIREmptySnapshotCanonicalRoundTrip(t *testing.T) {
	source := mustDomainSource(t, "testdata/entity-ir.json")
	snapshot, err := entity.ParseSnapshot(source, []byte(emptyEntityIR))
	if err != nil {
		t.Fatalf("ParseSnapshot() error = %v", err)
	}
	if got := snapshot.APIVersion(); got != entity.APIVersion {
		t.Fatalf("APIVersion() = %q", got)
	}
	if got := snapshot.SourceDigest().String(); got != "sha256:ce3235a0208c8c09b12d97f35cd5305bc7b3e5d66371cc774136e90367827f61" {
		t.Fatalf("SourceDigest() = %q", got)
	}
	if len(snapshot.ProjectedSources()) != 0 || len(snapshot.Entities()) != 0 {
		t.Fatal("empty snapshot exposed projected state")
	}
	if _, ok := snapshot.Entity("schema:Account"); ok {
		t.Fatal("empty snapshot resolved a missing entity")
	}

	first, err := snapshot.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	if string(first) != emptyEntityIR {
		t.Fatalf("CanonicalJSON() = %s", first)
	}
	first[0] = '!'
	second, err := snapshot.CanonicalJSON()
	if err != nil || string(second) != emptyEntityIR {
		t.Fatalf("CanonicalJSON() was aliased: %q, %v", second, err)
	}

	schemaA := entity.Schema()
	schemaB := entity.Schema()
	if len(schemaA) == 0 || !bytes.Equal(schemaA, schemaB) {
		t.Fatal("Schema() did not return stable bytes")
	}
	schemaA[0] ^= 0xff
	if bytes.Equal(schemaA, entity.Schema()) {
		t.Fatal("Schema() returned aliased bytes")
	}
}

func TestTenantMarkerCanonicalRoundTripAndDefensiveReadback(t *testing.T) {
	entityRef, err := provenance.RepositoryRef("ent/schema/account.go", "schema:Account")
	if err != nil {
		t.Fatal(err)
	}
	tenantRef, err := provenance.RepositoryRef("ent/schema/account.go", "schema:Account/field:tenant_id")
	if err != nil {
		t.Fatal(err)
	}
	crud := snapshotCRUD(t, nexaent.CRUDCreate)
	projection := entityvalue.Projection{Entities: []entityvalue.EntityProjection{{
		Name: "Account", SourceRef: entityRef, Meta: nexaent.SchemaMeta{
			Label:       nexaent.LocalizedText{Key: "account.label", ZhCN: "账户", EnUS: "Account"},
			Description: nexaent.LocalizedText{Key: "account.description", ZhCN: "账户记录", EnUS: "Account record"},
			Identity:    nexaent.IdentityEntID, Scope: nexaent.ScopeTenant,
		},
		CRUD: &crud, Identity: entityvalue.IdentityProjection{Kind: "implicit", Name: "id", Type: "int64"},
		Fields: []entityvalue.FieldProjection{{
			Name: "tenant_id", SourceRef: tenantRef, Type: "int64", Immutable: true,
			IsTenantField: true, Meta: nexaent.FieldMeta{
				Label:       nexaent.LocalizedText{Key: "account.tenant_id.label", ZhCN: "租户", EnUS: "Tenant"},
				Description: nexaent.LocalizedText{Key: "account.tenant_id.description", ZhCN: "租户字段", EnUS: "Tenant field"},
				UIHint:      nexaent.UIHintReadonly, Visibility: nexaent.VisibilityInternal,
			},
		}},
	}}}
	value, err := entityvalue.NewDocument(projection)
	if err != nil {
		t.Fatal(err)
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	field, ok := document.Entities()[0].Field("schema:Account/field:tenant_id")
	if !ok || !field.IsTenantField() {
		t.Fatalf("document tenant field = %#v, %v", field, ok)
	}
	canonical, err := entity.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := entity.ParseSnapshot(mustDomainSource(t, "quality/entity-ir.json"), canonical)
	if err != nil {
		t.Fatalf("ParseSnapshot() error = %v\n%s", err, canonical)
	}
	readField, ok := snapshot.Entities()[0].Field("schema:Account/field:tenant_id")
	if !ok || !readField.IsTenantField() {
		t.Fatalf("snapshot tenant field = %#v, %v", readField, ok)
	}
	canonical[0] = 'X'
	roundTrip, err := snapshot.CanonicalJSON()
	if err != nil || len(roundTrip) == 0 || roundTrip[0] == 'X' {
		t.Fatalf("snapshot canonical defensive readback = %q, %v", roundTrip, err)
	}
}

func TestTenantMarkerCRUDPolicyRuleSurvivesSnapshotReadback(t *testing.T) {
	canonical := tenantCRUDCanonical(t)
	source := mustDomainSource(t, "quality/tenant-policy-entity-ir.json")
	if _, err := entity.ParseSnapshot(source, canonical); err != nil {
		t.Fatalf("valid tenant snapshot rejected: %v", err)
	}

	ordinary := mutateTenantSnapshotField(t, canonical, func(field, _ map[string]any) {
		field["isTenantField"] = false
	})
	if _, err := entity.ParseSnapshot(source, ordinary); err == nil {
		t.Fatal("ordinary CRUD field without policy was accepted from snapshot")
	}

	authored := mutateTenantSnapshotField(t, canonical, func(_ map[string]any, payload map[string]any) {
		payload["crud"] = map[string]any{"mutation": "none", "read": "exclude"}
	})
	if _, err := entity.ParseSnapshot(source, authored); err == nil {
		t.Fatal("tenant marker with authored CRUD policy was accepted from snapshot")
	}
}

func tenantCRUDCanonical(t *testing.T) []byte {
	t.Helper()
	entityRef, _ := provenance.RepositoryRef("ent/schema/account.go", "schema:Account")
	tenantRef, _ := provenance.RepositoryRef("ent/schema/account.go", "schema:Account/field:tenant_id")
	crud := snapshotCRUD(t, nexaent.CRUDCreate)
	value, err := entityvalue.NewDocument(entityvalue.Projection{Entities: []entityvalue.EntityProjection{{
		Name: "Account", SourceRef: entityRef, CRUD: &crud,
		Meta:     nexaent.SchemaMeta{Label: nexaent.LocalizedText{Key: "account.label", ZhCN: "Account", EnUS: "Account"}, Description: nexaent.LocalizedText{Key: "account.description", ZhCN: "Account", EnUS: "Account"}, Identity: nexaent.IdentityEntID, Scope: nexaent.ScopeTenant},
		Identity: entityvalue.IdentityProjection{Kind: "implicit", Name: "id", Type: "int64"},
		Fields:   []entityvalue.FieldProjection{{Name: "tenant_id", SourceRef: tenantRef, Type: "int64", Immutable: true, IsTenantField: true, Meta: nexaent.FieldMeta{Label: nexaent.LocalizedText{Key: "account.tenant_id.label", ZhCN: "Tenant", EnUS: "Tenant"}, Description: nexaent.LocalizedText{Key: "account.tenant_id.description", ZhCN: "Tenant", EnUS: "Tenant"}, UIHint: nexaent.UIHintReadonly, Visibility: nexaent.VisibilityInternal}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := entity.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func mutateTenantSnapshotField(t *testing.T, canonical []byte, mutate func(map[string]any, map[string]any)) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(canonical, &root); err != nil {
		t.Fatal(err)
	}
	entityWire := root["entities"].([]any)[0].(map[string]any)
	fieldWire := entityWire["fields"].([]any)[0].(map[string]any)
	payload := fieldWire["fieldMeta"].(map[string]any)["payload"].(map[string]any)
	mutate(fieldWire, payload)
	fieldNode := map[string]any{
		"apiVersion": "nexa.dev/entity-field-node/v2", "entityId": entityWire["id"], "enumValues": fieldWire["enumValues"], "fieldMeta": fieldWire["fieldMeta"],
		"hasDefault": fieldWire["hasDefault"], "id": fieldWire["id"], "immutable": fieldWire["immutable"], "isIdentity": fieldWire["isIdentity"], "isTenantField": fieldWire["isTenantField"], "kind": "Field", "name": fieldWire["name"],
		"nillable": fieldWire["nillable"], "optional": fieldWire["optional"], "sensitive": fieldWire["sensitive"], "type": fieldWire["type"],
	}
	fieldNodeJSON, _ := json.Marshal(fieldNode)
	fieldNodeCanonical, err := jcs.Transform(fieldNodeJSON)
	if err != nil {
		t.Fatal(err)
	}
	fieldRef := fieldWire["sourceRef"].(string)
	sources := root["sources"].([]any)
	for _, item := range sources {
		sourceWire := item.(map[string]any)
		if sourceWire["ref"] == fieldRef {
			sourceWire["digest"] = provenance.SHA256(fieldNodeCanonical).String()
		}
	}
	sourceSetJSON, _ := json.Marshal(map[string]any{"apiVersion": entity.SourceSetAPIVersion, "sources": sources})
	sourceSetCanonical, err := jcs.Transform(sourceSetJSON)
	if err != nil {
		t.Fatal(err)
	}
	root["sourceDigest"] = provenance.SHA256(sourceSetCanonical).String()
	encoded, _ := json.Marshal(root)
	result, err := jcs.Transform(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTask2EntityIRPublicSchemaMatchesReferenceExclusivity(t *testing.T) {
	var schemaDocument any
	if err := json.Unmarshal(entity.Schema(), &schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaID = "https://nexa.dev/schemas/generation/entity/entity-ir-v2.schema.json"
	if err := compiler.AddResource(schemaID, schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(schemaID)
	if err != nil {
		t.Fatal(err)
	}
	canonical := canonicalEntityIRWithUIHint(t, nexaent.UIHintReference)
	for _, test := range []struct {
		name    string
		mutate  func(map[string]any)
		wantErr bool
	}{
		{name: "physical only", mutate: func(payload map[string]any) { payload["physicalDisplay"] = map[string]any{"field": "name"} }},
		{name: "logical only", mutate: func(payload map[string]any) {
			payload["logicalReference"] = map[string]any{"target": "Account", "display": "name"}
		}},
		{name: "both", wantErr: true, mutate: func(payload map[string]any) {
			payload["physicalDisplay"] = map[string]any{"field": "name"}
			payload["logicalReference"] = map[string]any{"target": "Account", "display": "name"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(canonical, &document); err != nil {
				t.Fatal(err)
			}
			payload := document["entities"].([]any)[0].(map[string]any)["fields"].([]any)[0].(map[string]any)["fieldMeta"].(map[string]any)["payload"].(map[string]any)
			test.mutate(payload)
			err := compiled.Validate(document)
			if test.wantErr != (err != nil) {
				t.Fatalf("schema validation error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestGenericUIHintsPassEmbeddedEntityIRSchemaAndCanonicalRoundTrip(t *testing.T) {
	for _, hint := range []nexaent.UIHint{nexaent.UIHintLocale, nexaent.UIHintTimezone} {
		t.Run(string(hint), func(t *testing.T) {
			canonical := canonicalEntityIRWithUIHint(t, hint)
			wireValue := []byte(`"uiHint":"` + string(hint) + `"`)
			if !bytes.Contains(canonical, wireValue) {
				t.Fatalf("canonical EntityIR = %s, want exact %s", canonical, wireValue)
			}

			source := mustDomainSource(t, "review/generic-ui-hint-"+string(hint)+".json")
			snapshot, err := entity.ParseSnapshot(source, canonical)
			if err != nil {
				t.Fatalf("ParseSnapshot() error = %v\n%s", err, canonical)
			}
			account, ok := snapshot.Entity("schema:Account")
			if !ok {
				t.Fatal("snapshot Account missing")
			}
			preference, ok := account.Field("schema:Account/field:preference")
			if !ok {
				t.Fatal("snapshot preference field missing")
			}
			if got := preference.Meta().UIHint; got != hint {
				t.Fatalf("snapshot UIHint = %q, want %q", got, hint)
			}
			roundTrip, err := snapshot.CanonicalJSON()
			if err != nil || !bytes.Equal(roundTrip, canonical) {
				t.Fatalf("snapshot canonical round trip = %s, %v; want byte-identical %s", roundTrip, err, canonical)
			}
		})
	}
}

func TestTask2SnapshotSensitiveTupleMatchesConstructionSemantics(t *testing.T) {
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
				canonical := canonicalEntityIRWithSensitiveTuple(t, withCRUD, test.sensitive, test.visibility, test.hint)
				source := mustDomainSource(t, "review/sensitive-"+contextName+".json")
				_, err := entity.ParseSnapshot(source, canonical)
				if test.valid && err != nil {
					t.Fatalf("valid sensitive tuple rejected: %v\n%s", err, canonical)
				}
				if !test.valid {
					assertEntityError(t, err, "entity_snapshot_invalid", "source_closure_invalid", "/entities/0/fields/0/fieldMeta", source.String())
				}
			})
		}
	}
}

func TestTask2SnapshotEdgeClosureUsesDirectionSpecificOwners(t *testing.T) {
	canonical := canonicalEntityIRWithBoundInverseEdges(t)
	source := mustDomainSource(t, "review/edge-closure.json")
	tests := []struct {
		name        string
		entityIndex int
		mutate      func(map[string]any)
		pointer     string
	}{
		{name: "from foreign field", pointer: "/entities/0/edges/0/boundFieldId", mutate: func(edge map[string]any) {
			edge["boundFieldId"] = "schema:Other/field:foreign_id"
		}},
		{name: "same direction inverse", pointer: "/entities/0/edges/0/inverseName", mutate: func(edge map[string]any) {
			edge["direction"] = "to"
			delete(edge, "boundFieldId")
		}},
		{name: "inverse bound field mismatch", pointer: "/entities/0/edges/0/inverseName", mutate: func(edge map[string]any) {
			edge["boundFieldId"] = "schema:Member/field:other_id"
		}},
		{name: "edge bound field present inverse missing", entityIndex: 1, pointer: "/entities/0/edges/0/inverseName", mutate: func(edge map[string]any) {
			delete(edge, "boundFieldId")
		}},
		{name: "edge bound field missing inverse present", pointer: "/entities/0/edges/0/inverseName", mutate: func(edge map[string]any) {
			delete(edge, "boundFieldId")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := mutateTask2SnapshotEdge(t, canonical, test.entityIndex, test.mutate)
			_, err := entity.ParseSnapshot(source, mutated)
			assertEntityError(t, err, "entity_snapshot_invalid", "source_closure_invalid", test.pointer, source.String())
		})
	}
}

func canonicalEntityIRWithBoundInverseEdges(t *testing.T) []byte {
	t.Helper()
	entityRef := func(name string) provenance.SourceRef {
		ref, err := provenance.RepositoryRef("ent/schema/"+name+".go", "schema:"+name)
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	field := func(owner, name string) entityvalue.FieldProjection {
		ref, err := provenance.RepositoryRef("ent/schema/"+owner+".go", "schema:"+owner+"/field:"+name)
		if err != nil {
			t.Fatal(err)
		}
		return entityvalue.FieldProjection{Name: name, SourceRef: ref, Type: "int64", Meta: nexaent.FieldMeta{
			Label: nexaent.LocalizedText{Key: owner + "." + name, ZhCN: name, EnUS: name}, Description: nexaent.LocalizedText{Key: owner + "." + name + ".description", ZhCN: name + " description", EnUS: name + " description"}, UIHint: nexaent.UIHintNumber, Visibility: nexaent.VisibilityPublic,
		}}
	}
	entityProjection := func(name string, fields []entityvalue.FieldProjection) entityvalue.EntityProjection {
		return entityvalue.EntityProjection{Name: name, SourceRef: entityRef(name), Meta: nexaent.SchemaMeta{
			Label: nexaent.LocalizedText{Key: name + ".label", ZhCN: name, EnUS: name}, Description: nexaent.LocalizedText{Key: name + ".description", ZhCN: name + " description", EnUS: name + " description"}, Identity: nexaent.IdentityEntID, Scope: nexaent.ScopeGlobal,
		}, Identity: entityvalue.IdentityProjection{Kind: "implicit", Name: "id", Type: "int64"}, Fields: fields}
	}
	account := entityProjection("Account", nil)
	member := entityProjection("Member", []entityvalue.FieldProjection{field("Member", "account_id"), field("Member", "other_id")})
	other := entityProjection("Other", []entityvalue.FieldProjection{field("Other", "foreign_id")})
	accountEdgeRef, err := provenance.RepositoryRef(account.SourceRef.Path(), "schema:Account/edge:members")
	if err != nil {
		t.Fatal(err)
	}
	memberEdgeRef, err := provenance.RepositoryRef(member.SourceRef.Path(), "schema:Member/edge:account")
	if err != nil {
		t.Fatal(err)
	}
	account.Edges = []entityvalue.EdgeProjection{{Name: "members", SourceRef: accountEdgeRef, TargetEntityID: "schema:Member", Direction: "from", InverseName: "account", BoundFieldID: "schema:Member/field:account_id"}}
	member.Edges = []entityvalue.EdgeProjection{{Name: "account", SourceRef: memberEdgeRef, TargetEntityID: "schema:Account", Direction: "to", InverseName: "members", BoundFieldID: "schema:Member/field:account_id", Unique: true}}
	value, err := entityvalue.NewDocument(entityvalue.Projection{Entities: []entityvalue.EntityProjection{account, member, other}})
	if err != nil {
		t.Fatal(err)
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := entity.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func mutateTask2SnapshotEdge(t *testing.T, canonical []byte, entityIndex int, mutate func(map[string]any)) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(canonical, &root); err != nil {
		t.Fatal(err)
	}
	entityWire := root["entities"].([]any)[entityIndex].(map[string]any)
	edgeWire := entityWire["edges"].([]any)[0].(map[string]any)
	mutate(edgeWire)
	edgeNode := map[string]any{
		"apiVersion": "nexa.dev/entity-edge-node/v1", "direction": edgeWire["direction"], "entityId": entityWire["id"], "id": edgeWire["id"], "kind": "Edge", "name": edgeWire["name"],
		"optional": edgeWire["optional"], "targetEntityId": edgeWire["targetEntityId"], "unique": edgeWire["unique"],
	}
	for _, member := range []string{"boundFieldId", "inverseName"} {
		if value, ok := edgeWire[member]; ok {
			edgeNode[member] = value
		}
	}
	edgeNodeJSON, err := json.Marshal(edgeNode)
	if err != nil {
		t.Fatal(err)
	}
	edgeNodeCanonical, err := jcs.Transform(edgeNodeJSON)
	if err != nil {
		t.Fatal(err)
	}
	sources := root["sources"].([]any)
	for _, item := range sources {
		sourceWire := item.(map[string]any)
		if sourceWire["ref"] == edgeWire["sourceRef"] {
			sourceWire["digest"] = provenance.SHA256(edgeNodeCanonical).String()
		}
	}
	sourceSetJSON, err := json.Marshal(map[string]any{"apiVersion": entity.SourceSetAPIVersion, "sources": sources})
	if err != nil {
		t.Fatal(err)
	}
	sourceSetCanonical, err := jcs.Transform(sourceSetJSON)
	if err != nil {
		t.Fatal(err)
	}
	root["sourceDigest"] = provenance.SHA256(sourceSetCanonical).String()
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := jcs.Transform(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func canonicalEntityIRWithSensitiveTuple(t *testing.T, withCRUD, sensitive bool, visibility nexaent.FieldVisibility, hint nexaent.UIHint) []byte {
	t.Helper()
	entityRef, err := provenance.RepositoryRef("ent/schema/secret_record.go", "schema:SecretRecord")
	if err != nil {
		t.Fatal(err)
	}
	fieldRef, err := provenance.RepositoryRef("ent/schema/secret_record.go", "schema:SecretRecord/field:secret")
	if err != nil {
		t.Fatal(err)
	}
	meta := nexaent.FieldMeta{
		Label: nexaent.LocalizedText{Key: "secret.label", ZhCN: "secret", EnUS: "Secret"}, Description: nexaent.LocalizedText{Key: "secret.description", ZhCN: "secret description", EnUS: "Secret description"},
		UIHint: nexaent.UIHintText, Visibility: nexaent.VisibilityPublic,
	}
	var crud *nexaent.CRUDSpec
	if withCRUD {
		value := snapshotCRUD(t, nexaent.CRUDCreate)
		crud = &value
		meta.CRUD = &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: nexaent.MutationCreate}
	}
	value, err := entityvalue.NewDocument(entityvalue.Projection{Entities: []entityvalue.EntityProjection{{
		Name: "SecretRecord", SourceRef: entityRef,
		Meta: nexaent.SchemaMeta{Label: nexaent.LocalizedText{Key: "secret_record.label", ZhCN: "record", EnUS: "Record"}, Description: nexaent.LocalizedText{Key: "secret_record.description", ZhCN: "record description", EnUS: "Record description"}, Identity: nexaent.IdentityEntID, Scope: nexaent.ScopeGlobal},
		CRUD: crud, Identity: entityvalue.IdentityProjection{Kind: "implicit", Name: "id", Type: "int64"},
		Fields: []entityvalue.FieldProjection{{Name: "secret", SourceRef: fieldRef, Type: "string", Meta: meta}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := entity.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(canonical, &root); err != nil {
		t.Fatal(err)
	}
	entityWire := root["entities"].([]any)[0].(map[string]any)
	fieldWire := entityWire["fields"].([]any)[0].(map[string]any)
	fieldWire["sensitive"] = sensitive
	payload := fieldWire["fieldMeta"].(map[string]any)["payload"].(map[string]any)
	payload["visibility"] = string(visibility)
	payload["uiHint"] = string(hint)
	if withCRUD {
		read := nexaent.ReadInclude
		if visibility == nexaent.VisibilitySensitive {
			read = nexaent.ReadExclude
		}
		payload["crud"].(map[string]any)["read"] = string(read)
	}
	fieldNode := map[string]any{
		"apiVersion": "nexa.dev/entity-field-node/v2", "entityId": entityWire["id"], "enumValues": fieldWire["enumValues"], "fieldMeta": fieldWire["fieldMeta"],
		"hasDefault": fieldWire["hasDefault"], "id": fieldWire["id"], "immutable": fieldWire["immutable"], "isIdentity": fieldWire["isIdentity"], "isTenantField": fieldWire["isTenantField"], "kind": "Field", "name": fieldWire["name"],
		"nillable": fieldWire["nillable"], "optional": fieldWire["optional"], "sensitive": fieldWire["sensitive"], "type": fieldWire["type"],
	}
	fieldNodeJSON, err := json.Marshal(fieldNode)
	if err != nil {
		t.Fatal(err)
	}
	fieldNodeCanonical, err := jcs.Transform(fieldNodeJSON)
	if err != nil {
		t.Fatal(err)
	}
	sources := root["sources"].([]any)
	for _, item := range sources {
		sourceWire := item.(map[string]any)
		if sourceWire["ref"] == fieldRef.String() {
			sourceWire["digest"] = provenance.SHA256(fieldNodeCanonical).String()
		}
	}
	sourceSetJSON, err := json.Marshal(map[string]any{"apiVersion": entity.SourceSetAPIVersion, "sources": sources})
	if err != nil {
		t.Fatal(err)
	}
	sourceSetCanonical, err := jcs.Transform(sourceSetJSON)
	if err != nil {
		t.Fatal(err)
	}
	root["sourceDigest"] = provenance.SHA256(sourceSetCanonical).String()
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := jcs.Transform(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func snapshotCRUD(t *testing.T, operations ...nexaent.CRUDOperation) nexaent.CRUDSpec {
	t.Helper()
	canonical, err := nexaent.CRUD(operations...).CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	result, err := nexaent.DecodeCRUD(canonical)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func canonicalEntityIRWithUIHint(t *testing.T, hint nexaent.UIHint) []byte {
	t.Helper()
	entityRef, err := provenance.RepositoryRef("ent/schema/account.go", "schema:Account")
	if err != nil {
		t.Fatal(err)
	}
	fieldRef, err := provenance.RepositoryRef("ent/schema/account.go", "schema:Account/field:preference")
	if err != nil {
		t.Fatal(err)
	}
	value, err := entityvalue.NewDocument(entityvalue.Projection{Entities: []entityvalue.EntityProjection{{
		Name:      "Account",
		SourceRef: entityRef,
		Meta: nexaent.SchemaMeta{
			Label:       nexaent.LocalizedText{Key: "account.label", ZhCN: "account", EnUS: "Account"},
			Description: nexaent.LocalizedText{Key: "account.description", ZhCN: "account description", EnUS: "Account description"},
			Identity:    nexaent.IdentityEntID,
			Scope:       nexaent.ScopeTenant,
		},
		Identity: entityvalue.IdentityProjection{Kind: string(entity.IdentityImplicit), Name: "id", Type: string(entity.ScalarInt64)},
		Fields: []entityvalue.FieldProjection{{
			Name:      "preference",
			SourceRef: fieldRef,
			Type:      string(entity.ScalarString),
			Meta: nexaent.FieldMeta{
				Label:       nexaent.LocalizedText{Key: "account.preference.label", ZhCN: "preference", EnUS: "Preference"},
				Description: nexaent.LocalizedText{Key: "account.preference.description", ZhCN: "preference description", EnUS: "Preference description"},
				UIHint:      hint,
				Visibility:  nexaent.VisibilityPublic,
			},
		}},
	}}})
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatalf("AdoptLoadedDocument() error = %v", err)
	}
	canonical, err := entity.CanonicalJSON(document)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	return canonical
}

func TestEntityIRSnapshotRejectsNonCanonicalAndMalformedDocuments(t *testing.T) {
	source := mustDomainSource(t, "review/entity-ir.json")
	tests := []struct {
		name    string
		data    string
		reason  string
		pointer string
	}{
		{name: "unknown", data: `{"apiVersion":"nexa.dev/entity-ir/v2","entities":[],"extra":true,"kind":"EntityIR","sourceDigest":"sha256:ce3235a0208c8c09b12d97f35cd5305bc7b3e5d66371cc774136e90367827f61","sources":[]}`, reason: "document_unknown_field", pointer: "/extra"},
		{name: "duplicate", data: `{"apiVersion":"nexa.dev/entity-ir/v2","entities":[],"entities":[],"kind":"EntityIR","sourceDigest":"sha256:ce3235a0208c8c09b12d97f35cd5305bc7b3e5d66371cc774136e90367827f61","sources":[]}`, reason: "document_duplicate_key", pointer: "/entities"},
		{name: "trailing", data: emptyEntityIR + `{}`, reason: "document_trailing_input"},
		{name: "missing", data: `{"apiVersion":"nexa.dev/entity-ir/v2","entities":[],"kind":"EntityIR","sources":[]}`, reason: "document_required_missing", pointer: "/sourceDigest"},
		{name: "null", data: `{"apiVersion":"nexa.dev/entity-ir/v2","entities":null,"kind":"EntityIR","sourceDigest":"sha256:ce3235a0208c8c09b12d97f35cd5305bc7b3e5d66371cc774136e90367827f61","sources":[]}`, reason: "document_type_invalid", pointer: "/entities"},
		{name: "version", data: `{"apiVersion":"nexa.dev/entity-ir/v1","entities":[],"kind":"EntityIR","sourceDigest":"sha256:ce3235a0208c8c09b12d97f35cd5305bc7b3e5d66371cc774136e90367827f61","sources":[]}`, reason: "version_unsupported", pointer: "/apiVersion"},
		{name: "kind", data: `{"apiVersion":"nexa.dev/entity-ir/v2","entities":[],"kind":"Other","sourceDigest":"sha256:ce3235a0208c8c09b12d97f35cd5305bc7b3e5d66371cc774136e90367827f61","sources":[]}`, reason: "kind_invalid", pointer: "/kind"},
		{name: "order", data: `{"kind":"EntityIR","apiVersion":"nexa.dev/entity-ir/v2","entities":[],"sourceDigest":"sha256:ce3235a0208c8c09b12d97f35cd5305bc7b3e5d66371cc774136e90367827f61","sources":[]}`, reason: "canonical_order_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := entity.ParseSnapshot(source, []byte(test.data))
			assertEntityError(t, err, "entity_snapshot_invalid", test.reason, test.pointer, source.String())
		})
	}
}

func TestEntityIRZeroValuesAreReadOnlyAndInvalid(t *testing.T) {
	if _, err := entity.CanonicalJSON(entity.Document{}); err == nil {
		t.Fatal("zero Document was accepted")
	}
	if _, err := (entity.Snapshot{}).CanonicalJSON(); err == nil {
		t.Fatal("zero Snapshot was accepted")
	}
	if len((entity.Document{}).Entities()) != 0 || len((entity.Snapshot{}).Entities()) != 0 {
		t.Fatal("zero value accessors exposed state")
	}
}

func TestEntityIRSnapshotUnicodeAndDocumentErrorPrecedence(t *testing.T) {
	source := mustDomainSource(t, "review/unicode-entity-ir.json")
	tests := []struct {
		name    string
		data    []byte
		reason  string
		pointer string
	}{
		{name: "isolated surrogate value", data: []byte(`{"apiVersion":"\ud800","entities":[],"kind":"EntityIR","sourceDigest":"sha256:ce3235a0208c8c09b12d97f35cd5305bc7b3e5d66371cc774136e90367827f61","sources":[]}`), reason: "unicode_invalid", pointer: "/apiVersion"},
		{name: "invalid object key", data: []byte(`{"apiVersion":"nexa.dev/entity-ir/v2","entities":[],"kind":"EntityIR","sourceDigest":"sha256:ce3235a0208c8c09b12d97f35cd5305bc7b3e5d66371cc774136e90367827f61","sources":[],"\ud800":true}`), reason: "unicode_invalid", pointer: ""},
		{name: "unicode precedes malformed escape", data: []byte(`{"apiVersion":"\ud800\q","entities":[],"kind":"EntityIR","sourceDigest":"sha256:ce3235a0208c8c09b12d97f35cd5305bc7b3e5d66371cc774136e90367827f61","sources":[]}`), reason: "document_unicode_invalid", pointer: ""},
		{name: "unicode precedes duplicate", data: []byte(`{"apiVersion":"\ud800","apiVersion":"nexa.dev/entity-ir/v2","entities":[],"kind":"EntityIR","sourceDigest":"sha256:ce3235a0208c8c09b12d97f35cd5305bc7b3e5d66371cc774136e90367827f61","sources":[]}`), reason: "document_unicode_invalid", pointer: ""},
		{name: "unicode precedes trailing", data: []byte(`{"apiVersion":"\ud800","entities":[],"kind":"EntityIR","sourceDigest":"sha256:ce3235a0208c8c09b12d97f35cd5305bc7b3e5d66371cc774136e90367827f61","sources":[]} {}`), reason: "document_unicode_invalid", pointer: ""},
	}
	rawInvalid := []byte(emptyEntityIR)
	marker := bytes.Index(rawInvalid, []byte("nexa.dev"))
	rawInvalid[marker] = 0xff
	tests = append(tests, struct {
		name    string
		data    []byte
		reason  string
		pointer string
	}{name: "raw invalid utf8", data: rawInvalid, reason: "unicode_invalid", pointer: "/apiVersion"})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := entity.ParseSnapshot(source, test.data)
			assertEntityError(t, err, "entity_snapshot_invalid", test.reason, test.pointer, source.String())
		})
	}
}

func assertEntityError(t *testing.T, err error, code, reason, pointer, source string) {
	t.Helper()
	owner, ok := err.(*entity.Error)
	if !ok || owner == nil {
		t.Fatalf("error = %T %v, want *entity.Error", err, err)
	}
	if owner.Code() != code || owner.Reason() != reason || owner.Pointer() != pointer || owner.Source() != source {
		t.Fatalf("error tuple = %q/%q/%q/%q", owner.Code(), owner.Reason(), owner.Pointer(), owner.Source())
	}
}

func mustDomainSource(t *testing.T, value string) provenance.DomainSource {
	t.Helper()
	source, err := provenance.ParseDomainSource(value)
	if err != nil {
		t.Fatalf("ParseDomainSource(%q): %v", value, err)
	}
	return source
}

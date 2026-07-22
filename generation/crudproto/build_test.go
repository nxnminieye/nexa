package crudproto_test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/bufbuild/protocompile"
	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestBuildAndRenderExplicitCRUDProtocol(t *testing.T) {
	document := buildEntityDocument(t, accountProjection(t, nexaent.AllCRUDOperations(), accountFields(t)...), auditProjection(t))

	protocol, proposal, err := crudproto.Build(document, crudproto.BuildOptions{
		ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1",
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !proposal.Changed() || proposal.Before() != nil || proposal.After().APIVersion() != crudproto.LockAPIVersion {
		t.Fatalf("initial proposal = changed:%v before:%v after:%q", proposal.Changed(), proposal.Before(), proposal.After().APIVersion())
	}
	if got := len(protocol.Services()); got != 1 {
		t.Fatalf("services = %d, want 1", got)
	}
	if _, ok := protocol.Message("AuditEntry"); ok {
		t.Fatal("annotation absence generated CRUD messages")
	}

	protoBytes, err := crudproto.Render(protocol)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	file := compileProto(t, "identity.crud.generated.proto", protoBytes)
	service := file.Services().ByName("AccountCRUDService")
	if service == nil || service.Methods().Len() != 5 {
		t.Fatalf("AccountCRUDService = %v", service)
	}
	for _, methodName := range []protoreflect.Name{"List", "Get", "Create", "Update", "Delete"} {
		if service.Methods().ByName(methodName) == nil {
			t.Fatalf("method %s missing", methodName)
		}
	}

	item := requireMessage(t, file, "Account")
	assertFieldSet(t, item, "id", "name", "status", "created_at", "payload")
	if item.Fields().ByName("id").Number() != 1 {
		t.Fatal("identity did not receive the first legal wire number")
	}
	if item.Fields().ByName("secret") != nil || item.Fields().ByName("code") != nil {
		t.Fatal("read-excluded field entered item projection")
	}
	if got := item.Fields().ByName("created_at").Message().FullName(); got != "google.protobuf.Timestamp" {
		t.Fatalf("created_at type = %s", got)
	}
	if got := item.Fields().ByName("payload").Message().FullName(); got != "google.protobuf.Value" {
		t.Fatalf("payload type = %s", got)
	}

	create := requireMessage(t, file, "CreateAccountRequest")
	assertFieldSet(t, create, "name", "code", "payload")
	update := requireMessage(t, file, "UpdateAccountRequest")
	assertFieldSet(t, update, "id", "update_mask", "name", "status", "payload")
	if got := update.Fields().ByName("update_mask").Message().FullName(); got != "google.protobuf.FieldMask" {
		t.Fatalf("update_mask type = %s", got)
	}
	for _, name := range []protoreflect.Name{"name", "status", "payload"} {
		if !update.Fields().ByName(name).HasOptionalKeyword() {
			t.Fatalf("update field %s is not proto3 optional", name)
		}
	}

	listRequest := requireMessage(t, file, "ListAccountRequest")
	if listRequest.Fields().ByName("offset").Number() != 1 || listRequest.Fields().ByName("limit").Number() != 2 {
		t.Fatal("list request wire contract changed")
	}
	listResponse := requireMessage(t, file, "ListAccountResponse")
	if !listResponse.Fields().ByName("items").IsList() || listResponse.Fields().ByName("total").Number() != 4 {
		t.Fatal("list response wire contract changed")
	}
	if requireMessage(t, file, "DeleteAccountResponse").Fields().Len() != 0 {
		t.Fatal("delete response must be explicit and empty")
	}
}

func TestTenantCRUDAddsInternalInt64ContextField(t *testing.T) {
	projection := accountProjection(t, nexaent.AllCRUDOperations(), append(accountFields(t), tenantField(t))...)
	document := buildEntityDocument(t, projection)
	protocol, _, err := crudproto.Build(document, crudproto.BuildOptions{ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1", MultiTenant: crudproto.MultiTenantConfig{Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	protoBytes, err := crudproto.Render(protocol)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(protoBytes, []byte(`import "nexa/protocol/v1/options.proto";`)) || !bytes.Contains(protoBytes, []byte(`option (nexa.protocol.v1.rpc_context)`)) {
		t.Fatalf("tenant Proto lost method RPC context:\n%s", protoBytes)
	}
	file := compileProto(t, "identity.crud.generated.proto", protoBytes)
	if field := requireMessage(t, file, "CreateAccountRequest").Fields().ByName("tenant_id"); field == nil || field.Kind() != protoreflect.Int64Kind {
		t.Fatalf("rendered tenant field = %v", field)
	}
	for _, service := range protocol.Services() {
		for _, method := range service.Methods() {
			request, ok := protocol.Message(method.Input())
			if !ok {
				t.Fatalf("request %s missing", method.Input())
			}
			var tenant crudproto.Field
			for _, field := range request.Fields() {
				if field.IsTenantContext() {
					tenant = field
				}
			}
			if tenant.Name() != "tenant_id" || tenant.Type() != "int64" || !tenant.Internal() {
				t.Fatalf("%s tenant field = %#v", method.Name(), tenant)
			}
			bindings := method.RPCContext().ContextFields()
			if len(bindings) != 1 || bindings[0].Source() != crudproto.ContextTenantID || bindings[0].RPCField() != "tenant_id" {
				t.Fatalf("%s context = %#v", method.Name(), bindings)
			}
		}
	}
	if !protocol.HasTenantEntities() || !reflect.DeepEqual(protocol.TenantEntityIDs(), []string{"schema:Account"}) {
		t.Fatalf("public tenant entities = %#v", protocol.TenantEntityIDs())
	}
	ids := protocol.TenantEntityIDs()
	ids[0] = "changed"
	if protocol.TenantEntityIDs()[0] == "changed" {
		t.Fatal("public tenant entity IDs are mutable")
	}
}

func TestTenantFieldNeverAppearsInItemOrExternalMutationFields(t *testing.T) {
	projection := accountProjection(t, nexaent.AllCRUDOperations(), append(accountFields(t), tenantField(t))...)
	protocol, _, err := crudproto.Build(buildEntityDocument(t, projection), crudproto.BuildOptions{ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1", MultiTenant: crudproto.MultiTenantConfig{Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Account", "CreateAccountRequest", "UpdateAccountRequest"} {
		message, ok := protocol.Message(name)
		if !ok {
			t.Fatalf("message %s missing", name)
		}
		for _, field := range message.Fields() {
			if field.Name() == "tenant_id" && !field.Internal() {
				t.Fatalf("tenant field leaked into external %s", name)
			}
		}
	}
}

func TestPlainTenantIDAndScopeDoNotActivateIsolation(t *testing.T) {
	fields := accountFields(t)
	fields = append(fields, entityvalue.FieldProjection{Name: "tenant_id", SourceRef: mustRef(t, "ent/schema/account.go", "schema:Account/field:tenant_id"), Type: string(entity.ScalarInt64), Meta: fieldMeta("account.tenant_id", nexaent.VisibilityInternal, nexaent.ReadExclude, nexaent.MutationNone)})
	protocol, _, err := crudproto.Build(buildEntityDocument(t, accountProjection(t, nexaent.AllCRUDOperations(), fields...)), crudproto.BuildOptions{ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1", MultiTenant: crudproto.MultiTenantConfig{Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range protocol.Services() {
		for _, method := range service.Methods() {
			if len(method.RPCContext().ContextFields()) != 0 {
				t.Fatalf("plain tenant_id activated %s", method.Name())
			}
		}
	}
}

func TestTenantOnlyAndOrdinaryCRUDDoNotAddUnusedOptionsImport(t *testing.T) {
	tenantOnly := accountProjection(t, nil, tenantField(t))
	ordinary := auditProjection(t)
	crud := mustCRUD(t, nexaent.CRUDGet)
	ordinary.CRUD = &crud
	document, _, err := crudproto.Build(buildEntityDocument(t, tenantOnly, ordinary), crudproto.BuildOptions{
		ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1", MultiTenant: crudproto.MultiTenantConfig{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !document.HasTenantEntities() || bytes.Contains(mustRender(t, document), []byte("nexa/protocol/v1/options.proto")) {
		t.Fatalf("mixed imports = %#v tenant=%v", document.Imports(), document.HasTenantEntities())
	}
}

func TestBuildWithoutCRUDDoesNotCreateOrChangeLock(t *testing.T) {
	withoutCRUD := buildEntityDocument(t, accountProjection(t, nil, accountFields(t)...))
	protocol, proposal, err := crudproto.Build(withoutCRUD, crudproto.BuildOptions{
		ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(protocol.Messages()) != 0 || len(protocol.Services()) != 0 || proposal.Changed() || proposal.Before() != nil || proposal.After().APIVersion() != "" {
		t.Fatalf("absence result = messages:%d services:%d changed:%v", len(protocol.Messages()), len(protocol.Services()), proposal.Changed())
	}

	withCRUD := buildEntityDocument(t, accountProjection(t, []nexaent.CRUDOperation{nexaent.CRUDGet}, accountFields(t)...))
	_, initial, err := crudproto.Build(withCRUD, crudproto.BuildOptions{
		ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1",
	})
	if err != nil {
		t.Fatal(err)
	}
	before := initial.After()
	_, unchanged, err := crudproto.Build(withoutCRUD, crudproto.BuildOptions{
		ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1", ExistingLock: &before,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Changed() || unchanged.Before() == nil {
		t.Fatal("annotation absence changed existing lock")
	}
	beforeJSON, _ := crudproto.CanonicalLockJSON(before)
	afterJSON, _ := crudproto.CanonicalLockJSON(unchanged.After())
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("annotation absence rewrote existing lock")
	}
}

func TestBuildUsesExplicitIdentityFieldExactlyOnce(t *testing.T) {
	identityRef := mustRef(t, "ent/schema/external_account.go", "schema:ExternalAccount/field:account_id")
	crud := mustCRUD(t, nexaent.CRUDGet, nexaent.CRUDUpdate, nexaent.CRUDDelete)
	projection := entityvalue.EntityProjection{
		Name: "ExternalAccount", SourceRef: mustRef(t, "ent/schema/external_account.go", "schema:ExternalAccount"), Meta: schemaMeta("external_account"), CRUD: &crud,
		Identity: entityvalue.IdentityProjection{Kind: string(entity.IdentityField), Name: "account_id", Type: string(entity.ScalarString)},
		Fields: []entityvalue.FieldProjection{{
			Name: "account_id", SourceRef: identityRef, Type: string(entity.ScalarString), IsIdentity: true,
			Meta: fieldMeta("external_account.account_id", nexaent.VisibilityPublic, nexaent.ReadInclude, nexaent.MutationNone),
		}},
	}
	protocol, _, err := crudproto.Build(buildEntityDocument(t, projection), crudproto.BuildOptions{
		ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1",
	})
	if err != nil {
		t.Fatal(err)
	}
	file := compileProto(t, "identity.crud.generated.proto", mustRender(t, protocol))
	for _, messageName := range []protoreflect.Name{"ExternalAccount", "GetExternalAccountRequest", "UpdateExternalAccountRequest", "DeleteExternalAccountRequest"} {
		message := requireMessage(t, file, messageName)
		identity := message.Fields().ByName("account_id")
		if identity == nil || identity.Number() != 1 || identity.Kind() != protoreflect.StringKind {
			t.Fatalf("%s identity = %v", messageName, identity)
		}
		count := 0
		for index := 0; index < message.Fields().Len(); index++ {
			if message.Fields().Get(index).Name() == "account_id" {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s identity count = %d", messageName, count)
		}
	}
	item, _ := protocol.Message("ExternalAccount")
	request, _ := protocol.Message("GetExternalAccountRequest")
	if item.Fields()[0].ID() != request.Fields()[0].ID() || item.Fields()[0].Source() != request.Fields()[0].Source() {
		t.Fatal("explicit identity projections diverged")
	}
}

func TestBuildRejectsGlobalProtoSymbolCollision(t *testing.T) {
	getRequestCRUD := mustCRUD(t, nexaent.CRUDDelete)
	getRequestEntity := entityvalue.EntityProjection{
		Name: "GetAccountRequest", SourceRef: mustRef(t, "ent/schema/get_account_request.go", "schema:GetAccountRequest"), Meta: schemaMeta("get_account_request"), CRUD: &getRequestCRUD,
		Identity: entityvalue.IdentityProjection{Kind: string(entity.IdentityImplicit), Name: "id", Type: string(entity.ScalarInt64)},
	}
	_, _, err := crudproto.Build(buildEntityDocument(t,
		accountProjection(t, []nexaent.CRUDOperation{nexaent.CRUDGet}, accountFields(t)...),
		getRequestEntity,
	), crudproto.BuildOptions{ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1"})
	owner, ok := err.(*crudproto.Error)
	if !ok || owner.Code() != "crud_render_invalid" || owner.Stage() != "render" || owner.Reason() != "proto_symbol_duplicate" {
		t.Fatalf("symbol collision error = %#v", err)
	}
}

func TestBuildRejectsPackageEnumValueSymbolCollision(t *testing.T) {
	crud := mustCRUD(t, nexaent.CRUDGet)
	projection := func(entityName, path, fieldName, valueName string) entityvalue.EntityProjection {
		return entityvalue.EntityProjection{
			Name: entityName, SourceRef: mustRef(t, path, "schema:"+entityName), Meta: schemaMeta(strings.ToLower(entityName)), CRUD: &crud,
			Identity: entityvalue.IdentityProjection{Kind: string(entity.IdentityImplicit), Name: "id", Type: string(entity.ScalarInt64)},
			Fields: []entityvalue.FieldProjection{{
				Name: fieldName, SourceRef: mustRef(t, path, "schema:"+entityName+"/field:"+fieldName), Type: string(entity.ScalarEnum),
				EnumValues: []entityvalue.EnumValue{{Name: valueName, Value: valueName}},
				Meta:       fieldMeta(strings.ToLower(entityName)+"."+fieldName, nexaent.VisibilityPublic, nexaent.ReadInclude, nexaent.MutationNone),
			}},
		}
	}
	_, _, err := crudproto.Build(buildEntityDocument(t,
		projection("Foo", "ent/schema/foo.go", "bar", "baz"),
		projection("F", "ent/schema/f.go", "oo", "bar_baz"),
	), crudproto.BuildOptions{ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1"})
	owner, ok := err.(*crudproto.Error)
	if !ok || owner.Code() != "crud_render_invalid" || owner.Stage() != "render" || owner.Reason() != "proto_symbol_duplicate" {
		t.Fatalf("enum value symbol collision error = %#v", err)
	}
}

func compileProto(t *testing.T, name string, source []byte) protoreflect.FileDescriptor {
	t.Helper()
	resolver := &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{name: string(source), "nexa/protocol/v1/options.proto": string(genprotocol.OptionsProto())})}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(resolver)}
	files, err := compiler.Compile(context.Background(), name)
	if err != nil {
		t.Fatalf("compiled generated Proto: %v\n%s", err, source)
	}
	return files[0]
}

func mustRender(t *testing.T, document crudproto.Document) []byte {
	t.Helper()
	value, err := crudproto.Render(document)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func requireMessage(t *testing.T, file protoreflect.FileDescriptor, name protoreflect.Name) protoreflect.MessageDescriptor {
	t.Helper()
	message := file.Messages().ByName(name)
	if message == nil {
		t.Fatalf("message %s missing", name)
	}
	return message
}

func assertFieldSet(t *testing.T, message protoreflect.MessageDescriptor, names ...protoreflect.Name) {
	t.Helper()
	if message.Fields().Len() != len(names) {
		t.Fatalf("%s field count = %d, want %d", message.Name(), message.Fields().Len(), len(names))
	}
	for _, name := range names {
		if message.Fields().ByName(name) == nil {
			t.Fatalf("%s.%s missing", message.Name(), name)
		}
	}
}

func buildEntityDocument(t *testing.T, projections ...entityvalue.EntityProjection) entity.Document {
	t.Helper()
	value, err := entityvalue.NewDocument(entityvalue.Projection{Entities: projections})
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatalf("AdoptLoadedDocument() error = %v", err)
	}
	return document
}

func accountProjection(t *testing.T, operations []nexaent.CRUDOperation, fields ...entityvalue.FieldProjection) entityvalue.EntityProjection {
	t.Helper()
	ref := mustRef(t, "ent/schema/account.go", "schema:Account")
	projection := entityvalue.EntityProjection{
		Name: "Account", SourceRef: ref, Meta: schemaMeta("account"),
		Identity: entityvalue.IdentityProjection{Kind: string(entity.IdentityImplicit), Name: "id", Type: string(entity.ScalarInt64)},
		Fields:   fields,
	}
	if operations != nil {
		crud := mustCRUD(t, operations...)
		projection.CRUD = &crud
	} else {
		for index := range projection.Fields {
			projection.Fields[index].Meta.CRUD = nil
		}
	}
	return projection
}

func auditProjection(t *testing.T) entityvalue.EntityProjection {
	t.Helper()
	return entityvalue.EntityProjection{
		Name: "AuditEntry", SourceRef: mustRef(t, "ent/schema/audit_entry.go", "schema:AuditEntry"), Meta: schemaMeta("audit"),
		Identity: entityvalue.IdentityProjection{Kind: string(entity.IdentityImplicit), Name: "id", Type: string(entity.ScalarInt64)},
	}
}

func accountFields(t *testing.T) []entityvalue.FieldProjection {
	t.Helper()
	field := func(name string, typ entity.ScalarType, read nexaent.ReadPolicy, mutation nexaent.MutationPolicy) entityvalue.FieldProjection {
		visibility := nexaent.VisibilityPublic
		sensitive := false
		if name == "secret" {
			visibility, sensitive = nexaent.VisibilitySensitive, true
		}
		return entityvalue.FieldProjection{
			Name: name, SourceRef: mustRef(t, "ent/schema/account.go", "schema:Account/field:"+name), Type: string(typ), Sensitive: sensitive,
			Meta: fieldMeta("account."+name, visibility, read, mutation),
		}
	}
	fields := []entityvalue.FieldProjection{
		field("name", entity.ScalarString, nexaent.ReadInclude, nexaent.MutationCreateUpdate),
		field("secret", entity.ScalarString, nexaent.ReadExclude, nexaent.MutationNone),
		field("code", entity.ScalarString, nexaent.ReadExclude, nexaent.MutationCreate),
		field("status", entity.ScalarEnum, nexaent.ReadInclude, nexaent.MutationUpdate),
		field("created_at", entity.ScalarTimestamp, nexaent.ReadInclude, nexaent.MutationNone),
		field("payload", entity.ScalarJSON, nexaent.ReadInclude, nexaent.MutationCreateUpdate),
	}
	fields[3].EnumValues = []entityvalue.EnumValue{{Name: "active", Value: "active"}, {Name: "disabled", Value: "disabled"}}
	fields[1].Optional = true
	fields[3].HasDefault = true
	fields[4].HasDefault = true
	fields[5].Nillable = true
	return fields
}

func schemaMeta(prefix string) nexaent.SchemaMeta {
	return nexaent.SchemaMeta{
		Label:       nexaent.LocalizedText{Key: prefix + ".label", ZhCN: "标签", EnUS: "Label"},
		Description: nexaent.LocalizedText{Key: prefix + ".description", ZhCN: "说明", EnUS: "Description"},
		Identity:    nexaent.IdentityEntID, Scope: nexaent.ScopeTenant,
	}
}

func fieldMeta(prefix string, visibility nexaent.FieldVisibility, read nexaent.ReadPolicy, mutation nexaent.MutationPolicy) nexaent.FieldMeta {
	hint := nexaent.UIHintText
	if visibility == nexaent.VisibilitySensitive {
		hint = nexaent.UIHintSensitive
	}
	return nexaent.FieldMeta{
		Label:       nexaent.LocalizedText{Key: prefix + ".label", ZhCN: "字段", EnUS: "Field"},
		Description: nexaent.LocalizedText{Key: prefix + ".description", ZhCN: "字段说明", EnUS: "Field description"},
		UIHint:      hint, Visibility: visibility, CRUD: &nexaent.CRUDFieldPolicy{Read: read, Mutation: mutation},
	}
}

func tenantField(t *testing.T) entityvalue.FieldProjection {
	t.Helper()
	return entityvalue.FieldProjection{
		Name: "tenant_id", SourceRef: mustRef(t, "ent/schema/account.go", "schema:Account/field:tenant_id"), Type: string(entity.ScalarInt64), Immutable: true, IsTenantField: true,
		Meta: nexaent.FieldMeta{Label: nexaent.LocalizedText{Key: "account.tenant_id.label", ZhCN: "Tenant ID", EnUS: "Tenant ID"}, Description: nexaent.LocalizedText{Key: "account.tenant_id.description", ZhCN: "Tenant identifier", EnUS: "Tenant identifier"}, UIHint: nexaent.UIHintReadonly, Visibility: nexaent.VisibilityInternal},
	}
}

func mustCRUD(t *testing.T, operations ...nexaent.CRUDOperation) nexaent.CRUDSpec {
	t.Helper()
	encoded, err := nexaent.CRUD(operations...).CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	value, err := nexaent.DecodeCRUD(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustRef(t *testing.T, path, fragment string) provenance.SourceRef {
	t.Helper()
	ref, err := provenance.RepositoryRef(path, fragment)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

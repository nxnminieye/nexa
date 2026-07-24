package crudbuild

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bufbuild/protocompile"
	"github.com/bufbuild/protocompile/linker"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestBuildFragmentsDerivesCanonicalSchemaKeysAndCompilesSet(t *testing.T) {
	document := fragmentEntityDocument(t, []string{"HTTPRoute", "Account", "AuditEntry", "User_Profile"}, true)
	projection, err := BuildFragments(document, fragmentSpec(nil))
	if err != nil {
		t.Fatalf("BuildFragments: %v", err)
	}
	fragments := projection.Fragments()
	wantKeys := []string{"account", "audit-entry", "http-route", "user-profile"}
	if len(fragments) != len(wantKeys) {
		t.Fatalf("fragments = %d, want %d", len(fragments), len(wantKeys))
	}
	for index, fragment := range fragments {
		var schemaID SchemaID = fragment.SchemaID()
		var schemaKey SchemaKey = fragment.SchemaKey()
		if schemaID == "" {
			t.Fatalf("fragment %d has empty schema ID", index)
		}
		if schemaKey != SchemaKey(wantKeys[index]) {
			t.Fatalf("fragment %d key = %q, want %q", index, fragment.SchemaKey(), wantKeys[index])
		}
	}
}

func TestSchemaKeyCanonicalBoundaries(t *testing.T) {
	tests := map[string]string{
		"Account":      "account",
		"AuditEntry":   "audit-entry",
		"HTTPRoute":    "http-route",
		"Audit_Entry":  "audit-entry",
		"Account2Role": "account2-role",
	}
	for input, want := range tests {
		got, err := schemaKey(input)
		if err != nil || got != SchemaKey(want) {
			t.Errorf("schemaKey(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	maximum := strings.Repeat("a", MaxSchemaKeyBytes)
	key, err := schemaKey(maximum)
	if err != nil || len(key) != MaxSchemaKeyBytes || len("ent."+string(key)+".generated.proto") != 255 {
		t.Fatalf("maximum key = len %d, err %v", len(key), err)
	}
	_, err = schemaKey(maximum + "a")
	typed, ok := err.(*Error)
	if !ok || typed.Code() != "crud_build_invalid" || typed.Stage() != "build" || typed.Reason() != "schema_key_too_long" || typed.Pointer() != "/entities" {
		t.Fatalf("oversized key error = %T (%q,%q,%q,%q)", err, typed.Code(), typed.Stage(), typed.Reason(), typed.Pointer())
	}
}

func TestBuildFragmentsSchemaKeyBasenameBoundaryBeforeReconcile(t *testing.T) {
	maximum := strings.Repeat("A", MaxSchemaKeyBytes)
	accepted, err := BuildFragments(fragmentEntityDocument(t, []string{maximum}, true), fragmentSpec(nil))
	if err != nil {
		t.Fatalf("255-byte basename rejected: %v", err)
	}
	fragments := accepted.Fragments()
	if len(fragments) != 1 || len("ent."+string(fragments[0].SchemaKey())+".generated.proto") != 255 || !accepted.LockProposal().After().Valid() {
		t.Fatal("maximum schema key did not produce one fragment and global lock")
	}

	rejected, err := BuildFragments(fragmentEntityDocument(t, []string{maximum + "A"}, true), fragmentSpec(nil))
	typed, ok := err.(*Error)
	if !ok || typed.Code() != "crud_build_invalid" || typed.Stage() != "build" || typed.Reason() != "schema_key_too_long" || typed.Pointer() != "/entities/0/name" {
		t.Fatalf("256-byte basename error = %T (%q,%q,%q,%q)", err, typed.Code(), typed.Stage(), typed.Reason(), typed.Pointer())
	}
	if rejected.Valid() || rejected.LockProposal().Valid() || len(rejected.Fragments()) != 0 {
		t.Fatal("oversized key returned projection or reconciled lock")
	}
	domain, projected := ProjectEntHelperError(err)
	if !projected || domain.Code() != "crud_build_invalid" || domain.Reason() != "schema_key_too_long" || domain.Pointer() != "/entities/0/name" {
		t.Fatalf("oversized key domain projection = %#v, %v", domain, projected)
	}
}

func TestBuildFragmentsRejectsDistinctSchemaKeyCollisionWithoutOutput(t *testing.T) {
	document := fragmentEntityDocument(t, []string{"AuditEntry", "Audit_Entry"}, true)
	projection, err := BuildFragments(document, fragmentSpec(nil))
	if err == nil {
		t.Fatal("BuildFragments accepted colliding schema keys")
	}
	typed, ok := err.(*Error)
	if !ok || typed.Code() != "crud_build_invalid" || typed.Stage() != "build" || typed.Reason() != "schema_key_collision" || typed.Pointer() != "/entities/1/name" {
		t.Fatalf("error = %T (%q,%q,%q,%q), want *Error (%q,%q,%q,%q)", err, typed.Code(), typed.Stage(), typed.Reason(), typed.Pointer(), "crud_build_invalid", "build", "schema_key_collision", "/entities/1/name")
	}
	if projection.Valid() || len(projection.Fragments()) != 0 || len(projection.EntitySnapshot()) != 0 || projection.LockProposal().Valid() {
		t.Fatal("collision returned partial projection")
	}
}

func TestCompileFragmentSetProjectsFragmentCompileError(t *testing.T) {
	err := compileFragmentSet([]*protoFragmentState{{protoBytes: []byte("not valid proto")}})
	typed, ok := err.(*Error)
	if !ok || typed.Code() != "crud_proto_compile_failed" || typed.Stage() != "compile" || typed.Reason() != "proto_compile_failed" || typed.Pointer() != "/fragments" {
		t.Fatalf("error = %T (%q,%q,%q,%q), want *Error (%q,%q,%q,%q)", err, typed.Code(), typed.Stage(), typed.Reason(), typed.Pointer(), "crud_proto_compile_failed", "compile", "proto_compile_failed", "/fragments")
	}
	domain, projected := ProjectEntHelperError(err)
	if !projected || domain.Code() != "crud_proto_compile_failed" || domain.Reason() != "proto_compile_failed" || domain.Pointer() != "/fragments" {
		t.Fatalf("compile domain projection = %#v, %v", domain, projected)
	}
}

func TestBuildFragmentsCompleteProtoSetAndSnapshotsAreImmutable(t *testing.T) {
	document := fragmentEntityDocument(t, []string{"HTTPRoute", "Account"}, true)
	projection, err := BuildFragments(document, fragmentSpec(nil))
	if err != nil {
		t.Fatalf("BuildFragments: %v", err)
	}
	fragments := projection.Fragments()
	compiled := compileTestFragmentSet(t, fragments)
	for index, fragment := range fragments {
		content := fragment.ProtoBytes()
		if first := strings.SplitN(string(content), "\n", 2)[0]; first != GeneratedProtoMarker {
			t.Fatalf("fragment %d first line = %q", index, first)
		}
		assertFragmentDescriptor(t, protodesc.ToFileDescriptorProto(compiled[index]), fragment.SchemaID())
	}

	entitySnapshot := projection.EntitySnapshot()
	crudSnapshot := projection.CRUDSnapshot()
	protoBytes := fragments[0].ProtoBytes()
	sourceRefs := fragments[0].SourceRefs()
	fragments[0] = ProtoFragment{}
	entitySnapshot[0], crudSnapshot[0], protoBytes[0] = 'x', 'x', 'x'
	if len(sourceRefs) > 0 {
		sourceRefs[0] = provenance.SourceRef{}
	}
	if !projection.Valid() || !projection.Fragments()[0].Valid() || projection.EntitySnapshot()[0] == 'x' || projection.CRUDSnapshot()[0] == 'x' || projection.Fragments()[0].ProtoBytes()[0] == 'x' {
		t.Fatal("projection exposed mutable snapshot or fragment state")
	}
	if len(projection.Fragments()[0].SourceRefs()) > 0 && projection.Fragments()[0].SourceRefs()[0].String() == "" {
		t.Fatal("fragment exposed mutable source refs")
	}
}

func compileTestFragmentSet(t *testing.T, fragments []ProtoFragment) []linker.File {
	t.Helper()
	files := map[string]string{"nexa/protocol/v1/options.proto": string(genprotocol.OptionsProto())}
	names := make([]string, len(fragments))
	for index, fragment := range fragments {
		name := fmt.Sprintf("__fragment_test_%03d.proto", index)
		names[index] = name
		files[name] = string(fragment.ProtoBytes())
	}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(files)})}
	compiled, err := compiler.Compile(context.Background(), names...)
	if err != nil || len(compiled) != len(names) {
		t.Fatalf("compile fragments = %d, %v", len(compiled), err)
	}
	result := make([]linker.File, len(compiled))
	for index := range compiled {
		result[index] = compiled[index]
	}
	return result
}

func assertFragmentDescriptor(t *testing.T, file *descriptorpb.FileDescriptorProto, schemaID SchemaID) {
	t.Helper()
	schemaName := strings.TrimPrefix(string(schemaID), "schema:")
	if file.GetPackage() != "acme.accounts.v1" {
		t.Fatalf("%s package = %q", schemaID, file.GetPackage())
	}
	if file.GetOptions().GetGoPackage() != "example.com/acme/accounts/v1;accountsv1" {
		t.Fatalf("%s go_package = %q", schemaID, file.GetOptions().GetGoPackage())
	}
	if !reflect.DeepEqual(file.GetDependency(), []string{"google/protobuf/timestamp.proto"}) {
		t.Fatalf("%s dependencies = %#v", schemaID, file.GetDependency())
	}
	messages := make([]string, len(file.GetMessageType()))
	for index, value := range file.GetMessageType() {
		messages[index] = value.GetName()
	}
	sort.Strings(messages)
	wantMessages := expectedFragmentMessages(schemaName)
	if !reflect.DeepEqual(messages, wantMessages) {
		t.Fatalf("%s messages = %#v, want %#v", schemaID, messages, wantMessages)
	}
	enums := make([]string, len(file.GetEnumType()))
	for index, value := range file.GetEnumType() {
		enums[index] = value.GetName()
	}
	if !reflect.DeepEqual(enums, []string{schemaName + "Status"}) {
		t.Fatalf("%s enums = %#v", schemaID, enums)
	}
	services := make([]string, len(file.GetService()))
	for index, value := range file.GetService() {
		services[index] = value.GetName()
	}
	if !reflect.DeepEqual(services, []string{schemaName + "CRUDService"}) {
		t.Fatalf("%s services = %#v", schemaID, services)
	}
}

func expectedFragmentMessages(schemaName string) []string {
	result := []string{schemaName}
	for _, operation := range []string{"Create", "Get", "List"} {
		result = append(result, operation+schemaName+"Request", operation+schemaName+"Response")
	}
	sort.Strings(result)
	return result
}

func TestBuildFragmentsGlobalLockBranches(t *testing.T) {
	noCRUD := fragmentEntityDocument(t, []string{"Account"}, false)
	absent, err := BuildFragments(noCRUD, fragmentSpec(nil))
	if err != nil {
		t.Fatal(err)
	}
	if absent.LockProposal().After().Valid() || absent.LockProposal().Before() != nil || absent.LockProposal().Changed() {
		t.Fatal("no CRUD without existing lock proposed a lock write")
	}

	withCRUD := fragmentEntityDocument(t, []string{"Account", "AuditEntry"}, true)
	created, err := BuildFragments(withCRUD, fragmentSpec(nil))
	if err != nil {
		t.Fatal(err)
	}
	createdLock := created.LockProposal().After()
	if !createdLock.Valid() || !created.LockProposal().Changed() || len(createdLock.Schemas()) != 2 {
		t.Fatalf("CRUD set did not produce one global lock: valid=%v changed=%v schemas=%d", createdLock.Valid(), created.LockProposal().Changed(), len(createdLock.Schemas()))
	}

	preserved, err := BuildFragments(noCRUD, fragmentSpec(&createdLock))
	if err != nil {
		t.Fatal(err)
	}
	if preserved.LockProposal().Changed() || preserved.LockProposal().Before() == nil || !bytes.Equal(preserved.LockProposal().After().CanonicalJSON(), createdLock.CanonicalJSON()) {
		t.Fatal("no CRUD with existing lock did not preserve original bytes")
	}
}

func TestBuildFragmentsPreservesMultiTenantClosure(t *testing.T) {
	projection := planEntityProjection(t, true, true)
	projection.Entities[0].Fields = append(projection.Entities[0].Fields, planTenantField(t))
	value, err := entityvalue.NewDocument(projection)
	if err != nil {
		t.Fatal(err)
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	spec := fragmentSpec(nil)
	spec.MultiTenant.Enabled = true
	fragments, err := BuildFragments(document, spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(fragments.Fragments()) != 1 {
		t.Fatalf("fragments = %d, want 1", len(fragments.Fragments()))
	}
	snapshot, err := ParseSnapshot(testPlanSource(t), fragments.CRUDSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.HasTenantEntities() {
		t.Fatal("fragment projection lost multi-tenant CRUD closure")
	}
}

func TestBuildFragmentsRetainsAggregateBuildPlanBehavior(t *testing.T) {
	document := planEntityDocument(t, true)
	plan, err := BuildPlan(document, Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("aggregate-regression")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Valid() || bytes.HasPrefix(plan.ProtoBytes(), []byte(GeneratedProtoMarker)) {
		t.Fatal("additive fragment projection changed prerelease aggregate output")
	}
	aggregate, _, err := Build(document, Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := Render(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plan.ProtoBytes(), want) {
		t.Fatal("fragment lane changed aggregate BuildPlan bytes")
	}
}

func fragmentSpec(lock *Lock) FragmentSpec {
	return FragmentSpec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ExistingLock: lock,
	}
}

func fragmentEntityDocument(t *testing.T, names []string, withCRUD bool) entity.Document {
	t.Helper()
	projection := entityvalue.Projection{Entities: make([]entityvalue.EntityProjection, 0, len(names))}
	for _, name := range names {
		path := "fixtures/generation/ent-consumer/schema/" + strings.ToLower(strings.ReplaceAll(name, "_", "-")) + ".go"
		schemaRef := mustPlanRepositoryRef(t, path, "schema:"+name)
		fieldRef := mustPlanRepositoryRef(t, path, "schema:"+name+"/field:name")
		var fieldPolicy *nexaent.CRUDFieldPolicy
		if withCRUD {
			fieldPolicy = &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: nexaent.MutationCreateUpdate}
		}
		item := entityvalue.EntityProjection{
			Name: name, SourceRef: schemaRef,
			Meta:     nexaent.SchemaMeta{Label: localized(strings.ToLower(name), name), Description: localized(strings.ToLower(name)+".desc", name), Identity: nexaent.IdentityEntID, Scope: nexaent.ScopeGlobal},
			Identity: entityvalue.IdentityProjection{Kind: string(entity.IdentityImplicit), Name: "id", Type: string(entity.ScalarInt64)},
			Fields: []entityvalue.FieldProjection{
				{Name: "name", SourceRef: fieldRef, Type: string(entity.ScalarString), Meta: nexaent.FieldMeta{Label: localized(strings.ToLower(name)+".name", "Name"), Description: localized(strings.ToLower(name)+".name.desc", "Name"), UIHint: nexaent.UIHintText, Visibility: nexaent.VisibilityPublic, CRUD: fieldPolicy}},
				{Name: "status", SourceRef: mustPlanRepositoryRef(t, path, "schema:"+name+"/field:status"), Type: string(entity.ScalarEnum), EnumValues: []entityvalue.EnumValue{{Name: "active", Value: "active"}, {Name: "disabled", Value: "disabled"}}, Meta: nexaent.FieldMeta{Label: localized(strings.ToLower(name)+".status", "Status"), Description: localized(strings.ToLower(name)+".status.desc", "Status"), UIHint: nexaent.UIHintSelect, Visibility: nexaent.VisibilityPublic, CRUD: fieldPolicy}},
				{Name: "created_at", SourceRef: mustPlanRepositoryRef(t, path, "schema:"+name+"/field:created_at"), Type: string(entity.ScalarTimestamp), HasDefault: true, Meta: nexaent.FieldMeta{Label: localized(strings.ToLower(name)+".created_at", "Created at"), Description: localized(strings.ToLower(name)+".created_at.desc", "Created at"), UIHint: nexaent.UIHintReadonly, Visibility: nexaent.VisibilityPublic, CRUD: readOnlyFieldPolicy(withCRUD)}},
			},
		}
		if withCRUD {
			raw, err := nexaent.CRUD(nexaent.CRUDList, nexaent.CRUDGet, nexaent.CRUDCreate).CanonicalJSON()
			if err != nil {
				t.Fatal(err)
			}
			crud, err := nexaent.DecodeCRUD(raw)
			if err != nil {
				t.Fatal(err)
			}
			item.CRUD = &crud
		}
		projection.Entities = append(projection.Entities, item)
	}
	value, err := entityvalue.NewDocument(projection)
	if err != nil {
		t.Fatal(err)
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func readOnlyFieldPolicy(enabled bool) *nexaent.CRUDFieldPolicy {
	if !enabled {
		return nil
	}
	return &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: nexaent.MutationNone}
}

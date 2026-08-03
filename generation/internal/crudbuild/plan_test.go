package crudbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/bufbuild/protocompile"
	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

type sourceProjectionResolver map[string]string

func (r sourceProjectionResolver) Open(_ context.Context, path string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(r[path])), nil
}

func TestBuildPlanSnapshotsAndCompilesRenderedProto(t *testing.T) {
	document := planEntityDocument(t, true)
	requestDigest := provenance.SHA256([]byte("request"))
	plan, err := BuildPlan(document, Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: requestDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.RequestDigest(); got != requestDigest {
		t.Fatalf("request digest = %s", got.String())
	}
	entityJSON, crudJSON, protoBytes := plan.EntitySnapshot(), plan.CRUDSnapshot(), plan.ProtoBytes()
	if len(entityJSON) == 0 || len(crudJSON) == 0 || len(protoBytes) == 0 {
		t.Fatalf("plan snapshots = entity:%d crud:%d proto:%d", len(entityJSON), len(crudJSON), len(protoBytes))
	}
	entityJSON[0], crudJSON[0], protoBytes[0] = 0, 0, 0
	if plan.EntitySnapshot()[0] == 0 || plan.CRUDSnapshot()[0] == 0 || plan.ProtoBytes()[0] == 0 {
		t.Fatal("plan exposed mutable bytes")
	}
	compilePlanProto(t, plan.ProtoPath(), plan.ProtoBytes())
	if plan.ProtoDigest() != provenance.SHA256(plan.ProtoBytes()) || plan.PlanDigest().String() == "" || plan.SourceDigest().String() == "" {
		t.Fatal("plan digests are incomplete")
	}
	if !plan.LockProposal().Valid() || !plan.LockProposal().Changed() {
		t.Fatal("initial CRUD plan did not propose a lock")
	}
	second, err := BuildPlan(document, Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: requestDigest,
	})
	if err != nil || second.PlanDigest() != plan.PlanDigest() {
		t.Fatalf("deterministic plan = %s, %v", second.PlanDigest().String(), err)
	}
}

func TestRenderedCRUDProtoExtendsEntGraphAndRejectsInheritedMutation(t *testing.T) {
	entities := planEntityDocument(t, true)
	plan, err := BuildPlan(entities, Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("source-comment")),
	})
	if err != nil {
		t.Fatal(err)
	}
	generated := string(plan.ProtoBytes())
	if !strings.HasPrefix(generated, "// @nexa $contract: ") || !strings.Contains(generated, " @nexa $source: ") {
		t.Fatalf("generated Proto is missing source comments:\n%s", generated)
	}
	baselineText := stripGeneratedSources(generated)
	baseline, err := protocol.Compile(context.Background(), protocol.CompileOptions{
		ServiceID: "accounts", EntryFiles: []string{plan.ProtoPath()}, Resolver: sourceProjectionResolver{plan.ProtoPath(): baselineText},
	})
	if err != nil {
		t.Fatalf("compile generated baseline: %v", err)
	}
	document, _, err := Build(entities, Spec{ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1"})
	if err != nil {
		t.Fatal(err)
	}
	projection, err := ProtocolProjection(document, plan.ProtoPath(), baseline, entities.FactGraph())
	if err != nil {
		t.Fatal(err)
	}
	if projection.Lock == nil {
		t.Fatal("generated CRUD Proto projection has no source projection lock")
	}
	lock, err := sourcecomment.NewProjectionLock(projection.Nodes, projection.InheritedFacts)
	if err != nil {
		t.Fatal(err)
	}
	projection.Lock = &lock
	compiled, err := protocol.Compile(context.Background(), protocol.CompileOptions{
		ServiceID: "accounts", EntryFiles: []string{plan.ProtoPath()}, Resolver: sourceProjectionResolver{plan.ProtoPath(): generated}, SourceProjection: &projection,
	})
	if err != nil {
		t.Fatalf("compile source projection: %v", err)
	}
	if !compiled.FactGraph().Valid() {
		t.Fatal("projected Proto has no FactGraph")
	}

	localAddition := strings.Replace(generated, "string name = 2;", "string name = 2;\n  string local_note = 99;", 1)
	if _, err := protocol.Compile(context.Background(), protocol.CompileOptions{
		ServiceID: "accounts", EntryFiles: []string{plan.ProtoPath()}, Resolver: sourceProjectionResolver{plan.ProtoPath(): localAddition}, SourceProjection: &projection,
	}); err != nil {
		t.Fatalf("local Proto addition was rejected: %v", err)
	}

	drifted := strings.Replace(generated, "string name = 2;", "int64 name = 2;", 1)
	_, err = protocol.Compile(context.Background(), protocol.CompileOptions{
		ServiceID: "accounts", EntryFiles: []string{plan.ProtoPath()}, Resolver: sourceProjectionResolver{plan.ProtoPath(): drifted}, SourceProjection: &projection,
	})
	owner, ok := err.(*protocol.Error)
	if !ok || owner.Reason() != string(sourcecomment.CodeInheritedNodeChanged) {
		t.Fatalf("inherited Proto mutation error = %#v", err)
	}
}

func stripGeneratedSources(value string) string {
	lines := strings.Split(value, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(line, " @nexa $source: ") {
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

func TestBuildPlanKeepsCompatibilityInputsOutOfProtoSourceRefs(t *testing.T) {
	document := planEntityDocument(t, true)
	initial, err := BuildPlan(document, Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("request-initial")),
	})
	if err != nil {
		t.Fatal(err)
	}
	lock := initial.LockProposal().After()
	lockSource := provenance.Source{Ref: mustPlanRepositoryRef(t, "api/accounts.crud-protocol.lock.json", ""), Digest: provenance.SHA256(lock.CanonicalJSON())}
	manifestSource := provenance.Source{Ref: mustPlanRepositoryRef(t, ".nexa/generation/crud-proto.accounts.manifest.json", ""), Digest: provenance.SHA256([]byte("manifest"))}

	plan, err := BuildPlan(document, Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("request-with-baseline")),
		ExistingLock: &lock, ExistingLockSource: &lockSource,
		PublishedArtifact: &PublishedArtifact{ID: "crud-proto.accounts", Digest: initial.ProtoDigest(), ManifestSource: manifestSource},
	})
	if err != nil {
		t.Fatal(err)
	}
	allRefs := sourceRefStrings(plan.Sources())
	if !containsString(allRefs, lockSource.Ref.String()) || !containsString(allRefs, manifestSource.Ref.String()) {
		t.Fatalf("plan sources lost compatibility inputs: %#v", allRefs)
	}
	protoRefs := refStrings(plan.ProtoSourceRefs())
	if containsString(protoRefs, lockSource.Ref.String()) || containsString(protoRefs, manifestSource.Ref.String()) {
		t.Fatalf("Proto source refs contain control-only inputs: %#v", protoRefs)
	}
	if len(protoRefs) == 0 {
		t.Fatal("Proto source refs lost artifact authoring inputs")
	}
	lockRefs := refStrings(plan.LockSourceRefs())
	if !containsString(lockRefs, lockSource.Ref.String()) || !containsString(lockRefs, manifestSource.Ref.String()) {
		t.Fatalf("lock source refs lost compatibility inputs: %#v", lockRefs)
	}
}

func TestBuildPlanWithoutCRUDProducesEmptyProtocolSnapshot(t *testing.T) {
	plan, err := BuildPlan(planEntityDocument(t, false), Spec{
		ServiceID: "audit", ProtoPackage: "acme.audit.v1", GoPackage: "example.com/acme/audit/v1;auditv1",
		ProtoArtifactPath: "api/audit.crud.generated.proto", LockPath: "api/audit.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("request")),
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ParseSnapshot(testPlanSource(t), plan.CRUDSnapshot())
	if err != nil {
		t.Fatalf("ParseSnapshot() error = %#v; snapshot=%s", err, plan.CRUDSnapshot())
	}
	if len(snapshot.Messages()) != 0 || len(snapshot.Services()) != 0 {
		t.Fatalf("empty CRUD snapshot has %d messages and %d services", len(snapshot.Messages()), len(snapshot.Services()))
	}
	compilePlanProto(t, plan.ProtoPath(), plan.ProtoBytes())
}

func TestMultiTenantConfigAndMixinMatrix(t *testing.T) {
	for _, test := range []struct {
		name, enabled, mixin  string
		wantTenant, wantError bool
	}{
		{name: "disabled without mixin", enabled: "false", mixin: "false"},
		{name: "disabled with mixin", enabled: "false", mixin: "true", wantError: true},
		{name: "enabled without mixin", enabled: "true", mixin: "false"},
		{name: "enabled with mixin", enabled: "true", mixin: "true", wantTenant: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			projection := planEntityProjection(t, true, true)
			if test.mixin == "true" {
				projection.Entities[0].Fields = append(projection.Entities[0].Fields, planTenantField(t))
			}
			value, err := entityvalue.NewDocument(projection)
			if err != nil {
				t.Fatal(err)
			}
			document, err := entity.AdoptLoadedDocument(value)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := BuildPlan(document, Spec{
				ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
				ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("request")),
				MultiTenant: MultiTenantConfig{Enabled: test.enabled == "true"},
			})
			if test.wantError {
				if err == nil {
					t.Fatal("disabled multi-tenant config accepted tenant mixin")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := ParseSnapshot(testPlanSource(t), plan.CRUDSnapshot())
			if err != nil {
				t.Fatal(err)
			}
			if got := snapshot.HasTenantEntities(); got != test.wantTenant {
				t.Fatalf("HasTenantEntities() = %v, want %v", got, test.wantTenant)
			}
		})
	}
}

func TestEnabledTenantMixinWithoutCRUDRetainsSnapshotState(t *testing.T) {
	projection := planEntityProjection(t, false, false)
	projection.Entities[0].Fields = append(projection.Entities[0].Fields, planTenantField(t))
	value, err := entityvalue.NewDocument(projection)
	if err != nil {
		t.Fatal(err)
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(document, Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("request")), MultiTenant: MultiTenantConfig{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ParseSnapshot(testPlanSource(t), plan.CRUDSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.HasTenantEntities() || len(snapshot.Services()) != 0 {
		t.Fatalf("snapshot tenant=%v services=%d", snapshot.HasTenantEntities(), len(snapshot.Services()))
	}
}

func TestRebuildPlanFromSnapshotPreservesTenantMarker(t *testing.T) {
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
	canonical, err := entity.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := entity.ParseSnapshot(testPlanSource(t), canonical)
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("request")), MultiTenant: MultiTenantConfig{Enabled: true},
	}
	rebuilt, err := RebuildPlanFromSnapshot(snapshot, spec)
	if err != nil {
		t.Fatalf("RebuildPlanFromSnapshot() error = %v", err)
	}
	crudSnapshot, err := ParseSnapshot(testPlanSource(t), rebuilt.CRUDSnapshot())
	if err != nil || !crudSnapshot.HasTenantEntities() {
		t.Fatalf("rebuilt tenant snapshot = %#v, %v", crudSnapshot, err)
	}
}

func TestEntityDocumentRejectsCRUDFieldWithoutPolicy(t *testing.T) {
	_, err := entityvalue.NewDocument(planEntityProjection(t, true, false))
	if err == nil {
		t.Fatal("CRUD entity accepted missing field policy")
	}
}

func TestTask2ScopeDoesNotChangeCRUDProtoOrCompatibilityAssignments(t *testing.T) {
	global := planEntityDocumentWithScope(t, sourcecomment.ScopeGlobal)
	tenant := planEntityDocumentWithScope(t, sourcecomment.ScopeTenant)
	spec := Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("request")),
	}
	globalPlan, err := BuildPlan(global, spec)
	if err != nil {
		t.Fatal(err)
	}
	tenantPlan, err := BuildPlan(tenant, spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(globalPlan.ProtoBytes()) != string(tenantPlan.ProtoBytes()) {
		t.Fatal("scope changed CRUD Proto bytes")
	}
	if globalPlan.ProtoDigest() != tenantPlan.ProtoDigest() {
		t.Fatal("scope changed CRUD Proto digest")
	}
	globalLock := globalPlan.LockProposal().After()
	tenantLock := tenantPlan.LockProposal().After()
	if !bytes.Equal(lockWithoutSourceDigests(t, globalLock.CanonicalJSON()), lockWithoutSourceDigests(t, tenantLock.CanonicalJSON())) {
		t.Fatalf("scope changed compatibility lock beyond assignment sourceDigest\nglobal=%s\ntenant=%s", globalLock.CanonicalJSON(), tenantLock.CanonicalJSON())
	}
	assertPlanSourcesMatchEntityIR(t, globalPlan, global)
	assertPlanSourcesMatchEntityIR(t, tenantPlan, tenant)

	tenantFromGlobalSpec := spec
	tenantFromGlobalSpec.ExistingLock = &globalLock
	tenantFromGlobal, err := BuildPlan(tenant, tenantFromGlobalSpec)
	if err != nil {
		t.Fatalf("tenant with global existing lock: %v", err)
	}
	globalFromTenantSpec := spec
	globalFromTenantSpec.ExistingLock = &tenantLock
	globalFromTenant, err := BuildPlan(global, globalFromTenantSpec)
	if err != nil {
		t.Fatalf("global with tenant existing lock: %v", err)
	}
	for name, lock := range map[string]Lock{
		"tenant from global": tenantFromGlobal.LockProposal().After(),
		"global from tenant": globalFromTenant.LockProposal().After(),
	} {
		if !bytes.Equal(lockWithoutSourceDigests(t, globalLock.CanonicalJSON()), lockWithoutSourceDigests(t, lock.CanonicalJSON())) {
			t.Fatalf("%s changed compatibility numbers or reservations: %s", name, lock.CanonicalJSON())
		}
	}
}

func lockWithoutSourceDigests(t *testing.T, canonical []byte) []byte {
	t.Helper()
	var lock any
	if err := json.Unmarshal(canonical, &lock); err != nil {
		t.Fatal(err)
	}
	var scrub func(any)
	scrub = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			for key, child := range item {
				if key == "sourceDigest" {
					item[key] = ""
					continue
				}
				scrub(child)
			}
		case []any:
			for _, child := range item {
				scrub(child)
			}
		}
	}
	scrub(lock)
	encoded, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	result, err := jcs.Transform(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertPlanSourcesMatchEntityIR(t *testing.T, plan Plan, document entity.Document) {
	t.Helper()
	want := map[string]provenance.Digest{}
	for _, group := range [][]provenance.Source{document.ExecutionModuleSources(), document.Sources()} {
		for _, source := range group {
			want[source.Ref.String()] = source.Digest
		}
	}
	seen := map[string]struct{}{}
	for _, source := range plan.Sources() {
		key := source.Ref.String()
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("plan contains duplicate SourceRef %s", key)
		}
		seen[key] = struct{}{}
		if digest, ok := want[key]; !ok || digest != source.Digest {
			t.Fatalf("plan source %s digest = %s, want EntityIR digest %s", key, source.Digest.String(), digest.String())
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("plan sources = %d, want %d EntityIR sources", len(seen), len(want))
	}
}

func TestTask2KeepsTask1EntityAndFieldSources(t *testing.T) {
	document := planEntityDocument(t, true)
	input := document.Entities()[0]
	inputField := input.Fields()[0]
	protocol, _, err := Build(document, Spec{ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1"})
	if err != nil {
		t.Fatal(err)
	}
	account, ok := protocol.Message("Account")
	if !ok {
		t.Fatal("Account message missing")
	}
	fields := account.Fields()
	if len(fields) != 2 {
		t.Fatalf("Account fields = %d, want 2", len(fields))
	}
	if fields[0].Name() != "id" || fields[0].Source() != input.Source() {
		t.Fatalf("implicit identity source = %#v, want Task1 entity source %#v", fields[0].Source(), input.Source())
	}
	if fields[1].Name() != "name" || fields[1].Source() != inputField.Source() {
		t.Fatalf("business field source = %#v, want Task1 field source %#v", fields[1].Source(), inputField.Source())
	}
	listRequest, ok := protocol.Message("ListAccountRequest")
	if !ok {
		t.Fatal("ListAccountRequest missing")
	}
	for _, field := range listRequest.Fields() {
		if field.Source() != input.Source() {
			t.Fatalf("fixed field %s source = %#v, want Task1 entity source %#v", field.Name(), field.Source(), input.Source())
		}
	}
	createRequest, ok := protocol.Message("CreateAccountRequest")
	if !ok || len(createRequest.Fields()) != 1 || createRequest.Fields()[0].Source() != inputField.Source() {
		t.Fatalf("create request did not preserve Task1 field source")
	}
}

func TestTask2CRUDCreateOmitsRequiredInternalExcludedField(t *testing.T) {
	projection := planEntityProjection(t, true, true)
	projection.Entities[0].Name = "NeutralRecord"
	projection.Entities[0].SourceRef = mustPlanRepositoryRef(t, "fixtures/generation/ent-consumer/schema/neutral_record.go", "schema:NeutralRecord")
	projection.Entities[0].Meta.Scope = sourcecomment.ScopeTenant
	projection.Entities[0].Fields[0].Name = "required_internal"
	projection.Entities[0].Fields[0].SourceRef = mustPlanRepositoryRef(t, "fixtures/generation/ent-consumer/schema/neutral_record.go", "schema:NeutralRecord/field:required_internal")
	projection.Entities[0].Fields[0].Meta.Visibility = sourcecomment.VisibilityInternal
	projection.Entities[0].Fields[0].Meta.CRUD = &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadExclude, Mutation: sourcecomment.MutationNone}
	value, err := entityvalue.NewDocument(projection)
	if err != nil {
		t.Fatalf("NewDocument: %v", err)
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	protocol, _, err := Build(document, Spec{ServiceID: "neutral", ProtoPackage: "acme.neutral.v1", GoPackage: "example.com/acme/neutral/v1;neutralv1"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	request, ok := protocol.Message("CreateNeutralRecordRequest")
	if !ok {
		t.Fatal("CreateNeutralRecordRequest missing")
	}
	if len(request.Fields()) != 0 {
		t.Fatalf("create request fields = %#v, want empty; plain ScopeTenant must not add fields", request.Fields())
	}
}

func planEntityDocumentWithScope(t *testing.T, scope sourcecomment.RecordScope) entity.Document {
	t.Helper()
	document := planEntityProjection(t, true, true)
	document.Entities[0].Meta.Scope = scope
	value, err := entityvalue.NewDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	result, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func planEntityDocument(t *testing.T, withCRUD bool) entity.Document {
	return planEntityDocumentWithFieldPolicy(t, withCRUD, withCRUD)
}

func planEntityDocumentWithFieldPolicy(t *testing.T, withCRUD, withFieldPolicy bool) entity.Document {
	t.Helper()
	projection := planEntityProjection(t, withCRUD, withFieldPolicy)
	value, err := entityvalue.NewDocument(projection)
	if err != nil {
		t.Fatal(err)
	}
	result, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func planEntityProjection(t *testing.T, withCRUD, withFieldPolicy bool) entityvalue.Projection {
	t.Helper()
	path := "fixtures/generation/ent-consumer/schema/account.go"
	ref, err := provenance.RepositoryRef(path, "schema:Account")
	if err != nil {
		t.Fatal(err)
	}
	fieldRef, err := provenance.RepositoryRef(path, "schema:Account/field:name")
	if err != nil {
		t.Fatal(err)
	}
	schemaMeta := sourcecomment.SchemaFacts{Label: localized("account", "Account"), Description: localized("account.desc", "Account record"), Scope: sourcecomment.ScopeTenant}
	fieldMeta := sourcecomment.FieldFacts{Label: localized("account.name", "Name"), Description: localized("account.name.desc", "Account name"), Control: sourcecomment.UIControlText, Visibility: sourcecomment.VisibilityPublic, CRUD: &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadInclude, Mutation: sourcecomment.MutationCreateUpdate}}
	if !withFieldPolicy {
		fieldMeta.CRUD = nil
	}
	projection := entityvalue.Projection{Entities: []entityvalue.EntityProjection{{Name: "Account", SourceRef: ref, Meta: schemaMeta, Identity: entityvalue.IdentityProjection{Kind: string(entity.IdentityImplicit), Name: "id", Type: string(entity.ScalarInt64)}, Fields: []entityvalue.FieldProjection{{Name: "name", SourceRef: fieldRef, Type: string(entity.ScalarString), Meta: fieldMeta}}}}}
	if withCRUD {
		crud, err := sourcecomment.NewCRUDOperations(sourcecomment.CRUDList, sourcecomment.CRUDGet, sourcecomment.CRUDCreate)
		if err != nil {
			t.Fatal(err)
		}
		projection.Entities[0].CRUD = &crud
	}
	return projection
}

func localized(key, english string) sourcecomment.LocalizedText {
	return sourcecomment.LocalizedText{Key: key, ZhCN: english, EnUS: english}
}

func planTenantField(t *testing.T) entityvalue.FieldProjection {
	t.Helper()
	return entityvalue.FieldProjection{
		Name: "tenant_id", SourceRef: mustPlanRepositoryRef(t, "fixtures/generation/ent-consumer/schema/account.go", "schema:Account/field:tenant_id"),
		Type: string(entity.ScalarInt64), Immutable: true, IsTenantField: true,
		Meta: sourcecomment.FieldFacts{Label: localized("tenant.id", "Tenant ID"), Description: localized("tenant.id.desc", "Tenant identifier"), Control: sourcecomment.UIControlReadonly, Visibility: sourcecomment.VisibilityInternal},
	}
}

func compilePlanProto(t *testing.T, name string, source []byte) {
	t.Helper()
	resolver := &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{name: string(source)})}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(resolver)}
	if _, err := compiler.Compile(context.Background(), name); err != nil {
		t.Fatalf("compile Proto: %v", err)
	}
}

func testPlanSource(t *testing.T) provenance.DomainSource {
	t.Helper()
	value, err := provenance.ParseDomainSource("quality/crud-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustPlanRepositoryRef(t *testing.T, path, fragment string) provenance.SourceRef {
	t.Helper()
	ref, err := provenance.RepositoryRef(path, fragment)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func sourceRefStrings(values []provenance.Source) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Ref.String()
	}
	return result
}

func refStrings(values []provenance.SourceRef) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return result
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

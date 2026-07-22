package entipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/crudbuild"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
)

func TestResultDomainUnionAcceptsDirectOwnerErrorAndRejectsUnknownOrWrapped(t *testing.T) {
	request := newTestRequest(t)
	_, _, ownerErr := crudbuild.Build(resultTestEntityDocument(t), crudbuild.Spec{ServiceID: "INVALID", ProtoPackage: "acme.v1", GoPackage: "example.com/acme"})
	input, ok, err := ResultFromDomainError(ownerErr)
	if err != nil || !ok {
		t.Fatalf("ResultFromDomainError = %v, %v", ok, err)
	}
	encoded, err := EncodeResult(request, input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ParseResult(testDomainSource(t, "quality/ent-result.json"), request, encoded)
	if err != nil {
		t.Fatal(err)
	}
	domain, ok := snapshot.DomainFailure()
	if !ok || domain.Owner() != "crudproto" || domain.Code() != "crud_build_invalid" || domain.Reason() != "service_id_invalid" || domain.Pointer() != "/serviceId" || domain.Source() != "" {
		t.Fatalf("domain failure = %#v", domain)
	}
	for _, unknown := range []error{errors.New("unknown"), fmt.Errorf("wrapped: %w", ownerErr)} {
		if _, ok, err := ResultFromDomainError(unknown); ok || err != nil {
			t.Fatalf("unknown projection = %v, %v", ok, err)
		}
	}
}

func TestResultDomainBranchRejectsTuplePollution(t *testing.T) {
	request := newTestRequest(t)
	_, _, ownerErr := crudbuild.Build(resultTestEntityDocument(t), crudbuild.Spec{ServiceID: "INVALID", ProtoPackage: "acme.v1", GoPackage: "example.com/acme"})
	input, ok, err := ResultFromDomainError(ownerErr)
	if err != nil || !ok {
		t.Fatal(err)
	}
	encoded, err := EncodeResult(request, input)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if json.Unmarshal(encoded, &root) != nil {
		t.Fatal("decode result")
	}
	domain := root["error"].(map[string]any)
	tests := []struct {
		name   string
		change func(map[string]any)
	}{{"unknown owner", func(v map[string]any) { v["owner"] = "other" }}, {"wrong reason", func(v map[string]any) { v["reason"] = "wire_incompatible" }}, {"wrong source", func(v map[string]any) { v["source"] = "repo/private" }}, {"pollution", func(v map[string]any) { v["message"] = "private" }}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyDomain := map[string]any{}
			for key, value := range domain {
				copyDomain[key] = value
			}
			test.change(copyDomain)
			copyRoot := map[string]any{}
			for key, value := range root {
				copyRoot[key] = value
			}
			copyRoot["error"] = copyDomain
			if _, err := ParseResult(testDomainSource(t, "quality/ent-result.json"), request, mustCanonicalJSON(t, copyRoot)); err == nil {
				t.Fatal("invalid domain tuple accepted")
			}
		})
	}
}

func TestResultPlanRoundTripIsRequestBoundAndReadOnly(t *testing.T) {
	request := newTestRequest(t)
	plan := resultTestPlan(t, request)
	input, err := ResultFromPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeResult(request, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) == 0 || encoded[len(encoded)-1] == '\n' {
		t.Fatalf("result framing = %q", encoded)
	}
	snapshot, err := ParseResult(testDomainSource(t, "quality/ent-result.json"), request, encoded)
	if err != nil {
		t.Fatal(err)
	}
	planSnapshot, ok := snapshot.Plan()
	if !ok {
		t.Fatal("plan branch missing")
	}
	if !planSnapshot.HasCRUD() || planSnapshot.SourceDigest() != plan.SourceDigest() {
		t.Fatal("plan snapshot lost verified CRUD or source metadata")
	}
	crudSnapshot, err := planSnapshot.CRUDSnapshot()
	if err != nil || !crudSnapshot.Valid() || len(crudSnapshot.Services()) != 1 {
		t.Fatalf("plan snapshot lost verified CRUD snapshot: %#v, %v", crudSnapshot, err)
	}
	methods := crudSnapshot.Services()[0].Methods()
	methods[0] = crudbuild.Method{}
	crudAgain, err := planSnapshot.CRUDSnapshot()
	if err != nil || crudAgain.Services()[0].Methods()[0].Name() == "" {
		t.Fatal("plan CRUD snapshot is mutable through returned slices")
	}
	spec := testRequestSpec(t)
	if planSnapshot.ModuleGraphDigest() != spec.ModuleGraphDigest || planSnapshot.BuildInputDigest() != spec.BuildInputDigest {
		t.Fatal("plan snapshot lost verified request input digests")
	}
	entities := planSnapshot.EntitySnapshot()
	entityValues := entities.Entities()
	if len(entityValues) != 1 || entityValues[0].Meta() != resultTestEntityDocument(t).Entities()[0].Meta() {
		t.Fatal("plan snapshot lost typed SchemaMeta")
	}
	account := entityValues[0]
	fields := account.Fields()
	if len(fields) != 1 || !reflect.DeepEqual(fields[0].Meta(), resultTestEntityDocument(t).Entities()[0].Fields()[0].Meta()) {
		t.Fatal("plan snapshot lost typed FieldMeta")
	}
	crud, ok := account.CRUD()
	if !ok || !reflect.DeepEqual(crud.Operations(), []nexaent.CRUDOperation{nexaent.CRUDList, nexaent.CRUDGet}) {
		t.Fatal("plan snapshot lost typed CRUD metadata")
	}
	projected := entities.ProjectedSources()
	projected[0] = provenance.Source{}
	if planSnapshot.EntitySnapshot().ProjectedSources()[0].Ref.String() == "" {
		t.Fatal("plan snapshot EntitySnapshot is mutable through returned slices")
	}
	if planSnapshot.ProtoID() != plan.ProtoID() || planSnapshot.ProtoPath() != plan.ProtoPath() || planSnapshot.ProtoDigest() != plan.ProtoDigest() ||
		planSnapshot.ProtoMediaType() != crudbuild.ProtoMediaType || planSnapshot.ProtoOwner() != crudbuild.ProtoOwner || planSnapshot.ProtoStalePolicy() != crudbuild.StaleDeleteIfUnmodified {
		t.Fatal("plan snapshot lost Proto artifact metadata")
	}
	protoRefs := planSnapshot.ProtoSourceRefs()
	if len(protoRefs) == 0 || !equalSourceRefs(protoRefs, plan.ProtoSourceRefs()) {
		t.Fatal("plan snapshot lost Proto source refs")
	}
	protoRefs[0] = provenance.SourceRef{}
	if planSnapshot.ProtoSourceRefs()[0].String() == "" {
		t.Fatal("plan snapshot Proto source refs are mutable")
	}
	proposal := plan.LockProposal()
	if planSnapshot.LockPath() != plan.LockPath() || !planSnapshot.LockChanged() || planSnapshot.LockDigest() != proposal.Digest() ||
		len(planSnapshot.LockBefore()) != 0 || !bytes.Equal(planSnapshot.LockAfter(), proposal.After().CanonicalJSON()) ||
		!equalSourceRefs(planSnapshot.LockSourceRefs(), plan.LockSourceRefs()) {
		t.Fatal("plan snapshot lost compatibility-lock proposal metadata")
	}
	canonical := planSnapshot.CanonicalJSON()
	canonical[0] = 0
	if planSnapshot.CanonicalJSON()[0] == 0 {
		t.Fatal("plan snapshot bytes are mutable")
	}
	again, err := CanonicalResult(snapshot)
	if err != nil || !bytes.Equal(again, encoded) {
		t.Fatalf("canonical result = %s, %v", again, err)
	}
	otherSpec := testRequestSpec(t)
	otherSpec.ServiceID = "other"
	other, err := NewRequest(otherSpec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseResult(testDomainSource(t, "quality/ent-result.json"), other, encoded); err == nil {
		t.Fatal("different request accepted result")
	}
}

func TestTenantPlanResultRetainsVerifiedCRUDSnapshot(t *testing.T) {
	for _, withCRUD := range []bool{true, false} {
		name := "tenant-only"
		if withCRUD {
			name = "tenant-crud"
		}
		t.Run(name, func(t *testing.T) {
			request := newTestRequest(t)
			spec, err := request.BuildSpec()
			if err != nil {
				t.Fatal(err)
			}
			plan, err := crudbuild.BuildPlan(resultTestTenantDocument(t, withCRUD), spec)
			if err != nil {
				t.Fatal(err)
			}
			input, err := ResultFromPlan(plan)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := EncodeResult(request, input)
			if err != nil {
				t.Fatal(err)
			}
			result, err := ParseResult(testDomainSource(t, "quality/tenant-result.json"), request, encoded)
			if err != nil {
				t.Fatalf("ParseResult() error = %#v", err)
			}
			planSnapshot, ok := result.Plan()
			if !ok {
				t.Fatal("plan branch missing")
			}
			crudSnapshot, err := planSnapshot.CRUDSnapshot()
			if err != nil || !crudSnapshot.HasTenantEntities() {
				t.Fatalf("CRUDSnapshot() tenant=%v error=%v", crudSnapshot.HasTenantEntities(), err)
			}
			if got := len(crudSnapshot.Services()); (got != 0) != withCRUD {
				t.Fatalf("services=%d withCRUD=%v", got, withCRUD)
			}
			if withCRUD {
				for _, method := range crudSnapshot.Services()[0].Methods() {
					bindings := method.RPCContext().ContextFields()
					if len(bindings) != 1 || bindings[0].Source() != crudbuild.ContextTenantID || bindings[0].RPCField() != "tenant_id" {
						t.Fatalf("method %s context = %#v", method.Name(), bindings)
					}
					var request crudbuild.Message
					for _, message := range crudSnapshot.Messages() {
						if message.Name() == method.Input() {
							request = message
							break
						}
					}
					var tenant crudbuild.Field
					for _, field := range request.Fields() {
						if field.IsTenantContext() {
							tenant = field
							break
						}
					}
					if tenant.Name() != "tenant_id" || tenant.Type() != "int64" || !tenant.Internal() {
						t.Fatalf("method %s tenant field = %#v", method.Name(), tenant)
					}
				}
			}
			ids := crudSnapshot.TenantEntityIDs()
			ids[0] = "changed"
			canonical := crudSnapshot.CanonicalJSON()
			canonical[0] = '!'
			again, err := planSnapshot.CRUDSnapshot()
			if err != nil || again.TenantEntityIDs()[0] == "changed" || again.CanonicalJSON()[0] == '!' {
				t.Fatal("CRUDSnapshot accessor exposed mutable state")
			}
		})
	}
}

func TestResultPlanKeepsCompatibilitySourcesOutsideProtoArtifactRefs(t *testing.T) {
	initialRequest := newTestRequest(t)
	initialPlan := resultTestPlan(t, initialRequest)
	lock := initialPlan.LockProposal().After()
	lockRef := testSourceRef(t, "api/accounts.crud-protocol.lock.json")
	lockSource := provenance.Source{Ref: lockRef, Digest: provenance.SHA256(lock.CanonicalJSON())}
	manifestRef := testSourceRef(t, ".nexa/generation/crud-proto.accounts.manifest.json")
	manifestSource := provenance.Source{Ref: manifestRef, Digest: provenance.SHA256([]byte("manifest"))}

	spec := testRequestSpec(t)
	spec.ExistingLock = &ExistingLockInput{Source: lockSource, Value: lock}
	spec.PublishedArtifact = &PublishedArtifact{ID: initialPlan.ProtoID(), Digest: initialPlan.ProtoDigest(), ManifestSource: manifestSource}
	request, err := NewRequest(spec)
	if err != nil {
		t.Fatal(err)
	}
	if request.RequestDigest() == initialRequest.RequestDigest() {
		t.Fatal("compatibility inputs did not constrain the request digest")
	}
	plan := resultTestPlan(t, request)
	input, err := ResultFromPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeResult(request, input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ParseResult(testDomainSource(t, "quality/ent-result.json"), request, encoded)
	if err != nil {
		t.Fatalf("ParseResult() error = %#v", err)
	}
	projected, ok := snapshot.Plan()
	if !ok {
		t.Fatal("plan branch missing")
	}
	allRefs := snapshotSourceRefStrings(projected.Sources())
	if !containsResultString(allRefs, lockRef.String()) || !containsResultString(allRefs, manifestRef.String()) {
		t.Fatalf("plan source closure lost compatibility inputs: %#v", allRefs)
	}
	protoRefs := resultRefStrings(projected.ProtoSourceRefs())
	if containsResultString(protoRefs, lockRef.String()) || containsResultString(protoRefs, manifestRef.String()) {
		t.Fatalf("Proto source refs contain control-only inputs: %#v", protoRefs)
	}
	lockRefs := resultRefStrings(projected.LockSourceRefs())
	if !containsResultString(lockRefs, lockRef.String()) || !containsResultString(lockRefs, manifestRef.String()) {
		t.Fatalf("lock source refs lost compatibility inputs: %#v", lockRefs)
	}

	authoringRef := testSourceRef(t, "go.mod").String()
	replacementRef := testSourceRef(t, "other/authoring.go").String()
	tests := []struct {
		name   string
		mutate func([]string) []string
	}{
		{
			name: "delete authoring ref",
			mutate: func(refs []string) []string {
				result := refs[:0]
				for _, ref := range refs {
					if ref != authoringRef {
						result = append(result, ref)
					}
				}
				return result
			},
		},
		{
			name: "add control-only ref",
			mutate: func(refs []string) []string {
				refs = append(refs, manifestRef.String())
				sort.Strings(refs)
				return refs
			},
		},
		{
			name: "replace authoring ref",
			mutate: func(refs []string) []string {
				for index, ref := range refs {
					if ref == authoringRef {
						refs[index] = replacementRef
					}
				}
				sort.Strings(refs)
				return refs
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal(encoded, &root); err != nil {
				t.Fatal(err)
			}
			plan := root["plan"].(map[string]any)
			proto := plan["protoArtifact"].(map[string]any)
			wireRefs := proto["sourceRefs"].([]any)
			refs := make([]string, len(wireRefs))
			for index, value := range wireRefs {
				refs[index] = value.(string)
			}
			mutated := test.mutate(refs)
			if equalResultStrings(mutated, resultRefStrings(projected.ProtoSourceRefs())) {
				t.Fatal("test mutation did not change Proto source refs")
			}
			proto["sourceRefs"] = mutated
			delete(plan, "planDigest")
			plan["planDigest"] = provenance.SHA256(mustCanonicalJSON(t, plan)).String()

			_, err := ParseResult(testDomainSource(t, "quality/ent-result.json"), request, mustCanonicalJSON(t, root))
			assertPlanResultError(t, err, "/plan/protoArtifact/sourceRefs")
		})
	}
}

func equalSourceRefs(left, right []provenance.SourceRef) bool {
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

func snapshotSourceRefStrings(values []provenance.Source) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Ref.String()
	}
	return result
}

func resultRefStrings(values []provenance.SourceRef) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return result
}

func containsResultString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func equalResultStrings(left, right []string) bool {
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

func TestResultRejectsPlansBuiltForDifferentRequestFacts(t *testing.T) {
	request := newTestRequest(t)
	tests := []struct {
		name, pointer string
		change        func(*crudbuild.Spec)
	}{
		{name: "service", pointer: "/plan/crudSnapshot", change: func(spec *crudbuild.Spec) { spec.ServiceID = "audit" }},
		{name: "Proto package", pointer: "/plan/crudSnapshot", change: func(spec *crudbuild.Spec) { spec.ProtoPackage = "acme.audit.v1" }},
		{name: "Proto artifact path", pointer: "/plan/protoArtifact/path", change: func(spec *crudbuild.Spec) { spec.ProtoArtifactPath = "api/audit.crud.generated.proto" }},
		{name: "lock path", pointer: "/plan/lockProposal/path", change: func(spec *crudbuild.Spec) { spec.LockPath = "api/audit.crud-protocol.lock.json" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec, err := request.BuildSpec()
			if err != nil {
				t.Fatal(err)
			}
			test.change(&spec)
			plan, err := crudbuild.BuildPlan(resultTestEntityDocument(t), spec)
			if err != nil {
				t.Fatal(err)
			}
			input, err := ResultFromPlan(plan)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := EncodeResult(request, input)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseResult(testDomainSource(t, "quality/ent-result.json"), request, encoded)
			assertPlanResultError(t, err, test.pointer)
		})
	}
}

func TestResultRejectsEntitySnapshotFromDifferentSourceProjection(t *testing.T) {
	request := newTestRequest(t)
	buildSpec, err := request.BuildSpec()
	if err != nil {
		t.Fatal(err)
	}
	entityPlan, err := crudbuild.BuildPlan(resultTestEntityDocumentAt(t, "fixtures/generation/ent-consumer/schema/account.go"), buildSpec)
	if err != nil {
		t.Fatal(err)
	}
	projectionPlan, err := crudbuild.BuildPlan(resultTestEntityDocumentAt(t, "consumer/schema/account.go"), buildSpec)
	if err != nil {
		t.Fatal(err)
	}
	input, err := ResultFromPlan(projectionPlan)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeResult(request, input)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatal(err)
	}
	plan := root["plan"].(map[string]any)
	var entityRoot map[string]any
	if err := json.Unmarshal(entityPlan.EntitySnapshot(), &entityRoot); err != nil {
		t.Fatal(err)
	}
	plan["entitySnapshot"] = entityRoot
	delete(plan, "planDigest")
	preimage := mustCanonicalJSON(t, plan)
	plan["planDigest"] = provenance.SHA256(preimage).String()
	_, err = ParseResult(testDomainSource(t, "quality/ent-result.json"), request, mustCanonicalJSON(t, root))
	assertPlanResultError(t, err, "/plan/sources")
}

func TestResultRejectsSelfConsistentEmptySourceProjection(t *testing.T) {
	request := newTestRequest(t)
	input, err := ResultFromPlan(resultTestPlan(t, request))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeResult(request, input)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatal(err)
	}
	plan := root["plan"].(map[string]any)
	plan["sources"] = []any{}
	plan["sourceDigest"] = provenance.SHA256(mustCanonicalJSON(t, map[string]any{"apiVersion": "nexa.dev/ent-graph-plan-source-set/v1", "sources": []any{}})).String()
	plan["protoArtifact"].(map[string]any)["sourceRefs"] = []any{}
	plan["lockProposal"].(map[string]any)["sourceRefs"] = []any{}
	delete(plan, "planDigest")
	plan["planDigest"] = provenance.SHA256(mustCanonicalJSON(t, plan)).String()
	_, err = ParseResult(testDomainSource(t, "quality/ent-result.json"), request, mustCanonicalJSON(t, root))
	assertPlanResultError(t, err, "/plan/sources")
}

func TestResultRejectsPollutionAndInvalidUnion(t *testing.T) {
	request := newTestRequest(t)
	input, err := ResultFromPlan(resultTestPlan(t, request))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeResult(request, input)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatal(err)
	}
	mutate := func(change func(map[string]any)) []byte {
		copyValue := map[string]any{}
		for key, value := range root {
			copyValue[key] = value
		}
		change(copyValue)
		return mustCanonicalJSON(t, copyValue)
	}
	tests := []struct {
		name string
		data []byte
	}{
		{name: "pollution", data: mutate(func(value map[string]any) { value["extra"] = true })},
		{name: "dual", data: mutate(func(value map[string]any) {
			value["error"] = map[string]any{"owner": "crudproto", "code": "crud_build_invalid", "reason": "service_id_invalid", "pointer": "/serviceId", "source": ""}
		})},
		{name: "missing", data: mutate(func(value map[string]any) { delete(value, "plan") })},
		{name: "null", data: mutate(func(value map[string]any) { value["plan"] = nil })},
		{name: "trailing", data: append(append([]byte(nil), encoded...), []byte(`{}`)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseResult(testDomainSource(t, "quality/ent-result.json"), request, test.data); err == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
}

func resultTestPlan(t *testing.T, request Request) crudbuild.Plan {
	t.Helper()
	document := resultTestEntityDocument(t)
	buildSpec, err := request.BuildSpec()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := crudbuild.BuildPlan(document, buildSpec)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func resultTestEntityDocument(t *testing.T) entity.Document {
	return resultTestEntityDocumentAt(t, "fixtures/generation/ent-consumer/schema/account.go")
}

func resultTestEntityDocumentAt(t *testing.T, path string) entity.Document {
	t.Helper()
	ref, err := provenance.RepositoryRef(path, "schema:Account")
	if err != nil {
		t.Fatal(err)
	}
	fieldRef, err := provenance.RepositoryRef(path, "schema:Account/field:name")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := nexaent.CRUD(nexaent.CRUDList, nexaent.CRUDGet).CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	crud, err := nexaent.DecodeCRUD(raw)
	if err != nil {
		t.Fatal(err)
	}
	meta := nexaent.SchemaMeta{Label: nexaent.LocalizedText{Key: "account.label", ZhCN: "账户", EnUS: "Account"}, Description: nexaent.LocalizedText{Key: "account.desc", ZhCN: "账户", EnUS: "Account"}, Identity: nexaent.IdentityEntID, Scope: nexaent.ScopeTenant}
	fieldMeta := nexaent.FieldMeta{Label: nexaent.LocalizedText{Key: "account.name", ZhCN: "名称", EnUS: "Name"}, Description: nexaent.LocalizedText{Key: "account.name.desc", ZhCN: "名称", EnUS: "Name"}, UIHint: nexaent.UIHintText, Visibility: nexaent.VisibilityPublic, CRUD: &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: nexaent.MutationCreateUpdate}}
	value, err := entityvalue.NewDocument(entityvalue.Projection{
		ExecutionModuleSources: []provenance.Source{{Ref: testSourceRef(t, "go.mod"), Digest: provenance.SHA256([]byte("go.mod"))}},
		Entities:               []entityvalue.EntityProjection{{Name: "Account", SourceRef: ref, Meta: meta, CRUD: &crud, Identity: entityvalue.IdentityProjection{Kind: string(entity.IdentityImplicit), Name: "id", Type: string(entity.ScalarInt64)}, Fields: []entityvalue.FieldProjection{{Name: "name", SourceRef: fieldRef, Type: string(entity.ScalarString), Meta: fieldMeta}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func resultTestTenantDocument(t *testing.T, withCRUD bool) entity.Document {
	t.Helper()
	path := "fixtures/generation/ent-consumer/schema/tenant_account.go"
	ref, _ := provenance.RepositoryRef(path, "schema:TenantAccount")
	tenantRef, _ := provenance.RepositoryRef(path, "schema:TenantAccount/field:tenant_id")
	projection := entityvalue.EntityProjection{
		Name: "TenantAccount", SourceRef: ref,
		Meta:     nexaent.SchemaMeta{Label: nexaent.LocalizedText{Key: "tenant_account.label", ZhCN: "Tenant Account", EnUS: "Tenant Account"}, Description: nexaent.LocalizedText{Key: "tenant_account.description", ZhCN: "Tenant Account", EnUS: "Tenant Account"}, Identity: nexaent.IdentityEntID, Scope: nexaent.ScopeTenant},
		Identity: entityvalue.IdentityProjection{Kind: string(entity.IdentityImplicit), Name: "id", Type: string(entity.ScalarInt64)},
		Fields:   []entityvalue.FieldProjection{{Name: "tenant_id", SourceRef: tenantRef, Type: string(entity.ScalarInt64), Immutable: true, IsTenantField: true, Meta: nexaent.FieldMeta{Label: nexaent.LocalizedText{Key: "tenant_account.tenant_id.label", ZhCN: "Tenant", EnUS: "Tenant"}, Description: nexaent.LocalizedText{Key: "tenant_account.tenant_id.description", ZhCN: "Tenant", EnUS: "Tenant"}, UIHint: nexaent.UIHintReadonly, Visibility: nexaent.VisibilityInternal}}},
	}
	if withCRUD {
		crud := taskResultCRUD(t, nexaent.CRUDGet, nexaent.CRUDCreate)
		projection.CRUD = &crud
	}
	value, err := entityvalue.NewDocument(entityvalue.Projection{ExecutionModuleSources: []provenance.Source{{Ref: testSourceRef(t, "go.mod"), Digest: provenance.SHA256([]byte("go.mod"))}}, Entities: []entityvalue.EntityProjection{projection}})
	if err != nil {
		t.Fatal(err)
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func taskResultCRUD(t *testing.T, operations ...nexaent.CRUDOperation) nexaent.CRUDSpec {
	t.Helper()
	raw, err := nexaent.CRUD(operations...).CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	crud, err := nexaent.DecodeCRUD(raw)
	if err != nil {
		t.Fatal(err)
	}
	return crud
}

func assertPlanResultError(t *testing.T, err error, pointer string) {
	t.Helper()
	typed, ok := err.(*Error)
	if !ok || typed.Code() != "ent_graph_result_invalid" || typed.Stage() != "result-decode" || typed.Reason() != "plan_invalid" || typed.Pointer() != pointer {
		t.Fatalf("plan result error = %#v, want %s", err, pointer)
	}
}

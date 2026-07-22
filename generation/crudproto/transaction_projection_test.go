package crudproto

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/crudbuild"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
)

func TestVerifiedPlanProjectsProtoAndLockIntoOneTransaction(t *testing.T) {
	repositoryPath := t.TempDir()
	document := projectionEntityDocument(t, true)
	firstBuild, err := crudbuild.BuildPlan(document, crudbuild.Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("request-v1")),
	})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifiedEntGraphPlanFromBuild(firstBuild)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.HasCRUD() {
		t.Fatal("verified plan lost explicit CRUD selection")
	}
	emitted := map[string][]byte{}
	inputs, controls, err := verified.TransactionInputs(func(name string, content []byte) error {
		emitted[name] = append([]byte(nil), content...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || len(controls) != 1 {
		t.Fatalf("transaction inputs = artifacts:%d controls:%d", len(inputs), len(controls))
	}
	if len(emitted) != 1 || provenance.SHA256(emitted[inputs[0].Path]) != inputs[0].Digest {
		t.Fatalf("publish candidates emitted by projection = %#v", emitted)
	}
	if _, ok := emitted["api/accounts.crud-protocol.lock.json"]; ok {
		t.Fatal("control candidate was emitted through external host projection")
	}
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	manifestPath := ".nexa/generation/crud-proto.manifest.json"
	plan, err := buildProjectionTransaction(t, repositoryPath, verified, firstBuild.Sources(), nil, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes()) != 2 {
		t.Fatalf("initial changes = %#v", plan.Changes())
	}
	next, ok := plan.NextManifest()
	if !ok || len(next.Artifacts()) != 1 || next.Artifacts()[0].ID() != "crud-proto.accounts" || next.Artifacts()[0].Path() != "api/accounts.crud.generated.proto" {
		t.Fatalf("next manifest = %#v, %v", next.Artifacts(), ok)
	}
	manifestJSON, _ := next.CanonicalJSON()
	if bytes.Contains(manifestJSON, []byte("crud-protocol.lock")) {
		t.Fatal("compatibility lock leaked into Artifact Manifest")
	}
	if _, err := transaction.Write(context.Background(), plan, repositoryPath, transaction.WriteOptions{PlanDigest: plan.PlanDigest()}); err != nil {
		t.Fatal(err)
	}

	lockPath := filepath.Join(repositoryPath, "api/accounts.crud-protocol.lock.json")
	lockBytes, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	lockSource, _ := provenance.ParseDomainSource("api/accounts.crud-protocol.lock.json")
	existing, err := crudbuild.ParseLock(lockSource, lockBytes)
	if err != nil {
		t.Fatal(err)
	}
	lockRef, _ := provenance.RepositoryRef("api/accounts.crud-protocol.lock.json", "")
	existingSource := provenance.Source{Ref: lockRef, Digest: provenance.SHA256(lockBytes)}
	secondBuild, err := crudbuild.BuildPlan(document, crudbuild.Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v2;accountsv2",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("request-v2")),
		ExistingLock: &existing, ExistingLockSource: &existingSource,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondVerified, err := verifiedEntGraphPlanFromBuild(secondBuild)
	if err != nil {
		t.Fatal(err)
	}
	_, secondControls, err := secondVerified.TransactionInputs(func(string, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(secondControls) != 0 {
		t.Fatal("unchanged compatibility lock produced a mutation")
	}
	update, err := buildProjectionTransaction(t, repositoryPath, secondVerified, secondBuild.Sources(), &next, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(update.Changes()) != 1 || update.Changes()[0].Kind() != transaction.ChangeUpdate || update.Changes()[0].ID() != "crud-proto.accounts" {
		t.Fatalf("update changes = %#v", update.Changes())
	}
}

func TestVerifiedPlanExposesReadOnlyTypedEntitySnapshot(t *testing.T) {
	document := projectionEntityDocument(t, true)
	plan, err := crudbuild.BuildPlan(document, crudbuild.Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("request")),
	})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifiedEntGraphPlanFromBuild(plan)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := verified.EntitySnapshot()
	if err != nil {
		t.Fatal(err)
	}
	entities := snapshot.Entities()
	if len(entities) != 1 || entities[0].Meta() != document.Entities()[0].Meta() {
		t.Fatal("typed SchemaMeta was not retained")
	}
	account := entities[0]
	crud, ok := account.CRUD()
	if !ok || !reflect.DeepEqual(crud.Operations(), []nexaent.CRUDOperation{nexaent.CRUDGet}) {
		t.Fatal("typed CRUD metadata was not retained")
	}
	sources := snapshot.ProjectedSources()
	sources[0] = provenance.Source{}
	again, err := verified.EntitySnapshot()
	if err != nil || again.ProjectedSources()[0].Ref.String() == "" {
		t.Fatal("EntitySnapshot returned mutable projected sources")
	}
	if _, err := verified.ModuleGraphDigest(); err == nil {
		t.Fatal("direct-build plan claimed a host module graph digest")
	}
	if _, err := verified.BuildInputDigest(); err == nil {
		t.Fatal("direct-build plan claimed a host build input digest")
	}
	crudSnapshot, err := verified.CRUDSnapshot()
	if err != nil || crudSnapshot.APIVersion() != APIVersion || len(crudSnapshot.Services()) != 1 {
		t.Fatalf("verified CRUD snapshot = %#v, error = %v", crudSnapshot, err)
	}
	methods := crudSnapshot.Services()[0].Methods()
	methods[0] = Method{}
	crudAgain, err := verified.CRUDSnapshot()
	if err != nil || len(crudAgain.Services()[0].Methods()) == 0 || crudAgain.Services()[0].Methods()[0].Name() == "" {
		t.Fatal("CRUDSnapshot returned mutable service methods")
	}
}

func TestRepeatedGenerationWithPublishedBaselineKeepsArtifactManifestByteIdentical(t *testing.T) {
	repositoryPath := t.TempDir()
	document := projectionEntityDocument(t, true)
	manifestPath := ".nexa/generation/crud-proto.accounts.manifest.json"
	spec := crudbuild.Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("request-initial")),
	}
	firstBuild, err := crudbuild.BuildPlan(document, spec)
	if err != nil {
		t.Fatal(err)
	}
	firstVerified, err := verifiedEntGraphPlanFromBuild(firstBuild)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	first, err := buildProjectionTransaction(t, repositoryPath, firstVerified, firstBuild.Sources(), nil, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Write(context.Background(), first, repositoryPath, transaction.WriteOptions{PlanDigest: first.PlanDigest()}); err != nil {
		t.Fatal(err)
	}
	firstManifest, ok := first.NextManifest()
	if !ok {
		t.Fatal("first next manifest is absent")
	}
	firstManifestJSON, err := firstManifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	lockBytes, err := os.ReadFile(filepath.Join(repositoryPath, spec.LockPath))
	if err != nil {
		t.Fatal(err)
	}
	lockDomain, _ := provenance.ParseDomainSource(spec.LockPath)
	existingLock, err := crudbuild.ParseLock(lockDomain, lockBytes)
	if err != nil {
		t.Fatal(err)
	}
	lockRef, _ := provenance.RepositoryRef(spec.LockPath, "")
	lockSource := provenance.Source{Ref: lockRef, Digest: provenance.SHA256(lockBytes)}
	manifestRef, _ := provenance.RepositoryRef(manifestPath, "")
	manifestSource := provenance.Source{Ref: manifestRef, Digest: provenance.SHA256(firstManifestJSON)}

	spec.RequestDigest = provenance.SHA256([]byte("request-with-published-baseline"))
	spec.ExistingLock = &existingLock
	spec.ExistingLockSource = &lockSource
	spec.PublishedArtifact = &crudbuild.PublishedArtifact{ID: "crud-proto.accounts", Digest: firstBuild.ProtoDigest(), ManifestSource: manifestSource}
	secondBuild, err := crudbuild.BuildPlan(document, spec)
	if err != nil {
		t.Fatal(err)
	}
	secondVerified, err := verifiedEntGraphPlanFromBuild(secondBuild)
	if err != nil {
		t.Fatal(err)
	}
	_, secondControls, err := secondVerified.TransactionInputs(func(string, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(secondControls) != 0 {
		t.Fatalf("unchanged compatibility lock produced %d mutations", len(secondControls))
	}
	second, err := buildProjectionTransaction(t, repositoryPath, secondVerified, secondBuild.Sources(), &firstManifest, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Changes()) != 0 || len(second.Conflicts()) != 0 {
		t.Fatalf("repeated generation changes=%#v conflicts=%#v", second.Changes(), second.Conflicts())
	}
	if !bytes.Contains(second.CanonicalJSON(), []byte(lockSource.Ref.String())) || !bytes.Contains(second.CanonicalJSON(), []byte(manifestSource.Ref.String())) {
		t.Fatal("transaction plan lost compatibility control sources")
	}
	secondManifest, ok := second.NextManifest()
	if !ok {
		t.Fatal("second next manifest is absent")
	}
	secondManifestJSON, err := secondManifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(secondManifestJSON, firstManifestJSON) {
		t.Fatalf("Artifact Manifest drifted on identical generation:\nfirst:  %s\nsecond: %s", firstManifestJSON, secondManifestJSON)
	}
}

func TestTenantProtoOwnershipSupportsFirstRepeatAndStaleProbe(t *testing.T) {
	repositoryPath := t.TempDir()
	document := projectionTenantEntityDocument(t)
	manifestPath := ".nexa/generation/crud-proto.accounts.manifest.json"
	spec := crudbuild.Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("tenant-request")), MultiTenant: crudbuild.MultiTenantConfig{Enabled: true},
	}
	firstBuild, err := crudbuild.BuildPlan(document, spec)
	if err != nil {
		t.Fatal(err)
	}
	firstVerified, err := verifiedEntGraphPlanFromBuild(firstBuild)
	if err != nil {
		t.Fatal(err)
	}
	first, err := buildProjectionTransaction(t, repositoryPath, firstVerified, firstBuild.Sources(), nil, manifestPath)
	if err != nil || len(first.Conflicts()) != 0 {
		t.Fatalf("first plan conflicts=%#v error=%v", first.Conflicts(), err)
	}
	if _, err := transaction.Write(context.Background(), first, repositoryPath, transaction.WriteOptions{PlanDigest: first.PlanDigest()}); err != nil {
		t.Fatal(err)
	}
	previous, ok := first.NextManifest()
	if !ok {
		t.Fatal("first manifest missing")
	}

	lockBytes, err := os.ReadFile(filepath.Join(repositoryPath, spec.LockPath))
	if err != nil {
		t.Fatal(err)
	}
	lockSourceName, _ := provenance.ParseDomainSource(spec.LockPath)
	lock, err := crudbuild.ParseLock(lockSourceName, lockBytes)
	if err != nil {
		t.Fatal(err)
	}
	lockRef, _ := provenance.RepositoryRef(spec.LockPath, "")
	manifestJSON, _ := previous.CanonicalJSON()
	manifestRef, _ := provenance.RepositoryRef(manifestPath, "")
	spec.ExistingLock = &lock
	spec.ExistingLockSource = &provenance.Source{Ref: lockRef, Digest: provenance.SHA256(lockBytes)}
	spec.PublishedArtifact = &crudbuild.PublishedArtifact{ID: "crud-proto.accounts", Digest: firstBuild.ProtoDigest(), ManifestSource: provenance.Source{Ref: manifestRef, Digest: provenance.SHA256(manifestJSON)}}
	repeatBuild, err := crudbuild.BuildPlan(document, spec)
	if err != nil {
		t.Fatal(err)
	}
	repeatVerified, err := verifiedEntGraphPlanFromBuild(repeatBuild)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := buildProjectionTransaction(t, repositoryPath, repeatVerified, repeatBuild.Sources(), &previous, manifestPath)
	if err != nil || len(repeat.Conflicts()) != 0 {
		t.Fatalf("repeat conflicts=%#v error=%v", repeat.Conflicts(), err)
	}
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	checked, err := transaction.Check(repeat, root)
	if err != nil || len(checked.Conflicts()) != 0 {
		t.Fatalf("repeat check conflicts=%#v error=%v", checked.Conflicts(), err)
	}
	if _, err := transaction.Write(context.Background(), repeat, repositoryPath, transaction.WriteOptions{PlanDigest: repeat.PlanDigest()}); err != nil {
		t.Fatal(err)
	}

	probes, err := repeatVerified.StaleOwnershipProbes()
	if err != nil || len(probes) != 1 {
		t.Fatalf("stale probes=%d error=%v", len(probes), err)
	}
	owned, err := probes[0].Inspect(spec.ProtoArtifactPath, repeatBuild.ProtoBytes(), transaction.Ownership{GeneratorID: "crud-proto", ArtifactID: "crud-proto.accounts", InputDigest: provenance.SHA256([]byte("input"))})
	if err != nil || !owned {
		t.Fatalf("tenant stale ownership=%v error=%v", owned, err)
	}
}

func TestVerifiedPlanWithoutCRUDProjectsNoTransactionInputs(t *testing.T) {
	plan, err := crudbuild.BuildPlan(projectionEntityDocument(t, false), crudbuild.Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("request")),
	})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifiedEntGraphPlanFromBuild(plan)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, controls, err := verified.TransactionInputs(nil)
	if err != nil || verified.HasCRUD() || len(artifacts) != 0 || len(controls) != 0 {
		t.Fatalf("absence projection = crud:%v artifacts:%d controls:%d error:%v", verified.HasCRUD(), len(artifacts), len(controls), err)
	}
	probes, err := verified.StaleOwnershipProbes()
	if err != nil || len(probes) != 1 {
		t.Fatalf("stale probes = %d, error:%v", len(probes), err)
	}
	owned, err := probes[0].Inspect(
		"api/accounts.crud.generated.proto",
		[]byte("syntax = \"proto3\";\npackage acme.accounts.v1;\n"),
		transaction.Ownership{GeneratorID: "crud-proto", ArtifactID: "crud-proto.accounts", InputDigest: provenance.SHA256([]byte("previous-input"))},
	)
	if err != nil || !owned {
		t.Fatalf("stale probe ownership = %v, error:%v", owned, err)
	}
	probes[0] = nil
	second, err := verified.StaleOwnershipProbes()
	if err != nil || len(second) != 1 || second[0] == nil {
		t.Fatalf("stale probe projection retained caller slice: %#v, %v", second, err)
	}
}

func buildProjectionTransaction(t *testing.T, repository string, projection EntGraphPlan, sources []provenance.Source, previous *artifact.Manifest, manifestPath string) (transaction.Plan, error) {
	t.Helper()
	return transaction.Build(context.Background(), repository, func(_ string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		expected, controls, err := projection.TransactionInputs(emit)
		if err != nil {
			return transaction.PlanRequest{}, err
		}
		return transaction.PlanRequest{
			Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"}, Sources: sources,
			Expected: expected, ControlSources: controls, Previous: previous, ManifestPath: manifestPath,
			RevalidateSources: func(context.Context) ([]provenance.Source, error) { return sources, nil },
		}, nil
	})
}

func projectionEntityDocument(t *testing.T, withCRUD bool) entity.Document {
	t.Helper()
	schemaRef, _ := provenance.RepositoryRef("ent/schema/account.go", "schema:Account")
	projection := entityvalue.EntityProjection{
		Name: "Account", SourceRef: schemaRef,
		Meta: nexaent.SchemaMeta{
			Label:       nexaent.LocalizedText{Key: "account.label", ZhCN: "Account", EnUS: "Account"},
			Description: nexaent.LocalizedText{Key: "account.description", ZhCN: "Account record", EnUS: "Account record"},
			Identity:    nexaent.IdentityEntID, Scope: nexaent.ScopeTenant,
		},
		Identity: entityvalue.IdentityProjection{Kind: string(entity.IdentityImplicit), Name: "id", Type: string(entity.ScalarInt64)},
	}
	if withCRUD {
		encoded, err := nexaent.CRUD(nexaent.CRUDGet).CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		crud, err := nexaent.DecodeCRUD(encoded)
		if err != nil {
			t.Fatal(err)
		}
		projection.CRUD = &crud
	}
	value, err := entityvalue.NewDocument(entityvalue.Projection{Entities: []entityvalue.EntityProjection{projection}})
	if err != nil {
		t.Fatal(err)
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func projectionTenantEntityDocument(t *testing.T) entity.Document {
	t.Helper()
	schemaRef, _ := provenance.RepositoryRef("ent/schema/account.go", "schema:Account")
	tenantRef, _ := provenance.RepositoryRef("ent/schema/account.go", "schema:Account/field:tenant_id")
	raw, err := nexaent.CRUD(nexaent.CRUDGet).CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	crud, err := nexaent.DecodeCRUD(raw)
	if err != nil {
		t.Fatal(err)
	}
	value, err := entityvalue.NewDocument(entityvalue.Projection{Entities: []entityvalue.EntityProjection{{
		Name: "Account", SourceRef: schemaRef, CRUD: &crud,
		Meta:     nexaent.SchemaMeta{Label: nexaent.LocalizedText{Key: "account.label", ZhCN: "Account", EnUS: "Account"}, Description: nexaent.LocalizedText{Key: "account.description", ZhCN: "Account", EnUS: "Account"}, Identity: nexaent.IdentityEntID, Scope: nexaent.ScopeTenant},
		Identity: entityvalue.IdentityProjection{Kind: string(entity.IdentityImplicit), Name: "id", Type: string(entity.ScalarInt64)},
		Fields:   []entityvalue.FieldProjection{{Name: "tenant_id", SourceRef: tenantRef, Type: string(entity.ScalarInt64), Immutable: true, IsTenantField: true, Meta: nexaent.FieldMeta{Label: nexaent.LocalizedText{Key: "account.tenant_id.label", ZhCN: "Tenant", EnUS: "Tenant"}, Description: nexaent.LocalizedText{Key: "account.tenant_id.description", ZhCN: "Tenant", EnUS: "Tenant"}, UIHint: nexaent.UIHintReadonly, Visibility: nexaent.VisibilityInternal}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

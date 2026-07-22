package crudbuild

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/provenance"
)

func TestTenantMarkerMethodRPCContextAndV2CanonicalRoundTrip(t *testing.T) {
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
	plan, err := BuildPlan(document, Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("request")),
		MultiTenant: MultiTenantConfig{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical := plan.CRUDSnapshot()
	snapshot, err := ParseSnapshot(testPlanSource(t), canonical)
	if err != nil {
		t.Fatal(err)
	}
	if APIVersion != "nexa.dev/crud-protocol-ir/v2" || !snapshot.HasTenantEntities() {
		t.Fatalf("snapshot version=%q tenant=%v", APIVersion, snapshot.HasTenantEntities())
	}
	if roundTrip := snapshot.CanonicalJSON(); !bytes.Equal(roundTrip, canonical) {
		t.Fatalf("round trip changed canonical snapshot\nwant=%s\ngot=%s", canonical, roundTrip)
	}
	for _, service := range snapshot.Services() {
		for _, method := range service.Methods() {
			bindings := method.RPCContext().ContextFields()
			if len(bindings) != 1 || bindings[0].Source() != ContextTenantID || bindings[0].RPCField() != "tenant_id" {
				t.Fatalf("method %s context = %#v", method.Name(), bindings)
			}
		}
	}

	var broken map[string]any
	if err := json.Unmarshal(canonical, &broken); err != nil {
		t.Fatal(err)
	}
	broken["tenantEntities"] = []any{}
	brokenCanonical, err := canonicalJSON(broken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSnapshot(testPlanSource(t), brokenCanonical); err == nil {
		t.Fatal("snapshot accepted tenant RPC context without tenant entity marker")
	}
}

func TestV2SnapshotRejectsNullCollectionsAndBrokenTenantClosure(t *testing.T) {
	tenant := tenantSnapshotCanonical(t)
	ordinaryPlan, err := BuildPlan(planEntityDocument(t, true), Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("ordinary")),
	})
	if err != nil {
		t.Fatal(err)
	}
	ordinary := ordinaryPlan.CRUDSnapshot()
	tests := []struct {
		name   string
		base   []byte
		mutate func(map[string]any)
	}{
		{name: "tenant entities null", base: tenant, mutate: func(root map[string]any) { root["tenantEntities"] = nil }},
		{name: "context fields null", base: ordinary, mutate: func(root map[string]any) {
			root["services"].([]any)[0].(map[string]any)["methods"].([]any)[0].(map[string]any)["rpcContext"].(map[string]any)["contextFields"] = nil
		}},
		{name: "options import missing", base: tenant, mutate: func(root map[string]any) {
			imports := root["imports"].([]any)
			kept := make([]any, 0, len(imports))
			for _, value := range imports {
				if value != "nexa/protocol/v1/options.proto" {
					kept = append(kept, value)
				}
			}
			root["imports"] = kept
		}},
		{name: "orphan tenant context field", base: tenant, mutate: func(root map[string]any) {
			root["tenantEntities"] = []any{}
			for _, service := range root["services"].([]any) {
				for _, method := range service.(map[string]any)["methods"].([]any) {
					method.(map[string]any)["rpcContext"].(map[string]any)["contextFields"] = []any{}
				}
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal(test.base, &root); err != nil {
				t.Fatal(err)
			}
			test.mutate(root)
			broken, err := canonicalJSON(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseSnapshot(testPlanSource(t), broken); err == nil {
				t.Fatal("broken v2 snapshot accepted")
			}
		})
	}
}

func tenantSnapshotCanonical(t *testing.T) []byte {
	t.Helper()
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
	plan, err := BuildPlan(document, Spec{
		ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("tenant")), MultiTenant: MultiTenantConfig{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan.CRUDSnapshot()
}

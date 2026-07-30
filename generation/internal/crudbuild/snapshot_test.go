package crudbuild

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/provenance"
)

func TestTenantMarkerDoesNotInjectRPCContextIntoV2Protocol(t *testing.T) {
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
	for _, message := range snapshot.Messages() {
		for _, field := range message.Fields() {
			if field.Name() == "tenant_id" {
				t.Fatalf("message %s contains injected tenant_id", message.Name())
			}
		}
	}
	compilePlanProto(t, plan.ProtoPath(), plan.ProtoBytes())

	var root map[string]any
	if err := json.Unmarshal(canonical, &root); err != nil {
		t.Fatal(err)
	}
	for _, service := range root["services"].([]any) {
		for _, method := range service.(map[string]any)["methods"].([]any) {
			if _, exists := method.(map[string]any)["rpcContext"]; exists {
				t.Fatal("canonical method contains rpcContext")
			}
		}
	}
	for _, message := range root["messages"].([]any) {
		for _, field := range message.(map[string]any)["fields"].([]any) {
			field := field.(map[string]any)
			if _, exists := field["tenantContext"]; exists {
				t.Fatal("canonical field contains tenantContext")
			}
			if _, exists := field["internal"]; exists {
				t.Fatal("canonical field contains internal context marker")
			}
		}
	}
}

func TestListResponseContainsExactItemsAndTotal(t *testing.T) {
	projection := planEntityProjection(t, true, true)
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
		ProtoArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json", RequestDigest: provenance.SHA256([]byte("list-response")),
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ParseSnapshot(testPlanSource(t), plan.CRUDSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	var response Message
	for _, message := range snapshot.Messages() {
		if message.Name() == "ListAccountResponse" {
			response = message
			break
		}
	}
	if response.Name() == "" {
		t.Fatal("ListAccountResponse is missing")
	}
	fields := response.Fields()
	if len(fields) != 2 || fields[0].Name() != "items" || fields[1].Name() != "total" {
		t.Fatalf("ListAccountResponse fields = %#v; want exact items,total", fields)
	}
}

func TestV2SnapshotRejectsNullCollectionsAndRemovedContextFields(t *testing.T) {
	tenant := tenantSnapshotCanonical(t)
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "tenant entities null", mutate: func(root map[string]any) { root["tenantEntities"] = nil }},
		{name: "rpc context", mutate: func(root map[string]any) {
			root["services"].([]any)[0].(map[string]any)["methods"].([]any)[0].(map[string]any)["rpcContext"] = map[string]any{"contextFields": []any{}}
		}},
		{name: "context fields", mutate: func(root map[string]any) {
			root["services"].([]any)[0].(map[string]any)["methods"].([]any)[0].(map[string]any)["contextFields"] = []any{}
		}},
		{name: "tenant context", mutate: func(root map[string]any) {
			root["messages"].([]any)[0].(map[string]any)["fields"].([]any)[0].(map[string]any)["tenantContext"] = false
		}},
		{name: "internal context marker", mutate: func(root map[string]any) {
			root["messages"].([]any)[0].(map[string]any)["fields"].([]any)[0].(map[string]any)["internal"] = false
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal(tenant, &root); err != nil {
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

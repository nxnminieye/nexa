package crudproto_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestCRUDProtocolCanonicalSnapshotSchemasAndDefensiveCopies(t *testing.T) {
	document := buildEntityDocument(t, accountProjection(t, nexaent.AllCRUDOperations(), accountFields(t)...))
	protocol, proposal, err := crudproto.Build(document, crudproto.BuildOptions{
		ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1",
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := crudproto.CanonicalJSON(protocol)
	if err != nil {
		t.Fatal(err)
	}
	validateSchemaDocument(t, crudproto.IRSchema(), canonical)
	if protocol.APIVersion() != "nexa.dev/crud-protocol-ir/v2" || bytes.Contains(crudproto.IRSchema(), []byte("crud-protocol-ir/v1")) {
		t.Fatalf("current CRUD IR identity = %q", protocol.APIVersion())
	}
	lockJSON, err := crudproto.CanonicalLockJSON(proposal.After())
	if err != nil {
		t.Fatal(err)
	}
	validateSchemaDocument(t, crudproto.LockSchema(), lockJSON)

	source, _ := provenance.ParseDomainSource("quality/crud-protocol-ir.json")
	snapshot, err := crudproto.ParseSnapshot(source, canonical)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(roundTrip, canonical) {
		t.Fatalf("snapshot canonical = %s, %v", roundTrip, err)
	}
	messages := snapshot.Messages()
	messages[0] = crudproto.Message{}
	second, _ := snapshot.CanonicalJSON()
	if !bytes.Equal(second, canonical) {
		t.Fatal("snapshot accessors aliased immutable state")
	}
	canonical[0] = '!'
	third, _ := crudproto.CanonicalJSON(protocol)
	if len(third) == 0 || third[0] == '!' {
		t.Fatal("CanonicalJSON returned aliased bytes")
	}
	imports := protocol.Imports()
	imports[0] = "changed.proto"
	if protocol.Imports()[0] == "changed.proto" {
		t.Fatal("Imports returned aliased slice")
	}
	reserved := proposal.After().Message("schema:Account/message:Account").ReservedNumbers()
	if len(reserved) > 0 {
		reserved[0] = 999
		if proposal.After().Message("schema:Account/message:Account").ReservedNumbers()[0] == 999 {
			t.Fatal("lock reservations were aliased")
		}
	}
}

func TestCRUDProtocolZeroValuesRemainInvalidReadbacks(t *testing.T) {
	if _, err := crudproto.CanonicalJSON(crudproto.Document{}); err == nil {
		t.Fatal("zero Document canonicalized")
	}
	if _, err := crudproto.CanonicalLockJSON(crudproto.Lock{}); err == nil {
		t.Fatal("zero Lock canonicalized")
	}
	if _, err := (crudproto.Snapshot{}).CanonicalJSON(); err == nil {
		t.Fatal("zero Snapshot canonicalized")
	}
	if (crudproto.Lock{}).APIVersion() != "" || len((crudproto.Document{}).Messages()) != 0 {
		t.Fatal("zero value exposed protocol state")
	}
}

func TestStrictReadbacksRejectIncompleteOrBrokenCanonicalDocuments(t *testing.T) {
	document := buildEntityDocument(t, accountProjection(t, []nexaent.CRUDOperation{nexaent.CRUDGet}, accountFields(t)...))
	protocol, proposal, err := crudproto.Build(document, crudproto.BuildOptions{ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1"})
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := crudproto.CanonicalJSON(protocol)
	lockJSON, _ := crudproto.CanonicalLockJSON(proposal.After())
	lockSource, _ := provenance.ParseDomainSource("proto/identity.lock.json")
	var lockValue map[string]any
	json.Unmarshal(lockJSON, &lockValue)
	lockValue["schemas"] = nil
	if _, err := crudproto.ParseLock(lockSource, canonicalizeTestJSON(t, lockValue)); err == nil {
		t.Fatal("schemas:null lock accepted")
	}

	source, _ := provenance.ParseDomainSource("quality/crud.json")
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "messages null", mutate: func(value map[string]any) { value["messages"] = nil }},
		{name: "broken method", mutate: func(value map[string]any) {
			value["services"].([]any)[0].(map[string]any)["methods"].([]any)[0].(map[string]any)["input"] = "MissingRequest"
		}},
		{name: "duplicate symbol", mutate: func(value map[string]any) {
			messages := value["messages"].([]any)
			messages[1].(map[string]any)["name"] = messages[0].(map[string]any)["name"]
		}},
		{name: "illegal number", mutate: func(value map[string]any) {
			value["messages"].([]any)[0].(map[string]any)["fields"].([]any)[0].(map[string]any)["number"] = float64(19000)
		}},
		{name: "source missing", mutate: func(value map[string]any) { sources := value["sources"].([]any); value["sources"] = sources[1:] }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(canonical, &value); err != nil {
				t.Fatal(err)
			}
			test.mutate(value)
			if _, err := crudproto.ParseSnapshot(source, canonicalizeTestJSON(t, value)); err == nil {
				t.Fatal("broken snapshot accepted")
			}
		})
	}
	if _, err := crudproto.ParseSnapshot(source, append(append([]byte(nil), canonical...), '\n')); err == nil {
		t.Fatal("noncanonical snapshot accepted")
	}
	if _, err := crudproto.ParseLock(lockSource, append(append([]byte(nil), lockJSON...), '\n')); err == nil {
		t.Fatal("noncanonical lock accepted")
	}
}

func TestPublicTenantSnapshotSchemaAndParserRejectNullCollections(t *testing.T) {
	tenant, _, err := crudproto.Build(
		buildEntityDocument(t, accountProjection(t, nexaent.AllCRUDOperations(), append(accountFields(t), tenantField(t))...)),
		crudproto.BuildOptions{
			ServiceID:    "identity",
			ProtoPackage: "identity.v1",
			GoPackage:    "example.com/identity/gen;identityv1",
			MultiTenant:  crudproto.MultiTenantConfig{Enabled: true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := crudproto.CanonicalJSON(tenant)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := provenance.ParseDomainSource("quality/tenant-crud.json")
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "tenant entities null", mutate: func(value map[string]any) { value["tenantEntities"] = nil }},
		{name: "context fields null", mutate: func(value map[string]any) {
			service := value["services"].([]any)[0].(map[string]any)
			method := service["methods"].([]any)[0].(map[string]any)
			method["rpcContext"].(map[string]any)["contextFields"] = nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(canonical, &value); err != nil {
				t.Fatal(err)
			}
			test.mutate(value)
			broken := canonicalizeTestJSON(t, value)
			if schemaAccepts(t, crudproto.IRSchema(), broken) {
				t.Fatal("public IR schema accepted null collection")
			}
			if _, err := crudproto.ParseSnapshot(source, broken); err == nil {
				t.Fatal("public snapshot parser accepted null collection")
			}
		})
	}
}

func canonicalizeTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func validateSchemaDocument(t *testing.T, schemaBytes, document []byte) {
	t.Helper()
	if !schemaAccepts(t, schemaBytes, document) {
		t.Fatalf("schema validation failed:\n%s", document)
	}
}

func schemaAccepts(t *testing.T, schemaBytes, document []byte) bool {
	t.Helper()
	var schemaDocument, value any
	if err := json.Unmarshal(schemaBytes, &schemaDocument); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "https://nexa.dev/test-schema.json"
	if err := compiler.AddResource(schemaURL, schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	return compiled.Validate(value) == nil
}

package crudproto_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestCompatibilityLockPreservesWireHistory(t *testing.T) {
	options := crudproto.BuildOptions{ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1"}
	initialDocument := buildEntityDocument(t, accountProjection(t, nexaent.AllCRUDOperations(), accountFields(t)...))
	initialProtocol, initial, err := crudproto.Build(initialDocument, options)
	if err != nil {
		t.Fatal(err)
	}
	initialNumbers := messageNumbers(t, initialProtocol, "Account")
	lock := initial.After()

	reorderedFields := accountFields(t)
	for left, right := 0, len(reorderedFields)-1; left < right; left, right = left+1, right-1 {
		reorderedFields[left], reorderedFields[right] = reorderedFields[right], reorderedFields[left]
	}
	reorderedDocument := buildEntityDocument(t, accountProjection(t, nexaent.AllCRUDOperations(), reorderedFields...))
	options.ExistingLock = &lock
	reorderedProtocol, reordered, err := crudproto.Build(reorderedDocument, options)
	if err != nil {
		t.Fatal(err)
	}
	if reordered.Changed() {
		t.Fatal("field reorder changed compatibility lock")
	}
	assertNumbersEqual(t, initialNumbers, messageNumbers(t, reorderedProtocol, "Account"))

	withoutName := accountFields(t)
	withoutName = removeProjection(withoutName, "name")
	removedDocument := buildEntityDocument(t, accountProjection(t, nexaent.AllCRUDOperations(), withoutName...))
	removedProtocol, removed, err := crudproto.Build(removedDocument, options)
	if err != nil {
		t.Fatal(err)
	}
	if !removed.Changed() || !containsInt(removed.After().Message("schema:Account/message:Account").ReservedNumbers(), initialNumbers["name"]) {
		t.Fatal("removed field number was not reserved")
	}
	if !containsString(removed.After().Message("schema:Account/message:Account").ReservedNames(), "name") {
		t.Fatal("removed field name was not reserved")
	}
	if _, exists := messageNumbers(t, removedProtocol, "Account")["name"]; exists {
		t.Fatal("removed field remained in current protocol")
	}
	removedProto, err := crudproto.Render(removedProtocol)
	if err != nil {
		t.Fatal(err)
	}
	removedDescriptor := compileProto(t, "identity.crud.generated.proto", removedProto).Messages().ByName("Account")
	if !removedDescriptor.ReservedNames().Has("name") || !removedDescriptor.ReservedRanges().Has(protoreflect.FieldNumber(initialNumbers["name"])) {
		t.Fatal("rendered descriptor did not preserve retired name and number")
	}

	renamedFields := append(removeProjection(accountFields(t), "name"), entityvalue.FieldProjection{
		Name: "display_name", SourceRef: mustRef(t, "ent/schema/account.go", "schema:Account/field:display_name"), Type: string(entity.ScalarString),
		Meta: fieldMeta("account.display_name", nexaent.VisibilityPublic, nexaent.ReadInclude, nexaent.MutationCreateUpdate),
	})
	renamedLock := removed.After()
	options.ExistingLock = &renamedLock
	renamedProtocol, renamed, err := crudproto.Build(buildEntityDocument(t, accountProjection(t, nexaent.AllCRUDOperations(), renamedFields...)), options)
	if err != nil {
		t.Fatal(err)
	}
	if got := messageNumbers(t, renamedProtocol, "Account")["display_name"]; got == initialNumbers["name"] {
		t.Fatal("rename reused retired field number")
	}
	if !containsInt(renamed.After().Message("schema:Account/message:Account").ReservedNumbers(), initialNumbers["name"]) {
		t.Fatal("rename dropped prior reservation")
	}
}

func TestCompatibilityLockRejectsWireTypeChangeAndReactivatesOperations(t *testing.T) {
	options := crudproto.BuildOptions{ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1"}
	all := buildEntityDocument(t, accountProjection(t, nexaent.AllCRUDOperations(), accountFields(t)...))
	_, initial, err := crudproto.Build(all, options)
	if err != nil {
		t.Fatal(err)
	}
	initialLock := initial.After()
	options.ExistingLock = &initialLock

	getOnly := buildEntityDocument(t, accountProjection(t, []nexaent.CRUDOperation{nexaent.CRUDGet}, accountFields(t)...))
	_, reduced, err := crudproto.Build(getOnly, options)
	if err != nil {
		t.Fatal(err)
	}
	if !reduced.Changed() {
		t.Fatal("operation removal did not update wire history")
	}
	reducedLock := reduced.After()
	options.ExistingLock = &reducedLock
	_, restored, err := crudproto.Build(all, options)
	if err != nil {
		t.Fatal(err)
	}
	initialJSON, _ := crudproto.CanonicalLockJSON(initialLock)
	restoredJSON, _ := crudproto.CanonicalLockJSON(restored.After())
	if !bytes.Equal(initialJSON, restoredJSON) {
		t.Fatalf("operation re-add did not restore assignments\ninitial: %s\nrestored: %s", initialJSON, restoredJSON)
	}

	compatibleFields := accountFields(t)
	for index := range compatibleFields {
		if compatibleFields[index].Name == "payload" {
			compatibleFields[index].Nillable = false
		}
	}
	if _, _, err := crudproto.Build(buildEntityDocument(t, accountProjection(t, nexaent.AllCRUDOperations(), compatibleFields...)), crudproto.BuildOptions{
		ServiceID: options.ServiceID, ProtoPackage: options.ProtoPackage, GoPackage: options.GoPackage, ExistingLock: &initialLock,
	}); err != nil {
		t.Fatalf("compatible optionality change failed: %v", err)
	}

	changedFields := accountFields(t)
	for index := range changedFields {
		if changedFields[index].Name == "name" {
			changedFields[index].Type = string(entity.ScalarInt64)
		}
	}
	_, _, err = crudproto.Build(buildEntityDocument(t, accountProjection(t, nexaent.AllCRUDOperations(), changedFields...)), crudproto.BuildOptions{
		ServiceID: options.ServiceID, ProtoPackage: options.ProtoPackage, GoPackage: options.GoPackage, ExistingLock: &initialLock,
	})
	owner, ok := err.(*crudproto.Error)
	if !ok || owner.Code() != "crud_compatibility_failed" || owner.Stage() != "compatibility" || owner.Reason() != "wire_incompatible" {
		t.Fatalf("type change error = %#v", err)
	}
}

func TestLockCanonicalRoundTripAndStrictValidation(t *testing.T) {
	document := buildEntityDocument(t, accountProjection(t, []nexaent.CRUDOperation{nexaent.CRUDGet}, accountFields(t)...))
	_, proposal, err := crudproto.Build(document, crudproto.BuildOptions{
		ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1",
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := crudproto.CanonicalLockJSON(proposal.After())
	if err != nil {
		t.Fatal(err)
	}
	source, _ := provenance.ParseDomainSource("proto/identity.crud-protocol.lock.json")
	parsed, err := crudproto.ParseLock(source, canonical)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, _ := crudproto.CanonicalLockJSON(parsed)
	if !bytes.Equal(canonical, roundTrip) {
		t.Fatal("lock canonical round trip changed bytes")
	}

	var value map[string]any
	if err := json.Unmarshal(canonical, &value); err != nil {
		t.Fatal(err)
	}
	value["serviceId"] = "other"
	tampered, _ := json.Marshal(value)
	tamperedLock, err := crudproto.ParseLock(source, tampered)
	if err != nil {
		t.Fatal("a different valid service identity must remain a valid standalone lock")
	}
	if _, _, err := crudproto.Build(document, crudproto.BuildOptions{
		ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1", ExistingLock: &tamperedLock,
	}); err == nil {
		t.Fatal("mismatched lock service was accepted by Build")
	} else if owner, ok := err.(*crudproto.Error); !ok || owner.Reason() != "lock_service_mismatch" {
		t.Fatalf("mismatched lock error = %#v", err)
	}
	duplicate := append([]byte(`{"apiVersion":"nexa.dev/crud-protocol-lock/v1","apiVersion":"nexa.dev/crud-protocol-lock/v1"}`), '\n')
	if _, err := crudproto.ParseLock(source, duplicate); err == nil {
		t.Fatal("duplicate lock member was accepted")
	} else if owner, ok := err.(*crudproto.Error); !ok || owner.Code() != "crud_lock_invalid" || owner.Stage() != "lock-decode" || owner.Source() != source.String() {
		t.Fatalf("duplicate lock error = %#v", err)
	}

	schemaA, schemaB := crudproto.IRSchema(), crudproto.IRSchema()
	schemaA[0] = '!'
	if len(schemaB) == 0 || schemaB[0] == '!' {
		t.Fatal("IRSchema returned aliased bytes")
	}
	lockSchemaA, lockSchemaB := crudproto.LockSchema(), crudproto.LockSchema()
	lockSchemaA[0] = '!'
	if len(lockSchemaB) == 0 || lockSchemaB[0] == '!' {
		t.Fatal("LockSchema returned aliased bytes")
	}
}

func TestEnumCompatibilityLockPreservesValueHistory(t *testing.T) {
	options := crudproto.BuildOptions{ServiceID: "identity", ProtoPackage: "identity.v1", GoPackage: "example.com/identity/gen;identityv1"}
	initialFields := accountFields(t)
	initial := buildEntityDocument(t, accountProjection(t, []nexaent.CRUDOperation{nexaent.CRUDGet}, initialFields...))
	initialProtocol, initialProposal, err := crudproto.Build(initial, options)
	if err != nil {
		t.Fatal(err)
	}
	initialDescriptor := compileProto(t, "identity.crud.generated.proto", mustRender(t, initialProtocol)).Enums().ByName("AccountStatus")
	activeNumber := initialDescriptor.Values().ByName("ACCOUNT_STATUS_ACTIVE").Number()
	disabledNumber := initialDescriptor.Values().ByName("ACCOUNT_STATUS_DISABLED").Number()
	lock := initialProposal.After()
	options.ExistingLock = &lock

	addedFields := accountFields(t)
	for index := range addedFields {
		if addedFields[index].Name == "status" {
			addedFields[index].EnumValues = []entityvalue.EnumValue{{Name: "archived", Value: "archived"}, {Name: "disabled", Value: "disabled"}, {Name: "active", Value: "active"}}
		}
	}
	addedProtocol, addedProposal, err := crudproto.Build(buildEntityDocument(t, accountProjection(t, []nexaent.CRUDOperation{nexaent.CRUDGet}, addedFields...)), options)
	if err != nil {
		t.Fatal(err)
	}
	addedDescriptor := compileProto(t, "identity.crud.generated.proto", mustRender(t, addedProtocol)).Enums().ByName("AccountStatus")
	if addedDescriptor.Values().ByName("ACCOUNT_STATUS_ACTIVE").Number() != activeNumber || addedDescriptor.Values().ByName("ACCOUNT_STATUS_DISABLED").Number() != disabledNumber {
		t.Fatal("enum insertion renumbered existing values")
	}
	if addedDescriptor.Values().ByName("ACCOUNT_STATUS_ARCHIVED").Number() <= disabledNumber {
		t.Fatal("new enum value did not receive the next legal number")
	}

	removedFields := accountFields(t)
	for index := range removedFields {
		if removedFields[index].Name == "status" {
			removedFields[index].EnumValues = []entityvalue.EnumValue{{Name: "disabled", Value: "disabled"}}
		}
	}
	addedLock := addedProposal.After()
	options.ExistingLock = &addedLock
	removedProtocol, _, err := crudproto.Build(buildEntityDocument(t, accountProjection(t, []nexaent.CRUDOperation{nexaent.CRUDGet}, removedFields...)), options)
	if err != nil {
		t.Fatal(err)
	}
	removedDescriptor := compileProto(t, "identity.crud.generated.proto", mustRender(t, removedProtocol)).Enums().ByName("AccountStatus")
	if !removedDescriptor.ReservedNames().Has("ACCOUNT_STATUS_ACTIVE") || !removedDescriptor.ReservedRanges().Has(activeNumber) {
		t.Fatal("removed enum value was not reserved")
	}

	changedFields := accountFields(t)
	for index := range changedFields {
		if changedFields[index].Name == "status" {
			changedFields[index].EnumValues = []entityvalue.EnumValue{{Name: "active", Value: "enabled"}, {Name: "disabled", Value: "disabled"}}
		}
	}
	options.ExistingLock = &lock
	_, _, err = crudproto.Build(buildEntityDocument(t, accountProjection(t, []nexaent.CRUDOperation{nexaent.CRUDGet}, changedFields...)), options)
	if owner, ok := err.(*crudproto.Error); !ok || owner.Reason() != "wire_incompatible" {
		t.Fatalf("enum semantic change error = %#v", err)
	}
}

func messageNumbers(t *testing.T, document crudproto.Document, messageName string) map[string]int32 {
	t.Helper()
	message, ok := document.Message(messageName)
	if !ok {
		t.Fatalf("message %s missing", messageName)
	}
	result := make(map[string]int32)
	for _, field := range message.Fields() {
		result[field.Name()] = field.Number()
	}
	return result
}

func assertNumbersEqual(t *testing.T, left, right map[string]int32) {
	t.Helper()
	if len(left) != len(right) {
		t.Fatalf("number maps differ: %#v %#v", left, right)
	}
	for name, number := range left {
		if right[name] != number {
			t.Fatalf("number for %s = %d, want %d", name, right[name], number)
		}
	}
}

func removeProjection(fields []entityvalue.FieldProjection, name string) []entityvalue.FieldProjection {
	result := make([]entityvalue.FieldProjection, 0, len(fields))
	for _, field := range fields {
		if field.Name != name {
			result = append(result, field)
		}
	}
	return result
}

func containsInt(values []int32, wanted int32) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

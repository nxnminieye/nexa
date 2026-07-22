package crudlogic

import "testing"

func TestProtoGoPackageMapsFrameworkOptionsToTargetGoPackage(t *testing.T) {
	const name = "rpc/target.proto"
	const source = `syntax = "proto3";
package fixture.v1;
option go_package = "test/domain;pb";
import "nexa/protocol/v1/options.proto";
message Request {}
`
	importPath, packageName, names, err := protoGoPackage(name, []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if importPath != "test/domain" || packageName != "pb" {
		t.Fatalf("Go package = %q %q", importPath, packageName)
	}
	if message, ok := names.message("Request"); !ok || message.goName != "Request" {
		t.Fatalf("message Request = %#v, %v", message, ok)
	}
}

func TestProtoGoPackageUsesProtogenMessageFieldNames(t *testing.T) {
	const name = "rpc/collision.proto"
	const source = `syntax = "proto3";
package fixture.v1;
option go_package = "example.com/fixture/pb;fixturepb";
enum CollisionState {
  COLLISION_STATE_UNSPECIFIED = 0;
  COLLISION_STATE_ACTIVE = 1;
}
message Collision {
  string reset = 1;
  string string = 2;
  string descriptor = 3;
  string proto_message = 4;
  string marshal = 5;
  string unmarshal = 6;
  string extension_range_array = 7;
  string extension_map = 8;
  string foo = 9;
  string get_foo = 10;
  string version_2_name = 11;
  string foo_2bar = 12;
  string _leading = 13;
  string foo__bar = 14;
  CollisionState state = 15;
}
message Reordered {
  string get_foo = 1;
  string foo = 2;
}
`
	importPath, packageName, names, err := protoGoPackage(name, []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if importPath != "example.com/fixture/pb" || packageName != "fixturepb" {
		t.Fatalf("Go package = %q %q", importPath, packageName)
	}
	for _, test := range []struct {
		message string
		field   string
		want    string
	}{
		{message: "Collision", field: "reset", want: "Reset_"},
		{message: "Collision", field: "string", want: "String_"},
		{message: "Collision", field: "descriptor", want: "Descriptor_"},
		{message: "Collision", field: "proto_message", want: "ProtoMessage_"},
		{message: "Collision", field: "marshal", want: "Marshal_"},
		{message: "Collision", field: "unmarshal", want: "Unmarshal_"},
		{message: "Collision", field: "extension_range_array", want: "ExtensionRangeArray_"},
		{message: "Collision", field: "extension_map", want: "ExtensionMap_"},
		{message: "Collision", field: "foo", want: "Foo"},
		{message: "Collision", field: "get_foo", want: "GetFoo_"},
		{message: "Collision", field: "version_2_name", want: "Version_2Name"},
		{message: "Collision", field: "foo_2bar", want: "Foo_2Bar"},
		{message: "Collision", field: "_leading", want: "XLeading"},
		{message: "Collision", field: "foo__bar", want: "Foo_Bar"},
		{message: "Reordered", field: "get_foo", want: "GetFoo"},
		{message: "Reordered", field: "foo", want: "Foo_"},
	} {
		field, ok := names.field(test.message, test.field)
		if !ok || field.goName != test.want {
			t.Errorf("field %s.%s = %#v, %v; want %q", test.message, test.field, field, ok, test.want)
		}
	}
	state := &planState{protoNames: names}
	if got := state.protoFieldName("Collision", "reset"); got != "Reset_" {
		t.Fatalf("sealed field name = %q", got)
	}
	if got := state.protoEnumName("Collision", "state"); got != "CollisionState" {
		t.Fatalf("sealed enum name = %q", got)
	}
	for protoName, want := range map[string]string{
		"COLLISION_STATE_UNSPECIFIED": "CollisionState_COLLISION_STATE_UNSPECIFIED",
		"COLLISION_STATE_ACTIVE":      "CollisionState_COLLISION_STATE_ACTIVE",
	} {
		if got := state.protoEnumValueName("Collision", "state", protoName); got != want {
			t.Errorf("sealed enum value %s = %q; want %q", protoName, got, want)
		}
	}
	t.Run("missing name is an invariant failure", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("missing protobuf name returned silently")
			}
		}()
		_ = state.protoFieldName("Collision", "missing")
	})
}

func TestValidateServiceLayoutRequiresOneServiceRoot(t *testing.T) {
	tests := []struct {
		name   string
		layout ServiceLayout
		root   string
		ok     bool
	}{
		{"valid", ServiceLayout{ServiceID: "account", EntSchemaDir: "backend/account/ent/schema", LogicRoot: "backend/account/internal/logic"}, "backend/account", true},
		{"cross service", ServiceLayout{ServiceID: "account", EntSchemaDir: "backend/account/ent/schema", LogicRoot: "backend/role/internal/logic"}, "", false},
		{"wrong schema suffix", ServiceLayout{ServiceID: "account", EntSchemaDir: "backend/account/schema", LogicRoot: "backend/account/internal/logic"}, "", false},
		{"wrong logic suffix", ServiceLayout{ServiceID: "account", EntSchemaDir: "backend/account/ent/schema", LogicRoot: "backend/account/logic"}, "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, err := validateServiceLayout(test.layout)
			if test.ok && (err != nil || root != test.root) {
				t.Fatalf("validateServiceLayout() = %q, %v", root, err)
			}
			if !test.ok && err == nil {
				t.Fatalf("validateServiceLayout() = %q, nil", root)
			}
		})
	}
}

func TestLogicArtifactIdentityAndPathAreDerived(t *testing.T) {
	id, path := logicArtifactIdentity("account", "backend/account/internal/logic", "ListAPIKey")
	if id != "crud-logic.account.listapikey" || path != "backend/account/internal/logic/listapikeylogic.go" {
		t.Fatalf("logic artifact = %q %q", id, path)
	}
}

func TestCRUDOperationSetRejectsNonCRUDMethods(t *testing.T) {
	for _, operation := range []string{"list", "get", "create", "update", "delete"} {
		if !validOperation(operation) {
			t.Fatalf("validOperation(%q) = false", operation)
		}
	}
	for _, operation := range []string{"", "archive", "Get", "list_all"} {
		if validOperation(operation) {
			t.Fatalf("validOperation(%q) = true", operation)
		}
	}
}

func TestAppendCandidateRejectsDuplicateDerivedIDOrPath(t *testing.T) {
	for _, test := range []struct {
		name   string
		second candidate
	}{
		{name: "id", second: candidate{id: "crud-logic.account.getaccount", path: "backend/account/internal/logic/other.go"}},
		{name: "path", second: candidate{id: "crud-logic.account.other", path: "backend/account/internal/logic/getaccountlogic.go"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &planState{}
			seenID, seenPath := map[string]bool{}, map[string]bool{}
			first := candidate{id: "crud-logic.account.getaccount", path: "backend/account/internal/logic/getaccountlogic.go"}
			if err := appendCandidate(state, seenID, seenPath, first); err != nil {
				t.Fatal(err)
			}
			if err := appendCandidate(state, seenID, seenPath, test.second); err == nil {
				t.Fatal("duplicate candidate accepted")
			}
			if len(state.candidates) != 1 {
				t.Fatalf("candidate count = %d", len(state.candidates))
			}
		})
	}
}

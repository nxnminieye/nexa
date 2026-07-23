package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

func TestRenderProtocolIRGoUsesProtobufFieldNames(t *testing.T) {
	file := protocolFieldNameFixture()
	output := string(renderProtocolIRGo(file, "accountspb"))
	assertProtocolFieldNames(t, output)
}

func protocolFieldNameFixture() protocolFile {
	return protocolFile{
		Path:  "backend/accounts/desc/accounts.crud.generated.proto",
		Enums: []protocolEnum{{FullName: "acme.accounts.v1.AccountState", Values: []protocolEnumValue{{Name: "ACCOUNT_STATE_UNSPECIFIED", Number: 0}, {Name: "ACCOUNT_STATE_ACTIVE", Number: 1}}}},
		Messages: []protocolMessage{
			{FullName: "acme.accounts.v1.Account", Fields: []protocolField{
				{FullName: "acme.accounts.v1.Account.id", Cardinality: "singular", Presence: "implicit", Type: protocolType{Kind: "scalar", Name: "int64"}},
				{FullName: "acme.accounts.v1.Account.name", Cardinality: "singular", Presence: "implicit", Type: protocolType{Kind: "scalar", Name: "string"}},
				{FullName: "acme.accounts.v1.Account.state", Cardinality: "singular", Presence: "implicit", Type: protocolType{Kind: "enum", Name: "acme.accounts.v1.AccountState"}},
			}},
			{FullName: "acme.accounts.v1.CRUDPayload", Fields: []protocolField{
				{FullName: "acme.accounts.v1.CRUDPayload.item", Cardinality: "singular", Presence: "implicit", Type: protocolType{Kind: "message", Name: "acme.accounts.v1.Account"}},
				{FullName: "acme.accounts.v1.CRUDPayload.items", Cardinality: "repeated", Presence: "implicit", Type: protocolType{Kind: "message", Name: "acme.accounts.v1.Account"}},
				{FullName: "acme.accounts.v1.CRUDPayload.offset", Cardinality: "singular", Presence: "implicit", Type: protocolType{Kind: "scalar", Name: "uint64"}},
				{FullName: "acme.accounts.v1.CRUDPayload.limit", Cardinality: "singular", Presence: "implicit", Type: protocolType{Kind: "scalar", Name: "uint64"}},
				{FullName: "acme.accounts.v1.CRUDPayload.total", Cardinality: "singular", Presence: "implicit", Type: protocolType{Kind: "scalar", Name: "uint64"}},
			}},
		},
	}
}

func assertProtocolFieldNames(t *testing.T, output string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "generated.go", output, parser.AllErrors)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{"Account": {"Id": "int64", "Name": "string", "State": "AccountState"}, "CRUDPayload": {"Item": "*Account", "Items": "[]*Account", "Offset": "uint64", "Limit": "uint64", "Total": "uint64"}}
	got := map[string]map[string]string{}
	for _, declaration := range file.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range generic.Specs {
			named, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structure, ok := named.Type.(*ast.StructType)
			if !ok {
				continue
			}
			fields := map[string]string{}
			for _, field := range structure.Fields.List {
				for _, name := range field.Names {
					fields[name.Name] = types.ExprString(field.Type)
				}
			}
			got[named.Name.Name] = fields
		}
	}
	for structure, fields := range want {
		for name, typ := range fields {
			if got[structure][name] != typ {
				t.Fatalf("%s.%s = %q, want %q", structure, name, got[structure][name], typ)
			}
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if identifier, ok := node.(*ast.Ident); ok && (identifier.Name == "Account_Id" || identifier.Name == "CRUDPayload_Items") {
			t.Fatalf("generated output contains conflicting identifier %q", identifier.Name)
		}
		return true
	})
}

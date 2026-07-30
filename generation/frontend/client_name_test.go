package frontend

import (
	"testing"

	"github.com/nxnminieye/nexa/generation/api"
)

func TestGeneratedClientNamesFollowPDCLExportRules(t *testing.T) {
	names, failure := generatedClientNames(api.Closure{Operations: []api.ClosureOperation{
		{IDValue: "asset.list"},
		{IDValue: "records.getRecord"},
		{IDValue: "role.list"},
	}})
	if failure != nil {
		t.Fatal(failure)
	}
	for operationID, want := range map[string]string{
		"asset.list":        "AssetList",
		"records.getRecord": "getRecord",
		"role.list":         "RoleList",
	} {
		if got := names[operationID]; got != want {
			t.Fatalf("client name for %s = %q, want %q", operationID, got, want)
		}
	}
}

func TestGeneratedClientNamesRejectReservedTypeScriptWords(t *testing.T) {
	_, failure := generatedClientNames(api.Closure{Operations: []api.ClosureOperation{{IDValue: "records.delete"}}})
	if failure == nil || failure.Reason() != "generated_operation_name_reserved" {
		t.Fatalf("reserved client name failure = %#v", failure)
	}
}

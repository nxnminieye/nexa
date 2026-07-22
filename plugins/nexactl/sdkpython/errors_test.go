package sdkpython

import (
	"encoding/json"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
)

func TestInputErrorHasSmallStableDetail(t *testing.T) {
	err := inputError(invocationMember{name: "repo-root", reason: "repo_root_invalid", pointer: "/repo-root"}, "unchanged")
	p := protocol.Project(err)
	if p.Code != "sdk_python_assets_input_invalid" || p.Category != protocol.CategoryInput {
		t.Fatalf("projection=%#v", p)
	}
	var detail map[string]any
	if json.Unmarshal(p.Details, &detail) != nil {
		t.Fatal("invalid details")
	}
	if len(detail) != 3 || detail["reason"] != "repo_root_invalid" || detail["pointer"] != "/repo-root" {
		t.Fatalf("detail=%#v", detail)
	}
}

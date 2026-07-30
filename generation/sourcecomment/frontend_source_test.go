package sourcecomment

import "testing"

func TestParseFrontendSourceBuildsPageFacts(t *testing.T) {
	data := []byte(`# @nexa $contract: "nexa.dev/source-comment/v1"
apiVersion: nexa.dev/frontend-source/v1
kind: Page
# @nexa ui.entity: "Record"
# @nexa ui.pageSize: 25
# @nexa route.path: "/records"
# @nexa route.name: "Records"
# @nexa route.icon: "lucide:database"
# @nexa menu.order: 10
id: records
`)
	graph, diagnostics, err := ParseFrontendSource("frontend/records.page.yaml", data)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("graph diagnostics=%#v err=%v", diagnostics, err)
	}
	for _, key := range []string{"ui.entity", "ui.pageSize", "route.path", "route.name", "route.icon", "menu.order"} {
		if _, ok := graph.Fact(FactID{SemanticID: "records", Key: key}); !ok {
			t.Fatalf("missing %s", key)
		}
	}
}

func TestParseFrontendSourceRejectsJSONUnknownFieldsAndUnboundFacts(t *testing.T) {
	if _, _, err := ParseFrontendSource("frontend/records.page.json", []byte(`{"apiVersion":"nexa.dev/frontend-source/v1","kind":"Page","id":"records"}`)); err == nil {
		t.Fatal("JSON frontend source accepted")
	}
	unknown := []byte("apiVersion: nexa.dev/frontend-source/v1\nkind: Page\nid: records\nfields: []\n")
	if _, _, err := ParseFrontendSource("frontend/records.page.yaml", unknown); err == nil {
		t.Fatal("unknown YAML field accepted")
	}
	unbound := []byte(`# @nexa $contract: "nexa.dev/source-comment/v1"
# @nexa route.path: "/records"
apiVersion: nexa.dev/frontend-source/v1
kind: Page
id: records
`)
	_, diagnostics, err := ParseFrontendSource("frontend/records.page.yaml", unbound)
	if err != nil || len(diagnostics) != 1 || diagnostics[0].Code != CodeInvalidTarget {
		t.Fatalf("diagnostics=%#v err=%v", diagnostics, err)
	}
}

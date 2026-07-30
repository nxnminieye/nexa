package composition_test

import (
	"testing"

	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/protocol"
)

func TestRenderEmitsOnlyCanonicalAPI(t *testing.T) {
	document, err := composition.Build(testCatalog(t), []protocol.Document{testProtocol(t, canonicalProtocolSource())}, testNative(t), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := composition.Render(document, composition.RenderOptions{CoreRoot: "backend/core"})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].ID != "api.account" {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	generated, err := composition.GeneratedAPI(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := httpapi.VerifyRenderedGenerated(artifacts[0].Path, artifacts[0].Content, generated); err != nil {
		t.Fatalf("verify rendered API: %v", err)
	}
	operation, ok := generated.Operation("account.account.v1.accountService.get")
	if !ok {
		t.Fatal("rendered canonical RPC operation is missing")
	}
	request, ok := generated.Type(operation.RequestType())
	if !ok || len(request.Fields()) != 5 {
		t.Fatalf("rendered request = %#v, %v", request, ok)
	}
}

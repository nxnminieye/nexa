package sourcecomment_test

import (
	"testing"

	"github.com/nxnminieye/nexa/generation/sourcecomment"
)

func TestStandardRegistryCoversFrozenFactFamilies(t *testing.T) {
	registry := sourcecomment.StandardRegistry()
	tests := []struct {
		key      string
		kind     sourcecomment.ValueKind
		earliest sourcecomment.Stage
		consumer sourcecomment.Consumer
		security bool
	}{
		{"label.zh-CN", sourcecomment.ValueString, sourcecomment.StageEnt, sourcecomment.ConsumerFrontend, false},
		{"scope", sourcecomment.ValueString, sourcecomment.StageEnt, sourcecomment.ConsumerEntity, true},
		{"crud.operations", sourcecomment.ValueList, sourcecomment.StageEnt, sourcecomment.ConsumerCRUD, false},
		{"ui.control", sourcecomment.ValueString, sourcecomment.StageEnt, sourcecomment.ConsumerFrontend, false},
		{"visibility", sourcecomment.ValueString, sourcecomment.StageEnt, sourcecomment.ConsumerEntity, true},
		{"crud.read", sourcecomment.ValueString, sourcecomment.StageEnt, sourcecomment.ConsumerCRUD, false},
		{"crud.mutation", sourcecomment.ValueString, sourcecomment.StageEnt, sourcecomment.ConsumerCRUD, false},
		{"auth", sourcecomment.ValueString, sourcecomment.StageProto, sourcecomment.ConsumerHTTP, true},
		{"permission", sourcecomment.ValueString, sourcecomment.StageProto, sourcecomment.ConsumerHTTP, true},
		{"http.method", sourcecomment.ValueString, sourcecomment.StageProto, sourcecomment.ConsumerHTTP, true},
		{"http.path", sourcecomment.ValueString, sourcecomment.StageProto, sourcecomment.ConsumerHTTP, true},
		{"ui.reference", sourcecomment.ValueObject, sourcecomment.StageEnt, sourcecomment.ConsumerFrontend, false},
		{"ui.entity", sourcecomment.ValueString, sourcecomment.StagePage, sourcecomment.ConsumerFrontend, false},
		{"ui.pageSize", sourcecomment.ValueInteger, sourcecomment.StagePage, sourcecomment.ConsumerFrontend, false},
		{"ui.extensionComponent", sourcecomment.ValueString, sourcecomment.StagePage, sourcecomment.ConsumerFrontend, false},
		{"route.path", sourcecomment.ValueString, sourcecomment.StagePage, sourcecomment.ConsumerFrontend, false},
		{"route.name", sourcecomment.ValueString, sourcecomment.StagePage, sourcecomment.ConsumerFrontend, false},
		{"route.icon", sourcecomment.ValueString, sourcecomment.StagePage, sourcecomment.ConsumerFrontend, false},
		{"menu.order", sourcecomment.ValueInteger, sourcecomment.StagePage, sourcecomment.ConsumerFrontend, false},
	}
	for _, test := range tests {
		entry, ok := registry.Lookup(test.key)
		if !ok || entry.ValueKind() != test.kind || entry.EarliestStage() != test.earliest || entry.Consumer() != test.consumer || entry.SecuritySensitive() != test.security || !entry.Propagates() {
			t.Errorf("entry %q = %#v, %v", test.key, entry, ok)
		}
	}
	for _, removed := range []string{"label.key", "description.key", "identity", "http.operationId", "route.keepAlive", "menu.parentId", "menu.icon", "page.size", "extension.component", "ui.layout", "ui.surfaces", "ui.choices", "reference.target"} {
		if _, ok := registry.Lookup(removed); ok {
			t.Errorf("removed key %q was accepted", removed)
		}
	}
	if _, ok := registry.Lookup("x-consumer.private"); ok {
		t.Fatal("consumer extension key was accepted")
	}
	if len(registry.Entries()) != 22 {
		t.Fatalf("registry entries = %d, want frozen 22", len(registry.Entries()))
	}
	wantKeys := []string{"auth", "crud.mutation", "crud.operations", "crud.read", "description.en-US", "description.zh-CN", "http.method", "http.path", "label.en-US", "label.zh-CN", "menu.order", "permission", "route.icon", "route.name", "route.path", "scope", "ui.control", "ui.entity", "ui.extensionComponent", "ui.pageSize", "ui.reference", "visibility"}
	entries := registry.Entries()
	for index, want := range wantKeys {
		if entries[index].Key() != want {
			t.Fatalf("entry[%d] = %q, want %q", index, entries[index].Key(), want)
		}
	}
}

func TestProtoOnlyMessageMayOwnSchemaFacts(t *testing.T) {
	targetValue := target(t, sourcecomment.NodeMessage, sourcecomment.StageProto, "proto://sample.proto#sample.v1.Record")
	parsed, diagnostics := sourcecomment.ParseFile(sourcecomment.StandardRegistry(), "sample.proto", []sourcecomment.Line{
		line(`// @nexa $contract: "nexa.dev/source-comment/v1"`, nil, 1),
		line(`// @nexa scope: "tenant"`, &targetValue, 2),
		line(`// @nexa crud.operations: ["list","get"]`, &targetValue, 3),
	})
	if len(diagnostics) != 0 || len(parsed.Facts()) != 2 {
		t.Fatalf("facts = %#v, diagnostics = %#v", parsed.Facts(), diagnostics)
	}
}

func TestFrozenRegistryValueConstraints(t *testing.T) {
	permissionTarget := target(t, sourcecomment.NodeRPC, sourcecomment.StageProto, "proto://rpc/a.proto#A.Get")
	parsed, diagnostics := sourcecomment.ParseFile(sourcecomment.StandardRegistry(), permissionTarget.Source.Path(), []sourcecomment.Line{
		line(`// @nexa $contract: "nexa.dev/source-comment/v1"`, nil, 1),
		line(`// @nexa permission: "nexa.auth.role_permissions.bind"`, &permissionTarget, 2),
	})
	if len(diagnostics) != 0 || len(parsed.Facts()) != 1 {
		t.Fatalf("PDCL permission facts = %#v, diagnostics = %#v", parsed.Facts(), diagnostics)
	}

	tests := []struct {
		name, directive string
		target          sourcecomment.Target
		code            sourcecomment.Code
	}{
		{"optional auth", `// @nexa auth: "optional"`, target(t, sourcecomment.NodeRPC, sourcecomment.StageProto, "proto://rpc/a.proto#A.Get"), sourcecomment.CodeInvalidValue},
		{"patch method", `// @nexa http.method: "PATCH"`, target(t, sourcecomment.NodeRPC, sourcecomment.StageProto, "proto://rpc/a.proto#A.Get"), sourcecomment.CodeInvalidValue},
		{"bad permission", `// @nexa permission: "Records.Read"`, target(t, sourcecomment.NodeRPC, sourcecomment.StageProto, "proto://rpc/a.proto#A.Get"), sourcecomment.CodeInvalidValue},
		{"page size zero", `// @nexa ui.pageSize: 0`, target(t, sourcecomment.NodePage, sourcecomment.StagePage, "page://frontend/pages/a.yaml#records"), sourcecomment.CodeInvalidValue},
		{"page size high", `// @nexa ui.pageSize: 101`, target(t, sourcecomment.NodePage, sourcecomment.StagePage, "page://frontend/pages/a.yaml#records"), sourcecomment.CodeInvalidValue},
		{"bad route", `// @nexa route.path: "/Records_List"`, target(t, sourcecomment.NodePage, sourcecomment.StagePage, "page://frontend/pages/a.yaml#records"), sourcecomment.CodeInvalidValue},
		{"bad reference", `// @nexa ui.reference: {"target":"bad target","display":"name"}`, target(t, sourcecomment.NodeField, sourcecomment.StageEnt, "ent://schema/a.go#A.ref"), sourcecomment.CodeInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, diagnostics := sourcecomment.ParseFile(sourcecomment.StandardRegistry(), test.target.Source.Path(), []sourcecomment.Line{line(`// @nexa $contract: "nexa.dev/source-comment/v1"`, nil, 1), line(test.directive, &test.target, 2)})
			if len(diagnostics) != 1 || diagnostics[0].Code != test.code {
				t.Fatalf("diagnostics = %#v", diagnostics)
			}
		})
	}
}

func target(t *testing.T, kind sourcecomment.NodeKind, stage sourcecomment.Stage, raw string) sourcecomment.Target {
	ref := mustRef(t, raw)
	return sourcecomment.Target{SemanticID: ref.Symbol(), Kind: kind, Stage: stage, Source: ref}
}

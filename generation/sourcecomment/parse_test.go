package sourcecomment_test

import (
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/sourcecomment"
)

func TestParseLineStrictJSONScalarAndList(t *testing.T) {
	tests := []struct {
		name, line string
		kind       sourcecomment.ValueKind
		selected   bool
		code       sourcecomment.Code
	}{
		{name: "quoted string", line: `// @nexa label.zh-CN: "记录"`, kind: sourcecomment.ValueString, selected: true},
		{name: "boolean", line: `// @nexa visible: true`, kind: sourcecomment.ValueBoolean, selected: true},
		{name: "integer", line: `# @nexa menu.order: 12`, kind: sourcecomment.ValueInteger, selected: true},
		{name: "list", line: `// @nexa crud.operations: ["list","get"]`, kind: sourcecomment.ValueList, selected: true},
		{name: "closed reference object", line: `// @nexa ui.reference: {"target":"accounts.v1.User","display":"name"}`, kind: sourcecomment.ValueObject, selected: true},
		{name: "ordinary comment", line: `// this mentions @nexa but is not a directive`},
		{name: "unquoted string", line: `// @nexa label.zh-CN: 记录`, code: sourcecomment.CodeInvalidSyntax},
		{name: "yaml boolean", line: `// @nexa visible: yes`, code: sourcecomment.CodeInvalidSyntax},
		{name: "null", line: `// @nexa ui.control: null`, code: sourcecomment.CodeInvalidSyntax},
		{name: "object", line: `// @nexa ui.control: {"name":"input"}`, code: sourcecomment.CodeInvalidSyntax},
		{name: "reference unknown member", line: `// @nexa ui.reference: {"target":"User","display":"name","extra":"x"}`, code: sourcecomment.CodeInvalidSyntax},
		{name: "reference duplicate member", line: `// @nexa ui.reference: {"target":"User","target":"Other","display":"name"}`, code: sourcecomment.CodeInvalidSyntax},
		{name: "reference missing member", line: `// @nexa ui.reference: {"target":"User"}`, code: sourcecomment.CodeInvalidSyntax},
		{name: "fraction", line: `// @nexa menu.order: 1.5`, code: sourcecomment.CodeInvalidSyntax},
		{name: "non ascii key", line: `// @nexa 标签.zh-CN: "记录"`, code: sourcecomment.CodeInvalidSyntax},
		{name: "wrong separator", line: `// @nexa ui.control = "text"`, code: sourcecomment.CodeInvalidSyntax},
		{name: "trailing data", line: `// @nexa ui.control: "text" trailing`, code: sourcecomment.CodeInvalidSyntax},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prefix := "//"
			if strings.HasPrefix(test.line, "#") {
				prefix = "#"
			}
			directive, selected, failure := sourcecomment.ParseLine(sourcecomment.Line{Text: test.line, CommentPrefix: prefix, Location: sourcecomment.Location{File: "sample", Line: 7, Column: 1}})
			if test.code != "" {
				if failure == nil || failure.Code != test.code || failure.File != "sample" || failure.Line != 7 || failure.Column <= 0 || failure.Suggestion == "" {
					t.Fatalf("failure = %#v", failure)
				}
				return
			}
			if failure != nil || selected != test.selected {
				t.Fatalf("selected=%v failure=%#v", selected, failure)
			}
			if selected && directive.Value().Kind() != test.kind {
				t.Fatalf("kind = %q", directive.Value().Kind())
			}
		})
	}
}

func TestParseFileContractRegistryTargetAndDuplicates(t *testing.T) {
	ref := mustRef(t, "ent://backend/records/schema.go#Record.name")
	target := sourcecomment.Target{SemanticID: "Record.name", Kind: sourcecomment.NodeField, Stage: sourcecomment.StageEnt, Source: ref}
	tests := []struct {
		name  string
		lines []sourcecomment.Line
		codes []sourcecomment.Code
	}{
		{name: "valid", lines: []sourcecomment.Line{
			line(`// @nexa $contract: "nexa.dev/source-comment/v1"`, nil, 1),
			line(`// @nexa label.zh-CN: "名称"`, &target, 2),
		}},
		{name: "missing contract", lines: []sourcecomment.Line{line(`// @nexa unknown.fact: "x"`, &target, 2)}, codes: []sourcecomment.Code{sourcecomment.CodeInvalidSyntax, sourcecomment.CodeUnknownKey}},
		{name: "duplicate", lines: []sourcecomment.Line{
			line(`// @nexa $contract: "nexa.dev/source-comment/v1"`, nil, 1),
			line(`// @nexa label.zh-CN: "名称"`, &target, 2), line(`// @nexa label.zh-CN: "名字"`, &target, 3),
		}, codes: []sourcecomment.Code{sourcecomment.CodeDuplicateFact}},
		{name: "invalid enum", lines: []sourcecomment.Line{line(`// @nexa $contract: "nexa.dev/source-comment/v1"`, nil, 1), line(`// @nexa visibility: "secret"`, &target, 2)}, codes: []sourcecomment.Code{sourcecomment.CodeInvalidValue}},
		{name: "empty list", lines: []sourcecomment.Line{line(`// @nexa $contract: "nexa.dev/source-comment/v1"`, nil, 1), line(`// @nexa crud.operations: []`, &sourcecomment.Target{SemanticID: "Record", Kind: sourcecomment.NodeSchema, Stage: sourcecomment.StageEnt, Source: mustRef(t, "ent://backend/records/schema.go#Record")}, 2)}, codes: []sourcecomment.Code{sourcecomment.CodeInvalidValue}},
		{name: "wrong target", lines: []sourcecomment.Line{line(`// @nexa $contract: "nexa.dev/source-comment/v1"`, nil, 1), line(`// @nexa permission: "records.read"`, &target, 2)}, codes: []sourcecomment.Code{sourcecomment.CodeInvalidTarget}},
		{name: "unknown system", lines: []sourcecomment.Line{line(`// @nexa $contract: "nexa.dev/source-comment/v1"`, nil, 1), line(`// @nexa $alias: "x"`, &target, 2)}, codes: []sourcecomment.Code{sourcecomment.CodeUnknownKey}},
		{name: "removed key", lines: []sourcecomment.Line{line(`// @nexa $contract: "nexa.dev/source-comment/v1"`, nil, 1), line(`// @nexa identity: "ent-id"`, &target, 2)}, codes: []sourcecomment.Code{sourcecomment.CodeUnknownKey}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, diagnostics := sourcecomment.ParseFile(sourcecomment.StandardRegistry(), "sample.go", test.lines)
			if len(diagnostics) != len(test.codes) {
				t.Fatalf("diagnostics = %#v", diagnostics)
			}
			for index, code := range test.codes {
				if diagnostics[index].Code != code {
					t.Fatalf("diagnostic[%d] = %#v", index, diagnostics[index])
				}
			}
			if len(test.codes) == 0 && (parsed.Contract() != sourcecomment.Contract || len(parsed.Facts()) != 1) {
				t.Fatalf("parsed = %#v", parsed)
			}
		})
	}
}

func TestParseFileExposesTypedSourceBinding(t *testing.T) {
	target := sourcecomment.Target{SemanticID: "Record.name", Kind: sourcecomment.NodeProtoField, Stage: sourcecomment.StageProto, Source: mustRef(t, "proto://rpc/record.proto#records.v1.Record.name")}
	parsed, diagnostics := sourcecomment.ParseFile(sourcecomment.StandardRegistry(), "record.proto", []sourcecomment.Line{
		line(`// @nexa $contract: "nexa.dev/source-comment/v1"`, nil, 1),
		line(`// @nexa $source: "ent://schema/record.go#Record.name"`, &target, 2),
	})
	if len(diagnostics) != 0 || len(parsed.Sources()) != 1 || parsed.Sources()[0].Target().SemanticID != "Record.name" || parsed.Sources()[0].Source().Stage() != sourcecomment.StageEnt {
		t.Fatalf("parsed=%#v diagnostics=%#v", parsed, diagnostics)
	}
}

func TestSourceRefRejectsNonCanonicalReferences(t *testing.T) {
	tests := []string{"/absolute", "proto://../x.proto#A", "proto://x/../y.proto#A", "http://x.proto#A", "proto://x.proto?query#A", "proto://x.proto#bad symbol", "proto://x.proto"}
	for _, raw := range tests {
		if _, err := sourcecomment.ParseSourceRef(raw); err == nil {
			t.Errorf("ParseSourceRef(%q) succeeded", raw)
		}
	}
	valid := mustRef(t, "proto://backend/records/record.proto#records.v1.Record.name")
	if valid.Stage() != sourcecomment.StageProto || valid.String() != "proto://backend/records/record.proto#records.v1.Record.name" {
		t.Fatalf("ref = %#v", valid)
	}
}

func line(text string, target *sourcecomment.Target, number int) sourcecomment.Line {
	return sourcecomment.Line{Text: text, CommentPrefix: "//", Location: sourcecomment.Location{File: "sample.go", Line: number, Column: 1}, Target: target}
}
func mustRef(t *testing.T, raw string) sourcecomment.SourceRef {
	t.Helper()
	value, err := sourcecomment.ParseSourceRef(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

package buildinput

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/provenance"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

type invalidH1Vector struct {
	name  string
	value string
}

func invalidH1Vectors() []invalidH1Vector {
	return []invalidH1Vector{
		{name: "non-zero padding bits", value: "h1:" + strings.Repeat("A", 42) + "B="},
		{name: "missing padding", value: "h1:" + strings.Repeat("A", 43)},
		{name: "extra padding", value: "h1:" + strings.Repeat("A", 43) + "=="},
		{name: "wrong decoded length", value: "h1:" + base64.StdEncoding.EncodeToString(make([]byte, 31))},
		{name: "non alphabet character", value: "h1:!" + strings.Repeat("A", 42) + "="},
	}
}

const minimalGraphGolden = `{"apiVersion":"nexa.dev/ent-helper-module-graph/v1","consumerModule":{"path":"example.com/acme/consumer","version":"v0.0.0"},"goVersion":"1.25","helperDigest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","kind":"EntHelperModuleGraph","moduleSources":[],"modules":[],"toolModule":{"path":"github.com/nxnminieye/nexa","version":"v0.1.0"}}`

const replacementGraphGolden = `{"apiVersion":"nexa.dev/ent-helper-module-graph/v1","consumerModule":{"path":"example.com/acme/consumer/v2","version":"v2.0.0"},"goVersion":"1.25","helperDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","kind":"EntHelperModuleGraph","moduleSources":[{"digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","ref":"repo:go.mod"},{"digest":"sha256:2222222222222222222222222222222222222222222222222222222222222222","ref":"repo:go.sum"},{"digest":"sha256:3333333333333333333333333333333333333333333333333333333333333333","ref":"repo:localdeps/schema-helper-v3/go.mod"}],"modules":[{"content":{"kind":"local"},"path":"example.com/acme/consumer/v2","replacement":{"kind":"repository","repositoryPath":"."},"version":"v2.0.0"},{"content":{"kind":"local"},"path":"example.com/acme/schema-helper/v3","replacement":{"kind":"repository","repositoryPath":"localdeps/schema-helper-v3"},"version":"v3.2.1"},{"content":{"goModSum":"h1:ERERERERERERERERERERERERERERERERERERERERERE=","kind":"remote","sum":"h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="},"path":"example.com/acme/versioned","replacement":{"kind":"version","path":"example.com/acme/versioned-fork","version":"v1.2.4"},"version":"v1.2.3"},{"content":{"goModSum":"h1:MzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzM=","kind":"remote","sum":"h1:IiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiI="},"path":"github.com/nxnminieye/nexa","replacement":{"kind":"none"},"version":"v0.1.0"}],"toolModule":{"path":"github.com/nxnminieye/nexa","version":"v0.1.0"},"toolchainVersion":"go1.25.0"}`

func TestModuleGraphMinimalGoldenJCSAndDigest(t *testing.T) {
	source, err := provenance.ParseDomainSource("quality/module-graph.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ParseGraph(source, []byte(minimalGraphGolden))
	if err != nil {
		t.Fatalf("ParseGraph() error = %v", err)
	}
	canonical, err := CanonicalGraphSnapshot(snapshot)
	if err != nil {
		t.Fatalf("CanonicalGraphSnapshot() error = %v", err)
	}
	if !bytes.Equal(canonical, []byte(minimalGraphGolden)) {
		t.Fatalf("canonical graph = %s", canonical)
	}

	var independent any
	if err := json.Unmarshal([]byte(minimalGraphGolden), &independent); err != nil {
		t.Fatal(err)
	}
	ordinary, err := json.Marshal(independent)
	if err != nil {
		t.Fatal(err)
	}
	independentJCS, err := jcs.Transform(ordinary)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, independentJCS) {
		t.Fatalf("canonical graph is not independent RFC 8785 output")
	}
	if got := provenance.SHA256(canonical).String(); got != "sha256:dbdb906f3406fd80e6818d4decab63da4aa14e82c618a976a285c02fa5c4b5cf" {
		t.Fatalf("graph digest = %s", got)
	}
}

func TestModuleGraphSnapshotTypedReadbackAndSchemaAreDefensive(t *testing.T) {
	source, err := provenance.ParseDomainSource("quality/module-graph.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ParseGraph(source, []byte(replacementGraphGolden))
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := snapshot.ConsumerModule()
	if err != nil || consumer.Path != "example.com/acme/consumer/v2" || consumer.Version != "v2.0.0" {
		t.Fatalf("ConsumerModule() = %#v, %v", consumer, err)
	}
	toolchain, present, err := snapshot.ToolchainVersion()
	if err != nil || !present || toolchain != "go1.25.0" {
		t.Fatalf("ToolchainVersion() = %q, %v, %v", toolchain, present, err)
	}
	sources, err := snapshot.ModuleSources()
	if err != nil || len(sources) != 3 {
		t.Fatalf("ModuleSources() = %#v, %v", sources, err)
	}
	sources[0] = provenance.Source{}
	again, err := snapshot.ModuleSources()
	if err != nil || again[0].Ref.String() != "repo:go.mod" {
		t.Fatalf("ModuleSources() after caller mutation = %#v, %v", again, err)
	}
	modules, err := snapshot.Modules()
	if err != nil || len(modules) != 4 || modules[2].Replacement.Kind != "version" || modules[3].Content.GoModSum == "" {
		t.Fatalf("Modules() = %#v, %v", modules, err)
	}
	modules[0] = ModuleIdentity{}
	againModules, err := snapshot.Modules()
	if err != nil || againModules[0].Path != "example.com/acme/consumer/v2" {
		t.Fatalf("Modules() after caller mutation = %#v, %v", againModules, err)
	}

	first := GraphSchema()
	second := GraphSchema()
	if len(first) == 0 || !bytes.Equal(first, second) {
		t.Fatal("GraphSchema() is empty or unstable")
	}
	first[0] ^= 0xff
	if bytes.Equal(first, GraphSchema()) {
		t.Fatal("GraphSchema() did not return a defensive copy")
	}
	var schemaDocument any
	if err := json.Unmarshal(GraphSchema(), &schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("https://nexa.dev/schemas/generation/toolchain/ent-helper-module-graph-v1.schema.json", schemaDocument); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("https://nexa.dev/schemas/generation/toolchain/ent-helper-module-graph-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal([]byte(replacementGraphGolden), &document); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatalf("schema rejected replacement golden: %v", err)
	}
}

func TestModuleGraphReplacementGoldenAndCanonicalArrayOrder(t *testing.T) {
	source, err := provenance.ParseDomainSource("quality/module-graph.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ParseGraph(source, []byte(replacementGraphGolden))
	if err != nil {
		t.Fatalf("ParseGraph() error = %v", err)
	}
	canonical, err := CanonicalGraphSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, []byte(replacementGraphGolden)) {
		t.Fatalf("canonical replacement graph = %s", canonical)
	}
	if got := provenance.SHA256(canonical).String(); got != "sha256:95e6d28159547761a0bf772f8b8918460cf7a146566e5b9ce3e8832d90184fc9" {
		t.Fatalf("replacement graph digest = %s", got)
	}

	var reordered map[string]any
	if err := json.Unmarshal([]byte(replacementGraphGolden), &reordered); err != nil {
		t.Fatal(err)
	}
	sources := reordered["moduleSources"].([]any)
	sources[0], sources[1] = sources[1], sources[0]
	encoded, err := json.Marshal(reordered)
	if err != nil {
		t.Fatal(err)
	}
	reorderedJCS, err := jcs.Transform(encoded)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseGraph(source, reorderedJCS)
	assertBuildInputError(t, err, "module_graph_snapshot_invalid", "decode", "canonical_order_invalid", "/moduleSources", source.String())
}

func TestModuleGraphStrictWireVariantsAndZeroReadback(t *testing.T) {
	source, err := provenance.ParseDomainSource("quality/module-graph.json")
	if err != nil {
		t.Fatal(err)
	}
	_, err = CanonicalGraphSnapshot(GraphSnapshot{})
	assertBuildInputError(t, err, "module_graph_readback_invalid", "readback", "module_graph_state_invalid", "/moduleGraph", "")

	tests := []struct {
		name, document, reason, pointer string
	}{
		{name: "null root", document: `null`, reason: "document_type_invalid", pointer: ""},
		{name: "unknown field", document: `{"apiVersion":"nexa.dev/ent-helper-module-graph/v1","consumerModule":{"path":"example.com/acme/consumer","version":"v0.0.0"},"extra":true,"goVersion":"1.25","helperDigest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","kind":"EntHelperModuleGraph","moduleSources":[],"modules":[],"toolModule":{"path":"github.com/nxnminieye/nexa","version":"v0.1.0"}}`, reason: "document_unknown_field", pointer: "/extra"},
		{name: "duplicate key", document: `{"apiVersion":"nexa.dev/ent-helper-module-graph/v1","kind":"EntHelperModuleGraph","kind":"EntHelperModuleGraph"}`, reason: "document_duplicate_key", pointer: "/kind"},
		{name: "trailing input", document: minimalGraphGolden + `{}`, reason: "document_trailing_input", pointer: ""},
		{name: "non JCS object order", document: `{"kind":"EntHelperModuleGraph","apiVersion":"nexa.dev/ent-helper-module-graph/v1","consumerModule":{"path":"example.com/acme/consumer","version":"v0.0.0"},"goVersion":"1.25","helperDigest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","moduleSources":[],"modules":[],"toolModule":{"path":"github.com/nxnminieye/nexa","version":"v0.1.0"}}`, reason: "canonical_invalid", pointer: ""},
		{name: "source conflict", document: mutateGraphJCS(t, replacementGraphGolden, func(value map[string]any) {
			sources := value["moduleSources"].([]any)
			value["moduleSources"] = append(sources, sources[len(sources)-1])
		}), reason: "source_conflict", pointer: "/moduleSources/3/ref"},
		{name: "module duplicate", document: mutateGraphJCS(t, replacementGraphGolden, func(value map[string]any) {
			modules := value["modules"].([]any)
			value["modules"] = append(modules, modules[len(modules)-1])
		}), reason: "module_identity_duplicate", pointer: "/modules/4"},
		{name: "local content carries sum", document: mutateGraphJCS(t, replacementGraphGolden, func(value map[string]any) {
			content := value["modules"].([]any)[0].(map[string]any)["content"].(map[string]any)
			content["sum"] = "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
		}), reason: "content_variant_invalid", pointer: "/modules/0/content/sum"},
		{name: "version replacement carries repository path", document: mutateGraphJCS(t, replacementGraphGolden, func(value map[string]any) {
			replacement := value["modules"].([]any)[2].(map[string]any)["replacement"].(map[string]any)
			replacement["repositoryPath"] = "fork"
		}), reason: "replacement_variant_invalid", pointer: "/modules/2/replacement/repositoryPath"},
		{name: "remote sum", document: mutateGraphJCS(t, replacementGraphGolden, func(value map[string]any) {
			content := value["modules"].([]any)[3].(map[string]any)["content"].(map[string]any)
			content["sum"] = "h1:not-base64"
		}), reason: "remote_sum_invalid", pointer: "/modules/3/content/sum"},
		{name: "remote go mod sum", document: mutateGraphJCS(t, replacementGraphGolden, func(value map[string]any) {
			content := value["modules"].([]any)[3].(map[string]any)["content"].(map[string]any)
			content["goModSum"] = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}), reason: "remote_go_mod_sum_invalid", pointer: "/modules/3/content/goModSum"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, parseErr := ParseGraph(source, []byte(test.document))
			assertBuildInputError(t, parseErr, "module_graph_snapshot_invalid", "decode", test.reason, test.pointer, source.String())
		})
	}
}

func TestModuleGraphSnapshotRejectsNonCanonicalRemoteH1Sums(t *testing.T) {
	source, err := provenance.ParseDomainSource("quality/module-graph.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("canonical", func(t *testing.T) {
		if _, parseErr := ParseGraph(source, []byte(replacementGraphGolden)); parseErr != nil {
			t.Fatalf("ParseGraph() rejected canonical h1 values: %v", parseErr)
		}
	})
	for _, field := range []struct {
		name, jsonName, reason string
	}{{"sum", "sum", "remote_sum_invalid"}, {"go mod sum", "goModSum", "remote_go_mod_sum_invalid"}} {
		for _, vector := range invalidH1Vectors() {
			t.Run(field.name+"/"+vector.name, func(t *testing.T) {
				document := mutateGraphJCS(t, replacementGraphGolden, func(value map[string]any) {
					content := value["modules"].([]any)[3].(map[string]any)["content"].(map[string]any)
					content[field.jsonName] = vector.value
				})
				_, parseErr := ParseGraph(source, []byte(document))
				assertBuildInputError(t, parseErr, "module_graph_snapshot_invalid", "decode", field.reason, "/modules/3/content/"+field.jsonName, source.String())
			})
		}
	}
}

func TestModuleGraphSnapshotPreservesClosedUnionMemberPresence(t *testing.T) {
	source, err := provenance.ParseDomainSource("quality/module-graph.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, reason, pointer string
		mutate                func(map[string]any)
	}{
		{name: "local content explicit empty sum", reason: "content_variant_invalid", pointer: "/modules/0/content/sum", mutate: func(value map[string]any) {
			value["modules"].([]any)[0].(map[string]any)["content"].(map[string]any)["sum"] = ""
		}},
		{name: "local content explicit empty go mod sum", reason: "content_variant_invalid", pointer: "/modules/0/content/goModSum", mutate: func(value map[string]any) {
			value["modules"].([]any)[0].(map[string]any)["content"].(map[string]any)["goModSum"] = ""
		}},
		{name: "none replacement explicit empty path", reason: "replacement_variant_invalid", pointer: "/modules/3/replacement/path", mutate: func(value map[string]any) {
			value["modules"].([]any)[3].(map[string]any)["replacement"].(map[string]any)["path"] = ""
		}},
		{name: "repository replacement explicit empty path", reason: "replacement_variant_invalid", pointer: "/modules/1/replacement/path", mutate: func(value map[string]any) {
			value["modules"].([]any)[1].(map[string]any)["replacement"].(map[string]any)["path"] = ""
		}},
		{name: "repository replacement explicit empty version", reason: "replacement_variant_invalid", pointer: "/modules/1/replacement/version", mutate: func(value map[string]any) {
			value["modules"].([]any)[1].(map[string]any)["replacement"].(map[string]any)["version"] = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := mutateGraphJCS(t, replacementGraphGolden, test.mutate)
			_, parseErr := ParseGraph(source, []byte(document))
			assertBuildInputError(t, parseErr, "module_graph_snapshot_invalid", "decode", test.reason, test.pointer, source.String())
		})
	}
}

func TestModuleGraphSnapshotValidatesRequiredTypesUnicodeAndSemanticIdentity(t *testing.T) {
	source, err := provenance.ParseDomainSource("quality/module-graph.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, document, reason, pointer string
	}{
		{name: "required tool module", document: mutateGraphJCS(t, minimalGraphGolden, func(value map[string]any) { delete(value, "toolModule") }), reason: "document_required_missing", pointer: "/toolModule"},
		{name: "go version type", document: mutateGraphJCS(t, minimalGraphGolden, func(value map[string]any) { value["goVersion"] = 1.25 }), reason: "document_type_invalid", pointer: "/goVersion"},
		{name: "consumer path", document: mutateGraphJCS(t, minimalGraphGolden, func(value map[string]any) { value["consumerModule"].(map[string]any)["path"] = "../consumer" }), reason: "module_identity_invalid", pointer: "/consumerModule/path"},
		{name: "tool major", document: mutateGraphJCS(t, minimalGraphGolden, func(value map[string]any) {
			tool := value["toolModule"].(map[string]any)
			tool["path"], tool["version"] = "example.com/tool/v2", "v1.0.0"
		}), reason: "module_identity_invalid", pointer: "/toolModule/version"},
		{name: "go directive", document: mutateGraphJCS(t, minimalGraphGolden, func(value map[string]any) { value["goVersion"] = "go1.25" }), reason: "canonical_invalid", pointer: "/goVersion"},
		{name: "toolchain directive", document: mutateGraphJCS(t, replacementGraphGolden, func(value map[string]any) { value["toolchainVersion"] = "1.25.0" }), reason: "canonical_invalid", pointer: "/toolchainVersion"},
		{name: "module major", document: mutateGraphJCS(t, replacementGraphGolden, func(value map[string]any) {
			module := value["modules"].([]any)[1].(map[string]any)
			module["path"], module["version"] = "example.com/acme/schema-helper/v2", "v1.0.0"
		}), reason: "module_identity_invalid", pointer: "/modules/1/version"},
		{name: "replacement major", document: mutateGraphJCS(t, replacementGraphGolden, func(value map[string]any) {
			replacement := value["modules"].([]any)[2].(map[string]any)["replacement"].(map[string]any)
			replacement["path"], replacement["version"] = "example.com/fork/v2", "v1.0.0"
		}), reason: "replacement_variant_invalid", pointer: "/modules/2/replacement/version"},
		{name: "repository path", document: mutateGraphJCS(t, replacementGraphGolden, func(value map[string]any) {
			value["modules"].([]any)[1].(map[string]any)["replacement"].(map[string]any)["repositoryPath"] = "../escape"
		}), reason: "replacement_variant_invalid", pointer: "/modules/1/replacement/repositoryPath"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, parseErr := ParseGraph(source, []byte(test.document))
			assertBuildInputError(t, parseErr, "module_graph_snapshot_invalid", "decode", test.reason, test.pointer, source.String())
		})
	}

	invalidUnicode := []byte(minimalGraphGolden)
	index := bytes.Index(invalidUnicode, []byte("consumer"))
	if index < 0 {
		t.Fatal("fixture consumer substring not found")
	}
	invalidUnicode[index] = 0xff
	_, err = ParseGraph(source, invalidUnicode)
	assertBuildInputError(t, err, "module_graph_snapshot_invalid", "decode", "unicode_invalid", "", source.String())
}

func TestModuleGraphSnapshotRejectsZeroSourceBeforeDocumentBytes(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "valid canonical", data: []byte(minimalGraphGolden)},
		{name: "malformed bytes", data: []byte{0xff, '{'}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseGraph(provenance.DomainSource{}, test.data)
			assertBuildInputError(t, err, "module_graph_snapshot_invalid", "decode", "document_invalid", "", "")
		})
	}
}

func TestModuleGraphCanonicalJSONUsesRFC8785Escaping(t *testing.T) {
	source, err := provenance.ParseDomainSource("quality/module-graph.json")
	if err != nil {
		t.Fatal(err)
	}
	escapeSensitiveGolden := strings.Replace(
		replacementGraphGolden,
		`"repositoryPath":"localdeps/schema-helper-v3"`,
		"\"repositoryPath\":\"localdeps/<&\u2028\"",
		1,
	)
	snapshot, err := ParseGraph(source, []byte(escapeSensitiveGolden))
	if err != nil {
		t.Fatalf("ParseGraph() rejected independent RFC 8785 vector: %v", err)
	}
	canonical, err := CanonicalGraphSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, []byte(escapeSensitiveGolden)) {
		t.Fatalf("canonical escape-sensitive graph = %s, want %s", canonical, escapeSensitiveGolden)
	}
	for _, escaped := range [][]byte{[]byte(`\u003c`), []byte(`\u0026`), []byte(`\u2028`)} {
		if bytes.Contains(canonical, escaped) {
			t.Fatalf("canonical graph retained non-JCS escape %q", escaped)
		}
	}
}

func TestModuleGraphSnapshotValidatesNullAndVariantRequiredShapeFirst(t *testing.T) {
	source, err := provenance.ParseDomainSource("quality/module-graph.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, reason, pointer string
		mutate                func(map[string]any)
	}{
		{name: "null consumer", reason: "document_type_invalid", pointer: "/consumerModule", mutate: func(value map[string]any) { value["consumerModule"] = nil }},
		{name: "null modules", reason: "document_type_invalid", pointer: "/modules", mutate: func(value map[string]any) { value["modules"] = nil }},
		{name: "null module item", reason: "document_type_invalid", pointer: "/modules/0", mutate: func(value map[string]any) { value["modules"].([]any)[0] = nil }},
		{name: "null content", reason: "document_type_invalid", pointer: "/modules/0/content", mutate: func(value map[string]any) { value["modules"].([]any)[0].(map[string]any)["content"] = nil }},
		{name: "remote go mod sum required", reason: "document_required_missing", pointer: "/modules/3/content/goModSum", mutate: func(value map[string]any) {
			delete(value["modules"].([]any)[3].(map[string]any)["content"].(map[string]any), "goModSum")
		}},
		{name: "version path required", reason: "document_required_missing", pointer: "/modules/2/replacement/path", mutate: func(value map[string]any) {
			delete(value["modules"].([]any)[2].(map[string]any)["replacement"].(map[string]any), "path")
		}},
		{name: "version required", reason: "document_required_missing", pointer: "/modules/2/replacement/version", mutate: func(value map[string]any) {
			delete(value["modules"].([]any)[2].(map[string]any)["replacement"].(map[string]any), "version")
		}},
		{name: "repository path required", reason: "document_required_missing", pointer: "/modules/1/replacement/repositoryPath", mutate: func(value map[string]any) {
			delete(value["modules"].([]any)[1].(map[string]any)["replacement"].(map[string]any), "repositoryPath")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := mutateGraphJCS(t, replacementGraphGolden, test.mutate)
			_, parseErr := ParseGraph(source, []byte(document))
			assertBuildInputError(t, parseErr, "module_graph_snapshot_invalid", "decode", test.reason, test.pointer, source.String())
		})
	}
}

func TestModuleGraphSnapshotAcceptsGraphOnlyRemoteModuleWithoutZipSum(t *testing.T) {
	source, err := provenance.ParseDomainSource("quality/module-graph.json")
	if err != nil {
		t.Fatal(err)
	}
	document := mutateGraphJCS(t, replacementGraphGolden, func(value map[string]any) {
		delete(value["modules"].([]any)[3].(map[string]any)["content"].(map[string]any), "sum")
	})
	snapshot, err := ParseGraph(source, []byte(document))
	if err != nil {
		t.Fatalf("ParseGraph() rejected graph-only remote module: %v", err)
	}
	canonical, err := CanonicalGraphSnapshot(snapshot)
	if err != nil || !bytes.Equal(canonical, []byte(document)) {
		t.Fatalf("CanonicalGraphSnapshot() = %s, %v", canonical, err)
	}
}

func TestModuleGraphSnapshotAcceptsGraphOnlyRemoteModuleWithoutContentSums(t *testing.T) {
	source, err := provenance.ParseDomainSource("quality/module-graph.json")
	if err != nil {
		t.Fatal(err)
	}
	document := mutateGraphJCS(t, replacementGraphGolden, func(value map[string]any) {
		content := value["modules"].([]any)[3].(map[string]any)["content"].(map[string]any)
		delete(content, "sum")
		delete(content, "goModSum")
	})
	snapshot, err := ParseGraph(source, []byte(document))
	if err != nil {
		t.Fatalf("ParseGraph() rejected graph-only remote module: %v", err)
	}
	canonical, err := CanonicalGraphSnapshot(snapshot)
	if err != nil || !bytes.Equal(canonical, []byte(document)) {
		t.Fatalf("CanonicalGraphSnapshot() = %s, %v", canonical, err)
	}
}

func mutateGraphJCS(t *testing.T, document string, mutate func(map[string]any)) string {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(document), &value); err != nil {
		t.Fatal(err)
	}
	mutate(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(canonical)
}

func assertBuildInputError(t *testing.T, err error, code, stage, reason, pointer, source string) {
	t.Helper()
	if err == nil {
		t.Fatal("operation succeeded, want closed build-input error")
	}
	var buildErr *Error
	if !errors.As(err, &buildErr) {
		t.Fatalf("error type = %T, want *buildinput.Error", err)
	}
	if buildErr.Code() != code || buildErr.Stage() != stage || buildErr.Reason() != reason || buildErr.Pointer() != pointer || buildErr.Source() != source {
		t.Fatalf("error tuple = (%q,%q,%q,%q,%q), want (%q,%q,%q,%q,%q)", buildErr.Code(), buildErr.Stage(), buildErr.Reason(), buildErr.Pointer(), buildErr.Source(), code, stage, reason, pointer, source)
	}
}

package buildinput

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/provenance"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestRetainedBuildInputManifestCanonicalSelfDigestAndSnapshotRoundTrip(t *testing.T) {
	firstFixture := newDiscoveryFixture(t)
	first, err := Compile(firstFixture.input)
	if err != nil {
		t.Fatal(err)
	}
	firstCanonical, err := CanonicalManifest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondFixture := newDiscoveryFixture(t)
	second, err := Compile(secondFixture.input)
	if err != nil {
		t.Fatal(err)
	}
	secondCanonical, err := CanonicalManifest(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstCanonical, secondCanonical) {
		t.Fatalf("canonical manifest changed across absolute roots:\n%s\n%s", firstCanonical, secondCanonical)
	}
	if bytes.HasSuffix(firstCanonical, []byte("\n")) || bytes.Contains(firstCanonical, []byte(firstFixture.repo)) {
		t.Fatalf("canonical manifest has newline or absolute root: %s", firstCanonical)
	}
	transformed, err := jcs.Transform(firstCanonical)
	if err != nil || !bytes.Equal(transformed, firstCanonical) {
		t.Fatalf("manifest is not RFC 8785: %v", err)
	}

	var document map[string]any
	if err := json.Unmarshal(firstCanonical, &document); err != nil {
		t.Fatal(err)
	}
	storedDigest, ok := document["digest"].(string)
	if !ok {
		t.Fatal("manifest digest is missing")
	}
	delete(document, "digest")
	preimageJSON, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	preimage, err := jcs.Transform(preimageJSON)
	if err != nil {
		t.Fatal(err)
	}
	if got := provenance.SHA256(preimage).String(); got != storedDigest {
		t.Fatalf("self digest = %s, want %s", storedDigest, got)
	}
	for index, raw := range document["inputs"].([]any) {
		moduleRef := raw.(map[string]any)["module"].(map[string]any)
		if len(moduleRef) != 2 || moduleRef["path"] == nil || moduleRef["version"] == nil {
			t.Fatalf("input %d module ref = %#v, want exact path/version", index, moduleRef)
		}
	}

	source, err := provenance.ParseDomainSource("quality/retained-build-input.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ParseManifest(source, firstCanonical)
	if err != nil {
		t.Fatalf("ParseManifest() error = %v", err)
	}
	roundTrip, err := CanonicalSnapshot(snapshot)
	if err != nil || !bytes.Equal(roundTrip, firstCanonical) {
		t.Fatalf("snapshot canonical round trip = %s, %v", roundTrip, err)
	}
	inputs, err := snapshot.Inputs()
	if err != nil || len(inputs) == 0 {
		t.Fatalf("Inputs() = %#v, %v", inputs, err)
	}
	firstModule := inputs[0].Module
	inputs[0] = RetainedBuildInput{}
	again, err := snapshot.Inputs()
	if err != nil || !reflect.DeepEqual(again[0].Module, firstModule) {
		t.Fatalf("Inputs() after caller mutation = %#v, %v", again, err)
	}
}

func TestRetainedBuildInputManifestSnapshotStrictDocumentShape(t *testing.T) {
	canonical := canonicalManifestFixture(t)
	source, err := provenance.ParseDomainSource("quality/retained-build-input.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, document, reason, pointer string
	}{
		{name: "null root", document: `null`, reason: "document_type_invalid", pointer: ""},
		{name: "unknown", document: mutateManifestJCS(t, canonical, func(value map[string]any) { value["unknown"] = true }), reason: "document_unknown_field", pointer: "/unknown"},
		{name: "duplicate", document: strings.Replace(string(canonical), `"kind":"RetainedBuildInputManifest"`, `"kind":"RetainedBuildInputManifest","kind":"RetainedBuildInputManifest"`, 1), reason: "document_duplicate_key", pointer: "/kind"},
		{name: "trailing", document: string(canonical) + `{}`, reason: "document_trailing_input", pointer: ""},
		{name: "required", document: mutateManifestJCS(t, canonical, func(value map[string]any) { delete(value, "schemaImportPath") }), reason: "document_required_missing", pointer: "/schemaImportPath"},
		{name: "null inputs", document: mutateManifestJCS(t, canonical, func(value map[string]any) { value["inputs"] = nil }), reason: "document_type_invalid", pointer: "/inputs"},
		{name: "input size type", document: mutateManifestJCS(t, canonical, func(value map[string]any) { value["inputs"].([]any)[0].(map[string]any)["size"] = "one" }), reason: "document_type_invalid", pointer: "/inputs/0/size"},
		{name: "version", document: mutateManifestJCS(t, canonical, func(value map[string]any) { value["apiVersion"] = "nexa.dev/retained-build-input-manifest/v2" }), reason: "version_unsupported", pointer: "/apiVersion"},
		{name: "kind", document: mutateManifestJCS(t, canonical, func(value map[string]any) { value["kind"] = "Other" }), reason: "kind_invalid", pointer: "/kind"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, parseErr := ParseManifest(source, []byte(test.document))
			assertBuildInputError(t, parseErr, "build_input_snapshot_invalid", "decode", test.reason, test.pointer, source.String())
		})
	}
	invalidUnicode := append([]byte(nil), canonical...)
	index := bytes.Index(invalidUnicode, []byte("RetainedBuildInputManifest"))
	if index < 0 {
		t.Fatal("kind not found")
	}
	invalidUnicode[index] = 0xff
	_, err = ParseManifest(source, invalidUnicode)
	assertBuildInputError(t, err, "build_input_snapshot_invalid", "decode", "unicode_invalid", "", source.String())
}

func TestRetainedBuildInputManifestSnapshotRejectsZeroSourceBeforeDocumentBytes(t *testing.T) {
	canonical := canonicalManifestFixture(t)
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "valid canonical", data: canonical},
		{name: "malformed bytes", data: []byte{0xff, '{'}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseManifest(provenance.DomainSource{}, test.data)
			assertBuildInputError(t, err, "build_input_snapshot_invalid", "decode", "document_invalid", "", "")
		})
	}
}

func TestRetainedBuildInputManifestSnapshotEnforcesSemanticCeilings(t *testing.T) {
	canonical := canonicalManifestFixture(t)
	source, err := provenance.ParseDomainSource("quality/retained-build-input.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, reason, pointer string
		mutate                func(map[string]any)
	}{
		{name: "local module count", reason: "retained_module_invalid", pointer: "/localModules/64", mutate: func(value map[string]any) {
			modules := value["localModules"].([]any)
			for len(modules) <= MaxRetainedModuleRoots {
				index := len(modules)
				path := "example.com/extra/module" + strconv.Itoa(index)
				repositoryPath := "extra/module" + strconv.Itoa(index)
				modules = append(modules, map[string]any{
					"module": map[string]any{
						"content": map[string]any{"kind": "local"}, "path": path,
						"replacement": map[string]any{"kind": "repository", "repositoryPath": repositoryPath}, "version": "v1.0.0",
					},
					"repositoryPath": repositoryPath, "role": "repository-module",
				})
			}
			value["localModules"] = modules
		}},
		{name: "input count", reason: "retained_input_invalid", pointer: "/inputs/8192", mutate: func(value map[string]any) {
			inputs := value["inputs"].([]any)
			template := cloneManifestObject(inputs[0].(map[string]any))
			for len(inputs) <= MaxRetainedBuildInputs {
				item := cloneManifestObject(template)
				item["path"] = "generated/member-" + strconv.Itoa(len(inputs)) + ".go"
				item["kind"], item["size"] = "go", float64(0)
				inputs = append(inputs, item)
			}
			value["inputs"] = inputs
		}},
		{name: "single input size", reason: "retained_input_invalid", pointer: "/inputs/0/size", mutate: func(value map[string]any) {
			value["inputs"].([]any)[0].(map[string]any)["size"] = float64(MaxRetainedBuildInputBytes + 1)
		}},
		{name: "declaration order total size", reason: "retained_input_invalid", pointer: "/inputs/16/size", mutate: func(value map[string]any) {
			template := cloneManifestObject(value["inputs"].([]any)[0].(map[string]any))
			inputs := make([]any, 17)
			for index := range inputs {
				item := cloneManifestObject(template)
				item["path"] = "total/member-" + strconv.Itoa(1000+index) + ".go"
				item["kind"], item["size"] = "go", float64(MaxRetainedBuildInputBytes)
				inputs[index] = item
			}
			value["inputs"] = inputs
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := mutateManifestJCS(t, canonical, test.mutate)
			_, parseErr := ParseManifest(source, []byte(document))
			assertBuildInputError(t, parseErr, "build_input_snapshot_invalid", "decode", test.reason, test.pointer, source.String())
		})
	}
}

func TestRetainedBuildInputManifestSnapshotPreservesClosedUnionMemberPresence(t *testing.T) {
	canonical := canonicalManifestFixture(t)
	source, err := provenance.ParseDomainSource("quality/retained-build-input.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, pointer string
		mutate        func(map[string]any)
	}{
		{name: "scratch explicit empty repository path", pointer: "/localModules/1/repositoryPath", mutate: func(value map[string]any) {
			value["localModules"].([]any)[1].(map[string]any)["repositoryPath"] = ""
		}},
		{name: "local content explicit empty sum", pointer: "/localModules/0/module/content/sum", mutate: func(value map[string]any) {
			value["localModules"].([]any)[0].(map[string]any)["module"].(map[string]any)["content"].(map[string]any)["sum"] = ""
		}},
		{name: "local content explicit empty go mod sum", pointer: "/localModules/0/module/content/goModSum", mutate: func(value map[string]any) {
			value["localModules"].([]any)[0].(map[string]any)["module"].(map[string]any)["content"].(map[string]any)["goModSum"] = ""
		}},
		{name: "repository replacement explicit empty path", pointer: "/localModules/0/module/replacement/path", mutate: func(value map[string]any) {
			value["localModules"].([]any)[0].(map[string]any)["module"].(map[string]any)["replacement"].(map[string]any)["path"] = ""
		}},
		{name: "repository replacement explicit empty version", pointer: "/localModules/0/module/replacement/version", mutate: func(value map[string]any) {
			value["localModules"].([]any)[0].(map[string]any)["module"].(map[string]any)["replacement"].(map[string]any)["version"] = ""
		}},
		{name: "scratch none replacement explicit empty path", pointer: "/localModules/1/module/replacement/path", mutate: func(value map[string]any) {
			value["localModules"].([]any)[1].(map[string]any)["module"].(map[string]any)["replacement"].(map[string]any)["path"] = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := mutateManifestJCS(t, canonical, test.mutate)
			_, parseErr := ParseManifest(source, []byte(document))
			assertBuildInputError(t, parseErr, "build_input_snapshot_invalid", "decode", "retained_module_invalid", test.pointer, source.String())
		})
	}
}

func TestRetainedBuildInputManifestSnapshotNestedSemanticPrecedence(t *testing.T) {
	canonical := canonicalManifestFixture(t)
	source, err := provenance.ParseDomainSource("quality/retained-build-input.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, reason, pointer string
		mutate                func(map[string]any)
	}{
		{name: "repository path required", reason: "retained_module_invalid", pointer: "/localModules/0/repositoryPath", mutate: func(value map[string]any) {
			delete(value["localModules"].([]any)[0].(map[string]any), "repositoryPath")
		}},
		{name: "scratch repository path forbidden", reason: "retained_module_invalid", pointer: "/localModules/1/repositoryPath", mutate: func(value map[string]any) {
			value["localModules"].([]any)[1].(map[string]any)["repositoryPath"] = "scratch"
		}},
		{name: "duplicate local module", reason: "retained_module_duplicate", pointer: "/localModules/2/module", mutate: func(value map[string]any) {
			modules := value["localModules"].([]any)
			value["localModules"] = append(modules, modules[0])
		}},
		{name: "missing input ref", reason: "retained_input_invalid", pointer: "/inputs/0/module", mutate: func(value map[string]any) {
			value["inputs"].([]any)[0].(map[string]any)["module"].(map[string]any)["path"] = "example.com/missing"
		}},
		{name: "duplicate input path", reason: "retained_input_duplicate", pointer: "/inputs/4/path", mutate: func(value map[string]any) {
			inputs := value["inputs"].([]any)
			duplicate := cloneManifestObject(inputs[0].(map[string]any))
			duplicate["kind"] = "embed"
			value["inputs"] = append(inputs, duplicate)
		}},
		{name: "ancestor overlap", reason: "retained_input_invalid", pointer: "/inputs/1/path", mutate: func(value map[string]any) {
			inputs := value["inputs"].([]any)
			inputs[0].(map[string]any)["path"] = "ancestor"
			inputs[1].(map[string]any)["path"] = "ancestor/child"
		}},
		{name: "local module order", reason: "canonical_order_invalid", pointer: "", mutate: func(value map[string]any) {
			modules := value["localModules"].([]any)
			modules[0], modules[1] = modules[1], modules[0]
		}},
		{name: "self digest", reason: "digest_mismatch", pointer: "/digest", mutate: func(value map[string]any) {
			value["digest"] = provenance.SHA256([]byte("wrong")).String()
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := mutateManifestJCS(t, canonical, test.mutate)
			_, parseErr := ParseManifest(source, []byte(document))
			assertBuildInputError(t, parseErr, "build_input_snapshot_invalid", "decode", test.reason, test.pointer, source.String())
		})
	}

	withWhitespace := append(append([]byte(nil), canonical...), '\n')
	_, err = ParseManifest(source, withWhitespace)
	assertBuildInputError(t, err, "build_input_snapshot_invalid", "decode", "canonical_invalid", "", source.String())
}

func TestRetainedBuildInputManifestSnapshotCompoundSemanticPrecedence(t *testing.T) {
	canonical := canonicalManifestFixture(t)
	source, err := provenance.ParseDomainSource("quality/retained-build-input.json")
	if err != nil {
		t.Fatal(err)
	}
	badDigest := "sha256:not-a-digest"
	tests := []struct {
		name, reason, pointer string
		mutate                func(map[string]any)
	}{
		{name: "local semantic before input digest", reason: "retained_module_invalid", pointer: "/localModules/0/repositoryPath", mutate: func(value map[string]any) {
			delete(value["localModules"].([]any)[0].(map[string]any), "repositoryPath")
			value["inputs"].([]any)[0].(map[string]any)["digest"] = badDigest
		}},
		{name: "local duplicate before input digest", reason: "retained_module_duplicate", pointer: "/localModules/2/module", mutate: func(value map[string]any) {
			modules := value["localModules"].([]any)
			value["localModules"] = append(modules, modules[0])
			value["inputs"].([]any)[0].(map[string]any)["digest"] = badDigest
		}},
		{name: "missing ref before its digest", reason: "retained_input_invalid", pointer: "/inputs/0/module", mutate: func(value map[string]any) {
			input := value["inputs"].([]any)[0].(map[string]any)
			input["module"].(map[string]any)["path"] = "example.com/missing"
			input["digest"] = badDigest
		}},
		{name: "later missing ref before earlier digest", reason: "retained_input_invalid", pointer: "/inputs/1/module", mutate: func(value map[string]any) {
			inputs := value["inputs"].([]any)
			inputs[0].(map[string]any)["digest"] = badDigest
			inputs[1].(map[string]any)["module"].(map[string]any)["path"] = "example.com/missing"
		}},
		{name: "duplicate path before kind size and digest", reason: "retained_input_duplicate", pointer: "/inputs/4/path", mutate: func(value map[string]any) {
			inputs := value["inputs"].([]any)
			inputs[0].(map[string]any)["digest"] = badDigest
			duplicate := cloneManifestObject(inputs[0].(map[string]any))
			duplicate["kind"] = "invalid"
			duplicate["size"] = float64(-1)
			value["inputs"] = append(inputs, duplicate)
		}},
		{name: "overlap before kind size and digest", reason: "retained_input_invalid", pointer: "/inputs/1/path", mutate: func(value map[string]any) {
			inputs := value["inputs"].([]any)
			inputs[0].(map[string]any)["path"] = "ancestor"
			inputs[0].(map[string]any)["digest"] = badDigest
			inputs[1].(map[string]any)["path"] = "ancestor/child"
			inputs[1].(map[string]any)["kind"] = "invalid"
			inputs[1].(map[string]any)["size"] = float64(-1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := mutateManifestJCS(t, canonical, test.mutate)
			_, parseErr := ParseManifest(source, []byte(document))
			assertBuildInputError(t, parseErr, "build_input_snapshot_invalid", "decode", test.reason, test.pointer, source.String())
		})
	}
}

func TestRetainedBuildInputManifestConstructorSemanticPrecedence(t *testing.T) {
	tests := []struct {
		name, reason, pointer string
		mutate                func(*manifestState)
	}{
		{name: "duplicate local module", reason: "retained_module_duplicate", pointer: "/retainedModules/2", mutate: func(state *manifestState) {
			state.localModules = append(state.localModules, state.localModules[0])
		}},
		{name: "missing input module", reason: "retained_module_invalid", pointer: "/retainedInputs/0/module", mutate: func(state *manifestState) {
			state.inputs[0].Module.Module.Path = "example.com/missing"
		}},
		{name: "duplicate path", reason: "retained_path_duplicate", pointer: "/retainedInputs/1/path", mutate: func(state *manifestState) {
			state.inputs[1].Path = state.inputs[0].Path
			state.inputs[1].Kind = "embed"
		}},
		{name: "ancestor overlap", reason: "retained_path_invalid", pointer: "/retainedInputs/1/path", mutate: func(state *manifestState) {
			state.inputs[0].Path = "ancestor"
			state.inputs[1].Path = "ancestor/child"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDiscoveryFixture(t)
			compilation, err := Compile(fixture.input)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(compilation.manifest.state)
			_, err = CanonicalManifest(compilation)
			assertBuildInputError(t, err, "build_input_invalid", "retain", test.reason, test.pointer, "")
		})
	}
}

func TestRetainedBuildInputManifestConstructorEnforcesSemanticCeilings(t *testing.T) {
	tests := []struct {
		name, reason, pointer string
		mutate                func(*manifestState)
	}{
		{name: "local module count", reason: "retained_module_invalid", pointer: "/retainedModules/64", mutate: func(state *manifestState) {
			for len(state.localModules) <= MaxRetainedModuleRoots {
				index := len(state.localModules)
				repositoryPath := "extra/module" + strconv.Itoa(index)
				state.localModules = append(state.localModules, RetainedLocalModule{
					Role: "repository-module", RepositoryPath: repositoryPath, HasRepositoryPath: true,
					Module: ModuleIdentity{Path: "example.com/extra/module" + strconv.Itoa(index), Version: "v1.0.0", Replacement: ModuleReplacement{Kind: "repository", RepositoryPath: repositoryPath}, Content: ModuleContent{Kind: "local"}},
				})
			}
		}},
		{name: "input count", reason: "retained_input_limit_exceeded", pointer: "/retainedInputs/8192", mutate: func(state *manifestState) {
			template := state.inputs[0]
			for len(state.inputs) <= MaxRetainedBuildInputs {
				item := template
				item.Path, item.Kind, item.Size = "generated/member-"+strconv.Itoa(len(state.inputs))+".go", "go", 0
				state.inputs = append(state.inputs, item)
			}
		}},
		{name: "single input size", reason: "retained_input_limit_exceeded", pointer: "/retainedInputs/0", mutate: func(state *manifestState) {
			state.inputs[0].Size = MaxRetainedBuildInputBytes + 1
		}},
		{name: "declaration order total size", reason: "retained_input_limit_exceeded", pointer: "/retainedInputs/16", mutate: func(state *manifestState) {
			template := state.inputs[0]
			state.inputs = make([]RetainedBuildInput, 17)
			for index := range state.inputs {
				item := template
				item.Path, item.Kind, item.Size = "total/member-"+strconv.Itoa(1000+index)+".go", "go", MaxRetainedBuildInputBytes
				state.inputs[index] = item
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDiscoveryFixture(t)
			compilation, err := Compile(fixture.input)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(compilation.manifest.state)
			_, err = CanonicalManifest(compilation)
			assertBuildInputError(t, err, "build_input_invalid", "retain", test.reason, test.pointer, "")
		})
	}
}

func TestRetainedBuildInputManifestSchemaIsDefensiveAndValidatesNestedWire(t *testing.T) {
	first, second := ManifestSchema(), ManifestSchema()
	if len(first) == 0 || !bytes.Equal(first, second) {
		t.Fatal("ManifestSchema() is empty or unstable")
	}
	first[0] ^= 0xff
	if bytes.Equal(first, ManifestSchema()) {
		t.Fatal("ManifestSchema() did not return a defensive copy")
	}
	var schemaDocument any
	if err := json.Unmarshal(ManifestSchema(), &schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "https://nexa.dev/schemas/generation/toolchain/retained-build-input-manifest-v1.schema.json"
	if err := compiler.AddResource(schemaURL, schemaDocument); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	canonical := canonicalManifestFixture(t)
	var valid any
	if err := json.Unmarshal(canonical, &valid); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(valid); err != nil {
		t.Fatalf("schema rejected canonical manifest: %v", err)
	}

	var invalid map[string]any
	if err := json.Unmarshal(canonical, &invalid); err != nil {
		t.Fatal(err)
	}
	invalid["inputs"].([]any)[0].(map[string]any)["module"].(map[string]any)["content"] = map[string]any{"kind": "local"}
	if err := schema.Validate(invalid); err == nil {
		t.Fatal("schema accepted full identity in input.module ref")
	}
	delete(invalid["inputs"].([]any)[0].(map[string]any)["module"].(map[string]any), "content")
	invalid["localModules"].([]any)[1].(map[string]any)["repositoryPath"] = "scratch"
	if err := schema.Validate(invalid); err == nil {
		t.Fatal("schema accepted repositoryPath on scratch-main")
	}
}

func cloneManifestObject(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func canonicalManifestFixture(t *testing.T) []byte {
	t.Helper()
	fixture := newDiscoveryFixture(t)
	compilation, err := Compile(fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalManifest(compilation)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func mutateManifestJCS(t *testing.T, canonical []byte, mutate func(map[string]any)) string {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(canonical, &value); err != nil {
		t.Fatal(err)
	}
	mutate(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	result, err := jcs.Transform(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(result)
}

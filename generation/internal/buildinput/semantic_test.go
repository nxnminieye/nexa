package buildinput

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nxnminieye/nexa/provenance"
)

func TestCompileRetainsOnlySelectedPureGoEmbedAndModuleBoundaries(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	compilation, err := Compile(fixture.input)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	graph, err := compilation.Graph()
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := graph.ConsumerModule()
	if err != nil || consumer != (ModuleRequirement{Path: "example.com/acme/consumer", Version: "v0.0.0"}) {
		t.Fatalf("ConsumerModule() = %#v, %v", consumer, err)
	}
	tool, err := graph.ToolModule()
	if err != nil || tool != fixture.input.ToolModule {
		t.Fatalf("ToolModule() = %#v, %v", tool, err)
	}
	sources, err := graph.ModuleSources()
	if err != nil {
		t.Fatal(err)
	}
	gotSourceRefs := make([]string, len(sources))
	for index, source := range sources {
		gotSourceRefs[index] = source.Ref.String()
	}
	if want := []string{"repo:go.mod", "repo:go.sum"}; !reflect.DeepEqual(gotSourceRefs, want) {
		t.Fatalf("module source refs = %#v, want %#v", gotSourceRefs, want)
	}

	manifest, err := compilation.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	buildTags, err := manifest.BuildTags()
	if err != nil || !reflect.DeepEqual(buildTags, []string{"alpha", "zeta"}) {
		t.Fatalf("BuildTags() = %#v, %v", buildTags, err)
	}
	buildTags[0] = "mutated"
	again, err := manifest.BuildTags()
	if err != nil || !reflect.DeepEqual(again, []string{"alpha", "zeta"}) {
		t.Fatalf("BuildTags() after caller mutation = %#v, %v", again, err)
	}
	modules, err := manifest.LocalModules()
	if err != nil || len(modules) != 2 {
		t.Fatalf("LocalModules() = %#v, %v", modules, err)
	}
	roles := []string{modules[0].Role, modules[1].Role}
	sort.Strings(roles)
	if !reflect.DeepEqual(roles, []string{"repository-module", "scratch-main"}) {
		t.Fatalf("local module roles = %#v", roles)
	}
	inputs, err := manifest.Inputs()
	if err != nil {
		t.Fatal(err)
	}
	gotInputs := make([]string, len(inputs))
	for index, input := range inputs {
		gotInputs[index] = input.Module.Module.Path + ":" + input.Path + ":" + input.Kind
		if input.Size < 0 || input.Digest.String() == "" {
			t.Fatalf("retained input %d has invalid size/digest: %#v", index, input)
		}
	}
	sort.Strings(gotInputs)
	wantInputs := []string{
		"example.com/acme/consumer:go.mod:module-file",
		"example.com/acme/consumer:go.sum:module-sum",
		"example.com/acme/consumer:schema/models/schema.go:go",
		"example.com/acme/consumer:schema/models/vendor/data.txt:embed",
	}
	sort.Strings(wantInputs)
	if !reflect.DeepEqual(gotInputs, wantInputs) {
		t.Fatalf("retained inputs = %#v, want %#v", gotInputs, wantInputs)
	}
	for _, input := range gotInputs {
		if strings.Contains(input, "schema_test.go") || strings.Contains(input, "ignored.go") || strings.Contains(input, "unrelated") {
			t.Fatalf("unretained input leaked into manifest: %q", input)
		}
		if strings.HasPrefix(input, "example.com/ent-helper:go.mod:") {
			t.Fatalf("derived scratch module boundary entered manifest: %q", input)
		}
	}
}

func TestCompileRetainsScratchHelperSourcesWithoutDerivedModuleBoundaries(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	helper := filepath.Join(fixture.input.ScratchRoot, "cmd", "enthelper", "main.go")
	mustWriteFile(t, helper, []byte("package main\n\nfunc main() {}\n"))
	mustWriteFile(t, filepath.Join(fixture.input.ScratchRoot, "go.sum"), []byte("example.com/dependency v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"))
	packages := decodeTestJSONStream(t, fixture.input.PackageList)
	packages = append(packages, map[string]any{
		"Dir": filepath.Dir(helper), "ImportPath": "example.com/ent-helper/cmd/enthelper", "Name": "main",
		"Module": map[string]any{
			"Path": "example.com/ent-helper", "Main": true, "Dir": fixture.input.ScratchRoot,
			"GoMod": filepath.Join(fixture.input.ScratchRoot, "go.mod"), "GoVersion": "1.25",
		},
		"GoFiles": []string{"main.go"},
	})
	fixture.input.PackageList = jsonStream(t, packages...)
	compilation, err := Compile(fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := compilation.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := manifest.Inputs()
	if err != nil {
		t.Fatal(err)
	}
	foundHelper := false
	for _, input := range inputs {
		if input.Module.Role == "scratch-main" && (input.Kind == "module-file" || input.Kind == "module-sum") {
			t.Fatalf("derived scratch module boundary entered manifest: %#v", input)
		}
		foundHelper = foundHelper || input.Module.Role == "scratch-main" && input.Path == "cmd/enthelper/main.go" && input.Kind == "go"
	}
	if !foundHelper {
		t.Fatalf("scratch helper source missing from retained inputs: %#v", inputs)
	}
}

func TestCompileRejectsEveryActiveNativeInputField(t *testing.T) {
	fields := []string{"CgoFiles", "CFiles", "CXXFiles", "MFiles", "HFiles", "FFiles", "SFiles", "SwigFiles", "SwigCXXFiles", "SysoFiles"}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			fixture := newDiscoveryFixture(t)
			var pkg map[string]any
			if err := json.Unmarshal(fixture.input.PackageList, &pkg); err != nil {
				t.Fatal(err)
			}
			pkg[field] = []string{"native.input"}
			fixture.input.PackageList = jsonStream(t, pkg)
			_, err := Compile(fixture.input)
			assertBuildInputError(t, err, "build_input_unsupported", "retain", "native_input_unsupported", "/retainedInputs/packages/0/"+field+"/0", "")
		})
	}
}

func TestCompileBindsStandardLibraryToGoIdentityWithoutRetainingNativeMembers(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	packages := decodeTestJSONStream(t, fixture.input.PackageList)
	packages = append(packages, map[string]any{
		"Dir": "/goroot/src/runtime", "ImportPath": "runtime", "Name": "runtime", "Standard": true,
		"GoFiles": []string{"runtime.go"}, "SFiles": []string{"asm.s"}, "CgoFiles": []string{"cgo.go"},
	})
	fixture.input.PackageList = jsonStream(t, packages...)
	compilation, err := Compile(fixture.input)
	if err != nil {
		t.Fatalf("Compile() rejected standard-library native members: %v", err)
	}
	manifest, err := compilation.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := manifest.Inputs()
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range inputs {
		if input.Module.Module.Path == "runtime" || input.Path == "runtime.go" || input.Path == "asm.s" || input.Path == "cgo.go" {
			t.Fatalf("standard-library member entered retained inputs: %#v", input)
		}
	}
}

func TestCompileAppliesSelectedInputCountLimitBeforeReadingMembers(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	var pkg map[string]any
	if err := json.Unmarshal(fixture.input.PackageList, &pkg); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, MaxRetainedBuildInputs+1)
	for index := range paths {
		paths[index] = "generated/member-" + strconv.Itoa(index) + ".go"
	}
	pkg["GoFiles"] = paths
	pkg["EmbedFiles"] = []string{}
	fixture.input.PackageList = jsonStream(t, pkg)
	_, err := Compile(fixture.input)
	assertBuildInputError(t, err, "build_input_invalid", "retain", "retained_input_limit_exceeded", "/retainedInputs/8192", "")
}

func TestCompileAppliesTotalByteBudgetDuringDeclarationOrderReads(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	pkg := decodeTestJSONStream(t, fixture.input.PackageList)[0].(map[string]any)
	members := make([]string, 18)
	for index := range members {
		members[index] = "generated/member-" + strconv.Itoa(index) + ".go"
	}
	pkg["GoFiles"], pkg["EmbedFiles"] = members, []string{}
	fixture.input.PackageList = jsonStream(t, pkg)
	payload := bytes.Repeat([]byte{'x'}, MaxRetainedBuildInputBytes)
	var packageLimits []int64
	var packageNames []string
	fixture.input.readFile = func(root, name string, limit int64, pointer string) ([]byte, error) {
		if filepath.Base(name) == "go.mod" || filepath.Base(name) == "go.sum" {
			return readSemanticFile(root, name, limit, pointer)
		}
		packageLimits = append(packageLimits, limit)
		packageNames = append(packageNames, name)
		return payload, nil
	}
	_, err := Compile(fixture.input)
	assertBuildInputError(t, err, "build_input_invalid", "retain", "retained_input_limit_exceeded", "/retainedInputs/17", "")
	if len(packageLimits) != 16 {
		t.Fatalf("package reads = %d (%#v), want 16 through first total-budget offender", len(packageLimits), packageNames)
	}
	if packageLimits[15] >= MaxRetainedBuildInputBytes || packageLimits[15] <= 0 {
		t.Fatalf("first offender read limit = %d, want remaining total budget below %d", packageLimits[15], MaxRetainedBuildInputBytes)
	}
	for _, name := range packageNames {
		if strings.HasSuffix(name, "member-16.go") || strings.HasSuffix(name, "member-17.go") {
			t.Fatalf("reader was called after total-budget failure: %q", name)
		}
	}
}

func TestCompileRejectsMismatchedLocalReplacementModulePath(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	nested := filepath.Join(fixture.repo, "localdeps/helper")
	mustWriteFile(t, filepath.Join(nested, "go.mod"), []byte("module example.com/acme/wrong\n\ngo 1.25\n"))
	modules := decodeTestJSONStream(t, fixture.input.ModuleList)
	modules = append(modules, map[string]any{
		"Path": "example.com/acme/helper", "Version": "v1.0.0", "Dir": nested, "GoMod": filepath.Join(nested, "go.mod"),
		"Replace": map[string]any{"Path": "example.com/acme/helper", "Dir": nested, "GoMod": filepath.Join(nested, "go.mod")},
	})
	fixture.input.ModuleList = jsonStream(t, modules...)
	_, err := Compile(fixture.input)
	assertBuildInputError(t, err, "build_input_invalid", "retain", "normalized_module_file_invalid", "/buildInputs/normalized/goMod", "")
}

func TestCompileRemoteReplacementSumsEnterGraphDigestAndUseCanonicalPointer(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	modules := decodeTestJSONStream(t, fixture.input.ModuleList)
	tool := modules[2].(map[string]any)
	delete(tool, "Sum")
	delete(tool, "GoModSum")
	tool["Replace"] = map[string]any{
		"Path": "github.com/nxnminieye/nexa-fork", "Version": "v0.1.1",
		"Sum":      "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"GoModSum": "h1:MzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzM=",
	}
	fixture.input.ModuleList = jsonStream(t, modules...)
	first, err := Compile(fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	firstGraph, _ := first.Graph()
	firstJSON, err := CanonicalGraphSnapshot(firstGraph)
	if err != nil {
		t.Fatal(err)
	}
	firstDigest := provenance.SHA256(firstJSON)

	replacement := tool["Replace"].(map[string]any)
	replacement["Sum"] = "h1:IiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiI="
	fixture.input.ModuleList = jsonStream(t, modules...)
	second, err := Compile(fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	secondGraph, _ := second.Graph()
	secondJSON, err := CanonicalGraphSnapshot(secondGraph)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == provenance.SHA256(secondJSON) {
		t.Fatal("replacement Sum change did not change ModuleGraph digest")
	}

	delete(replacement, "GoModSum")
	fixture.input.ModuleList = jsonStream(t, modules...)
	_, err = Compile(fixture.input)
	assertBuildInputError(t, err, "build_input_invalid", "retain", "remote_go_mod_sum_invalid", "/moduleGraph/modules/1/content/goModSum", "")
}

func TestCompileAcceptsGraphOnlyRemoteModuleWithoutContentSums(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	modules := decodeTestJSONStream(t, fixture.input.ModuleList)
	tool := modules[2].(map[string]any)
	delete(tool, "Dir")
	delete(tool, "GoMod")
	delete(tool, "Sum")
	delete(tool, "GoModSum")
	fixture.input.ModuleList = jsonStream(t, modules...)

	compilation, err := Compile(fixture.input)
	if err != nil {
		t.Fatalf("Compile() rejected graph-only remote module: %v", err)
	}
	graph, err := compilation.Graph()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalGraphSnapshot(graph)
	if err != nil {
		t.Fatal(err)
	}
	source, err := provenance.ParseDomainSource("quality/module-graph.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseGraph(source, canonical); err != nil {
		t.Fatalf("ParseGraph() rejected compiled graph-only module: %v", err)
	}
}

func TestCompileRejectsNonCanonicalRemoteH1Sums(t *testing.T) {
	t.Run("canonical", func(t *testing.T) {
		fixture := newDiscoveryFixture(t)
		if _, err := Compile(fixture.input); err != nil {
			t.Fatalf("Compile() rejected canonical h1 values: %v", err)
		}
	})
	for _, field := range []struct {
		name, jsonName, pointerName, reason string
	}{{"sum", "Sum", "sum", "remote_sum_invalid"}, {"go mod sum", "GoModSum", "goModSum", "remote_go_mod_sum_invalid"}} {
		for _, vector := range invalidH1Vectors() {
			t.Run(field.name+"/"+vector.name, func(t *testing.T) {
				fixture := newDiscoveryFixture(t)
				modules := decodeTestJSONStream(t, fixture.input.ModuleList)
				modules[2].(map[string]any)[field.jsonName] = vector.value
				fixture.input.ModuleList = jsonStream(t, modules...)
				_, err := Compile(fixture.input)
				assertBuildInputError(t, err, "build_input_invalid", "retain", field.reason, "/moduleGraph/modules/1/content/"+field.pointerName, "")
			})
		}
	}
}

func TestCompileRejectsDuplicateKeysInGoListStreams(t *testing.T) {
	tests := []struct {
		name, reason, pointer string
		mutate                func(*DiscoveryInput)
	}{
		{
			name: "module stream", reason: "module_list_output_invalid", pointer: "/moduleGraph",
			mutate: func(input *DiscoveryInput) {
				input.ModuleList = bytes.Replace(input.ModuleList, []byte(`"Path":"example.com/ent-helper"`), []byte(`"Path":"example.com/ent-helper","Path":"example.com/ent-helper"`), 1)
			},
		},
		{
			name: "package stream", reason: "package_list_output_invalid", pointer: "/retainedInputs",
			mutate: func(input *DiscoveryInput) {
				input.PackageList = bytes.Replace(input.PackageList, []byte(`"ImportPath":"example.com/acme/consumer/schema/models"`), []byte(`"ImportPath":"example.com/acme/consumer/schema/models","ImportPath":"example.com/acme/consumer/schema/models"`), 1)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDiscoveryFixture(t)
			test.mutate(&fixture.input)
			_, err := Compile(fixture.input)
			assertBuildInputError(t, err, "build_input_invalid", "retain", test.reason, test.pointer, "")
		})
	}
}

func TestCompileValidatesDuplicateAndOverlappingPathsBeforeReading(t *testing.T) {
	tests := []struct {
		name, reason string
		paths        []string
	}{
		{name: "duplicate", reason: "retained_path_duplicate", paths: []string{"missing.go", "missing.go"}},
		{name: "ancestor overlap", reason: "retained_path_invalid", paths: []string{"missing", "missing/child.go"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDiscoveryFixture(t)
			var pkg map[string]any
			if err := json.Unmarshal(fixture.input.PackageList, &pkg); err != nil {
				t.Fatal(err)
			}
			pkg["GoFiles"] = test.paths
			pkg["EmbedFiles"] = []string{}
			fixture.input.PackageList = jsonStream(t, pkg)
			_, err := Compile(fixture.input)
			assertBuildInputError(t, err, "build_input_invalid", "retain", test.reason, "/retainedInputs/3/path", "")
		})
	}
}

func TestCompileSortsRetainedModulesBySelectedIdentityBeforeRole(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	nested := filepath.Join(fixture.repo, "localdeps/zeta")
	mustWriteFile(t, filepath.Join(nested, "go.mod"), []byte("module z.example/zeta\n\ngo 1.25\n"))
	modules := decodeTestJSONStream(t, fixture.input.ModuleList)
	modules = append(modules, map[string]any{
		"Path": "z.example/zeta", "Version": "v1.0.0", "Dir": nested, "GoMod": filepath.Join(nested, "go.mod"),
		"Replace": map[string]any{"Path": "z.example/zeta", "Dir": nested, "GoMod": filepath.Join(nested, "go.mod")},
	})
	fixture.input.ModuleList = jsonStream(t, modules...)
	compilation, err := Compile(fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := compilation.Manifest()
	localModules, err := manifest.LocalModules()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(localModules))
	for index, local := range localModules {
		got[index] = local.Module.Path + "@" + local.Module.Version + ":" + local.Role + ":" + local.RepositoryPath
	}
	want := []string{
		"example.com/acme/consumer@v0.0.0:repository-module:.",
		"example.com/ent-helper@:scratch-main:",
		"z.example/zeta@v1.0.0:repository-module:localdeps/zeta",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained module order = %#v, want %#v", got, want)
	}
}

func TestCompileRejectsDuplicateModuleSourceRefEvenWhenDigestMatches(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	modules := decodeTestJSONStream(t, fixture.input.ModuleList)
	consumer := modules[1].(map[string]any)
	second := make(map[string]any, len(consumer))
	for key, value := range consumer {
		second[key] = value
	}
	second["Version"] = "v1.0.0"
	modules = append(modules, second)
	fixture.input.ModuleList = jsonStream(t, modules...)
	_, err := Compile(fixture.input)
	assertBuildInputError(t, err, "build_input_invalid", "retain", "module_graph_state_invalid", "/moduleGraph", "")
}

func TestCompileRequiresExactPackageModuleIdentityFromModuleGraph(t *testing.T) {
	t.Run("local replacement", func(t *testing.T) {
		fixture := newDiscoveryFixture(t)
		pkg := decodeTestJSONStream(t, fixture.input.PackageList)[0].(map[string]any)
		pkgModule := pkg["Module"].(map[string]any)
		pkgModule["Replace"].(map[string]any)["Dir"] = filepath.Join(fixture.repo, "other")
		fixture.input.PackageList = jsonStream(t, pkg)
		_, err := Compile(fixture.input)
		assertBuildInputError(t, err, "build_input_invalid", "retain", "package_identity_invalid", "/retainedInputs/packages/0", "")
	})

	t.Run("remote content", func(t *testing.T) {
		fixture := newDiscoveryFixture(t)
		packages := decodeTestJSONStream(t, fixture.input.PackageList)
		modules := decodeTestJSONStream(t, fixture.input.ModuleList)
		tool := modules[2].(map[string]any)
		remotePackageModule := cloneJSONMap(tool)
		remotePackageModule["Sum"] = "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
		packages = append(packages, map[string]any{"Dir": "/gomodcache/nexa", "ImportPath": "github.com/nxnminieye/nexa/provenance", "Name": "provenance", "Module": remotePackageModule, "GoFiles": []string{"digest.go"}})
		fixture.input.PackageList = jsonStream(t, packages...)
		_, err := Compile(fixture.input)
		assertBuildInputError(t, err, "build_input_invalid", "retain", "package_identity_invalid", "/retainedInputs/packages/1", "")
	})

	t.Run("version replacement", func(t *testing.T) {
		fixture := newDiscoveryFixture(t)
		packages := decodeTestJSONStream(t, fixture.input.PackageList)
		modules := decodeTestJSONStream(t, fixture.input.ModuleList)
		tool := modules[2].(map[string]any)
		delete(tool, "Sum")
		delete(tool, "GoModSum")
		tool["Replace"] = map[string]any{
			"Path": "github.com/nxnminieye/nexa-fork", "Version": "v0.1.1", "Dir": "/gomodcache/nexa-fork",
			"GoMod": "/gomodcache/cache/nexa-fork.mod", "Sum": "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", "GoModSum": "h1:MzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzM=",
		}
		packageModule := cloneJSONMap(tool)
		packageModule["Replace"] = cloneJSONMap(tool["Replace"].(map[string]any))
		packageModule["Replace"].(map[string]any)["Version"] = "v0.1.2"
		packages = append(packages, map[string]any{"Dir": "/gomodcache/nexa-fork", "ImportPath": "github.com/nxnminieye/nexa/provenance", "Name": "provenance", "Module": packageModule, "GoFiles": []string{"digest.go"}})
		fixture.input.ModuleList = jsonStream(t, modules...)
		fixture.input.PackageList = jsonStream(t, packages...)
		_, err := Compile(fixture.input)
		assertBuildInputError(t, err, "build_input_invalid", "retain", "package_identity_invalid", "/retainedInputs/packages/1", "")
	})
}

func TestCompileRequiresReportedLocalGoModAtExactModuleRoot(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	alternate := filepath.Join(fixture.repo, "alternate.mod")
	mustWriteFile(t, alternate, []byte("module example.com/acme/consumer\n\ngo 1.25\ntoolchain go1.25.0\n"))
	modules := decodeTestJSONStream(t, fixture.input.ModuleList)
	consumer := modules[1].(map[string]any)
	consumer["GoMod"] = alternate
	consumer["Replace"].(map[string]any)["GoMod"] = alternate
	packages := decodeTestJSONStream(t, fixture.input.PackageList)
	packageConsumer := packages[0].(map[string]any)["Module"].(map[string]any)
	packageConsumer["GoMod"] = alternate
	packageConsumer["Replace"].(map[string]any)["GoMod"] = alternate
	fixture.input.ModuleList = jsonStream(t, modules...)
	fixture.input.PackageList = jsonStream(t, packages...)
	_, err := Compile(fixture.input)
	assertBuildInputError(t, err, "build_input_invalid", "retain", "module_graph_state_invalid", "/moduleGraph", "")
}

func TestCompileReadsEachModuleBoundaryOnceAndReusesItsFact(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	consumerGoMod := filepath.Join(fixture.repo, "go.mod")
	original, err := os.ReadFile(consumerGoMod)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	fixture.input.readFile = func(root, name string, limit int64, pointer string) ([]byte, error) {
		counts[name]++
		content, readErr := readSemanticFile(root, name, limit, pointer)
		if readErr == nil && name == consumerGoMod && counts[name] == 1 {
			if writeErr := os.WriteFile(name, []byte("module example.com/acme/consumer\n\ngo 1.24\n"), 0o644); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		return content, readErr
	}
	compilation, err := Compile(fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	if got := counts[consumerGoMod]; got != 1 {
		t.Fatalf("consumer go.mod semantic reads = %d, want 1", got)
	}
	graph, _ := compilation.Graph()
	goVersion, err := graph.GoVersion()
	if err != nil || goVersion != "1.25" {
		t.Fatalf("GoVersion() = %q, %v", goVersion, err)
	}
	wantDigest := provenance.SHA256(original)
	sources, _ := graph.ModuleSources()
	if len(sources) == 0 || sources[0].Ref.String() != "repo:go.mod" || sources[0].Digest != wantDigest {
		t.Fatalf("consumer module source = %#v, want digest %s", sources, wantDigest.String())
	}
	manifest, _ := compilation.Manifest()
	inputs, _ := manifest.Inputs()
	found := false
	for _, input := range inputs {
		if input.Module.Module.Path == "example.com/acme/consumer" && input.Path == "go.mod" {
			found = true
			if input.Digest != wantDigest {
				t.Fatalf("retained consumer go.mod digest = %s, want %s", input.Digest.String(), wantDigest.String())
			}
		}
	}
	if !found {
		t.Fatal("retained consumer go.mod input not found")
	}
}

func TestCompileRejectsSelectedFileSafetyViolations(t *testing.T) {
	tests := []struct {
		name, member, reason, pointer string
		prepare                       func(*testing.T, discoveryFixture, string)
	}{
		{name: "symlink", member: "selected-link.go", reason: "retained_input_symlink", pointer: "/retainedInputs/2", prepare: func(t *testing.T, fixture discoveryFixture, path string) {
			if err := os.Symlink(filepath.Join(fixture.repo, "schema/models/schema.go"), path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "not regular", member: "selected-directory", reason: "retained_input_not_regular", pointer: "/retainedInputs/2", prepare: func(t *testing.T, _ discoveryFixture, path string) {
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "size", member: "selected-large.go", reason: "retained_input_limit_exceeded", pointer: "/retainedInputs/2", prepare: func(t *testing.T, _ discoveryFixture, path string) {
			mustWriteFile(t, path, bytes.Repeat([]byte{'x'}, MaxRetainedBuildInputBytes+1))
		}},
		{name: "missing", member: "selected-missing.go", reason: "retained_input_read_failed", pointer: "/retainedInputs/2", prepare: func(*testing.T, discoveryFixture, string) {}},
		{name: "escape", member: "../escape.go", reason: "retained_path_invalid", pointer: "/retainedInputs/2/path", prepare: func(*testing.T, discoveryFixture, string) {}},
		{name: "symlink parent", member: "selected-parent/file.go", reason: "retained_parent_drift", pointer: "/retainedInputs/2", prepare: func(t *testing.T, fixture discoveryFixture, path string) {
			target := filepath.Join(fixture.repo, "real-selected-parent")
			mustWriteFile(t, filepath.Join(target, "file.go"), []byte("package models\n"))
			if err := os.Symlink(target, filepath.Dir(path)); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDiscoveryFixture(t)
			path := filepath.Join(fixture.repo, "schema/models", filepath.FromSlash(test.member))
			test.prepare(t, fixture, path)
			pkg := decodeTestJSONStream(t, fixture.input.PackageList)[0].(map[string]any)
			pkg["GoFiles"], pkg["EmbedFiles"] = []string{test.member}, []string{}
			fixture.input.PackageList = jsonStream(t, pkg)
			_, err := Compile(fixture.input)
			assertBuildInputError(t, err, "build_input_invalid", "retain", test.reason, test.pointer, "")
		})
	}
}

func TestCompileRejectsRepositoryReplacementThroughIntermediateSymlink(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	outsideParent := t.TempDir()
	outsideModule := filepath.Join(outsideParent, "module")
	mustWriteFile(t, filepath.Join(outsideModule, "go.mod"), []byte("module example.com/escape\n\ngo 1.25\n"))
	if err := os.Symlink(outsideParent, filepath.Join(fixture.repo, "link")); err != nil {
		t.Fatal(err)
	}
	linkedModule := filepath.Join(fixture.repo, "link/module")
	modules := decodeTestJSONStream(t, fixture.input.ModuleList)
	modules = append(modules, map[string]any{
		"Path": "example.com/escape", "Version": "v1.0.0", "Dir": linkedModule, "GoMod": filepath.Join(linkedModule, "go.mod"),
		"Replace": map[string]any{"Path": "example.com/escape", "Dir": linkedModule, "GoMod": filepath.Join(linkedModule, "go.mod")},
	})
	fixture.input.ModuleList = jsonStream(t, modules...)
	_, err := Compile(fixture.input)
	assertBuildInputError(t, err, "build_input_invalid", "retain", "retained_parent_drift", "/retainedInputs/2", "")
}

func TestCompileDoesNotEnumerateUnretainedSiblingsOrObserveTheirMutation(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	unretained := filepath.Join(fixture.repo, "unretained-siblings")
	if err := os.MkdirAll(unretained, 0o755); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxRetainedBuildInputs+1; index++ {
		mustWriteFile(t, filepath.Join(unretained, "sibling-"+strconv.Itoa(index)), nil)
	}
	noise := filepath.Join(unretained, "concurrent-noise")
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for sequence := 0; ; sequence++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
				if err := os.WriteFile(noise, []byte(strconv.Itoa(sequence)), 0o644); err != nil {
					done <- err
					return
				}
			}
		}
	}()
	compilation, err := Compile(fixture.input)
	close(stop)
	select {
	case writerErr := <-done:
		if writerErr != nil {
			t.Fatal(writerErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unretained mutation writer did not stop")
	}
	if err != nil {
		t.Fatalf("Compile() observed unretained sibling state: %v", err)
	}
	manifest, err := compilation.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := manifest.Inputs()
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range inputs {
		if strings.Contains(input.Path, "unretained-siblings") || strings.Contains(input.Path, "concurrent-noise") {
			t.Fatalf("unretained sibling entered manifest: %#v", input)
		}
	}
}

func TestCompileIsInvariantToGoListStreamAndMemberOrder(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	pkg := decodeTestJSONStream(t, fixture.input.PackageList)[0].(map[string]any)
	pkg["GoFiles"] = []string{"schema.go", "ignored.go"}
	pkg["EmbedFiles"] = []string{"vendor/data.txt"}
	fixture.input.PackageList = jsonStream(t, pkg)
	first, err := Compile(fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	firstGraph, err := CanonicalGraph(first)
	if err != nil {
		t.Fatal(err)
	}
	firstManifest, err := CanonicalManifest(first)
	if err != nil {
		t.Fatal(err)
	}

	modules := decodeTestJSONStream(t, fixture.input.ModuleList)
	for left, right := 0, len(modules)-1; left < right; left, right = left+1, right-1 {
		modules[left], modules[right] = modules[right], modules[left]
	}
	fixture.input.ModuleList = jsonStream(t, modules...)
	pkg["GoFiles"] = []string{"ignored.go", "schema.go"}
	fixture.input.PackageList = jsonStream(t, pkg)
	second, err := Compile(fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	secondGraph, err := CanonicalGraph(second)
	if err != nil {
		t.Fatal(err)
	}
	secondManifest, err := CanonicalManifest(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstGraph, secondGraph) || !bytes.Equal(firstManifest, secondManifest) {
		t.Fatalf("semantic output changed after Go-list reordering:\ngraph1=%s\ngraph2=%s\nmanifest1=%s\nmanifest2=%s", firstGraph, secondGraph, firstManifest, secondManifest)
	}
}

func TestCompileAssignsNestedLocalPackageToExactModuleGraphOwner(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	nestedRoot := filepath.Join(fixture.repo, "localdeps/helper")
	mustWriteFile(t, filepath.Join(nestedRoot, "go.mod"), []byte("module example.com/local/helper\n\ngo 1.25\n"))
	mustWriteFile(t, filepath.Join(nestedRoot, "models/model.go"), []byte("package models\n"))
	nestedModule := map[string]any{
		"Path": "example.com/local/helper", "Version": "v1.0.0", "Dir": nestedRoot, "GoMod": filepath.Join(nestedRoot, "go.mod"),
		"Replace": map[string]any{"Path": "example.com/local/helper", "Dir": nestedRoot, "GoMod": filepath.Join(nestedRoot, "go.mod")},
	}
	modules := decodeTestJSONStream(t, fixture.input.ModuleList)
	modules = append(modules, nestedModule)
	packages := decodeTestJSONStream(t, fixture.input.PackageList)
	packages = append(packages, map[string]any{
		"Dir": filepath.Join(nestedRoot, "models"), "ImportPath": "example.com/local/helper/models", "Name": "models",
		"Module": nestedModule, "GoFiles": []string{"model.go"},
	})
	fixture.input.ModuleList = jsonStream(t, modules...)
	fixture.input.PackageList = jsonStream(t, packages...)
	compilation, err := Compile(fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := compilation.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := manifest.Inputs()
	if err != nil {
		t.Fatal(err)
	}
	foundBoundary, foundMember := false, false
	for _, input := range inputs {
		if input.Module.Module.Path != "example.com/local/helper" || input.Module.Module.Version != "v1.0.0" {
			continue
		}
		if input.Module.RepositoryPath != "localdeps/helper" || input.Module.Module.Replacement.RepositoryPath != "localdeps/helper" {
			t.Fatalf("nested owner coordinate = %#v", input.Module)
		}
		foundBoundary = foundBoundary || input.Path == "go.mod" && input.Kind == "module-file"
		foundMember = foundMember || input.Path == "models/model.go" && input.Kind == "go"
	}
	if !foundBoundary || !foundMember {
		t.Fatalf("nested module retained closure missing boundary/member: %#v", inputs)
	}
}

func TestCompileRejectsInvalidRepositoryCoordinateBeforeSemanticRead(t *testing.T) {
	fixture := newDiscoveryFixture(t)
	repositoryPath := strings.Repeat("part/", 51) + "module"
	if len(repositoryPath) <= provenance.MaxDomainSourceBytes {
		t.Fatalf("invalid repository coordinate length = %d", len(repositoryPath))
	}
	moduleRoot := filepath.Join(fixture.repo, filepath.FromSlash(repositoryPath))
	mustWriteFile(t, filepath.Join(moduleRoot, "go.mod"), []byte("module example.com/aaa/deep\n\ngo 1.25\n"))
	modules := decodeTestJSONStream(t, fixture.input.ModuleList)
	modules = append(modules, map[string]any{
		"Path": "example.com/aaa/deep", "Version": "v1.0.0", "Dir": moduleRoot, "GoMod": filepath.Join(moduleRoot, "go.mod"),
		"Replace": map[string]any{"Path": "example.com/aaa/deep", "Dir": moduleRoot, "GoMod": filepath.Join(moduleRoot, "go.mod")},
	})
	fixture.input.ModuleList = jsonStream(t, modules...)
	reads, presenceChecks := 0, 0
	fixture.input.readFile = func(root, name string, limit int64, pointer string) ([]byte, error) {
		reads++
		return readSemanticFile(root, name, limit, pointer)
	}
	fixture.input.filePresent = func(_, _, _ string) (bool, error) {
		presenceChecks++
		return false, nil
	}

	_, err := Compile(fixture.input)
	if reads != 0 {
		t.Fatalf("semantic reader calls = %d, want 0", reads)
	}
	if presenceChecks != 0 {
		t.Fatalf("semantic presence calls = %d, want 0", presenceChecks)
	}
	assertBuildInputError(t, err, "build_input_invalid", "retain", "module_content_variant_invalid", "/moduleGraph/modules/0/content/kind", "")
}

func cloneJSONMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

type discoveryFixture struct {
	input DiscoveryInput
	repo  string
}

func newDiscoveryFixture(t *testing.T) discoveryFixture {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repository")
	scratch := filepath.Join(root, "scratch")
	mustWriteFile(t, filepath.Join(repo, "go.mod"), []byte("module example.com/acme/consumer\n\ngo 1.25\ntoolchain go1.25.0\n"))
	mustWriteFile(t, filepath.Join(repo, "go.sum"), []byte("example.com/dependency v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"))
	mustWriteFile(t, filepath.Join(repo, "schema/models/schema.go"), []byte("package models\n"))
	mustWriteFile(t, filepath.Join(repo, "schema/models/schema_test.go"), []byte("package models\n"))
	mustWriteFile(t, filepath.Join(repo, "schema/models/ignored.go"), []byte("package models\n"))
	mustWriteFile(t, filepath.Join(repo, "schema/models/vendor/data.txt"), []byte("embedded\n"))
	mustWriteFile(t, filepath.Join(repo, "unrelated-large.bin"), bytes.Repeat([]byte{'x'}, (16<<20)+1))
	if err := os.Symlink("missing-target", filepath.Join(repo, "unrelated-link")); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(scratch, "go.mod"), []byte("module example.com/ent-helper\n\ngo 1.25\n\nrequire example.com/acme/consumer v0.0.0\n\nreplace example.com/acme/consumer => "+repo+"\n"))

	consumer := map[string]any{
		"Path": "example.com/acme/consumer", "Version": "v0.0.0", "Dir": repo, "GoMod": filepath.Join(repo, "go.mod"),
		"Replace": map[string]any{"Path": "example.com/acme/consumer", "Dir": repo, "GoMod": filepath.Join(repo, "go.mod")},
	}
	moduleStream := jsonStream(t,
		map[string]any{"Path": "example.com/ent-helper", "Main": true, "Dir": scratch, "GoMod": filepath.Join(scratch, "go.mod"), "GoVersion": "1.25"},
		consumer,
		map[string]any{"Path": "github.com/nxnminieye/nexa", "Version": "v0.1.0", "Sum": "h1:IiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiI=", "GoModSum": "h1:MzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzM="},
	)
	packageStream := jsonStream(t, map[string]any{
		"Dir":            filepath.Join(repo, "schema/models"),
		"ImportPath":     "example.com/acme/consumer/schema/models",
		"Name":           "models",
		"Module":         consumer,
		"GoFiles":        []string{"schema.go"},
		"EmbedFiles":     []string{"vendor/data.txt"},
		"TestGoFiles":    []string{"schema_test.go"},
		"IgnoredGoFiles": []string{"ignored.go"},
	})
	schemaDir, err := provenance.ParseDomainSource("schema/models")
	if err != nil {
		t.Fatal(err)
	}
	return discoveryFixture{
		repo: repo,
		input: DiscoveryInput{
			RepositoryRoot:      repo,
			ScratchRoot:         scratch,
			SchemaDir:           schemaDir,
			SchemaImportPath:    "example.com/acme/consumer/schema/models",
			BuildTags:           []string{"zeta", "alpha"},
			ToolModule:          ModuleRequirement{Path: "github.com/nxnminieye/nexa", Version: "v0.1.0"},
			HelperDigest:        provenance.SHA256([]byte("helper")),
			GoExecutableVersion: "go1.25.0",
			ModuleList:          moduleStream,
			PackageList:         packageStream,
		},
	}
}

func jsonStream(t *testing.T, values ...any) []byte {
	t.Helper()
	var result bytes.Buffer
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		result.Write(encoded)
		result.WriteByte('\n')
	}
	return result.Bytes()
}

func decodeTestJSONStream(t *testing.T, data []byte) []any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	var values []any
	for {
		var value any
		if err := decoder.Decode(&value); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	return values
}

func mustWriteFile(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

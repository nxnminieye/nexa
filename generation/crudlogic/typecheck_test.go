package crudlogic

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/bufbuild/protocompile"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestValidationCanonicalExcludesHostSpecificValues(t *testing.T) {
	input := validationCanonicalInput{
		PlanDigest:       provenance.SHA256([]byte("plan")),
		RPCGoTool:        toolchain.Tool{ID: "rpc-go", Version: "v1", Executable: "/private/tool-a", Probe: toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: "rpc-go-v1"}},
		GoTool:           toolchain.Tool{ID: "go", Version: "go1", Executable: "/private/go-a", Probe: toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: "go1.25.0"}},
		Environment:      []validationEnvironment{{Name: "GOROOT", Source: toolchain.EnvironmentHost, Value: "sha256:one"}},
		CandidateDigests: []provenance.Digest{provenance.SHA256([]byte("candidate"))},
		WiringDigest:     provenance.SHA256([]byte("wiring")),
		ReadFiles:        []validatedFile{{Path: "backend/account/internal/svc/servicecontext.go", Digest: provenance.SHA256([]byte("svc"))}},
	}
	first, err := canonicalValidation(input)
	if err != nil {
		t.Fatal(err)
	}
	input.RPCGoTool.Executable = "/different/host/tool"
	input.GoTool.Executable = "/different/host/go"
	second, err := canonicalValidation(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || bytes.Contains(first, []byte("/private")) || bytes.Contains(first, []byte("/different")) {
		t.Fatalf("canonical validation is host-bound: %s / %s", first, second)
	}
	for name, mutate := range map[string]func(*validationCanonicalInput){
		"go tool":  func(value *validationCanonicalInput) { value.GoTool.Version = "go2" },
		"rpc tool": func(value *validationCanonicalInput) { value.RPCGoTool.Version = "v2" },
		"wiring":   func(value *validationCanonicalInput) { value.WiringDigest = provenance.SHA256([]byte("other wiring")) },
		"candidate": func(value *validationCanonicalInput) {
			value.CandidateDigests[0] = provenance.SHA256([]byte("other candidate"))
		},
		"read file": func(value *validationCanonicalInput) {
			value.ReadFiles[0].Digest = provenance.SHA256([]byte("other svc"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := input
			changed.Environment = append([]validationEnvironment(nil), input.Environment...)
			changed.CandidateDigests = append([]provenance.Digest(nil), input.CandidateDigests...)
			changed.ReadFiles = append([]validatedFile(nil), input.ReadFiles...)
			mutate(&changed)
			canonical, err := canonicalValidation(changed)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(second, canonical) {
				t.Fatalf("canonical validation ignored %s", name)
			}
		})
	}
}

func TestValidationCanonicalNormalizesHostAndScratchEnvironment(t *testing.T) {
	goTool, firstEnvironment := validationGoProvider(t)
	secondEnvironment := append([]toolchain.EnvVar(nil), firstEnvironment...)
	for index := range secondEnvironment {
		switch secondEnvironment[index].Name {
		case "PATH":
			secondEnvironment[index].Value = "/different-host/bin"
		case "GOROOT":
			secondEnvironment[index].Value = "/different-host/go"
		case "GOMODCACHE":
			secondEnvironment[index].Value = "/different-host/modcache"
		case "GOPROXY":
			secondEnvironment[index].Value = "https://different-proxy.invalid"
		case "GOSUMDB":
			secondEnvironment[index].Value = "different-sumdb.invalid"
		case "HOME", "TMPDIR", "GOPATH", "GOCACHE":
			secondEnvironment[index].Value = filepath.Join("/different-scratch", strings.ToLower(secondEnvironment[index].Name))
		}
	}
	_, firstProvider, _, err := validateGoProvider(goTool, firstEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	_, secondProvider, _, err := validateGoProvider(goTool, secondEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	canonicalInput := func(provider []validationEnvironment) validationCanonicalInput {
		return validationCanonicalInput{
			PlanDigest:       provenance.SHA256([]byte("plan")),
			RPCGoTool:        toolchain.Tool{ID: "rpc-go", Version: "v1", Executable: "/host/rpc-go", Probe: toolchain.ExecutableProbe{ExpectedVersion: "rpc-go-v1"}},
			GoTool:           goTool,
			Environment:      provider,
			CandidateDigests: []provenance.Digest{provenance.SHA256([]byte("candidate"))},
			WiringDigest:     provenance.SHA256([]byte("wiring")),
		}
	}
	first, err := canonicalValidation(canonicalInput(firstProvider))
	if err != nil {
		t.Fatal(err)
	}
	second, err := canonicalValidation(canonicalInput(secondProvider))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("canonical validation changed across host paths:\n%s\n%s", first, second)
	}
	sources := make(map[string]toolchain.EnvironmentValueSource, len(goTool.Environment))
	for _, rule := range goTool.Environment {
		sources[rule.Name] = rule.Source
	}
	for _, environment := range [][]toolchain.EnvVar{firstEnvironment, secondEnvironment} {
		for _, item := range environment {
			if sources[item.Name] == toolchain.EnvironmentFixed || item.Value == "" || item.Value == "off" || item.Value == "local" {
				continue
			}
			if bytes.Contains(first, []byte(item.Value)) || bytes.Contains(first, []byte(provenance.SHA256([]byte(item.Value)).String())) {
				t.Fatalf("canonical validation retained host value or hash for %s: %s", item.Name, first)
			}
		}
	}
}

func TestRealCRUDCandidateMatrix(t *testing.T) {
	fixture := newRealCRUDConsumer(t)
	command := exec.Command("go", "run", "-mod=mod", "./cmd/plan", "all")
	command.Dir = fixture
	command.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("real BuildPlan/Validate/TransactionInputs matrix: %v\n%s", err, output)
	}
	writeFixtureFile(t, filepath.Join(fixture, "internal/logic/crud_runtime_test.go"), realCRUDRuntimeTest)
	runtimeTest := exec.Command("go", "test", "./internal/logic", "-count=1")
	runtimeTest.Dir = fixture
	runtimeTest.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local")
	if output, err := runtimeTest.CombinedOutput(); err != nil {
		t.Fatalf("real CRUD runtime matrix: %v\n%s", err, output)
	}
}

func TestRealPBFixtureGeneratorRequestContract(t *testing.T) {
	const target = "rpc/record.crud.generated.proto"
	source := `syntax = "proto3";
package fixture.v1;
option go_package = "example.com/crudlogicfixture/backend/account/internal/pb;accountpb";
import "google/protobuf/field_mask.proto";
import "google/protobuf/struct.proto";
import "google/protobuf/timestamp.proto";
import "nexa/protocol/v1/options.proto";
message Request { google.protobuf.FieldMask mask = 1; google.protobuf.Value value = 2; google.protobuf.Timestamp at = 3; string version_2_name = 4; }
message Response {}
service Fixture { rpc Get(Request) returns (Response) { option (nexa.protocol.v1.rpc_context) = {}; } }
`
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{
		target: source, "nexa/protocol/v1/options.proto": string(genprotocol.OptionsProto()),
	})})}
	files, err := compiler.Compile(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	request := &pluginpb.CodeGeneratorRequest{FileToGenerate: []string{target, crudProtocolOptionsProtoPath}}
	appendFixtureDescriptors(request, files[0])
	parameter, err := fixtureGeneratorParameter(request.ProtoFile)
	if err != nil {
		t.Fatal(err)
	}
	request.Parameter = &parameter
	wantParameter := "module=example.com/crudlogicfixture,Mnexa/protocol/v1/options.proto=example.com/crudlogicfixture/backend/account/internal/pb;accountpb"
	if request.GetParameter() != wantParameter || strings.Count(request.GetParameter(), "module=") != 1 || strings.Count(request.GetParameter(), "Mnexa/protocol/v1/options.proto=") != 1 {
		t.Fatalf("parameter = %q", request.GetParameter())
	}
	if len(request.FileToGenerate) != 2 || request.FileToGenerate[0] != target || request.FileToGenerate[1] != crudProtocolOptionsProtoPath {
		t.Fatalf("file_to_generate = %#v", request.FileToGenerate)
	}
	wantNames := map[string]bool{
		target: true, "nexa/protocol/v1/options.proto": true, "google/protobuf/descriptor.proto": true,
		"google/protobuf/field_mask.proto": true, "google/protobuf/struct.proto": true, "google/protobuf/timestamp.proto": true,
	}
	for _, file := range request.ProtoFile {
		delete(wantNames, file.GetName())
		if file.GetOptions().GetGoPackage() == "" && !strings.Contains(request.GetParameter(), "M"+file.GetName()+"=") {
			t.Fatalf("descriptor %q has neither go_package nor M mapping", file.GetName())
		}
	}
	if len(wantNames) != 0 {
		t.Fatalf("request missing descriptors: %#v", wantNames)
	}
	if strings.Count(realPlanProgram, wantParameter) != 0 {
		t.Fatal("fixture program must construct the parameter from descriptors, not embed the assertion value")
	}
}

func TestGeneratedRPCProtoSourceRolesAreClosed(t *testing.T) {
	parse := func(source string) *ast.File {
		t.Helper()
		file, err := parser.ParseFile(token.NewFileSet(), "generated.go", "// Code generated by protoc-gen-go. DO NOT EDIT.\n// source: "+source+"\npackage accountpb\n", parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		return file
	}
	if !generatedRPCProtoSourceAllowed(parse("rpc/record.crud.generated.proto"), "rpc/record.crud.generated.proto") || !generatedRPCProtoSourceAllowed(parse(crudProtocolOptionsProtoPath), "rpc/record.crud.generated.proto") {
		t.Fatal("expected target and framework options generated roles")
	}
	for _, source := range []string{"rpc/other.proto", "nexa/protocol/v1/other.proto", "google/protobuf/timestamp.proto"} {
		if generatedRPCProtoSourceAllowed(parse(source), "rpc/record.crud.generated.proto") {
			t.Fatalf("accepted unrelated generated source %q", source)
		}
	}
}

func appendFixtureDescriptors(request *pluginpb.CodeGeneratorRequest, root protoreflect.FileDescriptor) {
	seen := make(map[string]bool)
	var add func(protoreflect.FileDescriptor)
	add = func(file protoreflect.FileDescriptor) {
		if file == nil || seen[file.Path()] {
			return
		}
		for index := 0; index < file.Imports().Len(); index++ {
			add(file.Imports().Get(index).FileDescriptor)
		}
		seen[file.Path()] = true
		request.ProtoFile = append(request.ProtoFile, protodesc.ToFileDescriptorProto(file))
	}
	add(root)
}

func fixtureGeneratorParameter(files []*descriptorpb.FileDescriptorProto) (string, error) {
	mappings := map[string]string{"nexa/protocol/v1/options.proto": "example.com/crudlogicfixture/backend/account/internal/pb;accountpb"}
	result := []string{"module=example.com/crudlogicfixture"}
	var mapped []string
	for _, file := range files {
		if file.GetOptions().GetGoPackage() != "" {
			continue
		}
		value, ok := mappings[file.GetName()]
		if !ok {
			return "", fmt.Errorf("descriptor %q has no Go import mapping", file.GetName())
		}
		mapped = append(mapped, "M"+file.GetName()+"="+value)
	}
	sort.Strings(mapped)
	return strings.Join(append(result, mapped...), ","), nil
}

func newRealCRUDConsumer(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate source")
	}
	framework, err := filepath.EvalSymlinks(filepath.Clean(filepath.Join(filepath.Dir(filename), "../..")))
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(filepath.Dir(framework), ".crudlogic-real-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	moduleRoot := filepath.Join(root, "backend/account")
	writeFixtureFile(t, filepath.Join(moduleRoot, "go.mod"), fmt.Sprintf(realConsumerGoMod, filepath.ToSlash(framework)))
	writeFixtureFile(t, filepath.Join(moduleRoot, "ent/schema/record.go"), realRecordSchema)
	writeFixtureFile(t, filepath.Join(moduleRoot, "ent/template/stringer.tmpl"), realEntStringerOverride)
	writeFixtureFile(t, filepath.Join(moduleRoot, "internal/svc/servicecontext.go"), realServiceContext)
	writeFixtureFile(t, filepath.Join(moduleRoot, "cmd/plan/aliased_pb.go.txt"), realAliasedPBSource)
	writeFixtureFile(t, filepath.Join(moduleRoot, "deps/deps.go"), realDependencyPins)
	writeFixtureFile(t, filepath.Join(moduleRoot, "cmd/plan/main.go"), realPlanProgram)
	tenantRoot := filepath.Join(root, "backend/tenantonly")
	tenantModule := strings.Replace(fmt.Sprintf(realConsumerGoMod, filepath.ToSlash(framework)), "module example.com/crudlogicfixture/backend/account", "module example.com/crudlogictenantonly", 1)
	writeFixtureFile(t, filepath.Join(tenantRoot, "go.mod"), tenantModule)
	writeFixtureFile(t, filepath.Join(tenantRoot, "ent/schema/tenant_only.go"), realTenantOnlySchema)
	writeFixtureFile(t, filepath.Join(tenantRoot, "deps/deps.go"), realDependencyPins)
	check := exec.Command("go", "test", "-mod=mod", "./ent/schema")
	check.Dir = moduleRoot
	check.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local")
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("compile real Ent schema: %v\n%s", err, output)
	}
	command := exec.Command("go", "run", "-mod=mod", "entgo.io/ent/cmd/ent@v0.14.5", "generate", "--template", "file=ent/template/stringer.tmpl", "./ent/schema")
	command.Dir = moduleRoot
	command.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate real Ent fixture: %v\n%s", err, output)
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = moduleRoot
	tidy.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local")
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("tidy real fixture: %v\n%s", err, output)
	}
	download := exec.Command("go", "mod", "download", "all")
	download.Dir = moduleRoot
	download.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local")
	if output, err := download.CombinedOutput(); err != nil {
		t.Fatalf("download pinned fixture graph: %v\n%s", err, output)
	}
	tenantCheck := exec.Command("go", "test", "-mod=mod", "./ent/schema")
	tenantCheck.Dir = tenantRoot
	tenantCheck.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local")
	if output, err := tenantCheck.CombinedOutput(); err != nil {
		t.Fatalf("compile tenant-only schema: %v\n%s", err, output)
	}
	tenantTidy := exec.Command("go", "mod", "tidy")
	tenantTidy.Dir = tenantRoot
	tenantTidy.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local")
	if output, err := tenantTidy.CombinedOutput(); err != nil {
		t.Fatalf("tidy tenant-only fixture: %v\n%s", err, output)
	}
	return moduleRoot
}

func writeFixtureFile(t *testing.T, name, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

const realConsumerGoMod = `module example.com/crudlogicfixture/backend/account

go 1.25.0

require (
	entgo.io/ent v0.14.5
	github.com/bufbuild/protocompile v0.14.1
	github.com/google/uuid v1.6.0
	github.com/mattn/go-sqlite3 v1.14.17
	github.com/nxnminieye/nexa v0.8.0
	github.com/zeromicro/go-zero v1.9.2
	golang.org/x/mod v0.31.0
	golang.org/x/sync v0.19.0
	golang.org/x/tools v0.39.0
	google.golang.org/grpc v1.79.3
	google.golang.org/protobuf v1.36.10
)

replace github.com/nxnminieye/nexa v0.8.0 => %s
`

const realRecordSchema = `package schema

import (
	"encoding/json"
	"errors"
	"entgo.io/ent"
	entschema "entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
	"github.com/nxnminieye/nexa/nexaent"
	nexamixin "github.com/nxnminieye/nexa/nexaent/mixin"
)

type JSONPayload struct { Value string ` + "`json:\"value\"`" + ` }
func (value JSONPayload) MarshalJSON() ([]byte,error) { if value.Value=="marshal-error" { return nil,errors.New("marshal error") }; type plain JSONPayload; return json.Marshal(plain(value)) }

type Record struct{ ent.Schema }
func (Record) Mixin() []ent.Mixin { return []ent.Mixin{nexamixin.Tenant{}} }
func localized(key string) nexaent.LocalizedText { return nexaent.LocalizedText{Key:key, ZhCN:key, EnUS:key} }
func fieldMeta(name string) nexaent.FieldMeta { return nexaent.FieldMeta{Label:localized(name+".label"), Description:localized(name+".description"), UIHint:nexaent.UIHintText, Visibility:nexaent.VisibilityPublic, CRUD:&nexaent.CRUDFieldPolicy{Read:nexaent.ReadInclude, Mutation:nexaent.MutationCreateUpdate}} }
func identityMeta(name string) nexaent.FieldMeta { value:=fieldMeta(name);value.CRUD.Mutation=nexaent.MutationNone;return value }
func internalMeta(name string) nexaent.FieldMeta { value:=fieldMeta(name);value.Visibility=nexaent.VisibilityInternal;value.CRUD.Read=nexaent.ReadExclude;value.CRUD.Mutation=nexaent.MutationNone;return value }
func readOnlyMeta(name string) nexaent.FieldMeta { value:=fieldMeta(name);value.CRUD.Mutation=nexaent.MutationNone;return value }
func (Record) Annotations() []entschema.Annotation { return []entschema.Annotation{nexaent.Schema(nexaent.SchemaMeta{Label:localized("record.label"), Description:localized("record.description"), Identity:nexaent.IdentityEntID, Scope:nexaent.ScopeTenant}), nexaent.CRUD(nexaent.AllCRUDOperations()...)} }
func (Record) Fields() []ent.Field { return []ent.Field{
	field.String("name").NotEmpty().Unique().Annotations(nexaent.Field(fieldMeta("record.name"))),
	field.UUID("external_id", uuid.UUID{}).Optional().Annotations(nexaent.Field(fieldMeta("record.external_id"))),
	field.UUID("required_uuid", uuid.UUID{}).Annotations(nexaent.Field(fieldMeta("record.required_uuid"))),
	field.UUID("correlation_uuid", uuid.UUID{}).Annotations(nexaent.Field(fieldMeta("record.correlation_uuid"))),
	field.UUID("nillable_uuid", uuid.UUID{}).Optional().Nillable().Annotations(nexaent.Field(fieldMeta("record.nillable_uuid"))),
	field.Int8("int8_value").Annotations(nexaent.Field(fieldMeta("record.int8_value"))),
	field.Int8("nillable_int8").Optional().Nillable().Annotations(nexaent.Field(fieldMeta("record.nillable_int8"))),
	field.Int16("int16_value").Annotations(nexaent.Field(fieldMeta("record.int16_value"))),
	field.Int32("int32_value").Annotations(nexaent.Field(fieldMeta("record.int32_value"))),
	field.Int("int_value").Annotations(nexaent.Field(fieldMeta("record.int_value"))),
	field.Int64("int64_value").Annotations(nexaent.Field(fieldMeta("record.int64_value"))),
	field.Uint8("uint8_value").Annotations(nexaent.Field(fieldMeta("record.uint8_value"))),
	field.Uint16("uint16_value").Annotations(nexaent.Field(fieldMeta("record.uint16_value"))),
	field.Uint16("nillable_uint16").Optional().Nillable().Annotations(nexaent.Field(fieldMeta("record.nillable_uint16"))),
	field.Uint32("uint32_value").Annotations(nexaent.Field(fieldMeta("record.uint32_value"))),
	field.Uint("uint_value").Annotations(nexaent.Field(fieldMeta("record.uint_value"))),
	field.Uint64("uint64_value").Annotations(nexaent.Field(fieldMeta("record.uint64_value"))),
	field.JSON("document", (*JSONPayload)(nil)).Annotations(nexaent.Field(fieldMeta("record.document"))),
	field.Time("occurred_at").Annotations(nexaent.Field(fieldMeta("record.occurred_at"))),
	field.Time("nillable_occurred_at").Optional().Nillable().Annotations(nexaent.Field(fieldMeta("record.nillable_occurred_at"))),
	field.String("version_2_name").Annotations(nexaent.Field(fieldMeta("record.version_2_name"))),
	field.String("reset").Annotations(nexaent.Field(fieldMeta("record.reset"))),
	field.String("string").Annotations(nexaent.Field(fieldMeta("record.string"))),
	field.String("descriptor").Annotations(nexaent.Field(fieldMeta("record.descriptor"))),
	field.String("foo").Annotations(nexaent.Field(fieldMeta("record.foo"))),
	field.String("get_foo").Annotations(nexaent.Field(fieldMeta("record.get_foo"))),
	field.String("cpu_guid_uri").Optional().Annotations(nexaent.Field(fieldMeta("record.cpu_guid_uri"))),
	field.String("xml_value").Optional().Annotations(nexaent.Field(fieldMeta("record.xml_value"))),
	field.Enum("state").Values("active", "disabled").Default("active").Annotations(nexaent.Field(readOnlyMeta("record.state"))),
	field.Bytes("payload").Optional().Annotations(nexaent.Field(fieldMeta("record.payload"))),
	field.Bytes("nillable_payload").Optional().Nillable().Annotations(nexaent.Field(fieldMeta("record.nillable_payload"))),
	field.Bytes("seed").Default([]byte("seed")).Annotations(nexaent.Field(fieldMeta("record.seed"))),
	field.String("internal_note").Optional().Annotations(nexaent.Field(internalMeta("record.internal_note"))),
} }

type UintRecord struct{ ent.Schema }
func (UintRecord) Annotations() []entschema.Annotation { return []entschema.Annotation{nexaent.Schema(nexaent.SchemaMeta{Label:localized("uint_record.label"),Description:localized("uint_record.description"),Identity:nexaent.IdentityEntID,Scope:nexaent.ScopeGlobal}),nexaent.CRUD(nexaent.CRUDGet,nexaent.CRUDDelete)} }
func (UintRecord) Fields() []ent.Field { return []ent.Field{field.Uint64("id").Annotations(nexaent.Field(identityMeta("uint_record.id")))} }

type StringRecord struct{ ent.Schema }
func (StringRecord) Annotations() []entschema.Annotation { return []entschema.Annotation{nexaent.Schema(nexaent.SchemaMeta{Label:localized("string_record.label"),Description:localized("string_record.description"),Identity:nexaent.IdentityEntID,Scope:nexaent.ScopeGlobal}),nexaent.CRUD(nexaent.CRUDGet)} }
func (StringRecord) Fields() []ent.Field { return []ent.Field{field.String("id").Annotations(nexaent.Field(identityMeta("string_record.id")))} }

type UUIDRecord struct{ ent.Schema }
func (UUIDRecord) Annotations() []entschema.Annotation { return []entschema.Annotation{nexaent.Schema(nexaent.SchemaMeta{Label:localized("uuid_record.label"),Description:localized("uuid_record.description"),Identity:nexaent.IdentityEntID,Scope:nexaent.ScopeGlobal}),nexaent.CRUD(nexaent.CRUDList,nexaent.CRUDGet)} }
func (UUIDRecord) Fields() []ent.Field { return []ent.Field{field.UUID("id",uuid.UUID{}).Annotations(nexaent.Field(identityMeta("uuid_record.id")))} }

type NarrowIDRecord struct{ ent.Schema }
func (NarrowIDRecord) Annotations() []entschema.Annotation { return []entschema.Annotation{nexaent.Schema(nexaent.SchemaMeta{Label:localized("narrow_id_record.label"),Description:localized("narrow_id_record.description"),Identity:nexaent.IdentityEntID,Scope:nexaent.ScopeGlobal}),nexaent.CRUD(nexaent.CRUDGet,nexaent.CRUDDelete)} }
func (NarrowIDRecord) Fields() []ent.Field { return []ent.Field{field.Int8("id").Annotations(nexaent.Field(identityMeta("narrow_id_record.id")))} }

type NarrowUintIDRecord struct{ ent.Schema }
func (NarrowUintIDRecord) Annotations() []entschema.Annotation { return []entschema.Annotation{nexaent.Schema(nexaent.SchemaMeta{Label:localized("narrow_uint_id_record.label"),Description:localized("narrow_uint_id_record.description"),Identity:nexaent.IdentityEntID,Scope:nexaent.ScopeGlobal}),nexaent.CRUD(nexaent.CRUDGet)} }
func (NarrowUintIDRecord) Fields() []ent.Field { return []ent.Field{field.Uint8("id").Annotations(nexaent.Field(identityMeta("narrow_uint_id_record.id")))} }

type SubsetRecord struct{ ent.Schema }
func (SubsetRecord) Annotations() []entschema.Annotation { return []entschema.Annotation{nexaent.Schema(nexaent.SchemaMeta{Label:localized("subset_record.label"),Description:localized("subset_record.description"),Identity:nexaent.IdentityEntID,Scope:nexaent.ScopeGlobal}),nexaent.CRUD(nexaent.CRUDDelete,nexaent.CRUDList)} }
func (SubsetRecord) Fields() []ent.Field { return []ent.Field{field.String("name").Annotations(nexaent.Field(readOnlyMeta("subset_record.name")))} }
`

const realEntStringerOverride = `{{ define "model/stringer" }}
// String method omitted so the fixture can exercise a Go field named String.
{{ end }}
`

const realServiceContext = `package svc
import "example.com/crudlogicfixture/backend/account/ent"
type ServiceContext struct { DB *ent.Client }
`

const realTenantOnlySchema = `package schema
import (
	"entgo.io/ent"
	entschema "entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/nexaent"
	nexamixin "github.com/nxnminieye/nexa/nexaent/mixin"
)
type TenantOnly struct{ent.Schema}
func (TenantOnly) Mixin()[]ent.Mixin{return []ent.Mixin{nexamixin.Tenant{}}}
func tenantText(key string)nexaent.LocalizedText{return nexaent.LocalizedText{Key:key,ZhCN:key,EnUS:key}}
func (TenantOnly) Annotations()[]entschema.Annotation{return []entschema.Annotation{nexaent.Schema(nexaent.SchemaMeta{Label:tenantText("tenant_only.label"),Description:tenantText("tenant_only.description"),Identity:nexaent.IdentityEntID,Scope:nexaent.ScopeTenant})}}
func (TenantOnly) Fields()[]ent.Field{return []ent.Field{field.String("name").Annotations(nexaent.Field(nexaent.FieldMeta{Label:tenantText("tenant_only.name.label"),Description:tenantText("tenant_only.name.description"),UIHint:nexaent.UIHintText,Visibility:nexaent.VisibilityPublic}))}}
`

const realAliasedPBSource = `// Code generated by protoc-gen-go. DO NOT EDIT.
// source: rpc/record.crud.generated.proto
package accountpb
import oldpb "example.com/crudlogicfixture/backend/account/oldpb"
type RecordState = oldpb.RecordState
const ( RecordState_RECORD_STATE_UNSPECIFIED = oldpb.RecordState_RECORD_STATE_UNSPECIFIED; RecordState_RECORD_STATE_STATE_ACTIVE = oldpb.RecordState_RECORD_STATE_STATE_ACTIVE; RecordState_RECORD_STATE_STATE_DISABLED = oldpb.RecordState_RECORD_STATE_STATE_DISABLED )
type Record = oldpb.Record
type ListRecordRequest = oldpb.ListRecordRequest
type ListRecordResponse = oldpb.ListRecordResponse
type GetRecordRequest = oldpb.GetRecordRequest
type GetRecordResponse = oldpb.GetRecordResponse
type CreateRecordRequest = oldpb.CreateRecordRequest
type CreateRecordResponse = oldpb.CreateRecordResponse
type UpdateRecordRequest = oldpb.UpdateRecordRequest
type UpdateRecordResponse = oldpb.UpdateRecordResponse
type DeleteRecordRequest = oldpb.DeleteRecordRequest
type DeleteRecordResponse = oldpb.DeleteRecordResponse
type UintRecord = oldpb.UintRecord
type GetUintRecordRequest = oldpb.GetUintRecordRequest
type GetUintRecordResponse = oldpb.GetUintRecordResponse
type DeleteUintRecordRequest = oldpb.DeleteUintRecordRequest
type DeleteUintRecordResponse = oldpb.DeleteUintRecordResponse
type StringRecord = oldpb.StringRecord
type GetStringRecordRequest = oldpb.GetStringRecordRequest
type GetStringRecordResponse = oldpb.GetStringRecordResponse
type UUIDRecord = oldpb.UUIDRecord
type ListUUIDRecordRequest = oldpb.ListUUIDRecordRequest
type ListUUIDRecordResponse = oldpb.ListUUIDRecordResponse
type GetUUIDRecordRequest = oldpb.GetUUIDRecordRequest
type GetUUIDRecordResponse = oldpb.GetUUIDRecordResponse
type NarrowIDRecord = oldpb.NarrowIDRecord
type GetNarrowIDRecordRequest = oldpb.GetNarrowIDRecordRequest
type GetNarrowIDRecordResponse = oldpb.GetNarrowIDRecordResponse
type DeleteNarrowIDRecordRequest = oldpb.DeleteNarrowIDRecordRequest
type DeleteNarrowIDRecordResponse = oldpb.DeleteNarrowIDRecordResponse
type NarrowUintIDRecord = oldpb.NarrowUintIDRecord
type GetNarrowUintIDRecordRequest = oldpb.GetNarrowUintIDRecordRequest
type GetNarrowUintIDRecordResponse = oldpb.GetNarrowUintIDRecordResponse
type SubsetRecord = oldpb.SubsetRecord
type ListSubsetRecordRequest = oldpb.ListSubsetRecordRequest
type ListSubsetRecordResponse = oldpb.ListSubsetRecordResponse
type DeleteSubsetRecordRequest = oldpb.DeleteSubsetRecordRequest
type DeleteSubsetRecordResponse = oldpb.DeleteSubsetRecordResponse
`

const realDependencyPins = `package deps
import (
	_ "github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	_ "github.com/zeromicro/go-zero/core/logx"
	_ "entgo.io/ent/dialect"
	_ "google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/status"
	_ "google.golang.org/protobuf/types/known/fieldmaskpb"
	_ "google.golang.org/protobuf/types/known/structpb"
	_ "google.golang.org/protobuf/types/known/timestamppb"
)
`

const realCRUDRuntimeTest = `package logic
import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"
	"entgo.io/ent/dialect"
	"example.com/crudlogicfixture/backend/account/ent"
	"example.com/crudlogicfixture/backend/account/ent/enttest"
	entschema "example.com/crudlogicfixture/backend/account/ent/schema"
	"example.com/crudlogicfixture/backend/account/internal/svc"
	"example.com/crudlogicfixture/backend/account/internal/logic/crudtenant"
	pb "example.com/crudlogicfixture/backend/account/internal/pb"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)
func badUUID(t *testing.T, err error) { t.Helper(); if status.Code(err)!=codes.InvalidArgument || status.Convert(err).Message()!="invalid field value" { t.Fatalf("error=%v",err) } }
func validCreate(name string,tenant int64)*pb.CreateRecordRequest{document,err:=structpb.NewValue(map[string]any{"value":"valid"});if err!=nil{panic(err)};return &pb.CreateRecordRequest{Name:name,RequiredUuid:uuid.NewString(),CorrelationUuid:uuid.NewString(),Int8Value:8,Int16Value:16,Int32Value:32,IntValue:64,Int64Value:640,Uint8Value:8,Uint16Value:16,Uint32Value:32,UintValue:64,Uint64Value:640,Document:document,OccurredAt:timestamppb.New(time.Unix(1700000000,123000000)),Version_2Name:"v2",Reset_:"reset",String_:"string",Descriptor_:"descriptor",Foo:"foo",GetFoo_:"get-foo",TenantId:tenant}}
func TestInvalidUUIDCreate(t *testing.T){ value:="not-a-uuid"; _,err:=NewCreateRecordLogic(context.Background(),&svc.ServiceContext{}).CreateRecord(&pb.CreateRecordRequest{ExternalId:&value,TenantId:1});badUUID(t,err) }
func TestInvalidUUIDUpdate(t *testing.T){ value:="not-a-uuid"; _,err:=NewUpdateRecordLogic(context.Background(),&svc.ServiceContext{}).UpdateRecord(&pb.UpdateRecordRequest{Id:1,TenantId:1,UpdateMask:&fieldmaskpb.FieldMask{Paths:[]string{"external_id"}},ExternalId:&value});badUUID(t,err) }
func requireStatus(t *testing.T,err error,code codes.Code,message string){t.Helper();if status.Code(err)!=code||status.Convert(err).Message()!=message{t.Fatalf("status=%v",err)}}
func TestCRUDTenantPaginationMaskAndErrors(t *testing.T){
	ctx:=context.Background();client:=enttest.Open(t,dialect.SQLite,"file:crudlogic?mode=memory&cache=shared&_fk=1");defer client.Close();service:=&svc.ServiceContext{DB:client}
	first,err:=NewCreateRecordLogic(ctx,service).CreateRecord(validCreate("first",1));if err!=nil{t.Fatal(err)}
	second,err:=NewCreateRecordLogic(ctx,service).CreateRecord(validCreate("second",2));if err!=nil{t.Fatal(err)};_ = second
	zero,err:=NewListRecordLogic(ctx,service).ListRecord(&pb.ListRecordRequest{TenantId:1,Limit:0});if err!=nil||zero.Total!=1||len(zero.Items)!=0{t.Fatalf("zero list=%#v %v",zero,err)}
	list,err:=NewListRecordLogic(ctx,service).ListRecord(&pb.ListRecordRequest{TenantId:1,Limit:10});if err!=nil||list.Total!=1||len(list.Items)!=1||list.Items[0].Id!=first.Item.Id{t.Fatalf("list=%#v %v",list,err)}
	tooLarge:=uint64(math.MaxInt)+1;_,err=NewListRecordLogic(ctx,service).ListRecord(&pb.ListRecordRequest{TenantId:1,Offset:tooLarge,Limit:1});requireStatus(t,err,codes.InvalidArgument,"invalid pagination");_,err=NewListRecordLogic(ctx,service).ListRecord(&pb.ListRecordRequest{TenantId:1,Limit:tooLarge});requireStatus(t,err,codes.InvalidArgument,"invalid pagination")
	_,err=NewGetRecordLogic(ctx,service).GetRecord(&pb.GetRecordRequest{Id:first.Item.Id,TenantId:2});requireStatus(t,err,codes.NotFound,"entity not found")
	name:="updated"; cases:=[]struct{name string;mask *fieldmaskpb.FieldMask;want string}{{"missing",nil,"update_mask is required"},{"empty",&fieldmaskpb.FieldMask{},"update_mask is required"},{"duplicate",&fieldmaskpb.FieldMask{Paths:[]string{"name","name"}},"update_mask contains unsupported field"},{"nested",&fieldmaskpb.FieldMask{Paths:[]string{"name.value"}},"update_mask contains unsupported field"},{"unknown",&fieldmaskpb.FieldMask{Paths:[]string{"missing"}},"update_mask contains unsupported field"},{"identity",&fieldmaskpb.FieldMask{Paths:[]string{"id"}},"update_mask contains unsupported field"},{"tenant",&fieldmaskpb.FieldMask{Paths:[]string{"tenant_id"}},"update_mask contains unsupported field"},{"internal",&fieldmaskpb.FieldMask{Paths:[]string{"internal_note"}},"update_mask contains unsupported field"}}
	for _,tc:=range cases{t.Run(tc.name,func(t *testing.T){_,err:=NewUpdateRecordLogic(ctx,service).UpdateRecord(&pb.UpdateRecordRequest{Id:first.Item.Id,TenantId:1,UpdateMask:tc.mask,Name:&name});requireStatus(t,err,codes.InvalidArgument,tc.want)})}
	_,err=NewUpdateRecordLogic(ctx,service).UpdateRecord(&pb.UpdateRecordRequest{Id:first.Item.Id,TenantId:2,UpdateMask:&fieldmaskpb.FieldMask{Paths:[]string{"name"}},Name:&name});requireStatus(t,err,codes.NotFound,"entity not found")
	updated,err:=NewUpdateRecordLogic(ctx,service).UpdateRecord(&pb.UpdateRecordRequest{Id:first.Item.Id,TenantId:1,UpdateMask:&fieldmaskpb.FieldMask{Paths:[]string{"name"}},Name:&name});if err!=nil||updated.Item.Name!="updated"{t.Fatalf("update=%#v %v",updated,err)}
	_,err=NewDeleteRecordLogic(ctx,service).DeleteRecord(&pb.DeleteRecordRequest{Id:first.Item.Id,TenantId:2});requireStatus(t,err,codes.NotFound,"entity not found")
	if _,err=NewDeleteRecordLogic(ctx,service).DeleteRecord(&pb.DeleteRecordRequest{Id:first.Item.Id,TenantId:1});err!=nil{t.Fatal(err)}
	_,err=NewGetRecordLogic(ctx,service).GetRecord(&pb.GetRecordRequest{Id:first.Item.Id,TenantId:1});requireStatus(t,err,codes.NotFound,"entity not found")
	emptyName:=validCreate("",1);_,err=NewCreateRecordLogic(ctx,service).CreateRecord(emptyName);requireStatus(t,err,codes.InvalidArgument,"invalid field value")
	duplicate:=validCreate("second",1);_,err=NewCreateRecordLogic(ctx,service).CreateRecord(duplicate);requireStatus(t,err,codes.FailedPrecondition,"constraint violation")
	uintRow,err:=client.UintRecord.Create().SetID(9).Save(ctx);if err!=nil{t.Fatal(err)};uintGot,err:=NewGetUintRecordLogic(ctx,service).GetUintRecord(&pb.GetUintRecordRequest{Id:9});if err!=nil||uintGot.Item.Id!=uintRow.ID{t.Fatalf("uint identity=%#v %v",uintGot,err)}
	stringRow,err:=client.StringRecord.Create().SetID("string-id").Save(ctx);if err!=nil{t.Fatal(err)};stringGot,err:=NewGetStringRecordLogic(ctx,service).GetStringRecord(&pb.GetStringRecordRequest{Id:"string-id"});if err!=nil||stringGot.Item.Id!=stringRow.ID{t.Fatalf("string identity=%#v %v",stringGot,err)}
	uuidID:=uuid.New();uuidRow,err:=client.UUIDRecord.Create().SetID(uuidID).Save(ctx);if err!=nil{t.Fatal(err)};uuidGot,err:=NewGetUUIDRecordLogic(ctx,service).GetUUIDRecord(&pb.GetUUIDRecordRequest{Id:uuidID.String()});if err!=nil||uuidGot.Item.Id!=uuidRow.ID.String(){t.Fatalf("uuid identity=%#v %v",uuidGot,err)}
	uuidList,err:=NewListUUIDRecordLogic(ctx,service).ListUUIDRecord(&pb.ListUUIDRecordRequest{Limit:10});if err!=nil||len(uuidList.Items)!=1||uuidList.Items[0].Id!=uuidID.String(){t.Fatalf("uuid list=%#v %v",uuidList,err)}
	narrowRow,err:=client.NarrowIDRecord.Create().SetID(7).Save(ctx);if err!=nil{t.Fatal(err)};narrowGot,err:=NewGetNarrowIDRecordLogic(ctx,service).GetNarrowIDRecord(&pb.GetNarrowIDRecordRequest{Id:7});if err!=nil||narrowGot.Item.Id!=int64(narrowRow.ID){t.Fatalf("narrow identity=%#v %v",narrowGot,err)}
	_,err=NewGetNarrowIDRecordLogic(ctx,service).GetNarrowIDRecord(&pb.GetNarrowIDRecordRequest{Id:128});requireStatus(t,err,codes.InvalidArgument,"invalid identity")
	_,err=NewGetNarrowUintIDRecordLogic(ctx,service).GetNarrowUintIDRecord(&pb.GetNarrowUintIDRecordRequest{Id:256});requireStatus(t,err,codes.InvalidArgument,"invalid identity")
	if _,err=NewDeleteUintRecordLogic(ctx,service).DeleteUintRecord(&pb.DeleteUintRecordRequest{Id:9});err!=nil{t.Fatal(err)}
	_,err=NewGetRecordLogic(ctx,&svc.ServiceContext{}).GetRecord(&pb.GetRecordRequest{Id:0});requireStatus(t,err,codes.InvalidArgument,"invalid identity");_,err=NewGetUintRecordLogic(ctx,&svc.ServiceContext{}).GetUintRecord(&pb.GetUintRecordRequest{Id:0});requireStatus(t,err,codes.InvalidArgument,"invalid identity");_,err=NewGetStringRecordLogic(ctx,&svc.ServiceContext{}).GetStringRecord(&pb.GetStringRecordRequest{});requireStatus(t,err,codes.InvalidArgument,"invalid identity");_,err=NewGetUUIDRecordLogic(ctx,&svc.ServiceContext{}).GetUUIDRecord(&pb.GetUUIDRecordRequest{Id:"bad"});requireStatus(t,err,codes.InvalidArgument,"invalid identity")
	broken:=enttest.Open(t,dialect.SQLite,"file:broken?mode=memory&cache=shared&_fk=1");if err:=broken.Close();err!=nil{t.Fatal(err)};_,err=NewListRecordLogic(ctx,&svc.ServiceContext{DB:broken}).ListRecord(&pb.ListRecordRequest{TenantId:1,Limit:1});requireStatus(t,err,codes.Internal,"crud operation failed")
}
func TestExactNumericJSONAndTimestamp(t *testing.T){
	ctx:=context.Background();client:=enttest.Open(t,dialect.SQLite,"file:exact?mode=memory&cache=shared&_fk=1");defer client.Close();service:=&svc.ServiceContext{DB:client}
	valid:=validCreate("exact",1);valid.Int8Value=127;valid.Int16Value=32767;valid.Int32Value=2147483647;valid.Uint8Value=255;valid.Uint16Value=65535;valid.Uint32Value=4294967295
	created,err:=NewCreateRecordLogic(ctx,service).CreateRecord(valid);if err!=nil||created.Item.Int8Value!=127||created.Item.Int16Value!=32767||created.Item.Int32Value!=2147483647||created.Item.IntValue!=64||created.Item.Int64Value!=640||created.Item.Uint8Value!=255||created.Item.Uint16Value!=65535||created.Item.Uint32Value!=4294967295||created.Item.UintValue!=64||created.Item.Uint64Value!=640||created.Item.Document.GetStructValue().Fields["value"].GetStringValue()!="valid"||created.Item.OccurredAt.CheckValid()!=nil{t.Fatalf("exact create=%#v %v",created,err)}
	for name,mutate:=range map[string]func(*pb.CreateRecordRequest){"int8 overflow":func(v *pb.CreateRecordRequest){v.Int8Value=128},"int16 overflow":func(v *pb.CreateRecordRequest){v.Int16Value=32768},"int32 overflow":func(v *pb.CreateRecordRequest){v.Int32Value=2147483648},"uint8 overflow":func(v *pb.CreateRecordRequest){v.Uint8Value=256},"uint16 overflow":func(v *pb.CreateRecordRequest){v.Uint16Value=65536},"uint32 overflow":func(v *pb.CreateRecordRequest){v.Uint32Value=4294967296},"json nil":func(v *pb.CreateRecordRequest){v.Document=nil},"timestamp nil":func(v *pb.CreateRecordRequest){v.OccurredAt=nil},"timestamp invalid":func(v *pb.CreateRecordRequest){v.OccurredAt=&timestamppb.Timestamp{Seconds:253402300800}},"json decode":func(v *pb.CreateRecordRequest){v.Document=structpb.NewStringValue("invalid")}}{request:=validCreate(name,1);mutate(request);_,err:=NewCreateRecordLogic(ctx,service).CreateRecord(request);requireStatus(t,err,codes.InvalidArgument,"invalid field value")}
	nullRequest:=validCreate("json-null",1);nullRequest.Document=structpb.NewNullValue();nullCreated,err:=NewCreateRecordLogic(ctx,service).CreateRecord(nullRequest);if err!=nil||nullCreated.Item.Document.GetNullValue()!=structpb.NullValue_NULL_VALUE{t.Fatalf("json null=%#v %v",nullCreated,err)}
	overflow:=int64(128);_,err=NewUpdateRecordLogic(ctx,service).UpdateRecord(&pb.UpdateRecordRequest{Id:created.Item.Id,TenantId:1,UpdateMask:&fieldmaskpb.FieldMask{Paths:[]string{"int8_value"}},Int8Value:&overflow});requireStatus(t,err,codes.InvalidArgument,"invalid field value")
	_,err=getRecordToPB(&ent.Record{Document:&entschema.JSONPayload{Value:"marshal-error"},OccurredAt:time.Unix(1700000000,0)});requireStatus(t,err,codes.Internal,"crud operation failed")
	_,err=getRecordToPB(&ent.Record{Document:&entschema.JSONPayload{Value:"valid"},OccurredAt:time.Date(10000,time.January,1,0,0,0,0,time.UTC)});requireStatus(t,err,codes.Internal,"crud operation failed")
}
func TestNillableValuesAndProtocNames(t *testing.T){
	ctx:=context.Background();client:=enttest.Open(t,dialect.SQLite,"file:nillable?mode=memory&cache=shared&_fk=1");defer client.Close();service:=&svc.ServiceContext{DB:client}
	absent,err:=NewCreateRecordLogic(ctx,service).CreateRecord(validCreate("absent",1));if err!=nil{t.Fatal(err)};if absent.Item.NillableInt8!=nil||absent.Item.NillableUint16!=nil||absent.Item.NillableUuid!=nil||absent.Item.NillableOccurredAt!=nil||absent.Item.NillablePayload!=nil||absent.Item.Version_2Name!="v2"||absent.Item.Reset_!="reset"||absent.Item.String_!="string"||absent.Item.Descriptor_!="descriptor"||absent.Item.Foo!="foo"||absent.Item.GetFoo_!="get-foo"{t.Fatalf("absent nillable=%#v",absent.Item)}
	if absent.Item.State!=pb.RecordState_RECORD_STATE_STATE_ACTIVE{t.Fatalf("active enum=%v",absent.Item.State)}
	unknown,err:=getRecordToPB(&ent.Record{State:"unknown",Document:&entschema.JSONPayload{Value:"valid"},OccurredAt:time.Unix(1700000000,0)});if err!=nil||unknown.State!=pb.RecordState_RECORD_STATE_UNSPECIFIED{t.Fatalf("unknown enum=%#v %v",unknown,err)}
	empty:=[]byte{};mapped,err:=getRecordToPB(&ent.Record{Document:&entschema.JSONPayload{Value:"valid"},OccurredAt:time.Unix(1700000000,0),NillablePayload:&empty});if err!=nil||mapped.NillablePayload==nil||len(mapped.NillablePayload)!=0{t.Fatalf("empty bytes projection=%#v %v",mapped,err)};mapped,err=getRecordToPB(&ent.Record{Document:&entschema.JSONPayload{Value:"valid"},OccurredAt:time.Unix(1700000000,0)});if err!=nil||mapped.NillablePayload!=nil{t.Fatalf("nil bytes projection=%#v %v",mapped,err)}
	signed:=int64(12);unsigned:=uint64(34);uuidValue:=uuid.NewString();occurred:=timestamppb.New(time.Unix(1700000100,456000000));request:=validCreate("present",1);request.NillableInt8=&signed;request.NillableUint16=&unsigned;request.NillableUuid=&uuidValue;request.NillableOccurredAt=occurred;request.NillablePayload=[]byte("present")
	present,err:=NewCreateRecordLogic(ctx,service).CreateRecord(request);if err!=nil{t.Fatal(err)};if present.Item.NillableInt8==nil||*present.Item.NillableInt8!=signed||present.Item.NillableUint16==nil||*present.Item.NillableUint16!=unsigned||present.Item.NillableUuid==nil||*present.Item.NillableUuid!=uuidValue||present.Item.NillableOccurredAt.AsTime()!=occurred.AsTime()||string(present.Item.NillablePayload)!="present"{t.Fatalf("present nillable=%#v",present.Item)}
	encoded,err:=proto.Marshal(present.Item);if err!=nil{t.Fatal(err)};roundTrip:=new(pb.Record);if err:=proto.Unmarshal(encoded,roundTrip);err!=nil{t.Fatal(err)};if string(roundTrip.NillablePayload)!="present"{t.Fatalf("round-trip bytes=%q",roundTrip.NillablePayload)}
	updatedSigned:=int64(-8);updatedUnsigned:=uint64(65535);updatedUUID:=uuid.NewString();updatedTime:=timestamppb.New(time.Unix(1700000200,789000000));resetValue,stringValue,descriptorValue,fooValue,getFooValue:="updated-reset","updated-string","updated-descriptor","updated-foo","updated-get-foo";updated,err:=NewUpdateRecordLogic(ctx,service).UpdateRecord(&pb.UpdateRecordRequest{Id:present.Item.Id,TenantId:1,UpdateMask:&fieldmaskpb.FieldMask{Paths:[]string{"nillable_int8","nillable_uint16","nillable_uuid","nillable_occurred_at","nillable_payload","reset","string","descriptor","foo","get_foo"}},NillableInt8:&updatedSigned,NillableUint16:&updatedUnsigned,NillableUuid:&updatedUUID,NillableOccurredAt:updatedTime,NillablePayload:[]byte{},Reset_:&resetValue,String_:&stringValue,Descriptor_:&descriptorValue,Foo:&fooValue,GetFoo_:&getFooValue});if err!=nil{t.Fatal(err)};if updated.Item.NillableInt8==nil||*updated.Item.NillableInt8!=updatedSigned||updated.Item.NillableUint16==nil||*updated.Item.NillableUint16!=updatedUnsigned||updated.Item.NillableUuid==nil||*updated.Item.NillableUuid!=updatedUUID||updated.Item.NillableOccurredAt.AsTime()!=updatedTime.AsTime()||updated.Item.NillablePayload==nil||len(updated.Item.NillablePayload)!=0||updated.Item.Reset_!=resetValue||updated.Item.String_!=stringValue||updated.Item.Descriptor_!=descriptorValue||updated.Item.Foo!=fooValue||updated.Item.GetFoo_!=getFooValue{t.Fatalf("updated nillable=%#v",updated.Item)}
}
func TestTenantHelper(t *testing.T){if got,err:=crudtenant.RequireTenantID(7);err!=nil||got!=7{t.Fatalf("positive=%d %v",got,err)};for _,value:=range []int64{0,-1}{_,err:=crudtenant.RequireTenantID(value);requireStatus(t,err,codes.Unauthenticated,"tenant context is required")};if strconv.IntSize==32{_,err:=crudtenant.RequireTenantID(int64(math.MaxInt32)+1);requireStatus(t,err,codes.Unauthenticated,"tenant context is required")}else if got,err:=crudtenant.RequireTenantID(math.MaxInt64);err!=nil||got!=math.MaxInt{t.Fatalf("max int=%d %v",got,err)}}
`

const realPlanProgram = `package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bufbuild/protocompile"
	"github.com/nxnminieye/nexa/generation/crudlogic"
	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

const protocolOptionsPath = "nexa/protocol/v1/options.proto"

type rpcRunner struct{ mode string }
func (r rpcRunner) Run(ctx context.Context, request toolchain.Request) (toolchain.Result,error) {
	if r.mode=="empty" { return toolchain.Result{ToolID:request.Tool.ID,Version:request.Tool.Version,ExecutableVersion:request.Tool.Probe.ExpectedVersion},nil }
	if r.mode=="unrelated" { target:=filepath.Join(request.StagingRoot,"backend/account/unrelated/value.go");if err:=os.MkdirAll(filepath.Dir(target),0755);err!=nil{return toolchain.Result{},err};if err:=os.WriteFile(target,[]byte("package unrelated\n"),0644);err!=nil{return toolchain.Result{},err};return toolchain.Result{ToolID:request.Tool.ID,Version:request.Tool.Version,ExecutableVersion:request.Tool.Probe.ExpectedVersion},nil }
	if r.mode=="marker" { target:=filepath.Join(request.StagingRoot,"backend/account/internal/pb/marker.go");if err:=os.MkdirAll(filepath.Dir(target),0755);err!=nil{return toolchain.Result{},err};content:=[]byte("// Code generated by protoc-gen-go. DO NOT EDIT.\n// source: rpc/record.crud.generated.proto\npackage accountpb\nconst GeneratedMarker = true\n");if err:=os.WriteFile(target,content,0644);err!=nil{return toolchain.Result{},err};return toolchain.Result{ToolID:request.Tool.ID,Version:request.Tool.Version,ExecutableVersion:request.Tool.Probe.ExpectedVersion},nil }
	if r.mode=="bad" { target:=filepath.Join(request.StagingRoot,"backend/account/internal/pb/pb.go");if err:=os.MkdirAll(filepath.Dir(target),0755);err!=nil{return toolchain.Result{},err};if err:=os.WriteFile(target,[]byte("package accountpb\nfunc"),0644);err!=nil{return toolchain.Result{},err};return toolchain.Result{ToolID:request.Tool.ID,Version:request.Tool.Version,ExecutableVersion:request.Tool.Probe.ExpectedVersion},nil }
	if strings.HasPrefix(r.mode,"shadow-") { area:=strings.TrimPrefix(r.mode,"shadow-");target:=filepath.Join(request.StagingRoot,"backend/account",area,"shadow.go");if err:=os.MkdirAll(filepath.Dir(target),0755);err!=nil{return toolchain.Result{},err};if err:=os.WriteFile(target,[]byte("package shadow\n"),0644);err!=nil{return toolchain.Result{},err} }
	if r.mode=="nested-pb" { target:=filepath.Join(request.StagingRoot,"backend/account/internal/pb/nested/pb.go");if err:=os.MkdirAll(filepath.Dir(target),0755);err!=nil{return toolchain.Result{},err};if err:=os.WriteFile(target,[]byte("package nested\n"),0644);err!=nil{return toolchain.Result{},err} }
	if r.mode=="foreign-file" { target:=filepath.Join(request.StagingRoot,"backend/account/internal/pb/output.txt");if err:=os.MkdirAll(filepath.Dir(target),0755);err!=nil{return toolchain.Result{},err};if err:=os.WriteFile(target,[]byte("foreign"),0644);err!=nil{return toolchain.Result{},err} }
	protoPath:="rpc/record.crud.generated.proto";paths,err:=generatePB(ctx,request.RepositoryRoot,request.StagingRoot,protoPath,request.Stdin,request.Environment);if err!=nil{return toolchain.Result{},err};if len(paths)!=2{return toolchain.Result{},fmt.Errorf("generated files=%d",len(paths))};target:="";var content []byte;for _,path:=range paths{candidate,readErr:=os.ReadFile(path);if readErr!=nil{return toolchain.Result{},readErr};if bytes.Contains(candidate,[]byte("source: "+protoPath)){target=path;content=candidate}};if target==""{return toolchain.Result{},errors.New("generated CRUD PB missing")}
	if r.mode=="alias"||r.mode=="alias-message"{content,err=os.ReadFile(filepath.Join(request.RepositoryRoot,"backend/account/cmd/plan/aliased_pb.go.txt"));if err!=nil{return toolchain.Result{},err}}
	if r.mode=="alias-enum"{content=[]byte(strings.Replace(string(content),"import (","import (\n\toldpb \"example.com/crudlogicfixture/backend/account/oldpb\"",1));content=[]byte(strings.Replace(string(content),"type RecordState int32","type RecordState = oldpb.RecordState",1))}
	if r.mode=="wrong-package"{content=[]byte(strings.Replace(string(content),"package accountpb","package wrongpb",1))}
	if err:=os.WriteFile(target,content,0644);err!=nil{return toolchain.Result{},err}
	return toolchain.Result{ToolID:request.Tool.ID,Version:request.Tool.Version,ExecutableVersion:request.Tool.Probe.ExpectedVersion},nil
}

func generatePB(ctx context.Context,repositoryRoot,outputRoot,protoPath string,content []byte,environment []toolchain.EnvVar)([]string,error){
	goExecutable,err:=exec.LookPath("go");if err!=nil{return nil,err};goExecutable,err=filepath.EvalSymlinks(goExecutable);if err!=nil{return nil,err};moduleRoot:=filepath.Join(repositoryRoot,"backend/account");moduleCommand:=exec.CommandContext(ctx,goExecutable,"list","-m","-f={{.Dir}}","github.com/nxnminieye/nexa");moduleCommand.Dir=moduleRoot;moduleCommand.Env=processEnvironment(environment);moduleOutput,err:=moduleCommand.Output();if err!=nil{return nil,err};optionsContent,err:=os.ReadFile(filepath.Join(strings.TrimSpace(string(moduleOutput)),"generation/protocol/nexa/protocol/v1/options.proto"));if err!=nil{return nil,err}
	compiler:=protocompile.Compiler{Resolver:protocompile.WithStandardImports(&protocompile.SourceResolver{Accessor:protocompile.SourceAccessorFromMap(map[string]string{protoPath:string(content),protocolOptionsPath:string(optionsContent)})}),SourceInfoMode:protocompile.SourceInfoStandard}
	files,err:=compiler.Compile(ctx,protoPath);if err!=nil{return nil,err};if len(files)!=1{return nil,fmt.Errorf("compiled files=%d",len(files))}
	request:=&pluginpb.CodeGeneratorRequest{FileToGenerate:[]string{protoPath,protocolOptionsPath}};seen:=map[string]bool{}
	var add func(protoreflect.FileDescriptor);add=func(file protoreflect.FileDescriptor){if file==nil||seen[file.Path()]{return};for index:=0;index<file.Imports().Len();index++{add(file.Imports().Get(index).FileDescriptor)};seen[file.Path()]=true;request.ProtoFile=append(request.ProtoFile,protodesc.ToFileDescriptorProto(file))};add(files[0])
	parameter,err:=generatorParameter(request.ProtoFile);if err!=nil{return nil,err};request.Parameter=&parameter
	encoded,err:=proto.Marshal(request);if err!=nil{return nil,err}
	command:=exec.CommandContext(ctx,goExecutable,"run","-mod=mod","google.golang.org/protobuf/cmd/protoc-gen-go");command.Dir=moduleRoot;command.Stdin=bytes.NewReader(encoded);var stdout,stderr bytes.Buffer;command.Stdout=&stdout;command.Stderr=&stderr;command.Env=processEnvironment(environment)
	if err:=command.Run();err!=nil{return nil,fmt.Errorf("protoc-gen-go: %w: %s",err,stderr.String())};response:=new(pluginpb.CodeGeneratorResponse);if err:=proto.Unmarshal(stdout.Bytes(),response);err!=nil{return nil,err};if response.GetError()!=""{return nil,errors.New(response.GetError())}
	paths:=make([]string,0,len(response.File));for _,file:=range response.File{name:=filepath.Clean(filepath.FromSlash(file.GetName()));if !filepath.IsLocal(name)||filepath.Ext(name)!=".go"{return nil,fmt.Errorf("invalid generated path %q",file.GetName())};target:=filepath.Join(outputRoot,name);if err:=os.MkdirAll(filepath.Dir(target),0755);err!=nil{return nil,err};if err:=os.WriteFile(target,[]byte(file.GetContent()),0644);err!=nil{return nil,err};paths=append(paths,target)};sort.Strings(paths);return paths,nil
}

func processEnvironment(values []toolchain.EnvVar)[]string{result:=make([]string,0,len(values)+1);for _,value:=range values{result=append(result,value.Name+"="+value.Value)};result=append(result,"GOPACKAGESDRIVER=off");sort.Strings(result);return result}

func generatorParameter(files []*descriptorpb.FileDescriptorProto)(string,error){mappings:=map[string]string{protocolOptionsPath:"example.com/crudlogicfixture/backend/account/internal/pb;accountpb"};result:=[]string{"module=example.com/crudlogicfixture"};var mapped []string;for _,file:=range files{if file.GetOptions().GetGoPackage()!=""{continue};value,ok:=mappings[file.GetName()];if !ok{return "",fmt.Errorf("descriptor %q has no Go import mapping",file.GetName())};mapped=append(mapped,"M"+file.GetName()+"="+value)};sort.Strings(mapped);return strings.Join(append(result,mapped...),","),nil}

func main(){
	consumer,err:=filepath.Abs(".");if err!=nil{panic(err)};consumer,err=filepath.EvalSymlinks(consumer);if err!=nil{panic(err)}; consumerRepository,err:=filepath.EvalSymlinks(filepath.Dir(filepath.Dir(consumer)));if err!=nil{panic(err)}; repository,err:=filepath.EvalSymlinks(filepath.Dir(consumerRepository));if err!=nil{panic(err)}
	staging,err:=os.MkdirTemp("","crudlogic-host-staging-");if err!=nil{panic(err)};staging,err=filepath.EvalSymlinks(staging);if err!=nil{panic(err)};defer os.RemoveAll(staging)
	scratch,err:=os.MkdirTemp("","crudlogic-host-scratch-");if err!=nil{panic(err)};scratch,err=filepath.EvalSymlinks(scratch);if err!=nil{panic(err)};defer os.RemoveAll(scratch)
	goTool,environment:=goExecution(staging)
	rel,err:=filepath.Rel(repository,filepath.Join(consumer,"ent/schema"));if err!=nil{panic(err)}; schema,err:=provenance.ParseDomainSource(filepath.ToSlash(rel));if err!=nil{panic(err)}
	destination,err:=crudproto.ProjectProtoDestination("record","rpc/record.proto");if err!=nil{panic(err)}
	verified,err:=crudproto.InvokeEntGraphHost(context.Background(),crudproto.EntGraphHostSpec{RepositoryRoot:repository,StagingRoot:staging,ScratchParent:scratch,SchemaDir:schema,ProtoPackage:"fixture.v1",GoPackage:"example.com/crudlogicfixture/backend/account/internal/pb;accountpb",Destination:destination,Tool:goTool,Environment:environment,Runner:toolchain.NewExecRunner(),MultiTenant:crudproto.MultiTenantConfig{Enabled:true}});if err!=nil{if e,ok:=err.(*crudproto.Error);ok{panic(fmt.Sprintf("host code=%s stage=%s reason=%s pointer=%s source=%s tool=%s exit=%d diagnostic=%s",e.Code(),e.Stage(),e.Reason(),e.Pointer(),e.Source(),e.ToolID(),e.ExitCode(),e.Diagnostic()))};panic(err)}
	protoArtifact,err:=verified.ProtoArtifact();if err!=nil{panic(err)};protoPath,err:=protoArtifact.Path();if err!=nil{panic(err)};protoContent,err:=protoArtifact.Bytes();if err!=nil{panic(err)};pbPaths,err:=generatePB(context.Background(),consumerRepository,consumerRepository,protoPath,protoContent,environment);if err!=nil{panic(err)};if len(pbPaths)!=2{panic(fmt.Sprintf("fixture PB files=%d",len(pbPaths)))};sawCRUD,sawOptions:=false,false;for _,pbPath:=range pbPaths{pbContent,readErr:=os.ReadFile(pbPath);if readErr!=nil{panic(readErr)};if bytes.Contains(pbContent,[]byte("source: "+protoPath)){sawCRUD=true;for _,name:=range []string{"Version_2Name","Reset_","String_","Descriptor_","Foo","GetFoo_","NillablePayload"}{if !bytes.Contains(pbContent,[]byte(name)){panic("real protoc-gen-go naming missing "+name)}}};if bytes.Contains(pbContent,[]byte("source: "+protocolOptionsPath)){sawOptions=true};oldContent:=[]byte(strings.Replace(string(pbContent),"package accountpb","package oldpb",1));if bytes.Equal(oldContent,pbContent){panic("PB package rewrite missing")};oldTarget:=filepath.Join(consumer,"oldpb",filepath.Base(pbPath));if err:=os.MkdirAll(filepath.Dir(oldTarget),0755);err!=nil{panic(err)};if err:=os.WriteFile(oldTarget,oldContent,0644);err!=nil{panic(err)}};if !sawCRUD||!sawOptions{panic("generated PB roles incomplete")}
	plan,err:=crudlogic.BuildPlan(verified,crudlogic.ServiceLayout{ServiceID:"record",EntSchemaDir:"backend/account/ent/schema",LogicRoot:"backend/account/internal/logic"},crudlogic.BuildOptions{});if err!=nil{panic(err)}
	validation,err:=os.MkdirTemp("","crudlogic-validation-");if err!=nil{panic(err)};defer os.RemoveAll(validation)
	rpcTool:=toolchain.Tool{ID:"rpc-go",Version:"v1",Executable:"rpc-go",Probe:toolchain.ExecutableProbe{ExpectedVersion:"rpc-go-v1"}}
	mode:="normal";if len(os.Args)>1{mode=os.Args[1]}
	for name,value:=range map[string]string{"GOPACKAGESDRIVER":"/ambient/driver","GOFLAGS":"-mod=mod","GOENV":"/ambient/goenv","GOWORK":"/ambient/go.work"}{if err:=os.Setenv(name,value);err!=nil{panic(err)}}
	beforeValidation:=treeDigest(consumerRepository);validated,err:=crudlogic.Validate(context.Background(),plan,crudlogic.ValidationInput{RepositoryRoot:consumerRepository,StagingRoot:validation,RPCGoTool:rpcTool,GoTool:goTool,Runner:rpcRunner{mode:mode},Environment:environment});if err!=nil{panic(fmt.Sprintf("validate: %v",errors.Unwrap(err)))};if treeDigest(consumerRepository)!=beforeValidation{panic("successful validation wrote repository")}
	write:=len(os.Args)>1&&os.Args[1]=="write"
	inputs,err:=validated.TransactionInputs(func(name string,content []byte)error{if !write{return nil};target:=filepath.Join(consumerRepository,filepath.FromSlash(name));if err:=os.MkdirAll(filepath.Dir(target),0755);err!=nil{return err};return os.WriteFile(target,content,0644)});if err!=nil{panic(err)};if len(inputs)!=16{panic(fmt.Sprintf("inputs=%d",len(inputs)))};wantIDs:=map[string]bool{"crud-logic.record.listrecord":true,"crud-logic.record.getrecord":true,"crud-logic.record.createrecord":true,"crud-logic.record.updaterecord":true,"crud-logic.record.deleterecord":true,"crud-logic.record.getuintrecord":true,"crud-logic.record.deleteuintrecord":true,"crud-logic.record.getstringrecord":true,"crud-logic.record.listuuidrecord":true,"crud-logic.record.getuuidrecord":true,"crud-logic.record.getnarrowidrecord":true,"crud-logic.record.deletenarrowidrecord":true,"crud-logic.record.getnarrowuintidrecord":true,"crud-logic.record.listsubsetrecord":true,"crud-logic.record.deletesubsetrecord":true,"crud-logic.record.tenant-helper":true};for _,input:=range inputs{if !wantIDs[input.ID]{panic("unexpected candidate "+input.ID)};delete(wantIDs,input.ID)};if len(wantIDs)!=0{panic(fmt.Sprintf("missing candidates=%v",wantIDs))}
	if mode=="matrix"{runMatrix(verified,plan,validated,consumerRepository,repository,rpcTool,goTool,environment)}
	if mode=="all"{runStagingNegatives(plan,consumerRepository,rpcTool,goTool,environment);runMatrix(verified,plan,validated,consumerRepository,repository,rpcTool,goTool,environment);_,err=validated.TransactionInputs(func(name string,content []byte)error{target:=filepath.Join(consumerRepository,filepath.FromSlash(name));if err:=os.MkdirAll(filepath.Dir(target),0755);err!=nil{return err};return os.WriteFile(target,content,0644)});if err!=nil{panic(err)}}
}

func runStagingNegatives(plan crudlogic.Plan,repository string,rpcTool,goTool toolchain.Tool,environment []toolchain.EnvVar){for _,mode:=range []string{"empty","unrelated","marker","alias","alias-enum","alias-message","wrong-package","shadow-ent","shadow-internal/svc","shadow-internal/logic","nested-pb","foreign-file"}{stage,err:=os.MkdirTemp("","crudlogic-negative-");if err!=nil{panic(err)};_,validationErr:=crudlogic.Validate(context.Background(),plan,crudlogic.ValidationInput{RepositoryRoot:repository,StagingRoot:stage,RPCGoTool:rpcTool,GoTool:goTool,Runner:rpcRunner{mode:mode},Environment:environment});os.RemoveAll(stage);if validationErr==nil{panic("Validate accepted "+mode+" RPC staging")}}}

func runMatrix(verified crudproto.EntGraphPlan,plan crudlogic.Plan,validated crudlogic.ValidatedPlan,repository,hostRepository string,rpcTool,goTool toolchain.Tool,environment []toolchain.EnvVar){
	if _,err:=crudlogic.BuildPlan(verified,crudlogic.ServiceLayout{ServiceID:"wrong",EntSchemaDir:"backend/account/ent/schema",LogicRoot:"backend/account/internal/logic"},crudlogic.BuildOptions{});err==nil{panic("service mismatch accepted")}
	if _,err:=crudlogic.BuildPlan(verified,crudlogic.ServiceLayout{ServiceID:"record",EntSchemaDir:"backend/account/ent/schema",LogicRoot:"backend/other/internal/logic"},crudlogic.BuildOptions{});err==nil{panic("cross service layout accepted")}
	sources,err:=verified.Sources();if err!=nil{panic(err)}
	runTenantHelperLifecycle(validated,sources)
	build:=func(value crudlogic.ValidatedPlan,previous *artifact.Manifest)(transaction.Plan,error){return transaction.Build(context.Background(),repository,func(_ string,emit func(string,[]byte)error)(transaction.PlanRequest,error){inputs,err:=value.TransactionInputs(emit);if err!=nil{return transaction.PlanRequest{},err};probes,err:=value.StaleOwnershipProbes();if err!=nil{return transaction.PlanRequest{},err};return transaction.PlanRequest{Generator:artifact.GeneratorSpec{ID:"crud-proto",Version:"v1.0.0"},Sources:sources,Expected:inputs,StaleOwnershipProbes:probes,Previous:previous,ManifestPath:".nexa/generation/crud-proto.record.manifest.json",RevalidateSources:func(context.Context)([]provenance.Source,error){return sources,nil}},nil})}
	first,err:=build(validated,nil);if err!=nil{panic(err)};defer first.Close();next,ok:=first.NextManifest();if !ok||len(next.Artifacts())!=1||next.Artifacts()[0].ID()!="crud-logic.record.tenant-helper"{panic("managed/manual ownership drift")};if _,err:=transaction.Write(context.Background(),first,repository,transaction.WriteOptions{PlanDigest:first.PlanDigest()});err!=nil{panic(err)}
	manual:=filepath.Join(repository,"backend/account/internal/logic/listrecordlogic.go");file,err:=os.OpenFile(manual,os.O_APPEND|os.O_WRONLY,0);if err!=nil{panic(err)};if _,err=file.WriteString("\n// consumer edit\n");err!=nil{panic(err)};if err=file.Close();err!=nil{panic(err)}
	repeat,err:=build(validated,&next);if err!=nil{panic(err)};defer repeat.Close();if len(repeat.Changes())!=0{panic(fmt.Sprintf("default changed manual: %d",len(repeat.Changes())))}
	overwritePlan,err:=crudlogic.BuildPlan(verified,crudlogic.ServiceLayout{ServiceID:"record",EntSchemaDir:"backend/account/ent/schema",LogicRoot:"backend/account/internal/logic"},crudlogic.BuildOptions{OverwriteExisting:true});if err!=nil{panic(err)}
	stage,err:=os.MkdirTemp("","crudlogic-overwrite-");if err!=nil{panic(err)};defer os.RemoveAll(stage);overwrite,err:=crudlogic.Validate(context.Background(),overwritePlan,crudlogic.ValidationInput{RepositoryRoot:repository,StagingRoot:stage,RPCGoTool:rpcTool,GoTool:goTool,Runner:rpcRunner{},Environment:environment});if err!=nil{panic(err)}
	overwriteTx,err:=build(overwrite,&next);if err!=nil{panic(err)};defer overwriteTx.Close();if len(overwriteTx.Changes())!=1||overwriteTx.Changes()[0].Path()!="backend/account/internal/logic/listrecordlogic.go"{panic(fmt.Sprintf("overwrite changes=%d",len(overwriteTx.Changes())))}
	badStage,err:=os.MkdirTemp("","crudlogic-bad-");if err!=nil{panic(err)};defer os.RemoveAll(badStage);before:=treeDigest(repository);_,badErr:=crudlogic.Validate(context.Background(),plan,crudlogic.ValidationInput{RepositoryRoot:repository,StagingRoot:badStage,RPCGoTool:rpcTool,GoTool:goTool,Runner:rpcRunner{mode:"bad"},Environment:environment});after:=treeDigest(repository);if badErr==nil||before!=after{panic("failed typecheck wrote repository")}
	runTenantOnly(repository,hostRepository,rpcTool)
}

func runTenantHelperLifecycle(validated crudlogic.ValidatedPlan,sources []provenance.Source){
	repository,err:=os.MkdirTemp("","crudlogic-helper-lifecycle-");if err!=nil{panic(err)};defer os.RemoveAll(repository)
	var helper transaction.ArtifactInput;var content []byte
	inputs,err:=validated.TransactionInputs(func(name string,value []byte)error{if strings.HasSuffix(name,"/crudtenant/tenant.generated.go"){content=append([]byte(nil),value...)};return nil});if err!=nil{panic(err)}
	for _,input:=range inputs{if input.ID=="crud-logic.record.tenant-helper"{helper=input}}
	if helper.ID==""||helper.Owner!="nexa.dev/generator/crud-logic/v1"||helper.Probe==nil||len(content)==0{panic("managed helper ownership invalid")}
	probes,err:=validated.StaleOwnershipProbes();if err!=nil||len(probes)!=1||probes[0]==nil{panic("stale helper probes missing")};probes[0]=nil;fresh,err:=validated.StaleOwnershipProbes();if err!=nil||len(fresh)!=1||fresh[0]==nil{panic("stale helper probes retained caller slice")}
	build:=func(expected []transaction.ArtifactInput,previous *artifact.Manifest,stale []transaction.OwnershipProbe,value []byte)(transaction.Plan,error){return transaction.Build(context.Background(),repository,func(_ string,emit func(string,[]byte)error)(transaction.PlanRequest,error){if len(expected)!=0{if err:=emit(helper.Path,value);err!=nil{return transaction.PlanRequest{},err}};return transaction.PlanRequest{Generator:artifact.GeneratorSpec{ID:"crud-proto",Version:"v1.0.0"},Sources:sources,Expected:expected,StaleOwnershipProbes:stale,Previous:previous,ManifestPath:".nexa/generation/crud-proto.record.manifest.json",RevalidateSources:func(context.Context)([]provenance.Source,error){return sources,nil}},nil})}
	first,err:=build([]transaction.ArtifactInput{helper},nil,nil,content);if err!=nil||len(first.Conflicts())!=0||len(first.Changes())!=1||first.Changes()[0].Kind()!=transaction.ChangeCreate{panic(fmt.Sprintf("helper first changes=%#v conflicts=%#v err=%v",first.Changes(),first.Conflicts(),err))};defer first.Close();if _,err:=transaction.Write(context.Background(),first,repository,transaction.WriteOptions{PlanDigest:first.PlanDigest()});err!=nil{panic(err)};firstManifest,ok:=first.NextManifest();firstArtifacts:=firstManifest.Artifacts();if !ok||firstManifest.Generator().ID()!="crud-proto"||len(firstArtifacts)!=1||firstArtifacts[0].Owner()!="crud-proto"{panic("helper first manifest identity invalid")}
	repeat,err:=build([]transaction.ArtifactInput{helper},&firstManifest,nil,content);if err!=nil||len(repeat.Changes())!=0||len(repeat.Conflicts())!=0{panic(fmt.Sprintf("helper repeat changes=%#v conflicts=%#v err=%v",repeat.Changes(),repeat.Conflicts(),err))};defer repeat.Close()
	old:=append(append([]byte(nil),content...),[]byte("\n// previous generated helper version\n")...);if err:=os.WriteFile(filepath.Join(repository,filepath.FromSlash(helper.Path)),old,0644);err!=nil{panic(err)}
	oldManifest,err:=artifact.NewManifest(artifact.ManifestSpec{Generator:artifact.GeneratorSpec{ID:"crud-proto",Version:"v1.0.0"},Sources:sources,Artifacts:[]artifact.ArtifactSpec{{ID:helper.ID,Path:helper.Path,Owner:"crud-proto",Digest:provenance.SHA256(old),Sources:helper.Sources,StalePolicy:artifact.StaleDeleteIfUnmodified}}});if err!=nil{panic(err)}
	oldManifestJSON,err:=oldManifest.CanonicalJSON();if err!=nil{panic(err)};if err:=os.WriteFile(filepath.Join(repository,".nexa/generation/crud-proto.record.manifest.json"),oldManifestJSON,0644);err!=nil{panic(err)}
	update,err:=build([]transaction.ArtifactInput{helper},&oldManifest,nil,content);if err!=nil||len(update.Conflicts())!=0||len(update.Changes())!=1||update.Changes()[0].Kind()!=transaction.ChangeUpdate{panic(fmt.Sprintf("helper update changes=%#v conflicts=%#v err=%v",update.Changes(),update.Conflicts(),err))};defer update.Close();if _,err:=transaction.Write(context.Background(),update,repository,transaction.WriteOptions{PlanDigest:update.PlanDigest()});err!=nil{panic(err)};currentManifest,ok:=update.NextManifest();currentArtifacts:=currentManifest.Artifacts();if !ok||currentManifest.Generator().ID()!="crud-proto"||len(currentArtifacts)!=1||currentArtifacts[0].Owner()!="crud-proto"{panic("helper update manifest identity invalid")}
	remove,err:=build(nil,&currentManifest,fresh,nil);if err!=nil||len(remove.Conflicts())!=0||len(remove.Changes())!=1||remove.Changes()[0].Kind()!=transaction.ChangeDelete{panic(fmt.Sprintf("helper delete changes=%#v conflicts=%#v err=%v",remove.Changes(),remove.Conflicts(),err))};defer remove.Close()
	if _,err:=transaction.Write(context.Background(),remove,repository,transaction.WriteOptions{PlanDigest:remove.PlanDigest()});err!=nil{panic(err)};if _,err:=os.Stat(filepath.Join(repository,filepath.FromSlash(helper.Path)));!errors.Is(err,os.ErrNotExist){panic("helper delete did not remove artifact")}
}

func treeDigest(root string)string{hash:=sha256.New();err:=filepath.WalkDir(root,func(name string,entry os.DirEntry,err error)error{if err!=nil{return err};if entry.IsDir(){return nil};relative,err:=filepath.Rel(root,name);if err!=nil{return err};content,err:=os.ReadFile(name);if err!=nil{return err};hash.Write([]byte(filepath.ToSlash(relative)));hash.Write(content);return nil});if err!=nil{panic(err)};return fmt.Sprintf("%x",hash.Sum(nil))}

type rejectingRunner struct{}
func(rejectingRunner)Run(context.Context,toolchain.Request)(toolchain.Result,error){return toolchain.Result{},fmt.Errorf("RPC runner must not run")}
func runTenantOnly(repository,hostRepository string,rpcTool toolchain.Tool){
	hostStage,err:=os.MkdirTemp("","tenant-only-host-");if err!=nil{panic(err)};hostStage,err=filepath.EvalSymlinks(hostStage);if err!=nil{panic(err)};defer os.RemoveAll(hostStage);scratch,err:=os.MkdirTemp("","tenant-only-scratch-");if err!=nil{panic(err)};scratch,err=filepath.EvalSymlinks(scratch);if err!=nil{panic(err)};defer os.RemoveAll(scratch)
	goTool,environment:=goExecution(hostStage)
	schema,err:=provenance.ParseDomainSource(filepath.ToSlash(filepath.Join(filepath.Base(repository),"backend/tenantonly/ent/schema")));if err!=nil{panic(err)};destination,err:=crudproto.ProjectProtoDestination("tenantonly","rpc/tenantonly.proto");if err!=nil{panic(err)}
	verified,err:=crudproto.InvokeEntGraphHost(context.Background(),crudproto.EntGraphHostSpec{RepositoryRoot:hostRepository,StagingRoot:hostStage,ScratchParent:scratch,SchemaDir:schema,ProtoPackage:"tenantonly.v1",GoPackage:"example.com/crudlogictenantonly/pb;pb",Destination:destination,Tool:goTool,Environment:environment,Runner:toolchain.NewExecRunner(),MultiTenant:crudproto.MultiTenantConfig{Enabled:true}});if err!=nil{panic(err)};if verified.HasCRUD(){panic("tenant-only gained CRUD")}
	plan,err:=crudlogic.BuildPlan(verified,crudlogic.ServiceLayout{ServiceID:"tenantonly",EntSchemaDir:"backend/tenantonly/ent/schema",LogicRoot:"backend/tenantonly/internal/logic"},crudlogic.BuildOptions{});if err!=nil{panic(err)};stage,err:=os.MkdirTemp("","tenant-only-validation-");if err!=nil{panic(err)};defer os.RemoveAll(stage)
	validated,err:=crudlogic.Validate(context.Background(),plan,crudlogic.ValidationInput{RepositoryRoot:repository,StagingRoot:stage,RPCGoTool:rpcTool,GoTool:goTool,Runner:rejectingRunner{},Environment:environment});if err!=nil{panic(err)};inputs,err:=validated.TransactionInputs(func(string,[]byte)error{return nil});if err!=nil{panic(err)};if len(inputs)!=1||inputs[0].CreateManual||inputs[0].OverwriteManual||inputs[0].ID!="crud-logic.tenantonly.tenant-helper"{panic("tenant-only helper ownership invalid")}
}

func goExecution(staging string)(toolchain.Tool,[]toolchain.EnvVar){
	goexe,err:=exec.LookPath("go");if err!=nil{panic(err)};goexe,err=filepath.EvalSymlinks(goexe);if err!=nil{panic(err)}
	version,err:=exec.Command(goexe,"version").Output();if err!=nil{panic(err)};goroot,err:=exec.Command(goexe,"env","GOROOT").Output();if err!=nil{panic(err)};modcache,err:=exec.Command(goexe,"env","GOMODCACHE").Output();if err!=nil{panic(err)};proxy,err:=exec.Command(goexe,"env","GOPROXY").Output();if err!=nil{panic(err)};sumdb,err:=exec.Command(goexe,"env","GOSUMDB").Output();if err!=nil{panic(err)}
	home,tmp,gopath,cache:=filepath.Join(staging,"home"),filepath.Join(staging,"tmp"),filepath.Join(staging,"gopath"),filepath.Join(staging,"gocache");for _,p:=range []string{home,tmp,gopath,cache}{if err:=os.MkdirAll(p,0755);err!=nil{panic(err)}}
	tool:=toolchain.Tool{ID:"go",Version:"go-test",Executable:goexe,InputScopes:[]string{"repository","scratch"},WriteScopes:[]string{"scratch"},Environment:[]toolchain.EnvironmentRule{{Name:"PATH",Source:toolchain.EnvironmentHost},{Name:"GOROOT",Source:toolchain.EnvironmentHost},{Name:"GOMODCACHE",Source:toolchain.EnvironmentHost},{Name:"GOPROXY",Source:toolchain.EnvironmentHost},{Name:"GOSUMDB",Source:toolchain.EnvironmentHost},{Name:"HOME",Source:toolchain.EnvironmentScratch},{Name:"TMPDIR",Source:toolchain.EnvironmentScratch},{Name:"GOPATH",Source:toolchain.EnvironmentScratch},{Name:"GOCACHE",Source:toolchain.EnvironmentScratch},{Name:"GOWORK",Source:toolchain.EnvironmentFixed,FixedValue:"off"},{Name:"GOENV",Source:toolchain.EnvironmentFixed,FixedValue:"off"},{Name:"GOTOOLCHAIN",Source:toolchain.EnvironmentFixed,FixedValue:"local"},{Name:"GOFLAGS",Source:toolchain.EnvironmentFixed,FixedValue:""},{Name:"CGO_ENABLED",Source:toolchain.EnvironmentFixed,FixedValue:"0"}},Probe:toolchain.ExecutableProbe{Args:[]string{"version"},ExpectedVersion:strings.TrimSpace(string(version))}}
	env:=[]toolchain.EnvVar{{Name:"PATH",Value:os.Getenv("PATH")},{Name:"GOROOT",Value:strings.TrimSpace(string(goroot))},{Name:"GOMODCACHE",Value:strings.TrimSpace(string(modcache))},{Name:"GOPROXY",Value:strings.TrimSpace(string(proxy))},{Name:"GOSUMDB",Value:strings.TrimSpace(string(sumdb))},{Name:"HOME",Value:home},{Name:"TMPDIR",Value:tmp},{Name:"GOPATH",Value:gopath},{Name:"GOCACHE",Value:cache},{Name:"GOWORK",Value:"off"},{Name:"GOENV",Value:"off"},{Name:"GOTOOLCHAIN",Value:"local"},{Name:"GOFLAGS",Value:""},{Name:"CGO_ENABLED",Value:"0"}}
	return tool,env
}
`

func TestCandidateOverlayWinsOverRPCSkeleton(t *testing.T) {
	got := mergeOverlay(map[string][]byte{"logic.go": []byte("rpc skeleton")}, map[string][]byte{"logic.go": []byte("crud candidate")})
	if string(got["logic.go"]) != "crud candidate" {
		t.Fatalf("overlay selected %q", got["logic.go"])
	}
}

func TestRealFixtureRawImportRoles(t *testing.T) {
	imports := func(name, source string) map[string]bool {
		t.Helper()
		file, err := parser.ParseFile(token.NewFileSet(), name, source, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		result := make(map[string]bool, len(file.Imports))
		for _, item := range file.Imports {
			result[strings.Trim(item.Path.Value, `"`)] = true
		}
		return result
	}
	runtimeImports := imports("crud_runtime_test.go", realCRUDRuntimeTest)
	planImports := imports("main.go", realPlanProgram)
	if runtimeImports["bytes"] || !runtimeImports["context"] {
		t.Fatalf("runtime imports = %#v", runtimeImports)
	}
	if !planImports["bytes"] || !planImports["context"] {
		t.Fatalf("plan imports = %#v", planImports)
	}
}

type validationCountingRunner struct{ calls int }

func (r *validationCountingRunner) Run(context.Context, toolchain.Request) (toolchain.Result, error) {
	r.calls++
	return toolchain.Result{}, nil
}

func TestValidateRejectsInvalidRootsAndGoProviderBeforeRunner(t *testing.T) {
	goTool, environment := validationGoProvider(t)
	canonical := func(path string) string {
		t.Helper()
		value, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	tests := map[string]struct {
		prepare func(repository, staging string, input *ValidationInput)
		reason  string
	}{
		"empty repository":    {prepare: func(_, _ string, input *ValidationInput) { input.RepositoryRoot = "" }, reason: "repository_invalid"},
		"relative repository": {prepare: func(_, _ string, input *ValidationInput) { input.RepositoryRoot = "." }, reason: "repository_invalid"},
		"empty staging":       {prepare: func(_, _ string, input *ValidationInput) { input.StagingRoot = "" }, reason: "staging_invalid"},
		"relative staging":    {prepare: func(_, _ string, input *ValidationInput) { input.StagingRoot = "." }, reason: "staging_invalid"},
		"overlapping roots":   {prepare: func(repository, _ string, input *ValidationInput) { input.StagingRoot = repository }, reason: "staging_invalid"},
		"nonempty staging": {prepare: func(_, staging string, _ *ValidationInput) {
			if err := os.WriteFile(filepath.Join(staging, "foreign"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, reason: "staging_invalid"},
		"missing provider": {prepare: func(_, _ string, input *ValidationInput) { input.Environment = nil }, reason: "go_environment_invalid"},
		"ambient go mismatch": {prepare: func(_, _ string, input *ValidationInput) {
			shell, err := exec.LookPath("sh")
			if err != nil {
				t.Fatal(err)
			}
			input.GoTool.Executable = canonical(shell)
		}, reason: "go_tool_invalid"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			repository := canonical(t.TempDir())
			staging := canonical(t.TempDir())
			runner := new(validationCountingRunner)
			input := ValidationInput{
				RepositoryRoot: repository,
				StagingRoot:    staging,
				RPCGoTool:      toolchain.Tool{ID: "rpc-go", Version: "v1", Executable: "rpc-go", Probe: toolchain.ExecutableProbe{ExpectedVersion: "rpc-go-v1"}},
				GoTool:         goTool,
				Runner:         runner,
				Environment:    append([]toolchain.EnvVar(nil), environment...),
			}
			test.prepare(repository, staging, &input)
			_, err := Validate(context.Background(), Plan{state: new(planState)}, input)
			var value *Error
			if err == nil || !strings.Contains(err.Error(), "crud logic") || !asValidationError(err, &value) || value.Reason() != test.reason {
				t.Fatalf("error=%#v reason=%v", err, value)
			}
			if runner.calls != 0 {
				t.Fatalf("runner calls=%d", runner.calls)
			}
		})
	}
}

func asValidationError(err error, target **Error) bool {
	value, ok := err.(*Error)
	if ok {
		*target = value
	}
	return ok
}

func validationGoProvider(t *testing.T) (toolchain.Tool, []toolchain.EnvVar) {
	t.Helper()
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goExecutable, err = filepath.EvalSymlinks(goExecutable)
	if err != nil {
		t.Fatal(err)
	}
	command := func(arguments ...string) string {
		t.Helper()
		output, err := exec.Command(goExecutable, arguments...).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(output))
	}
	tool := toolchain.Tool{
		ID: "go", Version: "go-test", Executable: goExecutable,
		InputScopes: []string{"repository", "scratch"}, WriteScopes: []string{"scratch"},
		Environment: []toolchain.EnvironmentRule{
			{Name: "PATH", Source: toolchain.EnvironmentHost}, {Name: "GOROOT", Source: toolchain.EnvironmentHost},
			{Name: "GOMODCACHE", Source: toolchain.EnvironmentHost}, {Name: "GOPROXY", Source: toolchain.EnvironmentHost},
			{Name: "GOSUMDB", Source: toolchain.EnvironmentHost}, {Name: "HOME", Source: toolchain.EnvironmentScratch},
			{Name: "TMPDIR", Source: toolchain.EnvironmentScratch}, {Name: "GOPATH", Source: toolchain.EnvironmentScratch},
			{Name: "GOCACHE", Source: toolchain.EnvironmentScratch}, {Name: "GOWORK", Source: toolchain.EnvironmentFixed, FixedValue: "off"},
			{Name: "GOENV", Source: toolchain.EnvironmentFixed, FixedValue: "off"}, {Name: "GOTOOLCHAIN", Source: toolchain.EnvironmentFixed, FixedValue: "local"},
			{Name: "GOFLAGS", Source: toolchain.EnvironmentFixed, FixedValue: ""}, {Name: "CGO_ENABLED", Source: toolchain.EnvironmentFixed, FixedValue: "0"},
		},
		Probe: toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: command("version")},
	}
	scratch := t.TempDir()
	environment := []toolchain.EnvVar{
		{Name: "PATH", Value: os.Getenv("PATH")}, {Name: "GOROOT", Value: command("env", "GOROOT")},
		{Name: "GOMODCACHE", Value: command("env", "GOMODCACHE")}, {Name: "GOPROXY", Value: command("env", "GOPROXY")},
		{Name: "GOSUMDB", Value: command("env", "GOSUMDB")}, {Name: "HOME", Value: filepath.Join(scratch, "home")},
		{Name: "TMPDIR", Value: filepath.Join(scratch, "tmp")}, {Name: "GOPATH", Value: filepath.Join(scratch, "gopath")},
		{Name: "GOCACHE", Value: filepath.Join(scratch, "gocache")}, {Name: "GOWORK", Value: "off"},
		{Name: "GOENV", Value: "off"}, {Name: "GOTOOLCHAIN", Value: "local"}, {Name: "GOFLAGS", Value: ""}, {Name: "CGO_ENABLED", Value: "0"},
	}
	return tool, environment
}

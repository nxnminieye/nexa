package buildinfo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const canonicalBuildInfoJSON = "{\"apiVersion\":\"nexa.dev/build-info/v1\",\"kind\":\"BuildInfo\",\"service\":\"sample\",\"serviceKind\":\"rpc\",\"contractVersion\":\"sample.Sample\",\"available\":true,\"commit\":\"0123456789abcdef\",\"dirty\":false,\"vcsTime\":\"2026-07-11T01:02:03Z\",\"goVersion\":\"go1.25.0\",\"modulePath\":\"example.com/sample\",\"moduleVersion\":\"v1.2.3\"}\n"

func TestCanonicalJSONGoldenAndAccessors(t *testing.T) {
	identity := testIdentity(t)
	info, err := Resolve(identity, ReaderFunc(func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.25.0",
			Main:      debug.Module{Path: "example.com/sample", Version: "v1.2.3"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.modified", Value: "false"},
				{Key: "vcs.time", Value: "2026-07-11T09:02:03+08:00"},
				{Key: "vcs.revision", Value: "0123456789abcdef"},
			},
		}, true
	}))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := info.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != canonicalBuildInfoJSON {
		t.Fatalf("CanonicalJSON() =\n%s\nwant\n%s", encoded, canonicalBuildInfoJSON)
	}
	if info.APIVersion() != APIVersion || info.Identity() != identity || info.Service() != "sample" || info.Kind() != "rpc" || info.ContractVersion() != "sample.Sample" {
		t.Fatalf("identity accessors = %q %#v %q %q %q", info.APIVersion(), info.Identity(), info.Service(), info.Kind(), info.ContractVersion())
	}
}

func TestCanonicalJSONFallbackIncludesEveryField(t *testing.T) {
	info, err := Resolve(testIdentity(t), ReaderFunc(func() (*debug.BuildInfo, bool) { return nil, false }))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := info.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"apiVersion\":\"nexa.dev/build-info/v1\",\"kind\":\"BuildInfo\",\"service\":\"sample\",\"serviceKind\":\"rpc\",\"contractVersion\":\"sample.Sample\",\"available\":false,\"commit\":\"unknown\",\"dirty\":true,\"vcsTime\":\"\",\"goVersion\":\"\",\"modulePath\":\"\",\"moduleVersion\":\"\"}\n"
	if string(encoded) != want {
		t.Fatalf("fallback JSON = %s, want %s", encoded, want)
	}
}

func TestSchemaValidatesCanonicalAndStructuralNegatives(t *testing.T) {
	schema := compiledTestSchema(t)
	var canonical any
	if err := json.Unmarshal([]byte(canonicalBuildInfoJSON), &canonical); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(canonical); err != nil {
		t.Fatalf("schema rejected canonical JSON: %v", err)
	}

	base := canonical.(map[string]any)
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing", mutate: func(value map[string]any) { delete(value, "moduleVersion") }},
		{name: "null", mutate: func(value map[string]any) { value["vcsTime"] = nil }},
		{name: "wrong type", mutate: func(value map[string]any) { value["dirty"] = "false" }},
		{name: "unknown field", mutate: func(value map[string]any) { value["profile"] = "test" }},
		{name: "wrong apiVersion const", mutate: func(value map[string]any) { value["apiVersion"] = "nexa.dev/build-info/v2" }},
		{name: "wrong kind const", mutate: func(value map[string]any) { value["kind"] = "RuntimeInfo" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyValue := make(map[string]any, len(base))
			for key, value := range base {
				copyValue[key] = value
			}
			test.mutate(copyValue)
			if err := schema.Validate(copyValue); err == nil {
				t.Fatal("schema accepted invalid document")
			}
		})
	}
}

func TestSchemaReturnsDefensiveCopy(t *testing.T) {
	first := Schema()
	if len(first) == 0 {
		t.Fatal("Schema() returned empty content")
	}
	first[0] ^= 0xff
	if bytes.Equal(first, Schema()) {
		t.Fatal("mutating Schema() changed future results")
	}
}

func TestZeroInfoAndInvalidInfoStateCannotSerialize(t *testing.T) {
	_, err := (Info{}).CanonicalJSON()
	assertInternalError(t, err, "identity_service_invalid", "/service")

	identity := testIdentity(t)
	valid := Info{identity: identity, available: true, commit: "abc123", dirty: false}
	tests := []struct {
		name, reason, pointer string
		mutate                func(*Info)
	}{
		{name: "available unknown commit", reason: "info_state_invalid", pointer: "/commit", mutate: func(info *Info) { info.commit = "unknown" }},
		{name: "available invalid commit", reason: "info_state_invalid", pointer: "/commit", mutate: func(info *Info) { info.commit = "bad commit" }},
		{name: "unavailable non fallback commit", reason: "info_state_invalid", pointer: "/commit", mutate: func(info *Info) { info.available = false; info.commit = "abc123"; info.dirty = true }},
		{name: "commit before dirty", reason: "info_state_invalid", pointer: "/commit", mutate: func(info *Info) { info.available = false; info.commit = "abc123"; info.dirty = false }},
		{name: "unavailable clean", reason: "info_state_invalid", pointer: "/dirty", mutate: func(info *Info) { info.available = false; info.commit = "unknown"; info.dirty = false }},
		{name: "noncanonical time", reason: "vcs_time_invalid", pointer: "/vcsTime", mutate: func(info *Info) { info.vcsTime = "2026-07-11T09:02:03+08:00" }},
		{name: "invalid Go version", reason: "go_version_invalid", pointer: "/goVersion", mutate: func(info *Info) { info.goVersion = "go1.25\n" }},
		{name: "invalid module path", reason: "module_path_invalid", pointer: "/modulePath", mutate: func(info *Info) { info.modulePath = "Example.com/sample" }},
		{name: "invalid module version", reason: "module_version_invalid", pointer: "/moduleVersion", mutate: func(info *Info) { info.moduleVersion = "v1.2" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := valid
			test.mutate(&info)
			_, err := info.CanonicalJSON()
			assertInternalError(t, err, test.reason, test.pointer)
		})
	}
}

func TestResolveConcurrentDeterminism(t *testing.T) {
	identity := testIdentity(t)
	build := &debug.BuildInfo{
		GoVersion: "go1.25.0", Main: debug.Module{Path: "example.com/sample", Version: "v1.2.3"},
		Settings: []debug.BuildSetting{{Key: "vcs.time", Value: "2026-07-11T01:02:03Z"}, {Key: "vcs.revision", Value: "abc123"}, {Key: "vcs.modified", Value: "false"}},
	}
	reader := ReaderFunc(func() (*debug.BuildInfo, bool) { return build, true })
	want, err := Resolve(identity, reader)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := want.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	const workers = 100
	errorsChannel := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			got, resolveErr := Resolve(identity, reader)
			if resolveErr != nil {
				errorsChannel <- resolveErr
				return
			}
			gotJSON, jsonErr := got.CanonicalJSON()
			if jsonErr != nil {
				errorsChannel <- jsonErr
				return
			}
			if got != want || !bytes.Equal(gotJSON, wantJSON) {
				errorsChannel <- fmt.Errorf("concurrent result differs")
			}
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Error(err)
	}
}

func TestCurrentPreservesIdentityAndProducesSchemaValidJSON(t *testing.T) {
	identity := testIdentity(t)
	info, err := Current(identity)
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}
	if info.Identity() != identity {
		t.Fatalf("Identity() = %#v, want %#v", info.Identity(), identity)
	}
	if info.Available() {
		if info.Commit() == "unknown" {
			t.Fatal("available Current() has fallback commit")
		}
	} else if info.Commit() != "unknown" || !info.Dirty() {
		t.Fatalf("unavailable Current() = commit %q dirty %v", info.Commit(), info.Dirty())
	}
	encoded, err := info.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if err := compiledTestSchema(t).Validate(document); err != nil {
		t.Fatalf("Current JSON does not match schema: %v", err)
	}
}

func compiledTestSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	var schemaDocument any
	if err := json.Unmarshal(Schema(), &schemaDocument); err != nil {
		t.Fatalf("Schema() is not JSON: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	const location = "https://nexa.dev/schemas/runtime/build-info-v1.schema.json"
	if err := compiler.AddResource(location, schemaDocument); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func testIdentity(t *testing.T) Identity {
	t.Helper()
	identity, err := NewIdentity("sample", "rpc", "sample.Sample")
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func assertInternalError(t *testing.T, err error, reason, pointer string) {
	t.Helper()
	var buildErr *Error
	if !errors.As(err, &buildErr) {
		t.Fatalf("error = %T %v, want *Error", err, err)
	}
	if buildErr.Code() != "build_info_invalid" || buildErr.Reason() != reason || buildErr.Pointer() != pointer {
		t.Fatalf("error projection = %q %q %q, want build_info_invalid %q %q", buildErr.Code(), buildErr.Reason(), buildErr.Pointer(), reason, pointer)
	}
}

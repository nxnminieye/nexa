package sdkpython

import (
	"context"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/internal/sdkpythonassets"
	"github.com/nxnminieye/nexa/nexactl/plugin"
)

func TestWriteHandlerUsesOnlyRepositoryRoot(t *testing.T) {
	owner := &recordingOwner{writeResult: sdkpythonassets.WriteResult{APIVersion: "nexa.dev/sdk-python-assets-write-result/v1", IndexDigest: "sha256:" + repeatHex("a"), BootstrapDigest: "sha256:" + repeatHex("b"), Roles: []sdkpythonassets.Role{}, ObjectsWritten: []string{}, ObjectsReused: []string{}}}
	candidate, err := newWithOwner(owner, false)
	if err != nil {
		t.Fatal(err)
	}
	command := candidate.Spec().Commands[0]
	result, err := command.Run(context.Background(), plugin.Invocation{Flags: map[string]any{"repo-root": "/repo"}})
	if err != nil {
		t.Fatal(err)
	}
	if owner.lastWrite != (sdkpythonassets.WriteRequest{RepoRoot: "/repo"}) {
		t.Fatalf("request=%#v", owner.lastWrite)
	}
	if !reflect.DeepEqual(result, owner.writeResult) {
		t.Fatalf("result=%#v", result)
	}
}

func TestWriteHandlerRejectsMissingExtraAndPositionalInput(t *testing.T) {
	owner := &recordingOwner{}
	candidate, err := newWithOwner(owner, false)
	if err != nil {
		t.Fatal(err)
	}
	run := candidate.Spec().Commands[0].Run
	for _, invocation := range []plugin.Invocation{{Flags: map[string]any{}}, {Flags: map[string]any{"repo-root": "/repo", "old-cas": "x"}}, {Args: []string{"x"}, Flags: map[string]any{"repo-root": "/repo"}}} {
		if _, err := run(context.Background(), invocation); err == nil {
			t.Fatalf("invocation accepted: %#v", invocation)
		}
	}
	if owner.writeCalls != 0 {
		t.Fatalf("write calls=%d", owner.writeCalls)
	}
}

type recordingOwner struct {
	writeCalls  int
	lastWrite   sdkpythonassets.WriteRequest
	writeResult sdkpythonassets.WriteResult
}

func (o *recordingOwner) Write(_ context.Context, r sdkpythonassets.WriteRequest) (sdkpythonassets.WriteResult, error) {
	o.writeCalls++
	o.lastWrite = r
	return o.writeResult, nil
}
func (*recordingOwner) Check(context.Context, sdkpythonassets.CheckRequest) (sdkpythonassets.CheckResult, error) {
	return sdkpythonassets.CheckResult{}, nil
}
func (*recordingOwner) Build(context.Context, sdkpythonassets.BuildRequest) (sdkpythonassets.BuildResult, error) {
	return sdkpythonassets.BuildResult{}, nil
}
func repeatHex(value string) string {
	out := ""
	for len(out) < 64 {
		out += value
	}
	return out[:64]
}

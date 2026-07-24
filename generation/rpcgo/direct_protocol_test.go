package rpcgo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/directwrite"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/rpcgo"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
)

func TestRPCGoV2ProtocolAndDirectRunner(t *testing.T) {
	snapshot := rpcProtocolSnapshot(t)
	scopes := []directwrite.OutputScope{{Path: "backend/account/internal/pb", Mode: directwrite.OutputModeReplaceTree}}
	request := rpcgo.RPCGoRequest{APIVersion: rpcgo.RPCGoRequestAPIVersion, Kind: rpcgo.RPCGoRequestKind, ServiceID: "account", ModulePath: "example.com/consumer", ProtocolIR: snapshot, OutputScopes: scopes}
	stdin, err := rpcgo.CanonicalRPCGoRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := rpcgo.ParseRPCGoRequest(stdin)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := rpcgo.CanonicalRPCGoRequest(parsed)
	if !bytes.Equal(stdin, again) || bytes.Contains(stdin, []byte(`"artifacts"`)) || bytes.Contains(stdin, []byte(`"manual"`)) {
		t.Fatalf("RPC v2 request = %s", stdin)
	}

	repository, _ := filepath.EvalSymlinks(t.TempDir())
	runner := toolchain.DirectRunnerFunc(func(_ context.Context, call toolchain.DirectRequest) (toolchain.Result, error) {
		if call.RepositoryRoot != repository || !reflect.DeepEqual(call.Args, []string{"generate", "--service", "account"}) || !bytes.Equal(call.Stdin, stdin) {
			t.Fatalf("direct call = %#v", call)
		}
		resultBytes, resultErr := rpcgo.CanonicalRPCGoResult(rpcgo.RPCGoResult{APIVersion: rpcgo.RPCGoResultAPIVersion, Kind: rpcgo.RPCGoResultKind, Status: rpcgo.RPCGoResultGenerated, ServiceID: "account", InputDigest: provenance.SHA256(call.Stdin), OutputScopes: scopes})
		return toolchain.Result{ToolID: "rpc", Version: "v2", ExecutableVersion: "rpc-v2", Stdout: resultBytes}, resultErr
	})
	result, err := rpcgo.RunDirectRPCGo(context.Background(), request, rpcgo.DirectOptions{RepositoryRoot: repository, Tool: toolchain.Tool{ID: "rpc", Version: "v2", WriteScopes: []string{scopes[0].Path}, Probe: toolchain.ExecutableProbe{ExpectedVersion: "rpc-v2"}}, Runner: runner, OutputScopes: scopes})
	if err != nil || result.InputDigest != provenance.SHA256(stdin) {
		t.Fatalf("RunDirectRPCGo = %#v, %v", result, err)
	}
	wrongScope := []directwrite.OutputScope{{Path: "backend/account/manual", Mode: directwrite.OutputModeFileSet}}
	invalidRunner := toolchain.DirectRunnerFunc(func(_ context.Context, call toolchain.DirectRequest) (toolchain.Result, error) {
		encoded, resultErr := rpcgo.CanonicalRPCGoResult(rpcgo.RPCGoResult{APIVersion: rpcgo.RPCGoResultAPIVersion, Kind: rpcgo.RPCGoResultKind, Status: rpcgo.RPCGoResultGenerated, ServiceID: "account", InputDigest: provenance.SHA256(call.Stdin), OutputScopes: wrongScope})
		return toolchain.Result{ToolID: "rpc", Version: "v2", ExecutableVersion: "rpc-v2", Stdout: encoded}, resultErr
	})
	if _, err := rpcgo.RunDirectRPCGo(context.Background(), request, rpcgo.DirectOptions{RepositoryRoot: repository, Tool: toolchain.Tool{ID: "rpc", Version: "v2", WriteScopes: []string{scopes[0].Path}, Probe: toolchain.ExecutableProbe{ExpectedVersion: "rpc-v2"}}, Runner: invalidRunner, OutputScopes: scopes}); err == nil {
		t.Fatal("accepted result scopes that do not match the request")
	}
}

func TestRPCGoV2RejectsUnknownFieldsAndScopeEscape(t *testing.T) {
	request := rpcgo.RPCGoRequest{APIVersion: rpcgo.RPCGoRequestAPIVersion, Kind: rpcgo.RPCGoRequestKind, ServiceID: "account", ModulePath: "example.com/consumer", ProtocolIR: rpcProtocolSnapshot(t), OutputScopes: []directwrite.OutputScope{{Path: "backend/account/generated", Mode: directwrite.OutputModeReplaceTree}}}
	canonical, _ := rpcgo.CanonicalRPCGoRequest(request)
	var document map[string]any
	_ = json.Unmarshal(canonical, &document)
	document["artifacts"] = []any{}
	foreign, _ := json.Marshal(document)
	if _, err := rpcgo.ParseRPCGoRequest(foreign); err == nil {
		t.Fatal("accepted artifact field")
	}
	request.OutputScopes[0].Path = "backend/.git/generated"
	if _, err := rpcgo.CanonicalRPCGoRequest(request); err == nil {
		t.Fatal("accepted denied scope")
	}
	request = rpcgo.RPCGoRequest{APIVersion: rpcgo.RPCGoRequestAPIVersion, Kind: rpcgo.RPCGoRequestKind, ServiceID: "other", ModulePath: "example.com/consumer", ProtocolIR: rpcProtocolSnapshot(t), OutputScopes: []directwrite.OutputScope{{Path: "backend/other/generated", Mode: directwrite.OutputModeReplaceTree}}}
	if _, err := rpcgo.CanonicalRPCGoRequest(request); err == nil {
		t.Fatal("accepted mismatched outer and nested service identity")
	}
}

func TestRunDirectRPCGoRejectsFixedToolArgsBeforeInvocation(t *testing.T) {
	request := rpcgo.RPCGoRequest{APIVersion: rpcgo.RPCGoRequestAPIVersion, Kind: rpcgo.RPCGoRequestKind, ServiceID: "account", ModulePath: "example.com/consumer", ProtocolIR: rpcProtocolSnapshot(t), OutputScopes: []directwrite.OutputScope{{Path: "backend/account/generated", Mode: directwrite.OutputModeReplaceTree}}}
	called := false
	runner := toolchain.DirectRunnerFunc(func(context.Context, toolchain.DirectRequest) (toolchain.Result, error) {
		called = true
		return toolchain.Result{}, nil
	})
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	_, err := rpcgo.RunDirectRPCGo(context.Background(), request, rpcgo.DirectOptions{RepositoryRoot: repository, Tool: toolchain.Tool{ID: "rpc", Version: "v2", Args: []string{"wrapper"}, WriteScopes: []string{"backend/account/generated"}}, Runner: runner, OutputScopes: request.OutputScopes})
	if err == nil || called {
		t.Fatalf("fixed args rejection = %v, called = %v", err, called)
	}
}

func rpcProtocolSnapshot(t *testing.T) protocol.Snapshot {
	t.Helper()
	document := compileProtocol(t)
	canonical, err := protocol.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := provenance.ParseDomainSource("facts/account/protocol-ir.json")
	snapshot, err := protocol.ParseSnapshot(source, canonical)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

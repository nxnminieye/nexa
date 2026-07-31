package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateOptionsRejectsUnsafeGeneratedPaths(t *testing.T) {
	base := generatorOptions{
		generatedScope: "generated",
		modulePath:     "git.minieye.tech/nexa/example",
		packageName:    "coretransport",
		rpcOutput:      "generated/rpc.go",
		apiOutput:      "generated/api.go",
		protoOutput:    "generated/transportpb/core.pb.go",
		grpcOutput:     "generated/transportpb/core_grpc.pb.go",
	}
	tests := map[string]func(*generatorOptions){
		"repository escape": func(options *generatorOptions) { options.rpcOutput = "../rpc.go" },
		"git alias":         func(options *generatorOptions) { options.rpcOutput = ".GIT/rpc.go" },
		"case collision":    func(options *generatorOptions) { options.apiOutput = "GENERATED/RPC.GO" },
		"path overlap":      func(options *generatorOptions) { options.apiOutput = "generated" },
		"canonical input":   func(options *generatorOptions) { options.rpcOutput = canonicalProto },
		"scope case alias":  func(options *generatorOptions) { options.rpcOutput = "GENERATED/rpc.go" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			options := base
			mutate(&options)
			if err := validateOptions(&options); err == nil {
				t.Fatal("unsafe generated path accepted")
			}
		})
	}
}

func TestValidateOptionsRejectsInvalidGoNames(t *testing.T) {
	tests := []generatorOptions{
		{modulePath: "invalid module", packageName: "coretransport"},
		{modulePath: "git.minieye.tech/nexa/example", packageName: "type"},
		{modulePath: "git.minieye.tech/nexa/example", packageName: "coretransport", coreAPIImport: "invalid import"},
	}
	for _, options := range tests {
		options.generatedScope = "generated"
		options.rpcOutput = "generated/rpc.go"
		options.apiOutput = "generated/api.go"
		options.protoOutput = "generated/transportpb/core.pb.go"
		options.grpcOutput = "generated/transportpb/core_grpc.pb.go"
		if err := validateOptions(&options); err == nil {
			t.Fatalf("invalid options accepted: %#v", options)
		}
	}
}

func TestRepositoryFileAndOutputRejectSymlinkParents(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "backend", "core", "rpc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "backend", "core", "rpc", "desc")); err != nil {
		t.Fatal(err)
	}
	if _, err := openRepositoryFile(root, canonicalProto); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("canonical source symlink accepted: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "core.pb.go"), []byte("package transportpb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "generated", "bindings")); err != nil {
		t.Fatal(err)
	}
	if _, err := readRepositoryFile(root, "generated/bindings/core.pb.go"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("generated binding symlink accepted: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "generated", "transport")); err != nil {
		t.Fatal(err)
	}
	if err := replaceGeneratedScope(root, "generated/transport", map[string][]byte{"generated/transport/rpc.go": []byte("package generated\n")}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("generated output symlink accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "rpc.go")); !os.IsNotExist(err) {
		t.Fatalf("generated output escaped repository: %v", err)
	}
}

func TestReplaceGeneratedScopeRemovesStaleFiles(t *testing.T) {
	root := t.TempDir()
	scope := filepath.Join(root, "generated")
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scope, "stale.go"), []byte("package stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"generated/api.go": []byte("package generated\n"),
		"generated/rpc.go": []byte("package generated\n"),
	}
	if err := replaceGeneratedScope(root, "generated", files); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(scope, "stale.go")); !os.IsNotExist(err) {
		t.Fatalf("stale generated file remains: %v", err)
	}
	for relative, want := range files {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil || string(got) != string(want) {
			t.Fatalf("generated file %s = %q, %v", relative, got, err)
		}
	}
}

func TestValidateProtoBindingsRejectsSymlinkInput(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "core.pb.go"), []byte("package transportpb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "generated", "transportpb")); err != nil {
		t.Fatal(err)
	}
	options := generatorOptions{
		protoOutput: "generated/transportpb/core.pb.go",
		grpcOutput:  "generated/transportpb/core_grpc.pb.go",
	}
	if _, err := validateProtoBindings(root, "sha256:test", nil, options); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("generated binding symlink accepted: %v", err)
	}
}

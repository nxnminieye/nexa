package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/bufbuild/protocompile"
	"github.com/nxnminieye/nexa/generation/protocol"
	"golang.org/x/mod/module"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	generatorVersion = "nexa-core-iam-transport-gen v1.0.0"
	canonicalProto   = "backend/core/rpc/desc/core.proto"
	rpcOutput        = "core_rpc_transport_generated_test.go"
	apiOutput        = "core_api_transport_generated_test.go"
	protoOutput      = "transportpb/core.pb.go"
	grpcOutput       = "transportpb/core_grpc.pb.go"
	protoOutputSHA   = "sha256:63d0f15598d78963993221ec904df1d6bf6b2fe3a8cb5f369e0e388ef5bfc18e"
	grpcOutputSHA    = "sha256:6ad5db3da48ea329ec53e2cdc1c3eaa2a541554c91c2ed3fdc1d1e4a5976d0f9"
	rpcBegin         = "// BEGIN GENERATED CORE RPC TRANSPORT"
	rpcEnd           = "// END GENERATED CORE RPC TRANSPORT"
	apiBegin         = "// BEGIN GENERATED CORE API TRANSPORT"
	apiEnd           = "// END GENERATED CORE API TRANSPORT"
)

//go:embed core_transport_template.go.tmpl
var transportTemplate []byte

var canonicalMethods = []string{
	"CheckPermission", "CreateRole", "CurrentSession", "GetAccessCodes", "GetAllMenus", "GetIdentityAccount", "GetMenu", "GetPermission", "GetRole", "GetTenant", "GetTenantMember", "GetUserInfo", "Health", "ListIdentityAccounts", "ListMenus", "ListPermissions", "ListRoles", "ListTenantMembers", "ListTenants", "Login", "Logout", "ProvisionTenant", "Refresh", "Register", "ReplaceRoleMenus", "ReplaceRolePermissions", "ReplaceTenantMemberRoles", "Revoke", "ResetAccountPassword", "UpdateAccountStatus", "UpdateRole", "UpdateRoleStatus", "UpdateTenant", "UpdateTenantStatus", "UpdateTenantMemberStatus",
}

type fileResolver struct{ root string }

func (r fileResolver) Open(_ context.Context, name string) (io.ReadCloser, error) {
	if name == "" || path.IsAbs(name) || path.Clean(name) != name || strings.HasPrefix(name, "../") || name == ".." {
		return nil, errors.New("proto path is not repository-relative")
	}
	return openRepositoryFile(r.root, name)
}

type result struct {
	APIVersion     string                  `json:"apiVersion"`
	Generator      string                  `json:"generator"`
	Kind           string                  `json:"kind"`
	Source         string                  `json:"source"`
	SourceSHA256   string                  `json:"sourceSha256"`
	Outputs        map[string]string       `json:"outputs"`
	ProtoBindings  map[string]protoBinding `json:"protoBindings"`
	ServiceMethods int                     `json:"serviceMethods"`
}

type protoBinding struct {
	SourceSHA256 string `json:"sourceSha256"`
	OutputSHA256 string `json:"outputSha256"`
}

type generatorOptions struct {
	repositoryRoot  string
	generatedScope  string
	protoPath       string
	modulePath      string
	packageName     string
	coreAPIImport   string
	coreRPCImport   string
	transportImport string
	rpcOutput       string
	apiOutput       string
	protoOutput     string
	grpcOutput      string
}

func main() {
	var options generatorOptions
	flag.StringVar(&options.repositoryRoot, "repository-root", "", "materialized consumer repository root")
	flag.StringVar(&options.generatedScope, "generated-scope", "", "generated replacement directory")
	flag.StringVar(&options.protoPath, "proto", canonicalProto, "canonical repository-relative Proto path")
	flag.StringVar(&options.modulePath, "module-path", "", "consumer Go module path")
	flag.StringVar(&options.packageName, "package", "consumer_test", "generated Go package name")
	flag.StringVar(&options.coreAPIImport, "core-api-import", "", "materialized Core API import path")
	flag.StringVar(&options.coreRPCImport, "core-rpc-import", "", "materialized Core RPC import path")
	flag.StringVar(&options.transportImport, "transport-import", "", "generated protobuf import path")
	flag.StringVar(&options.rpcOutput, "rpc-output", rpcOutput, "generated RPC adapter path")
	flag.StringVar(&options.apiOutput, "api-output", apiOutput, "generated API adapter path")
	flag.StringVar(&options.protoOutput, "proto-output", protoOutput, "generated protobuf binding path")
	flag.StringVar(&options.grpcOutput, "grpc-output", grpcOutput, "generated gRPC binding path")
	flag.Parse()
	if err := generate(options); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validateOptions(options *generatorOptions) error {
	if options == nil {
		return errors.New("generator options are required")
	}
	if module.CheckPath(options.modulePath) != nil {
		return errors.New("module-path is invalid")
	}
	if !token.IsIdentifier(options.packageName) || options.packageName == "_" {
		return errors.New("package is invalid")
	}
	options.generatedScope = filepath.ToSlash(options.generatedScope)
	if !validOutputPath(options.generatedScope) || options.generatedScope == "." {
		return errors.New("generated-scope is invalid")
	}
	if pathsOverlap(options.generatedScope, canonicalProto) {
		return errors.New("generated-scope overlaps the canonical Proto")
	}
	if options.coreAPIImport == "" {
		options.coreAPIImport = options.modulePath + "/backend/core/api"
	}
	if options.coreRPCImport == "" {
		options.coreRPCImport = options.modulePath + "/backend/core/rpc/coreapp"
	}
	if options.transportImport == "" {
		options.transportImport = options.modulePath + "/transportpb"
	}
	for label, value := range map[string]string{
		"core-api-import": options.coreAPIImport, "core-rpc-import": options.coreRPCImport,
		"transport-import": options.transportImport,
	} {
		if !validImportPath(value) {
			return fmt.Errorf("%s is invalid", label)
		}
	}
	options.protoPath = filepath.ToSlash(options.protoPath)
	options.rpcOutput = filepath.ToSlash(options.rpcOutput)
	options.apiOutput = filepath.ToSlash(options.apiOutput)
	options.protoOutput = filepath.ToSlash(options.protoOutput)
	options.grpcOutput = filepath.ToSlash(options.grpcOutput)
	for label, value := range map[string]string{
		"rpc-output": options.rpcOutput, "api-output": options.apiOutput,
		"proto-output": options.protoOutput, "grpc-output": options.grpcOutput,
	} {
		if !validOutputPath(value) {
			return fmt.Errorf("%s is invalid", label)
		}
		if pathsOverlap(value, canonicalProto) {
			return fmt.Errorf("%s overlaps the canonical Proto", label)
		}
		if !pathWithinScope(options.generatedScope, value) {
			return fmt.Errorf("%s is outside generated-scope", label)
		}
	}
	paths := []string{filepath.ToSlash(options.rpcOutput), filepath.ToSlash(options.apiOutput), filepath.ToSlash(options.protoOutput), filepath.ToSlash(options.grpcOutput)}
	for left := range paths {
		for right := left + 1; right < len(paths); right++ {
			if pathsOverlap(paths[left], paths[right]) {
				return fmt.Errorf("generated output paths overlap: %s", paths[left])
			}
		}
	}
	return nil
}

func pathsOverlap(left, right string) bool {
	left, right = strings.ToLower(left), strings.ToLower(right)
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func pathWithinScope(scope, value string) bool {
	scope = filepath.ToSlash(path.Clean(scope))
	value = filepath.ToSlash(path.Clean(value))
	return scope != "." && value != scope && strings.HasPrefix(value, scope+"/")
}

func validImportPath(value string) bool {
	return module.CheckImportPath(value) == nil
}

func validOutputPath(value string) bool {
	value = filepath.ToSlash(value)
	if value == "" || path.IsAbs(value) || path.Clean(value) != value || value == "." || strings.HasPrefix(value, "../") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || strings.EqualFold(component, ".git") {
			return false
		}
	}
	return true
}

func generate(options generatorOptions) error {
	if err := validateOptions(&options); err != nil {
		return err
	}
	root, err := filepath.Abs(options.repositoryRoot)
	if err != nil || strings.TrimSpace(options.repositoryRoot) == "" {
		return errors.New("repository-root is required")
	}
	if filepath.Clean(root) != root {
		return errors.New("repository-root is not canonical")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	protoPath := filepath.ToSlash(options.protoPath)
	if protoPath != canonicalProto || path.Clean(protoPath) != protoPath || path.IsAbs(protoPath) {
		return fmt.Errorf("proto must be %s", canonicalProto)
	}
	protoBytes, err := readRepositoryFile(root, protoPath)
	if err != nil {
		return fmt.Errorf("read canonical proto: %w", err)
	}
	sourceDigest := sha256.Sum256(protoBytes)
	sourceSHA := "sha256:" + hex.EncodeToString(sourceDigest[:])
	if err := validateProto(root, protoPath); err != nil {
		return err
	}
	protoBindings, err := validateProtoBindings(root, sourceSHA, protoBytes, options)
	if err != nil {
		return err
	}
	rpcBody, err := section(string(transportTemplate), rpcBegin, rpcEnd)
	if err != nil {
		return err
	}
	apiBody, err := section(string(transportTemplate), apiBegin, apiEnd)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("// @nexa $contract: \"nexa.dev/source-comment/v1\"\n\n// Code generated by %s. DO NOT EDIT.\n// canonical-source: %s\n// canonical-source-sha256: %s\n\n", generatorVersion, canonicalProto, sourceSHA)
	rpc := header + fmt.Sprintf("package %s\n\nimport (\n\t\"context\"\n\t\"strconv\"\n\n\tcoreapp %q\n\ttransportpb %q\n\t\"google.golang.org/grpc/codes\"\n\t\"google.golang.org/grpc/metadata\"\n\tgrpcstatus \"google.golang.org/grpc/status\"\n)\n\n%s\n", options.packageName, options.coreRPCImport, options.transportImport, strings.TrimSpace(rpcBody))
	api := header + fmt.Sprintf("package %s\n\nimport (\n\t\"context\"\n\t\"net/http\"\n\t\"strconv\"\n\t\"strings\"\n\n\tcoreapi %q\n\tcoreapp %q\n\ttransportpb %q\n\t\"google.golang.org/grpc/codes\"\n\t\"google.golang.org/grpc/metadata\"\n\tgrpcstatus \"google.golang.org/grpc/status\"\n)\n\n%s\n", options.packageName, options.coreAPIImport, options.coreRPCImport, options.transportImport, strings.TrimSpace(apiBody))
	outputs := map[string][]byte{
		options.rpcOutput:   []byte(rpc),
		options.apiOutput:   []byte(api),
		options.protoOutput: nil,
		options.grpcOutput:  nil,
	}
	for _, output := range []string{options.protoOutput, options.grpcOutput} {
		content, readErr := readRepositoryFile(root, output)
		if readErr != nil {
			return fmt.Errorf("read generated Proto binding %s: %w", output, readErr)
		}
		outputs[output] = content
	}
	digests := make(map[string]string, len(outputs))
	for output, content := range outputs {
		digest := sha256.Sum256(content)
		digests[output] = "sha256:" + hex.EncodeToString(digest[:])
	}
	if err := replaceGeneratedScope(root, options.generatedScope, outputs); err != nil {
		return err
	}
	encoded, err := json.Marshal(result{APIVersion: "nexa.dev/core-iam-transport-generator/v1", Generator: generatorVersion, Kind: "CoreIAMTransportGeneration", Source: canonicalProto, SourceSHA256: sourceSHA, Outputs: digests, ProtoBindings: protoBindings, ServiceMethods: len(canonicalMethods)})
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(encoded, '\n'))
	return err
}

func validateProtoBindings(root, sourceSHA string, canonicalSource []byte, options generatorOptions) (map[string]protoBinding, error) {
	bindings := make(map[string]protoBinding, 2)
	for _, specification := range []struct {
		path, digest string
	}{
		{path: options.protoOutput, digest: protoOutputSHA},
		{path: options.grpcOutput, digest: grpcOutputSHA},
	} {
		relative := specification.path
		data, err := readRepositoryFile(root, relative)
		if err != nil {
			return nil, fmt.Errorf("read generated Proto binding %s: %w", relative, err)
		}
		wantSource := "// canonical-source: " + options.protoPath
		wantDigest := "// canonical-source-sha256: " + sourceSHA
		var sourceCount, digestCount int
		for _, line := range strings.Split(string(data), "\n") {
			switch {
			case line == wantSource:
				sourceCount++
			case strings.HasPrefix(line, "// canonical-source:"):
				return nil, fmt.Errorf("generated Proto binding %s names another canonical source", relative)
			case line == wantDigest:
				digestCount++
			case strings.HasPrefix(line, "// canonical-source-sha256:"):
				return nil, fmt.Errorf("generated Proto binding %s has a stale canonical source digest", relative)
			}
		}
		if sourceCount != 1 || digestCount != 1 {
			return nil, fmt.Errorf("generated Proto binding %s is not bound to the canonical source", relative)
		}
		contentDigest := sha256.Sum256(data)
		outputSHA := "sha256:" + hex.EncodeToString(contentDigest[:])
		if outputSHA != specification.digest {
			return nil, fmt.Errorf("generated Proto binding %s content digest is stale", relative)
		}
		bindings[relative] = protoBinding{SourceSHA256: sourceSHA, OutputSHA256: outputSHA}
	}
	generated, err := embeddedProtoDescriptor(root, options.protoOutput)
	if err != nil {
		return nil, err
	}
	canonical, err := compileCanonicalDescriptor(canonicalSource)
	if err != nil {
		return nil, err
	}
	generated.SourceCodeInfo = nil
	canonical.SourceCodeInfo = nil
	if !proto.Equal(generated, canonical) {
		return nil, errors.New("generated Proto descriptor differs from the canonical source")
	}
	return bindings, nil
}

func embeddedProtoDescriptor(root, relative string) (*descriptorpb.FileDescriptorProto, error) {
	source, err := readRepositoryFile(root, relative)
	if err != nil {
		return nil, fmt.Errorf("read generated Proto descriptor: %w", err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), relative, source, 0)
	if err != nil {
		return nil, fmt.Errorf("parse generated Proto binding: %w", err)
	}
	var descriptor string
	for _, declaration := range parsed.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, specification := range group.Specs {
			values, ok := specification.(*ast.ValueSpec)
			if !ok || len(values.Names) != 1 || values.Names[0].Name != "file_backend_core_rpc_desc_core_proto_rawDesc" || len(values.Values) != 1 {
				continue
			}
			descriptor, err = constantString(values.Values[0])
			if err != nil {
				return nil, fmt.Errorf("decode generated Proto descriptor: %w", err)
			}
		}
	}
	if descriptor == "" {
		return nil, errors.New("generated Proto descriptor is missing")
	}
	result := new(descriptorpb.FileDescriptorProto)
	if err := proto.Unmarshal([]byte(descriptor), result); err != nil {
		return nil, fmt.Errorf("unmarshal generated Proto descriptor: %w", err)
	}
	return result, nil
}

func constantString(expression ast.Expr) (string, error) {
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", errors.New("descriptor constant is not a string")
		}
		return strconv.Unquote(value.Value)
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", errors.New("descriptor constant uses a non-additive expression")
		}
		left, err := constantString(value.X)
		if err != nil {
			return "", err
		}
		right, err := constantString(value.Y)
		if err != nil {
			return "", err
		}
		return left + right, nil
	case *ast.ParenExpr:
		return constantString(value.X)
	default:
		return "", errors.New("descriptor constant has an unsupported expression")
	}
}

func compileCanonicalDescriptor(source []byte) (*descriptorpb.FileDescriptorProto, error) {
	resolver := &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{canonicalProto: string(source)})}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(resolver)}
	files, err := compiler.Compile(context.Background(), canonicalProto)
	if err != nil {
		return nil, fmt.Errorf("compile canonical Proto descriptor: %w", err)
	}
	file := files.FindFileByPath(canonicalProto)
	if file == nil {
		return nil, errors.New("compiled canonical Proto descriptor is missing")
	}
	return protodesc.ToFileDescriptorProto(file), nil
}

func validateProto(root, entry string) error {
	document, err := protocol.Compile(context.Background(), protocol.CompileOptions{ServiceID: "core", EntryFiles: []string{entry}, Resolver: fileResolver{root: root}})
	if err != nil {
		return fmt.Errorf("compile canonical proto: %w", err)
	}
	service, ok := document.Service("core.v1.CoreService")
	if !ok {
		return errors.New("canonical proto does not declare core.v1.CoreService")
	}
	methods := service.Methods()
	if len(methods) != len(canonicalMethods) {
		return fmt.Errorf("CoreService methods=%d, want %d", len(methods), len(canonicalMethods))
	}
	want := append([]string(nil), canonicalMethods...)
	sort.Strings(want)
	seen := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		if method.ClientStreaming() || method.ServerStreaming() {
			return fmt.Errorf("CoreService method %s uses streaming", method.Name())
		}
		if _, duplicate := seen[method.Name()]; duplicate {
			return fmt.Errorf("CoreService method %s is duplicated", method.Name())
		}
		seen[method.Name()] = struct{}{}
	}
	actual := make([]string, 0, len(seen))
	for name := range seen {
		actual = append(actual, name)
	}
	sort.Strings(actual)
	for index := range want {
		if want[index] != actual[index] {
			return fmt.Errorf("CoreService method set differs at %d: got %s want %s", index, actual[index], want[index])
		}
	}
	return nil
}

func section(content, begin, end string) (string, error) {
	start := strings.Index(content, begin)
	if start < 0 {
		return "", fmt.Errorf("transport template missing %s", begin)
	}
	start += len(begin)
	finish := strings.Index(content[start:], end)
	if finish < 0 {
		return "", fmt.Errorf("transport template missing %s", end)
	}
	body := strings.TrimSpace(content[start : start+finish])
	if body == "" {
		return "", fmt.Errorf("transport template section %s is empty", begin)
	}
	return body, nil
}

func replaceGeneratedScope(root, scope string, files map[string][]byte) error {
	if len(files) == 0 {
		return errors.New("generated scope has no outputs")
	}
	paths := make([]string, 0, len(files))
	for relative := range files {
		if !validOutputPath(relative) || !pathWithinScope(scope, relative) {
			return fmt.Errorf("generated output is outside generated-scope: %s", relative)
		}
		paths = append(paths, relative)
	}
	for left := range paths {
		for right := left + 1; right < len(paths); right++ {
			if pathsOverlap(paths[left], paths[right]) {
				return fmt.Errorf("generated output paths overlap: %s", paths[left])
			}
		}
	}
	sort.Strings(paths)
	if err := validateRepositoryDirectory(root, scope); err != nil {
		return err
	}
	target := filepath.Join(root, filepath.FromSlash(scope))
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("clear generated scope %s: %w", scope, err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("create generated scope %s: %w", scope, err)
	}
	for _, relative := range paths {
		if err := writeGeneratedFile(root, relative, files[relative]); err != nil {
			return err
		}
	}
	return nil
}

func validateRepositoryDirectory(root, relative string) error {
	relative = filepath.ToSlash(relative)
	if !validOutputPath(relative) || relative == "." {
		return fmt.Errorf("generated scope is unsafe: %s", relative)
	}
	current := root
	components := strings.Split(relative, "/")
	for index, component := range components {
		current = filepath.Join(current, filepath.FromSlash(component))
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			return fmt.Errorf("inspect generated scope %s: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("generated scope contains symlink: %s", relative)
		}
		if index == len(components)-1 && !info.IsDir() {
			return fmt.Errorf("generated scope is not a directory: %s", relative)
		}
	}
	return nil
}

func writeGeneratedFile(root, relative string, content []byte) error {
	relative = filepath.ToSlash(relative)
	if !validOutputPath(relative) {
		return fmt.Errorf("generated output path is unsafe: %s", relative)
	}
	target := filepath.Join(root, filepath.FromSlash(relative))
	current := root
	components := strings.Split(relative, "/")
	for index, component := range components {
		current = filepath.Join(current, filepath.FromSlash(component))
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			return fmt.Errorf("inspect generated output %s: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("generated output path contains symlink: %s", relative)
		}
		if index == len(components)-1 && info.IsDir() {
			return fmt.Errorf("generated output is a directory: %s", relative)
		}
	}
	if existing, err := os.ReadFile(target); err == nil && string(existing) == string(content) {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read generated output %s: %w", relative, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create generated output directory: %w", err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		return fmt.Errorf("write generated output %s: %w", relative, err)
	}
	return nil
}

func readRepositoryFile(root, relative string) ([]byte, error) {
	file, err := openRepositoryFile(root, relative)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return data, nil
}

func openRepositoryFile(root, relative string) (*os.File, error) {
	relative = filepath.ToSlash(relative)
	if relative == "" || path.IsAbs(relative) || path.Clean(relative) != relative || relative == "." || strings.HasPrefix(relative, "../") {
		return nil, errors.New("repository file path is unsafe")
	}
	current := root
	components := strings.Split(relative, "/")
	for index, component := range components {
		if component == "" || strings.EqualFold(component, ".git") {
			return nil, errors.New("repository file path is unsafe")
		}
		current = filepath.Join(current, filepath.FromSlash(component))
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("repository file path contains symlink: %s", relative)
		}
		if info.IsDir() && index == len(components)-1 {
			return nil, fmt.Errorf("repository file path is a directory: %s", relative)
		}
	}
	return os.Open(current)
}

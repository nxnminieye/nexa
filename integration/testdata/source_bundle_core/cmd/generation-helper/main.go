package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const helperVersion = "nexa-core-generation-helper v1.0.0"

const frameworkModulePath = "github.com/nxnminieye/nexa"

type artifactWire struct {
	Digest string `json:"digest"`
	ID     string `json:"id"`
	Path   string `json:"path"`
	Role   string `json:"role"`
}

type rpcResult struct {
	APIVersion   string         `json:"apiVersion"`
	Artifacts    []artifactWire `json:"artifacts"`
	GoTestPassed bool           `json:"goTestPassed"`
	InputDigest  string         `json:"inputDigest"`
	Kind         string         `json:"kind"`
	ServiceID    string         `json:"serviceId"`
}

type apiResult struct {
	APIVersion    string         `json:"apiVersion"`
	Artifacts     []artifactWire `json:"artifacts"`
	CoreServiceID string         `json:"coreServiceId"`
	GoTestPassed  bool           `json:"goTestPassed"`
	InputDigest   string         `json:"inputDigest"`
	Kind          string         `json:"kind"`
}

type operationKey struct{ method, mode, permission string }
type credentialKey struct{ id, typeID, location, name string }

type protocolDocument struct {
	APIVersion   string         `json:"apiVersion"`
	Files        []protocolFile `json:"files"`
	Kind         string         `json:"kind"`
	ServiceID    string         `json:"serviceId"`
	SourceDigest string         `json:"sourceDigest"`
	Sources      []sourceWire   `json:"sources"`
}

type sourceWire struct {
	Digest string `json:"digest"`
	Ref    string `json:"ref"`
}

type protocolFile struct {
	Enums    []protocolEnum    `json:"enums"`
	Messages []protocolMessage `json:"messages"`
	Path     string            `json:"path"`
	Services []protocolService `json:"services"`
}

type protocolEnum struct {
	FullName string              `json:"fullName"`
	Values   []protocolEnumValue `json:"values"`
}

type protocolEnumValue struct {
	Name   string `json:"name"`
	Number int    `json:"number"`
}

type protocolMessage struct {
	Fields    []protocolField `json:"fields"`
	FullName  string          `json:"fullName"`
	SourceRef string          `json:"sourceRef"`
}

type protocolField struct {
	Cardinality string       `json:"cardinality"`
	FullName    string       `json:"fullName"`
	JSONName    string       `json:"jsonName"`
	Number      int          `json:"number"`
	Oneof       string       `json:"oneof,omitempty"`
	Presence    string       `json:"presence"`
	SourceRef   string       `json:"sourceRef"`
	Type        protocolType `json:"type"`
}

type protocolType struct {
	Key   *protocolType `json:"key,omitempty"`
	Kind  string        `json:"kind"`
	Name  string        `json:"name,omitempty"`
	Value *protocolType `json:"value,omitempty"`
}

type protocolService struct {
	FullName string           `json:"fullName"`
	Methods  []protocolMethod `json:"methods"`
}

type protocolMethod struct {
	ClientStreaming bool                `json:"clientStreaming"`
	FullName        string              `json:"fullName"`
	HTTPProxy       json.RawMessage     `json:"httpProxy,omitempty"`
	Input           string              `json:"input"`
	Output          string              `json:"output"`
	RPCContext      *protocolRPCContext `json:"rpcContext,omitempty"`
	ServerStreaming bool                `json:"serverStreaming"`
	SourceRef       string              `json:"sourceRef"`
}

type protocolRPCContext struct {
	ContextFields []protocolContextBinding `json:"contextFields"`
}

type protocolContextBinding struct {
	RPCPath []string `json:"rpcPath"`
	Source  string   `json:"source"`
}

type expectedProtocolField struct {
	name, jsonName, typeName string
	number                   int
}

type protocolContract struct {
	prefix, service string
	methods         map[string][2]string
	messages        map[string][]expectedProtocolField
	contexts        map[string][]protocolContextBinding
}

var protocolContracts = map[string]protocolContract{
	"core": {
		prefix: "core.v1.", service: "core.v1.CoreService",
		methods: map[string][2]string{
			"CheckPermission": {"core.v1.CheckPermissionRequest", "core.v1.CheckPermissionResponse"},
			"Health":          {"core.v1.HealthRequest", "core.v1.HealthResponse"},
			"Login":           {"core.v1.LoginRequest", "core.v1.LoginResponse"},
			"Refresh":         {"core.v1.RefreshRequest", "core.v1.RefreshResponse"},
			"Register":        {"core.v1.RegisterRequest", "core.v1.RegisterResponse"},
			"Revoke":          {"core.v1.RevokeRequest", "core.v1.RevokeResponse"},
		},
		messages: map[string][]expectedProtocolField{
			"CheckPermissionRequest":  {{"tenant_id", "tenantId", "int64", 1}, {"subject_id", "subjectId", "string", 2}, {"permission", "permission", "string", 3}},
			"CheckPermissionResponse": {{"allowed", "allowed", "bool", 1}},
			"HealthRequest":           {},
			"HealthResponse":          {{"ready", "ready", "bool", 1}},
			"LoginRequest":            {{"tenant", "tenant", "string", 1}, {"username", "username", "string", 2}, {"password", "password", "string", 3}, {"request_id", "requestId", "string", 4}, {"trace_id", "traceId", "string", 5}},
			"LoginResponse":           {{"session_id", "sessionId", "string", 1}, {"access_token", "accessToken", "string", 2}, {"refresh_token", "refreshToken", "string", 3}},
			"RefreshRequest":          {{"refresh_token", "refreshToken", "string", 1}, {"request_id", "requestId", "string", 2}, {"trace_id", "traceId", "string", 3}},
			"RefreshResponse":         {{"session_id", "sessionId", "string", 1}, {"access_token", "accessToken", "string", 2}, {"refresh_token", "refreshToken", "string", 3}},
			"RegisterRequest":         {{"tenant", "tenant", "string", 1}, {"username", "username", "string", 2}, {"password", "password", "string", 3}, {"email", "email", "string", 4}, {"display_name", "displayName", "string", 5}, {"request_id", "requestId", "string", 6}, {"trace_id", "traceId", "string", 7}},
			"RegisterResponse":        {{"account_id", "accountId", "string", 1}},
			"RevokeRequest":           {{"session_id", "sessionId", "string", 1}, {"request_id", "requestId", "string", 2}, {"trace_id", "traceId", "string", 3}},
			"RevokeResponse":          {{"revoked", "revoked", "bool", 1}},
		},
		contexts: map[string][]protocolContextBinding{
			"CheckPermission": {{Source: "subject-id", RPCPath: []string{"core.v1.CheckPermissionRequest#2"}}, {Source: "tenant-id", RPCPath: []string{"core.v1.CheckPermissionRequest#1"}}},
			"Login":           {{Source: "request-id", RPCPath: []string{"core.v1.LoginRequest#4"}}, {Source: "trace-id", RPCPath: []string{"core.v1.LoginRequest#5"}}},
			"Refresh":         {{Source: "request-id", RPCPath: []string{"core.v1.RefreshRequest#2"}}, {Source: "trace-id", RPCPath: []string{"core.v1.RefreshRequest#3"}}},
			"Register":        {{Source: "request-id", RPCPath: []string{"core.v1.RegisterRequest#6"}}, {Source: "trace-id", RPCPath: []string{"core.v1.RegisterRequest#7"}}},
			"Revoke":          {{Source: "request-id", RPCPath: []string{"core.v1.RevokeRequest#2"}}, {Source: "trace-id", RPCPath: []string{"core.v1.RevokeRequest#3"}}},
		},
	},
	"account": {
		prefix: "account.v1.", service: "account.v1.AccountService",
		methods: map[string][2]string{"Get": {"account.v1.GetAccountRequest", "account.v1.GetAccountResponse"}},
		messages: map[string][]expectedProtocolField{
			"GetAccountRequest":  {{"id", "id", "string", 1}},
			"GetAccountResponse": {{"name", "name", "string", 1}},
		},
	},
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println(helperVersion)
		return
	}
	if len(os.Args) != 5 || os.Args[2] != "generate" {
		panic("invalid helper arguments")
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	switch os.Args[1] {
	case "rpc":
		if os.Args[3] != "--service" {
			panic("invalid RPC helper arguments")
		}
		generateRPC(os.Args[4], input)
	case "api":
		if os.Args[3] != "--core-service" {
			panic("invalid API helper arguments")
		}
		generateAPI(os.Args[4], input)
	default:
		panic("invalid helper mode")
	}
}

func generateRPC(service string, input []byte) {
	var document protocolDocument
	if err := decodeStrictJSON(input, &document); err != nil || validateProtocolDocument(document, service) != nil {
		panic("invalid protocol input")
	}
	var artifactPath, artifactID string
	var content []byte
	switch service {
	case "core":
		artifactPath, artifactID, content = "backend/core/rpctransport/transport.generated.go", "rpc.transport", []byte(coreRPCTransport)
	case "account":
		artifactPath, artifactID, content = "backend/account/rpctransport/transport.generated.go", "rpc.transport", []byte(accountRPCTransport)
	default:
		panic("unsupported RPC service")
	}
	write(artifactPath, content)
	runGoTest()
	encoded, err := json.Marshal(rpcResult{
		APIVersion: "nexa.dev/rpc-go-result/v1",
		Artifacts: []artifactWire{{
			Digest: digest(content), ID: artifactID, Path: artifactPath, Role: "generated",
		}},
		GoTestPassed: true,
		InputDigest:  digest(input),
		Kind:         "RPCGoResult",
		ServiceID:    service,
	})
	if err != nil {
		panic(err)
	}
	if _, err := os.Stdout.Write(encoded); err != nil {
		panic(err)
	}
}

func decodeStrictJSON(input []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("JSON input contains trailing data")
	}
	return nil
}

func validateProtocolDocument(document protocolDocument, service string) error {
	contract, ok := protocolContracts[service]
	if !ok || document.APIVersion != "nexa.dev/protocol-ir/v2" || document.Kind != "ProtocolIR" || document.ServiceID != service || !validDigest(document.SourceDigest) || len(document.Sources) == 0 || len(document.Files) == 0 {
		return fmt.Errorf("protocol document identity is invalid")
	}
	previous := ""
	for _, source := range document.Sources {
		if source.Ref == "" || !validDigest(source.Digest) || previous != "" && source.Ref <= previous {
			return fmt.Errorf("protocol source inventory is not canonical")
		}
		previous = source.Ref
	}
	foundMessages := map[string]bool{}
	foundMethods := map[string]bool{}
	serviceCount := 0
	previous = ""
	for _, file := range document.Files {
		if file.Path == "" || previous != "" && file.Path <= previous {
			return fmt.Errorf("protocol file inventory is not canonical")
		}
		previous = file.Path
		previousMessage := ""
		for _, message := range file.Messages {
			if message.FullName == "" || message.SourceRef == "" || previousMessage != "" && message.FullName <= previousMessage {
				return fmt.Errorf("protocol message inventory is not canonical")
			}
			previousMessage = message.FullName
			if !strings.HasPrefix(message.FullName, contract.prefix) {
				continue
			}
			name := strings.TrimPrefix(message.FullName, contract.prefix)
			expected, exists := contract.messages[name]
			if !exists || foundMessages[name] || len(message.Fields) != len(expected) {
				return fmt.Errorf("protocol message inventory is not exact")
			}
			foundMessages[name] = true
			for index, field := range message.Fields {
				wanted := expected[index]
				if field.FullName != message.FullName+"."+wanted.name || field.Number != wanted.number || field.JSONName != wanted.jsonName || field.Cardinality != "singular" || field.Presence != "implicit" || field.Type.Kind != "scalar" || field.Type.Name != wanted.typeName || field.Type.Key != nil || field.Type.Value != nil || field.Oneof != "" || field.SourceRef == "" {
					return fmt.Errorf("protocol field shape is invalid")
				}
			}
		}
		previousEnum := ""
		for _, enum := range file.Enums {
			if enum.FullName == "" || previousEnum != "" && enum.FullName <= previousEnum {
				return fmt.Errorf("protocol enum inventory is not canonical")
			}
			previousEnum = enum.FullName
		}
		previousService := ""
		for _, candidate := range file.Services {
			if candidate.FullName == "" || previousService != "" && candidate.FullName <= previousService {
				return fmt.Errorf("protocol service inventory is not canonical")
			}
			previousService = candidate.FullName
			if candidate.FullName != contract.service {
				return fmt.Errorf("protocol service inventory is not exact")
			}
			serviceCount++
			previousMethod := ""
			for _, method := range candidate.Methods {
				if method.FullName == "" || previousMethod != "" && method.FullName <= previousMethod {
					return fmt.Errorf("protocol method inventory is not canonical")
				}
				previousMethod = method.FullName
				name := strings.TrimPrefix(method.FullName, contract.service+".")
				expected, exists := contract.methods[name]
				if !exists || foundMethods[name] || method.Input != expected[0] || method.Output != expected[1] || method.ClientStreaming || method.ServerStreaming || len(method.HTTPProxy) == 0 || method.SourceRef == "" {
					return fmt.Errorf("protocol method contract is invalid")
				}
				var proxy map[string]any
				if err := json.Unmarshal(method.HTTPProxy, &proxy); err != nil || proxy["contextFields"] != nil {
					return fmt.Errorf("protocol HTTP proxy contract is invalid")
				}
				expectedContexts := contract.contexts[name]
				if len(expectedContexts) == 0 {
					if method.RPCContext != nil {
						return fmt.Errorf("protocol RPC context contract is invalid")
					}
				} else if method.RPCContext == nil || !reflect.DeepEqual(method.RPCContext.ContextFields, expectedContexts) {
					return fmt.Errorf("protocol RPC context contract is invalid")
				}
				foundMethods[name] = true
			}
		}
	}
	if serviceCount != 1 || len(foundMethods) != len(contract.methods) || len(foundMessages) != len(contract.messages) {
		return fmt.Errorf("protocol contract closure is not exact")
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func generateAPI(service string, input []byte) {
	var document struct {
		Operations []struct {
			ID         string `json:"id"`
			Method     string `json:"method"`
			Path       string `json:"path"`
			Permission string `json:"permission"`
			Auth       struct {
				Credentials []struct {
					ID       string `json:"id"`
					Location string `json:"in"`
					Name     string `json:"name"`
					Type     string `json:"type"`
				} `json:"credentials"`
				Mode string `json:"mode"`
			} `json:"auth"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(input, &document); err != nil || service != "core" {
		panic("invalid Core API input")
	}
	expected := map[string]operationKey{
		"account.get":              {"GET", "none", ""},
		"core.auth.login":          {"POST", "none", ""},
		"core.auth.providers":      {"GET", "none", ""},
		"core.auth.refresh":        {"POST", "none", ""},
		"core.auth.register":       {"POST", "none", ""},
		"core.auth.revoke":         {"POST", "required", "core.session.revoke"},
		"core.authorization.check": {"GET", "required", "core.authorization.check"},
		"core.health":              {"GET", "none", ""},
	}
	actual := make(map[string]operationKey, len(document.Operations))
	credentials := make(map[string][]credentialKey, len(document.Operations))
	paths := make(map[string]string, len(document.Operations))
	for _, operation := range document.Operations {
		if _, duplicate := actual[operation.ID]; duplicate {
			panic("Core API input contains duplicate operation " + operation.ID)
		}
		actual[operation.ID] = operationKey{operation.Method, operation.Auth.Mode, operation.Permission}
		for _, credential := range operation.Auth.Credentials {
			credentials[operation.ID] = append(credentials[operation.ID], credentialKey{credential.ID, credential.Type, credential.Location, credential.Name})
		}
		paths[operation.ID] = operation.Path
	}
	if len(actual) != len(expected) {
		panic("Core API input operation inventory is not exact")
	}
	for id, wanted := range expected {
		if actual[id] != wanted {
			panic("Core API input operation does not match transport contract: " + id)
		}
		values := credentials[id]
		if wanted.mode == "required" {
			if len(values) != 1 || values[0] != (credentialKey{"primary", "bearer", "header", "authorization"}) {
				panic("Core API input credential does not match transport contract: " + id)
			}
		} else if len(values) != 0 {
			panic("Core API input unexpectedly declares credentials: " + id)
		}
	}
	if !strings.Contains(paths["account.get"], "{id}") || !strings.Contains(paths["core.authorization.check"], "{permission}") {
		panic("Core API input route variables do not match transport contract")
	}
	if err := validateRoutePatterns(actual, paths); err != nil {
		panic(err)
	}
	const artifactPath = "backend/core/apitransport/transport.generated.go"
	content := []byte(coreAPITransport)
	for id, operation := range actual {
		content = []byte(strings.ReplaceAll(string(content), "{{route:"+id+"}}", strconv.Quote(operation.method+" "+paths[id])))
	}
	if strings.Contains(string(content), "{{route:") {
		panic("Core API transport route substitution is incomplete")
	}
	write(artifactPath, content)
	prepareFrameworkDependency()
	runGoTest()
	artifacts := collectAPIArtifacts(service)
	encoded, err := json.Marshal(apiResult{
		APIVersion:    "nexa.dev/api-go-result/v1",
		Artifacts:     artifacts,
		CoreServiceID: service,
		GoTestPassed:  true,
		InputDigest:   digest(input),
		Kind:          "APIGoResult",
	})
	if err != nil {
		panic(err)
	}
	if _, err := os.Stdout.Write(encoded); err != nil {
		panic(err)
	}
}

func prepareFrameworkDependency() {
	root := os.Getenv("NEXA_FRAMEWORK_ROOT")
	if root == "" {
		return
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || !filepath.IsAbs(root) || canonical != root {
		panic("framework module root is invalid")
	}
	commands := [][]string{
		{"mod", "edit", "-require=" + frameworkModulePath + "@v0.0.0", "-replace=" + frameworkModulePath + "=" + canonical},
		{"mod", "tidy"},
	}
	for _, arguments := range commands {
		command := exec.Command("go", arguments...)
		if output, err := command.CombinedOutput(); err != nil {
			panic(fmt.Sprintf("stage framework dependency: %v: %s", err, output))
		}
	}
}

func collectAPIArtifacts(core string) []artifactWire {
	artifacts := make([]artifactWire, 0)
	seenIDs := map[string]bool{}
	err := filepath.WalkDir(".", func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		extension := filepath.Ext(name)
		if extension != ".go" && extension != ".api" {
			return nil
		}
		relative, err := filepath.Rel(".", name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		id := apiArtifactID(relative, core)
		if seenIDs[id] {
			return fmt.Errorf("duplicate staged API artifact id: %s", id)
		}
		seenIDs[id] = true
		artifacts = append(artifacts, artifactWire{Digest: digest(content), ID: id, Path: relative, Role: "generated"})
		return nil
	})
	if err != nil {
		panic(err)
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].ID < artifacts[j].ID })
	return artifacts
}

func apiArtifactID(name, core string) string {
	switch {
	case name == "backend/"+core+"/desc/generated/"+core+".generated.api":
		return "api.aggregate." + core
	case name == "backend/"+core+"/desc/generated/"+core+".proxy.generated.api":
		return "api." + core
	case strings.Contains(name, "/desc/generated/") && strings.HasSuffix(name, ".generated.api"):
		return "api." + strings.TrimSuffix(filepath.Base(name), ".generated.api")
	case strings.HasSuffix(name, "/client.generated.go"):
		return "client." + filepath.Base(filepath.Dir(name))
	case strings.HasSuffix(name, "/errors.generated.go"):
		return "errors." + filepath.Base(filepath.Dir(name))
	case strings.HasSuffix(name, "/mapper.generated.go"):
		return "mapper." + filepath.Base(filepath.Dir(name))
	case strings.Contains(name, "/internal/logic/rpcproxy/"):
		return "logic." + strings.ReplaceAll(strings.TrimSuffix(filepath.Base(name), ".generated.go"), "-", ".")
	case strings.HasSuffix(name, "/internal/rpcproxy/generated/register.generated.go"):
		return "register"
	case name == "backend/"+core+"/apitransport/transport.generated.go":
		return "api.transport"
	default:
		panic("unrecognized staged API artifact: " + name)
	}
}

func validateRoutePatterns(operations map[string]operationKey, paths map[string]string) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("Core API route pattern is invalid: %v", recovered)
		}
	}()
	ids := make([]string, 0, len(operations))
	for id := range operations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	mux := http.NewServeMux()
	for _, id := range ids {
		path := paths[id]
		if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\r\n\x00") {
			return fmt.Errorf("Core API route path is invalid: %s", id)
		}
		mux.HandleFunc(operations[id].method+" "+path, func(http.ResponseWriter, *http.Request) {})
	}
	return nil
}

func write(name string, content []byte) {
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		panic(err)
	}
	if err := os.WriteFile(name, content, 0o600); err != nil {
		panic(err)
	}
}

func runGoTest() {
	command := exec.Command("go", "test", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("staged go test failed: %v: %s", err, output))
	}
}

func digest(content []byte) string {
	value := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(value[:])
}

const coreRPCTransport = `// Code generated by nexa-core-generation-helper. DO NOT EDIT.
package rpctransport

import (
	"context"
	"net"
	"net/rpc"
)

type HealthRequest struct{}
type HealthResponse struct { Ready bool }
type RegisterRequest struct { Tenant string; Username string; Password string; Email string; DisplayName string; RequestID string; TraceID string }
type RegisterResponse struct { AccountID string }
type LoginRequest struct { Tenant string; Username string; Password string; RequestID string; TraceID string }
type LoginResponse struct { SessionID string; AccessToken string; RefreshToken string }
type RefreshRequest struct { RefreshToken string; RequestID string; TraceID string }
type RefreshResponse struct { SessionID string; AccessToken string; RefreshToken string }
type RevokeRequest struct { SessionID string; RequestID string; TraceID string }
type RevokeResponse struct { Revoked bool }
type CheckPermissionRequest struct { TenantID int64; SubjectID string; Permission string }
type CheckPermissionResponse struct { Allowed bool }

type Service interface {
	Health(context.Context, HealthRequest) (HealthResponse, error)
	Register(context.Context, RegisterRequest) (RegisterResponse, error)
	Login(context.Context, LoginRequest) (LoginResponse, error)
	Refresh(context.Context, RefreshRequest) (RefreshResponse, error)
	Revoke(context.Context, RevokeRequest) (RevokeResponse, error)
	CheckPermission(context.Context, CheckPermissionRequest) (CheckPermissionResponse, error)
}

type serverAdapter struct { service Service }

func (adapter *serverAdapter) Health(request HealthRequest, response *HealthResponse) error {
	value, err := adapter.service.Health(context.Background(), request)
	if err != nil { return err }
	*response = value
	return nil
}

func (adapter *serverAdapter) Register(request RegisterRequest, response *RegisterResponse) error {
	value, err := adapter.service.Register(context.Background(), request)
	if err != nil { return err }
	*response = value
	return nil
}
func (adapter *serverAdapter) Login(request LoginRequest, response *LoginResponse) error {
	value, err := adapter.service.Login(context.Background(), request)
	if err != nil { return err }
	*response = value
	return nil
}
func (adapter *serverAdapter) Refresh(request RefreshRequest, response *RefreshResponse) error {
	value, err := adapter.service.Refresh(context.Background(), request)
	if err != nil { return err }
	*response = value
	return nil
}
func (adapter *serverAdapter) Revoke(request RevokeRequest, response *RevokeResponse) error {
	value, err := adapter.service.Revoke(context.Background(), request)
	if err != nil { return err }
	*response = value
	return nil
}
func (adapter *serverAdapter) CheckPermission(request CheckPermissionRequest, response *CheckPermissionResponse) error {
	value, err := adapter.service.CheckPermission(context.Background(), request)
	if err != nil { return err }
	*response = value
	return nil
}

type Server struct { server *rpc.Server }

func NewServer(service Service) *Server {
	server := rpc.NewServer()
	if err := server.RegisterName("Core", &serverAdapter{service: service}); err != nil { panic(err) }
	return &Server{server: server}
}

func (server *Server) Serve(listener net.Listener) error {
	for {
		connection, err := listener.Accept()
		if err != nil { return err }
		go server.server.ServeConn(connection)
	}
}

type Client struct { client *rpc.Client }

func Dial(address string) (*Client, error) {
	client, err := rpc.Dial("tcp", address)
	if err != nil { return nil, err }
	return &Client{client: client}, nil
}

func (client *Client) Close() error { return client.client.Close() }

func (client *Client) Health(ctx context.Context, request HealthRequest) (HealthResponse, error) {
	var response HealthResponse
	call := client.client.Go("Core.Health", request, &response, nil)
	select {
	case <-ctx.Done(): return HealthResponse{}, ctx.Err()
	case completed := <-call.Done: return response, completed.Error
	}
}
func (client *Client) Register(ctx context.Context, request RegisterRequest) (RegisterResponse, error) {
	var response RegisterResponse
	call := client.client.Go("Core.Register", request, &response, nil)
	select {
	case <-ctx.Done(): return RegisterResponse{}, ctx.Err()
	case completed := <-call.Done: return response, completed.Error
	}
}
func (client *Client) Login(ctx context.Context, request LoginRequest) (LoginResponse, error) {
	var response LoginResponse
	call := client.client.Go("Core.Login", request, &response, nil)
	select {
	case <-ctx.Done(): return LoginResponse{}, ctx.Err()
	case completed := <-call.Done: return response, completed.Error
	}
}
func (client *Client) Refresh(ctx context.Context, request RefreshRequest) (RefreshResponse, error) {
	var response RefreshResponse
	call := client.client.Go("Core.Refresh", request, &response, nil)
	select {
	case <-ctx.Done(): return RefreshResponse{}, ctx.Err()
	case completed := <-call.Done: return response, completed.Error
	}
}
func (client *Client) Revoke(ctx context.Context, request RevokeRequest) (RevokeResponse, error) {
	var response RevokeResponse
	call := client.client.Go("Core.Revoke", request, &response, nil)
	select {
	case <-ctx.Done(): return RevokeResponse{}, ctx.Err()
	case completed := <-call.Done: return response, completed.Error
	}
}
func (client *Client) CheckPermission(ctx context.Context, request CheckPermissionRequest) (CheckPermissionResponse, error) {
	var response CheckPermissionResponse
	call := client.client.Go("Core.CheckPermission", request, &response, nil)
	select {
	case <-ctx.Done(): return CheckPermissionResponse{}, ctx.Err()
	case completed := <-call.Done: return response, completed.Error
	}
}
`

const accountRPCTransport = `// Code generated by nexa-core-generation-helper. DO NOT EDIT.
package rpctransport

import (
	"context"
	"net"
	"net/rpc"
)

type GetRequest struct { ID string }
type GetResponse struct { Name string }
type Service interface { Get(context.Context, GetRequest) (GetResponse, error) }
type serverAdapter struct { service Service }
func (adapter *serverAdapter) Get(request GetRequest, response *GetResponse) error {
	value, err := adapter.service.Get(context.Background(), request)
	if err != nil { return err }
	*response = value
	return nil
}
type Server struct { server *rpc.Server }
func NewServer(service Service) *Server {
	server := rpc.NewServer()
	if err := server.RegisterName("Account", &serverAdapter{service: service}); err != nil { panic(err) }
	return &Server{server: server}
}
func (server *Server) Serve(listener net.Listener) error {
	for {
		connection, err := listener.Accept()
		if err != nil { return err }
		go server.server.ServeConn(connection)
	}
}
type Client struct { client *rpc.Client }
func Dial(address string) (*Client, error) {
	client, err := rpc.Dial("tcp", address)
	if err != nil { return nil, err }
	return &Client{client: client}, nil
}
func (client *Client) Close() error { return client.client.Close() }
func (client *Client) Get(ctx context.Context, request GetRequest) (GetResponse, error) {
	var response GetResponse
	call := client.client.Go("Account.Get", request, &response, nil)
	select {
	case <-ctx.Done(): return GetResponse{}, ctx.Err()
	case completed := <-call.Done: return response, completed.Error
	}
}
`

const coreAPITransport = `// Code generated by nexa-core-generation-helper. DO NOT EDIT.
package apitransport

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type HealthRequest struct{}
type HealthResponse struct { Ready bool }
type Identity struct { TenantID int64; SubjectID string }
type RegisterRequest struct { Tenant string; Username string; Password string; Email string; DisplayName string }
type RegisterResponse struct { AccountID string }
type LoginRequest struct { Tenant string; Username string; Password string }
type LoginResponse struct { SessionID string; AccessToken string; RefreshToken string }
type RefreshRequest struct { RefreshToken string }
type RefreshResponse struct { SessionID string; AccessToken string; RefreshToken string }
type RevokeRequest struct { Identity Identity; SessionID string }
type RevokeResponse struct { Revoked bool }
type CheckPermissionRequest struct { Identity Identity; Permission string }
type CheckPermissionResponse struct { Allowed bool }
type ProviderCapabilities struct { Authenticate bool; AutoProvision bool; GroupClaims bool }
type ProviderDescriptor struct { ID string; Protocol string; Capabilities ProviderCapabilities }
type ListProvidersRequest struct{}
type ListProvidersResponse struct { Items []ProviderDescriptor }
type GetAccountRequest struct { ID string }
type GetAccountResponse struct { Name string }

type Backend interface {
	Health(context.Context, HealthRequest) (HealthResponse, error)
	Register(context.Context, RegisterRequest) (RegisterResponse, error)
	Login(context.Context, LoginRequest) (LoginResponse, error)
	Refresh(context.Context, RefreshRequest) (RefreshResponse, error)
	Revoke(context.Context, RevokeRequest) (RevokeResponse, error)
	CheckPermission(context.Context, CheckPermissionRequest) (CheckPermissionResponse, error)
	Providers(context.Context, ListProvidersRequest) (ListProvidersResponse, error)
	GetAccount(context.Context, GetAccountRequest) (GetAccountResponse, error)
}

type Security interface {
	Authenticate(context.Context, string) (Identity, error)
	Authorize(context.Context, Identity, string) error
}

func NewHandler(backend Backend, security Security) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc({{route:core.health}}, func(writer http.ResponseWriter, request *http.Request) {
		response, err := backend.Health(request.Context(), HealthRequest{})
		if err != nil { writeBackendError(writer, err); return }
		writeJSON(writer, http.StatusOK, map[string]bool{"ready": response.Ready})
	})
	mux.HandleFunc({{route:core.auth.register}}, func(writer http.ResponseWriter, request *http.Request) {
		var input RegisterRequest
		if !decodeJSON(writer, request, &input) { return }
		response, err := backend.Register(request.Context(), input)
		if err != nil { writeBackendError(writer, err); return }
		writeJSON(writer, http.StatusCreated, map[string]string{"accountId": response.AccountID})
	})
	mux.HandleFunc({{route:core.auth.login}}, func(writer http.ResponseWriter, request *http.Request) {
		var input LoginRequest
		if !decodeJSON(writer, request, &input) { return }
		response, err := backend.Login(request.Context(), input)
		if err != nil { writeBackendError(writer, err); return }
		writeJSON(writer, http.StatusOK, session(response.SessionID, response.AccessToken, response.RefreshToken))
	})
	mux.HandleFunc({{route:core.auth.refresh}}, func(writer http.ResponseWriter, request *http.Request) {
		var input RefreshRequest
		if !decodeJSON(writer, request, &input) { return }
		response, err := backend.Refresh(request.Context(), input)
		if err != nil { writeBackendError(writer, err); return }
		writeJSON(writer, http.StatusOK, session(response.SessionID, response.AccessToken, response.RefreshToken))
	})
	mux.HandleFunc({{route:core.auth.revoke}}, func(writer http.ResponseWriter, request *http.Request) {
		identity, ok := authorize(writer, request, security, "core.session.revoke")
		if !ok { return }
		var input struct { SessionID string }
		if !decodeJSON(writer, request, &input) { return }
		response, err := backend.Revoke(request.Context(), RevokeRequest{Identity: identity, SessionID: input.SessionID})
		if err != nil { writeBackendError(writer, err); return }
		writeJSON(writer, http.StatusOK, map[string]bool{"revoked": response.Revoked})
	})
	mux.HandleFunc({{route:core.authorization.check}}, func(writer http.ResponseWriter, request *http.Request) {
		identity, ok := authorize(writer, request, security, "core.authorization.check")
		if !ok { return }
		response, err := backend.CheckPermission(request.Context(), CheckPermissionRequest{Identity: identity, Permission: request.PathValue("permission")})
		if err != nil { writeBackendError(writer, err); return }
		writeJSON(writer, http.StatusOK, map[string]bool{"allowed": response.Allowed})
	})
	mux.HandleFunc({{route:core.auth.providers}}, func(writer http.ResponseWriter, request *http.Request) {
		response, err := backend.Providers(request.Context(), ListProvidersRequest{})
		if err != nil { writeBackendError(writer, err); return }
		items := make([]map[string]any, len(response.Items))
		for index, item := range response.Items {
			items[index] = map[string]any{"id": item.ID, "protocol": item.Protocol, "capabilities": map[string]bool{"authenticate": item.Capabilities.Authenticate, "autoProvision": item.Capabilities.AutoProvision, "groupClaims": item.Capabilities.GroupClaims}}
		}
		writeJSON(writer, http.StatusOK, map[string]any{"items": items})
	})
	mux.HandleFunc({{route:account.get}}, func(writer http.ResponseWriter, request *http.Request) {
		response, err := backend.GetAccount(request.Context(), GetAccountRequest{ID: request.PathValue("id")})
		if err != nil { writeBackendError(writer, err); return }
		writeJSON(writer, http.StatusOK, map[string]string{"name": response.Name})
	})
	return mux
}

func authorize(writer http.ResponseWriter, request *http.Request, security Security, permission string) (Identity, bool) {
	token, ok := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" { writeError(writer, http.StatusUnauthorized); return Identity{}, false }
	identity, err := security.Authenticate(request.Context(), token)
	if err != nil { writeError(writer, http.StatusUnauthorized); return Identity{}, false }
	if err := security.Authorize(request.Context(), identity, permission); err != nil { writeError(writer, http.StatusForbidden); return Identity{}, false }
	return identity, true
}
func decodeJSON(writer http.ResponseWriter, request *http.Request, value any) bool {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil { writeError(writer, http.StatusBadRequest); return false }
	return true
}
func session(id, access, refresh string) map[string]string { return map[string]string{"sessionId": id, "accessToken": access, "refreshToken": refresh} }
func writeBackendError(writer http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	message := err.Error()
	if strings.Contains(message, "invalid_credentials") || strings.Contains(message, "session_expired") || strings.Contains(message, "session_replayed") { status = http.StatusUnauthorized }
	if strings.Contains(message, "conflict") { status = http.StatusConflict }
	writeJSON(writer, status, map[string]string{"error": message})
}
func writeError(writer http.ResponseWriter, status int) { writeJSON(writer, status, map[string]string{"error": http.StatusText(status)}) }
func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
`

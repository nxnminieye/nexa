package consumer_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	coreapi "example.com/core-iam-consumer/backend/core/api"
	"example.com/core-iam-consumer/backend/core/rpc/coreapp"
	coretransport "example.com/core-iam-consumer/generated"
	transportpb "example.com/core-iam-consumer/generated/transportpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	transportModeEnv        = "NEXA_CORE_TRANSPORT_HELPER"
	transportAddressFileEnv = "NEXA_CORE_TRANSPORT_ADDRESS_FILE"
	transportRPCAddressEnv  = "NEXA_CORE_TRANSPORT_RPC_ADDRESS"
	transportTenantEnv      = "NEXA_CORE_TRANSPORT_TENANT"
)

type tenantMemberWire struct {
	MemberID        string   `json:"memberId"`
	AccountID       string   `json:"accountId"`
	Username        string   `json:"username"`
	Email           string   `json:"email"`
	DisplayName     string   `json:"displayName"`
	SourceCode      string   `json:"sourceCode"`
	ExternalSubject string   `json:"externalSubject"`
	Status          string   `json:"status"`
	RoleCodes       []string `json:"roleCodes"`
	Version         uint64   `json:"version"`
}

type tenantMemberListWire struct {
	Items []tenantMemberWire `json:"items"`
	Total uint64             `json:"total"`
}

type coreHTTPEnvelope struct {
	Code    int             `json:"code"`
	Msg     string          `json:"msg"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// TestCoreTransportHelper is executed only in child processes created by the
// PostgreSQL integration test. The API process receives no database settings.
func TestCoreTransportHelper(t *testing.T) {
	mode := os.Getenv(transportModeEnv)
	if mode == "" {
		t.Skip("transport helper process only")
	}
	addressFile := os.Getenv(transportAddressFileEnv)
	if addressFile == "" {
		t.Fatal("transport address file is required")
	}
	switch mode {
	case "rpc":
		runCoreRPCProcess(t, addressFile)
	case "api":
		runCoreAPIProcess(t, addressFile)
	default:
		t.Fatalf("unknown transport helper mode %q", mode)
	}
}

func runCoreRPCProcess(t *testing.T, addressFile string) {
	database, err := sql.Open("pgx", os.Getenv("NEXA_CORE_IAM_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock := coreapp.ClockFunc(func() time.Time { return time.Now().UTC() })
	serviceContext, err := coreapp.NewServiceContext(database, reconcileFunc(func(context.Context, coreapp.PolicyReconcileInput) error { return nil }), coreapp.ServiceContextOptions{
		DefaultTenant: os.Getenv(transportTenantEnv),
		DefaultRouter: "/home",
		Password:      coreapp.Argon2idOptions{MemoryKiB: 8192, Iterations: 1, Parallelism: 1, SaltBytes: 16, KeyBytes: 16},
		Sessions:      coreapp.SessionOptions{AccessTTL: time.Minute, RefreshTTL: time.Hour, TokenBytes: 16, Clock: clock},
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	transportpb.RegisterCoreServiceServer(server, coretransport.NewRPCServer(serviceContext.RPC))
	writeTransportAddress(t, addressFile, listener.Addr().String())
	if err := server.Serve(listener); err != nil {
		t.Fatal(err)
	}
}

func runCoreAPIProcess(t *testing.T, addressFile string) {
	rpcAddress := os.Getenv(transportRPCAddressEnv)
	if rpcAddress == "" {
		t.Fatal("Core RPC address is required")
	}
	connection, err := grpc.NewClient(rpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	handler, err := coreapi.NewHandler(coretransport.NewAPIClient(transportpb.NewCoreServiceClient(connection)), coreapi.Config{
		ListenAddress: "127.0.0.1:0", RPCAddress: rpcAddress, DefaultTenant: os.Getenv(transportTenantEnv),
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	writeTransportAddress(t, addressFile, listener.Addr().String())
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
}

func writeTransportAddress(t *testing.T, path, address string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(address), 0o600); err != nil {
		t.Fatal(err)
	}
}

type transportProcess struct {
	command *exec.Cmd
	done    chan error
	address string
	output  *os.File
}

func startTransportProcess(t *testing.T, mode, dsn, rpcAddress string) *transportProcess {
	t.Helper()
	directory := t.TempDir()
	addressFile := filepath.Join(directory, "address")
	output, err := os.Create(filepath.Join(directory, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestCoreTransportHelper$", "-test.v")
	command.Env = append(os.Environ(), transportModeEnv+"="+mode, transportAddressFileEnv+"="+addressFile, transportRPCAddressEnv+"="+rpcAddress, transportTenantEnv+"=tenant-a", "NEXA_CORE_IAM_TEST_DSN="+dsn)
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		output.Close()
		t.Fatal(err)
	}
	process := &transportProcess{command: command, done: make(chan error, 1), output: output}
	go func() { process.done <- command.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(addressFile)
		if readErr == nil && strings.TrimSpace(string(data)) != "" {
			process.address = strings.TrimSpace(string(data))
			t.Cleanup(process.stop)
			return process
		}
		select {
		case <-process.done:
			output.Close()
			t.Fatalf("%s transport process exited before readiness", mode)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	process.stop()
	t.Fatalf("%s transport process did not become ready", mode)
	return nil
}

func (p *transportProcess) stop() {
	if p == nil || p.command == nil || p.command.Process == nil {
		return
	}
	_ = p.command.Process.Signal(os.Interrupt)
	select {
	case <-p.done:
	case <-time.After(time.Second):
		_ = p.command.Process.Kill()
		<-p.done
	}
	_ = p.output.Close()
	p.command = nil
}

func exerciseCoreRPCAPITransport(t *testing.T, dsn string, accessToken, deniedAccessToken, tenantID, otherTenantID string, memberID coreapp.TenantMemberID, accountID coreapp.IdentityAccountID) {
	t.Helper()
	rpcProcess := startTransportProcess(t, "rpc", dsn, "")
	apiProcess := startTransportProcess(t, "api", "", rpcProcess.address)
	client := &http.Client{Timeout: 2 * time.Second}
	request := func(path, tenant, token string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, "http://"+apiProcess.address+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if tenant != "" {
			req.Header.Set("X-Tenant-ID", tenant)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := request("/api/users?keyword=Bootstrap&status=enabled&limit=10&offset=0", tenantID, accessToken)
	var envelope coreHTTPEnvelope
	decodeCoreHTTPResponse(t, response, http.StatusOK, &envelope)
	if envelope.Code != 0 || envelope.Msg != "ok" {
		t.Fatalf("member list envelope=%#v", envelope)
	}
	var page tenantMemberListWire
	if err := json.Unmarshal(envelope.Data, &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].MemberID != string(memberID) || page.Items[0].AccountID != string(accountID) || page.Items[0].Username != "owner" || page.Items[0].DisplayName != "Bootstrap Owner" || len(page.Items[0].RoleCodes) != 1 || page.Items[0].RoleCodes[0] != "operator" {
		t.Fatalf("member API page=%#v", page)
	}
	response = request("/api/user/info", tenantID, accessToken)
	decodeCoreHTTPResponse(t, response, http.StatusOK, &envelope)
	if envelope.Code != 0 || envelope.Msg != "ok" {
		t.Fatalf("user info envelope=%#v", envelope)
	}
	var userInfo struct {
		UserID   string   `json:"userId"`
		MemberID int64    `json:"memberId"`
		Username string   `json:"username"`
		Email    string   `json:"email"`
		RealName string   `json:"realName"`
		Roles    []string `json:"roles"`
	}
	if err := json.Unmarshal(envelope.Data, &userInfo); err != nil {
		t.Fatal(err)
	}
	memberNumeric, err := strconv.ParseInt(string(memberID), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if userInfo.UserID != string(accountID) || userInfo.MemberID != memberNumeric || userInfo.Username != "owner" || userInfo.Email != "owner@example.test" || userInfo.RealName != "Bootstrap Owner" || len(userInfo.Roles) != 2 || userInfo.Roles[0] != "operator" || userInfo.Roles[1] != "tenant-owner" {
		t.Fatalf("user info=%#v", userInfo)
	}
	response = request("/api/users", "", accessToken)
	decodeCoreHTTPResponse(t, response, http.StatusOK, &envelope)
	response = request("/api/users/"+url.PathEscape(string(memberID)), tenantID, accessToken)
	decodeCoreHTTPResponse(t, response, http.StatusOK, &envelope)
	var itemData struct {
		Item tenantMemberWire `json:"item"`
	}
	if err := json.Unmarshal(envelope.Data, &itemData); err != nil || itemData.Item.MemberID != string(memberID) || itemData.Item.AccountID != string(accountID) {
		t.Fatalf("member API get=%#v err=%v", itemData, err)
	}
	for _, test := range []struct {
		name       string
		path       string
		tenant     string
		token      string
		wantStatus int
	}{
		{name: "invalid-status", path: "/api/users?status=unknown", tenant: tenantID, token: accessToken, wantStatus: http.StatusBadRequest},
		{name: "limit-overflow", path: "/api/users?limit=201", tenant: tenantID, token: accessToken, wantStatus: http.StatusBadRequest},
		{name: "cross-tenant", path: "/api/users", tenant: otherTenantID, token: accessToken, wantStatus: http.StatusForbidden},
		{name: "missing-permission", path: "/api/users", tenant: otherTenantID, token: deniedAccessToken, wantStatus: http.StatusForbidden},
		{name: "invalid-token", path: "/api/users", tenant: tenantID, token: "wrong", wantStatus: http.StatusUnauthorized},
	} {
		t.Run("transport-"+test.name, func(t *testing.T) {
			response := request(test.path, test.tenant, test.token)
			var failure coreHTTPEnvelope
			decodeCoreHTTPResponse(t, response, test.wantStatus, &failure)
			if failure.Code != test.wantStatus || failure.Msg == "" || failure.Message == "" {
				t.Fatalf("failure envelope=%#v", failure)
			}
		})
	}
	rpcProcess.stop()
	response = request("/api/health", "", "")
	var unavailable coreHTTPEnvelope
	decodeCoreHTTPResponse(t, response, http.StatusServiceUnavailable, &unavailable)
	if unavailable.Code != http.StatusServiceUnavailable || unavailable.Msg != "service_unavailable" {
		t.Fatalf("RPC unavailable envelope=%#v", unavailable)
	}
}

func decodeCoreHTTPResponse(t *testing.T, response *http.Response, wantStatus int, target any) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != wantStatus || response.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("HTTP status=%d content-type=%q want=%d", response.StatusCode, response.Header.Get("Content-Type"), wantStatus)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

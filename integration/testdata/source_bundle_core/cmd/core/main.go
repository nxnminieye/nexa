package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	accounttransport "example.com/nexa-generation-consumer/backend/account/rpctransport"
	"example.com/nexa-generation-consumer/backend/core/apitransport"
	"example.com/nexa-generation-consumer/backend/core/coreapp"
	coretransport "example.com/nexa-generation-consumer/backend/core/rpctransport"
)

type identityProvider struct{}

func (identityProvider) Descriptor() coreapp.ProviderDescriptor {
	return coreapp.ProviderDescriptor{ID: "fixture", Protocol: "fixture", Capabilities: coreapp.ProviderCapabilities{Authenticate: true, AutoProvision: true, GroupClaims: true}}
}

func (identityProvider) Authorize(context.Context, coreapp.AuthorizeInput) (coreapp.AuthorizeResult, error) {
	return coreapp.AuthorizeResult{URL: "https://identity.example.test/authorize"}, nil
}

func (identityProvider) Exchange(context.Context, coreapp.ExchangeInput) (coreapp.NormalizedIdentity, error) {
	return coreapp.NormalizedIdentity{SourceCode: "fixture", ExternalSubject: "subject-1", Username: "alice", ExternalGroups: []string{"operators"}}, nil
}

type fixtureUnmatchedIdentityPolicy struct{ store *memoryStore }

func (policy fixtureUnmatchedIdentityPolicy) ResolveUnmatchedIdentity(ctx context.Context, input coreapp.UnmatchedIdentityInput) error {
	_, err := policy.store.bindExternalIdentity(ctx, input.Identity)
	if errors.Is(err, coreapp.ErrStoreConflict) {
		return nil
	}
	return err
}

type fixtureTenantAdmissionPolicy struct{ store *memoryStore }

func (policy fixtureTenantAdmissionPolicy) AdmitTenant(ctx context.Context, input coreapp.TenantAdmissionInput) (coreapp.TenantMember, error) {
	return policy.store.admitTenant(ctx, input.Tenant, input.Account.ID)
}

type fixtureExternalRoleMapper struct{}

func (fixtureExternalRoleMapper) MapExternalRoles(_ context.Context, input coreapp.ExternalRoleMappingInput) ([]string, error) {
	if input.Identity.SourceCode != "fixture" {
		return nil, nil
	}
	for _, group := range input.Identity.ExternalGroups {
		if group == "operators" {
			return []string{"role.operator"}, nil
		}
	}
	return nil, nil
}

type coreRuntime struct {
	store      *memoryStore
	auth       *coreapp.LocalAuthenticator
	authorizer *coreapp.Authorizer
	providers  coreapp.ProviderSet
	external   *coreapp.ExternalLoginService
}

func newCoreRuntime(withProvider bool) (*coreRuntime, error) {
	if health, err := coreapp.CheckHealth(context.Background()); err != nil || !health.Ready {
		return nil, fmt.Errorf("core health: %v", err)
	}
	store := newMemoryStore()
	hasher, err := coreapp.NewArgon2idHasher(coreapp.Argon2idOptions{MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltBytes: 16, KeyBytes: 32})
	if err != nil {
		return nil, err
	}
	sessionOptions := coreapp.SessionOptions{
		AccessTTL: 5 * time.Minute, RefreshTTL: time.Hour, TokenBytes: 32, Clock: coreapp.ClockFunc(time.Now),
	}
	auth, err := coreapp.NewLocalAuthenticator(store, hasher, sessionOptions)
	if err != nil {
		return nil, err
	}
	authorizer, err := coreapp.NewAuthorizer([]coreapp.RoleGrant{{
		RoleRef: "role.local", Permissions: []string{"core.authorization.check", "core.session.revoke"},
	}})
	if err != nil {
		return nil, err
	}
	var configured []coreapp.IdentityProvider
	if withProvider {
		configured = append(configured, identityProvider{})
	}
	providers, err := coreapp.NewProviderSet(configured...)
	if err != nil {
		return nil, err
	}
	issuer, err := coreapp.NewDefaultSessionIssuer(store, sessionOptions)
	if err != nil {
		return nil, err
	}
	external, err := coreapp.NewExternalLoginService(coreapp.ExternalLoginOptions{
		Providers: providers, Accounts: store,
		Unmatched: fixtureUnmatchedIdentityPolicy{store: store}, Admission: fixtureTenantAdmissionPolicy{store: store},
		RoleMapper: fixtureExternalRoleMapper{}, Grants: store, Sessions: issuer,
	})
	if err != nil {
		return nil, err
	}
	runtime := &coreRuntime{store: store, auth: auth, authorizer: authorizer, providers: providers, external: external}
	if withProvider {
		if err := runtime.verifyProviderComposition(context.Background()); err != nil {
			return nil, err
		}
	}
	return runtime, nil
}

func (runtime *coreRuntime) verifyProviderComposition(ctx context.Context) error {
	loggedIn, err := runtime.external.Login(ctx, "fixture", coreapp.ExchangeInput{Tenant: "tenant-a", Code: "fixture-code"})
	if err != nil {
		return err
	}
	roles := runtime.store.roles(loggedIn.Member.ID)
	if loggedIn.Account.Username != "alice" || loggedIn.Session.ID == "" || len(roles) != 1 || roles[0] != "role.operator" {
		return fmt.Errorf("provider login = %#v roles=%#v", loggedIn, roles)
	}
	return nil
}

func (runtime *coreRuntime) Health(ctx context.Context, _ coretransport.HealthRequest) (coretransport.HealthResponse, error) {
	health, err := coreapp.CheckHealth(ctx)
	return coretransport.HealthResponse{Ready: health.Ready}, err
}

func (runtime *coreRuntime) Register(ctx context.Context, request coretransport.RegisterRequest) (coretransport.RegisterResponse, error) {
	account, err := runtime.auth.Register(ctx, coreapp.LocalRegistration{
		Tenant: request.Tenant, Username: request.Username, Password: []byte(request.Password), Email: request.Email, DisplayName: request.DisplayName,
	})
	if err != nil {
		return coretransport.RegisterResponse{}, err
	}
	member, err := runtime.store.admitTenant(ctx, request.Tenant, account.ID)
	if err != nil {
		return coretransport.RegisterResponse{}, err
	}
	if err := runtime.store.replaceLocalRoles(ctx, member.ID, []string{"role.local"}); err != nil {
		return coretransport.RegisterResponse{}, err
	}
	return coretransport.RegisterResponse{AccountID: string(account.ID)}, nil
}

func (runtime *coreRuntime) Login(ctx context.Context, request coretransport.LoginRequest) (coretransport.LoginResponse, error) {
	session, err := runtime.auth.Login(ctx, coreapp.LocalLogin{Tenant: request.Tenant, Username: request.Username, Password: []byte(request.Password)})
	if err != nil {
		return coretransport.LoginResponse{}, err
	}
	return coretransport.LoginResponse{SessionID: string(session.ID), AccessToken: session.AccessToken, RefreshToken: string(session.RefreshToken)}, nil
}

func (runtime *coreRuntime) Refresh(ctx context.Context, request coretransport.RefreshRequest) (coretransport.RefreshResponse, error) {
	session, err := runtime.auth.Refresh(ctx, coreapp.RefreshToken(request.RefreshToken))
	if err != nil {
		return coretransport.RefreshResponse{}, err
	}
	return coretransport.RefreshResponse{SessionID: string(session.ID), AccessToken: session.AccessToken, RefreshToken: string(session.RefreshToken)}, nil
}

func (runtime *coreRuntime) Revoke(ctx context.Context, request coretransport.RevokeRequest) (coretransport.RevokeResponse, error) {
	if err := runtime.auth.Revoke(ctx, coreapp.SessionID(request.SessionID)); err != nil {
		return coretransport.RevokeResponse{}, err
	}
	return coretransport.RevokeResponse{Revoked: true}, nil
}

func (runtime *coreRuntime) CheckPermission(ctx context.Context, request coretransport.CheckPermissionRequest) (coretransport.CheckPermissionResponse, error) {
	roles, exists := runtime.store.rolesForMember(request.TenantID, coreapp.TenantMemberID(request.SubjectID))
	if !exists {
		return coretransport.CheckPermissionResponse{}, errors.New("invalid_credentials")
	}
	allowed, err := runtime.authorizer.Allowed(ctx, roles, request.Permission)
	return coretransport.CheckPermissionResponse{Allowed: allowed}, err
}

func (runtime *coreRuntime) Authenticate(ctx context.Context, token string) (apitransport.Identity, error) {
	session, member, err := runtime.store.authenticateAccess(ctx, token)
	if err != nil {
		return apitransport.Identity{}, err
	}
	return apitransport.Identity{TenantID: runtime.store.tenantID(session.Tenant), SubjectID: string(member.ID)}, nil
}

func (runtime *coreRuntime) Authorize(ctx context.Context, identity apitransport.Identity, permission string) error {
	roles, exists := runtime.store.rolesForMember(identity.TenantID, coreapp.TenantMemberID(identity.SubjectID))
	if !exists {
		return errors.New("invalid_credentials")
	}
	allowed, err := runtime.authorizer.Allowed(ctx, roles, permission)
	if err != nil {
		return err
	}
	if !allowed {
		return errors.New("forbidden")
	}
	return nil
}

type accountService struct{}

func (accountService) Get(_ context.Context, request accounttransport.GetRequest) (accounttransport.GetResponse, error) {
	return accounttransport.GetResponse{Name: "account:" + request.ID}, nil
}

type httpBackend struct {
	runtime *coreRuntime
	core    *coretransport.Client
	account *accounttransport.Client
}

func (backend *httpBackend) Health(ctx context.Context, _ apitransport.HealthRequest) (apitransport.HealthResponse, error) {
	response, err := backend.core.Health(ctx, coretransport.HealthRequest{})
	return apitransport.HealthResponse{Ready: response.Ready}, err
}

func (backend *httpBackend) Register(ctx context.Context, request apitransport.RegisterRequest) (apitransport.RegisterResponse, error) {
	response, err := backend.core.Register(ctx, coretransport.RegisterRequest{Tenant: request.Tenant, Username: request.Username, Password: request.Password, Email: request.Email, DisplayName: request.DisplayName})
	return apitransport.RegisterResponse{AccountID: response.AccountID}, err
}

func (backend *httpBackend) Login(ctx context.Context, request apitransport.LoginRequest) (apitransport.LoginResponse, error) {
	response, err := backend.core.Login(ctx, coretransport.LoginRequest{Tenant: request.Tenant, Username: request.Username, Password: request.Password})
	return apitransport.LoginResponse{SessionID: response.SessionID, AccessToken: response.AccessToken, RefreshToken: response.RefreshToken}, err
}

func (backend *httpBackend) Refresh(ctx context.Context, request apitransport.RefreshRequest) (apitransport.RefreshResponse, error) {
	response, err := backend.core.Refresh(ctx, coretransport.RefreshRequest{RefreshToken: request.RefreshToken})
	return apitransport.RefreshResponse{SessionID: response.SessionID, AccessToken: response.AccessToken, RefreshToken: response.RefreshToken}, err
}

func (backend *httpBackend) Revoke(ctx context.Context, request apitransport.RevokeRequest) (apitransport.RevokeResponse, error) {
	if !backend.runtime.store.sessionBelongsTo(coreapp.SessionID(request.SessionID), request.Identity.TenantID, coreapp.TenantMemberID(request.Identity.SubjectID)) {
		return apitransport.RevokeResponse{}, errors.New("invalid_credentials")
	}
	response, err := backend.core.Revoke(ctx, coretransport.RevokeRequest{SessionID: request.SessionID})
	return apitransport.RevokeResponse{Revoked: response.Revoked}, err
}

func (backend *httpBackend) CheckPermission(ctx context.Context, request apitransport.CheckPermissionRequest) (apitransport.CheckPermissionResponse, error) {
	response, err := backend.core.CheckPermission(ctx, coretransport.CheckPermissionRequest{TenantID: request.Identity.TenantID, SubjectID: request.Identity.SubjectID, Permission: request.Permission})
	return apitransport.CheckPermissionResponse{Allowed: response.Allowed}, err
}

func (backend *httpBackend) Providers(context.Context, apitransport.ListProvidersRequest) (apitransport.ListProvidersResponse, error) {
	descriptors := backend.runtime.providers.Descriptors()
	items := make([]apitransport.ProviderDescriptor, len(descriptors))
	for index, descriptor := range descriptors {
		items[index] = apitransport.ProviderDescriptor{ID: descriptor.ID, Protocol: descriptor.Protocol, Capabilities: apitransport.ProviderCapabilities{Authenticate: descriptor.Capabilities.Authenticate, AutoProvision: descriptor.Capabilities.AutoProvision, GroupClaims: descriptor.Capabilities.GroupClaims}}
	}
	return apitransport.ListProvidersResponse{Items: items}, nil
}

func (backend *httpBackend) GetAccount(ctx context.Context, request apitransport.GetAccountRequest) (apitransport.GetAccountResponse, error) {
	response, err := backend.account.Get(ctx, accounttransport.GetRequest{ID: request.ID})
	return apitransport.GetAccountResponse{Name: response.Name}, err
}

func main() {
	httpAddress := flag.String("listen", "127.0.0.1:0", "HTTP listen address")
	rpcAddress := flag.String("rpc-listen", "127.0.0.1:0", "Core RPC listen address")
	withProvider := flag.Bool("with-provider", false, "compose the fixture identity provider")
	flag.Parse()
	runtime, err := newCoreRuntime(*withProvider)
	if err != nil {
		panic(err)
	}
	coreListener, err := net.Listen("tcp", *rpcAddress)
	if err != nil {
		panic(err)
	}
	defer coreListener.Close()
	accountListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer accountListener.Close()
	go func() { _ = coretransport.NewServer(runtime).Serve(coreListener) }()
	go func() { _ = accounttransport.NewServer(accountService{}).Serve(accountListener) }()
	coreClient, err := coretransport.Dial(coreListener.Addr().String())
	if err != nil {
		panic(err)
	}
	defer coreClient.Close()
	accountClient, err := accounttransport.Dial(accountListener.Addr().String())
	if err != nil {
		panic(err)
	}
	defer accountClient.Close()
	listener, err := net.Listen("tcp", *httpAddress)
	if err != nil {
		panic(err)
	}
	backend := &httpBackend{runtime: runtime, core: coreClient, account: accountClient}
	server := &http.Server{Handler: apitransport.NewHandler(backend, runtime)}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"http": "http://" + listener.Addr().String(), "rpc": coreListener.Addr().String()}); err != nil {
		panic(err)
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		_ = server.Close()
	case err := <-serveErrors:
		if err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}
}

var _ coreapp.IdentityProvider = identityProvider{}
var _ coretransport.Service = (*coreRuntime)(nil)
var _ accounttransport.Service = accountService{}
var _ apitransport.Security = (*coreRuntime)(nil)
var _ apitransport.Backend = (*httpBackend)(nil)

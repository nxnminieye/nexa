package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type testClient struct {
	health Health
}

func (c testClient) Health(context.Context) (Health, error) { return c.health, nil }
func (testClient) Register(context.Context, RegisterRequest) (RegisterResponse, error) {
	return RegisterResponse{}, nil
}
func (testClient) Login(context.Context, LoginRequest) (TokenPair, error)          { return TokenPair{}, nil }
func (testClient) Refresh(context.Context, string) (TokenPair, error)              { return TokenPair{}, nil }
func (testClient) Revoke(context.Context, Metadata) error                          { return nil }
func (testClient) AccessCodes(context.Context, Metadata) ([]string, error)         { return nil, nil }
func (testClient) UserInfo(context.Context, Metadata) (UserInfo, error)            { return UserInfo{}, nil }
func (testClient) AllMenus(context.Context, Metadata) ([]RouteItem, error)         { return nil, nil }
func (testClient) CheckPermission(context.Context, Metadata, string) (bool, error) { return false, nil }
func (testClient) ListTenantMembers(context.Context, Metadata, TenantMemberQuery) (TenantMemberPage, error) {
	return TenantMemberPage{}, nil
}
func (testClient) GetTenantMember(context.Context, Metadata, string, string) (TenantMemberItem, error) {
	return TenantMemberItem{}, nil
}
func (testClient) ListIdentityAccounts(context.Context, Metadata, ListQuery) (IdentityAccountPage, error) {
	return IdentityAccountPage{}, nil
}
func (testClient) GetIdentityAccount(context.Context, Metadata, string) (IdentityAccountItem, error) {
	return IdentityAccountItem{}, nil
}
func (testClient) UpdateAccountStatus(context.Context, Metadata, string, string) (string, error) {
	return "enabled", nil
}
func (testClient) ResetAccountPassword(context.Context, Metadata, string, string) error { return nil }
func (testClient) UpdateTenantMemberStatus(context.Context, Metadata, string, string, string, uint64) (StatusVersion, error) {
	return StatusVersion{}, nil
}
func (testClient) ReplaceTenantMemberRoles(context.Context, Metadata, string, string, []string, uint64) (CodesVersion, error) {
	return CodesVersion{}, nil
}
func (testClient) ProvisionTenant(context.Context, Metadata, string, string, string) (ProvisionTenantResult, error) {
	return ProvisionTenantResult{}, nil
}
func (testClient) ListTenants(context.Context, Metadata, ListQuery) (TenantPage, error) {
	return TenantPage{}, nil
}
func (testClient) GetTenant(context.Context, Metadata, string) (TenantItem, error) {
	return TenantItem{}, nil
}
func (testClient) UpdateTenant(context.Context, Metadata, string, string, uint64) (TenantItem, error) {
	return TenantItem{}, nil
}
func (testClient) UpdateTenantStatus(context.Context, Metadata, string, string, uint64) (StatusVersion, error) {
	return StatusVersion{}, nil
}
func (testClient) ListRoles(context.Context, Metadata, ListQuery) (RolePage, error) {
	return RolePage{}, nil
}
func (testClient) GetRole(context.Context, Metadata, string) (RoleItem, error) {
	return RoleItem{}, nil
}
func (testClient) CreateRole(context.Context, Metadata, string, string) (RoleCreate, error) {
	return RoleCreate{}, nil
}
func (testClient) UpdateRole(context.Context, Metadata, string, string, uint64) (RoleUpdate, error) {
	return RoleUpdate{}, nil
}
func (testClient) UpdateRoleStatus(context.Context, Metadata, string, string, uint64) (StatusVersion, error) {
	return StatusVersion{}, nil
}
func (testClient) ReplaceRolePermissions(context.Context, Metadata, string, []string, uint64) (CodesVersion, error) {
	return CodesVersion{}, nil
}
func (testClient) ReplaceRoleMenus(context.Context, Metadata, string, []string, uint64) (CodesVersion, error) {
	return CodesVersion{}, nil
}
func (testClient) ListMenus(context.Context, Metadata, ListQuery) (MenuPage, error) {
	return MenuPage{}, nil
}
func (testClient) GetMenu(context.Context, Metadata, string) (MenuItem, error) {
	return MenuItem{}, nil
}
func (testClient) ListPermissions(context.Context, Metadata, ListQuery) (PermissionPage, error) {
	return PermissionPage{}, nil
}
func (testClient) GetPermission(context.Context, Metadata, string) (PermissionItem, error) {
	return PermissionItem{}, nil
}

func TestHandlerOwnsOnlyCoreRoutes(t *testing.T) {
	handler, err := NewHandler(testClient{health: Health{Ready: true}}, Config{ListenAddress: "127.0.0.1:0", RPCAddress: "127.0.0.1:1", DefaultTenant: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}

	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/api/records", nil))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown route status = %d, want 404", unknown.Code)
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", health.Code)
	}
	var response struct {
		Code int `json:"code"`
		Data struct {
			Ready bool `json:"ready"`
		} `json:"data"`
	}
	if err := json.NewDecoder(health.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Code != 0 || !response.Data.Ready {
		t.Fatalf("health response = %#v", response)
	}
}

func TestProjectRPCErrorUsesStableRedactedMessages(t *testing.T) {
	tests := []struct {
		code   string
		status int
		value  string
	}{
		{code: "Unauthenticated", status: http.StatusUnauthorized, value: "invalid_credentials"},
		{code: "PermissionDenied", status: http.StatusForbidden, value: "permission_denied"},
		{code: "Unavailable", status: http.StatusServiceUnavailable, value: "service_unavailable"},
		{code: "Internal", status: http.StatusInternalServerError, value: "internal_error"},
		{code: "unauthenticated", status: http.StatusInternalServerError, value: "internal_error"},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			statusErr, ok := ProjectRPCError(test.code).(*StatusError)
			if !ok || statusErr.Status != test.status || statusErr.Code != test.value || statusErr.Message != test.value {
				t.Fatalf("error = %#v, want status=%d code=%q", statusErr, test.status, test.value)
			}
		})
	}
}

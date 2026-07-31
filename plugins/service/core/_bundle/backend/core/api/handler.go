package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Metadata struct {
	Authorization string
	TenantID      string
	Traceparent   string
	RequestID     string
}

type Health struct{ Ready bool }

type RegisterRequest struct {
	Tenant      string
	Username    string
	Password    string
	Email       string
	DisplayName string
}

type RegisterResponse struct {
	AccountID string `json:"accountId"`
}

type LoginRequest struct {
	Username string
	Password string
}

type TokenPair struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	TenantID     int64  `json:"tenantId"`
	MemberID     int64  `json:"memberId"`
}

type UserInfo struct {
	UserID   string   `json:"userId"`
	MemberID int64    `json:"memberId"`
	Username string   `json:"username"`
	Email    string   `json:"email"`
	RealName string   `json:"realName"`
	Avatar   string   `json:"avatar"`
	Roles    []string `json:"roles"`
}

type RouteMeta struct {
	Title              string `json:"title"`
	Icon               string `json:"icon,omitempty"`
	KeepAlive          bool   `json:"keepAlive,omitempty"`
	Order              int64  `json:"order,omitempty"`
	HideInMenu         bool   `json:"hideInMenu,omitempty"`
	HideChildrenInMenu bool   `json:"hideChildrenInMenu,omitempty"`
}

type RouteItem struct {
	Name      string      `json:"name"`
	Path      string      `json:"path"`
	Component string      `json:"component"`
	Redirect  string      `json:"redirect,omitempty"`
	Meta      RouteMeta   `json:"meta"`
	Children  []RouteItem `json:"children,omitempty"`
}

type TenantMemberQuery struct {
	TenantID string
	Keyword  string
	Status   string
	Limit    uint32
	Offset   uint32
}

type TenantMemberItem struct {
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

type TenantMemberPage struct {
	Items []TenantMemberItem `json:"items"`
	Total uint64             `json:"total"`
}

type ListQuery struct {
	Keyword string
	Status  string
	Limit   uint32
	Offset  uint32
}

type IdentityAccountItem struct {
	AccountID       string `json:"accountId"`
	Username        string `json:"username"`
	Email           string `json:"email"`
	DisplayName     string `json:"displayName"`
	SourceCode      string `json:"sourceCode"`
	ExternalSubject string `json:"externalSubject"`
	Status          string `json:"status"`
}

type IdentityAccountPage struct {
	Items []IdentityAccountItem `json:"items"`
	Total uint64                `json:"total"`
}

type TenantItem struct {
	TenantID string `json:"tenantId"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Version  uint64 `json:"version"`
}

type ProvisionTenantResult struct {
	TenantID string `json:"tenantId"`
}

type TenantPage struct {
	Items []TenantItem `json:"items"`
	Total uint64       `json:"total"`
}

type RoleItem struct {
	RoleID          string   `json:"roleId"`
	Code            string   `json:"code"`
	Name            string   `json:"name"`
	Status          string   `json:"status"`
	Managed         bool     `json:"managed"`
	PermissionCodes []string `json:"permissionCodes"`
	MenuCodes       []string `json:"menuCodes"`
	Version         uint64   `json:"version"`
}

type RolePage struct {
	Items []RoleItem `json:"items"`
	Total uint64     `json:"total"`
}

type MenuItem struct {
	MenuID     string `json:"menuId"`
	Code       string `json:"code"`
	ParentCode string `json:"parentCode"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Component  string `json:"component"`
	Icon       string `json:"icon"`
	SortOrder  int32  `json:"sortOrder"`
	SourceID   string `json:"sourceId"`
	Status     string `json:"status"`
}

type MenuPage struct {
	Items []MenuItem `json:"items"`
	Total uint64     `json:"total"`
}

type PermissionItem struct {
	PermissionID string `json:"permissionId"`
	ResourceCode string `json:"resourceCode"`
	Code         string `json:"code"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	SourceID     string `json:"sourceId"`
	Status       string `json:"status"`
}

type PermissionPage struct {
	Items []PermissionItem `json:"items"`
	Total uint64           `json:"total"`
}

type StatusVersion struct {
	Status  string `json:"status"`
	Version uint64 `json:"version"`
}

type CodesVersion struct {
	Codes   []string `json:"codes"`
	Version uint64   `json:"version"`
}

type RoleCreate struct {
	RoleID  string `json:"roleId"`
	Version uint64 `json:"version"`
}

type RoleUpdate struct {
	Updated bool   `json:"updated"`
	Version uint64 `json:"version"`
}

// Client is the generated-transport-neutral Core API dependency. A consumer
// binds it to the canonical Proto client in its own generated scope.
type Client interface {
	Health(context.Context) (Health, error)
	Register(context.Context, RegisterRequest) (RegisterResponse, error)
	Login(context.Context, LoginRequest) (TokenPair, error)
	Refresh(context.Context, string) (TokenPair, error)
	Revoke(context.Context, Metadata) error
	AccessCodes(context.Context, Metadata) ([]string, error)
	UserInfo(context.Context, Metadata) (UserInfo, error)
	AllMenus(context.Context, Metadata) ([]RouteItem, error)
	CheckPermission(context.Context, Metadata, string) (bool, error)
	ListTenantMembers(context.Context, Metadata, TenantMemberQuery) (TenantMemberPage, error)
	GetTenantMember(context.Context, Metadata, string, string) (TenantMemberItem, error)
	ListIdentityAccounts(context.Context, Metadata, ListQuery) (IdentityAccountPage, error)
	GetIdentityAccount(context.Context, Metadata, string) (IdentityAccountItem, error)
	UpdateAccountStatus(context.Context, Metadata, string, string) (string, error)
	ResetAccountPassword(context.Context, Metadata, string, string) error
	UpdateTenantMemberStatus(context.Context, Metadata, string, string, string, uint64) (StatusVersion, error)
	ReplaceTenantMemberRoles(context.Context, Metadata, string, string, []string, uint64) (CodesVersion, error)
	ProvisionTenant(context.Context, Metadata, string, string, string) (ProvisionTenantResult, error)
	ListTenants(context.Context, Metadata, ListQuery) (TenantPage, error)
	GetTenant(context.Context, Metadata, string) (TenantItem, error)
	UpdateTenant(context.Context, Metadata, string, string, uint64) (TenantItem, error)
	UpdateTenantStatus(context.Context, Metadata, string, string, uint64) (StatusVersion, error)
	ListRoles(context.Context, Metadata, ListQuery) (RolePage, error)
	GetRole(context.Context, Metadata, string) (RoleItem, error)
	CreateRole(context.Context, Metadata, string, string) (RoleCreate, error)
	UpdateRole(context.Context, Metadata, string, string, uint64) (RoleUpdate, error)
	UpdateRoleStatus(context.Context, Metadata, string, string, uint64) (StatusVersion, error)
	ReplaceRolePermissions(context.Context, Metadata, string, []string, uint64) (CodesVersion, error)
	ReplaceRoleMenus(context.Context, Metadata, string, []string, uint64) (CodesVersion, error)
	ListMenus(context.Context, Metadata, ListQuery) (MenuPage, error)
	GetMenu(context.Context, Metadata, string) (MenuItem, error)
	ListPermissions(context.Context, Metadata, ListQuery) (PermissionPage, error)
	GetPermission(context.Context, Metadata, string) (PermissionItem, error)
}

type Config struct {
	ListenAddress string `json:"listenAddress"`
	RPCAddress    string `json:"rpcAddress"`
	DefaultTenant string `json:"defaultTenant"`
}

type Handler struct {
	client        Client
	defaultTenant string
}

func NewHandler(client Client, config Config) (*Handler, error) {
	if client == nil {
		return nil, errors.New("core api client is required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Handler{client: client, defaultTenant: strings.TrimSpace(config.DefaultTenant)}, nil
}

func ParseConfig(data []byte) (Config, error) {
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, errors.New("core api config is invalid")
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.ListenAddress) == "" || strings.TrimSpace(c.RPCAddress) == "" || strings.TrimSpace(c.DefaultTenant) == "" {
		return errors.New("core api config requires listenAddress, rpcAddress, and defaultTenant")
	}
	return nil
}

func NewServer(client Client, config Config) (*http.Server, error) {
	handler, err := NewHandler(client, config)
	if err != nil {
		return nil, err
	}
	return &http.Server{
		Addr:              config.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: time.Second,
	}, nil
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/api/health":
		h.health(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/api/auth/register":
		h.register(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/api/auth/login":
		h.login(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/api/auth/refresh":
		h.refresh(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/api/auth/logout":
		h.logout(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/api/auth/codes":
		h.accessCodes(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/api/user/info":
		h.userInfo(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/api/menu/all":
		h.allMenus(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/auth/permissions/"):
		h.checkPermission(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/api/users":
		h.listMembers(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/users/"):
		h.getMember(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/api/identity-accounts":
		h.listIdentityAccounts(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/identity-accounts/"):
		h.getIdentityAccount(writer, request)
	case request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/status") && strings.HasPrefix(request.URL.Path, "/api/identity-accounts/"):
		h.updateAccountStatus(writer, request)
	case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/password/reset") && strings.HasPrefix(request.URL.Path, "/api/identity-accounts/"):
		h.resetAccountPassword(writer, request)
	case request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/status") && strings.HasPrefix(request.URL.Path, "/api/users/"):
		h.updateMemberStatus(writer, request)
	case request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/roles") && strings.HasPrefix(request.URL.Path, "/api/users/"):
		h.replaceMemberRoles(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/api/tenants":
		h.provisionTenant(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/api/tenants":
		h.listTenants(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/tenants/"):
		h.getTenant(writer, request)
	case request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/status") && strings.HasPrefix(request.URL.Path, "/api/tenants/"):
		h.updateTenantStatus(writer, request)
	case request.Method == http.MethodPut && strings.HasPrefix(request.URL.Path, "/api/tenants/"):
		h.updateTenant(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/api/roles":
		h.listRoles(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/api/roles":
		h.createRole(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/roles/"):
		h.getRole(writer, request)
	case request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/status") && strings.HasPrefix(request.URL.Path, "/api/roles/"):
		h.updateRoleStatus(writer, request)
	case request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/permissions") && strings.HasPrefix(request.URL.Path, "/api/roles/"):
		h.replaceRolePermissions(writer, request)
	case request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/menus") && strings.HasPrefix(request.URL.Path, "/api/roles/"):
		h.replaceRoleMenus(writer, request)
	case request.Method == http.MethodPut && strings.HasPrefix(request.URL.Path, "/api/roles/"):
		h.updateRole(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/api/menus":
		h.listMenus(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/menus/"):
		h.getMenu(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/api/permissions":
		h.listPermissions(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/permissions/"):
		h.getPermission(writer, request)
	default:
		writeStatus(writer, http.StatusNotFound)
	}
}

func (h *Handler) health(writer http.ResponseWriter, request *http.Request) {
	result, err := h.client.Health(request.Context())
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, map[string]bool{"ready": result.Ready})
}

func (h *Handler) register(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	result, err := h.client.Register(request.Context(), RegisterRequest{Tenant: h.defaultTenant, Username: body.Username, Password: body.Password, Email: body.Email, DisplayName: body.DisplayName})
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) login(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	result, err := h.client.Login(request.Context(), LoginRequest{Username: body.Username, Password: body.Password})
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) refresh(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		RefreshToken string `json:"refreshToken"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	result, err := h.client.Refresh(request.Context(), body.RefreshToken)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) logout(writer http.ResponseWriter, request *http.Request) {
	if err := h.client.Revoke(request.Context(), requestMetadata(request)); err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, nil)
}

func (h *Handler) accessCodes(writer http.ResponseWriter, request *http.Request) {
	if rejectQuery(request) {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.AccessCodes(request.Context(), requestMetadata(request))
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) userInfo(writer http.ResponseWriter, request *http.Request) {
	if rejectQuery(request) {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.UserInfo(request.Context(), requestMetadata(request))
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) allMenus(writer http.ResponseWriter, request *http.Request) {
	if rejectQuery(request) {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.AllMenus(request.Context(), requestMetadata(request))
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) checkPermission(writer http.ResponseWriter, request *http.Request) {
	permission := pathValue(request.URL.Path, "/api/auth/permissions/")
	if permission == "" {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	allowed, err := h.client.CheckPermission(request.Context(), requestMetadata(request), permission)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, map[string]bool{"allowed": allowed})
}

func (h *Handler) listMembers(writer http.ResponseWriter, request *http.Request) {
	query, err := memberQuery(request)
	if err != nil {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.ListTenantMembers(request.Context(), requestMetadata(request), query)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) getMember(writer http.ResponseWriter, request *http.Request) {
	memberID := strings.TrimPrefix(request.URL.Path, "/api/users/")
	if memberID == "" {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.GetTenantMember(request.Context(), requestMetadata(request), request.Header.Get("X-Tenant-ID"), memberID)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, map[string]TenantMemberItem{"item": result})
}

func (h *Handler) listIdentityAccounts(writer http.ResponseWriter, request *http.Request) {
	query, err := listQuery(request)
	if err != nil {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.ListIdentityAccounts(request.Context(), requestMetadata(request), query)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) getIdentityAccount(writer http.ResponseWriter, request *http.Request) {
	accountID := pathValue(request.URL.Path, "/api/identity-accounts/")
	if accountID == "" {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.GetIdentityAccount(request.Context(), requestMetadata(request), accountID)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, map[string]IdentityAccountItem{"item": result})
}

func (h *Handler) updateAccountStatus(writer http.ResponseWriter, request *http.Request) {
	accountID := pathValueSuffix(request.URL.Path, "/api/identity-accounts/", "/status")
	var body struct {
		Status string `json:"status"`
	}
	if accountID == "" || !decodeJSON(writer, request, &body) {
		return
	}
	status, err := h.client.UpdateAccountStatus(request.Context(), requestMetadata(request), accountID, body.Status)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, map[string]string{"status": status})
}

func (h *Handler) resetAccountPassword(writer http.ResponseWriter, request *http.Request) {
	accountID := pathValueSuffix(request.URL.Path, "/api/identity-accounts/", "/password/reset")
	var body struct {
		NewPassword string `json:"newPassword"`
	}
	if accountID == "" || !decodeJSON(writer, request, &body) {
		return
	}
	if err := h.client.ResetAccountPassword(request.Context(), requestMetadata(request), accountID, body.NewPassword); err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, map[string]bool{"reset": true})
}

func (h *Handler) updateMemberStatus(writer http.ResponseWriter, request *http.Request) {
	memberID := pathValueSuffix(request.URL.Path, "/api/users/", "/status")
	var body struct {
		Status          string `json:"status"`
		ExpectedVersion uint64 `json:"expectedVersion"`
	}
	if memberID == "" || !decodeJSON(writer, request, &body) {
		return
	}
	result, err := h.client.UpdateTenantMemberStatus(request.Context(), requestMetadata(request), request.Header.Get("X-Tenant-ID"), memberID, body.Status, body.ExpectedVersion)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{"status": result.Status, "version": result.Version})
}

func (h *Handler) replaceMemberRoles(writer http.ResponseWriter, request *http.Request) {
	memberID := pathValueSuffix(request.URL.Path, "/api/users/", "/roles")
	var body struct {
		RoleCodes       []string `json:"roleCodes"`
		ExpectedVersion uint64   `json:"expectedVersion"`
	}
	if memberID == "" || !decodeJSON(writer, request, &body) {
		return
	}
	result, err := h.client.ReplaceTenantMemberRoles(request.Context(), requestMetadata(request), request.Header.Get("X-Tenant-ID"), memberID, body.RoleCodes, body.ExpectedVersion)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{"roleCodes": result.Codes, "version": result.Version})
}

func (h *Handler) provisionTenant(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Code                  string `json:"code"`
		Name                  string `json:"name"`
		InitialAdminAccountID string `json:"initialAdminAccountId"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	result, err := h.client.ProvisionTenant(request.Context(), requestMetadata(request), body.Code, body.Name, body.InitialAdminAccountID)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) listTenants(writer http.ResponseWriter, request *http.Request) {
	query, err := listQuery(request)
	if err != nil {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.ListTenants(request.Context(), requestMetadata(request), query)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) getTenant(writer http.ResponseWriter, request *http.Request) {
	tenantID := pathValue(request.URL.Path, "/api/tenants/")
	if tenantID == "" {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.GetTenant(request.Context(), requestMetadata(request), tenantID)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, map[string]TenantItem{"item": result})
}

func (h *Handler) updateTenant(writer http.ResponseWriter, request *http.Request) {
	tenantID := pathValue(request.URL.Path, "/api/tenants/")
	var body struct {
		Name            string `json:"name"`
		ExpectedVersion uint64 `json:"expectedVersion"`
	}
	if tenantID == "" || !decodeJSON(writer, request, &body) {
		return
	}
	result, err := h.client.UpdateTenant(request.Context(), requestMetadata(request), tenantID, body.Name, body.ExpectedVersion)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, map[string]TenantItem{"item": result})
}

func (h *Handler) updateTenantStatus(writer http.ResponseWriter, request *http.Request) {
	tenantID := pathValueSuffix(request.URL.Path, "/api/tenants/", "/status")
	var body struct {
		Status          string `json:"status"`
		ExpectedVersion uint64 `json:"expectedVersion"`
	}
	if tenantID == "" || !decodeJSON(writer, request, &body) {
		return
	}
	result, err := h.client.UpdateTenantStatus(request.Context(), requestMetadata(request), tenantID, body.Status, body.ExpectedVersion)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) listRoles(writer http.ResponseWriter, request *http.Request) {
	query, err := listQuery(request)
	if err != nil {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.ListRoles(request.Context(), requestMetadata(request), query)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) getRole(writer http.ResponseWriter, request *http.Request) {
	roleID := pathValue(request.URL.Path, "/api/roles/")
	if roleID == "" {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.GetRole(request.Context(), requestMetadata(request), roleID)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, map[string]RoleItem{"item": result})
}

func (h *Handler) createRole(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	result, err := h.client.CreateRole(request.Context(), requestMetadata(request), body.Code, body.Name)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) updateRole(writer http.ResponseWriter, request *http.Request) {
	roleID := pathValue(request.URL.Path, "/api/roles/")
	var body struct {
		Name            string `json:"name"`
		ExpectedVersion uint64 `json:"expectedVersion"`
	}
	if roleID == "" || !decodeJSON(writer, request, &body) {
		return
	}
	result, err := h.client.UpdateRole(request.Context(), requestMetadata(request), roleID, body.Name, body.ExpectedVersion)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) updateRoleStatus(writer http.ResponseWriter, request *http.Request) {
	roleID := pathValueSuffix(request.URL.Path, "/api/roles/", "/status")
	var body struct {
		Status          string `json:"status"`
		ExpectedVersion uint64 `json:"expectedVersion"`
	}
	if roleID == "" || !decodeJSON(writer, request, &body) {
		return
	}
	result, err := h.client.UpdateRoleStatus(request.Context(), requestMetadata(request), roleID, body.Status, body.ExpectedVersion)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) replaceRolePermissions(writer http.ResponseWriter, request *http.Request) {
	roleID := pathValueSuffix(request.URL.Path, "/api/roles/", "/permissions")
	var body struct {
		PermissionCodes []string `json:"permissionCodes"`
		ExpectedVersion uint64   `json:"expectedVersion"`
	}
	if roleID == "" || !decodeJSON(writer, request, &body) {
		return
	}
	result, err := h.client.ReplaceRolePermissions(request.Context(), requestMetadata(request), roleID, body.PermissionCodes, body.ExpectedVersion)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) replaceRoleMenus(writer http.ResponseWriter, request *http.Request) {
	roleID := pathValueSuffix(request.URL.Path, "/api/roles/", "/menus")
	var body struct {
		MenuCodes       []string `json:"menuCodes"`
		ExpectedVersion uint64   `json:"expectedVersion"`
	}
	if roleID == "" || !decodeJSON(writer, request, &body) {
		return
	}
	result, err := h.client.ReplaceRoleMenus(request.Context(), requestMetadata(request), roleID, body.MenuCodes, body.ExpectedVersion)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) listMenus(writer http.ResponseWriter, request *http.Request) {
	query, err := listQuery(request)
	if err != nil {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.ListMenus(request.Context(), requestMetadata(request), query)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) getMenu(writer http.ResponseWriter, request *http.Request) {
	code := pathValue(request.URL.Path, "/api/menus/")
	if code == "" {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.GetMenu(request.Context(), requestMetadata(request), code)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, map[string]MenuItem{"item": result})
}

func (h *Handler) listPermissions(writer http.ResponseWriter, request *http.Request) {
	query, err := listQuery(request)
	if err != nil {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.ListPermissions(request.Context(), requestMetadata(request), query)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (h *Handler) getPermission(writer http.ResponseWriter, request *http.Request) {
	code := pathValue(request.URL.Path, "/api/permissions/")
	if code == "" {
		writeStatus(writer, http.StatusBadRequest)
		return
	}
	result, err := h.client.GetPermission(request.Context(), requestMetadata(request), code)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeSuccess(writer, map[string]PermissionItem{"item": result})
}

func requestMetadata(request *http.Request) Metadata {
	return Metadata{
		Authorization: request.Header.Get("Authorization"),
		TenantID:      strings.TrimSpace(request.Header.Get("X-Tenant-ID")),
		Traceparent:   strings.TrimSpace(request.Header.Get("Traceparent")),
		RequestID:     strings.TrimSpace(request.Header.Get("X-Request-ID")),
	}
}

func memberQuery(request *http.Request) (TenantMemberQuery, error) {
	query, err := listQuery(request)
	if err != nil {
		return TenantMemberQuery{}, err
	}
	return TenantMemberQuery{TenantID: strings.TrimSpace(request.Header.Get("X-Tenant-ID")), Keyword: query.Keyword, Status: query.Status, Limit: query.Limit, Offset: query.Offset}, nil
}

func listQuery(request *http.Request) (ListQuery, error) {
	values := request.URL.Query()
	limit, err := queryUint32(values.Get("limit"))
	if err != nil || limit > 200 {
		return ListQuery{}, errors.New("invalid limit")
	}
	offset, err := queryUint32(values.Get("offset"))
	if err != nil {
		return ListQuery{}, errors.New("invalid offset")
	}
	status := strings.TrimSpace(values.Get("status"))
	if status != "" && status != "enabled" && status != "disabled" {
		return ListQuery{}, errors.New("invalid status")
	}
	return ListQuery{Keyword: strings.TrimSpace(values.Get("keyword")), Status: status, Limit: limit, Offset: offset}, nil
}

func queryUint32(value string) (uint32, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	return uint32(parsed), err
}

func pathValue(path, prefix string) string {
	value := strings.TrimPrefix(path, prefix)
	if value == path || value == "" || strings.Contains(value, "/") {
		return ""
	}
	return value
}

func pathValueSuffix(path, prefix, suffix string) string {
	if !strings.HasSuffix(path, suffix) {
		return ""
	}
	return pathValue(strings.TrimSuffix(path, suffix), prefix)
}

func rejectQuery(request *http.Request) bool { return len(request.URL.Query()) != 0 }

type StatusError struct {
	Status  int
	Code    string
	Message string
}

func (e *StatusError) Error() string {
	if e == nil || e.Code == "" {
		return "internal_error"
	}
	return e.Code
}

func writeClientError(writer http.ResponseWriter, err error) {
	var statusErr *StatusError
	if errors.As(err, &statusErr) && statusErr != nil && statusErr.Status >= 400 && statusErr.Status <= 599 {
		writeFailure(writer, statusErr.Status, statusErr.Code, statusErr.Message)
		return
	}
	writeFailure(writer, http.StatusInternalServerError, "internal_error", "internal server error")
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	if err := json.NewDecoder(request.Body).Decode(target); err != nil {
		writeFailure(writer, http.StatusBadRequest, "invalid_input", "invalid request body")
		return false
	}
	return true
}

func writeSuccess(writer http.ResponseWriter, data any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data any    `json:"data"`
	}{Code: 0, Msg: "ok", Data: data})
}

func writeFailure(writer http.ResponseWriter, status int, code, message string) {
	if code == "" {
		code = http.StatusText(status)
	}
	if message == "" {
		message = code
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		Message string `json:"message"`
	}{Code: status, Msg: code, Message: message})
}

func writeStatus(writer http.ResponseWriter, status int) {
	writeFailure(writer, status, http.StatusText(status), http.StatusText(status))
}

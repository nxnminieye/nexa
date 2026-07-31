package coreapp

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// TransportMetadata is the small request context shared by the generated RPC
// adapter and the Core service. It deliberately contains no HTTP or gRPC type.
type TransportMetadata struct {
	Authorization string
	TenantID      string
	Traceparent   string
	RequestID     string
}

type LoginInput struct {
	Username string
	Password string
}

type TokenPair struct {
	AccessToken  string
	RefreshToken string
	TenantID     int64
	MemberID     int64
}

type CurrentSession struct {
	UserID          string
	Username        string
	RealName        string
	Avatar          string
	Roles           []string
	PermissionCodes []string
}

type UserInfo struct {
	UserID   string
	MemberID int64
	Username string
	Email    string
	RealName string
	Avatar   string
	Roles    []string
}

type RouteMeta struct {
	Title              string
	Icon               string
	KeepAlive          bool
	Order              int64
	HideInMenu         bool
	HideChildrenInMenu bool
}

type RouteItem struct {
	Name      string
	Path      string
	Component string
	Redirect  string
	Meta      RouteMeta
	Children  []RouteItem
}

type TenantMemberQuery struct {
	TenantID string
	Keyword  string
	Status   IAMStatus
	Limit    uint32
	Offset   uint32
}

type TenantMemberItem struct {
	MemberID        string
	AccountID       string
	Username        string
	Email           string
	DisplayName     string
	SourceCode      string
	ExternalSubject string
	Status          IAMStatus
	RoleCodes       []string
	Version         uint64
}

type TenantMemberPageResult struct {
	Items []TenantMemberItem
	Total uint64
}

// RPCService owns the Core behavior behind the generated transport. The
// consumer supplies the generated server adapter and process configuration.
type RPCService struct {
	auth          *LocalAuthenticator
	access        *AccessAuthenticator
	iam           *IAMService
	defaultTenant string
	defaultRouter string
}

func NewRPCService(auth *LocalAuthenticator, access *AccessAuthenticator, iam *IAMService, defaultTenant, defaultRouter string) (*RPCService, error) {
	defaultTenant = strings.TrimSpace(defaultTenant)
	defaultRouter = strings.TrimSpace(defaultRouter)
	if auth == nil || access == nil || iam == nil || defaultTenant == "" || !strings.HasPrefix(defaultRouter, "/") {
		return nil, invalid("rpc-service.new")
	}
	return &RPCService{auth: auth, access: access, iam: iam, defaultTenant: defaultTenant, defaultRouter: defaultRouter}, nil
}

func (s *RPCService) Health(ctx context.Context) (Health, error) {
	return CheckHealth(ctx)
}

func (s *RPCService) Register(ctx context.Context, input LocalRegistration) (IdentityAccount, error) {
	return s.auth.Register(ctx, input)
}

func (s *RPCService) Login(ctx context.Context, input LoginInput) (TokenPair, error) {
	session, err := s.auth.Login(ctx, LocalLogin{Tenant: s.defaultTenant, Username: input.Username, Password: []byte(input.Password)})
	if err != nil {
		return TokenPair{}, err
	}
	return s.tokenPair(ctx, session)
}

func (s *RPCService) Refresh(ctx context.Context, refresh RefreshToken) (TokenPair, error) {
	session, err := s.auth.Refresh(ctx, refresh)
	if err != nil {
		return TokenPair{}, err
	}
	return s.tokenPair(ctx, session)
}

func (s *RPCService) Revoke(ctx context.Context, metadata TransportMetadata) error {
	principal, err := s.authenticate(ctx, metadata, "")
	if err != nil {
		return err
	}
	return s.auth.Revoke(ctx, principal.SessionID)
}

func (s *RPCService) CurrentSession(ctx context.Context, metadata TransportMetadata) (CurrentSession, error) {
	principal, err := s.authenticate(ctx, metadata, "")
	if err != nil {
		return CurrentSession{}, err
	}
	return CurrentSession{
		UserID:          string(principal.Account.ID),
		Username:        principal.Account.Username,
		RealName:        principal.Account.DisplayName,
		Roles:           append([]string(nil), principal.RoleCodes...),
		PermissionCodes: append([]string(nil), principal.PermissionCodes...),
	}, nil
}

func (s *RPCService) AccessCodes(ctx context.Context, metadata TransportMetadata) ([]string, error) {
	principal, err := s.authenticate(ctx, metadata, "nexa.auth.permissions.read")
	if err != nil {
		return nil, err
	}
	return append([]string(nil), principal.PermissionCodes...), nil
}

func (s *RPCService) UserInfo(ctx context.Context, metadata TransportMetadata) (UserInfo, error) {
	principal, err := s.authenticate(ctx, metadata, "nexa.auth.me.read")
	if err != nil {
		return UserInfo{}, err
	}
	memberID, err := strconv.ParseInt(string(principal.MemberID), 10, 64)
	if err != nil {
		return UserInfo{}, storeFailure("rpc-service.user-info", err)
	}
	return UserInfo{
		UserID: string(principal.Account.ID), MemberID: memberID, Username: principal.Account.Username,
		Email: principal.Account.Email, RealName: principal.Account.DisplayName,
		Roles: append([]string(nil), principal.RoleCodes...),
	}, nil
}

func (s *RPCService) AllMenus(ctx context.Context, metadata TransportMetadata) ([]RouteItem, error) {
	principal, err := s.authenticate(ctx, metadata, "nexa.menu.read")
	if err != nil {
		return nil, err
	}
	menus := make([]Menu, 0, len(principal.MenuCodes))
	for _, code := range principal.MenuCodes {
		menu, err := s.iam.GetMenu(ctx, code)
		if err != nil {
			return nil, err
		}
		if menu.PermissionCode != "" && !containsTransportCode(principal.PermissionCodes, menu.PermissionCode) {
			continue
		}
		menus = append(menus, menu)
	}
	return buildRoutes(menus)
}

func (s *RPCService) CheckPermission(ctx context.Context, metadata TransportMetadata, permission string) (bool, error) {
	principal, err := s.authenticate(ctx, metadata, "core.authorization.check")
	if err != nil {
		return false, err
	}
	return containsTransportCode(principal.PermissionCodes, strings.TrimSpace(permission)), nil
}

func (s *RPCService) ListTenantMembers(ctx context.Context, metadata TransportMetadata, query TenantMemberQuery) (TenantMemberPageResult, error) {
	_, tenantID, err := s.authorizeTenant(ctx, metadata, query.TenantID, "nexa.user.read")
	if err != nil {
		return TenantMemberPageResult{}, err
	}
	page, err := s.iam.ListTenantMembers(ctx, ListTenantMembersInput{TenantID: tenantID, ListQuery: ListQuery{Keyword: query.Keyword, Status: query.Status, Limit: query.Limit, Offset: query.Offset}})
	if err != nil {
		return TenantMemberPageResult{}, err
	}
	items := make([]TenantMemberItem, len(page.Items))
	for index, member := range page.Items {
		items[index] = tenantMemberItem(member)
	}
	return TenantMemberPageResult{Items: items, Total: page.Total}, nil
}

func (s *RPCService) GetTenantMember(ctx context.Context, metadata TransportMetadata, tenantID string, memberID TenantMemberID) (TenantMemberItem, error) {
	_, tenantID, err := s.authorizeTenant(ctx, metadata, tenantID, "nexa.user.read")
	if err != nil {
		return TenantMemberItem{}, err
	}
	member, err := s.iam.GetTenantMember(ctx, TenantMemberKey{TenantID: tenantID, MemberID: memberID})
	if err != nil {
		return TenantMemberItem{}, err
	}
	return tenantMemberItem(member), nil
}

func (s *RPCService) tokenPair(ctx context.Context, session Session) (TokenPair, error) {
	principal, err := s.access.Authenticate(ctx, session.AccessToken)
	if err != nil {
		return TokenPair{}, err
	}
	tenantID, tenantErr := strconv.ParseInt(principal.TenantID, 10, 64)
	memberID, memberErr := strconv.ParseInt(string(principal.MemberID), 10, 64)
	if tenantErr != nil || memberErr != nil {
		return TokenPair{}, storeFailure("rpc-service.token-pair", nil)
	}
	return TokenPair{AccessToken: session.AccessToken, RefreshToken: string(session.RefreshToken), TenantID: tenantID, MemberID: memberID}, nil
}

func (s *RPCService) authenticate(ctx context.Context, metadata TransportMetadata, permission string) (AccessPrincipal, error) {
	const bearer = "Bearer "
	authorization := strings.TrimSpace(metadata.Authorization)
	if !strings.HasPrefix(authorization, bearer) || strings.TrimSpace(strings.TrimPrefix(authorization, bearer)) == "" {
		return AccessPrincipal{}, coreError("rpc-service.authenticate", CodeInvalidCredentials, nil)
	}
	principal, err := s.access.Authenticate(ctx, strings.TrimSpace(strings.TrimPrefix(authorization, bearer)))
	if err != nil {
		return AccessPrincipal{}, err
	}
	if metadata.TenantID != "" && metadata.TenantID != principal.TenantID {
		return AccessPrincipal{}, coreError("rpc-service.authenticate", CodePermissionDenied, nil)
	}
	if permission != "" && !containsTransportCode(principal.PermissionCodes, permission) {
		return AccessPrincipal{}, coreError("rpc-service.authorize", CodePermissionDenied, nil)
	}
	return principal, nil
}

func (s *RPCService) authorizeTenant(ctx context.Context, metadata TransportMetadata, requestedTenant, permission string) (AccessPrincipal, string, error) {
	principal, err := s.authenticate(ctx, metadata, permission)
	if err != nil {
		return AccessPrincipal{}, "", err
	}
	if requestedTenant != "" && requestedTenant != principal.TenantID {
		return AccessPrincipal{}, "", coreError("rpc-service.authorize-tenant", CodePermissionDenied, nil)
	}
	return principal, principal.TenantID, nil
}

func containsTransportCode(values []string, expected string) bool {
	if expected == "" {
		return false
	}
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func tenantMemberItem(member TenantMember) TenantMemberItem {
	return TenantMemberItem{
		MemberID: string(member.ID), AccountID: string(member.AccountID), Username: member.AccountUsername,
		Email: member.AccountEmail, DisplayName: member.AccountDisplayName, SourceCode: member.AccountSourceCode,
		ExternalSubject: member.AccountExternalSubject, Status: member.Status,
		RoleCodes: append([]string(nil), member.ManualRoleCodes...), Version: member.Version,
	}
}

type transportRouteNode struct {
	menu     Menu
	children []*transportRouteNode
}

func buildRoutes(menus []Menu) ([]RouteItem, error) {
	menus = append([]Menu(nil), menus...)
	sort.Slice(menus, func(i, j int) bool {
		if menus[i].SortOrder != menus[j].SortOrder {
			return menus[i].SortOrder < menus[j].SortOrder
		}
		return menus[i].Code < menus[j].Code
	})
	nodes := make(map[string]*transportRouteNode, len(menus))
	for _, menu := range menus {
		if menu.Code == "" || menu.RouteName == "" || menu.Path == "" || menu.Component == "" || menu.DisplayName == "" {
			return nil, coreError("rpc-service.menus", CodeFailedPrecondition, nil)
		}
		nodes[menu.Code] = &transportRouteNode{menu: menu}
	}
	roots := make([]*transportRouteNode, 0, len(nodes))
	for _, menu := range menus {
		node := nodes[menu.Code]
		if menu.ParentCode == "" {
			roots = append(roots, node)
			continue
		}
		parent := nodes[menu.ParentCode]
		if parent == nil {
			return nil, coreError("rpc-service.menus", CodeFailedPrecondition, nil)
		}
		parent.children = append(parent.children, node)
	}
	result := make([]RouteItem, len(roots))
	for index, root := range roots {
		result[index] = routeItem(root)
	}
	return result, nil
}

func routeItem(node *transportRouteNode) RouteItem {
	result := RouteItem{
		Name: node.menu.RouteName, Path: node.menu.Path, Component: node.menu.Component,
		Meta: RouteMeta{Title: node.menu.DisplayName, Icon: node.menu.Icon, KeepAlive: node.menu.KeepAlive, Order: int64(node.menu.SortOrder), HideInMenu: !node.menu.Visible},
	}
	if len(node.children) != 0 {
		result.Children = make([]RouteItem, len(node.children))
		for index, child := range node.children {
			result.Children[index] = routeItem(child)
		}
		if result.Component == "BasicLayout" {
			result.Redirect = result.Children[0].Path
		}
	}
	return result
}

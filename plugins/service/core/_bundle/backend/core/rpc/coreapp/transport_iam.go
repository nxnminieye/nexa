package coreapp

import (
	"context"
	"strings"
)

type IAMListQuery struct {
	Keyword string
	Status  IAMStatus
	Limit   uint32
	Offset  uint32
}

type IdentityAccountItem struct {
	AccountID       string
	Username        string
	Email           string
	DisplayName     string
	SourceCode      string
	ExternalSubject string
	Status          IAMStatus
}

type IdentityAccountPageResult struct {
	Items []IdentityAccountItem
	Total uint64
}

type TenantItem struct {
	TenantID string
	Code     string
	Name     string
	Status   IAMStatus
	Version  uint64
}

type TenantPageResult struct {
	Items []TenantItem
	Total uint64
}

type RoleItem struct {
	RoleID          string
	Code            string
	Name            string
	Status          IAMStatus
	Managed         bool
	PermissionCodes []string
	MenuCodes       []string
	Version         uint64
}

type RolePageResult struct {
	Items []RoleItem
	Total uint64
}

type MenuItem struct {
	MenuID     string
	Code       string
	ParentCode string
	Name       string
	Path       string
	Component  string
	Icon       string
	SortOrder  int32
	SourceID   string
	Status     IAMStatus
}

type MenuPageResult struct {
	Items []MenuItem
	Total uint64
}

type PermissionItem struct {
	PermissionID string
	ResourceCode string
	Code         string
	Name         string
	Description  string
	SourceID     string
	Status       IAMStatus
}

type PermissionPageResult struct {
	Items []PermissionItem
	Total uint64
}

type AccountStatusResult struct {
	Status IAMStatus
}

type RoleCreateResult struct {
	RoleID  string
	Version uint64
}

type RoleUpdateResult struct {
	Updated bool
	Version uint64
}

type RoleCodesResult struct {
	Codes   []string
	Version uint64
}

type TenantMemberStatusResult struct {
	Status  IAMStatus
	Version uint64
}

type TenantMemberRolesResult struct {
	Codes   []string
	Version uint64
}

type ProvisionTenantRequest struct {
	Code                  string
	Name                  string
	InitialAdminAccountID string
}

func (s *RPCService) ListIdentityAccounts(ctx context.Context, metadata TransportMetadata, query IAMListQuery) (IdentityAccountPageResult, error) {
	if _, err := s.authenticate(ctx, metadata, "nexa.identity.account.read"); err != nil {
		return IdentityAccountPageResult{}, err
	}
	listQuery, err := transportListQuery("rpc.list-identity-accounts", query)
	if err != nil {
		return IdentityAccountPageResult{}, err
	}
	page, err := s.iam.ListIdentityAccounts(ctx, ListIdentityAccountsInput{ListQuery: listQuery})
	if err != nil {
		return IdentityAccountPageResult{}, err
	}
	items := make([]IdentityAccountItem, len(page.Items))
	for index, account := range page.Items {
		items[index] = identityAccountItem(account)
	}
	return IdentityAccountPageResult{Items: items, Total: page.Total}, nil
}

func (s *RPCService) GetIdentityAccount(ctx context.Context, metadata TransportMetadata, accountID IdentityAccountID) (IdentityAccountItem, error) {
	if _, err := s.authenticate(ctx, metadata, "nexa.identity.account.read"); err != nil {
		return IdentityAccountItem{}, err
	}
	account, err := s.iam.GetIdentityAccount(ctx, accountID)
	if err != nil {
		return IdentityAccountItem{}, err
	}
	return identityAccountItem(account), nil
}

func (s *RPCService) UpdateAccountStatus(ctx context.Context, metadata TransportMetadata, accountID IdentityAccountID, status IAMStatus) (AccountStatusResult, error) {
	if _, err := s.authenticate(ctx, metadata, "nexa.identity.account.status.update"); err != nil {
		return AccountStatusResult{}, err
	}
	account, err := s.iam.SetAccountStatus(ctx, SetAccountStatusInput{Actor: SystemActor{System: true}, AccountID: accountID, Status: status})
	if err != nil {
		return AccountStatusResult{}, err
	}
	return AccountStatusResult{Status: account.Status}, nil
}

func (s *RPCService) ResetAccountPassword(ctx context.Context, metadata TransportMetadata, accountID IdentityAccountID, password string) error {
	if _, err := s.authenticate(ctx, metadata, "nexa.identity.account.password.reset"); err != nil {
		return err
	}
	return s.iam.ResetAccountPassword(ctx, ResetAccountPasswordInput{Actor: SystemActor{System: true}, AccountID: accountID, Password: []byte(password)})
}

func (s *RPCService) UpdateTenantMemberStatus(ctx context.Context, metadata TransportMetadata, tenantID string, memberID TenantMemberID, status IAMStatus, expectedVersion uint64) (TenantMemberStatusResult, error) {
	_, tenantID, err := s.authorizeTenant(ctx, metadata, tenantID, "nexa.user.status.update")
	if err != nil {
		return TenantMemberStatusResult{}, err
	}
	member, err := s.iam.SetTenantMemberStatus(ctx, SetTenantMemberStatusInput{TenantID: tenantID, MemberID: memberID, Status: status, ExpectedVersion: expectedVersion})
	if err != nil {
		return TenantMemberStatusResult{}, err
	}
	return TenantMemberStatusResult{Status: member.Status, Version: member.Version}, nil
}

func (s *RPCService) ReplaceTenantMemberRoles(ctx context.Context, metadata TransportMetadata, tenantID string, memberID TenantMemberID, roleCodes []string, expectedVersion uint64) (TenantMemberRolesResult, error) {
	_, tenantID, err := s.authorizeTenant(ctx, metadata, tenantID, "nexa.user.roles.update")
	if err != nil {
		return TenantMemberRolesResult{}, err
	}
	member, err := s.iam.ReplaceManualRoles(ctx, ReplaceManualRolesInput{TenantID: tenantID, MemberID: memberID, RoleCodes: roleCodes, ExpectedVersion: expectedVersion})
	if err != nil {
		return TenantMemberRolesResult{}, err
	}
	return TenantMemberRolesResult{Codes: append([]string(nil), member.ManualRoleCodes...), Version: member.Version}, nil
}

func (s *RPCService) ProvisionTenant(ctx context.Context, metadata TransportMetadata, request ProvisionTenantRequest) (TenantItem, error) {
	if _, err := s.authenticate(ctx, metadata, "nexa.tenant.create"); err != nil {
		return TenantItem{}, err
	}
	account, err := s.iam.GetIdentityAccount(ctx, IdentityAccountID(strings.TrimSpace(request.InitialAdminAccountID)))
	if err != nil {
		return TenantItem{}, err
	}
	result, err := s.iam.ProvisionTenant(ctx, ProvisionTenantInput{
		TenantCode: request.Code, DisplayName: request.Name, DefaultRouter: s.defaultRouter, OwnerAccountID: account.ID,
		OwnerUsername: account.Username, OwnerEmail: account.Email, OwnerName: account.DisplayName,
	})
	if err != nil {
		return TenantItem{}, err
	}
	return tenantItem(result.Tenant), nil
}

func (s *RPCService) ListTenants(ctx context.Context, metadata TransportMetadata, query IAMListQuery) (TenantPageResult, error) {
	if _, err := s.authenticate(ctx, metadata, "nexa.tenant.read"); err != nil {
		return TenantPageResult{}, err
	}
	listQuery, err := transportListQuery("rpc.list-tenants", query)
	if err != nil {
		return TenantPageResult{}, err
	}
	page, err := s.iam.ListTenants(ctx, ListTenantsInput{ListQuery: listQuery})
	if err != nil {
		return TenantPageResult{}, err
	}
	items := make([]TenantItem, len(page.Items))
	for index, tenant := range page.Items {
		items[index] = tenantItem(tenant)
	}
	return TenantPageResult{Items: items, Total: page.Total}, nil
}

func (s *RPCService) GetTenant(ctx context.Context, metadata TransportMetadata, tenantID string) (TenantItem, error) {
	if _, err := s.authenticate(ctx, metadata, "nexa.tenant.read"); err != nil {
		return TenantItem{}, err
	}
	tenant, err := s.iam.GetTenant(ctx, strings.TrimSpace(tenantID))
	if err != nil {
		return TenantItem{}, err
	}
	return tenantItem(tenant), nil
}

func (s *RPCService) UpdateTenant(ctx context.Context, metadata TransportMetadata, tenantID, name string, expectedVersion uint64) (TenantItem, error) {
	if _, err := s.authenticate(ctx, metadata, "nexa.tenant.update"); err != nil {
		return TenantItem{}, err
	}
	tenant, err := s.iam.UpdateTenant(ctx, UpdateTenantInput{Actor: SystemActor{System: true}, TenantID: tenantID, DisplayName: name, ExpectedVersion: expectedVersion})
	if err != nil {
		return TenantItem{}, err
	}
	return tenantItem(tenant), nil
}

func (s *RPCService) UpdateTenantStatus(ctx context.Context, metadata TransportMetadata, tenantID string, status IAMStatus, expectedVersion uint64) (TenantMemberStatusResult, error) {
	if _, err := s.authenticate(ctx, metadata, "nexa.tenant.update"); err != nil {
		return TenantMemberStatusResult{}, err
	}
	tenant, err := s.iam.SetTenantStatus(ctx, SetTenantStatusInput{Actor: SystemActor{System: true}, TenantID: tenantID, Status: status, ExpectedVersion: expectedVersion})
	if err != nil {
		return TenantMemberStatusResult{}, err
	}
	return TenantMemberStatusResult{Status: tenant.Status, Version: tenant.Version}, nil
}

func (s *RPCService) ListRoles(ctx context.Context, metadata TransportMetadata, tenantID string, query IAMListQuery) (RolePageResult, error) {
	_, tenantID, err := s.authorizeTenant(ctx, metadata, tenantID, "nexa.auth.roles.read")
	if err != nil {
		return RolePageResult{}, err
	}
	listQuery, err := transportListQuery("rpc.list-roles", query)
	if err != nil {
		return RolePageResult{}, err
	}
	page, err := s.iam.ListTenantRoles(ctx, ListTenantRolesInput{TenantID: tenantID, ListQuery: listQuery})
	if err != nil {
		return RolePageResult{}, err
	}
	items := make([]RoleItem, len(page.Items))
	for index, role := range page.Items {
		items[index] = roleItem(role)
	}
	return RolePageResult{Items: items, Total: page.Total}, nil
}

func (s *RPCService) GetRole(ctx context.Context, metadata TransportMetadata, tenantID string, roleID TenantRoleID) (RoleItem, error) {
	_, tenantID, err := s.authorizeTenant(ctx, metadata, tenantID, "nexa.auth.roles.read")
	if err != nil {
		return RoleItem{}, err
	}
	role, err := s.iam.GetTenantRole(ctx, TenantRoleKey{TenantID: tenantID, RoleID: roleID})
	if err != nil {
		return RoleItem{}, err
	}
	return roleItem(role), nil
}

func (s *RPCService) CreateRole(ctx context.Context, metadata TransportMetadata, tenantID, code, name string) (RoleCreateResult, error) {
	_, tenantID, err := s.authorizeTenant(ctx, metadata, tenantID, "nexa.auth.roles.create")
	if err != nil {
		return RoleCreateResult{}, err
	}
	role, err := s.iam.CreateTenantRole(ctx, CreateTenantRoleInput{TenantID: tenantID, Code: code, DisplayName: name})
	if err != nil {
		return RoleCreateResult{}, err
	}
	return RoleCreateResult{RoleID: string(role.ID), Version: role.Version}, nil
}

func (s *RPCService) UpdateRole(ctx context.Context, metadata TransportMetadata, tenantID string, roleID TenantRoleID, name string, expectedVersion uint64) (RoleUpdateResult, error) {
	_, tenantID, err := s.authorizeTenant(ctx, metadata, tenantID, "nexa.auth.roles.update")
	if err != nil {
		return RoleUpdateResult{}, err
	}
	role, err := s.iam.UpdateTenantRole(ctx, UpdateTenantRoleInput{TenantID: tenantID, RoleID: roleID, DisplayName: name, ExpectedVersion: expectedVersion})
	if err != nil {
		return RoleUpdateResult{}, err
	}
	return RoleUpdateResult{Updated: true, Version: role.Version}, nil
}

func (s *RPCService) UpdateRoleStatus(ctx context.Context, metadata TransportMetadata, tenantID string, roleID TenantRoleID, status IAMStatus, expectedVersion uint64) (TenantMemberStatusResult, error) {
	_, tenantID, err := s.authorizeTenant(ctx, metadata, tenantID, "nexa.auth.roles.update")
	if err != nil {
		return TenantMemberStatusResult{}, err
	}
	role, err := s.iam.SetTenantRoleStatus(ctx, SetTenantRoleStatusInput{TenantID: tenantID, RoleID: roleID, Status: status, ExpectedVersion: expectedVersion})
	if err != nil {
		return TenantMemberStatusResult{}, err
	}
	return TenantMemberStatusResult{Status: role.Status, Version: role.Version}, nil
}

func (s *RPCService) ReplaceRolePermissions(ctx context.Context, metadata TransportMetadata, tenantID string, roleID TenantRoleID, codes []string, expectedVersion uint64) (RoleCodesResult, error) {
	_, tenantID, err := s.authorizeTenant(ctx, metadata, tenantID, "nexa.auth.role_permissions.bind")
	if err != nil {
		return RoleCodesResult{}, err
	}
	role, err := s.iam.ReplaceRolePermissions(ctx, ReplaceRolePermissionsInput{TenantID: tenantID, RoleID: roleID, PermissionCodes: codes, ExpectedVersion: expectedVersion})
	if err != nil {
		return RoleCodesResult{}, err
	}
	return RoleCodesResult{Codes: append([]string(nil), role.PermissionCodes...), Version: role.Version}, nil
}

func (s *RPCService) ReplaceRoleMenus(ctx context.Context, metadata TransportMetadata, tenantID string, roleID TenantRoleID, codes []string, expectedVersion uint64) (RoleCodesResult, error) {
	_, tenantID, err := s.authorizeTenant(ctx, metadata, tenantID, "nexa.auth.roles.update")
	if err != nil {
		return RoleCodesResult{}, err
	}
	role, err := s.iam.ReplaceRoleMenus(ctx, ReplaceRoleMenusInput{TenantID: tenantID, RoleID: roleID, MenuCodes: codes, ExpectedVersion: expectedVersion})
	if err != nil {
		return RoleCodesResult{}, err
	}
	return RoleCodesResult{Codes: append([]string(nil), role.MenuCodes...), Version: role.Version}, nil
}

func (s *RPCService) ListMenus(ctx context.Context, metadata TransportMetadata, query IAMListQuery) (MenuPageResult, error) {
	if _, err := s.authenticate(ctx, metadata, "nexa.menu.read"); err != nil {
		return MenuPageResult{}, err
	}
	listQuery, err := transportListQuery("rpc.list-menus", query)
	if err != nil {
		return MenuPageResult{}, err
	}
	page, err := s.iam.ListMenus(ctx, ListMenusInput{ListQuery: listQuery})
	if err != nil {
		return MenuPageResult{}, err
	}
	items := make([]MenuItem, len(page.Items))
	for index, menu := range page.Items {
		items[index] = menuItem(menu)
	}
	return MenuPageResult{Items: items, Total: page.Total}, nil
}

func (s *RPCService) GetMenu(ctx context.Context, metadata TransportMetadata, code string) (MenuItem, error) {
	if _, err := s.authenticate(ctx, metadata, "nexa.menu.read"); err != nil {
		return MenuItem{}, err
	}
	menu, err := s.iam.GetMenu(ctx, code)
	if err != nil {
		return MenuItem{}, err
	}
	return menuItem(menu), nil
}

func (s *RPCService) ListPermissions(ctx context.Context, metadata TransportMetadata, query IAMListQuery) (PermissionPageResult, error) {
	if _, err := s.authenticate(ctx, metadata, "nexa.auth.permissions.read"); err != nil {
		return PermissionPageResult{}, err
	}
	listQuery, err := transportListQuery("rpc.list-permissions", query)
	if err != nil {
		return PermissionPageResult{}, err
	}
	page, err := s.iam.ListPermissions(ctx, ListPermissionsInput{ListQuery: listQuery})
	if err != nil {
		return PermissionPageResult{}, err
	}
	items := make([]PermissionItem, len(page.Items))
	for index, permission := range page.Items {
		items[index] = permissionItem(permission)
	}
	return PermissionPageResult{Items: items, Total: page.Total}, nil
}

func (s *RPCService) GetPermission(ctx context.Context, metadata TransportMetadata, code string) (PermissionItem, error) {
	if _, err := s.authenticate(ctx, metadata, "nexa.auth.permissions.read"); err != nil {
		return PermissionItem{}, err
	}
	permission, err := s.iam.GetPermission(ctx, code)
	if err != nil {
		return PermissionItem{}, err
	}
	return permissionItem(permission), nil
}

func transportListQuery(operation string, query IAMListQuery) (ListQuery, error) {
	if query.Limit > 200 {
		return ListQuery{}, invalid(operation)
	}
	if query.Status != "" && !validIAMStatus(query.Status) {
		return ListQuery{}, invalid(operation)
	}
	return ListQuery{Keyword: strings.TrimSpace(query.Keyword), Status: query.Status, Limit: query.Limit, Offset: query.Offset}, nil
}

func identityAccountItem(account IdentityAccount) IdentityAccountItem {
	return IdentityAccountItem{AccountID: string(account.ID), Username: account.Username, Email: account.Email, DisplayName: account.DisplayName, SourceCode: account.SourceCode, ExternalSubject: account.ExternalSubject, Status: account.Status}
}

func tenantItem(tenant Tenant) TenantItem {
	return TenantItem{TenantID: tenant.ID, Code: tenant.Code, Name: tenant.DisplayName, Status: tenant.Status, Version: tenant.Version}
}

func roleItem(role TenantRole) RoleItem {
	return RoleItem{RoleID: string(role.ID), Code: role.Code, Name: role.DisplayName, Status: role.Status, Managed: role.Managed, PermissionCodes: append([]string(nil), role.PermissionCodes...), MenuCodes: append([]string(nil), role.MenuCodes...), Version: role.Version}
}

func menuItem(menu Menu) MenuItem {
	return MenuItem{MenuID: menu.ID, Code: menu.Code, ParentCode: menu.ParentCode, Name: menu.DisplayName, Path: menu.Path, Component: menu.Component, Icon: menu.Icon, SortOrder: menu.SortOrder, SourceID: menu.SourceID, Status: menu.Status}
}

func permissionItem(permission Permission) PermissionItem {
	return PermissionItem{PermissionID: permission.ID, ResourceCode: permission.ResourceCode, Code: permission.Code, Name: permission.DisplayName, Description: permission.Description, SourceID: permission.SourceID, Status: permission.Status}
}

package coreapp

import (
	"context"
	"errors"
	"sort"
	"strings"
)

type IAMStatus string

const (
	IAMStatusEnabled  IAMStatus = "enabled"
	IAMStatusDisabled IAMStatus = "disabled"
)

type SystemActor struct {
	AccountID IdentityAccountID
	System    bool
}

type Tenant struct {
	ID          string
	Code        string
	DisplayName string
	Status      IAMStatus
	Version     uint64
}

type TenantRole struct {
	ID              TenantRoleID
	TenantID        string
	Code            string
	DisplayName     string
	Status          IAMStatus
	Managed         bool
	PermissionCodes []string
	MenuCodes       []string
	Version         uint64
}

type Menu struct {
	ID          string
	SourceID    string
	Code        string
	ParentCode  string
	DisplayName string
	Path        string
	Component   string
	Icon        string
	SortOrder   int32
	Status      IAMStatus
}

type Permission struct {
	ID           string
	SourceID     string
	ResourceCode string
	Code         string
	DisplayName  string
	Description  string
	Status       IAMStatus
}

type PolicyResourceKind string

const (
	PolicyResourceTenant  PolicyResourceKind = "tenant"
	PolicyResourceMember  PolicyResourceKind = "member"
	PolicyResourceRole    PolicyResourceKind = "role"
	PolicyResourceAccount PolicyResourceKind = "account"
	PolicyResourceCatalog PolicyResourceKind = "catalog"
)

type PolicyReconcileInput struct {
	Kind       PolicyResourceKind
	TenantID   string
	ResourceID string
}

type PolicyReconciler interface {
	ReconcilePolicy(context.Context, PolicyReconcileInput) error
}

type IAMService struct {
	store      IAMStore
	hasher     PasswordHasher
	reconciler PolicyReconciler
}

func NewIAMService(store IAMStore, hasher PasswordHasher, reconciler PolicyReconciler) (*IAMService, error) {
	if interfaceNil(store) || interfaceNil(hasher) || interfaceNil(reconciler) {
		return nil, invalid("iam.new")
	}
	return &IAMService{store: store, hasher: hasher, reconciler: reconciler}, nil
}

func (s *IAMService) ListIdentityAccounts(ctx context.Context, input ListIdentityAccountsInput) (IdentityAccountPage, error) {
	const operation = "iam.list-accounts"
	query, err := normalizeListQuery(operation, input.ListQuery)
	if err != nil {
		return IdentityAccountPage{}, err
	}
	page, err := s.store.ListIdentityAccounts(ctx, ListIdentityAccountsInput{ListQuery: query})
	if err != nil {
		return IdentityAccountPage{}, mapIAMStoreError(operation, err)
	}
	for _, account := range page.Items {
		if account.ID == "" || !validIAMStatus(account.Status) {
			return IdentityAccountPage{}, coreError(operation, CodeFailedPrecondition, nil)
		}
	}
	page.Items = append([]IdentityAccount(nil), page.Items...)
	return page, nil
}

func (s *IAMService) GetIdentityAccount(ctx context.Context, accountID IdentityAccountID) (IdentityAccount, error) {
	const operation = "iam.get-account"
	accountID = IdentityAccountID(strings.TrimSpace(string(accountID)))
	if accountID == "" {
		return IdentityAccount{}, invalid(operation)
	}
	account, err := s.store.GetIdentityAccount(ctx, accountID)
	if err != nil {
		return IdentityAccount{}, mapIAMStoreError(operation, err)
	}
	if account.ID != accountID || !validIAMStatus(account.Status) {
		return IdentityAccount{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	return account, nil
}

type ProvisionTenantInput struct {
	TenantCode     string
	DisplayName    string
	OwnerAccountID IdentityAccountID
	OwnerUsername  string
	OwnerEmail     string
	OwnerName      string
}

func (s *IAMService) ProvisionTenant(ctx context.Context, input ProvisionTenantInput) (ProvisionTenantResult, error) {
	const operation = "iam.provision-tenant"
	if err := contextError(operation, ctx); err != nil {
		return ProvisionTenantResult{}, err
	}
	command := ProvisionTenantStoreInput{
		TenantCode: strings.TrimSpace(input.TenantCode), DisplayName: strings.TrimSpace(input.DisplayName),
		OwnerAccountID: input.OwnerAccountID, OwnerUsername: strings.TrimSpace(input.OwnerUsername),
		OwnerEmail: strings.TrimSpace(input.OwnerEmail), OwnerName: strings.TrimSpace(input.OwnerName),
	}
	if command.TenantCode == "" || command.DisplayName == "" || command.OwnerAccountID == "" {
		return ProvisionTenantResult{}, invalid(operation)
	}
	result, err := s.store.ProvisionTenant(ctx, command)
	if err != nil {
		return ProvisionTenantResult{}, mapIAMStoreError(operation, err)
	}
	if result.Tenant.ID == "" || result.Tenant.Code != command.TenantCode || result.Owner.TenantID != result.Tenant.ID || result.Owner.AccountID != command.OwnerAccountID {
		return ProvisionTenantResult{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	if err := s.reconcile(ctx, operation, PolicyReconcileInput{Kind: PolicyResourceTenant, TenantID: result.Tenant.ID, ResourceID: result.Tenant.ID}); err != nil {
		return ProvisionTenantResult{}, err
	}
	return result, nil
}

func (s *IAMService) ListTenants(ctx context.Context, input ListTenantsInput) (TenantPage, error) {
	const operation = "iam.list-tenants"
	query, err := normalizeListQuery(operation, input.ListQuery)
	if err != nil {
		return TenantPage{}, err
	}
	page, err := s.store.ListTenants(ctx, ListTenantsInput{ListQuery: query})
	if err != nil {
		return TenantPage{}, mapIAMStoreError(operation, err)
	}
	for _, tenant := range page.Items {
		if tenant.ID == "" || tenant.Code == "" || !validIAMStatus(tenant.Status) || tenant.Version == 0 {
			return TenantPage{}, coreError(operation, CodeFailedPrecondition, nil)
		}
	}
	page.Items = append([]Tenant(nil), page.Items...)
	return page, nil
}

func (s *IAMService) GetTenant(ctx context.Context, tenantID string) (Tenant, error) {
	const operation = "iam.get-tenant"
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return Tenant{}, invalid(operation)
	}
	tenant, err := s.store.GetTenant(ctx, tenantID)
	if err != nil {
		return Tenant{}, mapIAMStoreError(operation, err)
	}
	if tenant.ID != tenantID || tenant.Code == "" || !validIAMStatus(tenant.Status) || tenant.Version == 0 {
		return Tenant{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	return tenant, nil
}

type UpdateTenantInput struct {
	Actor           SystemActor
	TenantID        string
	DisplayName     string
	ExpectedVersion uint64
}

func (s *IAMService) UpdateTenant(ctx context.Context, input UpdateTenantInput) (Tenant, error) {
	const operation = "iam.update-tenant"
	if !input.Actor.System {
		return Tenant{}, coreError(operation, CodePermissionDenied, nil)
	}
	command := UpdateTenantStoreInput{TenantID: strings.TrimSpace(input.TenantID), DisplayName: strings.TrimSpace(input.DisplayName), ExpectedVersion: input.ExpectedVersion}
	if command.TenantID == "" || command.DisplayName == "" || command.ExpectedVersion == 0 {
		return Tenant{}, invalid(operation)
	}
	tenant, err := s.store.UpdateTenant(ctx, command)
	if err != nil {
		return Tenant{}, mapIAMStoreError(operation, err)
	}
	if tenant.ID != command.TenantID || tenant.DisplayName != command.DisplayName || tenant.Version == 0 {
		return Tenant{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	if err := s.reconcile(ctx, operation, PolicyReconcileInput{Kind: PolicyResourceTenant, TenantID: tenant.ID, ResourceID: tenant.ID}); err != nil {
		return Tenant{}, err
	}
	return tenant, nil
}

type SetTenantStatusInput struct {
	Actor           SystemActor
	TenantID        string
	Status          IAMStatus
	ExpectedVersion uint64
}

func (s *IAMService) SetTenantStatus(ctx context.Context, input SetTenantStatusInput) (Tenant, error) {
	const operation = "iam.set-tenant-status"
	if !input.Actor.System {
		return Tenant{}, coreError(operation, CodePermissionDenied, nil)
	}
	input.TenantID = strings.TrimSpace(input.TenantID)
	if input.TenantID == "" || !validIAMStatus(input.Status) || input.ExpectedVersion == 0 {
		return Tenant{}, invalid(operation)
	}
	tenant, err := s.store.SetTenantStatus(ctx, SetTenantStatusStoreInput{TenantID: input.TenantID, Status: input.Status, ExpectedVersion: input.ExpectedVersion})
	if err != nil {
		return Tenant{}, mapIAMStoreError(operation, err)
	}
	if tenant.ID != input.TenantID || tenant.Status != input.Status {
		return Tenant{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	if err := s.reconcile(ctx, operation, PolicyReconcileInput{Kind: PolicyResourceTenant, TenantID: input.TenantID, ResourceID: input.TenantID}); err != nil {
		return Tenant{}, err
	}
	return tenant, nil
}

func (s *IAMService) ListTenantMembers(ctx context.Context, input ListTenantMembersInput) (TenantMemberPage, error) {
	const operation = "iam.list-members"
	input.TenantID = strings.TrimSpace(input.TenantID)
	query, err := normalizeListQuery(operation, input.ListQuery)
	if err != nil || input.TenantID == "" {
		if err != nil {
			return TenantMemberPage{}, err
		}
		return TenantMemberPage{}, invalid(operation)
	}
	input.ListQuery = query
	page, err := s.store.ListTenantMembers(ctx, input)
	if err != nil {
		return TenantMemberPage{}, mapIAMStoreError(operation, err)
	}
	result := make([]TenantMember, len(page.Items))
	for index, member := range page.Items {
		if member.ID == "" || member.TenantID != input.TenantID || member.AccountID == "" || member.Version == 0 {
			return TenantMemberPage{}, coreError(operation, CodeFailedPrecondition, nil)
		}
		result[index] = cloneTenantMember(member)
	}
	page.Items = result
	return page, nil
}

func (s *IAMService) GetTenantMember(ctx context.Context, key TenantMemberKey) (TenantMember, error) {
	const operation = "iam.get-member"
	key, err := normalizeMemberKey(operation, key)
	if err != nil {
		return TenantMember{}, err
	}
	member, err := s.store.GetTenantMember(ctx, key)
	if err != nil {
		return TenantMember{}, mapIAMStoreError(operation, err)
	}
	if member.ID != key.MemberID || member.TenantID != key.TenantID {
		return TenantMember{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	return cloneTenantMember(member), nil
}

type SetTenantMemberStatusInput struct {
	TenantID        string
	MemberID        TenantMemberID
	Status          IAMStatus
	ExpectedVersion uint64
}

func (s *IAMService) SetTenantMemberStatus(ctx context.Context, input SetTenantMemberStatusInput) (TenantMember, error) {
	const operation = "iam.set-member-status"
	key, err := normalizeMemberKey(operation, TenantMemberKey{TenantID: input.TenantID, MemberID: input.MemberID})
	if err != nil || !validIAMStatus(input.Status) || input.ExpectedVersion == 0 {
		if err != nil {
			return TenantMember{}, err
		}
		return TenantMember{}, invalid(operation)
	}
	member, err := s.store.SetTenantMemberStatus(ctx, SetTenantMemberStatusStoreInput{
		Key: key, Status: input.Status, ExpectedVersion: input.ExpectedVersion, PreserveLastEnabledOwner: true,
	})
	if err != nil {
		return TenantMember{}, mapIAMStoreError(operation, err)
	}
	if member.ID != key.MemberID || member.TenantID != key.TenantID || member.Status != input.Status {
		return TenantMember{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	if err := s.reconcile(ctx, operation, PolicyReconcileInput{Kind: PolicyResourceMember, TenantID: key.TenantID, ResourceID: string(key.MemberID)}); err != nil {
		return TenantMember{}, err
	}
	return cloneTenantMember(member), nil
}

type ReplaceManualRolesInput struct {
	TenantID        string
	MemberID        TenantMemberID
	RoleCodes       []string
	ExpectedVersion uint64
}

func (s *IAMService) ReplaceManualRoles(ctx context.Context, input ReplaceManualRolesInput) (TenantMember, error) {
	const operation = "iam.replace-manual-roles"
	key, err := normalizeMemberKey(operation, TenantMemberKey{TenantID: input.TenantID, MemberID: input.MemberID})
	if err != nil || input.ExpectedVersion == 0 {
		if err != nil {
			return TenantMember{}, err
		}
		return TenantMember{}, invalid(operation)
	}
	roleCodes, err := canonicalStrings(input.RoleCodes)
	if err != nil {
		return TenantMember{}, invalid(operation)
	}
	current, err := s.store.GetTenantMember(ctx, key)
	if err != nil {
		return TenantMember{}, mapIAMStoreError(operation, err)
	}
	if current.ID != key.MemberID || current.TenantID != key.TenantID {
		return TenantMember{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	member, err := s.store.ReplaceManualRoleGrants(ctx, ReplaceManualRolesStoreInput{
		Key: key, RoleCodes: roleCodes, ExpectedVersion: input.ExpectedVersion, PreserveManagedGrants: true,
	})
	if err != nil {
		return TenantMember{}, mapIAMStoreError(operation, err)
	}
	if member.ID != key.MemberID || member.TenantID != key.TenantID {
		return TenantMember{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	if err := s.reconcile(ctx, operation, PolicyReconcileInput{Kind: PolicyResourceMember, TenantID: key.TenantID, ResourceID: string(key.MemberID)}); err != nil {
		return TenantMember{}, err
	}
	return cloneTenantMember(member), nil
}

type CreateTenantRoleInput struct{ TenantID, Code, DisplayName string }
type UpdateTenantRoleInput struct {
	TenantID        string
	RoleID          TenantRoleID
	DisplayName     string
	ExpectedVersion uint64
}
type SetTenantRoleStatusInput struct {
	TenantID        string
	RoleID          TenantRoleID
	Status          IAMStatus
	ExpectedVersion uint64
}
type ReplaceRolePermissionsInput struct {
	TenantID        string
	RoleID          TenantRoleID
	PermissionCodes []string
	ExpectedVersion uint64
}
type ReplaceRoleMenusInput struct {
	TenantID        string
	RoleID          TenantRoleID
	MenuCodes       []string
	ExpectedVersion uint64
}

func (s *IAMService) ListTenantRoles(ctx context.Context, input ListTenantRolesInput) (TenantRolePage, error) {
	const operation = "iam.list-roles"
	input.TenantID = strings.TrimSpace(input.TenantID)
	query, err := normalizeListQuery(operation, input.ListQuery)
	if err != nil || input.TenantID == "" {
		if err != nil {
			return TenantRolePage{}, err
		}
		return TenantRolePage{}, invalid(operation)
	}
	input.ListQuery = query
	page, err := s.store.ListTenantRoles(ctx, input)
	if err != nil {
		return TenantRolePage{}, mapIAMStoreError(operation, err)
	}
	result := make([]TenantRole, len(page.Items))
	for index, role := range page.Items {
		if role.ID == "" || role.TenantID != input.TenantID || role.Code == "" || role.Version == 0 {
			return TenantRolePage{}, coreError(operation, CodeFailedPrecondition, nil)
		}
		result[index] = cloneTenantRole(role)
	}
	page.Items = result
	return page, nil
}

func (s *IAMService) GetTenantRole(ctx context.Context, key TenantRoleKey) (TenantRole, error) {
	const operation = "iam.get-role"
	key, err := normalizeRoleKey(operation, key)
	if err != nil {
		return TenantRole{}, err
	}
	role, err := s.store.GetTenantRole(ctx, key)
	if err != nil {
		return TenantRole{}, mapIAMStoreError(operation, err)
	}
	if role.ID != key.RoleID || role.TenantID != key.TenantID || role.Code == "" || role.Version == 0 {
		return TenantRole{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	return cloneTenantRole(role), nil
}

func (s *IAMService) CreateTenantRole(ctx context.Context, input CreateTenantRoleInput) (TenantRole, error) {
	const operation = "iam.create-role"
	command := CreateTenantRoleStoreInput{TenantID: strings.TrimSpace(input.TenantID), Code: strings.TrimSpace(input.Code), DisplayName: strings.TrimSpace(input.DisplayName)}
	if command.TenantID == "" || !validCode(command.Code) || command.DisplayName == "" {
		return TenantRole{}, invalid(operation)
	}
	role, err := s.store.CreateTenantRole(ctx, command)
	if err != nil {
		return TenantRole{}, mapIAMStoreError(operation, err)
	}
	return s.finishRoleMutation(ctx, operation, command.TenantID, role)
}

func (s *IAMService) UpdateTenantRole(ctx context.Context, input UpdateTenantRoleInput) (TenantRole, error) {
	const operation = "iam.update-role"
	key, role, err := s.mutableRole(ctx, operation, input.TenantID, input.RoleID, input.ExpectedVersion)
	if err != nil {
		return TenantRole{}, err
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		return TenantRole{}, invalid(operation)
	}
	role, err = s.store.UpdateTenantRole(ctx, UpdateTenantRoleStoreInput{Key: key, DisplayName: displayName, ExpectedVersion: input.ExpectedVersion, RejectManaged: true})
	if err != nil {
		return TenantRole{}, mapIAMStoreError(operation, err)
	}
	return s.finishRoleMutation(ctx, operation, key.TenantID, role)
}

func (s *IAMService) SetTenantRoleStatus(ctx context.Context, input SetTenantRoleStatusInput) (TenantRole, error) {
	const operation = "iam.set-role-status"
	key, _, err := s.mutableRole(ctx, operation, input.TenantID, input.RoleID, input.ExpectedVersion)
	if err != nil {
		return TenantRole{}, err
	}
	if !validIAMStatus(input.Status) {
		return TenantRole{}, invalid(operation)
	}
	role, err := s.store.SetTenantRoleStatus(ctx, SetTenantRoleStatusStoreInput{Key: key, Status: input.Status, ExpectedVersion: input.ExpectedVersion, RejectManaged: true})
	if err != nil {
		return TenantRole{}, mapIAMStoreError(operation, err)
	}
	return s.finishRoleMutation(ctx, operation, key.TenantID, role)
}

func (s *IAMService) ReplaceRolePermissions(ctx context.Context, input ReplaceRolePermissionsInput) (TenantRole, error) {
	const operation = "iam.replace-role-permissions"
	key, _, err := s.mutableRole(ctx, operation, input.TenantID, input.RoleID, input.ExpectedVersion)
	if err != nil {
		return TenantRole{}, err
	}
	codes, err := canonicalStrings(input.PermissionCodes)
	if err != nil {
		return TenantRole{}, invalid(operation)
	}
	role, err := s.store.ReplaceRolePermissions(ctx, ReplaceRolePermissionsStoreInput{Key: key, PermissionCodes: codes, ExpectedVersion: input.ExpectedVersion, RejectManaged: true})
	if err != nil {
		return TenantRole{}, mapIAMStoreError(operation, err)
	}
	return s.finishRoleMutation(ctx, operation, key.TenantID, role)
}

func (s *IAMService) ReplaceRoleMenus(ctx context.Context, input ReplaceRoleMenusInput) (TenantRole, error) {
	const operation = "iam.replace-role-menus"
	key, _, err := s.mutableRole(ctx, operation, input.TenantID, input.RoleID, input.ExpectedVersion)
	if err != nil {
		return TenantRole{}, err
	}
	codes, err := canonicalStrings(input.MenuCodes)
	if err != nil {
		return TenantRole{}, invalid(operation)
	}
	role, err := s.store.ReplaceRoleMenus(ctx, ReplaceRoleMenusStoreInput{Key: key, MenuCodes: codes, ExpectedVersion: input.ExpectedVersion, RejectManaged: true})
	if err != nil {
		return TenantRole{}, mapIAMStoreError(operation, err)
	}
	return s.finishRoleMutation(ctx, operation, key.TenantID, role)
}

func (s *IAMService) ListMenus(ctx context.Context, input ListMenusInput) (MenuPage, error) {
	const operation = "iam.list-menus"
	query, err := normalizeListQuery(operation, input.ListQuery)
	if err != nil {
		return MenuPage{}, err
	}
	page, err := s.store.ListMenus(ctx, ListMenusInput{ListQuery: query})
	if err != nil {
		return MenuPage{}, mapIAMStoreError(operation, err)
	}
	for _, menu := range page.Items {
		if menu.Code == "" || !validIAMStatus(menu.Status) {
			return MenuPage{}, coreError(operation, CodeFailedPrecondition, nil)
		}
	}
	page.Items = append([]Menu(nil), page.Items...)
	return page, nil
}

func (s *IAMService) GetMenu(ctx context.Context, code string) (Menu, error) {
	const operation = "iam.get-menu"
	code = strings.TrimSpace(code)
	if !validCode(code) {
		return Menu{}, invalid(operation)
	}
	menu, err := s.store.GetMenu(ctx, code)
	if err != nil {
		return Menu{}, mapIAMStoreError(operation, err)
	}
	if menu.Code != code || !validIAMStatus(menu.Status) {
		return Menu{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	return menu, nil
}

func (s *IAMService) ListPermissions(ctx context.Context, input ListPermissionsInput) (PermissionPage, error) {
	const operation = "iam.list-permissions"
	query, err := normalizeListQuery(operation, input.ListQuery)
	if err != nil {
		return PermissionPage{}, err
	}
	page, err := s.store.ListPermissions(ctx, ListPermissionsInput{ListQuery: query})
	if err != nil {
		return PermissionPage{}, mapIAMStoreError(operation, err)
	}
	for _, permission := range page.Items {
		if permission.Code == "" || !validIAMStatus(permission.Status) {
			return PermissionPage{}, coreError(operation, CodeFailedPrecondition, nil)
		}
	}
	page.Items = append([]Permission(nil), page.Items...)
	return page, nil
}

func (s *IAMService) GetPermission(ctx context.Context, code string) (Permission, error) {
	const operation = "iam.get-permission"
	code = strings.TrimSpace(code)
	if !validCode(code) {
		return Permission{}, invalid(operation)
	}
	permission, err := s.store.GetPermission(ctx, code)
	if err != nil {
		return Permission{}, mapIAMStoreError(operation, err)
	}
	if permission.Code != code || !validIAMStatus(permission.Status) {
		return Permission{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	return permission, nil
}

type SetAccountStatusInput struct {
	Actor     SystemActor
	AccountID IdentityAccountID
	Status    IAMStatus
}
type ResetAccountPasswordInput struct {
	Actor     SystemActor
	AccountID IdentityAccountID
	Password  []byte
}

func (s *IAMService) SetAccountStatus(ctx context.Context, input SetAccountStatusInput) (IdentityAccount, error) {
	const operation = "iam.set-account-status"
	if !input.Actor.System {
		return IdentityAccount{}, coreError(operation, CodePermissionDenied, nil)
	}
	if input.AccountID == "" || !validIAMStatus(input.Status) {
		return IdentityAccount{}, invalid(operation)
	}
	account, err := s.store.SetIdentityAccountStatus(ctx, SetAccountStatusStoreInput{AccountID: input.AccountID, Status: input.Status, RevokeSessions: input.Status == IAMStatusDisabled})
	if err != nil {
		return IdentityAccount{}, mapIAMStoreError(operation, err)
	}
	if account.ID != input.AccountID || account.Status != input.Status {
		return IdentityAccount{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	if err := s.reconcile(ctx, operation, PolicyReconcileInput{Kind: PolicyResourceAccount, ResourceID: string(input.AccountID)}); err != nil {
		return IdentityAccount{}, err
	}
	return account, nil
}

func (s *IAMService) ResetAccountPassword(ctx context.Context, input ResetAccountPasswordInput) error {
	const operation = "iam.reset-account-password"
	if !input.Actor.System {
		return coreError(operation, CodePermissionDenied, nil)
	}
	if input.AccountID == "" || len(input.Password) == 0 {
		return invalid(operation)
	}
	hash, err := s.hasher.Hash(input.Password)
	if err != nil {
		return coreError(operation, CodeFailedPrecondition, err)
	}
	if err := s.store.ResetLocalPassword(ctx, ResetLocalPasswordStoreInput{AccountID: input.AccountID, PasswordHash: hash, RevokeSessions: true}); err != nil {
		return mapIAMStoreError(operation, err)
	}
	return s.reconcile(ctx, operation, PolicyReconcileInput{Kind: PolicyResourceAccount, ResourceID: string(input.AccountID)})
}

func (s *IAMService) mutableRole(ctx context.Context, operation, tenantID string, roleID TenantRoleID, version uint64) (TenantRoleKey, TenantRole, error) {
	key, err := normalizeRoleKey(operation, TenantRoleKey{TenantID: tenantID, RoleID: roleID})
	if err != nil {
		return TenantRoleKey{}, TenantRole{}, err
	}
	if version == 0 {
		return TenantRoleKey{}, TenantRole{}, invalid(operation)
	}
	role, err := s.store.GetTenantRole(ctx, key)
	if err != nil {
		return TenantRoleKey{}, TenantRole{}, mapIAMStoreError(operation, err)
	}
	if role.ID != key.RoleID || role.TenantID != key.TenantID {
		return TenantRoleKey{}, TenantRole{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	if role.Managed {
		return TenantRoleKey{}, TenantRole{}, coreError(operation, CodePermissionDenied, nil)
	}
	return key, role, nil
}

func (s *IAMService) finishRoleMutation(ctx context.Context, operation, tenantID string, role TenantRole) (TenantRole, error) {
	if role.ID == "" || role.TenantID != tenantID {
		return TenantRole{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	if err := s.reconcile(ctx, operation, PolicyReconcileInput{Kind: PolicyResourceRole, TenantID: tenantID, ResourceID: string(role.ID)}); err != nil {
		return TenantRole{}, err
	}
	return cloneTenantRole(role), nil
}

func (s *IAMService) reconcile(ctx context.Context, operation string, input PolicyReconcileInput) error {
	if err := s.reconciler.ReconcilePolicy(ctx, input); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return canceled(operation, err)
		}
		return coreError(operation, CodeCapabilityUnavailable, err)
	}
	return nil
}

func contextError(operation string, ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return canceled(operation, err)
	}
	return nil
}

func mapIAMStoreError(operation string, err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return canceled(operation, err)
	case errors.Is(err, ErrStoreNotFound):
		return coreError(operation, CodeNotFound, err)
	case errors.Is(err, ErrStoreInvalidInput):
		return coreError(operation, CodeInvalidInput, err)
	case errors.Is(err, ErrStoreConflict):
		return coreError(operation, CodeConflict, err)
	case errors.Is(err, ErrStoreConcurrentWrite):
		return coreError(operation, CodeConcurrentWrite, err)
	case errors.Is(err, ErrStorePermissionDenied):
		return coreError(operation, CodePermissionDenied, err)
	case errors.Is(err, ErrStoreFailedPrecondition), errors.Is(err, ErrStoreCredentialUnavailable):
		return coreError(operation, CodeFailedPrecondition, err)
	default:
		return storeFailure(operation, err)
	}
}

func normalizeMemberKey(operation string, key TenantMemberKey) (TenantMemberKey, error) {
	key.TenantID = strings.TrimSpace(key.TenantID)
	if key.TenantID == "" || key.MemberID == "" {
		return TenantMemberKey{}, invalid(operation)
	}
	return key, nil
}

func normalizeRoleKey(operation string, key TenantRoleKey) (TenantRoleKey, error) {
	key.TenantID = strings.TrimSpace(key.TenantID)
	if key.TenantID == "" || key.RoleID == "" {
		return TenantRoleKey{}, invalid(operation)
	}
	return key, nil
}

func validIAMStatus(status IAMStatus) bool {
	return status == IAMStatusEnabled || status == IAMStatusDisabled
}

func normalizeListQuery(operation string, query ListQuery) (ListQuery, error) {
	query.Keyword = strings.TrimSpace(query.Keyword)
	if query.Status != "" && !validIAMStatus(query.Status) {
		return ListQuery{}, invalid(operation)
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.Limit > 200 {
		return ListQuery{}, invalid(operation)
	}
	return query, nil
}

func canonicalStrings(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !validCode(value) {
			return nil, errors.New("empty value")
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func validCode(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range []byte(value) {
		letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		digit := character >= '0' && character <= '9'
		if !letter && !digit && (index == 0 || character != '.' && character != '_' && character != ':' && character != '-') {
			return false
		}
	}
	return true
}

func cloneTenantMember(member TenantMember) TenantMember {
	member.ManualRoleCodes = append([]string(nil), member.ManualRoleCodes...)
	return member
}

func cloneTenantRole(role TenantRole) TenantRole {
	role.PermissionCodes = append([]string(nil), role.PermissionCodes...)
	role.MenuCodes = append([]string(nil), role.MenuCodes...)
	return role
}

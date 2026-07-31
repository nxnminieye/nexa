package coreapp

import (
	"context"
	"errors"
	"time"
)

var (
	ErrStoreNotFound              = errors.New("core store: not found")
	ErrStoreInvalidInput          = errors.New("core store: invalid input")
	ErrStoreConflict              = errors.New("core store: conflict")
	ErrStoreConcurrentWrite       = errors.New("core store: concurrent write")
	ErrStorePermissionDenied      = errors.New("core store: permission denied")
	ErrStoreFailedPrecondition    = errors.New("core store: failed precondition")
	ErrStoreCredentialUnavailable = errors.New("core store: credential unavailable")
)

type IdentityAccountID string
type TenantMemberID string
type SessionID string
type RefreshToken string
type TenantRoleID string

type IdentityAccount struct {
	ID              IdentityAccountID
	SourceCode      string
	ExternalSubject string
	Username        string
	Email           string
	DisplayName     string
	Status          IAMStatus
}

type LocalCredential struct {
	Account      IdentityAccount
	PasswordHash string
}

type TenantMember struct {
	ID                     TenantMemberID
	TenantID               string
	TenantCode             string
	AccountID              IdentityAccountID
	AccountUsername        string
	AccountEmail           string
	AccountDisplayName     string
	AccountSourceCode      string
	AccountExternalSubject string
	Status                 IAMStatus
	ManualRoleCodes        []string
	ManagedOwnerGrant      bool
	Version                uint64
}

type ListQuery struct {
	Keyword string
	Status  IAMStatus
	Limit   uint32
	Offset  uint32
}

type ListIdentityAccountsInput struct{ ListQuery }
type IdentityAccountPage struct {
	Total uint64
	Items []IdentityAccount
}

type LocalAccountKey struct {
	Tenant   string
	Username string
}

type CreateLocalAccountInput struct {
	Tenant       string
	Username     string
	Email        string
	DisplayName  string
	PasswordHash string
}

type ExternalIdentityKey struct {
	SourceCode      string
	ExternalSubject string
}

type ReplaceExternalRoleGrantsInput struct {
	Tenant     string
	MemberID   TenantMemberID
	SourceCode string
	RoleCodes  []string
}

type StoredSession struct {
	ID               SessionID
	AccountID        IdentityAccountID
	Tenant           string
	AccessTokenHash  string
	RefreshTokenHash string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	Revoked          bool
}

type RotateSessionInput struct {
	PreviousID          SessionID
	PreviousRefreshHash string
	Replacement         StoredSession
}

type LocalAccountStore interface {
	CreateLocalAccount(context.Context, CreateLocalAccountInput) (IdentityAccount, error)
	FindLocalAccount(context.Context, LocalAccountKey) (LocalCredential, error)
}

type SessionStore interface {
	SessionCreator
	FindSessionByRefreshHash(context.Context, string) (StoredSession, error)
	RotateSession(context.Context, RotateSessionInput) error
	RevokeSession(context.Context, SessionID) error
}

type SessionCreator interface {
	CreateSession(context.Context, StoredSession) error
}

type IdentityStore interface {
	LocalAccountStore
	SessionStore
}

type AccessPrincipal struct {
	SessionID       SessionID
	TenantID        string
	TenantCode      string
	MemberID        TenantMemberID
	Account         IdentityAccount
	RoleCodes       []string
	PermissionCodes []string
	MenuCodes       []string
}

type AccessSessionStore interface {
	FindAccessPrincipal(context.Context, string, time.Time) (AccessPrincipal, error)
}

type ExternalIdentityLookup interface {
	FindExternalAccount(context.Context, ExternalIdentityKey) (IdentityAccount, error)
}

type ExternalRoleGrantStore interface {
	// ReplaceExternalRoleGrants replaces only grants owned by the input source.
	// An empty RoleCodes removes that source's grants without changing local,
	// manual, or other provider grants.
	ReplaceExternalRoleGrants(context.Context, ReplaceExternalRoleGrantsInput) error
}

type ProvisionTenantStoreInput struct {
	TenantCode     string
	DisplayName    string
	DefaultRouter  string
	OwnerAccountID IdentityAccountID
	OwnerUsername  string
	OwnerEmail     string
	OwnerName      string
}

type ProvisionTenantResult struct {
	Tenant Tenant
	Owner  TenantMember
}

type TenantMemberKey struct {
	TenantID string
	MemberID TenantMemberID
}

type ListTenantMembersInput struct {
	TenantID string
	ListQuery
}

type TenantMemberPage struct {
	Total uint64
	Items []TenantMember
}

type ListTenantsInput struct{ ListQuery }
type TenantPage struct {
	Total uint64
	Items []Tenant
}

type UpdateTenantStoreInput struct {
	TenantID        string
	DisplayName     string
	ExpectedVersion uint64
}

type SetTenantMemberStatusStoreInput struct {
	Key                      TenantMemberKey
	Status                   IAMStatus
	ExpectedVersion          uint64
	PreserveLastEnabledOwner bool
}

type ReplaceManualRolesStoreInput struct {
	Key                   TenantMemberKey
	RoleCodes             []string
	ExpectedVersion       uint64
	PreserveManagedGrants bool
}

type SetTenantStatusStoreInput struct {
	TenantID        string
	Status          IAMStatus
	ExpectedVersion uint64
}

type TenantRoleKey struct {
	TenantID string
	RoleID   TenantRoleID
}

type ListTenantRolesInput struct {
	TenantID string
	ListQuery
}

type TenantRolePage struct {
	Total uint64
	Items []TenantRole
}

type ListMenusInput struct{ ListQuery }
type MenuPage struct {
	Total uint64
	Items []Menu
}

type ListPermissionsInput struct{ ListQuery }
type PermissionPage struct {
	Total uint64
	Items []Permission
}

type CreateTenantRoleStoreInput struct {
	TenantID    string
	Code        string
	DisplayName string
}

type UpdateTenantRoleStoreInput struct {
	Key             TenantRoleKey
	DisplayName     string
	ExpectedVersion uint64
	RejectManaged   bool
}

type SetTenantRoleStatusStoreInput struct {
	Key             TenantRoleKey
	Status          IAMStatus
	ExpectedVersion uint64
	RejectManaged   bool
}

type ReplaceRolePermissionsStoreInput struct {
	Key             TenantRoleKey
	PermissionCodes []string
	ExpectedVersion uint64
	RejectManaged   bool
}

type ReplaceRoleMenusStoreInput struct {
	Key             TenantRoleKey
	MenuCodes       []string
	ExpectedVersion uint64
	RejectManaged   bool
}

type SetAccountStatusStoreInput struct {
	AccountID      IdentityAccountID
	Status         IAMStatus
	RevokeSessions bool
}

type ResetLocalPasswordStoreInput struct {
	AccountID      IdentityAccountID
	PasswordHash   string
	RevokeSessions bool
}

// IAMStore owns atomic persistence and race-sensitive guards. In particular,
// status and grant methods must enforce the boolean guard carried by each
// command in the same transaction as the mutation.
type IAMStore interface {
	ListIdentityAccounts(context.Context, ListIdentityAccountsInput) (IdentityAccountPage, error)
	GetIdentityAccount(context.Context, IdentityAccountID) (IdentityAccount, error)
	ProvisionTenant(context.Context, ProvisionTenantStoreInput) (ProvisionTenantResult, error)
	ListTenants(context.Context, ListTenantsInput) (TenantPage, error)
	GetTenant(context.Context, string) (Tenant, error)
	UpdateTenant(context.Context, UpdateTenantStoreInput) (Tenant, error)
	SetTenantStatus(context.Context, SetTenantStatusStoreInput) (Tenant, error)
	ListTenantMembers(context.Context, ListTenantMembersInput) (TenantMemberPage, error)
	GetTenantMember(context.Context, TenantMemberKey) (TenantMember, error)
	SetTenantMemberStatus(context.Context, SetTenantMemberStatusStoreInput) (TenantMember, error)
	ReplaceManualRoleGrants(context.Context, ReplaceManualRolesStoreInput) (TenantMember, error)
	ListTenantRoles(context.Context, ListTenantRolesInput) (TenantRolePage, error)
	GetTenantRole(context.Context, TenantRoleKey) (TenantRole, error)
	CreateTenantRole(context.Context, CreateTenantRoleStoreInput) (TenantRole, error)
	UpdateTenantRole(context.Context, UpdateTenantRoleStoreInput) (TenantRole, error)
	SetTenantRoleStatus(context.Context, SetTenantRoleStatusStoreInput) (TenantRole, error)
	ReplaceRolePermissions(context.Context, ReplaceRolePermissionsStoreInput) (TenantRole, error)
	ReplaceRoleMenus(context.Context, ReplaceRoleMenusStoreInput) (TenantRole, error)
	ListMenus(context.Context, ListMenusInput) (MenuPage, error)
	GetMenu(context.Context, string) (Menu, error)
	ListPermissions(context.Context, ListPermissionsInput) (PermissionPage, error)
	GetPermission(context.Context, string) (Permission, error)
	SetIdentityAccountStatus(context.Context, SetAccountStatusStoreInput) (IdentityAccount, error)
	ResetLocalPassword(context.Context, ResetLocalPasswordStoreInput) error
}

type CatalogStore interface {
	SyncCatalog(context.Context, CatalogSyncStoreInput) (CatalogSyncResult, error)
}

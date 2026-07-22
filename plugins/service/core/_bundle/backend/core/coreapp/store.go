package coreapp

import (
	"context"
	"errors"
	"time"
)

var (
	ErrStoreNotFound = errors.New("core store: not found")
	ErrStoreConflict = errors.New("core store: conflict")
)

type IdentityAccountID string
type TenantMemberID string
type SessionID string
type RefreshToken string

type IdentityAccount struct {
	ID              IdentityAccountID
	SourceCode      string
	ExternalSubject string
	Username        string
	Email           string
	DisplayName     string
}

type LocalCredential struct {
	Account      IdentityAccount
	PasswordHash string
}

type TenantMember struct {
	ID        TenantMemberID
	Tenant    string
	AccountID IdentityAccountID
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
	RoleRefs   []string
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

type ExternalIdentityLookup interface {
	FindExternalAccount(context.Context, ExternalIdentityKey) (IdentityAccount, error)
}

type ExternalRoleGrantStore interface {
	// ReplaceExternalRoleGrants replaces only grants owned by the input source.
	// An empty RoleRefs removes that source's grants without changing local,
	// manual, or other provider grants.
	ReplaceExternalRoleGrants(context.Context, ReplaceExternalRoleGrantsInput) error
}

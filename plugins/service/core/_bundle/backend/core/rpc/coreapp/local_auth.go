package coreapp

import (
	"context"
	"errors"
	"strings"
)

type LocalRegistration struct {
	Tenant      string
	Username    string
	Password    []byte
	Email       string
	DisplayName string
}

type LocalLogin struct {
	Tenant   string
	Username string
	Password []byte
}

type LocalAuthenticator struct {
	store   IdentityStore
	hasher  PasswordHasher
	options SessionOptions
}

func NewLocalAuthenticator(store IdentityStore, hasher PasswordHasher, options SessionOptions) (*LocalAuthenticator, error) {
	if interfaceNil(store) || interfaceNil(hasher) {
		return nil, invalid("local-auth.new")
	}
	if err := validateSessionOptions(options); err != nil {
		return nil, err
	}
	return &LocalAuthenticator{store: store, hasher: hasher, options: options}, nil
}

func (a *LocalAuthenticator) Register(ctx context.Context, registration LocalRegistration) (IdentityAccount, error) {
	const operation = "local-auth.register"
	if err := ctx.Err(); err != nil {
		return IdentityAccount{}, canceled(operation, err)
	}
	registration.Tenant = strings.TrimSpace(registration.Tenant)
	registration.Username = strings.TrimSpace(registration.Username)
	if registration.Tenant == "" || registration.Username == "" || len(registration.Password) == 0 {
		return IdentityAccount{}, invalid(operation)
	}
	hash, err := a.hasher.Hash(registration.Password)
	if err != nil {
		return IdentityAccount{}, storeFailure(operation, err)
	}
	account, err := a.store.CreateLocalAccount(ctx, CreateLocalAccountInput{
		Tenant: registration.Tenant, Username: registration.Username, Email: strings.TrimSpace(registration.Email),
		DisplayName: strings.TrimSpace(registration.DisplayName), PasswordHash: hash,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return IdentityAccount{}, canceled(operation, err)
		}
		if errors.Is(err, ErrStoreConflict) {
			return IdentityAccount{}, coreError(operation, CodeConflict, err)
		}
		return IdentityAccount{}, storeFailure(operation, err)
	}
	return account, nil
}

func (a *LocalAuthenticator) Login(ctx context.Context, login LocalLogin) (Session, error) {
	const operation = "local-auth.login"
	if err := ctx.Err(); err != nil {
		return Session{}, canceled(operation, err)
	}
	login.Tenant = strings.TrimSpace(login.Tenant)
	login.Username = strings.TrimSpace(login.Username)
	if login.Tenant == "" || login.Username == "" || len(login.Password) == 0 {
		return Session{}, coreError(operation, CodeInvalidCredentials, nil)
	}
	credential, err := a.store.FindLocalAccount(ctx, LocalAccountKey{Tenant: login.Tenant, Username: login.Username})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Session{}, canceled(operation, err)
		}
		if errors.Is(err, ErrStoreNotFound) || errors.Is(err, ErrStoreCredentialUnavailable) {
			return Session{}, coreError(operation, CodeInvalidCredentials, err)
		}
		return Session{}, storeFailure(operation, err)
	}
	if err := a.hasher.Verify(credential.PasswordHash, login.Password); err != nil {
		return Session{}, coreError(operation, CodeInvalidCredentials, err)
	}
	session, stored, err := issueSession(credential.Account, login.Tenant, a.options)
	if err != nil {
		return Session{}, err
	}
	if err := a.store.CreateSession(ctx, stored); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Session{}, canceled(operation, err)
		}
		if errors.Is(err, ErrStoreCredentialUnavailable) {
			return Session{}, coreError(operation, CodeInvalidCredentials, err)
		}
		return Session{}, storeFailure(operation, err)
	}
	return session, nil
}

func (a *LocalAuthenticator) Refresh(ctx context.Context, refresh RefreshToken) (Session, error) {
	const operation = "local-auth.refresh"
	if err := ctx.Err(); err != nil {
		return Session{}, canceled(operation, err)
	}
	if refresh == "" {
		return Session{}, coreError(operation, CodeSessionReplayed, nil)
	}
	hash := tokenHash(string(refresh))
	current, err := a.store.FindSessionByRefreshHash(ctx, hash)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Session{}, canceled(operation, err)
		}
		if errors.Is(err, ErrStoreNotFound) {
			return Session{}, coreError(operation, CodeSessionReplayed, err)
		}
		if errors.Is(err, ErrStoreCredentialUnavailable) {
			return Session{}, coreError(operation, CodeInvalidCredentials, err)
		}
		return Session{}, storeFailure(operation, err)
	}
	if current.Revoked {
		return Session{}, coreError(operation, CodeSessionReplayed, nil)
	}
	if !a.options.Clock.Now().Before(current.RefreshExpiresAt) {
		return Session{}, coreError(operation, CodeSessionExpired, nil)
	}
	account := IdentityAccount{ID: current.AccountID}
	replacement, stored, err := issueSession(account, current.Tenant, a.options)
	if err != nil {
		return Session{}, err
	}
	if err := a.store.RotateSession(ctx, RotateSessionInput{PreviousID: current.ID, PreviousRefreshHash: hash, Replacement: stored}); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Session{}, canceled(operation, err)
		}
		if errors.Is(err, ErrStoreConflict) || errors.Is(err, ErrStoreNotFound) {
			return Session{}, coreError(operation, CodeSessionReplayed, err)
		}
		if errors.Is(err, ErrStoreCredentialUnavailable) {
			return Session{}, coreError(operation, CodeInvalidCredentials, err)
		}
		return Session{}, storeFailure(operation, err)
	}
	return replacement, nil
}

func (a *LocalAuthenticator) Revoke(ctx context.Context, sessionID SessionID) error {
	const operation = "local-auth.revoke"
	if err := ctx.Err(); err != nil {
		return canceled(operation, err)
	}
	if sessionID == "" {
		return invalid(operation)
	}
	if err := a.store.RevokeSession(ctx, sessionID); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return canceled(operation, err)
		}
		if errors.Is(err, ErrStoreNotFound) {
			return coreError(operation, CodeSessionReplayed, err)
		}
		return storeFailure(operation, err)
	}
	return nil
}

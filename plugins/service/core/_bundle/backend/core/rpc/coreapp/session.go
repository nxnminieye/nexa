package coreapp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type Clock interface {
	Now() time.Time
}

type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

type SessionOptions struct {
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	TokenBytes uint32
	Clock      Clock
}

type Session struct {
	ID               SessionID
	AccessToken      string
	RefreshToken     RefreshToken
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

type IssueSessionInput struct {
	Account IdentityAccount
	Tenant  string
}

type SessionIssuer interface {
	Issue(context.Context, IssueSessionInput) (Session, error)
}

type defaultSessionIssuer struct {
	store   SessionCreator
	options SessionOptions
}

func NewDefaultSessionIssuer(store SessionCreator, options SessionOptions) (SessionIssuer, error) {
	if interfaceNil(store) {
		return nil, invalid("session-issuer.new")
	}
	if err := validateSessionOptions(options); err != nil {
		return nil, err
	}
	return &defaultSessionIssuer{store: store, options: options}, nil
}

func (i *defaultSessionIssuer) Issue(ctx context.Context, input IssueSessionInput) (Session, error) {
	const operation = "session-issuer.issue"
	if err := ctx.Err(); err != nil {
		return Session{}, canceled(operation, err)
	}
	input.Tenant = strings.TrimSpace(input.Tenant)
	if input.Account.ID == "" || input.Tenant == "" {
		return Session{}, invalid(operation)
	}
	session, stored, err := issueSession(input.Account, input.Tenant, i.options)
	if err != nil {
		return Session{}, err
	}
	if err := i.store.CreateSession(ctx, stored); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Session{}, canceled(operation, err)
		}
		return Session{}, storeFailure(operation, err)
	}
	return session, nil
}

func validateSessionOptions(options SessionOptions) error {
	if options.AccessTTL <= 0 || options.RefreshTTL <= options.AccessTTL || options.TokenBytes < 16 || options.TokenBytes > 128 || interfaceNil(options.Clock) {
		return invalid("session.options")
	}
	return nil
}

func issueSession(account IdentityAccount, tenant string, options SessionOptions) (Session, StoredSession, error) {
	id, err := randomToken(options.TokenBytes)
	if err != nil {
		return Session{}, StoredSession{}, coreError("session.issue", CodeStoreFailure, err)
	}
	access, err := randomToken(options.TokenBytes)
	if err != nil {
		return Session{}, StoredSession{}, coreError("session.issue", CodeStoreFailure, err)
	}
	refresh, err := randomToken(options.TokenBytes)
	if err != nil {
		return Session{}, StoredSession{}, coreError("session.issue", CodeStoreFailure, err)
	}
	now := options.Clock.Now()
	session := Session{
		ID: SessionID(id), AccessToken: access, RefreshToken: RefreshToken(refresh),
		AccessExpiresAt: now.Add(options.AccessTTL), RefreshExpiresAt: now.Add(options.RefreshTTL),
	}
	stored := StoredSession{
		ID: session.ID, AccountID: account.ID, Tenant: tenant,
		AccessTokenHash: tokenHash(access), RefreshTokenHash: tokenHash(refresh),
		AccessExpiresAt: session.AccessExpiresAt, RefreshExpiresAt: session.RefreshExpiresAt,
	}
	return session, stored, nil
}

func randomToken(size uint32) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func tokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

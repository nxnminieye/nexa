package coreapp

import (
	"context"
	"errors"
	"sort"
)

type AccessAuthenticator struct {
	store AccessSessionStore
	clock Clock
}

func NewAccessAuthenticator(store AccessSessionStore, clock Clock) (*AccessAuthenticator, error) {
	if interfaceNil(store) || interfaceNil(clock) {
		return nil, invalid("access-auth.new")
	}
	return &AccessAuthenticator{store: store, clock: clock}, nil
}

func (a *AccessAuthenticator) Authenticate(ctx context.Context, opaqueToken string) (AccessPrincipal, error) {
	const operation = "access-auth.authenticate"
	if err := ctx.Err(); err != nil {
		return AccessPrincipal{}, canceled(operation, err)
	}
	if opaqueToken == "" {
		return AccessPrincipal{}, coreError(operation, CodeInvalidCredentials, nil)
	}
	principal, err := a.store.FindAccessPrincipal(ctx, tokenHash(opaqueToken), a.clock.Now())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return AccessPrincipal{}, canceled(operation, err)
		}
		if errors.Is(err, ErrStoreNotFound) || errors.Is(err, ErrStoreCredentialUnavailable) {
			return AccessPrincipal{}, coreError(operation, CodeInvalidCredentials, nil)
		}
		return AccessPrincipal{}, storeFailure(operation, err)
	}
	if principal.SessionID == "" || principal.TenantID == "" || principal.TenantCode == "" ||
		principal.MemberID == "" || principal.Account.ID == "" {
		return AccessPrincipal{}, storeFailure(operation, nil)
	}
	var valid bool
	principal.RoleCodes, valid = canonicalAccessCodes(principal.RoleCodes)
	if !valid {
		return AccessPrincipal{}, storeFailure(operation, nil)
	}
	principal.PermissionCodes, valid = canonicalAccessCodes(principal.PermissionCodes)
	if !valid {
		return AccessPrincipal{}, storeFailure(operation, nil)
	}
	principal.MenuCodes, valid = canonicalAccessCodes(principal.MenuCodes)
	if !valid {
		return AccessPrincipal{}, storeFailure(operation, nil)
	}
	return principal, nil
}

func canonicalAccessCodes(values []string) ([]string, bool) {
	result := append([]string(nil), values...)
	for _, value := range result {
		if value == "" {
			return nil, false
		}
	}
	sort.Strings(result)
	if len(result) == 0 {
		return result, true
	}
	write := 1
	for read := 1; read < len(result); read++ {
		if result[read] == result[write-1] {
			continue
		}
		result[write] = result[read]
		write++
	}
	return result[:write], true
}

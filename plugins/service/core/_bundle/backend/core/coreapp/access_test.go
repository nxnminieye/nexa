package coreapp

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type accessStoreFunc func(context.Context, string, time.Time) (AccessPrincipal, error)

func (f accessStoreFunc) FindAccessPrincipal(ctx context.Context, hash string, now time.Time) (AccessPrincipal, error) {
	return f(ctx, hash, now)
}

func TestAccessAuthenticatorHashesTokenReadsClockOnceAndCanonicalizesCodes(t *testing.T) {
	now := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	clockCalls := 0
	roles := []string{"writer", "reader", "writer"}
	permissions := []string{"write", "read", "write"}
	menus := []string{"settings", "home", "settings"}
	store := accessStoreFunc(func(_ context.Context, hash string, gotNow time.Time) (AccessPrincipal, error) {
		if hash == "opaque-access-token" || hash != tokenHash("opaque-access-token") {
			t.Fatalf("store hash = %q", hash)
		}
		if !gotNow.Equal(now) {
			t.Fatalf("store now = %v", gotNow)
		}
		return AccessPrincipal{
			SessionID: "session-1", TenantID: "7", TenantCode: "tenant-a", MemberID: "9",
			Account:   IdentityAccount{ID: "11", Username: "owner", Status: IAMStatusEnabled},
			RoleCodes: roles, PermissionCodes: permissions, MenuCodes: menus,
		}, nil
	})
	auth, err := NewAccessAuthenticator(store, ClockFunc(func() time.Time {
		clockCalls++
		return now
	}))
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.Authenticate(context.Background(), "opaque-access-token")
	if err != nil {
		t.Fatal(err)
	}
	if clockCalls != 1 {
		t.Fatalf("clock calls = %d", clockCalls)
	}
	if !reflect.DeepEqual(principal.RoleCodes, []string{"reader", "writer"}) ||
		!reflect.DeepEqual(principal.PermissionCodes, []string{"read", "write"}) ||
		!reflect.DeepEqual(principal.MenuCodes, []string{"home", "settings"}) {
		t.Fatalf("principal codes = %#v %#v %#v", principal.RoleCodes, principal.PermissionCodes, principal.MenuCodes)
	}
	roles[0], permissions[0], menus[0] = "changed", "changed", "changed"
	if principal.RoleCodes[0] != "reader" || principal.PermissionCodes[0] != "read" || principal.MenuCodes[0] != "home" {
		t.Fatal("principal retained store-owned slices")
	}
}

func TestAccessAuthenticatorErrorProjection(t *testing.T) {
	tests := []struct {
		name  string
		token string
		err   error
		ctx   func() context.Context
		want  ErrorCode
	}{
		{name: "empty", want: CodeInvalidCredentials},
		{name: "not-found", token: "unknown", err: ErrStoreNotFound, want: CodeInvalidCredentials},
		{name: "expired", token: "expired", err: ErrStoreCredentialUnavailable, want: CodeInvalidCredentials},
		{name: "revoked", token: "revoked", err: ErrStoreCredentialUnavailable, want: CodeInvalidCredentials},
		{name: "disabled", token: "disabled", err: ErrStoreCredentialUnavailable, want: CodeInvalidCredentials},
		{name: "store-failure", token: "token", err: errors.New("database details"), want: CodeStoreFailure},
		{name: "canceled-by-store", token: "token", err: context.Canceled, want: CodeCanceled},
		{name: "deadline-by-store", token: "token", err: context.DeadlineExceeded, want: CodeCanceled},
		{name: "canceled-before-store", token: "token", ctx: canceledContext, want: CodeCanceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			auth, err := NewAccessAuthenticator(accessStoreFunc(func(context.Context, string, time.Time) (AccessPrincipal, error) {
				calls++
				return AccessPrincipal{}, test.err
			}), ClockFunc(time.Now))
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if test.ctx != nil {
				ctx = test.ctx()
			}
			_, err = auth.Authenticate(ctx, test.token)
			if CodeOf(err) != test.want {
				t.Fatalf("code = %q, want %q, err=%v", CodeOf(err), test.want, err)
			}
			if (test.token == "" || test.ctx != nil) && calls != 0 {
				t.Fatalf("store calls = %d", calls)
			}
			if err != nil && (test.token != "" && (err.Error() == test.token || err.Error() == tokenHash(test.token))) {
				t.Fatalf("error exposed credential: %v", err)
			}
		})
	}
}

func TestAccessAuthenticatorRejectsIncompletePrincipal(t *testing.T) {
	auth, err := NewAccessAuthenticator(accessStoreFunc(func(context.Context, string, time.Time) (AccessPrincipal, error) {
		return AccessPrincipal{SessionID: "session", TenantID: "tenant", TenantCode: "code", MemberID: "member"}, nil
	}), ClockFunc(time.Now))
	if err != nil {
		t.Fatal(err)
	}
	_, err = auth.Authenticate(context.Background(), "token")
	if CodeOf(err) != CodeStoreFailure {
		t.Fatalf("code = %q, err=%v", CodeOf(err), err)
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

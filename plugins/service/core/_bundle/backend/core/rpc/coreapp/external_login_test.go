package coreapp

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestExternalLoginRequeriesExactBindingAfterUnmatchedPolicy(t *testing.T) {
	lookup := newExternalLookup()
	var policyCalls int
	policy := unmatchedIdentityPolicyFunc(func(_ context.Context, input UnmatchedIdentityInput) error {
		policyCalls++
		lookup.bind(input.Identity, IdentityAccount{ID: "account-bound", Username: "alice"})
		return nil
	})
	service := newExternalLoginTestService(t, lookup, policy,
		tenantAdmissionPolicyFunc(func(_ context.Context, input TenantAdmissionInput) (TenantMember, error) {
			return TenantMember{ID: "member-a", TenantCode: input.Tenant, AccountID: input.Account.ID}, nil
		}),
		externalRoleMapperFunc(func(context.Context, ExternalRoleMappingInput) ([]string, error) { return nil, nil }),
		newExternalGrantStore(),
		&recordingSessionIssuer{},
	)

	result, err := service.Login(context.Background(), "oidc-a", ExchangeInput{Tenant: "tenant-a", Code: "code"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Account.ID != "account-bound" || policyCalls != 1 || lookup.calls != 2 {
		t.Fatalf("result=%#v policy_calls=%d lookup_calls=%d", result, policyCalls, lookup.calls)
	}
}

func TestExternalLoginAutoProvisionHintDoesNotBypassPolicy(t *testing.T) {
	lookup := newExternalLookup()
	var policyCalls int
	policy := unmatchedIdentityPolicyFunc(func(_ context.Context, input UnmatchedIdentityInput) error {
		policyCalls++
		if len(input.Identity.CandidateSubjects) != 1 || input.Identity.CandidateSubjects[0] != "legacy-subject" {
			t.Fatalf("candidate hints = %#v", input.Identity.CandidateSubjects)
		}
		return ErrIdentityRejected
	})
	service := newExternalLoginTestService(t, lookup, policy,
		tenantAdmissionPolicyFunc(func(context.Context, TenantAdmissionInput) (TenantMember, error) {
			t.Fatal("tenant admission must not run after rejected identity")
			return TenantMember{}, nil
		}),
		externalRoleMapperFunc(func(context.Context, ExternalRoleMappingInput) ([]string, error) {
			t.Fatal("role mapper must not run after rejected identity")
			return nil, nil
		}),
		newExternalGrantStore(),
		&recordingSessionIssuer{},
	)

	_, err := service.Login(context.Background(), "oidc-a", ExchangeInput{Tenant: "tenant-a", Code: "code"})
	assertCode(t, err, CodeInvalidCredentials)
	if policyCalls != 1 || lookup.calls != 1 {
		t.Fatalf("policy_calls=%d lookup_calls=%d", policyCalls, lookup.calls)
	}
}

func TestExternalLoginRejectsPolicySuccessWithoutExactBinding(t *testing.T) {
	lookup := newExternalLookup()
	service := newExternalLoginTestService(t, lookup,
		unmatchedIdentityPolicyFunc(func(context.Context, UnmatchedIdentityInput) error { return nil }),
		tenantAdmissionPolicyFunc(func(context.Context, TenantAdmissionInput) (TenantMember, error) {
			t.Fatal("tenant admission must not run without a verified binding")
			return TenantMember{}, nil
		}),
		externalRoleMapperFunc(func(context.Context, ExternalRoleMappingInput) ([]string, error) {
			t.Fatal("role mapper must not run without a verified binding")
			return nil, nil
		}),
		newExternalGrantStore(),
		&recordingSessionIssuer{},
	)

	_, err := service.Login(context.Background(), "oidc-a", ExchangeInput{Tenant: "tenant-a", Code: "code"})
	assertCode(t, err, CodeInvalidCredentials)
	if lookup.calls != 2 {
		t.Fatalf("exact lookup calls = %d", lookup.calls)
	}
}

func TestExternalLoginRejectsUnverifiedAccountAndMember(t *testing.T) {
	tests := []struct {
		name      string
		account   IdentityAccount
		admission TenantAdmissionPolicy
	}{
		{
			name:    "empty account id",
			account: IdentityAccount{},
			admission: tenantAdmissionPolicyFunc(func(context.Context, TenantAdmissionInput) (TenantMember, error) {
				t.Fatal("admission must not run for an empty account id")
				return TenantMember{}, nil
			}),
		},
		{
			name:    "empty member id",
			account: IdentityAccount{ID: "account-a"},
			admission: tenantAdmissionPolicyFunc(func(_ context.Context, input TenantAdmissionInput) (TenantMember, error) {
				return TenantMember{TenantCode: input.Tenant, AccountID: input.Account.ID}, nil
			}),
		},
		{
			name:    "member tenant mismatch",
			account: IdentityAccount{ID: "account-a"},
			admission: tenantAdmissionPolicyFunc(func(_ context.Context, input TenantAdmissionInput) (TenantMember, error) {
				return TenantMember{ID: "member-a", TenantCode: "tenant-b", AccountID: input.Account.ID}, nil
			}),
		},
		{
			name:    "member account mismatch",
			account: IdentityAccount{ID: "account-a"},
			admission: tenantAdmissionPolicyFunc(func(_ context.Context, input TenantAdmissionInput) (TenantMember, error) {
				return TenantMember{ID: "member-a", TenantCode: input.Tenant, AccountID: "account-b"}, nil
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookup := newExternalLookup()
			lookup.bind(externalTestIdentity(), test.account)
			mapperCalls := 0
			grants := newExternalGrantStore()
			issuer := &recordingSessionIssuer{}
			service := newExternalLoginTestService(t, lookup,
				unmatchedIdentityPolicyFunc(func(context.Context, UnmatchedIdentityInput) error {
					t.Fatal("unmatched policy must not run for an exact binding")
					return nil
				}),
				test.admission,
				externalRoleMapperFunc(func(context.Context, ExternalRoleMappingInput) ([]string, error) {
					mapperCalls++
					return []string{"role.operator"}, nil
				}),
				grants,
				issuer,
			)

			_, err := service.Login(context.Background(), "oidc-a", ExchangeInput{Tenant: "tenant-a", Code: "code"})
			assertCode(t, err, CodeInvalidInput)
			if mapperCalls != 0 || len(grants.calls) != 0 || issuer.calls != 0 {
				t.Fatalf("downstream calls: mapper=%d grants=%d sessions=%d", mapperCalls, len(grants.calls), issuer.calls)
			}
		})
	}
}

func TestExternalLoginReplacesOnlyVerifiedSourceGrantsAndIssuesSessionLast(t *testing.T) {
	lookup := newExternalLookup()
	identity := externalTestIdentity()
	lookup.bind(identity, IdentityAccount{ID: "account-a", Username: "alice"})
	order := make([]string, 0, 3)
	grants := newExternalGrantStore()
	grants.order = &order
	grants.values[externalGrantKey("member-a", "local")] = []string{"role.local"}
	grants.values[externalGrantKey("member-a", "manual")] = []string{"role.manual"}
	grants.values[externalGrantKey("member-a", "oidc-a")] = []string{"role.old"}
	grants.values[externalGrantKey("member-a", "oidc-b")] = []string{"role.other-provider"}
	issuer := &recordingSessionIssuer{order: &order}
	service := newExternalLoginTestService(t, lookup,
		unmatchedIdentityPolicyFunc(func(context.Context, UnmatchedIdentityInput) error {
			t.Fatal("unmatched policy must not run for an exact binding")
			return nil
		}),
		tenantAdmissionPolicyFunc(func(_ context.Context, input TenantAdmissionInput) (TenantMember, error) {
			order = append(order, "admission")
			return TenantMember{ID: "member-a", TenantCode: input.Tenant, AccountID: input.Account.ID}, nil
		}),
		externalRoleMapperFunc(func(_ context.Context, input ExternalRoleMappingInput) ([]string, error) {
			if input.Identity.SourceCode != "oidc-a" {
				t.Fatalf("mapper identity source = %q", input.Identity.SourceCode)
			}
			return []string{"role.operator", "role.operator"}, nil
		}),
		grants,
		issuer,
	)

	if _, err := service.Login(context.Background(), "oidc-a", ExchangeInput{Tenant: "tenant-a", Code: "code"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"admission", "grants", "session"}) {
		t.Fatalf("call order = %#v", order)
	}
	assertExternalGrantValues(t, grants, map[string][]string{
		externalGrantKey("member-a", "local"):  {"role.local"},
		externalGrantKey("member-a", "manual"): {"role.manual"},
		externalGrantKey("member-a", "oidc-a"): {"role.operator"},
		externalGrantKey("member-a", "oidc-b"): {"role.other-provider"},
	})
	if got := grants.calls[0].SourceCode; got != identity.SourceCode {
		t.Fatalf("grant source = %q, want verified identity source %q", got, identity.SourceCode)
	}

	service.mapper = externalRoleMapperFunc(func(context.Context, ExternalRoleMappingInput) ([]string, error) { return nil, nil })
	if _, err := service.Login(context.Background(), "oidc-a", ExchangeInput{Tenant: "tenant-a", Code: "code"}); err != nil {
		t.Fatal(err)
	}
	assertExternalGrantValues(t, grants, map[string][]string{
		externalGrantKey("member-a", "local"):  {"role.local"},
		externalGrantKey("member-a", "manual"): {"role.manual"},
		externalGrantKey("member-a", "oidc-b"): {"role.other-provider"},
	})
}

func TestExternalLoginStopsBeforeSessionWhenGrantReplacementFails(t *testing.T) {
	lookup := newExternalLookup()
	lookup.bind(externalTestIdentity(), IdentityAccount{ID: "account-a"})
	grants := newExternalGrantStore()
	grants.err = errors.New("grant store unavailable")
	issuer := &recordingSessionIssuer{}
	service := newExternalLoginTestService(t, lookup,
		unmatchedIdentityPolicyFunc(func(context.Context, UnmatchedIdentityInput) error { return nil }),
		tenantAdmissionPolicyFunc(func(_ context.Context, input TenantAdmissionInput) (TenantMember, error) {
			return TenantMember{ID: "member-a", TenantCode: input.Tenant, AccountID: input.Account.ID}, nil
		}),
		externalRoleMapperFunc(func(context.Context, ExternalRoleMappingInput) ([]string, error) {
			return []string{"role.operator"}, nil
		}),
		grants,
		issuer,
	)

	_, err := service.Login(context.Background(), "oidc-a", ExchangeInput{Tenant: "tenant-a", Code: "code"})
	assertCode(t, err, CodeStoreFailure)
	if issuer.calls != 0 {
		t.Fatalf("session issuer calls = %d", issuer.calls)
	}
}

func TestExternalLoginExtensionErrorsAreStageSpecific(t *testing.T) {
	tests := []struct {
		name  string
		stage string
		err   error
		want  ErrorCode
	}{
		{name: "unmatched identity rejection", stage: "unmatched", err: ErrIdentityRejected, want: CodeInvalidCredentials},
		{name: "unmatched other stage rejection", stage: "unmatched", err: ErrTenantAdmissionRejected, want: CodeCapabilityUnavailable},
		{name: "unmatched canceled", stage: "unmatched", err: context.Canceled, want: CodeCanceled},
		{name: "unmatched deadline", stage: "unmatched", err: context.DeadlineExceeded, want: CodeCanceled},
		{name: "unmatched typed", stage: "unmatched", err: &Error{Operation: "business.unmatched", Code: CodeConflict}, want: CodeConflict},
		{name: "unmatched unclassified", stage: "unmatched", err: errors.New("unmatched unavailable"), want: CodeCapabilityUnavailable},
		{name: "unmatched store not found", stage: "unmatched", err: ErrStoreNotFound, want: CodeCapabilityUnavailable},
		{name: "admission rejection", stage: "admission", err: ErrTenantAdmissionRejected, want: CodeInvalidCredentials},
		{name: "admission other stage rejection", stage: "admission", err: ErrIdentityRejected, want: CodeCapabilityUnavailable},
		{name: "admission canceled", stage: "admission", err: context.Canceled, want: CodeCanceled},
		{name: "admission deadline", stage: "admission", err: context.DeadlineExceeded, want: CodeCanceled},
		{name: "admission typed", stage: "admission", err: &Error{Operation: "business.admission", Code: CodeConflict}, want: CodeConflict},
		{name: "admission unclassified", stage: "admission", err: errors.New("admission unavailable"), want: CodeCapabilityUnavailable},
		{name: "admission store not found", stage: "admission", err: ErrStoreNotFound, want: CodeCapabilityUnavailable},
		{name: "mapper rejection sentinel is not credential rejection", stage: "mapper", err: ErrIdentityRejected, want: CodeCapabilityUnavailable},
		{name: "mapper canceled", stage: "mapper", err: context.Canceled, want: CodeCanceled},
		{name: "mapper deadline", stage: "mapper", err: context.DeadlineExceeded, want: CodeCanceled},
		{name: "mapper typed", stage: "mapper", err: &Error{Operation: "business.mapper", Code: CodeConflict}, want: CodeConflict},
		{name: "mapper unclassified", stage: "mapper", err: errors.New("mapper unavailable"), want: CodeCapabilityUnavailable},
		{name: "mapper store not found", stage: "mapper", err: ErrStoreNotFound, want: CodeCapabilityUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookup := newExternalLookup()
			if test.stage != "unmatched" {
				lookup.bind(externalTestIdentity(), IdentityAccount{ID: "account-a"})
			}
			unmatched := unmatchedIdentityPolicyFunc(func(context.Context, UnmatchedIdentityInput) error {
				if test.stage != "unmatched" {
					t.Fatal("unmatched policy unexpectedly called")
				}
				return test.err
			})
			admission := tenantAdmissionPolicyFunc(func(_ context.Context, input TenantAdmissionInput) (TenantMember, error) {
				if test.stage == "admission" {
					return TenantMember{}, test.err
				}
				return TenantMember{ID: "member-a", TenantCode: input.Tenant, AccountID: input.Account.ID}, nil
			})
			mapper := externalRoleMapperFunc(func(context.Context, ExternalRoleMappingInput) ([]string, error) {
				if test.stage == "mapper" {
					return nil, test.err
				}
				return nil, nil
			})
			grants := newExternalGrantStore()
			issuer := &recordingSessionIssuer{}
			service := newExternalLoginTestService(t, lookup, unmatched, admission, mapper, grants, issuer)

			_, err := service.Login(context.Background(), "oidc-a", ExchangeInput{Tenant: "tenant-a", Code: "code"})
			assertCode(t, err, test.want)
			if got := err.Error(); got != "external-login.login: "+string(test.want) {
				t.Fatalf("projected error = %q", got)
			}
			if len(grants.calls) != 0 || issuer.calls != 0 {
				t.Fatalf("downstream calls: grants=%d sessions=%d", len(grants.calls), issuer.calls)
			}
		})
	}
}

func TestDefaultSessionIssuerStoresOnlyTokenHashes(t *testing.T) {
	store := newMemoryStore()
	issuer, err := NewDefaultSessionIssuer(store, SessionOptions{
		AccessTTL: time.Minute, RefreshTTL: time.Hour, TokenBytes: 32,
		Clock: ClockFunc(func() time.Time { return time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := issuer.Issue(context.Background(), IssueSessionInput{
		Account: IdentityAccount{ID: "account-a"}, Tenant: "tenant-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	stored := store.session(session.ID)
	if stored.AccountID != "account-a" || stored.Tenant != "tenant-a" {
		t.Fatalf("stored session = %#v", stored)
	}
	if stored.AccessTokenHash == "" || stored.RefreshTokenHash == "" ||
		stored.AccessTokenHash == session.AccessToken || stored.RefreshTokenHash == string(session.RefreshToken) {
		t.Fatalf("raw token reached store: session=%#v stored=%#v", session, stored)
	}
}

func newExternalLoginTestService(
	t *testing.T,
	lookup ExternalIdentityLookup,
	unmatched UnmatchedIdentityPolicy,
	admission TenantAdmissionPolicy,
	mapper ExternalRoleMapper,
	grants ExternalRoleGrantStore,
	issuer SessionIssuer,
) *ExternalLoginService {
	t.Helper()
	provider := &fakeProvider{
		descriptor: ProviderDescriptor{
			ID: "oidc-a", Protocol: "oidc",
			Capabilities: ProviderCapabilities{Authenticate: true, AutoProvision: true, GroupClaims: true},
		},
		identity: externalTestIdentity(),
	}
	providers, err := NewProviderSet(provider)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewExternalLoginService(ExternalLoginOptions{
		Providers: providers, Accounts: lookup, Unmatched: unmatched, Admission: admission,
		RoleMapper: mapper, Grants: grants, Sessions: issuer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func externalTestIdentity() NormalizedIdentity {
	return NormalizedIdentity{
		SourceCode: "oidc-a", ExternalSubject: "subject-a", Username: "alice",
		CandidateSubjects: []string{"legacy-subject"}, ExternalGroups: []string{"operators"},
	}
}

type externalLookup struct {
	accounts map[string]IdentityAccount
	calls    int
}

func newExternalLookup() *externalLookup {
	return &externalLookup{accounts: make(map[string]IdentityAccount)}
}

func (l *externalLookup) FindExternalAccount(ctx context.Context, key ExternalIdentityKey) (IdentityAccount, error) {
	if err := ctx.Err(); err != nil {
		return IdentityAccount{}, err
	}
	l.calls++
	account, ok := l.accounts[key.SourceCode+"\x00"+key.ExternalSubject]
	if !ok {
		return IdentityAccount{}, ErrStoreNotFound
	}
	return account, nil
}

func (l *externalLookup) bind(identity NormalizedIdentity, account IdentityAccount) {
	l.accounts[identity.SourceCode+"\x00"+identity.ExternalSubject] = account
}

type unmatchedIdentityPolicyFunc func(context.Context, UnmatchedIdentityInput) error

func (f unmatchedIdentityPolicyFunc) ResolveUnmatchedIdentity(ctx context.Context, input UnmatchedIdentityInput) error {
	return f(ctx, input)
}

type tenantAdmissionPolicyFunc func(context.Context, TenantAdmissionInput) (TenantMember, error)

func (f tenantAdmissionPolicyFunc) AdmitTenant(ctx context.Context, input TenantAdmissionInput) (TenantMember, error) {
	return f(ctx, input)
}

type externalRoleMapperFunc func(context.Context, ExternalRoleMappingInput) ([]string, error)

func (f externalRoleMapperFunc) MapExternalRoles(ctx context.Context, input ExternalRoleMappingInput) ([]string, error) {
	return f(ctx, input)
}

type externalGrantStore struct {
	values map[string][]string
	calls  []ReplaceExternalRoleGrantsInput
	err    error
	order  *[]string
}

func newExternalGrantStore() *externalGrantStore {
	return &externalGrantStore{values: make(map[string][]string)}
}

func (s *externalGrantStore) ReplaceExternalRoleGrants(ctx context.Context, input ReplaceExternalRoleGrantsInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.calls = append(s.calls, input)
	if s.order != nil {
		*s.order = append(*s.order, "grants")
	}
	if s.err != nil {
		return s.err
	}
	key := externalGrantKey(input.MemberID, input.SourceCode)
	if len(input.RoleCodes) == 0 {
		delete(s.values, key)
		return nil
	}
	s.values[key] = append([]string(nil), input.RoleCodes...)
	return nil
}

func externalGrantKey(memberID TenantMemberID, source string) string {
	return string(memberID) + "\x00" + source
}

func assertExternalGrantValues(t *testing.T, store *externalGrantStore, want map[string][]string) {
	t.Helper()
	got := make(map[string][]string, len(store.values))
	for key, roles := range store.values {
		copyRoles := append([]string(nil), roles...)
		sort.Strings(copyRoles)
		got[key] = copyRoles
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("grant values = %#v, want %#v", got, want)
	}
}

type recordingSessionIssuer struct {
	calls int
	order *[]string
}

func (i *recordingSessionIssuer) Issue(ctx context.Context, input IssueSessionInput) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	i.calls++
	if i.order != nil {
		*i.order = append(*i.order, "session")
	}
	return Session{
		ID: "session-a", AccessToken: "access", RefreshToken: "refresh",
		AccessExpiresAt: time.Now().Add(time.Minute), RefreshExpiresAt: time.Now().Add(time.Hour),
	}, nil
}

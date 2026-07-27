package coreapp

import (
	"context"
	"errors"
	"strings"
)

type UnmatchedIdentityInput struct {
	Tenant   string
	Identity NormalizedIdentity
}

type UnmatchedIdentityPolicy interface {
	// ResolveUnmatchedIdentity may establish a binding, but Core accepts it only
	// after FindExternalAccount returns that exact source and subject.
	ResolveUnmatchedIdentity(context.Context, UnmatchedIdentityInput) error
}

type TenantAdmissionInput struct {
	Tenant   string
	Identity NormalizedIdentity
	Account  IdentityAccount
}

type TenantAdmissionPolicy interface {
	AdmitTenant(context.Context, TenantAdmissionInput) (TenantMember, error)
}

type ExternalLoginOptions struct {
	Providers  ProviderSet
	Accounts   ExternalIdentityLookup
	Unmatched  UnmatchedIdentityPolicy
	Admission  TenantAdmissionPolicy
	RoleMapper ExternalRoleMapper
	Grants     ExternalRoleGrantStore
	Sessions   SessionIssuer
}

type ExternalLoginService struct {
	providers ProviderSet
	accounts  ExternalIdentityLookup
	unmatched UnmatchedIdentityPolicy
	admission TenantAdmissionPolicy
	mapper    ExternalRoleMapper
	grants    ExternalRoleGrantStore
	sessions  SessionIssuer
}

type ExternalLoginResult struct {
	Account IdentityAccount
	Member  TenantMember
	Session Session
}

func NewExternalLoginService(options ExternalLoginOptions) (*ExternalLoginService, error) {
	if interfaceNil(options.Accounts) || interfaceNil(options.Unmatched) || interfaceNil(options.Admission) ||
		interfaceNil(options.RoleMapper) || interfaceNil(options.Grants) || interfaceNil(options.Sessions) {
		return nil, invalid("external-login.new")
	}
	return &ExternalLoginService{
		providers: options.Providers, accounts: options.Accounts, unmatched: options.Unmatched,
		admission: options.Admission, mapper: options.RoleMapper, grants: options.Grants, sessions: options.Sessions,
	}, nil
}

func (s *ExternalLoginService) Login(ctx context.Context, providerID string, input ExchangeInput) (ExternalLoginResult, error) {
	const operation = "external-login.login"
	if err := ctx.Err(); err != nil {
		return ExternalLoginResult{}, canceled(operation, err)
	}
	providerID = strings.TrimSpace(providerID)
	input.Tenant = strings.TrimSpace(input.Tenant)
	if providerID == "" || input.Tenant == "" {
		return ExternalLoginResult{}, invalid(operation)
	}
	identity, err := s.providers.Exchange(ctx, providerID, input)
	if err != nil {
		return ExternalLoginResult{}, err
	}
	key := ExternalIdentityKey{SourceCode: identity.SourceCode, ExternalSubject: identity.ExternalSubject}
	account, err := s.accounts.FindExternalAccount(ctx, key)
	if errors.Is(err, ErrStoreNotFound) {
		if err := s.unmatched.ResolveUnmatchedIdentity(ctx, UnmatchedIdentityInput{Tenant: input.Tenant, Identity: cloneIdentity(identity)}); err != nil {
			return ExternalLoginResult{}, mapUnmatchedIdentityError(operation, err)
		}
		account, err = s.accounts.FindExternalAccount(ctx, key)
	}
	if err != nil {
		return ExternalLoginResult{}, mapExternalLookupError(operation, err)
	}
	if account.ID == "" {
		return ExternalLoginResult{}, invalid(operation)
	}

	member, err := s.admission.AdmitTenant(ctx, TenantAdmissionInput{Tenant: input.Tenant, Identity: cloneIdentity(identity), Account: account})
	if err != nil {
		return ExternalLoginResult{}, mapTenantAdmissionError(operation, err)
	}
	if member.ID == "" || member.TenantCode != input.Tenant || member.AccountID != account.ID {
		return ExternalLoginResult{}, invalid(operation)
	}
	roles, err := s.mapper.MapExternalRoles(ctx, ExternalRoleMappingInput{
		Tenant: input.Tenant, Identity: cloneIdentity(identity), Account: account, Member: member,
	})
	if err != nil {
		return ExternalLoginResult{}, mapExternalRoleMapperError(operation, err)
	}
	roles, err = canonicalExternalRoles(roles)
	if err != nil {
		return ExternalLoginResult{}, err
	}
	if err := s.grants.ReplaceExternalRoleGrants(ctx, ReplaceExternalRoleGrantsInput{
		Tenant: input.Tenant, MemberID: member.ID, SourceCode: identity.SourceCode, RoleCodes: roles,
	}); err != nil {
		return ExternalLoginResult{}, mapStoreError(operation, err)
	}
	session, err := s.sessions.Issue(ctx, IssueSessionInput{Account: account, Tenant: input.Tenant})
	if err != nil {
		if CodeOf(err) != "" {
			return ExternalLoginResult{}, err
		}
		return ExternalLoginResult{}, mapStoreError(operation, err)
	}
	return ExternalLoginResult{Account: account, Member: member, Session: session}, nil
}

func mapExternalLookupError(operation string, err error) error {
	if errors.Is(err, ErrStoreNotFound) {
		return coreError(operation, CodeInvalidCredentials, err)
	}
	return mapStoreError(operation, err)
}

func mapUnmatchedIdentityError(operation string, err error) error {
	return mapExternalExtensionError(operation, err, ErrIdentityRejected)
}

func mapTenantAdmissionError(operation string, err error) error {
	return mapExternalExtensionError(operation, err, ErrTenantAdmissionRejected)
}

func mapExternalRoleMapperError(operation string, err error) error {
	return mapExternalExtensionError(operation, err, nil)
}

func mapExternalExtensionError(operation string, err, rejected error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return canceled(operation, err)
	}
	if rejected != nil && errors.Is(err, rejected) {
		return coreError(operation, CodeInvalidCredentials, err)
	}
	if code := CodeOf(err); code != "" {
		return coreError(operation, code, err)
	}
	return coreError(operation, CodeCapabilityUnavailable, err)
}

func mapStoreError(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return canceled(operation, err)
	}
	if errors.Is(err, ErrStoreConflict) {
		return coreError(operation, CodeConflict, err)
	}
	return storeFailure(operation, err)
}

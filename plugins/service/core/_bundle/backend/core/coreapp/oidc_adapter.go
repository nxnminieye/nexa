package coreapp

import (
	"context"
	"strings"
)

type OIDCAuthorizeRequest struct {
	State       string
	Tenant      string
	RedirectURI string
}

type OIDCExchangeRequest struct {
	Code        string
	State       string
	Tenant      string
	RedirectURI string
}

type OIDCClaims struct {
	Subject           string
	Username          string
	Email             string
	DisplayName       string
	CandidateSubjects []string
	Groups            []string
}

type OIDCClient interface {
	AuthorizeURL(context.Context, OIDCAuthorizeRequest) (string, error)
	Exchange(context.Context, OIDCExchangeRequest) (OIDCClaims, error)
}

type OIDCProviderOptions struct {
	ID     string
	Client OIDCClient
	// AutoProvision is a deprecated informational hint for provider discovery.
	AutoProvision bool
	GroupClaims   bool
}

type oidcProvider struct {
	descriptor ProviderDescriptor
	client     OIDCClient
}

func NewOIDCProvider(options OIDCProviderOptions) (IdentityProvider, error) {
	id := strings.TrimSpace(options.ID)
	if id == "" || interfaceNil(options.Client) {
		return nil, invalid("oidc-provider.new")
	}
	return &oidcProvider{
		descriptor: ProviderDescriptor{
			ID: id, Protocol: "oidc",
			Capabilities: ProviderCapabilities{Authenticate: true, AutoProvision: options.AutoProvision, GroupClaims: options.GroupClaims},
		},
		client: options.Client,
	}, nil
}

func (p *oidcProvider) Descriptor() ProviderDescriptor { return p.descriptor }

func (p *oidcProvider) Authorize(ctx context.Context, input AuthorizeInput) (AuthorizeResult, error) {
	url, err := p.client.AuthorizeURL(ctx, OIDCAuthorizeRequest{State: input.State, Tenant: input.Tenant, RedirectURI: input.RedirectURI})
	if err != nil {
		return AuthorizeResult{}, err
	}
	return AuthorizeResult{URL: url}, nil
}

func (p *oidcProvider) Exchange(ctx context.Context, input ExchangeInput) (NormalizedIdentity, error) {
	claims, err := p.client.Exchange(ctx, OIDCExchangeRequest{Code: input.Code, State: input.State, Tenant: input.Tenant, RedirectURI: input.RedirectURI})
	if err != nil {
		return NormalizedIdentity{}, err
	}
	return NormalizedIdentity{
		SourceCode: p.descriptor.ID, ExternalSubject: strings.TrimSpace(claims.Subject),
		Username: strings.TrimSpace(claims.Username), Email: strings.TrimSpace(claims.Email), DisplayName: strings.TrimSpace(claims.DisplayName),
		CandidateSubjects: append([]string(nil), claims.CandidateSubjects...), ExternalGroups: append([]string(nil), claims.Groups...),
	}, nil
}

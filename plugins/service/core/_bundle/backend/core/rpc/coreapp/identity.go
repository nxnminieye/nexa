package coreapp

import "context"

type ProviderCapabilities struct {
	Authenticate bool
	// AutoProvision is a deprecated informational hint. Core never uses it to provision an account.
	AutoProvision bool
	GroupClaims   bool
}

type ProviderDescriptor struct {
	ID           string
	Protocol     string
	Capabilities ProviderCapabilities
}

type AuthorizeInput struct {
	State       string
	Tenant      string
	RedirectURI string
}

type AuthorizeResult struct {
	URL string
}

type ExchangeInput struct {
	Code        string
	State       string
	Tenant      string
	RedirectURI string
}

type NormalizedIdentity struct {
	SourceCode        string
	ExternalSubject   string
	Username          string
	Email             string
	DisplayName       string
	CandidateSubjects []string
	ExternalGroups    []string
}

type IdentityProvider interface {
	Descriptor() ProviderDescriptor
	Authorize(context.Context, AuthorizeInput) (AuthorizeResult, error)
	Exchange(context.Context, ExchangeInput) (NormalizedIdentity, error)
}

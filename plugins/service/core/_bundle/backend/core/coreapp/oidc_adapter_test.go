package coreapp

import (
	"context"
	"testing"
)

func TestOIDCProviderNormalizesClaims(t *testing.T) {
	client := &oidcClient{claims: OIDCClaims{
		Subject: " subject ", Username: " alice ", CandidateSubjects: []string{"legacy-subject"}, Groups: []string{"operators"},
	}}
	provider, err := NewOIDCProvider(OIDCProviderOptions{ID: "identity", Client: client, AutoProvision: true, GroupClaims: true})
	if err != nil {
		t.Fatal(err)
	}
	authorize := AuthorizeInput{State: "business-state", Tenant: "tenant-a", RedirectURI: "https://business.example.test/callback"}
	if _, err := provider.Authorize(context.Background(), authorize); err != nil {
		t.Fatal(err)
	}
	if client.authorize != (OIDCAuthorizeRequest(authorize)) {
		t.Fatalf("OIDC authorize request = %#v, want %#v", client.authorize, authorize)
	}
	request := ExchangeInput{Code: "code", State: "business-state", Tenant: "tenant-a", RedirectURI: "https://business.example.test/callback"}
	identity, err := provider.Exchange(context.Background(), request)
	if err != nil || identity.SourceCode != "identity" || identity.ExternalSubject != "subject" || identity.Username != "alice" || identity.ExternalGroups[0] != "operators" {
		t.Fatalf("identity = %#v, %v", identity, err)
	}
	if client.exchange != (OIDCExchangeRequest(request)) {
		t.Fatalf("OIDC exchange request = %#v, want %#v", client.exchange, request)
	}
	identity.ExternalGroups[0] = "mutated"
	identity.CandidateSubjects[0] = "mutated"
	if client.claims.Groups[0] != "operators" || client.claims.CandidateSubjects[0] != "legacy-subject" {
		t.Fatal("provider returned aliased claim hints")
	}
}

type oidcClient struct {
	claims    OIDCClaims
	authorize OIDCAuthorizeRequest
	exchange  OIDCExchangeRequest
}

func (c *oidcClient) AuthorizeURL(_ context.Context, request OIDCAuthorizeRequest) (string, error) {
	c.authorize = request
	return "https://identity.example.test/authorize", nil
}

func (c *oidcClient) Exchange(_ context.Context, request OIDCExchangeRequest) (OIDCClaims, error) {
	c.exchange = request
	return c.claims, nil
}

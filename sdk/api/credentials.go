package api

import "context"

// CredentialValue is one call-scoped credential selected by its Manifest ID.
type CredentialValue struct {
	ID    string
	Value string
}

// CredentialProvider resolves call-scoped credential values for one canonical API operation ID.
type CredentialProvider interface {
	Credentials(context.Context, string) ([]CredentialValue, error)
}

type CredentialProviderFunc func(context.Context, string) ([]CredentialValue, error)

func (fn CredentialProviderFunc) Credentials(ctx context.Context, apiOperationID string) ([]CredentialValue, error) {
	return fn(ctx, apiOperationID)
}

type staticCredentialProvider struct {
	values []CredentialValue
}

// NewStaticCredentialProvider freezes the supplied sequence without interpreting it.
func NewStaticCredentialProvider(values []CredentialValue) CredentialProvider {
	return staticCredentialProvider{values: append([]CredentialValue(nil), values...)}
}

func (p staticCredentialProvider) Credentials(context.Context, string) ([]CredentialValue, error) {
	return append([]CredentialValue(nil), p.values...), nil
}

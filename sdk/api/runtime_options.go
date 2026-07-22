package api

import "net/url"

// RuntimeOptions configures a Client from a validated RuntimeContract.
type RuntimeOptions struct {
	RuntimeContract    RuntimeContract
	Endpoint           *url.URL
	Transport          Transport
	CredentialProvider CredentialProvider
	MaxResponseBytes   int64
}

// NewRuntime constructs a Client over the same runtime model used by New.
func NewRuntime(options RuntimeOptions) (*Client, error) {
	if options.RuntimeContract.model == nil {
		return nil, newRuntimeContractInvalid("runtime_contract_schema_invalid", "/runtimeContract")
	}
	if issue := validateRuntimeModel(options.RuntimeContract.model); issue != nil {
		return nil, newRuntimeContractInvalid(issue.reason, issue.pointer)
	}
	return newClientFromRuntimeModel(
		options.RuntimeContract.model,
		options.Endpoint,
		options.Transport,
		options.CredentialProvider,
		options.MaxResponseBytes,
	)
}

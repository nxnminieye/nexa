package api_test

import (
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	generationapi "github.com/nxnminieye/nexa/generation/api"
	api "github.com/nxnminieye/nexa/sdk/api"
)

func TestExternalTransportUsesOnlyPublicResponseContract(t *testing.T) {
	corpus := externalRuntimeCorpus(t)
	manifest, err := generationapi.Parse("runtime-api-v1.json#manifest", corpus.ManifestJSON())
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := manifest.CanonicalJSON()
	if err != nil || string(canonical) != string(corpus.ManifestJSON()) {
		t.Fatalf("corpus Manifest is not owner-canonical: %v", err)
	}
	client, err := api.New(api.Options{
		Manifest:           manifest,
		Endpoint:           &url.URL{Scheme: "https", Host: "api.example.test"},
		Transport:          externalTransport{},
		CredentialProvider: api.NewStaticCredentialProvider([]api.CredentialValue{{ID: "primary", Value: "sample-token"}}),
		MaxResponseBytes:   api.RuntimeLimits().ResponseBytesMax,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := api.ParseRequest([]byte(`{"id":"sample-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Call(context.Background(), "sample.get", request)
	if err != nil {
		t.Fatal(err)
	}
	if result.APIOperationID() != "sample.get" || result.HTTPStatus() != 200 || result.ResponseBody() != generationapi.ResponseBodyJSON {
		t.Fatalf("Result = (%q,%d,%q)", result.APIOperationID(), result.HTTPStatus(), result.ResponseBody())
	}
	encoded, ok := result.JSON()
	if !ok || string(encoded) != `{"displayName":"Sample"}` {
		t.Fatalf("Result.JSON() = %s, %t", encoded, ok)
	}
}

type externalTransport struct{}

func (externalTransport) RoundTrip(context.Context, api.WireRequest) (api.WireResponse, error) {
	return api.NewWireResponse(
		200,
		[]api.Header{{Name: "content-type", Value: "application/json"}, {Name: "x-public", Value: "ok"}},
		io.NopCloser(strings.NewReader(`{"displayName":"Sample"}`)),
	)
}

var _ api.Transport = externalTransport{}

type embeddedCallOption struct{ api.CallOption }

func TestExternalCallOptionWrappersAreRejectedBeforeCallbacks(t *testing.T) {
	corpus := externalRuntimeCorpus(t)
	manifest, err := generationapi.Parse("runtime-api-v1.json#manifest", corpus.ManifestJSON())
	if err != nil {
		t.Fatal(err)
	}
	request, err := api.ParseRequest([]byte(`{"id":"sample-1"}`))
	if err != nil {
		t.Fatal(err)
	}

	vectors := []struct {
		name           string
		apiOperationID string
		option         api.CallOption
	}{
		{name: "zero wrapper before lookup", apiOperationID: "unknown", option: embeddedCallOption{}},
		{name: "zero wrapper before callbacks", apiOperationID: "sample.get", option: embeddedCallOption{}},
		{name: "nonzero wrapper", apiOperationID: "sample.get", option: embeddedCallOption{CallOption: api.WithMaxResponseBytes(api.RuntimeLimits().ResponseBytesMax)}},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			var providerCalls atomic.Int64
			var transportCalls atomic.Int64
			client, err := api.New(api.Options{
				Manifest: manifest,
				Endpoint: &url.URL{Scheme: "https", Host: "api.example.test"},
				Transport: api.TransportFunc(func(context.Context, api.WireRequest) (api.WireResponse, error) {
					transportCalls.Add(1)
					return api.WireResponse{}, errors.New("transport must not run")
				}),
				CredentialProvider: api.CredentialProviderFunc(func(context.Context, string) ([]api.CredentialValue, error) {
					providerCalls.Add(1)
					return []api.CredentialValue{{ID: "primary", Value: "secret"}}, nil
				}),
				MaxResponseBytes: api.RuntimeLimits().ResponseBytesMin,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Call(context.Background(), vector.apiOperationID, request, vector.option)
			var apiError *api.Error
			if !errors.As(err, &apiError) || apiError == nil {
				t.Fatalf("error = %T %v, want *api.Error", err, err)
			}
			if apiError.Code() != "client_invalid" || apiError.Domain() != "nexa.sdk.api" || apiError.Category() != protocol.CategoryInput || apiError.Retryable() || apiError.Error() != "API call configuration is invalid" {
				t.Fatalf("identity = (%q,%q,%q,%t,%q)", apiError.Code(), apiError.Domain(), apiError.Category(), apiError.Retryable(), apiError.Error())
			}
			if apiError.Details().Reason() != "call_option_invalid" || apiError.Details().Pointer() != "/options/0" || apiError.Details().HTTPStatus() != 0 || apiError.APIOperationID() != "" || apiError.RequestID() != "" || apiError.TraceID() != "" || apiError.Details().RemoteDomain() != "" || apiError.Details().RemoteCode() != "" {
				t.Fatalf("details/context = %#v op=%q request=%q trace=%q", apiError.Details(), apiError.APIOperationID(), apiError.RequestID(), apiError.TraceID())
			}
			if providerCalls.Load() != 0 || transportCalls.Load() != 0 {
				t.Fatalf("callback calls = provider %d transport %d", providerCalls.Load(), transportCalls.Load())
			}
		})
	}
}

func externalRuntimeCorpus(t *testing.T) api.RuntimeCorpus {
	t.Helper()
	data, err := api.RuntimeCorpusBytes()
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := api.ParseRuntimeCorpus(data)
	if err != nil {
		t.Fatal(err)
	}
	return corpus
}

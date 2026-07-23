package aws

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	cores3 "github.com/nxnminieye/nexa/runtime/s3"
)

type fakeAPI struct {
	getInput    *awss3.GetObjectInput
	getOutput   *awss3.GetObjectOutput
	getErr      error
	putInput    *awss3.PutObjectInput
	putOutput   *awss3.PutObjectOutput
	putErr      error
	headInput   *awss3.HeadObjectInput
	headOutput  *awss3.HeadObjectOutput
	headErr     error
	deleteInput *awss3.DeleteObjectInput
	deleteErr   error
}

func (f *fakeAPI) GetObject(_ context.Context, input *awss3.GetObjectInput, _ ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
	f.getInput = input
	return f.getOutput, f.getErr
}
func (f *fakeAPI) PutObject(_ context.Context, input *awss3.PutObjectInput, _ ...func(*awss3.Options)) (*awss3.PutObjectOutput, error) {
	f.putInput = input
	return f.putOutput, f.putErr
}
func (f *fakeAPI) HeadObject(_ context.Context, input *awss3.HeadObjectInput, _ ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
	f.headInput = input
	return f.headOutput, f.headErr
}
func (f *fakeAPI) DeleteObject(_ context.Context, input *awss3.DeleteObjectInput, _ ...func(*awss3.Options)) (*awss3.DeleteObjectOutput, error) {
	f.deleteInput = input
	return nil, f.deleteErr
}

type trackingBody struct {
	reader   io.Reader
	closed   bool
	readErr  error
	closeErr error
}

func (b *trackingBody) Read(p []byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	return b.reader.Read(p)
}
func (b *trackingBody) Close() error { b.closed = true; return b.closeErr }

type staticCredentials struct{}

func (staticCredentials) Retrieve(context.Context) (sdkaws.Credentials, error) {
	return sdkaws.Credentials{AccessKeyID: "access", SecretAccessKey: "secret", Source: "test"}, nil
}

type requestCapture struct {
	request *http.Request
}

func (c *requestCapture) RoundTrip(request *http.Request) (*http.Response, error) {
	c.request = request.Clone(request.Context())
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("payload")),
		Request:    request,
	}, nil
}

type arbitraryHTTPClient struct {
	called bool
}

func (c *arbitraryHTTPClient) Do(request *http.Request) (*http.Response, error) {
	c.called = true
	return (&requestCapture{}).RoundTrip(request)
}

type scriptedRedirectTransport struct {
	status int
	calls  int
}

func (t *scriptedRedirectTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.calls++
	if t.calls > 1 {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unexpected")), Request: request}, nil
	}
	header := make(http.Header)
	header.Set("Location", "http://objects.example.test/bucket/key")
	return &http.Response{StatusCode: t.status, Header: header, Body: io.NopCloser(strings.NewReader("redirect")), Request: request}, nil
}

type endpointConfigSource struct {
	endpoint string
}

type rewritingInterceptor struct {
	called *bool
}

func (i rewritingInterceptor) BeforeTransmit(_ context.Context, in *smithyhttp.InterceptorContext) error {
	*i.called = true
	in.Request.URL.Scheme = "http"
	in.Request.URL.Host = "interceptor-bypass.example.test"
	return nil
}

func (s endpointConfigSource) GetServiceBaseEndpoint(context.Context, string) (string, bool, error) {
	return s.endpoint, true, nil
}

func TestNewClientValidatesEndpointWithoutIO(t *testing.T) {
	client, err := NewClient(ClientOptions{Config: sdkaws.Config{Region: "us-east-1"}, Endpoint: "https://objects.example.test/", UsePathStyle: true})
	if err != nil || client == nil {
		t.Fatalf("client = %v, %v", client, err)
	}
	sdkClient, ok := client.api.(*awss3.Client)
	if !ok {
		t.Fatalf("SDK client type = %T", client.api)
	}
	controlled, ok := sdkClient.Options().HTTPClient.(*http.Client)
	if !ok {
		t.Fatalf("nil caller HTTP client guard = %T", sdkClient.Options().HTTPClient)
	}
	guardedTransport, ok := controlled.Transport.(*schemeGuardRoundTripper)
	if !ok {
		t.Fatalf("controlled default transport = %T", controlled.Transport)
	}
	if _, ok := guardedTransport.delegate.(*http.Transport); !ok {
		t.Fatalf("resolved default transport = %T", guardedTransport.delegate)
	}
	for _, endpoint := range []string{
		"http://objects.example.test",
		"/relative",
		"https://user@objects.example.test",
		"https://objects.example.test?q=1",
		"https://objects.example.test/bucket",
		"https://objects.example.test/%2F",
		"https://:443",
		"https://objects.example.test:",
		"https://[::1]:",
		"https://objects.example.test:0",
		"https://objects.example.test:65536",
		"https://objects.example.test:abc",
		"ftp://objects.example.test",
	} {
		_, err := NewClient(ClientOptions{Config: sdkaws.Config{Region: "us-east-1"}, Endpoint: endpoint})
		if !errors.Is(err, cores3.ErrValidation) {
			t.Fatalf("endpoint %q error = %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"https://objects.example.test:1", "https://objects.example.test:65535", "https://[::1]:443"} {
		if _, err := NewClient(ClientOptions{Config: sdkaws.Config{Region: "us-east-1"}, Endpoint: endpoint}); err != nil {
			t.Fatalf("valid endpoint %q error = %v", endpoint, err)
		}
	}
	if _, err := NewClient(ClientOptions{Config: sdkaws.Config{Region: "us-east-1"}, Endpoint: "http://objects.example.test", AllowInsecureHTTP: true}); err != nil {
		t.Fatalf("explicit insecure endpoint: %v", err)
	}
	_, err = NewClient(ClientOptions{})
	var typed *cores3.Error
	if !errors.As(err, &typed) || typed.Reason() != "region_required" || typed.Pointer() != "/config/region" {
		t.Fatalf("empty region error = %#v", err)
	}
	for _, region := range []string{" us-east-1", "us-east-1 ", "us-east-1\n", "us-east-1\x00", string([]byte{0xff})} {
		_, err := NewClient(ClientOptions{Config: sdkaws.Config{Region: region}})
		if !errors.As(err, &typed) || typed.Reason() != "region_invalid" || typed.Pointer() != "/config/region" {
			t.Fatalf("region %q error = %#v", region, err)
		}
	}
	var typedNil *http.Client
	_, err = NewClient(ClientOptions{Config: sdkaws.Config{Region: "us-east-1", HTTPClient: typedNil}})
	if !errors.As(err, &typed) || typed.Reason() != "http_client_invalid" || typed.Pointer() != "/config/httpClient" {
		t.Fatalf("typed-nil HTTP client error = %#v", err)
	}
	_, err = NewClient(ClientOptions{Config: sdkaws.Config{Region: "us-east-1", HTTPClient: &arbitraryHTTPClient{}}})
	if !errors.As(err, &typed) || typed.Reason() != "http_client_unconstrained" || typed.Pointer() != "/config/httpClient" {
		t.Fatalf("unconstrained HTTP client error = %#v", err)
	}
	_, err = NewClient(ClientOptions{Config: sdkaws.Config{Region: "us-east-1", HTTPClient: &http.Client{Transport: &requestCapture{}}}})
	if !errors.As(err, &typed) || typed.Reason() != "http_client_unconstrained" || typed.Pointer() != "/config/httpClient" {
		t.Fatalf("unconstrained RoundTripper error = %#v", err)
	}
	var typedNilTransport *http.Transport
	_, err = NewClient(ClientOptions{Config: sdkaws.Config{Region: "us-east-1", HTTPClient: &http.Client{Transport: typedNilTransport}}})
	if !errors.As(err, &typed) || typed.Reason() != "http_client_invalid" || typed.Pointer() != "/config/httpClient" {
		t.Fatalf("typed-nil RoundTripper error = %#v", err)
	}
}

func TestNewClientSanitizesConfigEndpointAndRegionOverrides(t *testing.T) {
	t.Setenv("AWS_ENDPOINT_URL_S3", "http://env-bypass.example.test")
	capture := &requestCapture{}
	captureClient := &http.Client{Transport: capture}
	baseEndpoint := "http://base-bypass.example.test"
	legacyResolverCalled := false
	optionsResolverCalled := false
	serviceOptionsCalled := false
	apiOptionCalled := false
	interceptorCalled := false
	interceptors := smithyhttp.InterceptorRegistry{}
	interceptors.AddBeforeTransmit(rewritingInterceptor{called: &interceptorCalled})
	config := sdkaws.Config{
		Region:       "approved-region-1",
		Credentials:  staticCredentials{},
		HTTPClient:   captureClient,
		BaseEndpoint: &baseEndpoint,
		EndpointResolver: sdkaws.EndpointResolverFunc(func(string, string) (sdkaws.Endpoint, error) {
			legacyResolverCalled = true
			return sdkaws.Endpoint{URL: "http://legacy-bypass.example.test"}, nil
		}),
		EndpointResolverWithOptions: sdkaws.EndpointResolverWithOptionsFunc(func(string, string, ...interface{}) (sdkaws.Endpoint, error) {
			optionsResolverCalled = true
			return sdkaws.Endpoint{URL: "http://options-bypass.example.test"}, nil
		}),
		ConfigSources: []any{endpointConfigSource{endpoint: "http://source-bypass.example.test"}},
		APIOptions: []func(*middleware.Stack) error{func(*middleware.Stack) error {
			apiOptionCalled = true
			return nil
		}},
		Interceptors: interceptors,
		ServiceOptions: []func(string, any){func(_ string, raw any) {
			serviceOptionsCalled = true
			service := raw.(*awss3.Options)
			service.Region = "bypass-region-1"
			service.BaseEndpoint = sdkaws.String("http://service-bypass.example.test")
		}},
	}
	client, err := NewClient(ClientOptions{Config: config, Endpoint: "https://approved.example.test", UsePathStyle: true, AllowInsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := cores3.NewObjectRef("bucket", "key")
	result, err := client.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body().Close()
	if capture.request == nil {
		t.Fatal("request was not captured")
	}
	if capture.request.URL.Scheme != "https" || capture.request.URL.Host != "approved.example.test" || capture.request.URL.Path != "/bucket/key" {
		t.Fatalf("request URL = %s", capture.request.URL)
	}
	authorization := capture.request.Header.Get("Authorization")
	if !strings.Contains(authorization, "/approved-region-1/s3/aws4_request") || strings.Contains(authorization, "bypass-region-1") {
		t.Fatalf("authorization scope = %q", authorization)
	}
	if legacyResolverCalled || optionsResolverCalled || serviceOptionsCalled || apiOptionCalled || interceptorCalled {
		t.Fatalf("override called = legacy:%t options:%t service:%t api:%t interceptor:%t", legacyResolverCalled, optionsResolverCalled, serviceOptionsCalled, apiOptionCalled, interceptorCalled)
	}
	if sdkaws.ToString(config.BaseEndpoint) != baseEndpoint || config.EndpointResolver == nil || config.EndpointResolverWithOptions == nil || len(config.ConfigSources) != 1 || len(config.APIOptions) != 1 || len(config.Interceptors.BeforeTransmit) != 1 || len(config.ServiceOptions) != 1 || config.Region != "approved-region-1" || config.Credentials == nil || config.HTTPClient != captureClient || captureClient.Transport != capture {
		t.Fatal("caller config was modified")
	}

	capture.request = nil
	client, err = NewClient(ClientOptions{Config: config, UsePathStyle: true, AllowInsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err = client.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body().Close()
	if capture.request == nil || capture.request.URL.Scheme != "https" || strings.Contains(capture.request.URL.Host, "bypass.example.test") {
		t.Fatalf("ambient endpoint survived: %v", capture.request)
	}
	if len(config.ConfigSources) != 1 {
		t.Fatal("caller ConfigSources was modified")
	}
}

func TestControlledHTTPClientSchemeGuard(t *testing.T) {
	for _, test := range []struct {
		name          string
		scheme        string
		allowInsecure bool
		wantDelegated bool
	}{
		{name: "https", scheme: "https", wantDelegated: true},
		{name: "http rejected", scheme: "http"},
		{name: "http explicitly allowed", scheme: "http", allowInsecure: true, wantDelegated: true},
		{name: "unknown scheme", scheme: "ftp"},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := &requestCapture{}
			client := newControlledHTTPClient(capture, 0, nil, test.allowInsecure)
			request, err := http.NewRequest(http.MethodGet, test.scheme+"://objects.example.test/bucket/key", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if response != nil {
				response.Body.Close()
			}
			if (err == nil) != test.wantDelegated || (capture.request != nil) != test.wantDelegated {
				t.Fatalf("Do error = %v, delegated=%t", err, capture.request != nil)
			}
		})
	}
}

func TestControlledHTTPClientRejects307And308DowngradeBeforeSecondRoundTrip(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			transport := &scriptedRedirectTransport{status: status}
			callerPolicyCalled := false
			controlled := newControlledHTTPClient(transport, 0, func(*http.Request, []*http.Request) error {
				callerPolicyCalled = true
				return nil
			}, false)
			request, _ := http.NewRequest(http.MethodGet, "https://objects.example.test/bucket/key", nil)
			response, err := controlled.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != status || transport.calls != 1 || callerPolicyCalled {
				t.Fatalf("redirect result = status:%d calls:%d callerPolicy:%t", response.StatusCode, transport.calls, callerPolicyCalled)
			}
		})
	}
}

func TestSecureModeClonesSupportedCallerHTTPClient(t *testing.T) {
	transport := &http.Transport{}
	caller := &http.Client{Transport: transport, Timeout: time.Second}
	client, err := NewClient(ClientOptions{Config: sdkaws.Config{Region: "us-east-1", HTTPClient: caller}})
	if err != nil {
		t.Fatal(err)
	}
	sdkClient := client.api.(*awss3.Client)
	controlled, ok := sdkClient.Options().HTTPClient.(*http.Client)
	if !ok || controlled == caller || controlled.Timeout != caller.Timeout {
		t.Fatalf("controlled caller client = %T same=%t timeout=%s", sdkClient.Options().HTTPClient, controlled == caller, controlled.Timeout)
	}
	guard, ok := controlled.Transport.(*schemeGuardRoundTripper)
	if !ok || guard.delegate != transport {
		t.Fatalf("controlled caller transport = %#v", controlled.Transport)
	}
	if caller.Transport != transport {
		t.Fatal("caller HTTP client was modified")
	}
}

func TestSecureModeNilCallerTransportDoesNotUseHTTPDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })

	assertIndependent := func(t *testing.T) {
		t.Helper()
		caller := &http.Client{}
		client, err := NewClient(ClientOptions{Config: sdkaws.Config{Region: "us-east-1", HTTPClient: caller}})
		if err != nil {
			t.Fatal(err)
		}
		sdkClient := client.api.(*awss3.Client)
		controlled := sdkClient.Options().HTTPClient.(*http.Client)
		guard, ok := controlled.Transport.(*schemeGuardRoundTripper)
		if !ok {
			t.Fatalf("controlled transport = %T", controlled.Transport)
		}
		independent, ok := guard.delegate.(*http.Transport)
		if !ok || independent == nil || guard.delegate == http.DefaultTransport {
			t.Fatalf("guard delegate = %T %#v", guard.delegate, guard.delegate)
		}
		if caller.Transport != nil {
			t.Fatal("caller HTTP client was modified")
		}
	}

	http.DefaultTransport = &requestCapture{}
	assertIndependent(t)
	var typedNil *http.Transport
	http.DefaultTransport = typedNil
	assertIndependent(t)
}

func TestInsecureModeAcceptsUnconstrainedHTTPClientWithInitialGuard(t *testing.T) {
	delegate := &arbitraryHTTPClient{}
	client, err := NewClient(ClientOptions{Config: sdkaws.Config{Region: "us-east-1", HTTPClient: delegate}, AllowInsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	sdkClient := client.api.(*awss3.Client)
	guard, ok := sdkClient.Options().HTTPClient.(*initialSchemeGuardHTTPClient)
	if !ok || guard.delegate != delegate {
		t.Fatalf("unconstrained client guard = %#v", sdkClient.Options().HTTPClient)
	}
}

func TestGetMapsInputMetadataAndBodyOwnership(t *testing.T) {
	body := &trackingBody{reader: strings.NewReader("payload")}
	api := &fakeAPI{getOutput: &awss3.GetObjectOutput{Body: body, ContentType: sdkaws.String("text/plain"), ContentLength: sdkaws.Int64(7), ETag: sdkaws.String("etag"), VersionId: sdkaws.String("version"), Metadata: map[string]string{"a": "b"}}}
	client := &Client{api: api}
	ref, _ := cores3.NewObjectRef("bucket", "key")
	result, err := client.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if sdkaws.ToString(api.getInput.Bucket) != "bucket" || sdkaws.ToString(api.getInput.Key) != "key" || body.closed {
		t.Fatalf("get input/body = %#v closed=%t", api.getInput, body.closed)
	}
	payload, err := io.ReadAll(result.Body())
	if err != nil || string(payload) != "payload" {
		t.Fatalf("payload = %q, %v", payload, err)
	}
	if err := result.Body().Close(); err != nil || !body.closed {
		t.Fatalf("close = %v, closed=%t", err, body.closed)
	}
}

func TestGetClosesCoReturnedBodyAndMapsFailures(t *testing.T) {
	ref, _ := cores3.NewObjectRef("bucket", "key")
	for _, test := range []struct {
		name     string
		err      error
		sentinel error
	}{
		{"no such key", &types.NoSuchKey{}, cores3.ErrNotFound},
		{"http 404", &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}}}, cores3.ErrNotFound},
		{"provider", errors.New("secret credential failure"), cores3.ErrReadFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &trackingBody{reader: bytes.NewReader(nil)}
			client := &Client{api: &fakeAPI{getOutput: &awss3.GetObjectOutput{Body: body}, getErr: test.err}}
			_, err := client.Get(context.Background(), ref)
			if !errors.Is(err, test.sentinel) || strings.Contains(err.Error(), "secret") || !body.closed {
				t.Fatalf("error = %v, closed=%t", err, body.closed)
			}
		})
	}
}

func TestGetClosesBodyWhenSuccessfulSDKOutputIsInvalid(t *testing.T) {
	ref, _ := cores3.NewObjectRef("bucket", "key")
	body := &trackingBody{reader: bytes.NewReader(nil)}
	client := &Client{api: &fakeAPI{getOutput: &awss3.GetObjectOutput{Body: body, Metadata: map[string]string{"": "invalid"}}}}
	_, err := client.Get(context.Background(), ref)
	if !errors.Is(err, cores3.ErrReadFailed) || !body.closed {
		t.Fatalf("invalid response error = %v, closed=%t", err, body.closed)
	}
}

func TestPutForwardsRequestAndPreservesBodyOwnership(t *testing.T) {
	api := &fakeAPI{putOutput: &awss3.PutObjectOutput{ETag: sdkaws.String("etag"), VersionId: sdkaws.String("version")}}
	client := &Client{api: api}
	ref, _ := cores3.NewObjectRef("bucket", "key")
	body := bytes.NewReader([]byte("payload"))
	request, _ := cores3.NewPutRequest(cores3.PutRequestSpec{Ref: ref, Body: body, ContentType: "text/plain", Metadata: map[string]string{"a": "b"}})
	result, err := client.Put(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if api.putInput.Body != body || sdkaws.ToString(api.putInput.Bucket) != "bucket" || sdkaws.ToString(api.putInput.Key) != "key" || sdkaws.ToString(api.putInput.ContentType) != "text/plain" || api.putInput.Metadata["a"] != "b" {
		t.Fatalf("put input = %#v", api.putInput)
	}
	if etag, ok := result.ETag(); !ok || etag != "etag" {
		t.Fatalf("ETag = %q,%t", etag, ok)
	}
}

func TestHeadMapsObjectInfoAndCopiesMetadata(t *testing.T) {
	lastModified := time.Date(2026, time.July, 23, 10, 0, 0, 0, time.UTC)
	api := &fakeAPI{headOutput: &awss3.HeadObjectOutput{
		ContentLength: sdkaws.Int64(7),
		ContentType:   sdkaws.String("text/plain"),
		ETag:          sdkaws.String("etag"),
		VersionId:     sdkaws.String("version"),
		LastModified:  sdkaws.Time(lastModified),
		Metadata:      map[string]string{"a": "b"},
	}}
	client := &Client{api: api}
	ref, _ := cores3.NewObjectRef("bucket", "key")
	info, err := client.Head(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if sdkaws.ToString(api.headInput.Bucket) != "bucket" || sdkaws.ToString(api.headInput.Key) != "key" {
		t.Fatalf("head input = %#v", api.headInput)
	}
	api.headOutput.Metadata["a"] = "mutated"
	copyOne := info.Metadata()
	copyOne["a"] = "again"
	if info.ContentLength() != 7 || info.Metadata()["a"] != "b" {
		t.Fatalf("object info = %#v", info)
	}
	if contentType, ok := info.ContentType(); !ok || contentType != "text/plain" {
		t.Fatalf("content type = %q,%t", contentType, ok)
	}
	if etag, ok := info.ETag(); !ok || etag != "etag" {
		t.Fatalf("ETag = %q,%t", etag, ok)
	}
	if versionID, ok := info.VersionID(); !ok || versionID != "version" {
		t.Fatalf("version ID = %q,%t", versionID, ok)
	}
	if value, ok := info.LastModified(); !ok || !value.Equal(lastModified) {
		t.Fatalf("last modified = %v,%t", value, ok)
	}
}

func TestHeadMapsNotFoundProviderAndContextFailures(t *testing.T) {
	ref, _ := cores3.NewObjectRef("bucket", "key")
	for _, test := range []struct {
		name     string
		err      error
		sentinel error
	}{
		{"http 404", &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}}}, cores3.ErrNotFound},
		{"provider", errors.New("secret credential failure"), cores3.ErrReadFailed},
		{"context", context.Canceled, context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &Client{api: &fakeAPI{headErr: test.err}}
			_, err := client.Head(context.Background(), ref)
			if !errors.Is(err, test.sentinel) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error = %v", err)
			}
			if test.name == "provider" && !errors.Is(err, cores3.ErrReadFailed) {
				t.Fatalf("provider error = %v", err)
			}
		})
	}
}

func TestDeleteMapsReferenceAndTreatsNilOutputAsSuccess(t *testing.T) {
	api := &fakeAPI{}
	client := &Client{api: api}
	ref, _ := cores3.NewObjectRef("bucket", "key")
	if err := client.Delete(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if sdkaws.ToString(api.deleteInput.Bucket) != "bucket" || sdkaws.ToString(api.deleteInput.Key) != "key" {
		t.Fatalf("delete input = %#v", api.deleteInput)
	}
}

func TestDeleteMapsProviderAndContextFailures(t *testing.T) {
	ref, _ := cores3.NewObjectRef("bucket", "key")
	for _, test := range []struct {
		name     string
		err      error
		sentinel error
	}{
		{"provider", errors.New("secret credential failure"), cores3.ErrWriteFailed},
		{"context", context.DeadlineExceeded, context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &Client{api: &fakeAPI{deleteErr: test.err}}
			err := client.Delete(context.Background(), ref)
			if !errors.Is(err, test.sentinel) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error = %v", err)
			}
			if test.name == "provider" && !errors.Is(err, cores3.ErrWriteFailed) {
				t.Fatalf("provider error = %v", err)
			}
		})
	}
}

func TestContextIdentityAndBodyErrorProjection(t *testing.T) {
	ref, _ := cores3.NewObjectRef("bucket", "key")
	client := &Client{api: &fakeAPI{getErr: context.Canceled}}
	_, err := client.Get(context.Background(), ref)
	if !errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("context error = %v", err)
	}
	body := &trackingBody{reader: bytes.NewReader(nil), readErr: errors.New("secret read"), closeErr: errors.New("secret close")}
	client = &Client{api: &fakeAPI{getOutput: &awss3.GetObjectOutput{Body: body}}}
	result, err := client.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := result.Body().Read(make([]byte, 1))
	if !errors.Is(readErr, cores3.ErrBodyFailed) || strings.Contains(readErr.Error(), "secret") {
		t.Fatalf("read error = %v", readErr)
	}
	closeErr := result.Body().Close()
	if !errors.Is(closeErr, cores3.ErrBodyFailed) || strings.Contains(closeErr.Error(), "secret") {
		t.Fatalf("close error = %v", closeErr)
	}
}

func TestNilAndZeroInputsFailDeterministically(t *testing.T) {
	ref, _ := cores3.NewObjectRef("bucket", "key")
	request, _ := cores3.NewPutRequest(cores3.PutRequestSpec{Ref: ref, Body: bytes.NewReader(nil)})
	var client *Client
	if _, err := client.Get(context.Background(), ref); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("nil client Get error = %v", err)
	}
	if _, err := client.Put(context.Background(), request); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("nil client Put error = %v", err)
	}
	if _, err := client.Head(context.Background(), ref); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("nil client Head error = %v", err)
	}
	if err := client.Delete(context.Background(), ref); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("nil client Delete error = %v", err)
	}
	validClient := &Client{api: &fakeAPI{}}
	if _, err := validClient.Get(nil, ref); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("nil context Get error = %v", err)
	}
	if _, err := validClient.Put(context.Background(), cores3.PutRequest{}); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("zero request Put error = %v", err)
	}
	if _, err := validClient.Head(nil, ref); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("nil context Head error = %v", err)
	}
	if err := validClient.Delete(nil, ref); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("nil context Delete error = %v", err)
	}
	if _, err := validClient.Head(context.Background(), cores3.ObjectRef{}); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("zero ref Head error = %v", err)
	}
	if err := validClient.Delete(context.Background(), cores3.ObjectRef{}); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("zero ref Delete error = %v", err)
	}
}

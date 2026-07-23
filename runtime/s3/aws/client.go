package aws

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	cores3 "github.com/nxnminieye/nexa/runtime/s3"
)

// ClientOptions is frozen by NewClient. Config and credentials remain owned by
// the consumer; construction does not resolve credentials or perform I/O.
type ClientOptions struct {
	Config            sdkaws.Config
	Endpoint          string
	UsePathStyle      bool
	AllowInsecureHTTP bool
}

type s3API interface {
	GetObject(context.Context, *awss3.GetObjectInput, ...func(*awss3.Options)) (*awss3.GetObjectOutput, error)
	PutObject(context.Context, *awss3.PutObjectInput, ...func(*awss3.Options)) (*awss3.PutObjectOutput, error)
}

// Client adapts the official AWS SDK for Go v2 to the transport-neutral ports.
type Client struct {
	api     s3API
	presign presignAPI
}

func NewClient(options ClientOptions) (*Client, error) {
	if options.Config.Region == "" {
		return nil, cores3.ProjectValidation("region_required", "/config/region")
	}
	if !validRegion(options.Config.Region) {
		return nil, cores3.ProjectValidation("region_invalid", "/config/region")
	}
	if err := validateHTTPClient(options.Config.HTTPClient, options.AllowInsecureHTTP); err != nil {
		return nil, err
	}
	endpoint, err := validateEndpoint(options.Endpoint, options.AllowInsecureHTTP)
	if err != nil {
		return nil, err
	}
	config := options.Config.Copy()
	config.BaseEndpoint = nil
	config.EndpointResolver = nil
	config.EndpointResolverWithOptions = nil
	config.ConfigSources = nil
	config.APIOptions = nil
	config.Interceptors = smithyhttp.InterceptorRegistry{}
	config.ServiceOptions = nil
	var httpClientError error
	api := awss3.NewFromConfig(config, func(s3Options *awss3.Options) {
		s3Options.UsePathStyle = options.UsePathStyle
		s3Options.HTTPClient, httpClientError = hardenHTTPClient(s3Options.HTTPClient, options.AllowInsecureHTTP)
		if endpoint != "" {
			s3Options.BaseEndpoint = sdkaws.String(endpoint)
		}
	})
	if httpClientError != nil {
		return nil, httpClientError
	}
	return &Client{api: api, presign: awss3.NewPresignClient(api)}, nil
}

func validateHTTPClient(client sdkaws.HTTPClient, allowInsecure bool) error {
	if client == nil {
		return nil
	}
	if nilValue(client) {
		return cores3.ProjectValidation("http_client_invalid", "/config/httpClient")
	}
	switch typed := client.(type) {
	case *awshttp.BuildableClient:
		return nil
	case *http.Client:
		if typed.Transport == nil {
			return nil
		}
		if nilValue(typed.Transport) {
			return cores3.ProjectValidation("http_client_invalid", "/config/httpClient")
		}
		if _, ok := typed.Transport.(*http.Transport); !ok && !allowInsecure {
			return cores3.ProjectValidation("http_client_unconstrained", "/config/httpClient")
		}
		return nil
	default:
		if !allowInsecure {
			return cores3.ProjectValidation("http_client_unconstrained", "/config/httpClient")
		}
		return nil
	}
}

func hardenHTTPClient(client sdkaws.HTTPClient, allowInsecure bool) (sdkaws.HTTPClient, error) {
	if client == nil || nilValue(client) {
		return nil, cores3.ProjectValidation("http_client_invalid", "/config/httpClient")
	}
	switch typed := client.(type) {
	case *awshttp.BuildableClient:
		return newControlledHTTPClient(typed.GetTransport(), typed.GetTimeout(), nil, allowInsecure), nil
	case *http.Client:
		if typed.Transport != nil && nilValue(typed.Transport) {
			return nil, cores3.ProjectValidation("http_client_invalid", "/config/httpClient")
		}
		if typed.Transport != nil {
			if _, ok := typed.Transport.(*http.Transport); !ok && !allowInsecure {
				return nil, cores3.ProjectValidation("http_client_unconstrained", "/config/httpClient")
			}
		}
		return cloneControlledHTTPClient(typed, allowInsecure), nil
	default:
		if !allowInsecure {
			return nil, cores3.ProjectValidation("http_client_unconstrained", "/config/httpClient")
		}
		return &initialSchemeGuardHTTPClient{delegate: client, allowInsecure: true}, nil
	}
}

func newControlledHTTPClient(transport http.RoundTripper, timeout time.Duration, callerPolicy func(*http.Request, []*http.Request) error, allowInsecure bool) *http.Client {
	return &http.Client{
		Transport:     &schemeGuardRoundTripper{delegate: transport, allowInsecure: allowInsecure},
		CheckRedirect: redirectPolicy(callerPolicy, allowInsecure),
		Timeout:       timeout,
	}
}

func cloneControlledHTTPClient(client *http.Client, allowInsecure bool) *http.Client {
	controlled := *client
	transport := controlled.Transport
	if transport == nil {
		transport = awshttp.NewBuildableClient().GetTransport()
	}
	controlled.Transport = &schemeGuardRoundTripper{delegate: transport, allowInsecure: allowInsecure}
	controlled.CheckRedirect = redirectPolicy(client.CheckRedirect, allowInsecure)
	return &controlled
}

type schemeGuardRoundTripper struct {
	delegate      http.RoundTripper
	allowInsecure bool
}

func (t *schemeGuardRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := validateRequestScheme(request, t.allowInsecure); err != nil {
		return nil, err
	}
	return t.delegate.RoundTrip(request)
}

func redirectPolicy(callerPolicy func(*http.Request, []*http.Request) error, allowInsecure bool) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, via []*http.Request) error {
		if err := validateRequestScheme(request, allowInsecure); err != nil {
			return http.ErrUseLastResponse
		}
		if request.Response == nil || (request.Response.StatusCode != http.StatusTemporaryRedirect && request.Response.StatusCode != http.StatusPermanentRedirect) || len(via) == 0 || len(via) >= 10 {
			return http.ErrUseLastResponse
		}
		previous := via[len(via)-1]
		if previous.URL == nil || request.URL.Scheme != previous.URL.Scheme || !strings.EqualFold(request.URL.Host, previous.URL.Host) {
			return http.ErrUseLastResponse
		}
		if callerPolicy != nil {
			return callerPolicy(request, via)
		}
		return nil
	}
}

func validateRequestScheme(request *http.Request, allowInsecure bool) error {
	if request == nil || request.URL == nil {
		return errors.New("runtime s3 request URL invalid")
	}
	if request.URL.Scheme != "https" && !(allowInsecure && request.URL.Scheme == "http") {
		return errors.New("runtime s3 insecure HTTP request forbidden")
	}
	return nil
}

type initialSchemeGuardHTTPClient struct {
	delegate      sdkaws.HTTPClient
	allowInsecure bool
}

func (c *initialSchemeGuardHTTPClient) Do(request *http.Request) (*http.Response, error) {
	if err := validateRequestScheme(request, c.allowInsecure); err != nil {
		return nil, err
	}
	return c.delegate.Do(request)
}

func validRegion(region string) bool {
	if !utf8.ValidString(region) || strings.TrimSpace(region) != region {
		return false
	}
	for _, r := range region {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validateEndpoint(endpoint string, allowInsecure bool) (string, error) {
	if endpoint == "" {
		return "", nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" || !validEndpointPort(parsed.Host) || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", endpointError("endpoint_invalid")
	}
	if parsed.Scheme == "http" && !allowInsecure {
		return "", endpointError("insecure_http_forbidden")
	}
	return endpoint, nil
}

func validEndpointPort(host string) bool {
	port := ""
	if strings.HasPrefix(host, "[") {
		closing := strings.LastIndexByte(host, ']')
		if closing < 0 {
			return false
		}
		if closing == len(host)-1 {
			return true
		}
		if closing+1 >= len(host) || host[closing+1] != ':' {
			return false
		}
		port = host[closing+2:]
	} else {
		if strings.Count(host, ":") == 0 {
			return true
		}
		if strings.Count(host, ":") != 1 {
			return false
		}
		port = host[strings.LastIndexByte(host, ':')+1:]
	}
	if port == "" {
		return false
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	value, err := strconv.ParseUint(port, 10, 16)
	return err == nil && value >= 1 && value <= 65535
}

func endpointError(reason string) error {
	return cores3.ProjectValidation(reason, "/endpoint")
}

func (c *Client) Get(ctx context.Context, ref cores3.ObjectRef) (cores3.ReadResult, error) {
	if nilValue(ctx) {
		return cores3.ReadResult{}, cores3.ProjectValidation("context_nil", "/context")
	}
	if !ref.Valid() {
		return cores3.ReadResult{}, cores3.ProjectValidation("object_ref_invalid", "/ref")
	}
	if c == nil || nilValue(c.api) {
		return cores3.ReadResult{}, cores3.ProjectValidation("client_invalid", "/client")
	}
	output, err := c.api.GetObject(ctx, &awss3.GetObjectInput{Bucket: sdkaws.String(ref.Bucket()), Key: sdkaws.String(ref.Key())})
	if err != nil {
		closeOutput(output)
		if contextError := projectedContext(ctx, err); contextError != nil {
			return cores3.ReadResult{}, cores3.ProjectReadFailure("get_canceled", contextError)
		}
		if isNotFound(err) {
			return cores3.ReadResult{}, cores3.ProjectNotFound()
		}
		return cores3.ReadResult{}, cores3.ProjectReadFailure("get_failed", nil)
	}
	if output == nil || nilValue(output.Body) {
		closeOutput(output)
		return cores3.ReadResult{}, cores3.ProjectReadFailure("response_body_nil", nil)
	}
	result, resultErr := cores3.NewReadResult(cores3.ReadResultSpec{
		Body:          &projectedBody{body: output.Body},
		ContentType:   sdkaws.ToString(output.ContentType),
		ContentLength: sdkaws.ToInt64(output.ContentLength),
		ETag:          sdkaws.ToString(output.ETag),
		VersionID:     sdkaws.ToString(output.VersionId),
		LastModified:  sdkaws.ToTime(output.LastModified),
		Metadata:      output.Metadata,
	})
	if resultErr != nil {
		_ = output.Body.Close()
		return cores3.ReadResult{}, cores3.ProjectReadFailure("response_invalid", nil)
	}
	return result, nil
}

func (c *Client) Put(ctx context.Context, request cores3.PutRequest) (cores3.WriteResult, error) {
	if nilValue(ctx) {
		return cores3.WriteResult{}, cores3.ProjectValidation("context_nil", "/context")
	}
	if !request.Valid() {
		return cores3.WriteResult{}, cores3.ProjectValidation("request_invalid", "/request")
	}
	if c == nil || nilValue(c.api) {
		return cores3.WriteResult{}, cores3.ProjectValidation("client_invalid", "/client")
	}
	ref := request.Ref()
	input := &awss3.PutObjectInput{
		Bucket:   sdkaws.String(ref.Bucket()),
		Key:      sdkaws.String(ref.Key()),
		Body:     request.Body(),
		Metadata: request.Metadata(),
	}
	if contentType, ok := request.ContentType(); ok {
		input.ContentType = sdkaws.String(contentType)
	}
	output, err := c.api.PutObject(ctx, input)
	if err != nil {
		if contextError := projectedContext(ctx, err); contextError != nil {
			return cores3.WriteResult{}, cores3.ProjectWriteFailure("put_canceled", contextError)
		}
		return cores3.WriteResult{}, cores3.ProjectWriteFailure("put_failed", nil)
	}
	if output == nil {
		return cores3.WriteResult{}, cores3.ProjectWriteFailure("response_nil", nil)
	}
	return cores3.NewWriteResult(cores3.WriteResultSpec{ETag: sdkaws.ToString(output.ETag), VersionID: sdkaws.ToString(output.VersionId)}), nil
}

func projectedContext(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}

func isNotFound(err error) bool {
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var responseError *smithyhttp.ResponseError
	if errors.As(err, &responseError) && responseError.HTTPStatusCode() == 404 {
		return true
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		return apiError.ErrorCode() == "NoSuchKey" || apiError.ErrorCode() == "NotFound"
	}
	return false
}

func closeOutput(output *awss3.GetObjectOutput) {
	if output != nil && !nilValue(output.Body) {
		_ = output.Body.Close()
	}
}

func nilValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

type projectedBody struct {
	body io.ReadCloser
}

func (b *projectedBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if err == nil || err == io.EOF {
		return n, err
	}
	return n, cores3.ProjectBodyFailure("body_read_failed")
}

func (b *projectedBody) Close() error {
	if err := b.body.Close(); err != nil {
		return cores3.ProjectBodyFailure("body_close_failed")
	}
	return nil
}

var _ cores3.Store = (*Client)(nil)

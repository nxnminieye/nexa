package aws

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	cores3 "github.com/nxnminieye/nexa/runtime/s3"
)

type fakePresignAPI struct {
	putCalls      int
	putInput      *awss3.PutObjectInput
	putOptions    awss3.PresignOptions
	putOutput     *v4.PresignedHTTPRequest
	putErr        error
	getCalls      int
	getInput      *awss3.GetObjectInput
	getOptions    awss3.PresignOptions
	getOutput     *v4.PresignedHTTPRequest
	getErr        error
	headCalls     int
	headInput     *awss3.HeadObjectInput
	headOptions   awss3.PresignOptions
	headOutput    *v4.PresignedHTTPRequest
	headErr       error
	deleteCalls   int
	deleteInput   *awss3.DeleteObjectInput
	deleteOptions awss3.PresignOptions
	deleteOutput  *v4.PresignedHTTPRequest
	deleteErr     error
}

func (f *fakePresignAPI) PresignPutObject(_ context.Context, input *awss3.PutObjectInput, options ...func(*awss3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	f.putCalls++
	f.putInput = input
	for _, option := range options {
		option(&f.putOptions)
	}
	return f.putOutput, f.putErr
}

func (f *fakePresignAPI) PresignGetObject(_ context.Context, input *awss3.GetObjectInput, options ...func(*awss3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	f.getCalls++
	f.getInput = input
	for _, option := range options {
		option(&f.getOptions)
	}
	return f.getOutput, f.getErr
}

func (f *fakePresignAPI) PresignHeadObject(_ context.Context, input *awss3.HeadObjectInput, options ...func(*awss3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	f.headCalls++
	f.headInput = input
	for _, option := range options {
		option(&f.headOptions)
	}
	return f.headOutput, f.headErr
}

func (f *fakePresignAPI) PresignDeleteObject(_ context.Context, input *awss3.DeleteObjectInput, options ...func(*awss3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	f.deleteCalls++
	f.deleteInput = input
	for _, option := range options {
		option(&f.deleteOptions)
	}
	return f.deleteOutput, f.deleteErr
}

func TestPresignUploadMapsRequestAndExpiration(t *testing.T) {
	fake := &fakePresignAPI{putOutput: &v4.PresignedHTTPRequest{URL: "https://objects.example.test/bucket/key?signature=value", SignedHeader: http.Header{"Content-Type": {"application/json"}}}}
	client := &Client{presign: fake}
	ref, _ := cores3.NewObjectRef("bucket", "key")
	request, _ := cores3.NewPresignUploadRequest(cores3.PresignUploadRequestSpec{Ref: ref, ContentType: "application/json", Expires: 7 * time.Minute})
	result, err := client.PresignUpload(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if sdkaws.ToString(fake.putInput.Bucket) != "bucket" || sdkaws.ToString(fake.putInput.Key) != "key" || sdkaws.ToString(fake.putInput.ContentType) != "application/json" || fake.putOptions.Expires != 7*time.Minute {
		t.Fatalf("put mapping = %#v, expires=%s", fake.putInput, fake.putOptions.Expires)
	}
	if result.URL() != "https://objects.example.test/bucket/key?signature=value" {
		t.Fatalf("URL = %q", result.URL())
	}
	if http.Header(result.Headers()).Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %#v", result.Headers())
	}
}

func TestPresignDownloadMapsRequestAndExpiration(t *testing.T) {
	fake := &fakePresignAPI{getOutput: &v4.PresignedHTTPRequest{URL: "https://objects.example.test/bucket/key?signature=value"}}
	client := &Client{presign: fake}
	ref, _ := cores3.NewObjectRef("bucket", "key")
	request, _ := cores3.NewPresignDownloadRequest(cores3.PresignDownloadRequestSpec{Ref: ref, Expires: 3 * time.Minute})
	result, err := client.PresignDownload(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if sdkaws.ToString(fake.getInput.Bucket) != "bucket" || sdkaws.ToString(fake.getInput.Key) != "key" || fake.getOptions.Expires != 3*time.Minute || result.URL() == "" {
		t.Fatalf("get mapping = %#v, expires=%s, URL=%q", fake.getInput, fake.getOptions.Expires, result.URL())
	}
}

func TestPresignHeadAndDeleteMapRequestExpirationAndHeaders(t *testing.T) {
	fake := &fakePresignAPI{
		headOutput:   &v4.PresignedHTTPRequest{URL: "https://objects.example.test/bucket/key?head-signature=value", SignedHeader: http.Header{"X-Test-Head": {"required"}}},
		deleteOutput: &v4.PresignedHTTPRequest{URL: "https://objects.example.test/bucket/key?delete-signature=value", SignedHeader: http.Header{"X-Test-Delete": {"required"}}},
	}
	client := &Client{presign: fake}
	ref, _ := cores3.NewObjectRef("bucket", "key")
	headRequest, _ := cores3.NewPresignHeadRequest(cores3.PresignHeadRequestSpec{Ref: ref, Expires: 2 * time.Minute})
	headResult, err := client.PresignHead(context.Background(), headRequest)
	if err != nil {
		t.Fatal(err)
	}
	deleteRequest, _ := cores3.NewPresignDeleteRequest(cores3.PresignDeleteRequestSpec{Ref: ref, Expires: 4 * time.Minute})
	deleteResult, err := client.PresignDelete(context.Background(), deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	if fake.headCalls != 1 || sdkaws.ToString(fake.headInput.Bucket) != "bucket" || sdkaws.ToString(fake.headInput.Key) != "key" || fake.headOptions.Expires != 2*time.Minute {
		t.Fatalf("head mapping = %#v, expires=%s, calls=%d", fake.headInput, fake.headOptions.Expires, fake.headCalls)
	}
	if fake.deleteCalls != 1 || sdkaws.ToString(fake.deleteInput.Bucket) != "bucket" || sdkaws.ToString(fake.deleteInput.Key) != "key" || fake.deleteOptions.Expires != 4*time.Minute {
		t.Fatalf("delete mapping = %#v, expires=%s, calls=%d", fake.deleteInput, fake.deleteOptions.Expires, fake.deleteCalls)
	}
	if headResult.URL() == "" || http.Header(headResult.Headers()).Get("X-Test-Head") != "required" {
		t.Fatalf("head result = %q %#v", headResult.URL(), headResult.Headers())
	}
	if deleteResult.URL() == "" || http.Header(deleteResult.Headers()).Get("X-Test-Delete") != "required" {
		t.Fatalf("delete result = %q %#v", deleteResult.URL(), deleteResult.Headers())
	}
}

func TestPresignUploadAWSBehavior(t *testing.T) {
	client := newSDKPresignClient(t)
	ref, _ := cores3.NewObjectRef("bucket", "key")
	request, _ := cores3.NewPresignUploadRequest(cores3.PresignUploadRequestSpec{Ref: ref, ContentType: "application/json", Expires: time.Second})
	result, err := client.PresignUpload(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(result.URL())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("X-Amz-Expires") != "1" || parsed.Path != "/bucket/key" {
		t.Fatalf("presigned upload URL = %q", result.URL())
	}
	if http.Header(result.Headers()).Get("Host") != "objects.example.test" {
		t.Fatalf("presigned upload headers = %#v", result.Headers())
	}
}

func TestPresignDownloadAWSBehavior(t *testing.T) {
	client := newSDKPresignClient(t)
	ref, _ := cores3.NewObjectRef("bucket", "key")
	request, _ := cores3.NewPresignDownloadRequest(cores3.PresignDownloadRequestSpec{Ref: ref, Expires: 7 * 24 * time.Hour})
	result, err := client.PresignDownload(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(result.URL())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("X-Amz-Expires") != "604800" || parsed.Path != "/bucket/key" {
		t.Fatalf("presigned download URL = %q", result.URL())
	}
}

func TestPresignHeadAndDeleteAWSMethodAndURLBehavior(t *testing.T) {
	client := newSDKPresignClient(t)
	ref, _ := cores3.NewObjectRef("bucket", "key")
	headRequest, _ := cores3.NewPresignHeadRequest(cores3.PresignHeadRequestSpec{Ref: ref, Expires: time.Second})
	headResult, err := client.PresignHead(context.Background(), headRequest)
	if err != nil {
		t.Fatal(err)
	}
	deleteRequest, _ := cores3.NewPresignDeleteRequest(cores3.PresignDeleteRequestSpec{Ref: ref, Expires: 7 * 24 * time.Hour})
	deleteResult, err := client.PresignDelete(context.Background(), deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		value   string
		expires string
	}{
		{name: "head", value: headResult.URL(), expires: "1"},
		{name: "delete", value: deleteResult.URL(), expires: "604800"},
	} {
		parsed, parseErr := url.Parse(test.value)
		if parseErr != nil || parsed.Path != "/bucket/key" || parsed.Query().Get("X-Amz-Expires") != test.expires {
			t.Fatalf("presigned %s URL = %q, %v", test.name, test.value, parseErr)
		}
	}
	directHead, err := client.presign.PresignHeadObject(context.Background(), &awss3.HeadObjectInput{Bucket: sdkaws.String("bucket"), Key: sdkaws.String("key")})
	if err != nil || directHead == nil || directHead.Method != http.MethodHead {
		t.Fatalf("AWS head response = %#v, %v", directHead, err)
	}
	directDelete, err := client.presign.PresignDeleteObject(context.Background(), &awss3.DeleteObjectInput{Bucket: sdkaws.String("bucket"), Key: sdkaws.String("key")})
	if err != nil || directDelete == nil || directDelete.Method != http.MethodDelete {
		t.Fatalf("AWS delete response = %#v, %v", directDelete, err)
	}
}

func TestPresignRejectsUnsupportedExpirationBeforeSDK(t *testing.T) {
	ref, _ := cores3.NewObjectRef("bucket", "key")
	for _, expires := range []time.Duration{time.Millisecond, 1500 * time.Millisecond, 7*24*time.Hour + time.Second} {
		t.Run(expires.String(), func(t *testing.T) {
			fake := &fakePresignAPI{}
			client := &Client{presign: fake}
			upload, _ := cores3.NewPresignUploadRequest(cores3.PresignUploadRequestSpec{Ref: ref, Expires: expires})
			_, uploadErr := client.PresignUpload(context.Background(), upload)
			download, _ := cores3.NewPresignDownloadRequest(cores3.PresignDownloadRequestSpec{Ref: ref, Expires: expires})
			_, downloadErr := client.PresignDownload(context.Background(), download)
			head, _ := cores3.NewPresignHeadRequest(cores3.PresignHeadRequestSpec{Ref: ref, Expires: expires})
			_, headErr := client.PresignHead(context.Background(), head)
			deleteRequest, _ := cores3.NewPresignDeleteRequest(cores3.PresignDeleteRequestSpec{Ref: ref, Expires: expires})
			_, deleteErr := client.PresignDelete(context.Background(), deleteRequest)
			for _, err := range []error{uploadErr, downloadErr, headErr, deleteErr} {
				var typed *cores3.Error
				if !errors.Is(err, cores3.ErrValidation) || !errors.As(err, &typed) || typed.Reason() != "expires_unsupported" || typed.Pointer() != "/request/expires" {
					t.Fatalf("expiration error = %#v", err)
				}
			}
			if fake.putCalls != 0 || fake.getCalls != 0 || fake.headCalls != 0 || fake.deleteCalls != 0 {
				t.Fatalf("SDK calls = put:%d get:%d head:%d delete:%d", fake.putCalls, fake.getCalls, fake.headCalls, fake.deleteCalls)
			}
		})
	}
}

func newSDKPresignClient(t *testing.T) *Client {
	t.Helper()
	client, err := NewClient(ClientOptions{
		Config:       sdkaws.Config{Region: "us-east-1", Credentials: staticCredentials{}},
		Endpoint:     "https://objects.example.test",
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestPresignFailuresAreSafelyProjected(t *testing.T) {
	ref, _ := cores3.NewObjectRef("bucket", "key")
	upload, _ := cores3.NewPresignUploadRequest(cores3.PresignUploadRequestSpec{Ref: ref, Expires: time.Minute})
	download, _ := cores3.NewPresignDownloadRequest(cores3.PresignDownloadRequestSpec{Ref: ref, Expires: time.Minute})
	head, _ := cores3.NewPresignHeadRequest(cores3.PresignHeadRequestSpec{Ref: ref, Expires: time.Minute})
	deleteRequest, _ := cores3.NewPresignDeleteRequest(cores3.PresignDeleteRequestSpec{Ref: ref, Expires: time.Minute})
	secret := errors.New("provider https://secret.example.test access-key credential")
	for _, test := range []struct {
		name string
		call func(*Client) error
	}{
		{name: "upload provider", call: func(client *Client) error { _, err := client.PresignUpload(context.Background(), upload); return err }},
		{name: "download provider", call: func(client *Client) error {
			_, err := client.PresignDownload(context.Background(), download)
			return err
		}},
		{name: "head provider", call: func(client *Client) error {
			_, err := client.PresignHead(context.Background(), head)
			return err
		}},
		{name: "delete provider", call: func(client *Client) error {
			_, err := client.PresignDelete(context.Background(), deleteRequest)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &Client{presign: &fakePresignAPI{putErr: secret, getErr: secret, headErr: secret, deleteErr: secret}}
			err := test.call(client)
			var typed *cores3.Error
			if !errors.Is(err, cores3.ErrPresignFailed) || !errors.As(err, &typed) || typed.Code() != "presign_failed" || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "credential") {
				t.Fatalf("projected error = %#v", err)
			}
		})
	}
	client := &Client{presign: &fakePresignAPI{putErr: context.Canceled}}
	_, err := client.PresignUpload(context.Background(), upload)
	if !errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("context projection = %v", err)
	}
	client = &Client{presign: &fakePresignAPI{headErr: context.Canceled, deleteErr: context.DeadlineExceeded}}
	_, err = client.PresignHead(context.Background(), head)
	if !errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("head context projection = %v", err)
	}
	_, err = client.PresignDelete(context.Background(), deleteRequest)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("delete context projection = %v", err)
	}
}

func TestPresignInvalidResponseAndInputs(t *testing.T) {
	ref, _ := cores3.NewObjectRef("bucket", "key")
	upload, _ := cores3.NewPresignUploadRequest(cores3.PresignUploadRequestSpec{Ref: ref, Expires: time.Minute})
	client := &Client{presign: &fakePresignAPI{}}
	_, err := client.PresignUpload(context.Background(), upload)
	if !errors.Is(err, cores3.ErrPresignFailed) {
		t.Fatalf("nil response error = %v", err)
	}
	if _, err := client.PresignUpload(nil, upload); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("nil context error = %v", err)
	}
	if _, err := client.PresignDownload(context.Background(), cores3.PresignDownloadRequest{}); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("zero request error = %v", err)
	}
	if _, err := client.PresignHead(context.Background(), cores3.PresignHeadRequest{}); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("zero head request error = %v", err)
	}
	if _, err := client.PresignDelete(context.Background(), cores3.PresignDeleteRequest{}); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("zero delete request error = %v", err)
	}
	var nilClient *Client
	head, _ := cores3.NewPresignHeadRequest(cores3.PresignHeadRequestSpec{Ref: ref, Expires: time.Minute})
	if _, err := nilClient.PresignHead(context.Background(), head); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("nil client head error = %v", err)
	}
	deleteRequest, _ := cores3.NewPresignDeleteRequest(cores3.PresignDeleteRequestSpec{Ref: ref, Expires: time.Minute})
	if _, err := nilClient.PresignDelete(context.Background(), deleteRequest); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("nil client delete error = %v", err)
	}
	if _, err := client.PresignHead(nil, head); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("nil context head error = %v", err)
	}
	if _, err := client.PresignDelete(nil, deleteRequest); !errors.Is(err, cores3.ErrValidation) {
		t.Fatalf("nil context delete error = %v", err)
	}
}

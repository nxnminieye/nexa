package aws

import (
	"context"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	cores3 "github.com/nxnminieye/nexa/runtime/s3"
)

type presignAPI interface {
	PresignPutObject(context.Context, *awss3.PutObjectInput, ...func(*awss3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
	PresignGetObject(context.Context, *awss3.GetObjectInput, ...func(*awss3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

func (c *Client) PresignUpload(ctx context.Context, request cores3.PresignUploadRequest) (cores3.PresignResult, error) {
	if nilValue(ctx) {
		return cores3.PresignResult{}, cores3.ProjectValidation("context_nil", "/context")
	}
	if !request.Valid() {
		return cores3.PresignResult{}, cores3.ProjectValidation("request_invalid", "/request")
	}
	if err := validatePresignExpires(request.Expires()); err != nil {
		return cores3.PresignResult{}, err
	}
	if c == nil || nilValue(c.presign) {
		return cores3.PresignResult{}, cores3.ProjectValidation("client_invalid", "/client")
	}
	ref := request.Ref()
	input := &awss3.PutObjectInput{Bucket: sdkaws.String(ref.Bucket()), Key: sdkaws.String(ref.Key())}
	if contentType, ok := request.ContentType(); ok {
		input.ContentType = sdkaws.String(contentType)
	}
	output, err := c.presign.PresignPutObject(ctx, input, func(options *awss3.PresignOptions) {
		options.Expires = request.Expires()
	})
	if err != nil {
		if contextError := projectedContext(ctx, err); contextError != nil {
			return cores3.PresignResult{}, cores3.ProjectPresignFailure("presign_upload_canceled", contextError)
		}
		return cores3.PresignResult{}, cores3.ProjectPresignFailure("presign_upload_failed", nil)
	}
	return projectPresignResult(output)
}

func (c *Client) PresignDownload(ctx context.Context, request cores3.PresignDownloadRequest) (cores3.PresignResult, error) {
	if nilValue(ctx) {
		return cores3.PresignResult{}, cores3.ProjectValidation("context_nil", "/context")
	}
	if !request.Valid() {
		return cores3.PresignResult{}, cores3.ProjectValidation("request_invalid", "/request")
	}
	if err := validatePresignExpires(request.Expires()); err != nil {
		return cores3.PresignResult{}, err
	}
	if c == nil || nilValue(c.presign) {
		return cores3.PresignResult{}, cores3.ProjectValidation("client_invalid", "/client")
	}
	ref := request.Ref()
	output, err := c.presign.PresignGetObject(ctx, &awss3.GetObjectInput{Bucket: sdkaws.String(ref.Bucket()), Key: sdkaws.String(ref.Key())}, func(options *awss3.PresignOptions) {
		options.Expires = request.Expires()
	})
	if err != nil {
		if contextError := projectedContext(ctx, err); contextError != nil {
			return cores3.PresignResult{}, cores3.ProjectPresignFailure("presign_download_canceled", contextError)
		}
		return cores3.PresignResult{}, cores3.ProjectPresignFailure("presign_download_failed", nil)
	}
	return projectPresignResult(output)
}

func projectPresignResult(output *v4.PresignedHTTPRequest) (cores3.PresignResult, error) {
	if output == nil {
		return cores3.PresignResult{}, cores3.ProjectPresignFailure("presign_response_invalid", nil)
	}
	result, err := cores3.NewPresignResultWithHeaders(output.URL, map[string][]string(output.SignedHeader))
	if err != nil {
		return cores3.PresignResult{}, cores3.ProjectPresignFailure("presign_response_invalid", nil)
	}
	return result, nil
}

func validatePresignExpires(expires time.Duration) error {
	const maximum = 7 * 24 * time.Hour
	if expires < time.Second || expires > maximum || expires%time.Second != 0 {
		return cores3.ProjectValidation("expires_unsupported", "/request/expires")
	}
	return nil
}

var _ cores3.Presigner = (*Client)(nil)

package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/continuity-lab/continuity-lab/internal/config"
)

type S3 struct {
	client *s3.Client
	bucket string
}

func NewS3(ctx context.Context, cfg config.Config) (*S3, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.S3Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("load S3 config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(cfg.S3Endpoint)
		options.UsePathStyle = cfg.S3ForcePathStyle
	})
	return &S3{client: client, bucket: cfg.Bucket}, nil
}

func (s *S3) EnsureBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	if err == nil {
		return nil
	}
	_, createErr := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)})
	if createErr != nil && statusCode(createErr) != http.StatusConflict {
		return mapError(createErr)
	}
	return nil
}

func (s *S3) Get(ctx context.Context, key string) (Object, error) {
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return Object{}, mapError(err)
	}
	return Object{Key: key, Body: output.Body, ETag: ETag(aws.ToString(output.ETag)), Size: aws.ToInt64(output.ContentLength), ContentType: aws.ToString(output.ContentType)}, nil
}

func (s *S3) GetIfChanged(ctx context.Context, key string, known ETag) (*Object, bool, error) {
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), IfNoneMatch: aws.String(string(known))})
	if err != nil {
		if statusCode(err) == http.StatusNotModified {
			return nil, true, nil
		}
		return nil, false, mapError(err)
	}
	return &Object{Key: key, Body: output.Body, ETag: ETag(aws.ToString(output.ETag)), Size: aws.ToInt64(output.ContentLength), ContentType: aws.ToString(output.ContentType)}, false, nil
}

func (s *S3) Head(ctx context.Context, key string) (ETag, int64, error) {
	output, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return "", 0, mapError(err)
	}
	return ETag(aws.ToString(output.ETag)), aws.ToInt64(output.ContentLength), nil
}

func (s *S3) PutImmutable(ctx context.Context, key string, body io.Reader, size int64, contentType string) (bool, ETag, error) {
	output, err := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: body, ContentLength: aws.Int64(size), ContentType: aws.String(contentType), IfNoneMatch: aws.String("*")})
	if err != nil {
		if statusCode(err) == http.StatusPreconditionFailed {
			etag, _, headErr := s.Head(ctx, key)
			return false, etag, headErr
		}
		return false, "", mapError(err)
	}
	return true, ETag(aws.ToString(output.ETag)), nil
}

func (s *S3) PutIfMatch(ctx context.Context, key string, expected ETag, body io.Reader, size int64, contentType string) (ETag, error) {
	output, err := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: body, ContentLength: aws.Int64(size), ContentType: aws.String(contentType), IfMatch: aws.String(string(expected))})
	if err != nil {
		return "", mapError(err)
	}
	return ETag(aws.ToString(output.ETag)), nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	return mapError(err)
}

func (s *S3) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	var out []ObjectInfo
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix)})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, mapError(err)
		}
		for _, item := range page.Contents {
			out = append(out, ObjectInfo{Key: aws.ToString(item.Key), ETag: ETag(aws.ToString(item.ETag)), Size: aws.ToInt64(item.Size), LastModified: aws.ToTime(item.LastModified)})
		}
	}
	return out, nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	switch statusCode(err) {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case http.StatusPreconditionFailed:
		return fmt.Errorf("%w: %v", ErrPrecondition, err)
	case http.StatusConflict:
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) || strings.Contains(strings.ToLower(err.Error()), "connection refused") {
		return fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	return err
}

func statusCode(err error) int {
	var responseErr smithyhttpResponseError
	if errors.As(err, &responseErr) {
		return responseErr.HTTPStatusCode()
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotModified":
			return http.StatusNotModified
		case "NoSuchKey", "NotFound", "NoSuchBucket":
			return http.StatusNotFound
		case "PreconditionFailed":
			return http.StatusPreconditionFailed
		case "Conflict", "ConditionalRequestConflict", "BucketAlreadyOwnedByYou":
			return http.StatusConflict
		}
	}
	return 0
}

type smithyhttpResponseError interface {
	error
	HTTPStatusCode() int
}

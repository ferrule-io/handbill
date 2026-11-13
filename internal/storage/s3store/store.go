// Package s3store implements the S3 storage adapter.
package s3store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/mjquinlan2000/agent-dash/internal/config"
	"github.com/mjquinlan2000/agent-dash/internal/storage/storagetype"
)

// Store implements storagetype.Store on AWS S3 or S3-compatible backends.
type Store struct {
	client   *s3.Client
	uploader *manager.Uploader
	bucket   string
	prefix   string
}

// New creates an S3 store from configuration.
func New(ctx context.Context, cfg config.S3) (*Store, error) {
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	var optFns []func(*awsconfig.LoadOptions) error
	optFns = append(optFns, awsconfig.WithRegion(region))

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return nil, fmt.Errorf("s3store: load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.ForcePathStyle
	})

	uploader := manager.NewUploader(client)

	return &Store{
		client:   client,
		uploader: uploader,
		bucket:   cfg.Bucket,
		prefix:   strings.Trim(cfg.Prefix, "/"),
	}, nil
}

func (s *Store) s3Key(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}

type countedReader struct {
	r io.Reader
	n int64
}

func (c *countedReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// Put writes an object to S3 using the manager Uploader (supports non-seekable streams).
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := storagetype.ValidKey(key); err != nil {
		return err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	bodyReader := r
	var cr *countedReader
	if size >= 0 {
		cr = &countedReader{r: io.LimitReader(r, size)}
		bodyReader = cr
	}

	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(s.s3Key(key)),
		Body:        bodyReader,
		ContentType: aws.String(contentType),
	}

	_, err := s.uploader.Upload(ctx, input)
	if err != nil {
		return fmt.Errorf("s3store: put object: %w", err)
	}

	if cr != nil && cr.n != size {
		_ = s.Delete(ctx, key)
		return fmt.Errorf("s3store: short read: read %d of %d bytes", cr.n, size)
	}

	return nil
}

// Get opens an object from S3.
func (s *Store) Get(ctx context.Context, key string) (*storagetype.Object, error) {
	if err := storagetype.ValidKey(key); err != nil {
		return nil, err
	}

	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.s3Key(key)),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, storagetype.ErrNotFound
		}
		return nil, fmt.Errorf("s3store: get object: %w", err)
	}

	var size int64
	if resp.ContentLength != nil {
		size = *resp.ContentLength
	}
	var contentType string
	if resp.ContentType != nil {
		contentType = *resp.ContentType
	}

	return &storagetype.Object{
		Info: storagetype.Info{
			Size:        size,
			ContentType: contentType,
		},
		Body: resp.Body,
	}, nil
}

// Stat retrieves object metadata from S3.
func (s *Store) Stat(ctx context.Context, key string) (storagetype.Info, error) {
	if err := storagetype.ValidKey(key); err != nil {
		return storagetype.Info{}, err
	}

	resp, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.s3Key(key)),
	})
	if err != nil {
		if isS3NotFound(err) {
			return storagetype.Info{}, storagetype.ErrNotFound
		}
		return storagetype.Info{}, fmt.Errorf("s3store: stat object: %w", err)
	}

	var size int64
	if resp.ContentLength != nil {
		size = *resp.ContentLength
	}
	var contentType string
	if resp.ContentType != nil {
		contentType = *resp.ContentType
	}

	return storagetype.Info{
		Size:        size,
		ContentType: contentType,
	}, nil
}

// Delete removes an object from S3.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := storagetype.ValidKey(key); err != nil {
		return err
	}

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.s3Key(key)),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil
		}
		return fmt.Errorf("s3store: delete object: %w", err)
	}
	return nil
}

// DeletePrefix removes every object whose key starts with prefix.
func (s *Store) DeletePrefix(ctx context.Context, prefix string) error {
	if err := storagetype.ValidPrefix(prefix); err != nil {
		return err
	}

	fullPrefix := prefix
	if s.prefix != "" {
		fullPrefix = s.prefix + "/" + prefix
	}

	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(fullPrefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("s3store: list objects: %w", err)
		}
		if len(page.Contents) == 0 {
			continue
		}

		const batchSize = 1000
		for i := 0; i < len(page.Contents); i += batchSize {
			end := i + batchSize
			if end > len(page.Contents) {
				end = len(page.Contents)
			}
			chunk := page.Contents[i:end]
			toDelete := make([]types.ObjectIdentifier, len(chunk))
			for j, obj := range chunk {
				toDelete[j] = types.ObjectIdentifier{Key: obj.Key}
			}

			delOutput, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(s.bucket),
				Delete: &types.Delete{
					Objects: toDelete,
					Quiet:   aws.Bool(true),
				},
			})
			if err != nil {
				return fmt.Errorf("s3store: delete objects batch: %w", err)
			}
			if len(delOutput.Errors) > 0 {
				msg := "unknown error"
				if delOutput.Errors[0].Message != nil {
					msg = *delOutput.Errors[0].Message
				}
				return fmt.Errorf("s3store: delete objects failed for %d keys: %s", len(delOutput.Errors), msg)
			}
		}
	}

	return nil
}

// Ping checks that the S3 bucket exists and is reachable.
func (s *Store) Ping(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s.bucket),
	})
	if err != nil {
		return fmt.Errorf("s3store: ping bucket %q: %w", s.bucket, err)
	}
	return nil
}

func isS3NotFound(err error) bool {
	if err == nil {
		return false
	}
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		if code == "NoSuchKey" || code == "NotFound" {
			return true
		}
	}
	return false
}

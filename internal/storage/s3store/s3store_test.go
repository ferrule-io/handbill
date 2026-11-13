package s3store_test

import (
	"context"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/mjquinlan2000/agent-dash/internal/config"
	"github.com/mjquinlan2000/agent-dash/internal/storage"
	"github.com/mjquinlan2000/agent-dash/internal/storage/s3store"
	"github.com/mjquinlan2000/agent-dash/internal/storage/storagetest"
)

func TestS3(t *testing.T) {
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		endpoint = os.Getenv("S3_ENDPOINT")
	}
	if endpoint == "" {
		t.Skip("TEST_S3_ENDPOINT not set; skipping S3 tests")
	}

	ctx := context.Background()
	bucket := "agent-dash-test"

	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		_ = os.Setenv("AWS_ACCESS_KEY_ID", "minioadmin")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		_ = os.Setenv("AWS_SECRET_ACCESS_KEY", "minioadmin")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			os.Getenv("AWS_ACCESS_KEY_ID"),
			os.Getenv("AWS_SECRET_ACCESS_KEY"),
			"",
		)),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	// Ensure bucket exists in MinIO
	_, _ = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})

	storagetest.Run(t, func(t *testing.T) storage.Store {
		prefix := "test-" + uuid.NewString()
		s, err := s3store.New(ctx, config.S3{
			Bucket:         bucket,
			Region:         "us-east-1",
			Endpoint:       endpoint,
			Prefix:         prefix,
			ForcePathStyle: true,
		})
		if err != nil {
			t.Fatalf("s3store.New: %v", err)
		}
		return s
	})
}

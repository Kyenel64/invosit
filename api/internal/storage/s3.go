package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// s3Storage implements Storage against any S3-compatible object store
// (AWS S3, Cloudflare R2, etc)
type s3Storage struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

func newS3Storage(cfg Config) (*s3Storage, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("storage: bucket is required")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("storage: access key and secret key are required")
	}
	if cfg.Region == "" {
		return nil, errors.New("storage: region is required")
	}

	awsCfg := aws.Config{
		Region:      cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	})

	return &s3Storage{
		client:  client,
		presign: s3.NewPresignClient(client),
		bucket:  cfg.Bucket,
	}, nil
}

func (s *s3Storage) SignedPutURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if err := validateExpiry(expiry); err != nil {
		return "", err
	}

	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	},
		s3.WithPresignExpires(expiry),
	)
	if err != nil {
		return "", fmt.Errorf("presign put %s: %w", key, err)
	}

	return req.URL, nil
}

func (s *s3Storage) SignedGetURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if err := validateExpiry(expiry); err != nil {
		return "", err
	}

	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	},
		s3.WithPresignExpires(expiry),
	)
	if err != nil {
		return "", fmt.Errorf("presign get %s: %w", key, err)
	}

	return req.URL, nil
}

func (s *s3Storage) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

func (s *s3Storage) List(ctx context.Context, prefix string, fn func(Object) error) error {
	input := &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket)}
	if prefix != "" {
		input.Prefix = aws.String(prefix)
	}

	paginator := s3.NewListObjectsV2Paginator(s.client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list objects: %w", err)
		}
		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}
			out := Object{Key: *obj.Key}
			if obj.Size != nil {
				out.Size = *obj.Size
			}
			if obj.LastModified != nil {
				out.LastModified = *obj.LastModified
			}
			if err := fn(out); err != nil {
				return err
			}
		}
	}
	return nil
}

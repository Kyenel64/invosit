package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const MaxSignedURLExpiry = 15 * time.Minute

var ErrExpiryTooLong = errors.New("storage: signed URL expiry exceeds maximum")
var ErrUnknownProvider = errors.New("storage: unknown provider")

// Object is the metadata for a stored blob, as returned by List.
type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

type Storage interface {
	SignedPutURL(ctx context.Context, key string, expiry time.Duration) (string, error)
	SignedGetURL(ctx context.Context, key string, expiry time.Duration) (string, error)

	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string, fn func(Object) error) error
}

type Config struct {
	Provider     string // "r2" | "s3"
	Bucket       string
	Endpoint     string // empty for AWS, required for R2
	AccessKey    string
	SecretKey    string
	Region       string // real AWS region for s3, "auto" for r2
	UsePathStyle bool   // true for R2, false for S3
}

func New(cfg Config) (Storage, error) {
	if cfg.Provider == "" {
		cfg.Provider = "r2"
	}

	switch cfg.Provider {
	case "r2":
		if cfg.Endpoint == "" {
			return nil, errors.New("storage: STORAGE_ENDPOINT is required for r2")
		}
		if cfg.Region == "" {
			cfg.Region = "auto"
		}
		cfg.UsePathStyle = true
		return newS3Storage(cfg)

	case "s3":
		cfg.UsePathStyle = false
		return newS3Storage(cfg)

	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, cfg.Provider)
	}
}

func validateExpiry(d time.Duration) error {
	if d <= 0 {
		return errors.New("storage: expiry must be positive")
	}
	if d > MaxSignedURLExpiry {
		return ErrExpiryTooLong
	}
	return nil
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kyenel64/invosit/api/internal/db"
	"github.com/kyenel64/invosit/api/internal/storage"
	"github.com/kyenel64/invosit/api/internal/sweep"
)

func main() {
	if err := run(context.Background(), os.Getenv, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Runs one sweep pass against the database and blob store, reporting per-run counts.
func run(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer) error {
	databaseURL := getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}

	ttl := sweep.DefaultTTL
	if raw := getenv("SWEEP_TTL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("failed to parse SWEEP_TTL: %w", err)
		}
		ttl = parsed
	}

	blobs, err := storage.New(storage.Config{
		Provider:  getenv("STORAGE_PROVIDER"),
		Bucket:    getenv("STORAGE_BUCKET"),
		Endpoint:  getenv("STORAGE_ENDPOINT"),
		AccessKey: getenv("STORAGE_ACCESS_KEY"),
		SecretKey: getenv("STORAGE_SECRET_KEY"),
		Region:    getenv("STORAGE_REGION"),
	})
	if err != nil {
		return fmt.Errorf("failed to init storage: %w", err)
	}

	database, err := db.Open(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("failed to open db: %w", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			_, _ = fmt.Fprintf(stderr, "closing db: %v\n", err)
		}
	}()

	res, err := sweep.Run(ctx, database, blobs, ttl)
	if err != nil {
		return fmt.Errorf("failed to run sweep: %w", err)
	}

	_, _ = fmt.Fprintf(stdout, "sweep complete: stale_pending_deleted=%d blobs_deleted=%d bytes_reclaimed=%d\n",
		res.StalePendingDeleted, res.BlobsDeleted, res.BytesReclaimed)
	return nil
}

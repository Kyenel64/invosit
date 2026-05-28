package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/kyenel64/invosit/api/internal/db"
	"github.com/kyenel64/invosit/api/internal/sweep"
)

func main() {
	if err := run(context.Background(), os.Getenv, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Runs one sweep pass against the database and reports the per-run counts.
func run(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer) error {
	databaseURL := getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
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

	res, err := sweep.Run(ctx, database, sweep.DefaultPendingTTL)
	if err != nil {
		return fmt.Errorf("failed to run sweep: %w", err)
	}

	_, _ = fmt.Fprintf(stdout, "sweep complete: stale_pending_deleted=%d\n", res.StalePendingDeleted)
	return nil
}

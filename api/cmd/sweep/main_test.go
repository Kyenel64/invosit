package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kyenel64/invosit/api/internal/storage"
)

func TestRunRejectsUnsafeSweepTTL(t *testing.T) {
	cases := []struct {
		name string
		ttl  string
	}{
		{"zero", "0s"},
		{"negative", "-1h"},
		{"below signed url expiry", "5m"},
		{"at signed url expiry", storage.MaxSignedURLExpiry.String()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{
				"DATABASE_URL": "postgres://localhost/invosit",
				"SWEEP_TTL":    tc.ttl,
			}
			err := run(context.Background(), func(key string) string { return env[key] }, io.Discard, io.Discard)
			if err == nil {
				t.Fatalf("expected error for SWEEP_TTL=%q, got nil", tc.ttl)
			}
			if !strings.Contains(err.Error(), "SWEEP_TTL must be greater than") {
				t.Fatalf("expected SWEEP_TTL validation error, got %v", err)
			}
		})
	}
}

func TestRunRequiresDatabaseURL(t *testing.T) {
	err := run(context.Background(), func(string) string { return "" }, io.Discard, io.Discard)
	if err == nil || !errors.Is(err, errMissingDatabaseURL) {
		t.Fatalf("expected missing DATABASE_URL error, got %v", err)
	}
}

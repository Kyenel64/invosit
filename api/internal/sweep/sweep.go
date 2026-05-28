package sweep

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DefaultPendingTTL must exceed storage.MaxSignedURLExpiry (15m) by a safe
// margin so an upload finishing near the signed-URL deadline isn't raced.
const DefaultPendingTTL = time.Hour

type Result struct {
	StalePendingDeleted int64
}

// Run executes one sweep pass: delete pending_files rows older than pendingTTL.
// Idempotent
func Run(ctx context.Context, db *sql.DB, pendingTTL time.Duration) (Result, error) {
	cutoff := time.Now().UTC().Add(-pendingTTL)

	res, err := db.ExecContext(ctx,
		`DELETE FROM pending_files WHERE pushed_at < $1`,
		cutoff,
	)
	if err != nil {
		return Result{}, fmt.Errorf("failed to delete stale pending files: %w", err)
	}

	deleted, err := res.RowsAffected()
	if err != nil {
		return Result{}, fmt.Errorf("failed to count deleted pending files: %w", err)
	}

	return Result{StalePendingDeleted: deleted}, nil
}

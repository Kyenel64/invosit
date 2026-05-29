package sweep

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/kyenel64/invosit/api/internal/storage"
)

// DefaultTTL must exceed storage.MaxSignedURLExpiry (15m) by a safe margin so an
// upload finishing near the signed-URL deadline isn't raced
const DefaultTTL = time.Hour

type Result struct {
	StalePendingDeleted int64
	BlobsDeleted        int64
	BytesReclaimed      int64
}

// Run executes one sweep pass, idempotent and safe to run repeatedly:
//  1. delete pending_files rows older than ttl;
//  2. delete blobs older than ttl that no files or pending_files row references.
func Run(ctx context.Context, db *sql.DB, blobs storage.Storage, ttl time.Duration) (Result, error) {
	cutoff := time.Now().UTC().Add(-ttl)

	res, err := db.ExecContext(ctx,
		`DELETE FROM pending_files WHERE pushed_at < $1`,
		cutoff,
	)
	if err != nil {
		return Result{}, fmt.Errorf("failed to delete stale pending files: %w", err)
	}
	pendingDeleted, err := res.RowsAffected()
	if err != nil {
		return Result{}, fmt.Errorf("failed to count deleted pending files: %w", err)
	}

	result := Result{StalePendingDeleted: pendingDeleted}

	referenced, err := referencedSet(ctx, db)
	if err != nil {
		return result, err
	}

	listErr := blobs.List(ctx, "", func(obj storage.Object) error {
		workspaceID, hash, ok := parseBlobKey(obj.Key)
		if !ok {
			log.Printf("sweep: skipping unparseable blob key %q", obj.Key)
			return nil
		}
		if obj.LastModified.After(cutoff) {
			return nil
		}
		if _, ok := referenced[obj.Key]; ok {
			return nil
		}
		// The set is a snapshot; an identical-content re-push may have enqueued a
		// pending row since it was built. Confirm against live state before the
		// destructive delete.
		stillReferenced, err := isReferenced(ctx, db, workspaceID, hash)
		if err != nil {
			log.Printf("sweep: skipping %q, reference recheck failed: %v", obj.Key, err)
			return nil
		}
		if stillReferenced {
			return nil
		}
		if err := blobs.Delete(ctx, obj.Key); err != nil {
			log.Printf("sweep: delete failed for %q: %v", obj.Key, err)
			return nil
		}
		result.BlobsDeleted++
		result.BytesReclaimed += obj.Size
		return nil
	})
	if listErr != nil {
		return result, fmt.Errorf("failed to sweep blobs: %w", listErr)
	}

	return result, nil
}

// referencedSet returns every referenced blob key (workspace_id/content_hash)
// across files and pending_files.
func referencedSet(ctx context.Context, db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT workspace_id, content_hash FROM files
		 UNION
		 SELECT workspace_id, content_hash FROM pending_files`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load referenced blobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	set := map[string]struct{}{}
	for rows.Next() {
		var workspaceID, hash string
		if err := rows.Scan(&workspaceID, &hash); err != nil {
			return nil, fmt.Errorf("failed to scan referenced blob: %w", err)
		}
		set[workspaceID+"/"+hash] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate referenced blobs: %w", err)
	}
	return set, nil
}

func isReferenced(ctx context.Context, db *sql.DB, workspaceID, hash string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS(
		    SELECT 1 FROM files         WHERE workspace_id = $1 AND content_hash = $2
		    UNION ALL
		    SELECT 1 FROM pending_files WHERE workspace_id = $1 AND content_hash = $2
		 )`,
		workspaceID, hash,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to recheck blob reference: %w", err)
	}
	return exists, nil
}

func parseBlobKey(key string) (workspaceID, hash string, ok bool) {
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	workspaceID, hash = parts[0], parts[1]
	if !strings.HasPrefix(workspaceID, "ws_") || len(workspaceID) <= len("ws_") {
		return "", "", false
	}
	if !isSha256Hex(hash) {
		return "", "", false
	}
	return workspaceID, hash, true
}

func isSha256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

package sweep

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// cutoffNear matches a time argument that is within a minute of now-ttl,
// confirming Run applies the TTL when computing the delete cutoff.
type cutoffNear struct{ ttl time.Duration }

func (c cutoffNear) Match(v driver.Value) bool {
	cutoff, ok := v.(time.Time)
	if !ok {
		return false
	}
	want := time.Now().UTC().Add(-c.ttl)
	diff := want.Sub(cutoff)
	if diff < 0 {
		diff = -diff
	}
	return diff < time.Minute
}

func TestRun_DeletesStalePending(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	ttl := time.Hour
	mock.ExpectExec(`DELETE FROM pending_files WHERE pushed_at < \$1`).
		WithArgs(cutoffNear{ttl: ttl}).
		WillReturnResult(sqlmock.NewResult(0, 3))

	res, err := Run(context.Background(), db, ttl)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.StalePendingDeleted != 3 {
		t.Errorf("StalePendingDeleted = %d, want 3", res.StalePendingDeleted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRun_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`DELETE FROM pending_files WHERE pushed_at < \$1`).
		WillReturnError(errors.New("db down"))

	if _, err := Run(context.Background(), db, time.Hour); err == nil {
		t.Fatal("Run: expected error, got nil")
	}
}

func TestRun_RowsAffectedError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`DELETE FROM pending_files WHERE pushed_at < \$1`).
		WillReturnResult(sqlmock.NewErrorResult(errors.New("no RowsAffected")))

	if _, err := Run(context.Background(), db, time.Hour); err == nil {
		t.Fatal("Run: expected error, got nil")
	}
}

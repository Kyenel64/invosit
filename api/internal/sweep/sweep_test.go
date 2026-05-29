package sweep

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/kyenel64/invosit/api/internal/storage"
)

// cutoffNear matches a time argument within a minute of now-ttl, confirming Run
// applies the TTL when computing the delete cutoff.
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

// fakeStorage records Delete calls and replays a canned object listing.
type fakeStorage struct {
	objects   []storage.Object
	deleted   []string
	listErr   error
	deleteErr error
}

func (f *fakeStorage) SignedPutURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (f *fakeStorage) SignedGetURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (f *fakeStorage) Delete(_ context.Context, key string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, key)
	return nil
}
func (f *fakeStorage) List(_ context.Context, _ string, fn func(storage.Object) error) error {
	if f.listErr != nil {
		return f.listErr
	}
	for _, obj := range f.objects {
		if err := fn(obj); err != nil {
			return err
		}
	}
	return nil
}

const (
	ws    = "ws_test"
	hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hashC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	hashE = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

func key(hash string) string { return ws + "/" + hash }

const refQuery = `SELECT workspace_id, content_hash FROM files\s+UNION\s+SELECT workspace_id, content_hash FROM pending_files`

func expectPendingDelete(mock sqlmock.Sqlmock, ttl time.Duration, affected int64) {
	mock.ExpectExec(`DELETE FROM pending_files WHERE pushed_at < \$1`).
		WithArgs(cutoffNear{ttl: ttl}).
		WillReturnResult(sqlmock.NewResult(0, affected))
}

func TestRun_DeletesStalePending(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	ttl := time.Hour
	expectPendingDelete(mock, ttl, 3)
	mock.ExpectQuery(refQuery).
		WillReturnRows(sqlmock.NewRows([]string{"workspace_id", "content_hash"}))

	res, err := Run(context.Background(), db, &fakeStorage{}, ttl)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.StalePendingDeleted != 3 {
		t.Errorf("StalePendingDeleted = %d, want 3", res.StalePendingDeleted)
	}
	if res.BlobsDeleted != 0 {
		t.Errorf("BlobsDeleted = %d, want 0", res.BlobsDeleted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRun_DeletesOrphanBlobs(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	ttl := time.Hour
	old := time.Now().Add(-2 * time.Hour)
	young := time.Now()

	expectPendingDelete(mock, ttl, 0)
	mock.ExpectQuery(refQuery).
		WillReturnRows(sqlmock.NewRows([]string{"workspace_id", "content_hash"}).
			AddRow(ws, hashB)) // hashB is referenced
	// Confirm-before-delete recheck for each snapshot orphan, in iteration order.
	mock.ExpectQuery(`SELECT EXISTS`).WithArgs(ws, hashA).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(`SELECT EXISTS`).WithArgs(ws, hashE).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	fake := &fakeStorage{objects: []storage.Object{
		{Key: key(hashA), Size: 10, LastModified: old},   // orphan, old -> deleted
		{Key: key(hashB), Size: 20, LastModified: old},   // referenced -> kept
		{Key: key(hashC), Size: 30, LastModified: young}, // young -> kept
		{Key: key(hashE), Size: 5, LastModified: old},    // orphan, old -> deleted
	}}

	res, err := Run(context.Background(), db, fake, ttl)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.BlobsDeleted != 2 {
		t.Errorf("BlobsDeleted = %d, want 2", res.BlobsDeleted)
	}
	if res.BytesReclaimed != 15 {
		t.Errorf("BytesReclaimed = %d, want 15", res.BytesReclaimed)
	}
	wantDeleted := []string{key(hashA), key(hashE)}
	if strings.Join(fake.deleted, ",") != strings.Join(wantDeleted, ",") {
		t.Errorf("deleted = %v, want %v", fake.deleted, wantDeleted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRun_RecheckKeepsRacedBlob(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	ttl := time.Hour
	expectPendingDelete(mock, ttl, 0)
	mock.ExpectQuery(refQuery).
		WillReturnRows(sqlmock.NewRows([]string{"workspace_id", "content_hash"})) // empty snapshot
	mock.ExpectQuery(`SELECT EXISTS`).WithArgs(ws, hashA).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true)) // re-push raced in

	fake := &fakeStorage{objects: []storage.Object{
		{Key: key(hashA), Size: 10, LastModified: time.Now().Add(-2 * time.Hour)},
	}}

	res, err := Run(context.Background(), db, fake, ttl)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.BlobsDeleted != 0 {
		t.Errorf("BlobsDeleted = %d, want 0 (recheck found a live reference)", res.BlobsDeleted)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("deleted = %v, want none", fake.deleted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRun_SkipsUnparseableKeys(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	ttl := time.Hour
	expectPendingDelete(mock, ttl, 0)
	mock.ExpectQuery(refQuery).
		WillReturnRows(sqlmock.NewRows([]string{"workspace_id", "content_hash"}))
	// No EXISTS expectations — unparseable keys never reach the recheck.

	fake := &fakeStorage{objects: []storage.Object{
		{Key: "garbage", Size: 1, LastModified: time.Now().Add(-2 * time.Hour)},
		{Key: ws + "/short", Size: 1, LastModified: time.Now().Add(-2 * time.Hour)},
		{Key: "notaworkspace/" + hashA, Size: 1, LastModified: time.Now().Add(-2 * time.Hour)},
	}}

	res, err := Run(context.Background(), db, fake, ttl)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.BlobsDeleted != 0 || len(fake.deleted) != 0 {
		t.Errorf("BlobsDeleted = %d, deleted = %v, want 0 / none", res.BlobsDeleted, fake.deleted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRun_PendingDeleteError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`DELETE FROM pending_files WHERE pushed_at < \$1`).
		WillReturnError(errors.New("db down"))

	if _, err := Run(context.Background(), db, &fakeStorage{}, time.Hour); err == nil {
		t.Fatal("Run: expected error, got nil")
	}
}

func TestRun_ListError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	ttl := time.Hour
	expectPendingDelete(mock, ttl, 2)
	mock.ExpectQuery(refQuery).
		WillReturnRows(sqlmock.NewRows([]string{"workspace_id", "content_hash"}))

	fake := &fakeStorage{listErr: errors.New("list failed")}
	res, err := Run(context.Background(), db, fake, ttl)
	if err == nil {
		t.Fatal("Run: expected error from List, got nil")
	}
	if res.StalePendingDeleted != 2 {
		t.Errorf("StalePendingDeleted = %d, want 2 (step 1 still counted)", res.StalePendingDeleted)
	}
}

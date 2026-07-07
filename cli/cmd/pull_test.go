package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/kyenel64/invosit/cli/internal/filecrypt"
	"github.com/kyenel64/invosit/cli/internal/keys"
	"github.com/kyenel64/invosit/cli/internal/syncstate"
)

func listedFile(id string, version int64, contentHash string) apiclient.ListedFileMeta {
	return apiclient.ListedFileMeta{
		FileMeta: apiclient.FileMeta{ID: id, Version: version, ContentHash: contentHash},
	}
}

func TestPlanPull(t *testing.T) {
	record := syncstate.FileRecord{FileID: "file_1", Version: 3, ContentHash: baseHash}
	movedRemote := listedFile("file_1", 5, remoteHash)
	unmovedRemote := listedFile("file_1", 3, baseHash)
	recreatedRemote := listedFile("file_2", 3, remoteHash)

	tests := []struct {
		name        string
		localHash   string
		localExists bool
		record      syncstate.FileRecord
		hasRecord   bool
		remote      apiclient.ListedFileMeta
		force       bool
		want        pullAction
	}{
		{
			name:      "force downloads over conflict",
			localHash: editedHash, localExists: true, record: record, hasRecord: true, remote: movedRemote, force: true,
			want: pullDownload,
		},
		{
			name:   "missing locally downloads",
			remote: movedRemote,
			want:   pullDownload,
		},
		{
			name:      "unchanged both sides is up to date",
			localHash: baseHash, localExists: true, record: record, hasRecord: true, remote: unmovedRemote,
			want: pullUpToDate,
		},
		{
			name:      "clean local fast-forwards",
			localHash: baseHash, localExists: true, record: record, hasRecord: true, remote: movedRemote,
			want: pullDownload,
		},
		{
			// The remote hash is ciphertext — a local plaintext hash equal to
			// it by coincidence must not read as up to date; the record decides.
			name:      "local equals remote hash by coincidence still conflicts",
			localHash: remoteHash, localExists: true, record: record, hasRecord: true, remote: movedRemote,
			want: pullConflict,
		},
		{
			name:      "local edit with unmoved remote is left alone",
			localHash: editedHash, localExists: true, record: record, hasRecord: true, remote: unmovedRemote,
			want: pullLocalOnly,
		},
		{
			name:      "both moved conflicts",
			localHash: editedHash, localExists: true, record: record, hasRecord: true, remote: movedRemote,
			want: pullConflict,
		},
		{
			name:      "recreated remote with matching version still conflicts",
			localHash: editedHash, localExists: true, record: record, hasRecord: true, remote: recreatedRemote,
			want: pullConflict,
		},
		{
			name:      "no sync state refuses to overwrite",
			localHash: editedHash, localExists: true, remote: movedRemote,
			want: pullRefuse,
		},
		{
			name:      "no sync state refuses even when local matches remote hash",
			localHash: remoteHash, localExists: true, remote: movedRemote,
			want: pullRefuse,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := planPull(test.localHash, test.localExists, test.record, test.hasRecord, test.remote, test.force)
			if got != test.want {
				t.Errorf("planPull = %v, want %v", got, test.want)
			}
		})
	}
}

// encryptedFixture produces a real encrypted blob the way push does: fresh
// DEK, AES-256-GCM ciphertext, DEK wrapped to the keypair.
func encryptedFixture(t *testing.T, plaintext []byte) ([]byte, []byte, keys.Keypair) {
	t.Helper()
	keypair, err := keys.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dek, cipherContent, err := filecrypt.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	wrappedDEK, err := keys.Wrap(dek, keypair.Public)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	return cipherContent, wrappedDEK, keypair
}

func blobServer(t *testing.T, content []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(content)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func listedBlob(url string, cipherContent, wrappedDEK []byte) apiclient.ListedFileMeta {
	sum := sha256.Sum256(cipherContent)
	return apiclient.ListedFileMeta{
		FileMeta: apiclient.FileMeta{
			ID:          "file_1",
			Version:     1,
			ContentHash: hex.EncodeToString(sum[:]),
			Size:        int64(len(cipherContent)),
		},
		DownloadURL: url,
		WrappedDEK:  wrappedDEK,
	}
}

func TestPullOneRoundTrip(t *testing.T) {
	plaintext := []byte("SECRET_TOKEN=hunter2\n")
	cipherContent, wrappedDEK, keypair := encryptedFixture(t, plaintext)
	srv := blobServer(t, cipherContent)
	dest := filepath.Join(t.TempDir(), "app.env")

	plainHash, plainSize, err := pullOne(context.Background(), dest, listedBlob(srv.URL, cipherContent, wrappedDEK), keypair.Private)
	if err != nil {
		t.Fatalf("pullOne: %v", err)
	}

	written, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(written, plaintext) {
		t.Error("pulled bytes differ from the pushed plaintext")
	}
	sum := sha256.Sum256(plaintext)
	if plainHash != hex.EncodeToString(sum[:]) {
		t.Errorf("plainHash = %q, want plaintext hash", plainHash)
	}
	if plainSize != int64(len(plaintext)) {
		t.Errorf("plainSize = %d, want %d", plainSize, len(plaintext))
	}
}

func TestPullOneEmptyFileRoundTrip(t *testing.T) {
	cipherContent, wrappedDEK, keypair := encryptedFixture(t, []byte{})
	srv := blobServer(t, cipherContent)
	dest := filepath.Join(t.TempDir(), "empty.env")

	_, plainSize, err := pullOne(context.Background(), dest, listedBlob(srv.URL, cipherContent, wrappedDEK), keypair.Private)
	if err != nil {
		t.Fatalf("pullOne: %v", err)
	}
	if plainSize != 0 {
		t.Errorf("plainSize = %d, want 0", plainSize)
	}
	written, err := os.ReadFile(dest)
	if err != nil || len(written) != 0 {
		t.Errorf("dest = %d bytes, err %v; want empty file", len(written), err)
	}
}

// assertPullFails runs pullOne against a pre-existing dest and asserts the
// failure leaves the local file untouched and no temp residue behind.
func assertPullFails(t *testing.T, dest string, file apiclient.ListedFileMeta, privateKey []byte, wantErr string) {
	t.Helper()
	existing := []byte("existing local content")
	if err := os.WriteFile(dest, existing, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, _, err := pullOne(context.Background(), dest, file, privateKey)
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("pullOne err = %v, want containing %q", err, wantErr)
	}

	after, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(after, existing) {
		t.Errorf("local file changed on failed pull (err %v)", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(dest), ".invosit-*.tmp"))
	if err != nil || len(leftovers) != 0 {
		t.Errorf("temp residue left behind: %v (err %v)", leftovers, err)
	}
}

func TestPullOneTamperedCiphertextFails(t *testing.T) {
	cipherContent, wrappedDEK, keypair := encryptedFixture(t, []byte("SECRET_TOKEN=hunter2\n"))
	tampered := bytes.Clone(cipherContent)
	tampered[len(tampered)/2] ^= 0xff
	srv := blobServer(t, tampered)

	// content_hash matches the tampered bytes, so the failure is GCM's.
	dest := filepath.Join(t.TempDir(), "app.env")
	assertPullFails(t, dest, listedBlob(srv.URL, tampered, wrappedDEK), keypair.Private, "failed to decrypt")
}

func TestPullOneHashMismatchFailsBeforeDecrypt(t *testing.T) {
	cipherContent, wrappedDEK, keypair := encryptedFixture(t, []byte("SECRET_TOKEN=hunter2\n"))
	srv := blobServer(t, append(bytes.Clone(cipherContent), 0x00))

	dest := filepath.Join(t.TempDir(), "app.env")
	assertPullFails(t, dest, listedBlob(srv.URL, cipherContent, wrappedDEK), keypair.Private, "content hash mismatch")
}

func TestPullOneWrongPrivateKeyFails(t *testing.T) {
	cipherContent, wrappedDEK, _ := encryptedFixture(t, []byte("SECRET_TOKEN=hunter2\n"))
	stranger, err := keys.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	srv := blobServer(t, cipherContent)

	dest := filepath.Join(t.TempDir(), "app.env")
	assertPullFails(t, dest, listedBlob(srv.URL, cipherContent, wrappedDEK), stranger.Private, "failed to unwrap file key")
}

func TestPullOneMissingWrappedDEKFails(t *testing.T) {
	cipherContent, _, keypair := encryptedFixture(t, []byte("SECRET_TOKEN=hunter2\n"))
	srv := blobServer(t, cipherContent)

	dest := filepath.Join(t.TempDir(), "app.env")
	assertPullFails(t, dest, listedBlob(srv.URL, cipherContent, nil), keypair.Private, "no wrapped key")
}

func TestLocalFileHash(t *testing.T) {
	dir := t.TempDir()

	path := filepath.Join(dir, "app.env")
	content := []byte("KEY=value\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sum := sha256.Sum256(content)

	hash, exists, err := localFileHash(path)
	if err != nil || !exists {
		t.Fatalf("localFileHash = %v, exists %v", err, exists)
	}
	if want := hex.EncodeToString(sum[:]); hash != want {
		t.Errorf("hash = %q, want %q", hash, want)
	}

	_, exists, err = localFileHash(filepath.Join(dir, "missing.env"))
	if err != nil || exists {
		t.Errorf("missing file: err = %v, exists = %v; want nil, false", err, exists)
	}

	if _, _, err := localFileHash(dir); err == nil {
		t.Error("directory must be an error, not a download target")
	}
}

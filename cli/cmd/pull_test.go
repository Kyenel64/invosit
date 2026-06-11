package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
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
			name:      "local matches remote is up to date",
			localHash: remoteHash, localExists: true, remote: movedRemote,
			want: pullUpToDate,
		},
		{
			name:      "clean local fast-forwards",
			localHash: baseHash, localExists: true, record: record, hasRecord: true, remote: movedRemote,
			want: pullDownload,
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

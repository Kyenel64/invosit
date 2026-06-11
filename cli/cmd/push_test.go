package cmd

import (
	"strings"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/kyenel64/invosit/cli/internal/syncstate"
)

var (
	baseHash   = strings.Repeat("a", 64)
	editedHash = strings.Repeat("b", 64)
	remoteHash = strings.Repeat("c", 64)
)

func TestPlanPush(t *testing.T) {
	record := syncstate.FileRecord{FileID: "file_1", Version: 3, ContentHash: baseHash}
	remote := apiclient.FileMeta{ID: "file_1", Version: 5, ContentHash: remoteHash}
	recreated := apiclient.FileMeta{ID: "file_2", Version: 3, ContentHash: remoteHash}

	tests := []struct {
		name      string
		localHash string
		record    syncstate.FileRecord
		hasRecord bool
		remote    apiclient.FileMeta
		hasRemote bool
		force     bool
		want      pushPlan
	}{
		{
			name:      "force sends current remote version",
			localHash: editedHash, record: record, hasRecord: true, remote: remote, hasRemote: true, force: true,
			want: pushPlan{baseVersion: 5},
		},
		{
			name:      "force without remote creates",
			localHash: editedHash, force: true,
			want: pushPlan{baseVersion: 0},
		},
		{
			name:      "force overrides up-to-date skip",
			localHash: baseHash, record: record, hasRecord: true, remote: remote, hasRemote: true, force: true,
			want: pushPlan{baseVersion: 5},
		},
		{
			name:      "local matches base skips",
			localHash: baseHash, record: record, hasRecord: true, remote: remote, hasRemote: true,
			want: pushPlan{skip: true},
		},
		{
			name:      "no record but local matches remote adopts",
			localHash: remoteHash, remote: remote, hasRemote: true,
			want: pushPlan{skip: true, adopt: true},
		},
		{
			name:      "record but remote deleted recreates",
			localHash: editedHash, record: record, hasRecord: true,
			want: pushPlan{baseVersion: 0},
		},
		{
			name:      "stale generation falls back to create",
			localHash: editedHash, record: record, hasRecord: true, remote: recreated, hasRemote: true,
			want: pushPlan{baseVersion: 0},
		},
		{
			name:      "record drives the CAS base",
			localHash: editedHash, record: record, hasRecord: true, remote: remote, hasRemote: true,
			want: pushPlan{baseVersion: 3},
		},
		{
			name:      "no record and remote differs creates",
			localHash: editedHash, remote: remote, hasRemote: true,
			want: pushPlan{baseVersion: 0},
		},
		{
			name:      "untracked file creates",
			localHash: editedHash,
			want:      pushPlan{baseVersion: 0},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := planPush(test.localHash, test.record, test.hasRecord, test.remote, test.hasRemote, test.force)
			if got != test.want {
				t.Errorf("planPush = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestDedupePrepared(t *testing.T) {
	first := preparedFile{projectRelPath: "app.env", hash: baseHash}
	second := preparedFile{projectRelPath: "other.env", hash: remoteHash}
	duplicate := preparedFile{projectRelPath: "app.env", hash: editedHash}

	deduped := dedupePrepared([]preparedFile{first, second, duplicate})

	if len(deduped) != 2 {
		t.Fatalf("len = %d, want 2", len(deduped))
	}
	if deduped[0].projectRelPath != "other.env" || deduped[1].projectRelPath != "app.env" || deduped[1].hash != editedHash {
		t.Errorf("deduped = %+v, want last occurrence kept in order", deduped)
	}
}

package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/kyenel64/invosit/cli/internal/filecrypt"
	"github.com/kyenel64/invosit/cli/internal/keys"
	"github.com/kyenel64/invosit/cli/internal/syncstate"
	"github.com/spf13/cobra"
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
			// The remote hash is ciphertext, so a matching local plaintext
			// hash means nothing without a record — this must create (base 0)
			// and surface the server CONFLICT, never silently adopt.
			name:      "no record and local matches remote hash still creates",
			localHash: remoteHash, remote: remote, hasRemote: true,
			want: pushPlan{baseVersion: 0},
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

func TestEncryptPlanned(t *testing.T) {
	keypair, err := keys.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	content := []byte("SECRET_TOKEN=hunter2\n")
	sum := sha256.Sum256(content)
	planned := []plannedFile{{
		preparedFile: preparedFile{
			projectRelPath: "app.env",
			content:        content,
			hash:           hex.EncodeToString(sum[:]),
			size:           int64(len(content)),
		},
		baseVersion: 0,
	}}

	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})

	encrypted, failed := encryptPlanned(cmd, planned, keypair.Public)
	if failed != 0 || len(encrypted) != 1 {
		t.Fatalf("failed = %d, len = %d, want 0/1", failed, len(encrypted))
	}
	file := encrypted[0]

	if bytes.Contains(file.cipherContent, content) {
		t.Error("ciphertext contains the plaintext")
	}
	if file.cipherHash == file.hash {
		t.Error("ciphertext hash equals plaintext hash")
	}
	if file.cipherSize != int64(len(file.cipherContent)) {
		t.Errorf("cipherSize = %d, want %d", file.cipherSize, len(file.cipherContent))
	}

	dek, err := keys.Unwrap(file.wrappedDEK, keypair.Private)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	plaintext, err := filecrypt.Decrypt(dek, file.cipherContent)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(plaintext, content) {
		t.Error("round-trip mismatch")
	}

	// A fresh DEK and nonce per encryption: pushing identical plaintext twice
	// must never produce the same ciphertext or blob key.
	again, failedAgain := encryptPlanned(cmd, planned, keypair.Public)
	if failedAgain != 0 || len(again) != 1 {
		t.Fatalf("second encrypt failed = %d, len = %d", failedAgain, len(again))
	}
	if again[0].cipherHash == file.cipherHash {
		t.Error("re-encrypting identical content produced an identical ciphertext hash")
	}
}

func TestEncryptPlannedBadPublicKey(t *testing.T) {
	planned := []plannedFile{{
		preparedFile: preparedFile{projectRelPath: "app.env", content: []byte("x"), size: 1},
	}}

	var stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&stderr)

	encrypted, failed := encryptPlanned(cmd, planned, []byte("short"))
	if failed != 1 || len(encrypted) != 0 {
		t.Fatalf("failed = %d, len = %d, want 1/0", failed, len(encrypted))
	}
	if !strings.Contains(stderr.String(), "failed to push app.env") {
		t.Errorf("stderr = %q, want per-file failure line", stderr.String())
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

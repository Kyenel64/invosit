package cmd

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/kyenel64/invosit/cli/internal/syncstate"
)

// TestClassifyStatus asserts classifyStatus and planPull together so the two
// three-way compares cannot drift apart.
func TestClassifyStatus(t *testing.T) {
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
		wantStatus  fileStatus
		wantPull    pullAction
	}{
		{
			name:       "missing locally",
			remote:     movedRemote,
			wantStatus: statusMissing, wantPull: pullDownload,
		},
		{
			name:      "local matches remote is up to date",
			localHash: remoteHash, localExists: true, remote: movedRemote,
			wantStatus: statusOK, wantPull: pullUpToDate,
		},
		{
			name:      "local matches remote despite stale record",
			localHash: remoteHash, localExists: true, record: record, hasRecord: true, remote: movedRemote,
			wantStatus: statusOK, wantPull: pullUpToDate,
		},
		{
			name:      "clean local with moved remote is behind",
			localHash: baseHash, localExists: true, record: record, hasRecord: true, remote: movedRemote,
			wantStatus: statusBehind, wantPull: pullDownload,
		},
		{
			name:      "clean local with recreated remote is behind",
			localHash: baseHash, localExists: true, record: record, hasRecord: true, remote: recreatedRemote,
			wantStatus: statusBehind, wantPull: pullDownload,
		},
		{
			name:      "local edit with unmoved remote is modified",
			localHash: editedHash, localExists: true, record: record, hasRecord: true, remote: unmovedRemote,
			wantStatus: statusModified, wantPull: pullLocalOnly,
		},
		{
			name:      "both moved conflicts",
			localHash: editedHash, localExists: true, record: record, hasRecord: true, remote: movedRemote,
			wantStatus: statusConflict, wantPull: pullConflict,
		},
		{
			name:      "recreated remote with local edit conflicts",
			localHash: editedHash, localExists: true, record: record, hasRecord: true, remote: recreatedRemote,
			wantStatus: statusConflict, wantPull: pullConflict,
		},
		{
			name:      "no sync state diverges",
			localHash: editedHash, localExists: true, remote: movedRemote,
			wantStatus: statusDiverged, wantPull: pullRefuse,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotStatus := classifyStatus(test.localHash, test.localExists, test.record, test.hasRecord, test.remote.FileMeta)
			if gotStatus != test.wantStatus {
				t.Errorf("classifyStatus = %v, want %v", gotStatus, test.wantStatus)
			}
			gotPull := planPull(test.localHash, test.localExists, test.record, test.hasRecord, test.remote, false)
			if gotPull != test.wantPull {
				t.Errorf("planPull = %v, want %v", gotPull, test.wantPull)
			}
		})
	}
}

func statusTestEntries() []statusEntry {
	return []statusEntry{
		{path: "database.env", status: statusOK},
		{path: "app.env", status: statusModified},
		{path: "ca.pem", status: statusBehind},
		{path: "zz-old.env", status: statusDiverged},
		{path: "prod-keys.env", status: statusConflict},
		{path: "legacy.env", status: statusMissing},
		{path: "shared-cert.pem", status: statusBehind},
	}
}

func renderStatusWith(profile termenv.Profile, workspaceName string, entries []statusEntry) string {
	var buf bytes.Buffer
	renderer := lipgloss.NewRenderer(&buf)
	renderer.SetColorProfile(profile)
	renderStatus(&buf, newStatusStyles(renderer), workspaceName, "ws_1", "development", entries)
	return buf.String()
}

func TestRenderStatus(t *testing.T) {
	got := renderStatusWith(termenv.Ascii, "acme-api", statusTestEntries())

	want := `Workspace: acme-api
Environment: development

Files:
  (changed locally and remotely — resolve or pull --force)
  conflict  prod-keys.env

  (differs from remote, no sync history — pull --force or push --force)
  conflict  zz-old.env

  (remote is newer — pull)
  behind    ca.pem
  behind    shared-cert.pem

  (local edits — push)
  modified  app.env

  (not on disk — pull)
  missing   legacy.env

  ok        database.env
`
	if got != want {
		t.Errorf("renderStatus output:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderStatusEmpty(t *testing.T) {
	got := renderStatusWith(termenv.Ascii, "", nil)

	want := `Workspace: ws_1
Environment: development

no files tracked in development
`
	if got != want {
		t.Errorf("renderStatus output:\n%q\nwant:\n%q", got, want)
	}
}

var ansiEscapes = regexp.MustCompile("\x1b\\[[0-9;]*m")

// TestRenderStatusColor proves text is padded before styling: stripping the
// escape codes from the color output must reproduce the plain output exactly.
func TestRenderStatusColor(t *testing.T) {
	colored := renderStatusWith(termenv.ANSI, "acme-api", statusTestEntries())
	if !strings.Contains(colored, "\x1b[") {
		t.Fatal("expected ANSI escape codes in color output")
	}

	plain := renderStatusWith(termenv.Ascii, "acme-api", statusTestEntries())
	if stripped := ansiEscapes.ReplaceAllString(colored, ""); stripped != plain {
		t.Errorf("stripped color output:\n%q\nwant plain output:\n%q", stripped, plain)
	}
}

package manifest_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/manifest"
)

func validManifest() *manifest.Manifest {
	return &manifest.Manifest{
		Version:     manifest.Version,
		WorkspaceID: "ws_abc123",
		Environment: "dev",
	}
}

func tmpPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), manifest.DefaultName)
}

func TestValidateAccepts(t *testing.T) {
	t.Run("minimal", func(t *testing.T) {
		if err := validManifest().Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("with files", func(t *testing.T) {
		m := validManifest()
		m.Files = []manifest.File{
			{Path: ".env", FileID: "file_one", ContentHash: "abc"},
			{Path: "secrets/db.key", FileID: "file_two", ContentHash: "def"},
		}
		if err := m.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*manifest.Manifest)
		wantSub string
	}{
		{
			name:    "wrong version",
			mutate:  func(m *manifest.Manifest) { m.Version = 999 },
			wantSub: "version",
		},
		{
			name:    "empty workspace id",
			mutate:  func(m *manifest.Manifest) { m.WorkspaceID = "" },
			wantSub: "workspace_id",
		},
		{
			name:    "workspace id wrong prefix",
			mutate:  func(m *manifest.Manifest) { m.WorkspaceID = "usr_abc123" },
			wantSub: "workspace_id",
		},
		{
			name:    "empty environment",
			mutate:  func(m *manifest.Manifest) { m.Environment = "" },
			wantSub: "environment",
		},
		{
			name: "file with empty path",
			mutate: func(m *manifest.Manifest) {
				m.Files = []manifest.File{{Path: "", FileID: "file_x"}}
			},
			wantSub: "path",
		},
		{
			name: "file with wrong file_id prefix",
			mutate: func(m *manifest.Manifest) {
				m.Files = []manifest.File{{Path: ".env", FileID: "ws_x"}}
			},
			wantSub: "file_id",
		},
		{
			name: "duplicate file paths",
			mutate: func(m *manifest.Manifest) {
				m.Files = []manifest.File{
					{Path: ".env", FileID: "file_one"},
					{Path: ".env", FileID: "file_two"},
				}
			},
			wantSub: "duplicate",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			tc.mutate(m)
			err := m.Validate()
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	path := tmpPath(t)
	in := validManifest()
	in.Files = []manifest.File{
		{Path: ".env", FileID: "file_one", ContentHash: "abc"},
		{Path: "secrets/db.key", FileID: "file_two", ContentHash: "def"},
	}

	if err := manifest.Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch\nin:  %+v\nout: %+v", in, out)
	}
}

func TestSaveRejectsNil(t *testing.T) {
	err := manifest.Save(tmpPath(t), nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	m := validManifest()
	m.WorkspaceID = "bad_prefix"
	err := manifest.Save(tmpPath(t), m)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestSaveLeavesNoTmpFile(t *testing.T) {
	path := tmpPath(t)
	if err := manifest.Save(path, validManifest()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".tmp file should not exist after successful save (stat err = %v)", err)
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := manifest.Load(tmpPath(t))
	if !errors.Is(err, manifest.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	path := tmpPath(t)
	body := "version: 1\nworkspace_id: ws_abc\nenvironment: dev\nbogus_field: hi\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := manifest.Load(path); err == nil {
		t.Fatal("want error for unknown field, got nil")
	}
}

func TestLoadRejectsMalformedYAML(t *testing.T) {
	path := tmpPath(t)
	if err := os.WriteFile(path, []byte("::: not valid yaml :::"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := manifest.Load(path); err == nil {
		t.Fatal("want error for malformed YAML, got nil")
	}
}

func TestLoadValidates(t *testing.T) {
	path := tmpPath(t)
	body := "version: 999\nworkspace_id: ws_abc\nenvironment: dev\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := manifest.Load(path)
	if err == nil {
		t.Fatal("want validation error, got nil")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error %q should mention version", err.Error())
	}
}

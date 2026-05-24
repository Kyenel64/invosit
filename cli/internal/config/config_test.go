package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/config"
)

func validConfig() *config.Config {
	return &config.Config{
		Version:            config.Version,
		WorkspaceID:        "ws_abc123",
		DefaultEnvironment: "dev",
	}
}

func tmpPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), config.FileName)
}

func TestValidateAccepts(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*config.Config)
		wantSub string
	}{
		{
			name:    "wrong version",
			mutate:  func(c *config.Config) { c.Version = 999 },
			wantSub: "version",
		},
		{
			name:    "empty workspace id",
			mutate:  func(c *config.Config) { c.WorkspaceID = "" },
			wantSub: "workspaceId",
		},
		{
			name:    "workspace id wrong prefix",
			mutate:  func(c *config.Config) { c.WorkspaceID = "usr_abc123" },
			wantSub: "workspaceId",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(cfg)
			err := cfg.Validate()
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
	in := validConfig()

	if err := config.Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch\nin:  %+v\nout: %+v", in, out)
	}
}

func TestSaveRejectsNil(t *testing.T) {
	err := config.Save(tmpPath(t), nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	cfg := validConfig()
	cfg.WorkspaceID = "bad_prefix"
	err := config.Save(tmpPath(t), cfg)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestSaveLeavesNoTmpFile(t *testing.T) {
	path := tmpPath(t)
	if err := config.Save(path, validConfig()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".tmp file should not exist after successful save (stat err = %v)", err)
	}
}

func TestSaveOmitsEmptyDefaultEnvironment(t *testing.T) {
	path := tmpPath(t)
	cfg := validConfig()
	cfg.DefaultEnvironment = ""

	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	body, err := os.ReadFile(path) //nolint:gosec // test reads a file in t.TempDir()
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(body), "defaultEnvironment") {
		t.Errorf("saved file should omit defaultEnvironment when empty; got:\n%s", body)
	}
}

func TestLoadAcceptsMissingDefaultEnvironment(t *testing.T) {
	path := tmpPath(t)
	body := `{"version":1,"workspaceId":"ws_abc"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.DefaultEnvironment != "" {
		t.Errorf("DefaultEnvironment = %q, want empty", loaded.DefaultEnvironment)
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := config.Load(tmpPath(t))
	if !errors.Is(err, config.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	path := tmpPath(t)
	body := `{"version":1,"workspaceId":"ws_abc","defaultEnvironment":"dev","bogus":"hi"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := config.Load(path); err == nil {
		t.Fatal("want error for unknown field, got nil")
	}
}

func TestLoadRejectsMalformedJSON(t *testing.T) {
	path := tmpPath(t)
	if err := os.WriteFile(path, []byte("::: not valid json :::"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := config.Load(path); err == nil {
		t.Fatal("want error for malformed JSON, got nil")
	}
}

func TestLoadValidates(t *testing.T) {
	path := tmpPath(t)
	body := `{"version":999,"workspaceId":"ws_abc","defaultEnvironment":"dev"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("want validation error, got nil")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error %q should mention version", err.Error())
	}
}

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

func TestFindInCwd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(path, []byte(`{"version":1,"workspaceId":"ws_x"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	found, err := config.Find(dir)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found != path {
		t.Errorf("Find = %q, want %q", found, path)
	}
}

func TestFindWalksUp(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfgPath := filepath.Join(root, config.FileName)
	if err := os.WriteFile(cfgPath, []byte(`{"version":1,"workspaceId":"ws_x"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	found, err := config.Find(nested)
	if err != nil {
		t.Fatalf("Find from %q: %v", nested, err)
	}
	if found != cfgPath {
		t.Errorf("Find from %q = %q, want %q", nested, found, cfgPath)
	}
}

func TestFindReturnsClosest(t *testing.T) {
	root := t.TempDir()
	mid := filepath.Join(root, "mid")
	leaf := filepath.Join(mid, "leaf")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	rootCfg := filepath.Join(root, config.FileName)
	midCfg := filepath.Join(mid, config.FileName)
	if err := os.WriteFile(rootCfg, []byte(`{"version":1,"workspaceId":"ws_x"}`), 0o644); err != nil {
		t.Fatalf("WriteFile root: %v", err)
	}
	if err := os.WriteFile(midCfg, []byte(`{"version":1,"workspaceId":"ws_y"}`), 0o644); err != nil {
		t.Fatalf("WriteFile mid: %v", err)
	}

	found, err := config.Find(leaf)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found != midCfg {
		t.Errorf("Find returned %q, want closest %q", found, midCfg)
	}
}

func TestFindSkipsDirectoryWithMatchingName(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "leaf")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatalf("MkdirAll leaf: %v", err)
	}
	if err := os.Mkdir(filepath.Join(leaf, config.FileName), 0o755); err != nil {
		t.Fatalf("Mkdir directory-named-like-config: %v", err)
	}
	rootCfg := filepath.Join(root, config.FileName)
	if err := os.WriteFile(rootCfg, []byte(`{"version":1,"workspaceId":"ws_x"}`), 0o644); err != nil {
		t.Fatalf("WriteFile root: %v", err)
	}

	found, err := config.Find(leaf)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found != rootCfg {
		t.Errorf("Find returned %q, want %q (should skip directory at leaf)", found, rootCfg)
	}
}

func TestFindReturnsErrNotFound(t *testing.T) {
	dir := t.TempDir()

	found, err := config.Find(dir)
	if err == nil {
		if !strings.HasPrefix(found, dir) {
			t.Skipf("ambient .invosit.json found at %q above test dir %q; skipping", found, dir)
		}
		t.Fatalf("Find returned unexpected path inside tmpdir %q: %q", dir, found)
	}
	if !errors.Is(err, config.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
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

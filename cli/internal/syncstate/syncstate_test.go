package syncstate_test

import (
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/syncstate"
)

func sampleState() *syncstate.State {
	state := syncstate.New("ws_abc123")
	state.Put("dev", "env_xyz", "config/secret.env", syncstate.FileRecord{
		FileID:      "file_abc",
		Version:     5,
		ContentHash: strings.Repeat("a", 64),
	})
	return state
}

func TestLoadMissingReturnsEmptyState(t *testing.T) {
	state, err := syncstate.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if state.WorkspaceID != "" || len(state.Environments) != 0 {
		t.Errorf("expected empty state, got %+v", state)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	projectRoot := t.TempDir()
	in := sampleState()

	if err := syncstate.Save(projectRoot, in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := syncstate.Load(projectRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Version != in.Version || out.WorkspaceID != in.WorkspaceID ||
		!reflect.DeepEqual(out.Environments, in.Environments) {
		t.Errorf("roundtrip mismatch:\n in: %+v\nout: %+v", in, out)
	}
}

func TestSaveLeavesNoTmpFile(t *testing.T) {
	projectRoot := t.TempDir()
	if err := syncstate.Save(projectRoot, sampleState()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(syncstate.Dir(projectRoot))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", entry.Name())
		}
	}
}

func TestSaveFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits not meaningful on windows")
	}
	projectRoot := t.TempDir()
	if err := syncstate.Save(projectRoot, sampleState()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	dirInfo, err := os.Stat(syncstate.Dir(projectRoot))
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode = %#o, want 0700", perm)
	}

	fileInfo, err := os.Stat(syncstate.Path(projectRoot))
	if err != nil {
		t.Fatalf("Stat file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %#o, want 0600", perm)
	}
}

func TestLoadCorruptRecovers(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.MkdirAll(syncstate.Dir(projectRoot), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(syncstate.Path(projectRoot), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	state, err := syncstate.Load(projectRoot)
	if !errors.Is(err, syncstate.ErrRecovered) {
		t.Errorf("err = %v, want ErrRecovered", err)
	}
	if state == nil || len(state.Environments) != 0 {
		t.Errorf("expected usable empty state, got %+v", state)
	}
}

func TestLoadWrongVersionRecovers(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.MkdirAll(syncstate.Dir(projectRoot), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	raw := `{"version": 99, "workspaceId": "ws_abc123", "environments": {}}`
	if err := os.WriteFile(syncstate.Path(projectRoot), []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	state, err := syncstate.Load(projectRoot)
	if !errors.Is(err, syncstate.ErrRecovered) {
		t.Errorf("err = %v, want ErrRecovered", err)
	}
	if state == nil || len(state.Environments) != 0 {
		t.Errorf("expected usable empty state, got %+v", state)
	}
}

func TestLoadToleratesUnknownFields(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.MkdirAll(syncstate.Dir(projectRoot), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	raw := `{"version": 1, "workspaceId": "ws_abc123", "environments": {}, "futureField": true}`
	if err := os.WriteFile(syncstate.Path(projectRoot), []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	state, err := syncstate.Load(projectRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if state.WorkspaceID != "ws_abc123" {
		t.Errorf("WorkspaceID = %q", state.WorkspaceID)
	}
}

func TestSaveInvalidState(t *testing.T) {
	if err := syncstate.Save(t.TempDir(), syncstate.New("")); err == nil {
		t.Error("expected error for missing workspaceId")
	}
	if err := syncstate.Save(t.TempDir(), nil); err == nil {
		t.Error("expected error for nil state")
	}
}

func TestBoundTo(t *testing.T) {
	state := syncstate.New("ws_abc123")
	if !state.BoundTo("ws_abc123") {
		t.Error("expected BoundTo to match")
	}
	if state.BoundTo("ws_other") {
		t.Error("expected BoundTo to reject other workspace")
	}
}

func TestPutAndRecord(t *testing.T) {
	state := syncstate.New("ws_abc123")

	if _, ok := state.Record("dev", "app.env"); ok {
		t.Error("expected no record before Put")
	}

	record := syncstate.FileRecord{FileID: "file_1", Version: 2, ContentHash: strings.Repeat("b", 64)}
	state.Put("dev", "env_1", "app.env", record)

	got, ok := state.Record("dev", "app.env")
	if !ok || got != record {
		t.Errorf("Record = %+v, %v; want %+v, true", got, ok, record)
	}
	if state.Environments["dev"].EnvironmentID != "env_1" {
		t.Errorf("EnvironmentID = %q, want env_1", state.Environments["dev"].EnvironmentID)
	}

	state.Put("dev", "", "app.env", record)
	if state.Environments["dev"].EnvironmentID != "env_1" {
		t.Error("empty environmentID should not clear the observed one")
	}

	if _, ok := state.Record("prod", "app.env"); ok {
		t.Error("record must be scoped to its environment")
	}
}

func TestPrune(t *testing.T) {
	state := syncstate.New("ws_abc123")
	record := syncstate.FileRecord{FileID: "file_1", Version: 1, ContentHash: strings.Repeat("c", 64)}
	state.Put("dev", "env_1", "keep.env", record)
	state.Put("dev", "env_1", "drop.env", record)
	state.Put("prod", "env_2", "drop.env", record)

	state.Prune("dev", map[string]struct{}{"keep.env": {}})

	if _, ok := state.Record("dev", "keep.env"); !ok {
		t.Error("keep.env should survive prune")
	}
	if _, ok := state.Record("dev", "drop.env"); ok {
		t.Error("drop.env should be pruned")
	}
	if _, ok := state.Record("prod", "drop.env"); !ok {
		t.Error("prune must not touch other environments")
	}
}

func TestDirty(t *testing.T) {
	state := syncstate.New("ws_abc123")
	if state.Dirty() {
		t.Error("fresh state must not be dirty")
	}

	record := syncstate.FileRecord{FileID: "file_1", Version: 1, ContentHash: strings.Repeat("e", 64)}
	state.Put("dev", "env_1", "app.env", record)
	if !state.Dirty() {
		t.Error("Put must mark state dirty")
	}

	projectRoot := t.TempDir()
	if err := syncstate.Save(projectRoot, state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := syncstate.Load(projectRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Dirty() {
		t.Error("loaded state must not be dirty")
	}

	loaded.Put("dev", "env_1", "app.env", record)
	if loaded.Dirty() {
		t.Error("Put with an identical record must not mark state dirty")
	}

	loaded.Prune("dev", map[string]struct{}{"app.env": {}})
	if loaded.Dirty() {
		t.Error("Prune that drops nothing must not mark state dirty")
	}

	loaded.Prune("dev", map[string]struct{}{})
	if !loaded.Dirty() {
		t.Error("Prune that drops a record must mark state dirty")
	}
}

// Guard: the state file must hold only paths, hashes, versions, and ids —
// never file content.
func TestStateFileContainsOnlyMetadata(t *testing.T) {
	projectRoot := t.TempDir()
	plaintext := "SECRET_API_KEY=hunter2"
	state := syncstate.New("ws_abc123")
	state.Put("dev", "env_xyz", "app.env", syncstate.FileRecord{
		FileID:      "file_abc",
		Version:     3,
		ContentHash: strings.Repeat("d", 64),
	})

	if err := syncstate.Save(projectRoot, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(syncstate.Path(projectRoot))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(raw), plaintext) {
		t.Error("state file must never contain file content")
	}
	for _, want := range []string{"app.env", "file_abc", strings.Repeat("d", 64), "ws_abc123"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("state file missing expected metadata %q", want)
		}
	}
}

package keystore_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/keys"
	"github.com/kyenel64/invosit/cli/internal/keystore"
)

func newStore(t *testing.T) *keystore.FileStore {
	t.Helper()
	store, err := keystore.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return store
}

func testKey(t *testing.T) []byte {
	t.Helper()
	keypair, err := keys.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return keypair.Private
}

func TestSaveLoadRoundTrip(t *testing.T) {
	store := newStore(t)
	privateKey := testKey(t)

	if err := store.Save("usr_abc", privateKey); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load("usr_abc")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Equal(loaded, privateKey) {
		t.Error("loaded key differs from saved key")
	}
}

func TestLoadMissingReturnsErrNotFound(t *testing.T) {
	store := newStore(t)

	_, err := store.Load("usr_missing")
	if !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestSaveSetsRestrictivePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions only")
	}
	store := newStore(t)

	if err := store.Save("usr_abc", testKey(t)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(store.Path("usr_abc"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %#o, want 0600", perm)
	}
}

func TestLoadRefusesInsecurePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions only")
	}
	store := newStore(t)

	if err := store.Save("usr_abc", testKey(t)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Chmod(store.Path("usr_abc"), 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	_, err := store.Load("usr_abc")
	if !errors.Is(err, keystore.ErrInsecurePerms) {
		t.Errorf("want ErrInsecurePerms, got %v", err)
	}
}

func TestSaveRejectsWrongLengthKey(t *testing.T) {
	store := newStore(t)

	for _, size := range []int{0, 16, 31, 33} {
		if err := store.Save("usr_abc", make([]byte, size)); !errors.Is(err, keys.ErrInvalidPrivateKey) {
			t.Errorf("key size %d: want ErrInvalidPrivateKey, got %v", size, err)
		}
	}
}

func TestLoadRejectsWrongLengthKey(t *testing.T) {
	dir := t.TempDir()
	store, err := keystore.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	doctored := `{"version":1,"user_id":"usr_abc","private_key":"c2hvcnQ=","created_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "usr_abc.key"), []byte(doctored), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := store.Load("usr_abc"); !errors.Is(err, keys.ErrInvalidPrivateKey) {
		t.Errorf("want ErrInvalidPrivateKey, got %v", err)
	}
}

func TestRejectsUnsafeUserIDs(t *testing.T) {
	store := newStore(t)
	privateKey := testKey(t)

	for _, userID := range []string{"", "../escape", "a/b", `a\b`, "usr_abc.key"} {
		if err := store.Save(userID, privateKey); !errors.Is(err, keystore.ErrInvalidUserID) {
			t.Errorf("Save(%q): want ErrInvalidUserID, got %v", userID, err)
		}
		if _, err := store.Load(userID); !errors.Is(err, keystore.ErrInvalidUserID) {
			t.Errorf("Load(%q): want ErrInvalidUserID, got %v", userID, err)
		}
	}
}

func TestSaveOverwritesAtomically(t *testing.T) {
	store := newStore(t)
	first := testKey(t)
	second := testKey(t)

	if err := store.Save("usr_abc", first); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	if err := store.Save("usr_abc", second); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	loaded, err := store.Load("usr_abc")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Equal(loaded, second) {
		t.Error("overwrite did not persist the second key")
	}
	if _, err := os.Stat(store.Path("usr_abc") + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temp file left behind: %v", err)
	}
}

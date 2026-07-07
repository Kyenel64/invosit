// Package keystore persists the user's long-term x25519 private key to disk.
package keystore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kyenel64/invosit/cli/internal/keys"
)

const SchemaVersion = 1

var (
	ErrNotFound      = errors.New("key not found")
	ErrInsecurePerms = errors.New("key file has insecure permissions")
	ErrInvalidUserID = errors.New("invalid user id")
)

type keyFile struct {
	Version    int       `json:"version"`
	UserID     string    `json:"user_id"`
	PrivateKey []byte    `json:"private_key"`
	CreatedAt  time.Time `json:"created_at"`
}

type FileStore struct {
	dir string
}

// NewFileStore returns a filestore rooted at the system user config directory
// or dirOverride.
func NewFileStore(dirOverride string) (*FileStore, error) {
	if dirOverride != "" {
		return &FileStore{dir: dirOverride}, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve user config directory: %w", err)
	}
	return &FileStore{dir: filepath.Join(configDir, "invosit", "keys")}, nil
}

func (s *FileStore) Path(userID string) string {
	return filepath.Join(s.dir, userID+".key")
}

// Load returns the private key stored for userID.
func (s *FileStore) Load(userID string) ([]byte, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	path := s.Path(userID)
	keyHandle, err := os.Open(path) //nolint:gosec // path is rooted at the config dir with a validated id
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer func() { _ = keyHandle.Close() }()

	// POSIX perm check. Windows ACLs don't map to mode bits.
	if runtime.GOOS != "windows" {
		info, err := keyHandle.Stat()
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve stat %s: %w", path, err)
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			return nil, fmt.Errorf("%w: %s has mode %#o", ErrInsecurePerms, path, perm)
		}
	}

	var stored keyFile
	if err := json.NewDecoder(keyHandle).Decode(&stored); err != nil {
		return nil, fmt.Errorf("failed to decode key file at %s: %w", path, err)
	}
	if len(stored.PrivateKey) != keys.PrivateKeySize {
		return nil, fmt.Errorf("failed to load key file at %s: %w", path, keys.ErrInvalidPrivateKey)
	}
	return stored.PrivateKey, nil
}

// Save stores privateKey for userID, overwriting any existing key file.
func (s *FileStore) Save(userID string, privateKey []byte) error {
	if err := validateUserID(userID); err != nil {
		return err
	}
	if len(privateKey) != keys.PrivateKeySize {
		return keys.ErrInvalidPrivateKey
	}

	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("failed to create dir %s: %w", s.dir, err)
	}

	data, err := json.MarshalIndent(keyFile{ //nolint:gosec // G117: persisting the private key at 0600 is this package's purpose
		Version:    SchemaVersion,
		UserID:     userID,
		PrivateKey: privateKey,
		CreatedAt:  time.Now().UTC(),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode key file: %w", err)
	}

	// Write-temp + rename. Makes the swap atomic, so a crash mid-write can't
	// leave a half-truncated key file.
	path := s.Path(userID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", tmp, err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to chmod %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to rename %s: %w", tmp, err)
	}
	return nil
}

// validateUserID rejects ids that could escape the keys directory when used
// as a filename.
func validateUserID(userID string) error {
	if userID == "" {
		return ErrInvalidUserID
	}
	for _, r := range userID {
		alnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !alnum && r != '_' && r != '-' {
			return ErrInvalidUserID
		}
	}
	return nil
}

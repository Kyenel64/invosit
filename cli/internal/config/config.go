package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	Version  = 1
	FileName = ".invosit.json"

	workspaceIDPrefix = "ws_"
)

type Config struct {
	Version            int    `json:"version"`
	WorkspaceID        string `json:"workspaceId"`
	DefaultEnvironment string `json:"defaultEnvironment,omitempty"`
}

var ErrNotFound = errors.New("invosit config not found")

func Find(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("absolute path %s: %w", startDir, err)
	}
	for {
		candidate := filepath.Join(dir, FileName)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat %s: %w", candidate, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%w from %s", ErrNotFound, startDir)
		}
		dir = parent
	}
}

func Load(path string) (*Config, error) {
	f, err := os.Open(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s", ErrNotFound, path)
	}
	if err != nil {
		return nil, fmt.Errorf("open config %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()

	var loaded Config
	if err := decoder.Decode(&loaded); err != nil {
		return nil, fmt.Errorf("decode config %s: %w", path, err)
	}

	if err := loaded.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return &loaded, nil
}

func Save(path string, cfg *Config) error {
	if cfg == nil {
		return errors.New("save: config is nil")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("save: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil { //nolint:gosec
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

func (c *Config) Validate() error {
	if c.Version != Version {
		return fmt.Errorf("version must be %d, got %d", Version, c.Version)
	}
	if !strings.HasPrefix(c.WorkspaceID, workspaceIDPrefix) {
		return fmt.Errorf("workspaceId must start with %q", workspaceIDPrefix)
	}
	return nil
}

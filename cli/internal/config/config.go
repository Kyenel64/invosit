package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	Version  = 2
	FileName = ".invosit.json"

	workspaceIDPrefix   = "ws_"
	environmentIDPrefix = "env_"
)

type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Environment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Project struct {
	Workspace          Workspace    `json:"workspace"`
	DefaultEnvironment *Environment `json:"defaultEnvironment,omitempty"`
}

type Config struct {
	Version int     `json:"version"`
	Project Project `json:"project"`
}

var (
	ErrNotFound           = errors.New("invosit config not found")
	ErrUnsupportedVersion = errors.New("invosit config version is unsupported")
)

// Find walks up parent directories to return the absolute path
// of the nearest .invosit.json config file.
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

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var peek struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return nil, fmt.Errorf("decode config %s: %w", path, err)
	}
	if peek.Version != Version {
		return nil, fmt.Errorf("%w: %s is version %d, want %d", ErrUnsupportedVersion, path, peek.Version, Version)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
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
	if !strings.HasPrefix(c.Project.Workspace.ID, workspaceIDPrefix) {
		return fmt.Errorf("workspace id must start with %q", workspaceIDPrefix)
	}
	if c.Project.Workspace.Name == "" {
		return errors.New("workspace name must not be empty")
	}
	if env := c.Project.DefaultEnvironment; env != nil {
		if !strings.HasPrefix(env.ID, environmentIDPrefix) {
			return fmt.Errorf("environment id must start with %q", environmentIDPrefix)
		}
		if env.Name == "" {
			return errors.New("environment name must not be empty")
		}
	}
	return nil
}
